package aws

import (
	"context"

	"github.com/aws/aws-sdk-go-v2/service/pinpoint"
	pptypes "github.com/aws/aws-sdk-go-v2/service/pinpoint/types"
	"github.com/icearp/disco-cli/internal/redact"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerType(restype.Descriptor{Type: TypePinpointApp, Service: "pinpoint"})
	// A hook's WebUrl, or a custom delivery URI that is a URL, can carry a
	// token in its query (best effort, as for the Amplify and Chatbot webhook
	// URLs). DeliveryUri may instead name a Lambda function, whose ARN is kept.
	registerType(restype.Descriptor{Type: TypePinpointApplicationSettings, Service: "pinpoint", Redact: []redact.Rule{
		{Path: "CampaignHook.WebUrl", Mode: redact.RedactScalar},
	}})
	registerType(restype.Descriptor{Type: TypePinpointEventStream, Service: "pinpoint", Untaggable: true})
	registerType(restype.Descriptor{Type: TypePinpointCampaign, Service: "pinpoint", Redact: []redact.Rule{
		{Path: "Hook.WebUrl", Mode: redact.RedactScalar},
		{Path: "CustomDeliveryConfiguration.DeliveryUri", Mode: redact.RedactUnlessSafe},
		{Path: "AdditionalTreatments[*].CustomDeliveryConfiguration.DeliveryUri", Mode: redact.RedactUnlessSafe},
	}})
	registerType(restype.Descriptor{Type: TypePinpointSegment, Service: "pinpoint"})
	registerType(restype.Descriptor{Type: TypePinpointADMChannel, Service: "pinpoint"})
	registerType(restype.Descriptor{Type: TypePinpointAPNSChannel, Service: "pinpoint"})
	registerType(restype.Descriptor{Type: TypePinpointAPNSSandboxChannel, Service: "pinpoint"})
	registerType(restype.Descriptor{Type: TypePinpointAPNSVoipChannel, Service: "pinpoint"})
	registerType(restype.Descriptor{Type: TypePinpointAPNSVoipSandboxChannel, Service: "pinpoint"})
	// Credential is the Baidu Cloud Push API key / the Google Web API key.
	registerType(restype.Descriptor{Type: TypePinpointBaiduChannel, Service: "pinpoint", Redact: []redact.Rule{{Path: "Credential", Mode: redact.RedactScalar}}})
	registerType(restype.Descriptor{Type: TypePinpointEmailChannel, Service: "pinpoint"})
	registerType(restype.Descriptor{Type: TypePinpointGCMChannel, Service: "pinpoint", Redact: []redact.Rule{{Path: "Credential", Mode: redact.RedactScalar}}})
	registerType(restype.Descriptor{Type: TypePinpointSMSChannel, Service: "pinpoint"})
	registerType(restype.Descriptor{Type: TypePinpointVoiceChannel, Service: "pinpoint"})
	registerType(restype.Descriptor{Type: TypePinpointEmailTemplate, Service: "pinpoint"})
	registerType(restype.Descriptor{Type: TypePinpointInAppTemplate, Service: "pinpoint"})
	registerType(restype.Descriptor{Type: TypePinpointPushTemplate, Service: "pinpoint"})
	registerType(restype.Descriptor{Type: TypePinpointSmsTemplate, Service: "pinpoint"})
	registerType(restype.Descriptor{Type: TypePinpointVoiceTemplate, Service: "pinpoint"})
	registerService(serviceEntry{
		name: "aws:pinpoint",
		fn:   scanPinpoint,
	})
}

// pinpointAPI — narrow set of Pinpoint ops used by scanPinpoint phases.
// Pinpoint has no SDK paginators; all list ops use manual Token/NextToken
// loops. App-scoped ops (Campaigns, Segments, Settings, EventStream,
// Channels) fan-out per-app.
type pinpointAPI interface {
	GetApps(context.Context, *pinpoint.GetAppsInput, ...func(*pinpoint.Options)) (*pinpoint.GetAppsOutput, error)
	GetCampaigns(context.Context, *pinpoint.GetCampaignsInput, ...func(*pinpoint.Options)) (*pinpoint.GetCampaignsOutput, error)
	GetSegments(context.Context, *pinpoint.GetSegmentsInput, ...func(*pinpoint.Options)) (*pinpoint.GetSegmentsOutput, error)
	GetApplicationSettings(context.Context, *pinpoint.GetApplicationSettingsInput, ...func(*pinpoint.Options)) (*pinpoint.GetApplicationSettingsOutput, error)
	GetEventStream(context.Context, *pinpoint.GetEventStreamInput, ...func(*pinpoint.Options)) (*pinpoint.GetEventStreamOutput, error)
	GetAdmChannel(context.Context, *pinpoint.GetAdmChannelInput, ...func(*pinpoint.Options)) (*pinpoint.GetAdmChannelOutput, error)
	GetApnsChannel(context.Context, *pinpoint.GetApnsChannelInput, ...func(*pinpoint.Options)) (*pinpoint.GetApnsChannelOutput, error)
	GetApnsSandboxChannel(context.Context, *pinpoint.GetApnsSandboxChannelInput, ...func(*pinpoint.Options)) (*pinpoint.GetApnsSandboxChannelOutput, error)
	GetApnsVoipChannel(context.Context, *pinpoint.GetApnsVoipChannelInput, ...func(*pinpoint.Options)) (*pinpoint.GetApnsVoipChannelOutput, error)
	GetApnsVoipSandboxChannel(context.Context, *pinpoint.GetApnsVoipSandboxChannelInput, ...func(*pinpoint.Options)) (*pinpoint.GetApnsVoipSandboxChannelOutput, error)
	GetBaiduChannel(context.Context, *pinpoint.GetBaiduChannelInput, ...func(*pinpoint.Options)) (*pinpoint.GetBaiduChannelOutput, error)
	GetEmailChannel(context.Context, *pinpoint.GetEmailChannelInput, ...func(*pinpoint.Options)) (*pinpoint.GetEmailChannelOutput, error)
	GetGcmChannel(context.Context, *pinpoint.GetGcmChannelInput, ...func(*pinpoint.Options)) (*pinpoint.GetGcmChannelOutput, error)
	GetSmsChannel(context.Context, *pinpoint.GetSmsChannelInput, ...func(*pinpoint.Options)) (*pinpoint.GetSmsChannelOutput, error)
	GetVoiceChannel(context.Context, *pinpoint.GetVoiceChannelInput, ...func(*pinpoint.Options)) (*pinpoint.GetVoiceChannelOutput, error)
	ListTemplates(context.Context, *pinpoint.ListTemplatesInput, ...func(*pinpoint.Options)) (*pinpoint.ListTemplatesOutput, error)
}

// The list ops' PageSize carries no range in the model, so it is left unset
// and the service's default page size applies.
const (
	pinpointAppsOp        = "pinpoint:GetApps"
	pinpointSettingsOp    = "pinpoint:GetApplicationSettings"
	pinpointEventStreamOp = "pinpoint:GetEventStream"
	pinpointCampaignsOp   = "pinpoint:GetCampaigns"
	pinpointSegmentsOp    = "pinpoint:GetSegments"
	pinpointTemplatesOp   = "pinpoint:ListTemplates"
)

// pinpointNotFound is the code every per-app op models for an app it does not
// find, such as one deleted since it was listed. GetEventStream and the
// Get*Channel ops are also expected to answer with it for an app with no event
// stream or no such channel (not verified live); either way the app is skipped
// silently.
var pinpointNotFound = []string{"NotFoundException"}

// pinpointPhase is one independent phase of scanPinpoint, warned under op.
type pinpointPhase struct {
	op  string
	run func() (int, int, error)
}

// pinpointApps is what every per-app phase reads and writes with: the apps the
// first phase stored, by Id.
type pinpointApps struct {
	st             *store.Store
	acct           *account
	region, scanID string
	ids            []string
}

// scanPinpoint stores the region's apps, then each app's settings, event
// stream, campaigns, segments and channels, then the account's message
// templates. Every phase runs; the per-app phases read only the apps the first
// phase stored, and only a store write failure stops the scan.
func scanPinpoint(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	client := pinpoint.NewFromConfig(acct.cfg, func(o *pinpoint.Options) { o.Region = region })
	var p phaseTotals
	t, i, appIDs, err := scanPinpointApps(ctx, client, acct, region, st, scanID)
	if p.addPhase(pinpointAppsOp, t, i, err) {
		return p.result(st, acct, region)
	}
	apps := pinpointApps{st: st, acct: acct, region: region, scanID: scanID, ids: appIDs}
	phases := []pinpointPhase{
		pinpointPerAppPhase(ctx, apps, pinpointSettingsOp, TypePinpointApplicationSettings, "settings", "settings",
			func(ctx context.Context, id string) (*pptypes.ApplicationSettingsResource, error) {
				out, err := client.GetApplicationSettings(ctx, &pinpoint.GetApplicationSettingsInput{ApplicationId: &id})
				if err != nil {
					return nil, err
				}
				return out.ApplicationSettingsResource, nil
			}),
		pinpointPerAppPhase(ctx, apps, pinpointEventStreamOp, TypePinpointEventStream, "eventstream", "eventstream",
			func(ctx context.Context, id string) (*pptypes.EventStream, error) {
				out, err := client.GetEventStream(ctx, &pinpoint.GetEventStreamInput{ApplicationId: &id})
				if err != nil {
					return nil, err
				}
				return out.EventStream, nil
			}),
		{pinpointCampaignsOp, func() (int, int, error) { return scanPinpointCampaigns(ctx, client, apps) }},
		{pinpointSegmentsOp, func() (int, int, error) { return scanPinpointSegments(ctx, client, apps) }},
	}
	phases = append(phases, pinpointChannelPhases(ctx, client, apps)...)
	phases = append(phases, pinpointPhase{pinpointTemplatesOp, func() (int, int, error) {
		return scanPinpointTemplates(ctx, client, acct, region, st, scanID)
	}})
	for _, ph := range phases {
		t, i, err := ph.run()
		if p.addPhase(ph.op, t, i, err) {
			break
		}
	}
	return p.result(st, acct, region)
}

func pinpointARN(partition, region, acct, kind, appID string) string {
	return arnFor(partition, "mobiletargeting", region, acct, "apps/"+appID+"/"+kind)
}

// pinpointListedRow builds the row of a listed Pinpoint resource, named by
// its Name or else its Id, or returns why it cannot be stored (what says which
// resources) and its Id when the Arn is empty.
func pinpointListedRow(acct *account, region, scanID, rtype, what string, arn, id, name *string, attrs any, tags map[string]string) (*store.Resource, string, string) {
	if sv(arn) == "" {
		return nil, what + " have an empty Arn", sv(id)
	}
	label := sv(name)
	if label == "" {
		label = sv(id)
	}
	return &store.Resource{
		Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
		Type: rtype, NativeID: *arn, Name: &label, Region: &region,
		AttributesJSON: mustJSON(attrs), TagsJSON: mapTagsJSON(tags), DiscoveredBy: scanID,
	}, "", ""
}

// scanPinpointApps stores every app GetApps lists and returns the Ids of the
// stored ones for the per-app phases.
func scanPinpointApps(ctx context.Context, client pinpointAPI, acct *account, region string, st *store.Store, scanID string) (int, int, []string, error) {
	total, inserted, parents, err := storeParentList(ctx, st, acct, region, pinpointAppsOp, "apps", "Id",
		func(ctx context.Context, token *string) ([]pptypes.ApplicationResponse, *string, error) {
			out, err := client.GetApps(ctx, &pinpoint.GetAppsInput{Token: token})
			if err != nil || out.ApplicationsResponse == nil {
				return nil, nil, err
			}
			return out.ApplicationsResponse.Item, out.ApplicationsResponse.NextToken, nil
		},
		func(a pptypes.ApplicationResponse) (*store.Resource, string, string) {
			return pinpointListedRow(acct, region, scanID, TypePinpointApp, "apps", a.Arn, a.Id, a.Name, a, a.Tags)
		},
		func(a pptypes.ApplicationResponse) string { return sv(a.Id) })
	ids := make([]string, len(parents))
	for i, p := range parents {
		ids[i] = p.id
	}
	return total, inserted, ids, err
}

// pinpointPerAppPhase is the phase that reads one configuration body per app
// concurrently through get and stores each as rtype, named name, under
// NativeID apps/{appId}/{path}. An app for which get returns a pinpointNotFound code is
// skipped; a nil body is not stored.
func pinpointPerAppPhase[Body any](ctx context.Context, apps pinpointApps, op, rtype, path, name string,
	get func(ctx context.Context, appID string) (*Body, error),
) pinpointPhase {
	return pinpointPhase{op, func() (int, int, error) {
		return storeTokenEachParent(ctx, apps.st, apps.acct, apps.region, op, pinpointNotFound, apps.ids,
			func(ctx context.Context, appID string, _ *string) ([]*Body, *string, error) {
				body, err := get(ctx, appID)
				if err != nil || body == nil {
					return nil, nil, err
				}
				return []*Body{body}, nil, nil
			},
			func(appID string, body *Body) (*store.Resource, string, string) {
				n := name
				return &store.Resource{
					Provider: "aws", AccountID: apps.acct.ID, AccountName: &apps.acct.Name,
					Type: rtype, NativeID: pinpointARN(apps.acct.partition(), apps.region, apps.acct.ID, path, appID),
					Name: &n, Region: &apps.region, AttributesJSON: mustJSON(body), DiscoveredBy: apps.scanID,
				}, "", ""
			})
	}}
}

// scanPinpointCampaigns lists the campaigns of each app concurrently.
func scanPinpointCampaigns(ctx context.Context, client pinpointAPI, apps pinpointApps) (int, int, error) {
	return storeTokenEachParent(ctx, apps.st, apps.acct, apps.region, pinpointCampaignsOp, pinpointNotFound, apps.ids,
		func(ctx context.Context, appID string, token *string) ([]pptypes.CampaignResponse, *string, error) {
			out, err := client.GetCampaigns(ctx, &pinpoint.GetCampaignsInput{ApplicationId: &appID, Token: token})
			if err != nil || out.CampaignsResponse == nil {
				return nil, nil, err
			}
			return out.CampaignsResponse.Item, out.CampaignsResponse.NextToken, nil
		},
		func(_ string, c pptypes.CampaignResponse) (*store.Resource, string, string) {
			return pinpointListedRow(apps.acct, apps.region, apps.scanID, TypePinpointCampaign, "campaigns", c.Arn, c.Id, c.Name, c, c.Tags)
		})
}

// scanPinpointSegments lists the segments of each app concurrently.
func scanPinpointSegments(ctx context.Context, client pinpointAPI, apps pinpointApps) (int, int, error) {
	return storeTokenEachParent(ctx, apps.st, apps.acct, apps.region, pinpointSegmentsOp, pinpointNotFound, apps.ids,
		func(ctx context.Context, appID string, token *string) ([]pptypes.SegmentResponse, *string, error) {
			out, err := client.GetSegments(ctx, &pinpoint.GetSegmentsInput{ApplicationId: &appID, Token: token})
			if err != nil || out.SegmentsResponse == nil {
				return nil, nil, err
			}
			return out.SegmentsResponse.Item, out.SegmentsResponse.NextToken, nil
		},
		func(_ string, s pptypes.SegmentResponse) (*store.Resource, string, string) {
			return pinpointListedRow(apps.acct, apps.region, apps.scanID, TypePinpointSegment, "segments", s.Arn, s.Id, s.Name, s, s.Tags)
		})
}

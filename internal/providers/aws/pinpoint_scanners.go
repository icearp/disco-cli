package aws

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/store"
	"github.com/aws/aws-sdk-go-v2/service/pinpoint"
)

func init() {
	registerService(serviceEntry{
		name: "aws:pinpoint",
		fn:   scanPinpoint,
		emits: []coverage.TypeDecl{
			{Service: "pinpoint", DiscoType: TypePinpointApp, Leaf: true},
			{Service: "pinpoint", DiscoType: TypePinpointApplicationSettings},
			{Service: "pinpoint", DiscoType: TypePinpointEventStream},
			{Service: "pinpoint", DiscoType: TypePinpointCampaign},
			{Service: "pinpoint", DiscoType: TypePinpointSegment},
			{Service: "pinpoint", DiscoType: TypePinpointADMChannel},
			{Service: "pinpoint", DiscoType: TypePinpointAPNSChannel},
			{Service: "pinpoint", DiscoType: TypePinpointAPNSSandboxChannel},
			{Service: "pinpoint", DiscoType: TypePinpointAPNSVoipChannel},
			{Service: "pinpoint", DiscoType: TypePinpointAPNSVoipSandboxChannel},
			{Service: "pinpoint", DiscoType: TypePinpointBaiduChannel},
			{Service: "pinpoint", DiscoType: TypePinpointEmailChannel},
			{Service: "pinpoint", DiscoType: TypePinpointGCMChannel},
			{Service: "pinpoint", DiscoType: TypePinpointSMSChannel},
			{Service: "pinpoint", DiscoType: TypePinpointVoiceChannel},
			{Service: "pinpoint", DiscoType: TypePinpointEmailTemplate, Leaf: true},
			{Service: "pinpoint", DiscoType: TypePinpointInAppTemplate, Leaf: true},
			{Service: "pinpoint", DiscoType: TypePinpointPushTemplate, Leaf: true},
			{Service: "pinpoint", DiscoType: TypePinpointSmsTemplate, Leaf: true},
		},
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

func scanPinpoint(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := pinpoint.NewFromConfig(acct.cfg, func(o *pinpoint.Options) { o.Region = region })

	appIDs, t, i, ferr := scanPinpointApps(ctx, client, acct, region, st, scanID)
	if ferr != nil {
		return 0, 0, ferr
	}
	total += t
	inserted += i

	for _, phase := range []func() (int, int, error){
		func() (int, int, error) {
			return scanPinpointAppSettings(ctx, client, acct, region, st, scanID, appIDs)
		},
		func() (int, int, error) {
			return scanPinpointEventStreams(ctx, client, acct, region, st, scanID, appIDs)
		},
		func() (int, int, error) { return scanPinpointCampaigns(ctx, client, acct, region, st, scanID, appIDs) },
		func() (int, int, error) { return scanPinpointSegments(ctx, client, acct, region, st, scanID, appIDs) },
		func() (int, int, error) { return scanPinpointChannels(ctx, client, acct, region, st, scanID, appIDs) },
		func() (int, int, error) { return scanPinpointTemplates(ctx, client, acct, region, st, scanID) },
	} {
		t, i, ferr := phase()
		if ferr != nil {
			return total, inserted, ferr
		}
		total += t
		inserted += i
	}
	return total, inserted, nil
}

func pinpointARN(region, acct, kind, appID string) string {
	return fmt.Sprintf("arn:aws:mobiletargeting:%s:%s:apps/%s/%s", region, acct, appID, kind)
}

func scanPinpointApps(ctx context.Context, client pinpointAPI, acct *account, region string, st *store.Store, scanID string) ([]string, int, int, error) {
	var ids []string
	var batch []*store.Resource
	var token *string
	for {
		out, err := client.GetApps(ctx, &pinpoint.GetAppsInput{Token: token})
		if err != nil {
			if isAccessDenied(err) {
				_ = skipIfAccessDenied(st, "pinpoint:GetApps", acct.ID, region, err)
				return nil, 0, 0, nil
			}
			return nil, 0, 0, fmt.Errorf("pinpoint:GetApps: %w", err)
		}
		if out.ApplicationsResponse == nil {
			break
		}
		for _, a := range out.ApplicationsResponse.Item {
			arn := sv(a.Arn)
			id := sv(a.Id)
			if id == "" {
				continue
			}
			ids = append(ids, id)
			label := sv(a.Name)
			if label == "" {
				label = id
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypePinpointApp, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(a), DiscoveredBy: scanID,
			})
		}
		if out.ApplicationsResponse.NextToken == nil || *out.ApplicationsResponse.NextToken == "" {
			break
		}
		token = out.ApplicationsResponse.NextToken
	}
	if len(batch) == 0 {
		return ids, 0, 0, nil
	}
	n, uerr := st.UpsertResources(batch)
	if uerr != nil {
		return ids, 0, 0, fmt.Errorf("upsert pinpoint apps: %w", uerr)
	}
	return ids, len(batch), n, nil
}

func scanPinpointAppSettings(ctx context.Context, client pinpointAPI, acct *account, region string, st *store.Store, scanID string, appIDs []string) (int, int, error) {
	if len(appIDs) == 0 {
		return 0, 0, nil
	}
	var batch []*store.Resource
	for _, appID := range appIDs {
		id := appID
		out, derr := client.GetApplicationSettings(ctx, &pinpoint.GetApplicationSettingsInput{ApplicationId: &id})
		if derr != nil {
			if isAccessDenied(derr) {
				continue
			}
			return 0, 0, fmt.Errorf("pinpoint:GetApplicationSettings %s: %w", appID, derr)
		}
		if out.ApplicationSettingsResource == nil {
			continue
		}
		arn := pinpointARN(region, acct.ID, "settings", appID)
		name := "settings"
		batch = append(batch, &store.Resource{
			Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
			Type: TypePinpointApplicationSettings, NativeID: arn,
			Name: &name, Region: &region, AttributesJSON: mustJSON(out.ApplicationSettingsResource), DiscoveredBy: scanID,
		})
	}
	return upsertBatch(st, batch, "pinpoint application-settings")
}

func scanPinpointEventStreams(ctx context.Context, client pinpointAPI, acct *account, region string, st *store.Store, scanID string, appIDs []string) (int, int, error) {
	if len(appIDs) == 0 {
		return 0, 0, nil
	}
	var batch []*store.Resource
	for _, appID := range appIDs {
		id := appID
		out, derr := client.GetEventStream(ctx, &pinpoint.GetEventStreamInput{ApplicationId: &id})
		if derr != nil {
			if isAccessDenied(derr) || isAPIErrorCode(derr, "NotFoundException") {
				continue
			}
			return 0, 0, fmt.Errorf("pinpoint:GetEventStream %s: %w", appID, derr)
		}
		if out.EventStream == nil {
			continue
		}
		arn := pinpointARN(region, acct.ID, "eventstream", appID)
		name := "eventstream"
		batch = append(batch, &store.Resource{
			Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
			Type: TypePinpointEventStream, NativeID: arn,
			Name: &name, Region: &region, AttributesJSON: mustJSON(out.EventStream), DiscoveredBy: scanID,
		})
	}
	return upsertBatch(st, batch, "pinpoint event-streams")
}

func scanPinpointCampaigns(ctx context.Context, client pinpointAPI, acct *account, region string, st *store.Store, scanID string, appIDs []string) (int, int, error) {
	if len(appIDs) == 0 {
		return 0, 0, nil
	}
	var batch []*store.Resource
	for _, appID := range appIDs {
		id := appID
		var token *string
		for {
			out, derr := client.GetCampaigns(ctx, &pinpoint.GetCampaignsInput{ApplicationId: &id, Token: token})
			if derr != nil {
				if isAccessDenied(derr) {
					break
				}
				return 0, 0, fmt.Errorf("pinpoint:GetCampaigns %s: %w", appID, derr)
			}
			if out.CampaignsResponse == nil {
				break
			}
			for _, c := range out.CampaignsResponse.Item {
				arn := sv(c.Arn)
				if arn == "" {
					continue
				}
				label := sv(c.Name)
				if label == "" {
					label = sv(c.Id)
				}
				batch = append(batch, &store.Resource{
					Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
					Type: TypePinpointCampaign, NativeID: arn,
					Name: &label, Region: &region, AttributesJSON: mustJSON(c), DiscoveredBy: scanID,
				})
			}
			if out.CampaignsResponse.NextToken == nil || *out.CampaignsResponse.NextToken == "" {
				break
			}
			token = out.CampaignsResponse.NextToken
		}
	}
	return upsertBatch(st, batch, "pinpoint campaigns")
}

func scanPinpointSegments(ctx context.Context, client pinpointAPI, acct *account, region string, st *store.Store, scanID string, appIDs []string) (int, int, error) {
	if len(appIDs) == 0 {
		return 0, 0, nil
	}
	var batch []*store.Resource
	for _, appID := range appIDs {
		id := appID
		var token *string
		for {
			out, derr := client.GetSegments(ctx, &pinpoint.GetSegmentsInput{ApplicationId: &id, Token: token})
			if derr != nil {
				if isAccessDenied(derr) {
					break
				}
				return 0, 0, fmt.Errorf("pinpoint:GetSegments %s: %w", appID, derr)
			}
			if out.SegmentsResponse == nil {
				break
			}
			for _, s := range out.SegmentsResponse.Item {
				arn := sv(s.Arn)
				if arn == "" {
					continue
				}
				label := sv(s.Name)
				if label == "" {
					label = sv(s.Id)
				}
				batch = append(batch, &store.Resource{
					Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
					Type: TypePinpointSegment, NativeID: arn,
					Name: &label, Region: &region, AttributesJSON: mustJSON(s), DiscoveredBy: scanID,
				})
			}
			if out.SegmentsResponse.NextToken == nil || *out.SegmentsResponse.NextToken == "" {
				break
			}
			token = out.SegmentsResponse.NextToken
		}
	}
	return upsertBatch(st, batch, "pinpoint segments")
}

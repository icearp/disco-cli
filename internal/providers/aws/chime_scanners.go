package aws

import (
	"context"
	"errors"
	"fmt"

	ci "github.com/aws/aws-sdk-go-v2/service/chimesdkidentity"
	cp "github.com/aws/aws-sdk-go-v2/service/chimesdkmediapipelines"
	cm "github.com/aws/aws-sdk-go-v2/service/chimesdkmessaging"
	cmtypes "github.com/aws/aws-sdk-go-v2/service/chimesdkmessaging/types"
	cv "github.com/aws/aws-sdk-go-v2/service/chimesdkvoice"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerType(restype.Descriptor{Type: TypeChimeAppInstance, Service: "chime"})
	registerType(restype.Descriptor{Type: TypeChimeAppInstanceBot, Service: "chime"})
	registerType(restype.Descriptor{Type: TypeChimeAppInstanceUser, Service: "chime"})
	registerType(restype.Descriptor{Type: TypeChimeChannelFlow, Service: "chime"})
	registerType(restype.Descriptor{Type: TypeChimeMediaPipeline, Service: "chime"})
	registerType(restype.Descriptor{Type: TypeChimeMediaInsightsPipelineConfiguration, Service: "chime"})
	registerType(restype.Descriptor{Type: TypeChimeMediaPipelineKinesisVideoStreamPool, Service: "chime"})
	registerType(restype.Descriptor{Type: TypeChimeSipMediaApplication, Service: "chime"})
	registerType(restype.Descriptor{Type: TypeChimeVoiceConnector, Service: "chime"})
	registerType(restype.Descriptor{Type: TypeChimeVoiceProfileDomain, Service: "chime"})
	registerType(restype.Descriptor{Type: TypeChimeVoiceProfile, Service: "chime", Untaggable: true})
	registerType(restype.Descriptor{Type: TypeChimeSipRule, Service: "chime"})
	registerType(restype.Descriptor{Type: TypeChimeVoiceConnectorGroup, Service: "chime"})
	registerService(serviceEntry{
		name: "aws:chime",
		fn:   scanChime,
	})
}

// chimeChannelFlowAttrs embeds the native channel-flow summary plus the owning
// app-instance ARN (ListChannelFlows is called per app-instance but does not
// echo it back), so the resolver can wire channel-flow → app-instance.
type chimeChannelFlowAttrs struct {
	cmtypes.ChannelFlowSummary
	AppInstanceArn *string `json:"appInstanceArn,omitempty"`
}

// scanChime discovers the Chime SDK surface across five sub-SDKs: identity
// (app-instances + per-instance bots/users), messaging (per-instance channel
// flows), media-pipelines, and voice (SIP apps, voice connectors, voice-profile
// domains + profiles, SIP rules, voice connector groups). Channels (data-plane, needs ChimeBearer user identity) and
// meetings (ephemeral, no list API) aren't scanned. Per-region; unsupported
// regions surface as endpoint/access errors, dispatcher-tolerated.
func scanChime(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	idClient := ci.NewFromConfig(acct.cfg, func(o *ci.Options) { o.Region = region })
	msgClient := cm.NewFromConfig(acct.cfg, func(o *cm.Options) { o.Region = region })
	mpClient := cp.NewFromConfig(acct.cfg, func(o *cp.Options) { o.Region = region })
	vClient := cv.NewFromConfig(acct.cfg, func(o *cv.Options) { o.Region = region })

	var batch []*store.Resource

	// The four sub-SDKs have independent regional availability (Voice Connector
	// reaches more regions than identity/messaging), so an identity failure must
	// not gate media-pipelines/voice — each phase tolerates its own errors.
	appInstanceARNs := chimeAppInstances(ctx, idClient, acct, region, st, scanID, &batch)
	for _, aiARN := range appInstanceARNs {
		chimeAppInstanceChildren(ctx, idClient, msgClient, acct, region, st, scanID, aiARN, &batch)
	}
	chimeMediaPipelines(ctx, mpClient, acct, region, st, scanID, &batch)
	chimeVoice(ctx, vClient, acct, region, st, scanID, &batch)
	routingErr := chimeVoiceRouting(ctx, vClient, acct, region, st, scanID, &batch)

	// The rows of every phase are stored even when a routing phase failed.
	total, inserted, err := upsertBatch(st, batch, "chime")
	return total, inserted, errors.Join(err, routingErr)
}

// chimeListErr records a ScanWarning for an AccessDenied list failure (partial
// IAM grant) and otherwise stays silent — most regions don't run a given Chime
// sub-SDK, so endpoint/region-absent errors are expected noise the dispatcher's
// transient handling already covers. Callers break the affected loop after this.
func chimeListErr(st *store.Store, op, acctID, region string, err error) {
	if !isAccessDenied(err) {
		return
	}
	// Chime sub-SDKs have independent regional/account availability; where one
	// isn't offered the gateway answers ForbiddenException ("This feature is not
	// available" / "AWS account is not enabled" / empty body). Availability noise,
	// not a real IAM denial — silent-skip. Message-bearing denials still warn.
	if isAccessDeniedWithMessage(err, "feature is not available") ||
		isAccessDeniedWithMessage(err, "account is not enabled") ||
		isClosedToNewCustomers(err) {
		return
	}
	_ = skipIfAccessDenied(st, op, acctID, region, err)
}

func chimeAppInstances(ctx context.Context, client *ci.Client, acct *account, region string, st *store.Store, scanID string, batch *[]*store.Resource) []string {
	var arns []string
	var token *string
	for {
		out, err := client.ListAppInstances(ctx, &ci.ListAppInstancesInput{NextToken: token})
		if err != nil {
			chimeListErr(st, "chime:ListAppInstances", acct.ID, region, err)
			break
		}
		for _, a := range out.AppInstances {
			arn := sv(a.AppInstanceArn)
			if arn == "" {
				continue
			}
			arns = append(arns, arn)
			*batch = append(*batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeChimeAppInstance, NativeID: arn,
				Name: a.Name, Region: &region, AttributesJSON: mustJSON(a), DiscoveredBy: scanID,
			})
		}
		if out.NextToken == nil || *out.NextToken == "" {
			break
		}
		token = out.NextToken
	}
	return arns
}

// chimeAppInstanceChildren scans one app-instance's bots, users and channel
// flows. Per-child errors are tolerated (warn on AccessDenied, then continue).
func chimeAppInstanceChildren(ctx context.Context, id *ci.Client, msg *cm.Client, acct *account, region string, st *store.Store, scanID, aiARN string, batch *[]*store.Resource) {
	var token *string
	for {
		out, err := id.ListAppInstanceBots(ctx, &ci.ListAppInstanceBotsInput{AppInstanceArn: &aiARN, NextToken: token})
		if err != nil {
			chimeListErr(st, "chime:ListAppInstanceBots", acct.ID, region, err)
			break
		}
		for _, b := range out.AppInstanceBots {
			if arn := sv(b.AppInstanceBotArn); arn != "" {
				*batch = append(*batch, &store.Resource{
					Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
					Type: TypeChimeAppInstanceBot, NativeID: arn,
					Name: b.Name, Region: &region, AttributesJSON: mustJSON(b), DiscoveredBy: scanID,
				})
			}
		}
		if out.NextToken == nil || *out.NextToken == "" {
			break
		}
		token = out.NextToken
	}

	token = nil
	for {
		out, err := id.ListAppInstanceUsers(ctx, &ci.ListAppInstanceUsersInput{AppInstanceArn: &aiARN, NextToken: token})
		if err != nil {
			chimeListErr(st, "chime:ListAppInstanceUsers", acct.ID, region, err)
			break
		}
		for _, u := range out.AppInstanceUsers {
			if arn := sv(u.AppInstanceUserArn); arn != "" {
				*batch = append(*batch, &store.Resource{
					Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
					Type: TypeChimeAppInstanceUser, NativeID: arn,
					Name: u.Name, Region: &region, AttributesJSON: mustJSON(u), DiscoveredBy: scanID,
				})
			}
		}
		if out.NextToken == nil || *out.NextToken == "" {
			break
		}
		token = out.NextToken
	}

	token = nil
	for {
		out, err := msg.ListChannelFlows(ctx, &cm.ListChannelFlowsInput{AppInstanceArn: &aiARN, NextToken: token})
		if err != nil {
			chimeListErr(st, "chime:ListChannelFlows", acct.ID, region, err)
			break
		}
		for _, cf := range out.ChannelFlows {
			arn := sv(cf.ChannelFlowArn)
			if arn == "" {
				continue
			}
			aiCopy := aiARN
			*batch = append(*batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeChimeChannelFlow, NativeID: arn,
				Name: cf.Name, Region: &region,
				AttributesJSON: mustJSON(chimeChannelFlowAttrs{ChannelFlowSummary: cf, AppInstanceArn: &aiCopy}),
				DiscoveredBy:   scanID,
			})
		}
		if out.NextToken == nil || *out.NextToken == "" {
			break
		}
		token = out.NextToken
	}
}

func chimeMediaPipelines(ctx context.Context, client *cp.Client, acct *account, region string, st *store.Store, scanID string, batch *[]*store.Resource) {
	var token *string
	for {
		out, err := client.ListMediaPipelines(ctx, &cp.ListMediaPipelinesInput{NextToken: token})
		if err != nil {
			chimeListErr(st, "chime:ListMediaPipelines", acct.ID, region, err)
			break
		}
		for _, p := range out.MediaPipelines {
			if arn := sv(p.MediaPipelineArn); arn != "" {
				*batch = append(*batch, &store.Resource{
					Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
					Type: TypeChimeMediaPipeline, NativeID: arn,
					Region: &region, AttributesJSON: mustJSON(p), DiscoveredBy: scanID,
				})
			}
		}
		if out.NextToken == nil || *out.NextToken == "" {
			break
		}
		token = out.NextToken
	}

	token = nil
	for {
		out, err := client.ListMediaInsightsPipelineConfigurations(ctx, &cp.ListMediaInsightsPipelineConfigurationsInput{NextToken: token})
		if err != nil {
			chimeListErr(st, "chime:ListMediaInsightsPipelineConfigurations", acct.ID, region, err)
			break
		}
		for _, c := range out.MediaInsightsPipelineConfigurations {
			if arn := sv(c.MediaInsightsPipelineConfigurationArn); arn != "" {
				*batch = append(*batch, &store.Resource{
					Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
					Type: TypeChimeMediaInsightsPipelineConfiguration, NativeID: arn,
					Name: c.MediaInsightsPipelineConfigurationName, Region: &region,
					AttributesJSON: mustJSON(c), DiscoveredBy: scanID,
				})
			}
		}
		if out.NextToken == nil || *out.NextToken == "" {
			break
		}
		token = out.NextToken
	}

	token = nil
	for {
		out, err := client.ListMediaPipelineKinesisVideoStreamPools(ctx, &cp.ListMediaPipelineKinesisVideoStreamPoolsInput{NextToken: token})
		if err != nil {
			chimeListErr(st, "chime:ListMediaPipelineKinesisVideoStreamPools", acct.ID, region, err)
			break
		}
		for _, p := range out.KinesisVideoStreamPools {
			if arn := sv(p.PoolArn); arn != "" {
				*batch = append(*batch, &store.Resource{
					Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
					Type: TypeChimeMediaPipelineKinesisVideoStreamPool, NativeID: arn,
					Name: p.PoolName, Region: &region, AttributesJSON: mustJSON(p), DiscoveredBy: scanID,
				})
			}
		}
		if out.NextToken == nil || *out.NextToken == "" {
			break
		}
		token = out.NextToken
	}
}

func chimeVoice(ctx context.Context, client *cv.Client, acct *account, region string, st *store.Store, scanID string, batch *[]*store.Resource) {
	var token *string
	for {
		out, err := client.ListSipMediaApplications(ctx, &cv.ListSipMediaApplicationsInput{NextToken: token})
		if err != nil {
			chimeListErr(st, "chime:ListSipMediaApplications", acct.ID, region, err)
			break
		}
		for _, s := range out.SipMediaApplications {
			if arn := sv(s.SipMediaApplicationArn); arn != "" {
				*batch = append(*batch, &store.Resource{
					Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
					Type: TypeChimeSipMediaApplication, NativeID: arn,
					Name: s.Name, Region: &region, CreatedAt: tp(s.CreatedTimestamp),
					AttributesJSON: mustJSON(s), DiscoveredBy: scanID,
				})
			}
		}
		if out.NextToken == nil || *out.NextToken == "" {
			break
		}
		token = out.NextToken
	}

	token = nil
	for {
		out, err := client.ListVoiceConnectors(ctx, &cv.ListVoiceConnectorsInput{NextToken: token})
		if err != nil {
			chimeListErr(st, "chime:ListVoiceConnectors", acct.ID, region, err)
			break
		}
		for _, vc := range out.VoiceConnectors {
			if arn := sv(vc.VoiceConnectorArn); arn != "" {
				*batch = append(*batch, &store.Resource{
					Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
					Type: TypeChimeVoiceConnector, NativeID: arn,
					Name: vc.Name, Region: &region, CreatedAt: tp(vc.CreatedTimestamp),
					AttributesJSON: mustJSON(vc), DiscoveredBy: scanID,
				})
			}
		}
		if out.NextToken == nil || *out.NextToken == "" {
			break
		}
		token = out.NextToken
	}

	chimeVoiceProfileDomains(ctx, client, acct, region, st, scanID, batch)
}

// chimeVoiceProfileDomains scans voice-profile domains (enriched via
// GetVoiceProfileDomain for the KMS key) and each domain's voice profiles.
func chimeVoiceProfileDomains(ctx context.Context, client *cv.Client, acct *account, region string, st *store.Store, scanID string, batch *[]*store.Resource) {
	var domainIDs []string
	var token *string
	for {
		out, err := client.ListVoiceProfileDomains(ctx, &cv.ListVoiceProfileDomainsInput{NextToken: token})
		if err != nil {
			chimeListErr(st, "chime:ListVoiceProfileDomains", acct.ID, region, err)
			break
		}
		for _, d := range out.VoiceProfileDomains {
			arn := sv(d.VoiceProfileDomainArn)
			id := sv(d.VoiceProfileDomainId)
			if arn == "" || id == "" {
				continue
			}
			domainIDs = append(domainIDs, id)
			body := any(d)
			if got, gerr := client.GetVoiceProfileDomain(ctx, &cv.GetVoiceProfileDomainInput{VoiceProfileDomainId: &id}); gerr == nil && got.VoiceProfileDomain != nil {
				body = *got.VoiceProfileDomain
			}
			*batch = append(*batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeChimeVoiceProfileDomain, NativeID: arn,
				Name: d.Name, Region: &region, CreatedAt: tp(d.CreatedTimestamp),
				AttributesJSON: mustJSON(body), DiscoveredBy: scanID,
			})
		}
		if out.NextToken == nil || *out.NextToken == "" {
			break
		}
		token = out.NextToken
	}

	for _, id := range domainIDs {
		var ptoken *string
		for {
			out, err := client.ListVoiceProfiles(ctx, &cv.ListVoiceProfilesInput{VoiceProfileDomainId: &id, NextToken: ptoken})
			if err != nil {
				chimeListErr(st, "chime:ListVoiceProfiles", acct.ID, region, err)
				break
			}
			for _, p := range out.VoiceProfiles {
				if arn := sv(p.VoiceProfileArn); arn != "" {
					*batch = append(*batch, &store.Resource{
						Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
						Type: TypeChimeVoiceProfile, NativeID: arn,
						Region: &region, CreatedAt: tp(p.CreatedTimestamp),
						AttributesJSON: mustJSON(p), DiscoveredBy: scanID,
					})
				}
			}
			if out.NextToken == nil || *out.NextToken == "" {
				break
			}
			ptoken = out.NextToken
		}
	}
}

// chimeVoicePageLimit is the documented MaxResults cap (ResultMax) of
// ListSipRules and ListVoiceConnectorGroups.
const chimeVoicePageLimit = 100

// chimeVoiceRoutingAPI is the Chime SDK Voice surface the SIP-rule and
// voice-connector-group phases call.
type chimeVoiceRoutingAPI interface {
	ListSipRules(ctx context.Context, in *cv.ListSipRulesInput, optFns ...func(*cv.Options)) (*cv.ListSipRulesOutput, error)
	ListVoiceConnectorGroups(ctx context.Context, in *cv.ListVoiceConnectorGroupsInput, optFns ...func(*cv.Options)) (*cv.ListVoiceConnectorGroupsOutput, error)
}

// chimeVoiceRouting lists SIP rules and voice connector groups into batch.
// Unlike the older chime phases it returns unexpected errors, so it runs after
// all of them; the two phases are independent and both always run.
func chimeVoiceRouting(ctx context.Context, client chimeVoiceRoutingAPI, acct *account, region string, st *store.Store, scanID string, batch *[]*store.Resource) error {
	sipErr := chimeSipRules(ctx, client, acct, region, st, scanID, batch)
	groupErr := chimeVoiceConnectorGroups(ctx, client, acct, region, st, scanID, batch)
	return errors.Join(sipErr, groupErr)
}

// chimeSipRuleNativeID builds a SIP rule's NativeID. AWS issues no SIP-rule ARN
// (SipRule carries only SipRuleId, and the Service Reference lists no sip-rule
// resource), so the id is synthesized in the chime ARN shape.
func chimeSipRuleNativeID(region, accountID, sipRuleID string) string {
	return "arn:aws:chime:" + region + ":" + accountID + ":sip-rule/" + sipRuleID
}

func chimeSipRules(ctx context.Context, client chimeVoiceRoutingAPI, acct *account, region string, st *store.Store, scanID string, batch *[]*store.Resource) error {
	const op = "chime:ListSipRules"
	p := cv.NewListSipRulesPaginator(client, &cv.ListSipRulesInput{},
		func(o *cv.ListSipRulesPaginatorOptions) { o.Limit = chimeVoicePageLimit })
	for firstPage := true; p.HasMorePages(); firstPage = false {
		out, err := p.NextPage(ctx)
		if err != nil {
			return chimeVoicePhaseErr(st, op, acct, region, firstPage, err)
		}
		for _, r := range out.SipRules {
			id := sv(r.SipRuleId)
			if id == "" {
				continue
			}
			*batch = append(*batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeChimeSipRule, NativeID: chimeSipRuleNativeID(region, acct.ID, id),
				Name: r.Name, Region: &region, CreatedAt: tp(r.CreatedTimestamp),
				AttributesJSON: mustJSON(r), DiscoveredBy: scanID,
			})
		}
	}
	return nil
}

// chimeVoiceConnectorGroups lists voice connector groups, keyed by the
// VoiceConnectorGroupArn the API returns. The model marks that ARN optional, so
// groups without one are skipped under one warning naming the count.
func chimeVoiceConnectorGroups(ctx context.Context, client chimeVoiceRoutingAPI, acct *account, region string, st *store.Store, scanID string, batch *[]*store.Resource) error {
	const op = "chime:ListVoiceConnectorGroups"
	var noARN skipTally
	defer func() {
		if noARN.count > 0 {
			st.ReportWarning(store.ScanWarning{
				Provider: "aws", Service: op, Scope: acct.ID + "/" + region,
				Message: fmt.Sprintf("%d voice connector group(s) not stored: no VoiceConnectorGroupArn, first id %q", noARN.count, noARN.first),
			})
		}
	}()
	p := cv.NewListVoiceConnectorGroupsPaginator(client, &cv.ListVoiceConnectorGroupsInput{},
		func(o *cv.ListVoiceConnectorGroupsPaginatorOptions) { o.Limit = chimeVoicePageLimit })
	for firstPage := true; p.HasMorePages(); firstPage = false {
		out, err := p.NextPage(ctx)
		if err != nil {
			return chimeVoicePhaseErr(st, op, acct, region, firstPage, err)
		}
		for _, g := range out.VoiceConnectorGroups {
			arn := sv(g.VoiceConnectorGroupArn)
			if arn == "" {
				noARN.skipped(sv(g.VoiceConnectorGroupId), nil)
				continue
			}
			*batch = append(*batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeChimeVoiceConnectorGroup, NativeID: arn,
				Name: g.Name, Region: &region, CreatedAt: tp(g.CreatedTimestamp),
				AttributesJSON: mustJSON(g), DiscoveredBy: scanID,
			})
		}
	}
	return nil
}

// chimeVoicePhaseErr settles a SIP-rule or voice-connector-group list failure;
// rows from earlier pages stay in batch. A denial goes through chimeListErr
// like every chime phase. On the first page, an absent endpoint or operation
// is a region gap and is skipped silently, as the older voice phases skip it:
// Chime SDK Voice has its own endpoint host, and returning the gap would make
// the dispatcher report the whole chime service unavailable in this region
// while it stored rows from the other Chime endpoints. For the same reason an
// NXDOMAIN after a page succeeded is never returned: it warns and keeps the
// rows. A transient error is reported as a warning so it cannot hide the other
// phase's error.
func chimeVoicePhaseErr(st *store.Store, op string, acct *account, region string, firstPage bool, err error) error {
	if isAccessDenied(err) {
		chimeListErr(st, op, acct.ID, region, err)
		return nil
	}
	if firstPage && (isDNSNotFound(err) || isOpNotDeployed(err)) {
		return nil
	}
	if isDNSNotFound(err) {
		return skipIfTransient(st, op, acct.ID, region, err)
	}
	return demoteTransient(st, op, acct, region, fmt.Errorf("%s: %w", op, err))
}

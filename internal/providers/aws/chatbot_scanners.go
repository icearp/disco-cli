package aws

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/service/chatbot"
	chatbottypes "github.com/aws/aws-sdk-go-v2/service/chatbot/types"
	"github.com/icearp/disco-cli/internal/redact"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerType(restype.Descriptor{Type: TypeChatbotCustomAction, Service: "chatbot"})
	registerType(restype.Descriptor{Type: TypeChatbotSlackChannelConfiguration, Service: "chatbot"})
	registerType(restype.Descriptor{Type: TypeChatbotMicrosoftTeamsChannelConfiguration, Service: "chatbot"})
	// The Chime webhook URL is a bearer credential: anyone holding it can post
	// to the room. Create/UpdateChimeWebhookConfiguration take it; the rule
	// keeps it out of the store whenever the describe output carries it. The
	// model marks WebhookDescription @sensitive and it is free text an admin
	// may paste the URL into, so it is redacted too; ConfigurationName still
	// names the row.
	registerType(restype.Descriptor{Type: TypeChatbotChimeWebhookConfiguration, Service: "chatbot", Redact: []redact.Rule{
		{Path: "WebhookUrl", Mode: redact.RedactScalar},
		{Path: "WebhookDescription", Mode: redact.RedactScalar},
	}})
	registerType(restype.Descriptor{Type: TypeChatbotSlackUserIdentity, Service: "chatbot"})
	registerType(restype.Descriptor{Type: TypeChatbotMicrosoftTeamsUserIdentity, Service: "chatbot"})
	registerService(serviceEntry{
		name:   "aws:chatbot",
		global: true,
		fn:     scanChatbot,
	})
}

type chatbotAPI interface {
	ListCustomActions(context.Context, *chatbot.ListCustomActionsInput, ...func(*chatbot.Options)) (*chatbot.ListCustomActionsOutput, error)
	DescribeSlackChannelConfigurations(context.Context, *chatbot.DescribeSlackChannelConfigurationsInput, ...func(*chatbot.Options)) (*chatbot.DescribeSlackChannelConfigurationsOutput, error)
	ListMicrosoftTeamsChannelConfigurations(context.Context, *chatbot.ListMicrosoftTeamsChannelConfigurationsInput, ...func(*chatbot.Options)) (*chatbot.ListMicrosoftTeamsChannelConfigurationsOutput, error)
	DescribeChimeWebhookConfigurations(context.Context, *chatbot.DescribeChimeWebhookConfigurationsInput, ...func(*chatbot.Options)) (*chatbot.DescribeChimeWebhookConfigurationsOutput, error)
	DescribeSlackUserIdentities(context.Context, *chatbot.DescribeSlackUserIdentitiesInput, ...func(*chatbot.Options)) (*chatbot.DescribeSlackUserIdentitiesOutput, error)
	ListMicrosoftTeamsUserIdentities(context.Context, *chatbot.ListMicrosoftTeamsUserIdentitiesInput, ...func(*chatbot.Options)) (*chatbot.ListMicrosoftTeamsUserIdentitiesOutput, error)
}

const (
	chatbotChimeWebhooksOp       = "chatbot:DescribeChimeWebhookConfigurations"
	chatbotSlackUserIdentitiesOp = "chatbot:DescribeSlackUserIdentities"
	chatbotTeamsUserIdentitiesOp = "chatbot:ListMicrosoftTeamsUserIdentities"
	// chatbotPageLimit is the model's MaxResults maximum for the Chime webhook
	// and user identity list ops.
	chatbotPageLimit = 100
)

// chatbotChannels holds the ARNs of the channel configurations the channel
// phases stored this scan, the parents of the user identities.
type chatbotChannels struct {
	slack, teams map[string]bool
}

// scanChatbot discovers Chatbot custom actions, channel configurations (Slack +
// Microsoft Teams), Chime webhook configurations, and Slack and Microsoft Teams
// user identities. Global service; gated to us-east-2 to dedupe across regions.
//
// The channel phases (custom actions and channel configurations) stop at their
// first error other than a denial. The Chime webhook and user identity phases
// run whatever the channel phases returned, so their failure costs no other
// phase its rows. Each result has its transient error demoted to a warning only
// beside a hard error: joined, it would be buried in the hard error instead of
// reported under its op; the rest are joined and the counts summed.
func scanChatbot(ctx context.Context, acct *account, _ string, st *store.Store, scanID string) (int, int, error) {
	region := "us-east-2"
	client := chatbot.NewFromConfig(acct.cfg, func(o *chatbot.Options) { o.Region = region })
	total, inserted, channels, err := scanChatbotChannelPhases(ctx, client, acct, region, st, scanID)
	var errs phaseErrs
	errs.add("chatbot", err)
	for _, phase := range []struct {
		op  string
		run func() (int, int, error)
	}{
		{chatbotChimeWebhooksOp, func() (int, int, error) { return scanChatbotChimeWebhooks(ctx, client, acct, region, st, scanID) }},
		{chatbotSlackUserIdentitiesOp, func() (int, int, error) {
			return scanChatbotSlackUserIdentities(ctx, client, acct, region, st, scanID, channels.slack)
		}},
		{chatbotTeamsUserIdentitiesOp, func() (int, int, error) {
			return scanChatbotTeamsUserIdentities(ctx, client, acct, region, st, scanID, channels.teams)
		}},
	} {
		t, i, perr := phase.run()
		total += t
		inserted += i
		errs.add(phase.op, perr)
	}
	return total, inserted, errs.join(st, acct, region)
}

// scanChatbotChannelPhases runs the custom action and channel configuration
// phases in order, stopping at the first error other than a denial, and
// returns the channel
// configurations stored before it: the parents the user identity phases link
// under.
func scanChatbotChannelPhases(ctx context.Context, client chatbotAPI, acct *account, region string, st *store.Store, scanID string) (total, inserted int, channels chatbotChannels, err error) {
	t, i, err := scanChatbotCustomActions(ctx, client, acct, region, st, scanID)
	if err != nil {
		return total, inserted, channels, err
	}
	total += t
	inserted += i

	channels.slack, t, i, err = scanChatbotSlackChannels(ctx, client, acct, region, st, scanID)
	if err != nil {
		return total, inserted, channels, err
	}
	total += t
	inserted += i

	channels.teams, t, i, err = scanChatbotTeamsChannels(ctx, client, acct, region, st, scanID)
	if err != nil {
		return total, inserted, channels, err
	}
	return total + t, inserted + i, channels, nil
}

func scanChatbotCustomActions(ctx context.Context, client chatbotAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var batch []*store.Resource
	var nextToken *string
	for {
		out, err := client.ListCustomActions(ctx, &chatbot.ListCustomActionsInput{NextToken: nextToken})
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "chatbot:ListCustomActions", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("chatbot:ListCustomActions: %w", err)
		}
		for _, arn := range out.CustomActions {
			if arn == "" {
				continue
			}
			a := arn
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeChatbotCustomAction, NativeID: a,
				Name: &a, Region: regionGlobal,
				AttributesJSON: mustJSON(map[string]string{"CustomActionArn": a}), DiscoveredBy: scanID,
			})
		}
		if out.NextToken == nil || *out.NextToken == "" {
			break
		}
		nextToken = out.NextToken
	}
	return upsertBatch(st, batch, "chatbot custom-actions")
}

// scanChatbotSlackChannels stores the Slack channel configurations and
// returns the ARNs of those it stored.
func scanChatbotSlackChannels(ctx context.Context, client chatbotAPI, acct *account, region string, st *store.Store, scanID string) (map[string]bool, int, int, error) {
	var batch []*store.Resource
	var nextToken *string
	for {
		out, err := client.DescribeSlackChannelConfigurations(ctx, &chatbot.DescribeSlackChannelConfigurationsInput{NextToken: nextToken})
		if err != nil {
			if isAccessDenied(err) {
				return nil, 0, 0, skipIfAccessDenied(st, "chatbot:DescribeSlackChannelConfigurations", acct.ID, region, err)
			}
			return nil, 0, 0, fmt.Errorf("chatbot:DescribeSlackChannelConfigurations: %w", err)
		}
		for _, c := range out.SlackChannelConfigurations {
			arn := sv(c.ChatConfigurationArn)
			if arn == "" {
				continue
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeChatbotSlackChannelConfiguration, NativeID: arn,
				Name: c.ConfigurationName, Region: regionGlobal,
				AttributesJSON: mustJSON(c), DiscoveredBy: scanID,
			})
		}
		if out.NextToken == nil || *out.NextToken == "" {
			break
		}
		nextToken = out.NextToken
	}
	return upsertChatbotChannels(st, batch, "chatbot slack-channel-configurations")
}

// scanChatbotTeamsChannels stores the Microsoft Teams channel configurations
// and returns the ARNs of those it stored.
func scanChatbotTeamsChannels(ctx context.Context, client chatbotAPI, acct *account, region string, st *store.Store, scanID string) (map[string]bool, int, int, error) {
	var batch []*store.Resource
	var nextToken *string
	for {
		out, err := client.ListMicrosoftTeamsChannelConfigurations(ctx, &chatbot.ListMicrosoftTeamsChannelConfigurationsInput{NextToken: nextToken})
		if err != nil {
			if isAccessDenied(err) {
				return nil, 0, 0, skipIfAccessDenied(st, "chatbot:ListMicrosoftTeamsChannelConfigurations", acct.ID, region, err)
			}
			return nil, 0, 0, fmt.Errorf("chatbot:ListMicrosoftTeamsChannelConfigurations: %w", err)
		}
		for _, c := range out.TeamChannelConfigurations {
			arn := sv(c.ChatConfigurationArn)
			if arn == "" {
				continue
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeChatbotMicrosoftTeamsChannelConfiguration, NativeID: arn,
				Name: c.ConfigurationName, Region: regionGlobal,
				AttributesJSON: mustJSON(c), DiscoveredBy: scanID,
			})
		}
		if out.NextToken == nil || *out.NextToken == "" {
			break
		}
		nextToken = out.NextToken
	}
	return upsertChatbotChannels(st, batch, "chatbot microsoft-teams-channel-configurations")
}

// upsertChatbotChannels stores a channel configuration batch and returns the
// stored ARNs; nil when the upsert failed, so no child links to a missing row.
func upsertChatbotChannels(st *store.Store, batch []*store.Resource, label string) (map[string]bool, int, int, error) {
	total, inserted, err := upsertBatch(st, batch, label)
	if err != nil {
		return nil, 0, 0, err
	}
	arns := make(map[string]bool, len(batch))
	for _, r := range batch {
		arns[r.NativeID] = true
	}
	return arns, total, inserted, nil
}

func scanChatbotChimeWebhooks(ctx context.Context, client chatbotAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := chatbot.NewDescribeChimeWebhookConfigurationsPaginator(client, &chatbot.DescribeChimeWebhookConfigurationsInput{},
		func(o *chatbot.DescribeChimeWebhookConfigurationsPaginatorOptions) { o.Limit = chatbotPageLimit })
	return pageScan(ctx, chatbotChimeWebhooksOp, acct, region, st,
		pager.HasMorePages,
		func(ctx context.Context) (*chatbot.DescribeChimeWebhookConfigurationsOutput, error) {
			return pager.NextPage(ctx)
		},
		func(out *chatbot.DescribeChimeWebhookConfigurationsOutput) []chatbottypes.ChimeWebhookConfiguration {
			return out.WebhookConfigurations
		},
		func(c chatbottypes.ChimeWebhookConfiguration) *store.Resource {
			arn := sv(c.ChatConfigurationArn)
			if arn == "" {
				return nil
			}
			return &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeChatbotChimeWebhookConfiguration, NativeID: arn,
				Name: c.ConfigurationName, Region: regionGlobal, Status: nonEmptyPtr(sv(c.State)),
				TagsJSON: chatbotTagsJSON(c.Tags), AttributesJSON: mustJSON(c), DiscoveredBy: scanID,
			}
		})
}

// chatbotSlackUserIdentityNativeID builds a Slack user identity's id from the
// keys DeleteSlackUserIdentity takes; the API issues no ARN. The user id goes
// last because its model pattern allows "/".
func chatbotSlackUserIdentityNativeID(configARN, teamID, userID string) string {
	return configARN + "/user-identity/" + teamID + "/" + userID
}

// chatbotTeamsUserIdentityNativeID builds a Microsoft Teams user identity's id
// from the keys DeleteMicrosoftTeamsUserIdentity takes; the API issues no ARN.
func chatbotTeamsUserIdentityNativeID(configARN, userID string) string {
	return configARN + "/user-identity/" + userID
}

// scanChatbotSlackUserIdentities lists every Slack user identity in one
// account-wide listing (no ChatConfigurationArn) and links each under its
// channel configuration when channels holds it.
func scanChatbotSlackUserIdentities(ctx context.Context, client chatbotAPI, acct *account, region string, st *store.Store, scanID string, channels map[string]bool) (int, int, error) {
	pager := chatbot.NewDescribeSlackUserIdentitiesPaginator(client, &chatbot.DescribeSlackUserIdentitiesInput{},
		func(o *chatbot.DescribeSlackUserIdentitiesPaginatorOptions) { o.Limit = chatbotPageLimit })
	var children []containedChild
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			return upsertRegionChildren(st, acct, region, chatbotSlackUserIdentitiesOp, children, err)
		}
		for _, u := range out.SlackUserIdentities {
			configARN, teamID, userID := sv(u.ChatConfigurationArn), sv(u.SlackTeamId), sv(u.SlackUserId)
			if configARN == "" || teamID == "" || userID == "" {
				continue
			}
			children = append(children, chatbotIdentityChild(acct, channels, configARN, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeChatbotSlackUserIdentity, NativeID: chatbotSlackUserIdentityNativeID(configARN, teamID, userID),
				Name: &userID, Region: regionGlobal, AttributesJSON: mustJSON(u), DiscoveredBy: scanID,
			}))
		}
	}
	return upsertRegionChildren(st, acct, region, chatbotSlackUserIdentitiesOp, children, nil)
}

// scanChatbotTeamsUserIdentities lists every Microsoft Teams user identity in
// one account-wide listing (no ChatConfigurationArn) and links each under its
// channel configuration when channels holds it.
func scanChatbotTeamsUserIdentities(ctx context.Context, client chatbotAPI, acct *account, region string, st *store.Store, scanID string, channels map[string]bool) (int, int, error) {
	pager := chatbot.NewListMicrosoftTeamsUserIdentitiesPaginator(client, &chatbot.ListMicrosoftTeamsUserIdentitiesInput{},
		func(o *chatbot.ListMicrosoftTeamsUserIdentitiesPaginatorOptions) { o.Limit = chatbotPageLimit })
	var children []containedChild
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			return upsertRegionChildren(st, acct, region, chatbotTeamsUserIdentitiesOp, children, err)
		}
		for _, u := range out.TeamsUserIdentities {
			configARN, userID := sv(u.ChatConfigurationArn), sv(u.UserId)
			if configARN == "" || userID == "" {
				continue
			}
			children = append(children, chatbotIdentityChild(acct, channels, configARN, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeChatbotMicrosoftTeamsUserIdentity, NativeID: chatbotTeamsUserIdentityNativeID(configARN, userID),
				Name: &userID, Region: regionGlobal, AttributesJSON: mustJSON(u), DiscoveredBy: scanID,
			}))
		}
	}
	return upsertRegionChildren(st, acct, region, chatbotTeamsUserIdentitiesOp, children, nil)
}

// chatbotIdentityChild pairs an identity row with its channel configuration's
// ResourceID, or "" when that configuration was not stored this scan.
func chatbotIdentityChild(acct *account, channels map[string]bool, configARN string, r *store.Resource) containedChild {
	c := containedChild{row: r}
	if channels[configARN] {
		c.parentID = store.ResourceID("aws", acct.ID, configARN)
	}
	return c
}

func chatbotTagsJSON(tags []chatbottypes.Tag) *string {
	m := make(map[string]string, len(tags))
	for _, t := range tags {
		if t.TagKey != nil && t.TagValue != nil {
			m[*t.TagKey] = *t.TagValue
		}
	}
	return mapTagsJSON(m)
}

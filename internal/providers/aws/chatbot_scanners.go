package aws

import (
	"context"
	"errors"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
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
	chatbotCustomActionsOp       = "chatbot:ListCustomActions"
	chatbotSlackChannelsOp       = "chatbot:DescribeSlackChannelConfigurations"
	chatbotTeamsChannelsOp       = "chatbot:ListMicrosoftTeamsChannelConfigurations"
	chatbotChimeWebhooksOp       = "chatbot:DescribeChimeWebhookConfigurations"
	chatbotSlackUserIdentitiesOp = "chatbot:DescribeSlackUserIdentities"
	chatbotTeamsUserIdentitiesOp = "chatbot:ListMicrosoftTeamsUserIdentities"
	// chatbotPageLimit is the model's MaxResults maximum for every list op
	// this file calls.
	chatbotPageLimit = 100
)

// scanChatbot discovers Chatbot custom actions, channel configurations (Slack +
// Microsoft Teams), Chime webhook configurations, and Slack and Microsoft Teams
// user identities. Global service; its API is called in its home region
// (homeRegion), us-east-2 for aws accounts.
//
// Phases run in order. A phase's error does not stop the later phases; only a
// failed store write does, since every later write would fail too. The user
// identity phases link under the channel configurations their channel phase
// stored; an identity whose configuration that phase did not store stays
// unlinked.
func scanChatbot(ctx context.Context, acct *account, _ string, st *store.Store, scanID string) (int, int, error) {
	region, err := acct.homeRegion("chatbot")
	if err != nil {
		return 0, 0, err
	}
	client := chatbot.NewFromConfig(acct.cfg, func(o *chatbot.Options) { o.Region = region })
	return scanChatbotAll(ctx, client, acct, region, st, scanID)
}

func scanChatbotAll(ctx context.Context, client chatbotAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var slackChannels, teamsChannels map[string]bool
	phases := []struct {
		op  string
		run func() (int, int, error)
	}{
		{chatbotCustomActionsOp, func() (int, int, error) { return scanChatbotCustomActions(ctx, client, acct, region, st, scanID) }},
		{chatbotSlackChannelsOp, func() (t, i int, err error) {
			t, i, slackChannels, err = scanChatbotSlackChannels(ctx, client, acct, region, st, scanID)
			return t, i, err
		}},
		{chatbotTeamsChannelsOp, func() (t, i int, err error) {
			t, i, teamsChannels, err = scanChatbotTeamsChannels(ctx, client, acct, region, st, scanID)
			return t, i, err
		}},
		{chatbotChimeWebhooksOp, func() (int, int, error) { return scanChatbotChimeWebhooks(ctx, client, acct, region, st, scanID) }},
		{chatbotSlackUserIdentitiesOp, func() (int, int, error) {
			return scanChatbotSlackUserIdentities(ctx, client, acct, region, st, scanID, slackChannels)
		}},
		{chatbotTeamsUserIdentitiesOp, func() (int, int, error) {
			return scanChatbotTeamsUserIdentities(ctx, client, acct, region, st, scanID, teamsChannels)
		}},
	}
	var p phaseTotals
	for _, ph := range phases {
		t, i, err := ph.run()
		if p.addPhase(ph.op, t, i, err) {
			break
		}
	}
	return p.result(st, acct, region)
}

// scanChatbotCustomActions stores the custom actions ListCustomActions names.
// The op models UnauthorizedException, a code accessDeniedCodes leaves out
// because other services give it other meanings, so it is taken for a denial
// here: it ends the listing, keeping the pages read before it, and is warned
// about once the write succeeded, as storeTokenList warns about the codes
// isAccessDenied matches.
func scanChatbotCustomActions(ctx context.Context, client chatbotAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var denied error
	total, inserted, err := storeTokenList(ctx, st, acct, region, chatbotCustomActionsOp,
		func(ctx context.Context, token *string) ([]string, *string, error) {
			out, err := client.ListCustomActions(ctx, &chatbot.ListCustomActionsInput{NextToken: token, MaxResults: sdkaws.Int32(chatbotPageLimit)})
			if isAPIErrorCode(err, "UnauthorizedException") {
				denied = err
				return nil, nil, nil
			}
			if err != nil {
				return nil, nil, err
			}
			return out.CustomActions, out.NextToken, nil
		},
		func(arn string) (*store.Resource, string, string) {
			if arn == "" {
				return nil, "custom actions are an empty ARN", "(empty)"
			}
			return &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeChatbotCustomAction, NativeID: arn,
				Name: &arn, Region: regionGlobal,
				AttributesJSON: mustJSON(map[string]string{"CustomActionArn": arn}), DiscoveredBy: scanID,
			}, "", ""
		})
	if denied != nil && !errors.Is(err, store.ErrStoreWrite) {
		_ = skipIfAccessDenied(st, chatbotCustomActionsOp, acct.ID, region, denied)
	}
	return total, inserted, err
}

// chatbotConfigRow builds the row of a chat configuration (Slack, Microsoft
// Teams or Chime webhook), or names why it cannot be stored.
func chatbotConfigRow(acct *account, scanID, rtype string, arn, name, state *string, tags []chatbottypes.Tag, attrs any) (*store.Resource, string, string) {
	if sv(arn) == "" {
		return nil, "chat configurations have an empty ChatConfigurationArn", sv(name)
	}
	return &store.Resource{
		Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
		Type: rtype, NativeID: *arn, Name: name, Region: regionGlobal, Status: nonEmptyPtr(sv(state)),
		TagsJSON:       kvTagsJSON(tags, func(t chatbottypes.Tag) (*string, *string) { return t.TagKey, t.TagValue }),
		AttributesJSON: mustJSON(attrs), DiscoveredBy: scanID,
	}, "", ""
}

// chatbotStoredARNs is the set of the ARNs of the channel configurations a
// channel phase stored; storeParentList returns none when the write failed.
func chatbotStoredARNs(parents []childParent) map[string]bool {
	arns := make(map[string]bool, len(parents))
	for _, p := range parents {
		arns[p.arn] = true
	}
	return arns
}

// scanChatbotSlackChannels stores the Slack channel configurations and
// returns the ARNs of those it stored.
func scanChatbotSlackChannels(ctx context.Context, client chatbotAPI, acct *account, region string, st *store.Store, scanID string) (int, int, map[string]bool, error) {
	total, inserted, parents, err := storeParentList(ctx, st, acct, region, chatbotSlackChannelsOp, "Slack channel configurations", "ChatConfigurationArn",
		func(ctx context.Context, token *string) ([]chatbottypes.SlackChannelConfiguration, *string, error) {
			out, err := client.DescribeSlackChannelConfigurations(ctx, &chatbot.DescribeSlackChannelConfigurationsInput{NextToken: token, MaxResults: sdkaws.Int32(chatbotPageLimit)})
			if err != nil {
				return nil, nil, err
			}
			return out.SlackChannelConfigurations, out.NextToken, nil
		},
		func(c chatbottypes.SlackChannelConfiguration) (*store.Resource, string, string) {
			return chatbotConfigRow(acct, scanID, TypeChatbotSlackChannelConfiguration, c.ChatConfigurationArn, c.ConfigurationName, c.State, c.Tags, c)
		},
		func(c chatbottypes.SlackChannelConfiguration) string { return sv(c.ChatConfigurationArn) })
	return total, inserted, chatbotStoredARNs(parents), err
}

// scanChatbotTeamsChannels stores the Microsoft Teams channel configurations
// and returns the ARNs of those it stored.
func scanChatbotTeamsChannels(ctx context.Context, client chatbotAPI, acct *account, region string, st *store.Store, scanID string) (int, int, map[string]bool, error) {
	total, inserted, parents, err := storeParentList(ctx, st, acct, region, chatbotTeamsChannelsOp, "Microsoft Teams channel configurations", "ChatConfigurationArn",
		func(ctx context.Context, token *string) ([]chatbottypes.TeamsChannelConfiguration, *string, error) {
			out, err := client.ListMicrosoftTeamsChannelConfigurations(ctx, &chatbot.ListMicrosoftTeamsChannelConfigurationsInput{NextToken: token, MaxResults: sdkaws.Int32(chatbotPageLimit)})
			if err != nil {
				return nil, nil, err
			}
			return out.TeamChannelConfigurations, out.NextToken, nil
		},
		func(c chatbottypes.TeamsChannelConfiguration) (*store.Resource, string, string) {
			return chatbotConfigRow(acct, scanID, TypeChatbotMicrosoftTeamsChannelConfiguration, c.ChatConfigurationArn, c.ConfigurationName, c.State, c.Tags, c)
		},
		func(c chatbottypes.TeamsChannelConfiguration) string { return sv(c.ChatConfigurationArn) })
	return total, inserted, chatbotStoredARNs(parents), err
}

func scanChatbotChimeWebhooks(ctx context.Context, client chatbotAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	return storeTokenList(ctx, st, acct, region, chatbotChimeWebhooksOp,
		func(ctx context.Context, token *string) ([]chatbottypes.ChimeWebhookConfiguration, *string, error) {
			out, err := client.DescribeChimeWebhookConfigurations(ctx, &chatbot.DescribeChimeWebhookConfigurationsInput{NextToken: token, MaxResults: sdkaws.Int32(chatbotPageLimit)})
			if err != nil {
				return nil, nil, err
			}
			return out.WebhookConfigurations, out.NextToken, nil
		},
		func(c chatbottypes.ChimeWebhookConfiguration) (*store.Resource, string, string) {
			return chatbotConfigRow(acct, scanID, TypeChatbotChimeWebhookConfiguration, c.ChatConfigurationArn, c.ConfigurationName, c.State, c.Tags, c)
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
	users, listErr := readTokenPages(ctx, func(ctx context.Context, token *string) ([]chatbottypes.SlackUserIdentity, *string, error) {
		out, err := client.DescribeSlackUserIdentities(ctx, &chatbot.DescribeSlackUserIdentitiesInput{NextToken: token, MaxResults: sdkaws.Int32(chatbotPageLimit)})
		if err != nil {
			return nil, nil, err
		}
		return out.SlackUserIdentities, out.NextToken, nil
	})
	var (
		children []containedChild
		skips    notStoredSkips
	)
	for _, u := range users {
		configARN, teamID, userID := sv(u.ChatConfigurationArn), sv(u.SlackTeamId), sv(u.SlackUserId)
		if configARN == "" || teamID == "" || userID == "" {
			skips.skipped("Slack user identities lack a ChatConfigurationArn, SlackTeamId or SlackUserId", configARN+" "+teamID+" "+userID)
			continue
		}
		children = append(children, chatbotIdentityChild(acct, channels, configARN, &store.Resource{
			Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
			Type: TypeChatbotSlackUserIdentity, NativeID: chatbotSlackUserIdentityNativeID(configARN, teamID, userID),
			Name: &userID, Region: regionGlobal, AttributesJSON: mustJSON(u), DiscoveredBy: scanID,
		}))
	}
	return storeChatbotIdentities(st, acct, region, chatbotSlackUserIdentitiesOp, children, listErr, &skips)
}

// scanChatbotTeamsUserIdentities lists every Microsoft Teams user identity in
// one account-wide listing (no ChatConfigurationArn) and links each under its
// channel configuration when channels holds it.
func scanChatbotTeamsUserIdentities(ctx context.Context, client chatbotAPI, acct *account, region string, st *store.Store, scanID string, channels map[string]bool) (int, int, error) {
	users, listErr := readTokenPages(ctx, func(ctx context.Context, token *string) ([]chatbottypes.TeamsUserIdentity, *string, error) {
		out, err := client.ListMicrosoftTeamsUserIdentities(ctx, &chatbot.ListMicrosoftTeamsUserIdentitiesInput{NextToken: token, MaxResults: sdkaws.Int32(chatbotPageLimit)})
		if err != nil {
			return nil, nil, err
		}
		return out.TeamsUserIdentities, out.NextToken, nil
	})
	var (
		children []containedChild
		skips    notStoredSkips
	)
	for _, u := range users {
		configARN, userID := sv(u.ChatConfigurationArn), sv(u.UserId)
		if configARN == "" || userID == "" {
			skips.skipped("Microsoft Teams user identities lack a ChatConfigurationArn or UserId", configARN+" "+userID)
			continue
		}
		children = append(children, chatbotIdentityChild(acct, channels, configARN, &store.Resource{
			Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
			Type: TypeChatbotMicrosoftTeamsUserIdentity, NativeID: chatbotTeamsUserIdentityNativeID(configARN, userID),
			Name: &userID, Region: regionGlobal, AttributesJSON: mustJSON(u), DiscoveredBy: scanID,
		}))
	}
	return storeChatbotIdentities(st, acct, region, chatbotTeamsUserIdentitiesOp, children, listErr, &skips)
}

// storeChatbotIdentities stores the identities a listing read before listErr
// and links them under their channel configurations, then hands listErr and
// the write's error to storeReadRows with no rows of its own: it returns
// listErr unless denied, and warns about a denial and the skipped identities
// only when the write succeeded.
func storeChatbotIdentities(st *store.Store, acct *account, region, op string, children []containedChild, listErr error, skips *notStoredSkips) (int, int, error) {
	total, inserted, werr := upsertContained(st, op, children)
	_, _, err := storeReadRows(st, acct, region, op, nil, listErr, func() { skips.warn(st, op, acct, region) }, werr)
	return total, inserted, err
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

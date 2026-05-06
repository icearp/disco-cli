package aws

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/internal/store"
	"github.com/aws/aws-sdk-go-v2/service/chatbot"
)

func init() {
	registerService(serviceEntry{
		name:   "aws:chatbot",
		global: true,
		fn:     scanChatbot,
		emits: []coverage.TypeDecl{
			{Service: "chatbot", DiscoType: TypeChatbotCustomAction},
			{Service: "chatbot", DiscoType: TypeChatbotSlackChannelConfiguration},
			{Service: "chatbot", DiscoType: TypeChatbotMicrosoftTeamsChannelConfiguration},
		},
	})
}

type chatbotAPI interface {
	ListCustomActions(context.Context, *chatbot.ListCustomActionsInput, ...func(*chatbot.Options)) (*chatbot.ListCustomActionsOutput, error)
	DescribeSlackChannelConfigurations(context.Context, *chatbot.DescribeSlackChannelConfigurationsInput, ...func(*chatbot.Options)) (*chatbot.DescribeSlackChannelConfigurationsOutput, error)
	ListMicrosoftTeamsChannelConfigurations(context.Context, *chatbot.ListMicrosoftTeamsChannelConfigurationsInput, ...func(*chatbot.Options)) (*chatbot.ListMicrosoftTeamsChannelConfigurationsOutput, error)
}

// scanChatbot discovers Chatbot custom actions and channel configurations
// (Slack + Microsoft Teams). Service is global; gate to us-east-2 to avoid
// duplicate scans across regions.
func scanChatbot(ctx context.Context, acct *account, _ string, st *store.Store, scanID string) (total, inserted int, err error) {
	region := "us-east-2"
	client := chatbot.NewFromConfig(acct.cfg, func(o *chatbot.Options) { o.Region = region })

	for _, phase := range []func() (int, int, error){
		func() (int, int, error) { return scanChatbotCustomActions(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanChatbotSlackChannels(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanChatbotTeamsChannels(ctx, client, acct, region, st, scanID) },
	} {
		t, i, perr := phase()
		if perr != nil {
			return total, inserted, perr
		}
		total += t
		inserted += i
	}
	return total, inserted, nil
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
				Name: &a, Region: &region,
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

func scanChatbotSlackChannels(ctx context.Context, client chatbotAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var batch []*store.Resource
	var nextToken *string
	for {
		out, err := client.DescribeSlackChannelConfigurations(ctx, &chatbot.DescribeSlackChannelConfigurationsInput{NextToken: nextToken})
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "chatbot:DescribeSlackChannelConfigurations", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("chatbot:DescribeSlackChannelConfigurations: %w", err)
		}
		for _, c := range out.SlackChannelConfigurations {
			arn := sv(c.ChatConfigurationArn)
			if arn == "" {
				continue
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeChatbotSlackChannelConfiguration, NativeID: arn,
				Name: c.ConfigurationName, Region: &region,
				AttributesJSON: mustJSON(c), DiscoveredBy: scanID,
			})
		}
		if out.NextToken == nil || *out.NextToken == "" {
			break
		}
		nextToken = out.NextToken
	}
	return upsertBatch(st, batch, "chatbot slack-channel-configurations")
}

func scanChatbotTeamsChannels(ctx context.Context, client chatbotAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var batch []*store.Resource
	var nextToken *string
	for {
		out, err := client.ListMicrosoftTeamsChannelConfigurations(ctx, &chatbot.ListMicrosoftTeamsChannelConfigurationsInput{NextToken: nextToken})
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "chatbot:ListMicrosoftTeamsChannelConfigurations", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("chatbot:ListMicrosoftTeamsChannelConfigurations: %w", err)
		}
		for _, c := range out.TeamChannelConfigurations {
			arn := sv(c.ChatConfigurationArn)
			if arn == "" {
				continue
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeChatbotMicrosoftTeamsChannelConfiguration, NativeID: arn,
				Name: c.ConfigurationName, Region: &region,
				AttributesJSON: mustJSON(c), DiscoveredBy: scanID,
			})
		}
		if out.NextToken == nil || *out.NextToken == "" {
			break
		}
		nextToken = out.NextToken
	}
	return upsertBatch(st, batch, "chatbot microsoft-teams-channel-configurations")
}

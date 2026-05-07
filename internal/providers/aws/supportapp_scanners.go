package aws

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/internal/store"
	"github.com/aws/aws-sdk-go-v2/service/supportapp"
)

func init() {
	registerService(serviceEntry{
		name:   "aws:support-app",
		global: true,
		fn:     scanSupportApp,
		emits: []coverage.TypeDecl{
			{Service: "support-app", DiscoType: TypeSupportAppAccountAlias},
			{Service: "support-app", DiscoType: TypeSupportAppSlackChannelConfiguration},
			{Service: "support-app", DiscoType: TypeSupportAppSlackWorkspaceConfiguration},
		},
	})
}

type supportAppAPI interface {
	GetAccountAlias(context.Context, *supportapp.GetAccountAliasInput, ...func(*supportapp.Options)) (*supportapp.GetAccountAliasOutput, error)
	ListSlackChannelConfigurations(context.Context, *supportapp.ListSlackChannelConfigurationsInput, ...func(*supportapp.Options)) (*supportapp.ListSlackChannelConfigurationsOutput, error)
	ListSlackWorkspaceConfigurations(context.Context, *supportapp.ListSlackWorkspaceConfigurationsInput, ...func(*supportapp.Options)) (*supportapp.ListSlackWorkspaceConfigurationsOutput, error)
}

// scanSupportApp discovers SupportApp account alias plus per-Slack-team
// channel/workspace configurations. Service is global with endpoints only
// in us-east-1; gate other regions.
func scanSupportApp(ctx context.Context, acct *account, _ string, st *store.Store, scanID string) (total, inserted int, err error) {
	region := "us-east-1"
	client := supportapp.NewFromConfig(acct.cfg, func(o *supportapp.Options) { o.Region = region })

	for _, phase := range []func() (int, int, error){
		func() (int, int, error) { return scanSAAccountAlias(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanSASlackChannelConfigs(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanSASlackWorkspaceConfigs(ctx, client, acct, region, st, scanID) },
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

func scanSAAccountAlias(ctx context.Context, client supportAppAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	out, err := client.GetAccountAlias(ctx, &supportapp.GetAccountAliasInput{})
	if err != nil {
		if isAccessDenied(err) {
			return 0, 0, skipIfAccessDenied(st, "supportapp:GetAccountAlias", acct.ID, region, err)
		}
		return 0, 0, fmt.Errorf("supportapp:GetAccountAlias: %w", err)
	}
	if sv(out.AccountAlias) == "" {
		return 0, 0, nil
	}
	arn := fmt.Sprintf("arn:aws:supportapp::%s:account-alias", acct.ID)
	label := sv(out.AccountAlias)
	r := &store.Resource{
		Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
		Type: TypeSupportAppAccountAlias, NativeID: arn,
		Name: &label, Region: regionGlobal,
		AttributesJSON: mustJSON(out), DiscoveredBy: scanID,
	}
	return upsertBatch(st, []*store.Resource{r}, "supportapp account-alias")
}

func scanSASlackChannelConfigs(ctx context.Context, client supportAppAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := supportapp.NewListSlackChannelConfigurationsPaginator(client, &supportapp.ListSlackChannelConfigurationsInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "supportapp:ListSlackChannelConfigurations", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("supportapp:ListSlackChannelConfigurations: %w", err)
		}
		for _, c := range out.SlackChannelConfigurations {
			ch := sv(c.ChannelId)
			team := sv(c.TeamId)
			if ch == "" || team == "" {
				continue
			}
			arn := fmt.Sprintf("arn:aws:supportapp::%s:slack-channel/%s/%s", acct.ID, team, ch)
			label := sv(c.ChannelName)
			if label == "" {
				label = ch
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeSupportAppSlackChannelConfiguration, NativeID: arn,
				Name: &label, Region: regionGlobal,
				AttributesJSON: mustJSON(c), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "supportapp slack-channel-configurations")
}

func scanSASlackWorkspaceConfigs(ctx context.Context, client supportAppAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := supportapp.NewListSlackWorkspaceConfigurationsPaginator(client, &supportapp.ListSlackWorkspaceConfigurationsInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "supportapp:ListSlackWorkspaceConfigurations", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("supportapp:ListSlackWorkspaceConfigurations: %w", err)
		}
		for _, w := range out.SlackWorkspaceConfigurations {
			team := sv(w.TeamId)
			if team == "" {
				continue
			}
			arn := fmt.Sprintf("arn:aws:supportapp::%s:slack-workspace/%s", acct.ID, team)
			label := sv(w.TeamName)
			if label == "" {
				label = team
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeSupportAppSlackWorkspaceConfiguration, NativeID: arn,
				Name: &label, Region: regionGlobal,
				AttributesJSON: mustJSON(w), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "supportapp slack-workspace-configurations")
}

package aws

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/store"
	"github.com/aws/aws-sdk-go-v2/service/configservice"
)

// scanConfigExtended discovers seven additional AWS Config resource types:
// AggregationAuthorization, ConfigurationAggregator, ConformancePack,
// OrganizationConfigRule, OrganizationConformancePack, RemediationConfiguration,
// and StoredQuery. RemediationConfigurations are fetched per Config rule (max
// 25 rule names per call); other types use account/region paginators.
func scanConfigExtended(ctx context.Context, client configserviceAPI, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	for _, phase := range []func() (int, int, error){
		func() (int, int, error) { return scanConfigAggAuthz(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanConfigAggregators(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanConfigConformancePacks(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanConfigOrgConfigRules(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanConfigOrgConformancePacks(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanConfigRemediationConfigs(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanConfigStoredQueries(ctx, client, acct, region, st, scanID) },
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

func scanConfigAggAuthz(ctx context.Context, client configserviceAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := configservice.NewDescribeAggregationAuthorizationsPaginator(client, &configservice.DescribeAggregationAuthorizationsInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "config:DescribeAggregationAuthorizations", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("config:DescribeAggregationAuthorizations: %w", err)
		}
		for _, a := range out.AggregationAuthorizations {
			arn := sv(a.AggregationAuthorizationArn)
			if arn == "" {
				continue
			}
			label := fmt.Sprintf("%s/%s", sv(a.AuthorizedAccountId), sv(a.AuthorizedAwsRegion))
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeConfigAggregationAuthorization, NativeID: arn,
				Name: &label, Region: &region,
				AttributesJSON: mustJSON(a), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "config aggregation-authorizations")
}

func scanConfigAggregators(ctx context.Context, client configserviceAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := configservice.NewDescribeConfigurationAggregatorsPaginator(client, &configservice.DescribeConfigurationAggregatorsInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "config:DescribeConfigurationAggregators", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("config:DescribeConfigurationAggregators: %w", err)
		}
		for _, a := range out.ConfigurationAggregators {
			arn := sv(a.ConfigurationAggregatorArn)
			if arn == "" {
				continue
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeConfigConfigurationAggregator, NativeID: arn,
				Name: a.ConfigurationAggregatorName, Region: &region,
				AttributesJSON: mustJSON(a), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "config configuration-aggregators")
}

func scanConfigConformancePacks(ctx context.Context, client configserviceAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := configservice.NewDescribeConformancePacksPaginator(client, &configservice.DescribeConformancePacksInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "config:DescribeConformancePacks", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("config:DescribeConformancePacks: %w", err)
		}
		for _, c := range out.ConformancePackDetails {
			arn := sv(c.ConformancePackArn)
			if arn == "" {
				continue
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeConfigConformancePack, NativeID: arn,
				Name: c.ConformancePackName, Region: &region,
				AttributesJSON: mustJSON(c), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "config conformance-packs")
}

func scanConfigOrgConfigRules(ctx context.Context, client configserviceAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := configservice.NewDescribeOrganizationConfigRulesPaginator(client, &configservice.DescribeOrganizationConfigRulesInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) || isAPIErrorCode(err, "OrganizationAccessDeniedException") {
				return 0, 0, skipIfAccessDenied(st, "config:DescribeOrganizationConfigRules", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("config:DescribeOrganizationConfigRules: %w", err)
		}
		for _, r := range out.OrganizationConfigRules {
			arn := sv(r.OrganizationConfigRuleArn)
			if arn == "" {
				continue
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeConfigOrganizationConfigRule, NativeID: arn,
				Name: r.OrganizationConfigRuleName, Region: &region,
				AttributesJSON: mustJSON(r), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "config organization-config-rules")
}

func scanConfigOrgConformancePacks(ctx context.Context, client configserviceAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := configservice.NewDescribeOrganizationConformancePacksPaginator(client, &configservice.DescribeOrganizationConformancePacksInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) || isAPIErrorCode(err, "OrganizationAccessDeniedException") {
				return 0, 0, skipIfAccessDenied(st, "config:DescribeOrganizationConformancePacks", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("config:DescribeOrganizationConformancePacks: %w", err)
		}
		for _, p := range out.OrganizationConformancePacks {
			arn := sv(p.OrganizationConformancePackArn)
			if arn == "" {
				continue
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeConfigOrganizationConformancePack, NativeID: arn,
				Name: p.OrganizationConformancePackName, Region: &region,
				AttributesJSON: mustJSON(p), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "config organization-conformance-packs")
}

// scanConfigRemediationConfigs fetches remediation configs per Config rule.
// DescribeRemediationConfigurations takes ConfigRuleNames (max 25 per call)
// and has no paginator. Re-lists rules via DescribeConfigRules paginator
// rather than threading rule names through scanConfigAll.
func scanConfigRemediationConfigs(ctx context.Context, client configserviceAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var ruleNames []string
	pager := configservice.NewDescribeConfigRulesPaginator(client, &configservice.DescribeConfigRulesInput{})
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "config:DescribeConfigRules(remediation)", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("config:DescribeConfigRules(remediation): %w", err)
		}
		for _, r := range out.ConfigRules {
			if n := sv(r.ConfigRuleName); n != "" {
				ruleNames = append(ruleNames, n)
			}
		}
	}
	if len(ruleNames) == 0 {
		return 0, 0, nil
	}
	const batchSize = 25
	var batch []*store.Resource
	for i := 0; i < len(ruleNames); i += batchSize {
		end := i + batchSize
		if end > len(ruleNames) {
			end = len(ruleNames)
		}
		out, err := client.DescribeRemediationConfigurations(ctx, &configservice.DescribeRemediationConfigurationsInput{
			ConfigRuleNames: ruleNames[i:end],
		})
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "config:DescribeRemediationConfigurations", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("config:DescribeRemediationConfigurations: %w", err)
		}
		for _, r := range out.RemediationConfigurations {
			ruleName := sv(r.ConfigRuleName)
			if ruleName == "" {
				continue
			}
			arn := sv(r.Arn)
			if arn == "" {
				// Synth: parent rule ARN + /remediation. ConfigRuleArn isn't
				// in the remediation struct, so use the standard rule ARN form.
				arn = fmt.Sprintf("arn:aws:config:%s:%s:config-rule/%s/remediation", region, acct.ID, ruleName)
			}
			label := ruleName
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeConfigRemediationConfiguration, NativeID: arn,
				Name: &label, Region: &region,
				AttributesJSON: mustJSON(r), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "config remediation-configurations")
}

func scanConfigStoredQueries(ctx context.Context, client configserviceAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := configservice.NewListStoredQueriesPaginator(client, &configservice.ListStoredQueriesInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "config:ListStoredQueries", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("config:ListStoredQueries: %w", err)
		}
		for _, q := range out.StoredQueryMetadata {
			arn := sv(q.QueryArn)
			if arn == "" {
				continue
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeConfigStoredQuery, NativeID: arn,
				Name: q.QueryName, Region: &region,
				AttributesJSON: mustJSON(q), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "config stored-queries")
}

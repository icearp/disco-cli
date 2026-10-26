package aws

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/service/configservice"
	"github.com/icearp/disco-cli/store"
)

// scanConfigExtended discovers the AWS Config types beyond recorders, delivery
// channels and rules. Remediation configurations and exceptions fetch per
// Config rule; the rest use account/region list calls.
//
// The retention-configuration, remediation-exception and connector phases run
// last so an error in one cannot cost the earlier phases their rows.
func scanConfigExtended(ctx context.Context, client configserviceAPI, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	var rules []childParent
	for _, phase := range []func() (int, int, error){
		func() (int, int, error) { return scanConfigAggAuthz(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanConfigAggregators(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanConfigConformancePacks(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanConfigOrgConfigRules(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanConfigOrgConformancePacks(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) {
			var lerr error
			if rules, lerr = listConfigRules(ctx, client, acct, region, st); lerr != nil {
				return 0, 0, lerr
			}
			return scanConfigRemediationConfigs(ctx, client, rules, acct, region, st, scanID)
		},
		func() (int, int, error) { return scanConfigStoredQueries(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) {
			return scanConfigRetentionConfigurations(ctx, client, acct, region, st, scanID)
		},
		func() (int, int, error) {
			return scanConfigRemediationExceptions(ctx, client, rules, acct, region, st, scanID)
		},
		// Connectors go last on an assumption not verified live: as the newest
		// feature, they may be missing in some regions with an unmodelled
		// error, and last is where such an error costs the least.
		func() (int, int, error) { return scanConfigConnectors(ctx, client, acct, region, st, scanID) },
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

// listConfigRules lists the region's Config rules for the per-rule phases. It
// re-lists rather than threading the list out of scanConfigAll. A rule without
// an ARN is dropped, as scanConfigAll drops it: it has no stored row to
// contain children.
func listConfigRules(ctx context.Context, client configserviceAPI, acct *account, region string, st *store.Store) ([]childParent, error) {
	var rules []childParent
	pager := configservice.NewDescribeConfigRulesPaginator(client, &configservice.DescribeConfigRulesInput{})
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return nil, skipIfAccessDenied(st, "config:DescribeConfigRules(remediation)", acct.ID, region, err)
			}
			return nil, fmt.Errorf("config:DescribeConfigRules(remediation): %w", err)
		}
		for _, r := range out.ConfigRules {
			name, arn := sv(r.ConfigRuleName), sv(r.ConfigRuleArn)
			if name == "" || arn == "" {
				continue
			}
			rules = append(rules, childParent{id: name, arn: arn})
		}
	}
	return rules, nil
}

// scanConfigRemediationConfigs fetches remediation configs per Config rule.
// DescribeRemediationConfigurations takes ConfigRuleNames (max 25/call), no
// paginator.
func scanConfigRemediationConfigs(ctx context.Context, client configserviceAPI, rules []childParent, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	if len(rules) == 0 {
		return 0, 0, nil
	}
	ruleNames := make([]string, len(rules))
	for i, r := range rules {
		ruleNames[i] = r.id
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
				// Synth: parent rule ARN + /remediation — ConfigRuleArn isn't
				// in the remediation struct.
				arn = arnFor(acct.partition(), "config", region, acct.ID, "config-rule/"+ruleName+"/remediation")
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

// scanConfigRetentionConfigurations stores the region's retention
// configuration. The API issues no ARN, so the id is synthesised from the name.
func scanConfigRetentionConfigurations(ctx context.Context, client configserviceAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := configservice.NewDescribeRetentionConfigurationsPaginator(client, &configservice.DescribeRetentionConfigurationsInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "config:DescribeRetentionConfigurations", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("config:DescribeRetentionConfigurations: %w", err)
		}
		for _, rc := range out.RetentionConfigurations {
			name := sv(rc.Name)
			if name == "" {
				continue
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type:     TypeConfigRetentionConfiguration,
				NativeID: arnFor(acct.partition(), "config", region, acct.ID, "retention-configuration/"+name),
				Name:     rc.Name, Region: &region,
				AttributesJSON: mustJSON(rc), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "config retention-configurations")
}

// scanConfigConnectors lists connector ARNs, then gets each: only
// GetConnector carries the connector configuration.
func scanConfigConnectors(ctx context.Context, client configserviceAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var arns []string
	pager := configservice.NewListConnectorsPaginator(client, &configservice.ListConnectorsInput{})
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "config:ListConnectors", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("config:ListConnectors: %w", err)
		}
		for _, c := range out.ConnectorSummaries {
			if arn := sv(c.Arn); arn != "" {
				arns = append(arns, arn)
			}
		}
	}
	// ResourceNotFoundException is GetConnector's modeled code for a connector
	// deleted after ListConnectors returned it.
	isGone := func(err error) bool { return isAPIErrorCode(err, "ResourceNotFoundException") }
	rows, err := describeFanOut(ctx, st, acct, region, "config:GetConnector", arns, isGone,
		func(gctx context.Context, arn string) (*store.Resource, error) {
			out, gerr := client.GetConnector(gctx, &configservice.GetConnectorInput{Arn: &arn})
			if gerr != nil {
				return nil, gerr
			}
			c := out.Connector
			if c == nil || sv(c.Arn) == "" {
				return nil, nil
			}
			return &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeConfigConnector, NativeID: *c.Arn,
				Name: c.Name, Region: &region, CreatedAt: tp(c.CreatedTime),
				AttributesJSON: mustJSON(c), DiscoveredBy: scanID,
			}, nil
		})
	if err != nil {
		return 0, 0, err
	}
	return upsertBatch(st, rows, "config connectors")
}

// scanConfigRemediationExceptions lists each rule's remediation exceptions
// under the rule. An exception has no ARN; the rule ARN plus the exempted
// resource's type and id identify it.
//
// No error is skipped silently: the op models no not-found code, and what it
// returns for a rule deleted after listConfigRules is not verified live, so
// such an error fails this phase.
func scanConfigRemediationExceptions(ctx context.Context, client configserviceAPI, rules []childParent, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	return childFanOut(ctx, st, acct, region, "config:DescribeRemediationExceptions", rules,
		func(error) bool { return false },
		func(gctx context.Context, rule childParent) ([]*store.Resource, error) {
			var rows []*store.Resource
			pager := configservice.NewDescribeRemediationExceptionsPaginator(client, &configservice.DescribeRemediationExceptionsInput{ConfigRuleName: &rule.id})
			for pager.HasMorePages() {
				out, err := pager.NextPage(gctx)
				if err != nil {
					return nil, err
				}
				for _, e := range out.RemediationExceptions {
					rtype, rid := sv(e.ResourceType), sv(e.ResourceId)
					if rtype == "" || rid == "" {
						continue
					}
					rows = append(rows, &store.Resource{
						Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
						Type:     TypeConfigRemediationException,
						NativeID: fmt.Sprintf("%s/remediation-exception/%s/%s", rule.arn, rtype, rid),
						Name:     e.ResourceId, Region: &region,
						AttributesJSON: mustJSON(e), DiscoveredBy: scanID,
					})
				}
			}
			return rows, nil
		})
}

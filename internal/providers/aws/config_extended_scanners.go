package aws

import (
	"cmp"
	"context"
	"fmt"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/configservice"
	cfgtypes "github.com/aws/aws-sdk-go-v2/service/configservice/types"
	"github.com/icearp/disco-cli/store"
)

func scanConfigAggAuthz(ctx context.Context, client configserviceAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) bool {
	return storeTokenListTags(ctx, st, acct, region, "config:DescribeAggregationAuthorizations",
		func(ctx context.Context, token *string) ([]cfgtypes.AggregationAuthorization, *string, error) {
			out, err := client.DescribeAggregationAuthorizations(ctx, &configservice.DescribeAggregationAuthorizationsInput{Limit: configPageCap, NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.AggregationAuthorizations, out.NextToken, nil
		},
		func(a cfgtypes.AggregationAuthorization) (*store.Resource, string, string) {
			label := fmt.Sprintf("%s/%s", sv(a.AuthorizedAccountId), sv(a.AuthorizedAwsRegion))
			return configRow(acct, region, scanID, TypeConfigAggregationAuthorization, "aggregation authorizations have no AggregationAuthorizationArn", sv(a.AggregationAuthorizationArn), label, &label, a)
		}, tags, p)
}

func scanConfigAggregators(ctx context.Context, client configserviceAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) bool {
	return storeTokenListTags(ctx, st, acct, region, "config:DescribeConfigurationAggregators",
		func(ctx context.Context, token *string) ([]cfgtypes.ConfigurationAggregator, *string, error) {
			out, err := client.DescribeConfigurationAggregators(ctx, &configservice.DescribeConfigurationAggregatorsInput{Limit: configPageCap, NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.ConfigurationAggregators, out.NextToken, nil
		},
		func(a cfgtypes.ConfigurationAggregator) (*store.Resource, string, string) {
			return configRow(acct, region, scanID, TypeConfigConfigurationAggregator, "configuration aggregators have no ConfigurationAggregatorArn", sv(a.ConfigurationAggregatorArn), sv(a.ConfigurationAggregatorName), a.ConfigurationAggregatorName, a)
		}, tags, p)
}

// scanConfigConformancePacks stores the conformance packs with their tags; a
// service-linked pack's ValidationException is configServiceLinked's.
func scanConfigConformancePacks(ctx context.Context, client configserviceAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) bool {
	linked := newConfigServiceLinked("conformance pack(s)")
	failed := storeTokenListTags(ctx, st, acct, region, "config:DescribeConformancePacks",
		func(ctx context.Context, token *string) ([]cfgtypes.ConformancePackDetail, *string, error) {
			out, err := client.DescribeConformancePacks(ctx, &configservice.DescribeConformancePacksInput{Limit: configConformancePackPageCap, NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.ConformancePackDetails, out.NextToken, nil
		},
		func(c cfgtypes.ConformancePackDetail) (*store.Resource, string, string) {
			r, why, key := configRow(acct, region, scanID, TypeConfigConformancePack, "conformance packs have no ConformancePackArn", sv(c.ConformancePackArn), sv(c.ConformancePackName), c.ConformancePackName, c)
			if r != nil {
				linked.add(r, c.CreatedBy)
			}
			return r, why, key
		}, linked.reader(tags), p)
	if !failed {
		linked.warn(st, acct, region)
	}
	return failed
}

// isConfigNotOrgAdmin reports OrganizationAccessDeniedException, which the
// model's doc says the organization rule and conformance pack APIs throw
// when called from a member account: its expected state, not a denial an
// IAM change could fix, so on a listing's first page it reads as empty
// (firstPageGapPages). What a standalone account gets is not modeled, and
// neither answer is verified live. An IAM denial (isAccessDenied) still
// warns.
func isConfigNotOrgAdmin(err error) bool {
	return isAPIErrorCode(err, "OrganizationAccessDeniedException")
}

func scanConfigOrgConfigRules(ctx context.Context, client configserviceAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) bool {
	return storeTokenListTags(ctx, st, acct, region, "config:DescribeOrganizationConfigRules",
		firstPageGapPages(isConfigNotOrgAdmin, func(ctx context.Context, token *string) ([]cfgtypes.OrganizationConfigRule, *string, error) {
			out, err := client.DescribeOrganizationConfigRules(ctx, &configservice.DescribeOrganizationConfigRulesInput{Limit: configPageCap, NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.OrganizationConfigRules, out.NextToken, nil
		}),
		func(r cfgtypes.OrganizationConfigRule) (*store.Resource, string, string) {
			return configRow(acct, region, scanID, TypeConfigOrganizationConfigRule, "organization config rules have no OrganizationConfigRuleArn", sv(r.OrganizationConfigRuleArn), sv(r.OrganizationConfigRuleName), r.OrganizationConfigRuleName, r)
		}, tags, p)
}

func scanConfigOrgConformancePacks(ctx context.Context, client configserviceAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) bool {
	return storeTokenListTags(ctx, st, acct, region, "config:DescribeOrganizationConformancePacks",
		firstPageGapPages(isConfigNotOrgAdmin, func(ctx context.Context, token *string) ([]cfgtypes.OrganizationConformancePack, *string, error) {
			out, err := client.DescribeOrganizationConformancePacks(ctx, &configservice.DescribeOrganizationConformancePacksInput{Limit: configPageCap, NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.OrganizationConformancePacks, out.NextToken, nil
		}),
		func(c cfgtypes.OrganizationConformancePack) (*store.Resource, string, string) {
			return configRow(acct, region, scanID, TypeConfigOrganizationConformancePack, "organization conformance packs have no OrganizationConformancePackArn", sv(c.OrganizationConformancePackArn), sv(c.OrganizationConformancePackName), c.OrganizationConformancePackName, c)
		}, tags, p)
}

// scanConfigRemediationConfigs fetches the remediation configs of the rules
// the rule phase stored. DescribeRemediationConfigurations takes
// ConfigRuleNames (max 25/call), no paginator.
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

func scanConfigStoredQueries(ctx context.Context, client configserviceAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) bool {
	return storeTokenListTags(ctx, st, acct, region, "config:ListStoredQueries",
		func(ctx context.Context, token *string) ([]cfgtypes.StoredQueryMetadata, *string, error) {
			out, err := client.ListStoredQueries(ctx, &configservice.ListStoredQueriesInput{MaxResults: sdkaws.Int32(configPageCap), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.StoredQueryMetadata, out.NextToken, nil
		},
		func(q cfgtypes.StoredQueryMetadata) (*store.Resource, string, string) {
			return configRow(acct, region, scanID, TypeConfigStoredQuery, "stored queries have no QueryArn", sv(q.QueryArn), sv(q.QueryName), q.QueryName, q)
		}, tags, p)
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

// scanConfigConnectors stores the region's connectors, each as its
// GetConnector body (only it carries the connector configuration) with its
// tags. A connector GetConnector fails for, other than not found, is stored
// as its ListConnectors element (readRowDetails).
func scanConfigConnectors(ctx context.Context, client configserviceAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) bool {
	const op = "config:ListConnectors"
	d := &detailedRows{getOp: "config:GetConnector", what: "connector(s)", notFound: configNotFoundCodes, tags: tags}
	total, inserted, err := storeTokenListWith(ctx, st, acct, region, op,
		func(ctx context.Context, token *string) ([]cfgtypes.ConnectorSummary, *string, error) {
			out, err := client.ListConnectors(ctx, &configservice.ListConnectorsInput{MaxResults: configPageCap, NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.ConnectorSummaries, out.NextToken, nil
		},
		func(c cfgtypes.ConnectorSummary) (*store.Resource, string, string) {
			r, why, key := configRow(acct, region, scanID, TypeConfigConnector, "connectors have no Arn", sv(c.Arn), cmp.Or(sv(c.Name), sv(c.TenantIdentifier)), c.Name, c)
			if r != nil {
				r.CreatedAt = tp(c.CreatedTime)
			}
			return r, why, key
		},
		d.before(ctx, st, acct, region, func(ctx context.Context, r *store.Resource) (any, error) {
			out, err := client.GetConnector(ctx, &configservice.GetConnectorInput{Arn: sdkaws.String(r.NativeID)})
			if err != nil || out.Connector == nil {
				return nil, err
			}
			return out.Connector, nil
		}))
	return d.addPhase(p, op, total, inserted, err)
}

// scanConfigRemediationExceptions lists each rule's remediation exceptions
// under the rule. An exception has no ARN; the rule ARN plus the exempted
// resource's type and id identify it.
//
// No error is skipped silently: the op models no not-found code, and what it
// returns for a rule deleted after the rule phase listed it is not verified
// live, so such an error fails this phase.
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

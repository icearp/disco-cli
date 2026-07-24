package aws

import (
	"context"
	"fmt"

	"github.com/icearp/disco-cli/store"
	"github.com/aws/aws-sdk-go-v2/service/securityhub"
)

// scanSecurityHubExtended adds 11 disco types: V2 hub config, automation
// rules, configuration policies, finding aggregators, security controls, and
// standards. Phases tolerate isAccessDenied + isSecurityHubNotEnabled +
// ResourceNotFoundException.
func scanSecurityHubExtended(ctx context.Context, client securityhubAPI, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	for _, phase := range []func() (int, int, error){
		func() (int, int, error) { return scanSHHubV2(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanSHOrgConfig(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanSHAggregatorsV2(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanSHAutomationRules(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanSHAutomationRulesV2(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanSHConfigPolicies(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanSHConnectorsV2(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanSHFindingAggregators(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanSHPolicyAssociations(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanSHSecurityControls(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanSHStandards(ctx, client, acct, region, st, scanID) },
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

func shSoftSkip(err error) bool {
	return isAccessDenied(err) || isSecurityHubNotEnabled(err)
}

func scanSHHubV2(ctx context.Context, client securityhubAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	out, err := client.DescribeSecurityHubV2(ctx, &securityhub.DescribeSecurityHubV2Input{})
	if err != nil {
		if shSoftSkip(err) {
			return 0, 0, nil
		}
		return 0, 0, fmt.Errorf("securityhub:DescribeSecurityHubV2: %w", err)
	}
	arn := sv(out.HubV2Arn)
	if arn == "" {
		return 0, 0, nil
	}
	name := "hub-v2"
	r := &store.Resource{
		Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
		Type: TypeSecurityHubHubV2, NativeID: arn,
		Name: &name, Region: &region, AttributesJSON: mustJSON(out), DiscoveredBy: scanID,
	}
	n, uerr := st.UpsertResources([]*store.Resource{r})
	if uerr != nil {
		return 0, 0, fmt.Errorf("upsert securityhub hub-v2: %w", uerr)
	}
	return 1, n, nil
}

// scanSHOrgConfig — only org-management account returns valid data;
// member accounts get InvalidAccessException (treated as soft-skip).
func scanSHOrgConfig(ctx context.Context, client securityhubAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	out, err := client.DescribeOrganizationConfiguration(ctx, &securityhub.DescribeOrganizationConfigurationInput{})
	if err != nil {
		if shSoftSkip(err) {
			return 0, 0, nil
		}
		return 0, 0, fmt.Errorf("securityhub:DescribeOrganizationConfiguration: %w", err)
	}
	arn := fmt.Sprintf("arn:aws:securityhub:%s:%s:organization-configuration", region, acct.ID)
	name := "organization-configuration"
	r := &store.Resource{
		Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
		Type: TypeSecurityHubOrganizationConfiguration, NativeID: arn,
		Name: &name, Region: &region, AttributesJSON: mustJSON(out), DiscoveredBy: scanID,
	}
	n, uerr := st.UpsertResources([]*store.Resource{r})
	if uerr != nil {
		return 0, 0, fmt.Errorf("upsert securityhub organization-configuration: %w", uerr)
	}
	return 1, n, nil
}

func scanSHAggregatorsV2(ctx context.Context, client securityhubAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := securityhub.NewListAggregatorsV2Paginator(client, &securityhub.ListAggregatorsV2Input{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if shSoftSkip(perr) {
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("securityhub:ListAggregatorsV2: %w", perr)
		}
		for _, a := range out.AggregatorsV2 {
			arn := sv(a.AggregatorV2Arn)
			if arn == "" {
				continue
			}
			label := arn
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeSecurityHubAggregatorV2, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(a), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "securityhub aggregators-v2")
}

func scanSHAutomationRules(ctx context.Context, client securityhubAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var batch []*store.Resource
	var token *string
	for {
		out, err := client.ListAutomationRules(ctx, &securityhub.ListAutomationRulesInput{NextToken: token})
		if err != nil {
			if shSoftSkip(err) {
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("securityhub:ListAutomationRules: %w", err)
		}
		for _, r := range out.AutomationRulesMetadata {
			arn := sv(r.RuleArn)
			if arn == "" {
				continue
			}
			label := sv(r.RuleName)
			if label == "" {
				label = arn
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeSecurityHubAutomationRule, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(r), DiscoveredBy: scanID,
			})
		}
		if out.NextToken == nil || *out.NextToken == "" {
			break
		}
		token = out.NextToken
	}
	return upsertBatch(st, batch, "securityhub automation-rules")
}

func scanSHAutomationRulesV2(ctx context.Context, client securityhubAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var batch []*store.Resource
	var token *string
	for {
		out, err := client.ListAutomationRulesV2(ctx, &securityhub.ListAutomationRulesV2Input{NextToken: token})
		if err != nil {
			if shSoftSkip(err) {
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("securityhub:ListAutomationRulesV2: %w", err)
		}
		for _, r := range out.Rules {
			arn := sv(r.RuleArn)
			if arn == "" {
				continue
			}
			label := sv(r.RuleName)
			if label == "" {
				label = sv(r.RuleId)
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeSecurityHubAutomationRuleV2, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(r), DiscoveredBy: scanID,
			})
		}
		if out.NextToken == nil || *out.NextToken == "" {
			break
		}
		token = out.NextToken
	}
	return upsertBatch(st, batch, "securityhub automation-rules-v2")
}

func scanSHConfigPolicies(ctx context.Context, client securityhubAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := securityhub.NewListConfigurationPoliciesPaginator(client, &securityhub.ListConfigurationPoliciesInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if shSoftSkip(perr) {
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("securityhub:ListConfigurationPolicies: %w", perr)
		}
		for _, p := range out.ConfigurationPolicySummaries {
			arn := sv(p.Arn)
			if arn == "" {
				continue
			}
			label := sv(p.Name)
			if label == "" {
				label = sv(p.Id)
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeSecurityHubConfigurationPolicy, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(p), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "securityhub configuration-policies")
}

func scanSHConnectorsV2(ctx context.Context, client securityhubAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var batch []*store.Resource
	var token *string
	for {
		out, err := client.ListConnectorsV2(ctx, &securityhub.ListConnectorsV2Input{NextToken: token})
		if err != nil {
			if shSoftSkip(err) {
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("securityhub:ListConnectorsV2: %w", err)
		}
		for _, c := range out.Connectors {
			arn := sv(c.ConnectorArn)
			if arn == "" {
				continue
			}
			label := sv(c.Name)
			if label == "" {
				label = sv(c.ConnectorId)
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeSecurityHubConnectorV2, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(c), DiscoveredBy: scanID,
			})
		}
		if out.NextToken == nil || *out.NextToken == "" {
			break
		}
		token = out.NextToken
	}
	return upsertBatch(st, batch, "securityhub connectors-v2")
}

func scanSHFindingAggregators(ctx context.Context, client securityhubAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := securityhub.NewListFindingAggregatorsPaginator(client, &securityhub.ListFindingAggregatorsInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if shSoftSkip(perr) {
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("securityhub:ListFindingAggregators: %w", perr)
		}
		for _, f := range out.FindingAggregators {
			arn := sv(f.FindingAggregatorArn)
			if arn == "" {
				continue
			}
			label := arn
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeSecurityHubFindingAggregator, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(f), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "securityhub finding-aggregators")
}

// scanSHPolicyAssociations — TargetId/TargetType identify accounts/OUs;
// no native ARN. Synth from (configurationPolicyId, targetType, targetId).
func scanSHPolicyAssociations(ctx context.Context, client securityhubAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := securityhub.NewListConfigurationPolicyAssociationsPaginator(client, &securityhub.ListConfigurationPolicyAssociationsInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if shSoftSkip(perr) {
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("securityhub:ListConfigurationPolicyAssociations: %w", perr)
		}
		for _, a := range out.ConfigurationPolicyAssociationSummaries {
			policy := sv(a.ConfigurationPolicyId)
			target := sv(a.TargetId)
			if policy == "" || target == "" {
				continue
			}
			arn := fmt.Sprintf("arn:aws:securityhub:%s:%s:policy-association/%s/%s/%s", region, acct.ID, policy, string(a.TargetType), target)
			label := string(a.TargetType) + ":" + target
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeSecurityHubPolicyAssociation, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(a), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "securityhub policy-associations")
}

// scanSHSecurityControls — control catalogue is AWS-managed. Mark
// ManagedByProvider=true so the entries hide from default list / graph.
func scanSHSecurityControls(ctx context.Context, client securityhubAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := securityhub.NewListSecurityControlDefinitionsPaginator(client, &securityhub.ListSecurityControlDefinitionsInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if shSoftSkip(perr) {
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("securityhub:ListSecurityControlDefinitions: %w", perr)
		}
		for _, c := range out.SecurityControlDefinitions {
			id := sv(c.SecurityControlId)
			if id == "" {
				continue
			}
			arn := fmt.Sprintf("arn:aws:securityhub:%s::security-control/%s", region, id)
			label := id
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeSecurityHubSecurityControl, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(c), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "securityhub security-controls")
}

// scanSHStandards — DescribeStandards returns the AWS-managed standards
// catalogue. Mark ManagedByProvider=true.
func scanSHStandards(ctx context.Context, client securityhubAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := securityhub.NewDescribeStandardsPaginator(client, &securityhub.DescribeStandardsInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if shSoftSkip(perr) {
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("securityhub:DescribeStandards: %w", perr)
		}
		for _, s := range out.Standards {
			arn := sv(s.StandardsArn)
			if arn == "" {
				continue
			}
			label := sv(s.Name)
			if label == "" {
				label = arn
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeSecurityHubStandard, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(s), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "securityhub standards")
}

package aws

import (
	"cmp"
	"context"
	"fmt"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	awsarn "github.com/aws/aws-sdk-go-v2/aws/arn"
	"github.com/aws/aws-sdk-go-v2/service/securityhub"
	securityhubtypes "github.com/aws/aws-sdk-go-v2/service/securityhub/types"
	"github.com/icearp/disco-cli/store"
)

// shPageLimit is the MaxResults cap the model sets for each tagged listing.
const shPageLimit = 100

// shExtendedPhases returns the phases of the 11 extended types: V2 hub
// config, organization configuration, aggregators, automation rules (V1 and
// V2), configuration policies, connectors, finding aggregators, policy
// associations, security controls, and standards, for scanSecurityHub to run
// in this order. The listings without tag reads tolerate isAccessDenied and
// isSecurityHubNotEnabled; the tagged ones read their rows' tags through tags
// and write a page at a time (storeTokenPagesTags). The security-control
// phase sets *controls to the controls it stored, for
// scanSecurityHubControlAssociations.
func shExtendedPhases(ctx context.Context, client securityhubAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, controls *[]childParent) []hostPhase {
	return []hostPhase{
		{op: "securityhub:DescribeSecurityHubV2", run: func(p *phaseTotals) bool {
			return scanSHHubV2(ctx, client, acct, region, st, scanID, tags, p)
		}},
		countedPhase(0, "securityhub:DescribeOrganizationConfiguration", func() (int, error) {
			return scanSHOrgConfig(ctx, client, acct, region, st, scanID)
		}),
		{op: "securityhub:ListAggregatorsV2", run: func(p *phaseTotals) bool {
			return scanSHAggregatorsV2(ctx, client, acct, region, st, scanID, tags, p)
		}},
		{op: "securityhub:ListAutomationRules", run: func(p *phaseTotals) bool {
			return scanSHAutomationRules(ctx, client, acct, region, st, scanID, tags, p)
		}},
		{op: "securityhub:ListAutomationRulesV2", run: func(p *phaseTotals) bool {
			return scanSHAutomationRulesV2(ctx, client, acct, region, st, scanID, tags, p)
		}},
		{op: "securityhub:ListConfigurationPolicies", run: func(p *phaseTotals) bool {
			return scanSHConfigPolicies(ctx, client, acct, region, st, scanID, tags, p)
		}},
		{op: "securityhub:ListConnectorsV2", run: func(p *phaseTotals) bool {
			return scanSHConnectorsV2(ctx, client, acct, region, st, scanID, tags, p)
		}},
		countedPhase(0, "securityhub:ListFindingAggregators", func() (int, error) {
			return scanSHFindingAggregators(ctx, client, acct, region, st, scanID)
		}),
		countedPhase(0, "securityhub:ListConfigurationPolicyAssociations", func() (int, error) {
			return scanSHPolicyAssociations(ctx, client, acct, region, st, scanID)
		}),
		countedPhase(0, "securityhub:ListSecurityControlDefinitions", func() (int, error) {
			t, stored, err := scanSHSecurityControls(ctx, client, acct, region, st, scanID)
			*controls = stored
			return t, err
		}),
		countedPhase(0, "securityhub:DescribeStandards", func() (int, error) {
			return scanSHStandards(ctx, client, acct, region, st, scanID)
		}),
	}
}

func shSoftSkip(err error) bool {
	return isAccessDenied(err) || isSecurityHubNotEnabled(err)
}

// scanSHHubV2 stores the region's Security Hub V2 hub. V2 not enabled
// (isSecurityHubNotEnabled) stores nothing, silently; a denial warns.
func scanSHHubV2(ctx context.Context, client securityhubAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) bool {
	const op = "securityhub:DescribeSecurityHubV2"
	out, err := client.DescribeSecurityHubV2(ctx, &securityhub.DescribeSecurityHubV2Input{})
	if isSecurityHubNotEnabled(err) {
		return false
	}
	var (
		rows  []*store.Resource
		skips notStoredSkips
	)
	if err == nil {
		name := "hub-v2"
		r, why, key := arnRow("Security Hub V2 hubs", out.HubV2Arn, &name, &store.Resource{
			Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
			Type: TypeSecurityHubHubV2, NativeID: sv(out.HubV2Arn),
			Name: &name, Region: &region, AttributesJSON: mustJSON(out), DiscoveredBy: scanID,
		})
		if r == nil {
			skips.skipped(why, key)
		} else {
			rows = append(rows, r)
		}
	}
	return storeRowsTags(ctx, st, acct, region, op, rows, err, func() { skips.warn(st, op, acct, region) }, tags, p)
}

// scanSHOrgConfig — only org-management account returns valid data;
// member accounts get InvalidAccessException (treated as soft-skip).
func scanSHOrgConfig(ctx context.Context, client securityhubAPI, acct *account, region string, st *store.Store, scanID string) (int, error) {
	out, err := client.DescribeOrganizationConfiguration(ctx, &securityhub.DescribeOrganizationConfigurationInput{})
	if err != nil {
		if shSoftSkip(err) {
			return 0, nil
		}
		return 0, fmt.Errorf("securityhub:DescribeOrganizationConfiguration: %w", err)
	}
	arn := arnFor(acct.partition(), "securityhub", region, acct.ID, "organization-configuration")
	name := "organization-configuration"
	r := &store.Resource{
		Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
		Type: TypeSecurityHubOrganizationConfiguration, NativeID: arn,
		Name: &name, Region: &region, AttributesJSON: mustJSON(out), DiscoveredBy: scanID,
	}
	uerr := st.UpsertResources([]*store.Resource{r})
	if uerr != nil {
		return 0, fmt.Errorf("upsert securityhub organization-configuration: %w", uerr)
	}
	return 1, nil
}

// The tagged listings below share one shape: the first page refused as not
// enabled (isSecurityHubNotEnabled) is an empty listing; the
// same answer on a later page follows a page the op served, so it is an
// error that keeps the pages read (firstPageGapPages). A denied listing warns
// once and keeps the pages read before it.

func scanSHAggregatorsV2(ctx context.Context, client securityhubAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) bool {
	return storeTokenPagesTags(ctx, st, acct, region, "securityhub:ListAggregatorsV2",
		firstPageGapPages(isSecurityHubNotEnabled, func(ctx context.Context, token *string) ([]securityhubtypes.AggregatorV2, *string, error) {
			out, err := client.ListAggregatorsV2(ctx, &securityhub.ListAggregatorsV2Input{NextToken: token, MaxResults: sdkaws.Int32(shPageLimit)})
			if err != nil {
				return nil, nil, err
			}
			return out.AggregatorsV2, out.NextToken, nil
		}),
		func(a securityhubtypes.AggregatorV2) (*store.Resource, string, string) {
			label := sv(a.AggregatorV2Arn)
			return arnRow("aggregators", a.AggregatorV2Arn, nil, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeSecurityHubAggregatorV2, NativeID: label,
				Name: &label, Region: &region, AttributesJSON: mustJSON(a), DiscoveredBy: scanID,
			})
		}, tags, p)
}

func scanSHAutomationRules(ctx context.Context, client securityhubAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) bool {
	return storeTokenPagesTags(ctx, st, acct, region, "securityhub:ListAutomationRules",
		firstPageGapPages(isSecurityHubNotEnabled, func(ctx context.Context, token *string) ([]securityhubtypes.AutomationRulesMetadata, *string, error) {
			out, err := client.ListAutomationRules(ctx, &securityhub.ListAutomationRulesInput{NextToken: token, MaxResults: sdkaws.Int32(shPageLimit)})
			if err != nil {
				return nil, nil, err
			}
			return out.AutomationRulesMetadata, out.NextToken, nil
		}),
		func(r securityhubtypes.AutomationRulesMetadata) (*store.Resource, string, string) {
			label := cmp.Or(sv(r.RuleName), sv(r.RuleArn))
			return arnRow("automation rules", r.RuleArn, r.RuleName, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeSecurityHubAutomationRule, NativeID: sv(r.RuleArn),
				Name: &label, Region: &region, AttributesJSON: mustJSON(r), DiscoveredBy: scanID,
			})
		}, tags, p)
}

func scanSHAutomationRulesV2(ctx context.Context, client securityhubAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) bool {
	return storeTokenPagesTags(ctx, st, acct, region, "securityhub:ListAutomationRulesV2",
		firstPageGapPages(isSecurityHubNotEnabled, func(ctx context.Context, token *string) ([]securityhubtypes.AutomationRulesMetadataV2, *string, error) {
			out, err := client.ListAutomationRulesV2(ctx, &securityhub.ListAutomationRulesV2Input{NextToken: token, MaxResults: sdkaws.Int32(shPageLimit)})
			if err != nil {
				return nil, nil, err
			}
			return out.Rules, out.NextToken, nil
		}),
		func(r securityhubtypes.AutomationRulesMetadataV2) (*store.Resource, string, string) {
			label := cmp.Or(sv(r.RuleName), sv(r.RuleId))
			return arnRow("V2 automation rules", r.RuleArn, &label, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeSecurityHubAutomationRuleV2, NativeID: sv(r.RuleArn),
				Name: &label, Region: &region, AttributesJSON: mustJSON(r), DiscoveredBy: scanID,
			})
		}, tags, p)
}

// scanSHConfigPolicies stores the configuration policies with one tag read
// each: no listing or Get returns their tags.
func scanSHConfigPolicies(ctx context.Context, client securityhubAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) bool {
	return storeTokenPagesTags(ctx, st, acct, region, "securityhub:ListConfigurationPolicies",
		firstPageGapPages(isSecurityHubNotEnabled, func(ctx context.Context, token *string) ([]securityhubtypes.ConfigurationPolicySummary, *string, error) {
			out, err := client.ListConfigurationPolicies(ctx, &securityhub.ListConfigurationPoliciesInput{NextToken: token, MaxResults: sdkaws.Int32(shPageLimit)})
			if err != nil {
				return nil, nil, err
			}
			return out.ConfigurationPolicySummaries, out.NextToken, nil
		}),
		func(c securityhubtypes.ConfigurationPolicySummary) (*store.Resource, string, string) {
			label := cmp.Or(sv(c.Name), sv(c.Id))
			return arnRow("configuration policies", c.Arn, &label, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeSecurityHubConfigurationPolicy, NativeID: sv(c.Arn),
				Name: &label, Region: &region, AttributesJSON: mustJSON(c), DiscoveredBy: scanID,
			})
		}, tags, p)
}

func scanSHConnectorsV2(ctx context.Context, client securityhubAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) bool {
	return storeTokenPagesTags(ctx, st, acct, region, "securityhub:ListConnectorsV2",
		firstPageGapPages(isSecurityHubNotEnabled, func(ctx context.Context, token *string) ([]securityhubtypes.ConnectorSummary, *string, error) {
			out, err := client.ListConnectorsV2(ctx, &securityhub.ListConnectorsV2Input{NextToken: token, MaxResults: sdkaws.Int32(shPageLimit)})
			if err != nil {
				return nil, nil, err
			}
			return out.Connectors, out.NextToken, nil
		}),
		func(c securityhubtypes.ConnectorSummary) (*store.Resource, string, string) {
			label := cmp.Or(sv(c.Name), sv(c.ConnectorId))
			return arnRow("connectors", c.ConnectorArn, &label, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeSecurityHubConnectorV2, NativeID: sv(c.ConnectorArn),
				Name: &label, Region: &region, AttributesJSON: mustJSON(c), DiscoveredBy: scanID,
			})
		}, tags, p)
}

func scanSHFindingAggregators(ctx context.Context, client securityhubAPI, acct *account, region string, st *store.Store, scanID string) (int, error) {
	pager := securityhub.NewListFindingAggregatorsPaginator(client, &securityhub.ListFindingAggregatorsInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if shSoftSkip(perr) {
				return 0, nil
			}
			return 0, fmt.Errorf("securityhub:ListFindingAggregators: %w", perr)
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
func scanSHPolicyAssociations(ctx context.Context, client securityhubAPI, acct *account, region string, st *store.Store, scanID string) (int, error) {
	pager := securityhub.NewListConfigurationPolicyAssociationsPaginator(client, &securityhub.ListConfigurationPolicyAssociationsInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if shSoftSkip(perr) {
				return 0, nil
			}
			return 0, fmt.Errorf("securityhub:ListConfigurationPolicyAssociations: %w", perr)
		}
		for _, a := range out.ConfigurationPolicyAssociationSummaries {
			policy := sv(a.ConfigurationPolicyId)
			target := sv(a.TargetId)
			if policy == "" || target == "" {
				continue
			}
			arn := arnFor(acct.partition(), "securityhub", region, acct.ID, "policy-association/"+policy+"/"+string(a.TargetType)+"/"+target)
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
// ManagedByProvider=true so the entries hide from default list / graph. It
// returns the controls it stored as parents (id = SecurityControlId).
func scanSHSecurityControls(ctx context.Context, client securityhubAPI, acct *account, region string, st *store.Store, scanID string) (int, []childParent, error) {
	pager := securityhub.NewListSecurityControlDefinitionsPaginator(client, &securityhub.ListSecurityControlDefinitionsInput{})
	var (
		batch    []*store.Resource
		controls []childParent
	)
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if shSoftSkip(perr) {
				return 0, nil, nil
			}
			return 0, nil, fmt.Errorf("securityhub:ListSecurityControlDefinitions: %w", perr)
		}
		for _, c := range out.SecurityControlDefinitions {
			id := sv(c.SecurityControlId)
			if id == "" {
				continue
			}
			arn := arnFor(acct.partition(), "securityhub", region, "", "security-control/"+id)
			label := id
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeSecurityHubSecurityControl, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(c), DiscoveredBy: scanID,
			})
			controls = append(controls, childParent{id: id, arn: arn})
		}
	}
	total, err := upsertBatch(st, batch, "securityhub security-controls")
	if err != nil {
		return 0, nil, err
	}
	return total, controls, nil
}

// scanSHStandards — DescribeStandards returns the AWS-managed standards
// catalogue. Mark ManagedByProvider=true. Every region lists a standard whose
// ARN carries no region (arn:{p}:securityhub:::ruleset/...) under the same
// NativeID, so Region comes from the ARN and is left unset when it names none;
// the scanned region would flip the row's Region to whichever region ran last.
func scanSHStandards(ctx context.Context, client securityhubAPI, acct *account, region string, st *store.Store, scanID string) (int, error) {
	pager := securityhub.NewDescribeStandardsPaginator(client, &securityhub.DescribeStandardsInput{})
	var (
		batch []*store.Resource
		noARN skipTally
	)
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if shSoftSkip(perr) {
				return 0, nil
			}
			return 0, fmt.Errorf("securityhub:DescribeStandards: %w", perr)
		}
		for _, s := range out.Standards {
			arn := sv(s.StandardsArn)
			if arn == "" {
				noARN.skipped(fmt.Sprintf("%q", sv(s.Name)), nil)
				continue
			}
			label := sv(s.Name)
			if label == "" {
				label = arn
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeSecurityHubStandard, NativeID: arn,
				Name: &label, Region: arnRegion(arn), AttributesJSON: mustJSON(s), DiscoveredBy: scanID,
			})
		}
	}
	warnNotStored(st, "securityhub:DescribeStandards", acct, region, &noARN, "standards have no StandardsArn")
	return upsertBatch(st, batch, "securityhub standards")
}

// arnRegion is the region segment of arn, or nil when it names none. An ARN
// that does not parse names no region either; its row is still stored.
func arnRegion(arn string) *string {
	parsed, err := awsarn.Parse(arn)
	if err != nil {
		return nil
	}
	return nonEmptyPtr(parsed.Region)
}

package aws

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/restype"
	"codeberg.org/icearp/disco/store"
	"github.com/aws/aws-sdk-go-v2/service/securityhub"
)

func init() {
	registerType(restype.Descriptor{Type: TypeSecurityHubHub, Service: "securityhub", Upstream: "AWS::SecurityHub::Hub", Leaf: true})
	registerType(restype.Descriptor{Type: TypeSecurityHubInsight, Service: "securityhub", Upstream: "AWS::SecurityHub::Insight", Leaf: true})
	registerType(restype.Descriptor{Type: TypeSecurityHubProductSubscription, Service: "securityhub", Upstream: "AWS::SecurityHub::ProductSubscription"})
	registerType(restype.Descriptor{Type: TypeSecurityHubStandardsSubscription, Service: "securityhub", Uncatalogued: true, Leaf: true})
	registerType(restype.Descriptor{Type: TypeSecurityHubAggregatorV2, Service: "securityhub", Leaf: true})
	registerType(restype.Descriptor{Type: TypeSecurityHubAutomationRule, Service: "securityhub", Leaf: true})
	registerType(restype.Descriptor{Type: TypeSecurityHubAutomationRuleV2, Service: "securityhub", Leaf: true})
	registerType(restype.Descriptor{Type: TypeSecurityHubConfigurationPolicy, Service: "securityhub", Leaf: true})
	registerType(restype.Descriptor{Type: TypeSecurityHubConnectorV2, Service: "securityhub", Leaf: true})
	registerType(restype.Descriptor{Type: TypeSecurityHubFindingAggregator, Service: "securityhub", Leaf: true})
	registerType(restype.Descriptor{Type: TypeSecurityHubHubV2, Service: "securityhub", Leaf: true})
	registerType(restype.Descriptor{Type: TypeSecurityHubOrganizationConfiguration, Service: "securityhub", Leaf: true})
	registerType(restype.Descriptor{Type: TypeSecurityHubPolicyAssociation, Service: "securityhub", Leaf: true})
	registerType(restype.Descriptor{Type: TypeSecurityHubSecurityControl, Service: "securityhub", Leaf: true, Managed: true})
	registerType(restype.Descriptor{Type: TypeSecurityHubStandard, Service: "securityhub", Leaf: true, Managed: true})
	registerService(serviceEntry{
		name: "aws:securityhub",
		fn:   scanSecurityHub,
	})
}

// securityhubAPI is the narrow set of Security Hub operations called by the
// scanSecurityHub sub-phases.
type securityhubAPI interface {
	DescribeHub(context.Context, *securityhub.DescribeHubInput, ...func(*securityhub.Options)) (*securityhub.DescribeHubOutput, error)
	GetInsights(context.Context, *securityhub.GetInsightsInput, ...func(*securityhub.Options)) (*securityhub.GetInsightsOutput, error)
	GetEnabledStandards(context.Context, *securityhub.GetEnabledStandardsInput, ...func(*securityhub.Options)) (*securityhub.GetEnabledStandardsOutput, error)
	ListEnabledProductsForImport(context.Context, *securityhub.ListEnabledProductsForImportInput, ...func(*securityhub.Options)) (*securityhub.ListEnabledProductsForImportOutput, error)
	ListAggregatorsV2(context.Context, *securityhub.ListAggregatorsV2Input, ...func(*securityhub.Options)) (*securityhub.ListAggregatorsV2Output, error)
	ListAutomationRules(context.Context, *securityhub.ListAutomationRulesInput, ...func(*securityhub.Options)) (*securityhub.ListAutomationRulesOutput, error)
	ListAutomationRulesV2(context.Context, *securityhub.ListAutomationRulesV2Input, ...func(*securityhub.Options)) (*securityhub.ListAutomationRulesV2Output, error)
	ListConfigurationPolicies(context.Context, *securityhub.ListConfigurationPoliciesInput, ...func(*securityhub.Options)) (*securityhub.ListConfigurationPoliciesOutput, error)
	ListConnectorsV2(context.Context, *securityhub.ListConnectorsV2Input, ...func(*securityhub.Options)) (*securityhub.ListConnectorsV2Output, error)
	ListFindingAggregators(context.Context, *securityhub.ListFindingAggregatorsInput, ...func(*securityhub.Options)) (*securityhub.ListFindingAggregatorsOutput, error)
	DescribeSecurityHubV2(context.Context, *securityhub.DescribeSecurityHubV2Input, ...func(*securityhub.Options)) (*securityhub.DescribeSecurityHubV2Output, error)
	DescribeOrganizationConfiguration(context.Context, *securityhub.DescribeOrganizationConfigurationInput, ...func(*securityhub.Options)) (*securityhub.DescribeOrganizationConfigurationOutput, error)
	ListConfigurationPolicyAssociations(context.Context, *securityhub.ListConfigurationPolicyAssociationsInput, ...func(*securityhub.Options)) (*securityhub.ListConfigurationPolicyAssociationsOutput, error)
	ListSecurityControlDefinitions(context.Context, *securityhub.ListSecurityControlDefinitionsInput, ...func(*securityhub.Options)) (*securityhub.ListSecurityControlDefinitionsOutput, error)
	DescribeStandards(context.Context, *securityhub.DescribeStandardsInput, ...func(*securityhub.Options)) (*securityhub.DescribeStandardsOutput, error)
}

// scanSecurityHub discovers the per-region hub, enabled standards, imported
// products, and saved insights. Security Hub is regional; accounts without
// the hub enabled here return InvalidAccessException on every API, so phase
// 1 sets a `present` flag (mirrors scanMacie) and sibling phases short-circuit
// when not enabled, avoiding N redundant denied errors.
func scanSecurityHub(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := securityhub.NewFromConfig(acct.cfg, func(o *securityhub.Options) { o.Region = region })

	hubARN, present, t, i, ferr := scanSecurityHubHub(ctx, client, acct, region, st, scanID)
	if ferr != nil {
		return 0, 0, ferr
	}
	total += t
	inserted += i
	if !present {
		return total, inserted, nil
	}

	{
		t, i, ferr := scanSecurityHubInsights(ctx, client, acct, region, hubARN, st, scanID)
		if ferr != nil {
			return total, inserted, ferr
		}
		total += t
		inserted += i
	}

	{
		t, i, ferr := scanSecurityHubStandards(ctx, client, acct, region, hubARN, st, scanID)
		if ferr != nil {
			return total, inserted, ferr
		}
		total += t
		inserted += i
	}

	{
		t, i, ferr := scanSecurityHubProducts(ctx, client, acct, region, hubARN, st, scanID)
		if ferr != nil {
			return total, inserted, ferr
		}
		total += t
		inserted += i
	}

	{
		t, i, ferr := scanSecurityHubExtended(ctx, client, acct, region, st, scanID)
		if ferr != nil {
			return total, inserted, ferr
		}
		total += t
		inserted += i
	}

	return total, inserted, nil
}

// isSecurityHubNotEnabled reports whether err means Security Hub isn't
// enabled in the calling region — DescribeHub and downstream APIs return
// InvalidAccessException in that case; treat as a soft skip like AccessDenied
// so non-enabled regions don't pollute scan errors.
func isSecurityHubNotEnabled(err error) bool {
	return isAPIErrorCode(err, "InvalidAccessException", "ResourceNotFoundException")
}

// securityHubHubNativeID synthesises the canonical hub ARN when DescribeHub
// returns no HubArn. Hub is a singleton per (acct, region); shape matches
// the AWS-issued ARN.
func securityHubHubNativeID(accountID, region string) string {
	return fmt.Sprintf("arn:aws:securityhub:%s:%s:hub/default", region, accountID)
}

func scanSecurityHubHub(ctx context.Context, client securityhubAPI, acct *account, region string, st *store.Store, scanID string) (hubARN string, present bool, total, inserted int, err error) {
	out, derr := client.DescribeHub(ctx, &securityhub.DescribeHubInput{})
	if derr != nil {
		if isAccessDenied(derr) {
			_ = skipIfAccessDenied(st, "securityhub:DescribeHub", acct.ID, region, derr)
			return "", false, 0, 0, nil
		}
		if isSecurityHubNotEnabled(derr) {
			return "", false, 0, 0, markServiceDisabled(derr)
		}
		return "", false, 0, 0, fmt.Errorf("securityhub:DescribeHub: %w", derr)
	}
	if out == nil {
		return "", false, 0, 0, nil
	}
	nid := sv(out.HubArn)
	if nid == "" {
		nid = securityHubHubNativeID(acct.ID, region)
	}
	r := &store.Resource{
		Provider:       "aws",
		AccountID:      acct.ID,
		AccountName:    &acct.Name,
		Type:           TypeSecurityHubHub,
		NativeID:       nid,
		Region:         &region,
		AttributesJSON: mustJSON(out),
		DiscoveredBy:   scanID,
	}
	n, uerr := st.UpsertResources([]*store.Resource{r})
	if uerr != nil {
		return "", false, 0, 0, fmt.Errorf("upsert securityhub hub: %w", uerr)
	}
	return nid, true, 1, n, nil
}

func scanSecurityHubInsights(ctx context.Context, client securityhubAPI, acct *account, region, hubARN string, st *store.Store, scanID string) (total, inserted int, err error) {
	pager := securityhub.NewGetInsightsPaginator(client, &securityhub.GetInsightsInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "securityhub:GetInsights", acct.ID, region, perr)
				return 0, 0, nil
			}
			if isSecurityHubNotEnabled(perr) {
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("securityhub:GetInsights: %w", perr)
		}
		for _, in := range out.Insights {
			arn := sv(in.InsightArn)
			if arn == "" {
				continue
			}
			r := &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeSecurityHubInsight,
				NativeID:       arn,
				Name:           in.Name,
				Region:         &region,
				AttributesJSON: mustJSON(in),
				DiscoveredBy:   scanID,
			}
			batch = append(batch, r)
		}
	}
	if len(batch) == 0 {
		return 0, 0, nil
	}
	n, uerr := upsertSecurityHubChildren(st, hubARN, acct, batch, "insights")
	if uerr != nil {
		return 0, 0, uerr
	}
	return len(batch), n, nil
}

func scanSecurityHubStandards(ctx context.Context, client securityhubAPI, acct *account, region, hubARN string, st *store.Store, scanID string) (total, inserted int, err error) {
	pager := securityhub.NewGetEnabledStandardsPaginator(client, &securityhub.GetEnabledStandardsInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "securityhub:GetEnabledStandards", acct.ID, region, perr)
				return 0, 0, nil
			}
			if isSecurityHubNotEnabled(perr) {
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("securityhub:GetEnabledStandards: %w", perr)
		}
		for _, s := range out.StandardsSubscriptions {
			arn := sv(s.StandardsSubscriptionArn)
			if arn == "" {
				continue
			}
			status := string(s.StandardsStatus)
			r := &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeSecurityHubStandardsSubscription,
				NativeID:       arn,
				Region:         &region,
				Status:         &status,
				AttributesJSON: mustJSON(s),
				DiscoveredBy:   scanID,
			}
			batch = append(batch, r)
		}
	}
	if len(batch) == 0 {
		return 0, 0, nil
	}
	n, uerr := upsertSecurityHubChildren(st, hubARN, acct, batch, "standards subscriptions")
	if uerr != nil {
		return 0, 0, uerr
	}
	return len(batch), n, nil
}

func scanSecurityHubProducts(ctx context.Context, client securityhubAPI, acct *account, region, hubARN string, st *store.Store, scanID string) (total, inserted int, err error) {
	pager := securityhub.NewListEnabledProductsForImportPaginator(client, &securityhub.ListEnabledProductsForImportInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "securityhub:ListEnabledProductsForImport", acct.ID, region, perr)
				return 0, 0, nil
			}
			if isSecurityHubNotEnabled(perr) {
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("securityhub:ListEnabledProductsForImport: %w", perr)
		}
		for _, arn := range out.ProductSubscriptions {
			if arn == "" {
				continue
			}
			r := &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeSecurityHubProductSubscription,
				NativeID:       arn,
				Region:         &region,
				AttributesJSON: mustJSON(map[string]string{"ProductSubscriptionArn": arn}),
				DiscoveredBy:   scanID,
			}
			batch = append(batch, r)
		}
	}
	if len(batch) == 0 {
		return 0, 0, nil
	}
	n, uerr := upsertSecurityHubChildren(st, hubARN, acct, batch, "product subscriptions")
	if uerr != nil {
		return 0, 0, uerr
	}
	return len(batch), n, nil
}

// upsertSecurityHubChildren persists hub children and links each to the
// per-region hub via the contains closure. Mirrors upsertMacieChildren.
func upsertSecurityHubChildren(st *store.Store, hubARN string, acct *account, batch []*store.Resource, kind string) (int, error) {
	n, err := st.UpsertResources(batch)
	if err != nil {
		return 0, fmt.Errorf("upsert securityhub %s: %w", kind, err)
	}
	parentID := store.ResourceID("aws", acct.ID, hubARN)
	pairs := make([][2]string, len(batch))
	for i, c := range batch {
		pairs[i] = [2]string{c.ID, parentID}
	}
	if err := st.RecordHierarchyBatch(pairs); err != nil {
		return 0, fmt.Errorf("closure securityhub %s: %w", kind, err)
	}
	return n, nil
}

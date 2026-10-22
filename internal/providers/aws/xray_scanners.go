package aws

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/service/xray"
	xraytypes "github.com/aws/aws-sdk-go-v2/service/xray/types"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerType(restype.Descriptor{Type: TypeXRayGroup, Service: "xray"})
	registerType(restype.Descriptor{Type: TypeXRayIndexingRule, Service: "xray", Untaggable: true})
	registerType(restype.Descriptor{Type: TypeXRayResourcePolicy, Service: "xray"})
	registerType(restype.Descriptor{Type: TypeXRaySamplingRule, Service: "xray"})
	registerType(restype.Descriptor{Type: TypeXRayTransactionSearchConfig, Service: "xray", Untaggable: true})
	registerService(serviceEntry{
		name: "aws:xray",
		fn:   scanXRay,
	})
}

type xrayAPI interface {
	GetGroups(context.Context, *xray.GetGroupsInput, ...func(*xray.Options)) (*xray.GetGroupsOutput, error)
	ListResourcePolicies(context.Context, *xray.ListResourcePoliciesInput, ...func(*xray.Options)) (*xray.ListResourcePoliciesOutput, error)
	GetSamplingRules(context.Context, *xray.GetSamplingRulesInput, ...func(*xray.Options)) (*xray.GetSamplingRulesOutput, error)
	GetTraceSegmentDestination(context.Context, *xray.GetTraceSegmentDestinationInput, ...func(*xray.Options)) (*xray.GetTraceSegmentDestinationOutput, error)
	GetIndexingRules(context.Context, *xray.GetIndexingRulesInput, ...func(*xray.Options)) (*xray.GetIndexingRulesOutput, error)
}

const xrayGetIndexingRulesOp = "xray:GetIndexingRules"

// scanXRay discovers X-Ray groups, resource policies, sampling rules, the
// per-(account,region) Transaction Search configuration and its indexing rules.
//
// AWS::XRay::TransactionSearchConfig has no list/dedicated Get endpoint;
// GetTraceSegmentDestination is the singleton that backs it (controls whether
// segments index to CloudWatch Logs for Transaction Search).
//
// The older phases stop at their first hard error. The indexing-rule phase runs
// whatever they returned, so its failure costs no other phase its rows and
// theirs does not skip it. Each result has its transient error demoted to a
// warning only beside a hard error: joined, it would be buried in the hard
// error instead of reported under its op; the rest are joined and the counts
// summed.
func scanXRay(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	client := xray.NewFromConfig(acct.cfg, func(o *xray.Options) { o.Region = region })
	total, inserted, err := scanXRayOlderPhases(ctx, client, acct, region, st, scanID)
	t, i, ierr := scanXRayIndexingRules(ctx, client, acct, region, st, scanID)
	var errs phaseErrs
	errs.add("xray", err)
	errs.add(xrayGetIndexingRulesOp, ierr)
	return total + t, inserted + i, errs.join(st, acct, region)
}

// scanXRayOlderPhases runs the phases that predate indexing rules, stopping at
// the first hard error.
func scanXRayOlderPhases(ctx context.Context, client xrayAPI, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	for _, phase := range []func() (int, int, error){
		func() (int, int, error) { return scanXRayGroups(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanXRayResourcePolicies(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanXRaySamplingRules(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) {
			return scanXRayTransactionSearchConfig(ctx, client, acct, region, st, scanID)
		},
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

func scanXRayGroups(ctx context.Context, client xrayAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := xray.NewGetGroupsPaginator(client, &xray.GetGroupsInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "xray:GetGroups", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("xray:GetGroups: %w", err)
		}
		for _, g := range out.Groups {
			arn := sv(g.GroupARN)
			if arn == "" {
				continue
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeXRayGroup, NativeID: arn,
				Name: g.GroupName, Region: &region,
				AttributesJSON: mustJSON(g), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "xray groups")
}

// scanXRayResourcePolicies synthesizes ARN per policy: arn:aws:xray:{r}:{a}:resource-policy/{name}.
func scanXRayResourcePolicies(ctx context.Context, client xrayAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := xray.NewListResourcePoliciesPaginator(client, &xray.ListResourcePoliciesInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "xray:ListResourcePolicies", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("xray:ListResourcePolicies: %w", err)
		}
		for _, p := range out.ResourcePolicies {
			name := sv(p.PolicyName)
			if name == "" {
				continue
			}
			arn := fmt.Sprintf("arn:aws:xray:%s:%s:resource-policy/%s", region, acct.ID, name)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeXRayResourcePolicy, NativeID: arn,
				Name: &name, Region: &region,
				AttributesJSON: mustJSON(p), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "xray resource-policies")
}

func scanXRaySamplingRules(ctx context.Context, client xrayAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := xray.NewGetSamplingRulesPaginator(client, &xray.GetSamplingRulesInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "xray:GetSamplingRules", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("xray:GetSamplingRules: %w", err)
		}
		for _, r := range out.SamplingRuleRecords {
			if r.SamplingRule == nil {
				continue
			}
			arn := sv(r.SamplingRule.RuleARN)
			if arn == "" {
				continue
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeXRaySamplingRule, NativeID: arn,
				Name: r.SamplingRule.RuleName, Region: &region,
				AttributesJSON: mustJSON(r), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "xray sampling-rules")
}

// scanXRayTransactionSearchConfig captures the per-(account,region)
// trace-segment-destination singleton backing X-Ray Transaction Search. No
// ARN — synthesize one.
func scanXRayTransactionSearchConfig(ctx context.Context, client xrayAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	out, err := client.GetTraceSegmentDestination(ctx, &xray.GetTraceSegmentDestinationInput{})
	if err != nil {
		if isAccessDenied(err) {
			return 0, 0, skipIfAccessDenied(st, "xray:GetTraceSegmentDestination", acct.ID, region, err)
		}
		if isAPIErrorCode(err, "ResourceNotFoundException") {
			return 0, 0, nil
		}
		return 0, 0, fmt.Errorf("xray:GetTraceSegmentDestination: %w", err)
	}
	arn := fmt.Sprintf("arn:aws:xray:%s:%s:transaction-search-config", region, acct.ID)
	label := "transaction-search-config"
	status := string(out.Status)
	return upsertBatch(st, []*store.Resource{{
		Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
		Type: TypeXRayTransactionSearchConfig, NativeID: arn,
		Name: &label, Region: &region, Status: &status,
		AttributesJSON: mustJSON(out), DiscoveredBy: scanID,
	}}, "xray transaction-search-config")
}

// xrayIndexingRuleAttrs shadows the rule's Rule union, which marshals as an
// unlabelled {"Value": ...}, with one key per known member so the stored row
// names the rule kind (precedent: acmAcmeEndpointAttrs). A member newer than
// the pinned SDK is kept under UnknownMember with its tag and its value, which
// is []byte and so stored base64-encoded. Rule stays null when the API
// returns none.
type xrayIndexingRuleAttrs struct {
	xraytypes.IndexingRule
	Rule *xrayIndexingRuleValueAttrs
}

type xrayIndexingRuleValueAttrs struct {
	Probabilistic *xraytypes.ProbabilisticRuleValue `json:",omitempty"`
	UnknownMember *xraytypes.UnknownUnionMember     `json:",omitempty"`
}

func flattenXRayIndexingRule(r xraytypes.IndexingRule) xrayIndexingRuleAttrs {
	out := xrayIndexingRuleAttrs{IndexingRule: r}
	switch v := r.Rule.(type) {
	case *xraytypes.IndexingRuleValueMemberProbabilistic:
		out.Rule = &xrayIndexingRuleValueAttrs{Probabilistic: &v.Value}
	case *xraytypes.UnknownUnionMember:
		out.Rule = &xrayIndexingRuleValueAttrs{UnknownMember: v}
	}
	return out
}

func xrayIndexingRuleNativeID(region, accountID, name string) string {
	return fmt.Sprintf("arn:aws:xray:%s:%s:indexing-rule/%s", region, accountID, name)
}

// scanXRayIndexingRules stores the region's Transaction Search indexing rules.
// The API has no create or delete operation for them (only GetIndexingRules and
// UpdateIndexingRule), so every rule is AWS-created and cannot be deleted. The
// type is still not Managed: its sampling percentage is the customer's choice,
// and Managed rows are left out of default `disco check` input. No rule
// ARN exists (the Service Reference lists only group and sampling-rule ARNs),
// so the NativeID is synthesized from the rule name, the identifier
// UpdateIndexingRule takes, like the resource policy's. A denial after a page
// keeps the rules already listed. An InvalidRequestException on the first
// page warns rather than fails: disco builds no input for that call, and it is
// unverified whether a region without Transaction Search answers with it. On a
// later page it is returned, since there disco echoes a NextToken.
func scanXRayIndexingRules(ctx context.Context, client xrayAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	in := &xray.GetIndexingRulesInput{}
	var (
		batch   []*store.Resource
		pageErr error
	)
	for {
		out, err := client.GetIndexingRules(ctx, in)
		if isAccessDenied(err) {
			_ = skipIfAccessDenied(st, xrayGetIndexingRulesOp, acct.ID, region, err)
			break
		}
		if in.NextToken == nil && isAPIErrorCode(err, "InvalidRequestException") {
			st.ReportWarning(store.ScanWarning{
				Provider: "aws", Service: xrayGetIndexingRulesOp, Scope: acct.ID + "/" + region,
				Message: fmt.Sprintf("indexing rules not listed: InvalidRequestException; the region may lack Transaction Search (unverified): %v", err),
			})
			return 0, 0, nil
		}
		if err != nil {
			return 0, 0, fmt.Errorf("%s: %w", xrayGetIndexingRulesOp, err)
		}
		for _, r := range out.IndexingRules {
			name := sv(r.Name)
			if name == "" {
				continue
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeXRayIndexingRule, NativeID: xrayIndexingRuleNativeID(region, acct.ID, name),
				Name: &name, Region: &region,
				AttributesJSON: mustJSON(flattenXRayIndexingRule(r)), DiscoveredBy: scanID,
			})
		}
		// GetIndexingRules has no paginator and no page-size input; a repeated
		// token would loop forever.
		var done bool
		if done, pageErr = lastPage(in.NextToken, out.NextToken); done {
			break
		}
		in.NextToken = out.NextToken
	}
	return upsertListed(st, batch, xrayGetIndexingRulesOp, pageErr)
}

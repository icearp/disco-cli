package aws

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/store"
	"github.com/aws/aws-sdk-go-v2/service/amp"
)

// scanAPSExtended discovers anomaly detectors, rule-groups namespaces, and
// resource-based policies — all three fan out per workspace ID.
// ResourcePolicy synth ARN: {workspaceArn}/policy.
func scanAPSExtended(ctx context.Context, client apsAPI, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	workspaces, err := loadAPSWorkspaceIDs(ctx, client, acct, region, st)
	if err != nil {
		return 0, 0, err
	}

	t, i, ferr := scanAPSAnomalyDetectors(ctx, client, workspaces, acct, region, st, scanID)
	if ferr != nil {
		return total, inserted, ferr
	}
	total += t
	inserted += i

	t, i, ferr = scanAPSRuleGroupsNamespaces(ctx, client, workspaces, acct, region, st, scanID)
	if ferr != nil {
		return total, inserted, ferr
	}
	total += t
	inserted += i

	t, i, ferr = scanAPSResourcePolicies(ctx, client, workspaces, acct, region, st, scanID)
	if ferr != nil {
		return total, inserted, ferr
	}
	total += t
	inserted += i
	return total, inserted, nil
}

type apsWorkspaceRef struct {
	ID  string
	ARN string
}

func loadAPSWorkspaceIDs(ctx context.Context, client apsAPI, acct *account, region string, st *store.Store) ([]apsWorkspaceRef, error) {
	var refs []apsWorkspaceRef
	p := amp.NewListWorkspacesPaginator(client, &amp.ListWorkspacesInput{})
	for p.HasMorePages() {
		page, err := p.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				_ = skipIfAccessDenied(st, "amp:ListWorkspaces(extended)", acct.ID, region, err)
				return nil, nil
			}
			return nil, fmt.Errorf("amp:ListWorkspaces(extended): %w", err)
		}
		for _, w := range page.Workspaces {
			id := sv(w.WorkspaceId)
			arn := sv(w.Arn)
			if id == "" || arn == "" {
				continue
			}
			refs = append(refs, apsWorkspaceRef{ID: id, ARN: arn})
		}
	}
	return refs, nil
}

func scanAPSAnomalyDetectors(ctx context.Context, client apsAPI, workspaces []apsWorkspaceRef, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var batch []*store.Resource
	for _, w := range workspaces {
		wid := w.ID
		var nextToken *string
		for {
			out, err := client.ListAnomalyDetectors(ctx, &amp.ListAnomalyDetectorsInput{
				WorkspaceId: &wid,
				NextToken:   nextToken,
			})
			if err != nil {
				if isAccessDenied(err) {
					_ = skipIfAccessDenied(st, "amp:ListAnomalyDetectors", acct.ID, region, err)
					break
				}
				if isAPIErrorCode(err, "ResourceNotFoundException", "ValidationException") {
					break
				}
				return 0, 0, fmt.Errorf("amp:ListAnomalyDetectors w=%s: %w", wid, err)
			}
			for _, d := range out.AnomalyDetectors {
				arn := sv(d.Arn)
				if arn == "" {
					continue
				}
				batch = append(batch, &store.Resource{
					Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
					Type: TypeAPSAnomalyDetector, NativeID: arn,
					Name: d.Alias, Region: &region,
					AttributesJSON: mustJSON(d), DiscoveredBy: scanID,
				})
			}
			if out.NextToken == nil || *out.NextToken == "" {
				break
			}
			nextToken = out.NextToken
		}
	}
	return upsertBatch(st, batch, "aps anomaly-detectors")
}

func scanAPSRuleGroupsNamespaces(ctx context.Context, client apsAPI, workspaces []apsWorkspaceRef, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var batch []*store.Resource
	for _, w := range workspaces {
		wid := w.ID
		var nextToken *string
		for {
			out, err := client.ListRuleGroupsNamespaces(ctx, &amp.ListRuleGroupsNamespacesInput{
				WorkspaceId: &wid,
				NextToken:   nextToken,
			})
			if err != nil {
				if isAccessDenied(err) {
					_ = skipIfAccessDenied(st, "amp:ListRuleGroupsNamespaces", acct.ID, region, err)
					break
				}
				if isAPIErrorCode(err, "ResourceNotFoundException", "ValidationException") {
					break
				}
				return 0, 0, fmt.Errorf("amp:ListRuleGroupsNamespaces w=%s: %w", wid, err)
			}
			for _, n := range out.RuleGroupsNamespaces {
				arn := sv(n.Arn)
				if arn == "" {
					continue
				}
				var status string
				if n.Status != nil {
					status = string(n.Status.StatusCode)
				}
				r := &store.Resource{
					Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
					Type: TypeAPSRuleGroupsNamespace, NativeID: arn,
					Name: n.Name, Region: &region,
					AttributesJSON: mustJSON(n), DiscoveredBy: scanID,
				}
				if status != "" {
					r.Status = &status
				}
				batch = append(batch, r)
			}
			if out.NextToken == nil || *out.NextToken == "" {
				break
			}
			nextToken = out.NextToken
		}
	}
	return upsertBatch(st, batch, "aps rule-groups-namespaces")
}

func scanAPSResourcePolicies(ctx context.Context, client apsAPI, workspaces []apsWorkspaceRef, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var batch []*store.Resource
	for _, w := range workspaces {
		wid := w.ID
		out, err := client.DescribeResourcePolicy(ctx, &amp.DescribeResourcePolicyInput{WorkspaceId: &wid})
		if err != nil {
			if isAccessDenied(err) || isAPIErrorCode(err, "ResourceNotFoundException", "ValidationException") {
				continue
			}
			return 0, 0, fmt.Errorf("amp:DescribeResourcePolicy w=%s: %w", wid, err)
		}
		if sv(out.PolicyDocument) == "" {
			continue
		}
		arn := w.ARN + "/policy"
		label := wid
		batch = append(batch, &store.Resource{
			Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
			Type: TypeAPSResourcePolicy, NativeID: arn,
			Name: &label, Region: &region,
			AttributesJSON: mustJSON(out), DiscoveredBy: scanID,
		})
	}
	return upsertBatch(st, batch, "aps resource-policies")
}

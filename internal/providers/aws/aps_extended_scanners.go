package aws

import (
	"context"
	"errors"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/service/amp"
	"github.com/icearp/disco-cli/store"
)

// scanAPSExtended discovers anomaly detectors, rule-groups namespaces, and
// resource-based policies — all three fan out per workspace ID.
// ResourcePolicy synth ARN: {workspaceArn}/policy.
func scanAPSExtended(ctx context.Context, client apsAPI, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	workspaces, err := loadAPSWorkspaceIDs(ctx, client, acct, region, st)
	if err != nil {
		return 0, 0, err
	}

	// These phases list independent per-workspace data, so each runs
	// whatever the others return.
	var errs phaseErrs
	t, i, ferr := scanAPSAnomalyDetectors(ctx, client, workspaces, acct, region, st, scanID)
	total, inserted = total+t, inserted+i
	errs.add("amp:ListAnomalyDetectors", ferr)

	t, i, ferr = scanAPSRuleGroupsNamespaces(ctx, client, workspaces, acct, region, st, scanID)
	total, inserted = total+t, inserted+i
	errs.add("amp:ListRuleGroupsNamespaces", ferr)

	t, i, ferr = scanAPSResourcePolicies(ctx, client, workspaces, acct, region, st, scanID)
	total, inserted = total+t, inserted+i
	errs.add("amp:DescribeResourcePolicy", ferr)
	return total, inserted, errs.join(st, acct, region)
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
	var (
		batch    []*store.Resource
		listErrs []error
		denied   skipTally
	)
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
					denied.skipped(wid, err)
					break
				}
				if isAPIErrorCode(err, "ResourceNotFoundException", "ValidationException") {
					break
				}
				listErrs = append(listErrs, fmt.Errorf("w=%s: %w", wid, err))
				break
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
					AttributesJSON: mustJSON(d), TagsJSON: mapTagsJSON(d.Tags), DiscoveredBy: scanID,
				})
			}
			last, terr := lastPage(nextToken, out.NextToken)
			if terr != nil {
				listErrs = append(listErrs, fmt.Errorf("w=%s: %w", wid, terr))
			}
			if last {
				break
			}
			nextToken = out.NextToken
		}
	}
	apsWarnDenied(st, "amp:ListAnomalyDetectors", acct, region, &denied, "anomaly detectors")
	return upsertListed(st, batch, "amp:ListAnomalyDetectors", errors.Join(listErrs...))
}

func scanAPSRuleGroupsNamespaces(ctx context.Context, client apsAPI, workspaces []apsWorkspaceRef, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var (
		batch    []*store.Resource
		listErrs []error
		denied   skipTally
	)
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
					denied.skipped(wid, err)
					break
				}
				if isAPIErrorCode(err, "ResourceNotFoundException", "ValidationException") {
					break
				}
				listErrs = append(listErrs, fmt.Errorf("w=%s: %w", wid, err))
				break
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
					AttributesJSON: mustJSON(n), TagsJSON: mapTagsJSON(n.Tags), DiscoveredBy: scanID,
				}
				if status != "" {
					r.Status = &status
				}
				batch = append(batch, r)
			}
			last, terr := lastPage(nextToken, out.NextToken)
			if terr != nil {
				listErrs = append(listErrs, fmt.Errorf("w=%s: %w", wid, terr))
			}
			if last {
				break
			}
			nextToken = out.NextToken
		}
	}
	apsWarnDenied(st, "amp:ListRuleGroupsNamespaces", acct, region, &denied, "rule groups namespaces")
	return upsertListed(st, batch, "amp:ListRuleGroupsNamespaces", errors.Join(listErrs...))
}

func scanAPSResourcePolicies(ctx context.Context, client apsAPI, workspaces []apsWorkspaceRef, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var (
		batch    []*store.Resource
		denied   skipTally
		readErrs []error
	)
	for _, w := range workspaces {
		wid := w.ID
		out, err := client.DescribeResourcePolicy(ctx, &amp.DescribeResourcePolicyInput{WorkspaceId: &wid})
		if err != nil {
			if isAccessDenied(err) {
				denied.skipped(wid, err)
				continue
			}
			// ResourceNotFoundException: the workspace has no policy.
			if isAPIErrorCode(err, "ResourceNotFoundException", "ValidationException") {
				continue
			}
			readErrs = append(readErrs, fmt.Errorf("w=%s: %w", wid, err))
			continue
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
	apsWarnDenied(st, "amp:DescribeResourcePolicy", acct, region, &denied, "resource policies")
	return upsertListed(st, batch, "amp:DescribeResourcePolicy", errors.Join(readErrs...))
}

// apsWarnDenied warns once for all the workspaces whose read of what was
// denied, naming the first.
func apsWarnDenied(st *store.Store, op string, acct *account, region string, denied *skipTally, what string) {
	if denied.count == 0 {
		return
	}
	_ = skipIfAccessDenied(st, op, acct.ID, region, fmt.Errorf(
		"%d workspace(s) denied, so their %s were not read; first %s: %w", denied.count, what, denied.first, denied.err))
}

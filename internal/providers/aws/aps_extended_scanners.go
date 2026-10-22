package aws

import (
	"context"
	"errors"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/service/amp"
	"github.com/icearp/disco-cli/store"
)

// apsWorkspaceRef is a workspace the workspace phase stored, which the
// anomaly-detector, rule-groups-namespace and resource-policy phases fan out over.
type apsWorkspaceRef struct {
	ID  string
	ARN string
}

// apsFanout files the per-workspace failures of one op so that each cause
// warns once and a failed workspace does not stop its siblings.
type apsFanout struct {
	op       string
	what     string // what the op reads, e.g. "anomaly detectors"
	denied   skipTally
	notReady notReadyTally
	vanished skipTally
	unkeyed  skipTally
	errs     []error
}

// failed files a workspace's failed call. On the first page the op's modeled
// ResourceNotFoundException means the workspace is gone (or, for
// DescribeResourcePolicy, has no policy) and is skipped silently, and its
// modeled ValidationException means the workspace is not ready: skipped, with
// one warning per op. On a later page the workspace was listable a page
// earlier, so ResourceNotFoundException means it was deleted while being
// paged: the rows already read are kept and it warns once. Every other error
// is returned by finish.
func (f *apsFanout) failed(wid string, laterPage bool, err error) {
	switch {
	case isAccessDenied(err):
		f.denied.skipped(wid, err)
	case !laterPage && isAPIErrorCode(err, "ResourceNotFoundException"):
	case !laterPage && isAPIErrorCode(err, "ValidationException"):
		f.notReady.skipped(wid, err)
	case isAPIErrorCode(err, "ResourceNotFoundException"): // a later page, as the case above took the first
		f.vanished.skipped(wid, err)
	default:
		f.errs = append(f.errs, fmt.Errorf("w=%s: %w", wid, err))
	}
}

// finish stores batch, warns once per cause, and returns the filed errors
// beside any write error. A failed write is the phase's whole outcome, so it
// warns about nothing else: "only the pages read before were stored" would be
// false.
func (f *apsFanout) finish(st *store.Store, acct *account, region string, batch []*store.Resource) (int, int, error) {
	total, inserted, err := upsertListed(st, batch, f.op, errors.Join(f.errs...))
	if errors.Is(err, store.ErrStoreWrite) {
		return total, inserted, err
	}
	f.notReady.warnSkipped(st, f.op, acct, region, "workspace(s)",
		"a workspace that is being created or updated refuses the call, so its "+f.what+" were not read")
	if f.denied.count > 0 {
		_ = skipIfAccessDenied(st, f.op, acct.ID, region, fmt.Errorf(
			"%d workspace(s) denied, so their %s were not read; first %s: %w", f.denied.count, f.what, f.denied.first, f.denied.err))
	}
	f.vanished.warnCode(st, f.op, acct, region, "ResourceNotFoundException", "workspace(s)",
		"each was deleted while its "+f.what+" were paged, so only the pages read before were stored")
	warnNotStored(st, f.op, acct, region, &f.unkeyed, f.what+" have an empty ARN")
	return total, inserted, err
}

func scanAPSAnomalyDetectors(ctx context.Context, client apsAPI, workspaces []apsWorkspaceRef, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	f := &apsFanout{op: "amp:ListAnomalyDetectors", what: "anomaly detectors"}
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
				f.failed(wid, nextToken != nil, err)
				break
			}
			for _, d := range out.AnomalyDetectors {
				arn := sv(d.Arn)
				if arn == "" {
					f.unkeyed.skipped("under workspace "+wid, nil)
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
				f.errs = append(f.errs, fmt.Errorf("w=%s: %w", wid, terr))
			}
			if last {
				break
			}
			nextToken = out.NextToken
		}
	}
	return f.finish(st, acct, region, batch)
}

func scanAPSRuleGroupsNamespaces(ctx context.Context, client apsAPI, workspaces []apsWorkspaceRef, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	f := &apsFanout{op: "amp:ListRuleGroupsNamespaces", what: "rule groups namespaces"}
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
				f.failed(wid, nextToken != nil, err)
				break
			}
			for _, n := range out.RuleGroupsNamespaces {
				arn := sv(n.Arn)
				if arn == "" {
					f.unkeyed.skipped("under workspace "+wid, nil)
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
				f.errs = append(f.errs, fmt.Errorf("w=%s: %w", wid, terr))
			}
			if last {
				break
			}
			nextToken = out.NextToken
		}
	}
	return f.finish(st, acct, region, batch)
}

// scanAPSResourcePolicies stores each workspace's resource-based policy under
// the synthesized ARN {workspaceArn}/policy.
func scanAPSResourcePolicies(ctx context.Context, client apsAPI, workspaces []apsWorkspaceRef, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	f := &apsFanout{op: "amp:DescribeResourcePolicy", what: "resource policies"}
	var batch []*store.Resource
	for _, w := range workspaces {
		wid := w.ID
		out, err := client.DescribeResourcePolicy(ctx, &amp.DescribeResourcePolicyInput{WorkspaceId: &wid})
		if err != nil {
			f.failed(wid, false, err)
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
	return f.finish(st, acct, region, batch)
}

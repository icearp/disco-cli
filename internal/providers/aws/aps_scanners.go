package aws

import (
	"context"
	"errors"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/service/amp"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerType(restype.Descriptor{Type: TypeAPSWorkspace, Service: "aps"})
	registerType(restype.Descriptor{Type: TypeAPSScraper, Service: "aps"})
	registerType(restype.Descriptor{Type: TypeAPSAnomalyDetector, Service: "aps"})
	registerType(restype.Descriptor{Type: TypeAPSRuleGroupsNamespace, Service: "aps"})
	registerType(restype.Descriptor{Type: TypeAPSResourcePolicy, Service: "aps"})
	registerService(serviceEntry{
		name: "aws:aps",
		fn:   scanAPS,
	})
}

// apsAPI is the narrow surface scanAPS uses. ListWorkspaces / ListScrapers
// return full WorkspaceSummary / ScraperSummary bodies; no Describe fan-out.
type apsAPI interface {
	ListWorkspaces(context.Context, *amp.ListWorkspacesInput, ...func(*amp.Options)) (*amp.ListWorkspacesOutput, error)
	ListScrapers(context.Context, *amp.ListScrapersInput, ...func(*amp.Options)) (*amp.ListScrapersOutput, error)
	ListAnomalyDetectors(context.Context, *amp.ListAnomalyDetectorsInput, ...func(*amp.Options)) (*amp.ListAnomalyDetectorsOutput, error)
	ListRuleGroupsNamespaces(context.Context, *amp.ListRuleGroupsNamespacesInput, ...func(*amp.Options)) (*amp.ListRuleGroupsNamespacesOutput, error)
	DescribeResourcePolicy(context.Context, *amp.DescribeResourcePolicyInput, ...func(*amp.Options)) (*amp.DescribeResourcePolicyOutput, error)
}

func scanAPS(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := amp.NewFromConfig(acct.cfg, func(o *amp.Options) { o.Region = region })
	return scanAPSAll(ctx, client, acct, region, st, scanID)
}

// scanAPSAll runs every phase whatever an earlier one returns, except after a
// failed store write: the phases list independent data, and the
// per-workspace phases fan out over whichever workspaces were stored.
func scanAPSAll(ctx context.Context, client apsAPI, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	var errs phaseErrs
	stop := func(op string, t, i int, ferr error) bool {
		total, inserted = total+t, inserted+i
		errs.add(op, ferr)
		return errors.Is(ferr, store.ErrStoreWrite)
	}

	workspaces, t, i, ferr := scanAPSWorkspaces(ctx, client, acct, region, st, scanID)
	if stop("amp:ListWorkspaces", t, i, ferr) {
		return total, inserted, errs.join(st, acct, region)
	}
	t, i, ferr = scanAPSScrapers(ctx, client, acct, region, st, scanID)
	if stop("amp:ListScrapers", t, i, ferr) {
		return total, inserted, errs.join(st, acct, region)
	}
	t, i, ferr = scanAPSAnomalyDetectors(ctx, client, workspaces, acct, region, st, scanID)
	if stop("amp:ListAnomalyDetectors", t, i, ferr) {
		return total, inserted, errs.join(st, acct, region)
	}
	t, i, ferr = scanAPSRuleGroupsNamespaces(ctx, client, workspaces, acct, region, st, scanID)
	if stop("amp:ListRuleGroupsNamespaces", t, i, ferr) {
		return total, inserted, errs.join(st, acct, region)
	}
	t, i, ferr = scanAPSResourcePolicies(ctx, client, workspaces, acct, region, st, scanID)
	stop("amp:DescribeResourcePolicy", t, i, ferr)
	return total, inserted, errs.join(st, acct, region)
}

// scanAPSWorkspaces stores the listed workspaces and returns those stored, for
// the per-workspace phases.
func scanAPSWorkspaces(ctx context.Context, client apsAPI, acct *account, region string, st *store.Store, scanID string) (refs []apsWorkspaceRef, total, inserted int, err error) {
	const op = "amp:ListWorkspaces"
	var unkeyed, noID skipTally
	defer func() {
		if errors.Is(err, store.ErrStoreWrite) {
			return
		}
		warnNotStored(st, op, acct, region, &unkeyed, "workspaces have an empty ARN")
		if noID.count > 0 {
			st.ReportWarning(store.ScanWarning{
				Provider: "aws", Service: op, Scope: acct.ID + "/" + region,
				Message: fmt.Sprintf("%d workspace(s) stored with an empty WorkspaceId, so their children were not read; first %s", noID.count, noID.first),
			})
		}
	}()
	p := amp.NewListWorkspacesPaginator(client, &amp.ListWorkspacesInput{})
	for p.HasMorePages() {
		page, err := p.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return refs, total, inserted, skipIfAccessDenied(st, op, acct.ID, region, err)
			}
			return refs, total, inserted, fmt.Errorf("%s: %w", op, err)
		}
		batch := make([]*store.Resource, 0, len(page.Workspaces))
		var pageRefs []apsWorkspaceRef
		for _, w := range page.Workspaces {
			arn := sv(w.Arn)
			if arn == "" {
				unkeyed.skipped(sv(w.WorkspaceId), nil)
				continue
			}
			name := sv(w.Alias)
			if name == "" {
				name = sv(w.WorkspaceId)
			}
			var status string
			if w.Status != nil {
				status = string(w.Status.StatusCode)
			}
			batch = append(batch, &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeAPSWorkspace,
				NativeID:       arn,
				Name:           &name,
				Region:         &region,
				Status:         &status,
				CreatedAt:      tp(w.CreatedAt),
				AttributesJSON: mustJSON(w),
				TagsJSON:       mapTagsJSON(w.Tags),
				DiscoveredBy:   scanID,
			})
			// Children are listed by WorkspaceId, which the model requires
			// but the response may still omit.
			if id := sv(w.WorkspaceId); id != "" {
				pageRefs = append(pageRefs, apsWorkspaceRef{ID: id, ARN: arn})
			} else {
				noID.skipped(arn, nil)
			}
		}
		if len(batch) > 0 {
			n, err := st.UpsertResources(batch)
			if err != nil {
				return refs, total, inserted, fmt.Errorf("upsert aps workspaces: %w", err)
			}
			total += len(batch)
			inserted += n
			refs = append(refs, pageRefs...)
		}
	}
	return refs, total, inserted, nil
}

func scanAPSScrapers(ctx context.Context, client apsAPI, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	const op = "amp:ListScrapers"
	var unkeyed skipTally
	defer func() {
		if !errors.Is(err, store.ErrStoreWrite) {
			warnNotStored(st, op, acct, region, &unkeyed, "scrapers have an empty ARN")
		}
	}()
	p := amp.NewListScrapersPaginator(client, &amp.ListScrapersInput{})
	for p.HasMorePages() {
		page, err := p.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return total, inserted, skipIfAccessDenied(st, op, acct.ID, region, err)
			}
			return total, inserted, fmt.Errorf("%s: %w", op, err)
		}
		batch := make([]*store.Resource, 0, len(page.Scrapers))
		for _, s := range page.Scrapers {
			arn := sv(s.Arn)
			if arn == "" {
				unkeyed.skipped(sv(s.ScraperId), nil)
				continue
			}
			name := sv(s.Alias)
			if name == "" {
				name = sv(s.ScraperId)
			}
			var status string
			if s.Status != nil {
				status = string(s.Status.StatusCode)
			}
			batch = append(batch, &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeAPSScraper,
				NativeID:       arn,
				Name:           &name,
				Region:         &region,
				Status:         &status,
				CreatedAt:      tp(s.CreatedAt),
				AttributesJSON: mustJSON(s),
				TagsJSON:       mapTagsJSON(s.Tags),
				DiscoveredBy:   scanID,
			})
		}
		if len(batch) > 0 {
			n, err := st.UpsertResources(batch)
			if err != nil {
				return total, inserted, fmt.Errorf("upsert aps scrapers: %w", err)
			}
			total += len(batch)
			inserted += n
		}
	}
	return total, inserted, nil
}

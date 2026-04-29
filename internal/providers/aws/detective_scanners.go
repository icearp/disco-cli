package aws

import (
	"context"
	"fmt"
	"sync"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/internal/store"
	"github.com/aws/aws-sdk-go-v2/service/detective"
	"golang.org/x/sync/errgroup"
	"golang.org/x/sync/semaphore"
)

func init() {
	registerService(serviceEntry{
		name: "aws:detective",
		fn:   scanDetective,
		emits: []coverage.TypeDecl{
			{Service: "detective", DiscoType: TypeDetectiveGraph},
			{Service: "detective", DiscoType: TypeDetectiveMember, Synthetic: true},
		},
	})
}

// detectiveAPI is the narrow set of Detective operations called by the
// scanDetective sub-phases.
type detectiveAPI interface {
	ListGraphs(context.Context, *detective.ListGraphsInput, ...func(*detective.Options)) (*detective.ListGraphsOutput, error)
	ListMembers(context.Context, *detective.ListMembersInput, ...func(*detective.Options)) (*detective.ListMembersOutput, error)
}

// scanDetective discovers Detective behavior graphs and their member accounts
// in one region. Detective is regional. Accounts that are not the
// administrator of any behavior graph in the region get an empty graph list
// from ListGraphs and the scan ends after phase 1. Per-phase AccessDenied is
// tolerated so a partial-IAM grant (read graphs but not members) still
// produces useful coverage.
func scanDetective(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := detective.NewFromConfig(acct.cfg, func(o *detective.Options) { o.Region = region })

	// Phase 1: behavior graphs (paginator).
	graphARNs, t, i, ferr := scanDetectiveGraphs(ctx, client, acct, region, st, scanID)
	if ferr != nil {
		return 0, 0, ferr
	}
	total += t
	inserted += i
	if len(graphARNs) == 0 {
		return total, inserted, nil
	}

	// Phase 2: member accounts per graph (paginator + fan-out).
	if t, i, ferr := scanDetectiveMembers(ctx, client, acct, region, st, scanID, graphARNs); ferr != nil {
		return total, inserted, ferr
	} else {
		total += t
		inserted += i
	}

	return total, inserted, nil
}

// detectiveMemberNativeID synthesises an identifier for a behavior-graph
// member. The Detective API exposes no member ARN — only AccountId scoped to
// the parent GraphArn. Synthetic shape keeps the parent context so re-scans
// dedupe and cross-graph members of the same account get distinct rows
// (precedent: KMS grant, EFS mount-target, SSO assignment).
func detectiveMemberNativeID(graphArn, accountID string) string {
	return fmt.Sprintf("%s/member/%s", graphArn, accountID)
}

func scanDetectiveGraphs(ctx context.Context, client detectiveAPI, acct *account, region string, st *store.Store, scanID string) (graphARNs []string, total, inserted int, err error) {
	pager := detective.NewListGraphsPaginator(client, &detective.ListGraphsInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "detective:ListGraphs", acct.ID, region, perr)
				return nil, 0, 0, nil
			}
			return nil, 0, 0, fmt.Errorf("detective:ListGraphs: %w", perr)
		}
		for _, g := range out.GraphList {
			arn := sv(g.Arn)
			if arn == "" {
				continue
			}
			graphARNs = append(graphARNs, arn)
			batch = append(batch, &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeDetectiveGraph,
				NativeID:       arn,
				Region:         &region,
				AttributesJSON: mustJSON(g),
				DiscoveredBy:   scanID,
			})
		}
	}
	if len(batch) == 0 {
		return nil, 0, 0, nil
	}
	n, uerr := st.UpsertResources(batch)
	if uerr != nil {
		return nil, 0, 0, fmt.Errorf("upsert detective graphs: %w", uerr)
	}
	return graphARNs, len(batch), n, nil
}

func scanDetectiveMembers(ctx context.Context, client detectiveAPI, acct *account, region string, st *store.Store, scanID string, graphARNs []string) (total, inserted int, err error) {
	sem := semaphore.NewWeighted(fanoutMed)
	var (
		mu    sync.Mutex
		batch []*store.Resource
		pairs [][2]string
	)
	g, gctx := errgroup.WithContext(ctx)
	for _, graphArn := range graphARNs {
		if err := sem.Acquire(gctx, 1); err != nil {
			return 0, 0, err
		}
		g.Go(func() error {
			defer sem.Release(1)
			pager := detective.NewListMembersPaginator(client, &detective.ListMembersInput{GraphArn: &graphArn})
			parentID := store.ResourceID("aws", acct.ID, TypeDetectiveGraph, graphArn)
			for pager.HasMorePages() {
				out, perr := pager.NextPage(gctx)
				if perr != nil {
					if isAccessDenied(perr) {
						return nil
					}
					return fmt.Errorf("detective:ListMembers %s: %w", graphArn, perr)
				}
				for _, m := range out.MemberDetails {
					accountID := sv(m.AccountId)
					if accountID == "" {
						continue
					}
					nativeID := detectiveMemberNativeID(graphArn, accountID)
					status := string(m.Status)
					r := &store.Resource{
						Provider:       "aws",
						AccountID:      acct.ID,
						AccountName:    &acct.Name,
						Type:           TypeDetectiveMember,
						NativeID:       nativeID,
						Region:         &region,
						Status:         &status,
						AttributesJSON: mustJSON(m),
						DiscoveredBy:   scanID,
					}
					mu.Lock()
					batch = append(batch, r)
					pairs = append(pairs, [2]string{store.ResourceID("aws", acct.ID, TypeDetectiveMember, nativeID), parentID})
					mu.Unlock()
				}
			}
			return nil
		})
	}
	if werr := g.Wait(); werr != nil {
		return 0, 0, werr
	}
	if len(batch) == 0 {
		return 0, 0, nil
	}
	n, uerr := st.UpsertResources(batch)
	if uerr != nil {
		return 0, 0, fmt.Errorf("upsert detective members: %w", uerr)
	}
	if err := st.RecordHierarchyBatch(pairs); err != nil {
		return 0, 0, fmt.Errorf("closure detective members: %w", err)
	}
	return len(batch), n, nil
}

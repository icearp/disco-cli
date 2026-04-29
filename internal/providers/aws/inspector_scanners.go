package aws

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/internal/store"
	"github.com/aws/aws-sdk-go-v2/service/inspector2"
)

func init() {
	registerService(serviceEntry{
		name: "aws:inspector2",
		fn:   scanInspector2,
		emits: []coverage.TypeDecl{
			{Service: "inspectorv2", DiscoType: TypeInspector2Filter},
			{Service: "inspectorv2", DiscoType: TypeInspector2Member, Synthetic: true},
		},
	})
}

// inspector2API is the narrow set of Inspector v2 operations called by the
// scanInspector2 sub-phases.
type inspector2API interface {
	ListFilters(context.Context, *inspector2.ListFiltersInput, ...func(*inspector2.Options)) (*inspector2.ListFiltersOutput, error)
	ListMembers(context.Context, *inspector2.ListMembersInput, ...func(*inspector2.Options)) (*inspector2.ListMembersOutput, error)
}

// scanInspector2 discovers Inspector v2 finding-filters and member accounts
// (delegated-admin perspective) in one region. Two phases run sequentially.
// Findings, coverage rows, and the singleton org-level configuration are
// deliberately out of scope: findings are event data (matches Macie /
// Detective / Security Hub precedent), coverage explodes the row count
// (one entry per scanned resource × scan-type), and configuration is a
// singleton config rather than a graph-edge target.
func scanInspector2(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := inspector2.NewFromConfig(acct.cfg, func(o *inspector2.Options) { o.Region = region })

	{
		t, i, ferr := scanInspector2Filters(ctx, client, acct, region, st, scanID)
		if ferr != nil {
			return total, inserted, ferr
		}
		total += t
		inserted += i
	}

	{
		t, i, ferr := scanInspector2Members(ctx, client, acct, region, st, scanID)
		if ferr != nil {
			return total, inserted, ferr
		}
		total += t
		inserted += i
	}

	return total, inserted, nil
}

// inspector2MemberNativeID synthesises an identifier for a member-account
// row. Inspector v2 does not issue an ARN per (admin, member) tuple; the
// shape mirrors Detective members (`{parentARN}/member/{accountId}`) but
// uses the delegated-admin's pseudo-ARN as the parent so multi-admin
// rescans dedupe.
func inspector2MemberNativeID(region, adminAccountID, memberAccountID string) string {
	return fmt.Sprintf("arn:aws:inspector2:%s:%s:member/%s", region, adminAccountID, memberAccountID)
}

func scanInspector2Filters(ctx context.Context, client inspector2API, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	pager := inspector2.NewListFiltersPaginator(client, &inspector2.ListFiltersInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "inspector2:ListFilters", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("inspector2:ListFilters: %w", perr)
		}
		for _, f := range out.Filters {
			arn := sv(f.Arn)
			if arn == "" {
				continue
			}
			batch = append(batch, &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeInspector2Filter,
				NativeID:       arn,
				Name:           f.Name,
				Region:         &region,
				AttributesJSON: mustJSON(f),
				DiscoveredBy:   scanID,
			})
		}
	}
	if len(batch) == 0 {
		return 0, 0, nil
	}
	n, uerr := st.UpsertResources(batch)
	if uerr != nil {
		return 0, 0, fmt.Errorf("upsert inspector2 filters: %w", uerr)
	}
	return len(batch), n, nil
}

func scanInspector2Members(ctx context.Context, client inspector2API, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	pager := inspector2.NewListMembersPaginator(client, &inspector2.ListMembersInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "inspector2:ListMembers", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("inspector2:ListMembers: %w", perr)
		}
		for _, m := range out.Members {
			memberAcct := sv(m.AccountId)
			if memberAcct == "" {
				continue
			}
			nativeID := inspector2MemberNativeID(region, acct.ID, memberAcct)
			status := string(m.RelationshipStatus)
			batch = append(batch, &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeInspector2Member,
				NativeID:       nativeID,
				Region:         &region,
				Status:         &status,
				AttributesJSON: mustJSON(m),
				DiscoveredBy:   scanID,
			})
		}
	}
	if len(batch) == 0 {
		return 0, 0, nil
	}
	n, uerr := st.UpsertResources(batch)
	if uerr != nil {
		return 0, 0, fmt.Errorf("upsert inspector2 members: %w", uerr)
	}
	return len(batch), n, nil
}

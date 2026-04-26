package aws

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/store"
)

// pageScan drives an AWS SDK v2 paginator, converts each page's items to
// *store.Resource via toResource, and upserts in per-page batches. iamAction
// labels the call for AccessDenied warnings (e.g. "secretsmanager:ListSecrets").
//
// hasMore + next are taken as callbacks rather than an interface because every
// SDK v2 paginator's NextPage carries a service-typed variadic
// (`...func(*<svc>.Options)`) that can't be expressed in a single generic
// interface. Caller passes `p.HasMorePages` and a one-line closure around
// `p.NextPage`. Transient network errors are wrapped at the dispatch layer
// (aws.go scanRegion); pageScan only handles AccessDenied + propagates.
func pageScan[Page any, Item any](
	ctx context.Context,
	iamAction string,
	acct *account,
	region string,
	st *store.Store,
	hasMore func() bool,
	next func(context.Context) (Page, error),
	items func(Page) []Item,
	toResource func(Item) *store.Resource,
) (total, inserted int, err error) {
	for hasMore() {
		page, err := next(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return total, inserted, skipIfAccessDenied(st, iamAction, acct.ID, region, err)
			}
			return total, inserted, fmt.Errorf("%s: %w", iamAction, err)
		}
		raw := items(page)
		if len(raw) == 0 {
			continue
		}
		batch := make([]*store.Resource, 0, len(raw))
		for _, it := range raw {
			if r := toResource(it); r != nil {
				batch = append(batch, r)
			}
		}
		if len(batch) == 0 {
			continue
		}
		n, err := st.UpsertResources(batch)
		if err != nil {
			return total, inserted, fmt.Errorf("upsert %s: %w", iamAction, err)
		}
		total += len(batch)
		inserted += n
	}
	return total, inserted, nil
}

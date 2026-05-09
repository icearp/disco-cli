package aws

import (
	"context"
	"fmt"
	"sync"

	"codeberg.org/icearp/disco/store"
	"golang.org/x/sync/errgroup"
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

// pageScanConcurrent is pageScan's sibling for the List-then-Describe N+1
// pattern: the List API yields skeletons (names or ARNs), each requiring a
// concurrent Describe/Get to assemble the persisted Resource. enrich is
// invoked per item with a derived context; returning (nil, nil) silently
// drops the item (e.g. on per-item AccessDenied — mirrors existing scanner
// behavior). A non-nil error from enrich aborts the page via errgroup.
//
// concurrency caps in-flight enrich goroutines per page; 0 means unbounded
// (matches current sns/acm/eks behavior). Use a positive bound for services
// where unbounded fan-out has tripped throttling in practice.
func pageScanConcurrent[Page any, Item any](
	ctx context.Context,
	iamAction string,
	acct *account,
	region string,
	st *store.Store,
	hasMore func() bool,
	next func(context.Context) (Page, error),
	items func(Page) []Item,
	enrich func(ctx context.Context, item Item) (*store.Resource, error),
	concurrency int,
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
		var (
			mu    sync.Mutex
			batch = make([]*store.Resource, 0, len(raw))
		)
		g, gctx := errgroup.WithContext(ctx)
		if concurrency > 0 {
			g.SetLimit(concurrency)
		}
		for _, it := range raw {
			g.Go(func() error {
				r, err := enrich(gctx, it)
				if err != nil {
					return err
				}
				if r == nil {
					return nil
				}
				mu.Lock()
				batch = append(batch, r)
				mu.Unlock()
				return nil
			})
		}
		if err := g.Wait(); err != nil {
			return total, inserted, err
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

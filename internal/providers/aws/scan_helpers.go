package aws

import (
	"context"
	"fmt"
	"sync"

	"github.com/icearp/disco-cli/store"
	"golang.org/x/sync/errgroup"
)

// pageScan drives an AWS SDK v2 paginator, converts each page's items to
// *store.Resource via toResource, and upserts in per-page batches. iamAction
// labels the call for AccessDenied warnings (e.g. "secretsmanager:ListSecrets").
//
// hasMore/next are callbacks, not an interface, because every SDK v2
// paginator's NextPage carries a service-typed variadic
// (`...func(*<svc>.Options)`) no single generic interface can express.
// Caller passes `p.HasMorePages` and a one-line closure around `p.NextPage`.
// Transient network errors are wrapped at the dispatch layer (aws.go
// scanRegion); pageScan only handles AccessDenied and propagates.
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
// concurrent Describe/Get to assemble the persisted Resource. enrich runs
// per item with a derived context; returning (nil, nil) silently drops the
// item (e.g. on per-item AccessDenied — mirrors existing scanner behavior).
// A non-nil error from enrich aborts the page via errgroup.
//
// concurrency caps in-flight enrich goroutines per page; 0 means unbounded
// (matches sns/acm/eks). Use a positive bound where unbounded fan-out has
// tripped throttling in practice.
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

// nonEmptyPtr returns nil for "", so an absent SDK enum leaves a column unset
// rather than storing an empty string.
func nonEmptyPtr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// childParent is a parent listed earlier in the same scan: id is what the
// child list op keys on, arn is the parent row's NativeID.
type childParent struct{ id, arn string }

// childFanOut runs list for each parent concurrently (fanoutMed), upserts every
// child and links it under its parent via the contains closure. An error isSkip
// accepts drops that parent silently; it is checked first so a service can claim
// access-denied-coded feature gaps. Any other AccessDenied drops that parent and
// warns once for op while the remaining parents continue; anything else is
// returned.
func childFanOut(
	ctx context.Context, st *store.Store, acct *account, region, op string, parents []childParent,
	isSkip func(error) bool, list func(context.Context, childParent) ([]*store.Resource, error),
) (int, int, error) {
	var (
		mu        sync.Mutex
		batch     []*store.Resource
		parentIDs []string
		denyOnce  sync.Once
	)
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(fanoutMed)
	for _, p := range parents {
		g.Go(func() error {
			rows, err := list(gctx, p)
			switch {
			case err == nil:
			case isSkip(err):
				return nil
			case isAccessDenied(err):
				denyOnce.Do(func() { _ = skipIfAccessDenied(st, op, acct.ID, region, err) })
				return nil
			default:
				return fmt.Errorf("%s %s: %w", op, p.id, err)
			}
			parentID := store.ResourceID("aws", acct.ID, p.arn)
			mu.Lock()
			defer mu.Unlock()
			for _, r := range rows {
				batch = append(batch, r)
				parentIDs = append(parentIDs, parentID)
			}
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return 0, 0, err
	}
	total, inserted, err := upsertBatch(st, batch, op)
	if err != nil {
		return 0, 0, err
	}
	pairs := make([][2]string, len(batch))
	for i, r := range batch {
		pairs[i] = [2]string{r.ID, parentIDs[i]}
	}
	if err := st.RecordHierarchyBatch(pairs); err != nil {
		return 0, 0, fmt.Errorf("closure %s: %w", op, err)
	}
	return total, inserted, nil
}

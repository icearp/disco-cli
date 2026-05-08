package gcp

import (
	"context"
	"strings"
	"sync"

	"codeberg.org/icearp/disco/internal/store"
	"codeberg.org/icearp/disco/internal/util"
	"golang.org/x/sync/errgroup"
	"golang.org/x/sync/semaphore"
	compute "google.golang.org/api/compute/v1"
)

// Generic paginate / fan-out / lookup helpers used by per-service scanners.
// Lifted out of gcp.go to keep the orchestration file focused on Scanner /
// Scan / scanProject. Documented in gcp/CLAUDE.md.

// pager is the structural interface satisfied by every *.List() call result in
// google.golang.org/api/*. Every Discovery-generated client exposes
// Pages(ctx, fn) on its List request structs — generics let one helper drive
// every paginated walk in this provider.
type pager[P any] interface {
	Pages(context.Context, func(*P) error) error
}

// runPaginated drives a paginated List call with the boilerplate every GCP
// scanner phase repeats: invoke req.Pages with a page handler that returns
// (pageTotal, pageInserted, err), accumulate the totals, then classify the
// final error via isPermissionDenied / skipIfDenied. Behavior:
//
//   - API-not-enabled (`isAPINotEnabled`) → returns the wrapped errServiceDisabled
//     sentinel so the dispatch loop renders "(service disabled)".
//   - Real permission denial → records a ScanWarning, returns nil.
//   - Other errors → propagated unwrapped.
//
// `action` is the per-API label used in any ScanWarning
// (e.g. "pubsub:topics.list"). `pageHandler` builds and persists the batch
// from each page and reports its own counts; counts are summed across pages.
func runPaginated[P any](ctx context.Context, st *store.Store, p *project, action string,
	req pager[P], pageHandler func(*P) (int, int, error),
) (total, inserted int, err error) {
	err = req.Pages(ctx, func(page *P) error {
		t, n, e := pageHandler(page)
		total += t
		inserted += n
		return e
	})
	if err != nil {
		if isPermissionDenied(err) {
			return total, inserted, skipIfDenied(st, action, p.ID, err)
		}
		return total, inserted, err
	}
	return total, inserted, nil
}

// forEachItem runs fn over each item with at most concurrency goroutines in
// flight. First non-nil error aborts siblings via the errgroup-derived
// context. Used by per-location / per-zone / per-SA fan-out scanners (KMS
// locations, DNS zones, BigQuery datasets, IAM-key SAs) so the
// errgroup+semaphore boilerplate lives in one place.
func forEachItem[T any](ctx context.Context, concurrency int, items []T, fn func(ctx context.Context, item T) error) error {
	sem := semaphore.NewWeighted(int64(concurrency))
	g, gctx := errgroup.WithContext(ctx)
	for _, it := range items {
		g.Go(func() error {
			if err := sem.Acquire(gctx, 1); err != nil {
				return err
			}
			defer sem.Release(1)
			return fn(gctx, it)
		})
	}
	return g.Wait()
}

// gcpRegions enumerates the compute regions enabled for a project. Used by
// per-region fan-out scanners (Dataproc, Dataflow when not using its
// aggregated API, Spanner instance regions). Returns empty + nil on
// permission denial (treats lack of compute.regions.list as "no per-region
// scope possible" rather than aborting). Cache per project via the caller —
// list calls are cheap but each scanner shouldn't burn one.
func gcpRegions(ctx context.Context, p *project) ([]string, error) {
	opts := clientOptions(ctx, providerCfg{})
	svc, err := compute.NewService(ctx, opts...)
	if err != nil {
		return nil, err
	}
	var out []string
	if err := svc.Regions.List(p.ID).Pages(ctx, func(page *compute.RegionList) error {
		for _, r := range page.Items {
			if r != nil && r.Name != "" {
				out = append(out, r.Name)
			}
		}
		return nil
	}); err != nil {
		if isPermissionDenied(err) || isAPINotEnabled(err) {
			return nil, nil
		}
		return nil, err
	}
	return out, nil
}

// gcpRegionFanoutScan drives the per-region fan-out pattern shared by
// GCP services that have no aggregated/wildcard list endpoint (Dataproc,
// future per-region Spanner, AI Platform regional, etc.). It enumerates
// enabled regions via gcpRegions, fans out one paginated list call per
// region (concurrency-bounded by `concurrency`), accumulates resources
// across all regions under a mutex, and finally upserts + closure-pairs
// the batch with the project as parent.
//
// Per-region permission-denied + API-not-enabled are tolerated silently
// per region (caller's region scope might be restricted) — other errors
// propagate. action is the label fed to skipIfDenied for the warning
// path. pagerFn returns a fresh pager per region. pageItems projects a
// page into items. itemToResource shapes each item; returning nil skips.
//
// Generic over Page (P) and Item (T) — works against any
// google.golang.org/api List request that exposes Pages(ctx, fn).
func gcpRegionFanoutScan[P any, T any](
	ctx context.Context,
	p *project,
	st *store.Store,
	concurrency int,
	action string,
	pagerFn func(region string) pager[P],
	pageItems func(*P) []T,
	itemToResource func(item T, region string) *store.Resource,
) (total, inserted int, err error) {
	regions, err := gcpRegions(ctx, p)
	if err != nil {
		return 0, 0, err
	}
	return gcpRegionFanoutScanIn(ctx, p, st, concurrency, regions, action, pagerFn, pageItems, itemToResource)
}

// gcpRegionFanoutScanIn is the testable core of gcpRegionFanoutScan: same
// fan-out + accumulate + upsert pipeline, but takes a pre-resolved region
// slice instead of calling gcpRegions. Lets unit tests inject an arbitrary
// region list (and skip the compute.Regions.List dependency).
func gcpRegionFanoutScanIn[P any, T any](
	ctx context.Context,
	p *project,
	st *store.Store,
	concurrency int,
	regions []string,
	action string,
	pagerFn func(region string) pager[P],
	pageItems func(*P) []T,
	itemToResource func(item T, region string) *store.Resource,
) (total, inserted int, err error) {
	if len(regions) == 0 {
		return 0, 0, nil
	}
	var (
		mu    sync.Mutex
		batch []*store.Resource
	)
	if err := forEachItem(ctx, concurrency, regions, func(gctx context.Context, region string) error {
		err := pagerFn(region).Pages(gctx, func(page *P) error {
			items := pageItems(page)
			local := make([]*store.Resource, 0, len(items))
			for _, it := range items {
				if r := itemToResource(it, region); r != nil {
					local = append(local, r)
				}
			}
			if len(local) > 0 {
				mu.Lock()
				batch = append(batch, local...)
				mu.Unlock()
			}
			return nil
		})
		if err != nil {
			if isPermissionDenied(err) {
				return skipIfDenied(st, action, p.ID+"/"+region, err)
			}
			return err
		}
		return nil
	}); err != nil {
		return 0, 0, err
	}
	if len(batch) == 0 {
		return 0, 0, nil
	}
	return upsertWithProjClosure(p, st, batch)
}

// buildSAEmailIndex returns email → resource ID for every
// gcp:iam:service-account in the project. Used by every resolver that
// FK-checks a runtime SA email against the project's discovered SAs (Cloud
// Functions, Cloud Run, Composer, Cloud Build, BinAuth attestor, Cloud Run
// Jobs, Batch, IAM-policy bindings). Cross-project SA refs implicitly skip
// — won't match in-project index.
func buildSAEmailIndex(p *project, st *store.Store) (map[string]string, error) {
	sas, err := st.ListResources(store.ResourceFilter{
		Provider: "gcp", AccountID: p.ID, Types: []string{TypeIAMServiceAccount},
		Limit: util.AllResources,
	})
	if err != nil {
		return nil, err
	}
	out := make(map[string]string, len(sas))
	for _, sa := range sas {
		if i := strings.LastIndex(sa.NativeID, "/"); i >= 0 {
			out[sa.NativeID[i+1:]] = sa.ID
		}
	}
	return out, nil
}

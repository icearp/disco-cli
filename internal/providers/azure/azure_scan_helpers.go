package azure

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync"

	"codeberg.org/icearp/disco/store"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
	"golang.org/x/sync/errgroup"
	"golang.org/x/sync/semaphore"
)

// Generic paginate-and-upsert helpers used by per-service scanners. Lifted
// out of azure.go to keep the orchestration file focused on Scanner /
// Scan / scanSubscription. Documented under "Helpers (reuse before
// reinventing)" in azure/CLAUDE.md.

// azPager is satisfied by every Azure SDK paginator (More/NextPage pair).
type azPager[P any] interface {
	More() bool
	NextPage(context.Context) (P, error)
}

// azRunPhases runs the given scan phases concurrently, summing their (total,
// inserted) counts and returning the first non-nil error. Each phase is one
// azSimpleScan / azRGFanoutScan call. Use for multi-type single-service
// scanners (e.g. azurestackhci, connectedvmware) instead of hand-rolling the
// WaitGroup + mutex aggregation each time.
func azRunPhases(phases ...func() (int, int, error)) (total, inserted int, err error) {
	var mu sync.Mutex
	var wg sync.WaitGroup
	for _, p := range phases {
		wg.Go(func() {
			t, i, e := p()
			mu.Lock()
			total += t
			inserted += i
			if e != nil && err == nil {
				err = e
			}
			mu.Unlock()
		})
	}
	wg.Wait()
	return total, inserted, err
}

// azPageScan runs a paginated Azure list call, converting each page to resources
// and hierarchy pairs via toResources, then upserts both. Handles access-denied
// by logging and returning nil.
func azPageScan[P any](
	ctx context.Context,
	action string,
	sub *subscription,
	st *store.Store,
	pager azPager[P],
	toResources func(P) ([]*store.Resource, [][2]string),
) (total, inserted int, err error) {
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			if isSkippableScanError(err) {
				return total, inserted, skipIfAccessDenied(st, action, sub.ID, err)
			}
			return total, inserted, fmt.Errorf("%s: %w", action, err)
		}
		batch, pairs := toResources(page)
		if len(batch) == 0 {
			continue
		}
		n, err := st.UpsertResources(batch)
		if err != nil {
			return total, inserted, fmt.Errorf("upsert %s: %w", action, err)
		}
		total += len(batch)
		inserted += n
		if len(pairs) > 0 {
			if err := st.RecordHierarchyBatch(pairs); err != nil {
				return total, inserted, fmt.Errorf("closure %s: %w", action, err)
			}
		}
	}
	return
}

// azTrackedBase is the four-field shape every Azure SDK tracked-resource type
// satisfies: ID, Name, Location, Tags. Per-type extractors return this base
// (plus the original SDK pointer in `full` for AttributesJSON serialization)
// so generic helpers can build store.Resource batches without knowing the
// concrete SDK type.
type azTrackedBase struct {
	id, name, location string
	tags               map[string]*string
	// managed flags provider-managed resources (auto-present at subscription
	// creation, or created indirectly by another customer action). Zero value
	// false keeps every existing extractor unchanged.
	managed bool
	full    any
}

// azTrackedRows builds a store.Resource batch + RG hierarchy pairs from a
// slice of SDK tracked-resource pointers. Generalizes the wanRows precedent.
// Items with nil pointers or empty IDs are skipped. RG hierarchy pairs are
// emitted only when the ID contains a /resourceGroups/ segment.
func azTrackedRows[T any](sub *subscription, scanID, rtype string, items []*T, extract func(*T) azTrackedBase) ([]*store.Resource, [][2]string) {
	var batch []*store.Resource
	var pairs [][2]string
	for _, item := range items {
		if item == nil {
			continue
		}
		b := extract(item)
		if b.id == "" {
			continue
		}
		batch = append(batch, &store.Resource{
			Provider: "azure", AccountID: sub.ID, AccountName: &sub.Name,
			Type: rtype, NativeID: b.id,
			Name: &b.name, Region: &b.location,
			TagsJSON: azTagsJSON(b.tags), AttributesJSON: mustJSON(b.full),
			ManagedByProvider: b.managed,
			DiscoveredBy:      scanID,
		})
		if rgFromID(b.id) != "" {
			pairs = append(pairs, rgHierarchyPair(sub, rtype, b.id))
		}
	}
	return batch, pairs
}

// listSubscriptionRGNames enumerates all resource groups in the subscription
// via the ARM resource-groups SDK. Used by azRGFanoutScan and any other
// caller that needs RG names without depending on scan order against the
// `azure:resourcegroups` service. AccessDenied tolerated (returns empty
// slice + nil error). Imported as a reverse dep on `armresources` already
// pulled in by `resourcegroups_scanners.go`.
func listSubscriptionRGNames(ctx context.Context, sub *subscription, cred *azidentity.DefaultAzureCredential) ([]string, error) {
	client, err := armresources.NewResourceGroupsClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return nil, fmt.Errorf("armresources:NewResourceGroupsClient: %w", err)
	}
	var out []string
	pager := client.NewListPager(nil)
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			if isSkippableScanError(err) {
				return nil, nil
			}
			return nil, fmt.Errorf("armresources:ResourceGroups.List: %w", err)
		}
		for _, rg := range page.Value {
			if rg == nil || rg.Name == nil {
				continue
			}
			out = append(out, *rg.Name)
		}
	}
	return out, nil
}

// azRGFanoutScan covers ARM resource types that have NO subscription-wide list
// API (only per-RG list endpoints). It enumerates RGs via
// listSubscriptionRGNames once, then fans out per-RG list calls bounded by
// maxConcurrentFanout (errgroup + semaphore). pagerFn returns a fresh pager
// for each RG; pageItems extracts the SDK slice; extract converts each item
// to azTrackedBase. Same shape as azSimpleScan but with a per-RG dimension.
//
// Per-RG AccessDenied + ResourceGroupNotFound errors are tolerated and skip
// that RG silently — partial-cred scans don't fail wholesale. Other errors
// abort. Hierarchy pairs to RG are emitted automatically when the resource
// ID contains a `/resourceGroups/` segment (consistent with azTrackedRows).
//
// Use when adding scanners for RG-scoped types like classic
// VirtualNetworkGateways, ExpressRouteGateways, Front Door endpoints, ADF
// linked services, Logic Apps API connections, etc.
func azRGFanoutScan[T any, P any](
	ctx context.Context,
	action, rtype string,
	sub *subscription,
	cred *azidentity.DefaultAzureCredential,
	st *store.Store,
	scanID string,
	pagerFn func(rg string) azPager[P],
	pageItems func(P) []*T,
	extract func(*T) azTrackedBase,
) (total, inserted int, err error) {
	rgs, err := listSubscriptionRGNames(ctx, sub, cred)
	if err != nil {
		return 0, 0, err
	}
	if len(rgs) == 0 {
		return 0, 0, nil
	}

	var (
		mu       sync.Mutex
		allBatch []*store.Resource
		allPairs [][2]string
	)
	sem := semaphore.NewWeighted(maxConcurrentFanout)
	g, gctx := errgroup.WithContext(ctx)
	for _, rg := range rgs {
		g.Go(func() error {
			if err := sem.Acquire(gctx, 1); err != nil {
				return err
			}
			defer sem.Release(1)
			pager := pagerFn(rg)
			for pager.More() {
				page, err := pager.NextPage(gctx)
				if err != nil {
					if isSkippableScanError(err) || isResourceGroupNotFound(err) {
						return nil
					}
					return fmt.Errorf("%s rg=%s: %w", action, rg, err)
				}
				batch, pairs := azTrackedRows(sub, scanID, rtype, pageItems(page), extract)
				if len(batch) == 0 {
					continue
				}
				mu.Lock()
				allBatch = append(allBatch, batch...)
				allPairs = append(allPairs, pairs...)
				mu.Unlock()
			}
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return 0, 0, err
	}
	if len(allBatch) == 0 {
		return 0, 0, nil
	}
	n, err := st.UpsertResources(allBatch)
	if err != nil {
		return 0, 0, fmt.Errorf("upsert %s: %w", action, err)
	}
	if len(allPairs) > 0 {
		if err := st.RecordHierarchyBatch(allPairs); err != nil {
			return len(allBatch), n, fmt.Errorf("closure %s: %w", action, err)
		}
	}
	return len(allBatch), n, nil
}

// isResourceGroupNotFound reports whether err is an Azure 404 RG-vanished
// error (race between RG-list and per-RG list calls). Treated as
// best-effort skip in azRGFanoutScan.
func isResourceGroupNotFound(err error) bool {
	var respErr *azcore.ResponseError
	if errors.As(err, &respErr) {
		return respErr.StatusCode == http.StatusNotFound
	}
	return false
}

// azSimpleScan wires NewListPager → azPageScan → azTrackedRows in a single
// call. Use for simple subscription-scoped list scanners with no sub-resource
// fanout. The scanner shrinks to: build SDK client, call azSimpleScan, supply
// (pageItems, extract) functions.
func azSimpleScan[T any, P any](
	ctx context.Context,
	action, rtype string,
	sub *subscription,
	st *store.Store,
	scanID string,
	pager azPager[P],
	pageItems func(P) []*T,
	extract func(*T) azTrackedBase,
) (total, inserted int, err error) {
	return azPageScan(ctx, action, sub, st, pager, func(page P) ([]*store.Resource, [][2]string) {
		return azTrackedRows(sub, scanID, rtype, pageItems(page), extract)
	})
}

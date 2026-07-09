package azure

import (
	"context"
	"fmt"
	"sync"

	"codeberg.org/icearp/disco/internal/providers/azure/azureregions"
	"codeberg.org/icearp/disco/internal/restype"
	"codeberg.org/icearp/disco/store"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/quota/armquota"
	"golang.org/x/sync/errgroup"
	"golang.org/x/sync/semaphore"
)

func init() {
	registerType(restype.Descriptor{Type: TypeQuotaLimit, Service: "microsoft.quota", Leaf: true})
	// Registration gate caveat: providerDisabled (azure_scanner.go) skips this
	// scanner only if a subscription's Providers/List reports microsoft.quota as
	// known-and-unregistered. Microsoft.Quota is a registration-free proxy RP
	// serving Compute/Network/ML quotas regardless of its own registration, so
	// in practice it's absent-or-registered and the scanner runs; a sub
	// explicitly listing it unregistered would be skipped (acceptable — no
	// quota RBAC there anyway).
	registerService(serviceEntry{
		name: "azure:microsoft.quota",
		fn:   scanQuotaLimits,
	})
}

// quotaProviderNamespaces are the resource-provider namespaces the unified
// Microsoft.Quota proxy serves limits for. Extensible: any (namespace, region)
// pair the proxy doesn't support returns a skippable error and is dropped, so
// over-listing is harmless.
var quotaProviderNamespaces = []string{
	"Microsoft.Compute",
	"Microsoft.Network",
	"Microsoft.MachineLearningServices",
}

// scanQuotaLimits discovers service quota *limits* (not usage) via the unified
// Microsoft.Quota proxy. The proxy is scope-addressed — one List per
// (provider-namespace, location) — so this fans out the cartesian product of
// quotaProviderNamespaces × azureregions.Regions, bounded by maxConcurrentFanout.
// Each limit is stored limit-only, so the resource version chain bumps only on
// an actual quota change (grant/reduction) — churn-free change-over-time
// history. The serialized CurrentQuotaLimitBase carries no usage, etag, or
// systemData timestamp (omits ProxyResource/SystemData), so the only
// theoretical churn source is the opaque RP-specific Properties.Properties
// bag — empty for the compute/network/ML namespaces scanned here.
func scanQuotaLimits(ctx context.Context, sub *subscription, cred azcore.TokenCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	client, err := armquota.NewClient(cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armquota:NewClient: %w", err)
	}
	return scanQuotaLimitsWithClient(ctx, sub, st, scanID, client, quotaProviderNamespaces, azureregions.Regions)
}

// quotaLister is the slice of armquota.Client used by the scanner body — a seam
// so tests can drive a fake-transport-backed client over a small (namespace,
// region) grid.
type quotaLister interface {
	NewListPager(scope string, options *armquota.ClientListOptions) *runtime.Pager[armquota.ClientListResponse]
}

func scanQuotaLimitsWithClient(ctx context.Context, sub *subscription, st *store.Store, scanID string, client quotaLister, namespaces, regions []string) (total, inserted int, err error) {
	var (
		mu       sync.Mutex
		allBatch []*store.Resource
	)
	sem := semaphore.NewWeighted(maxConcurrentFanout)
	g, gctx := errgroup.WithContext(ctx)
	for _, ns := range namespaces {
		for _, region := range regions {
			g.Go(func() error {
				if err := sem.Acquire(gctx, 1); err != nil {
					return err
				}
				defer sem.Release(1)
				scope := fmt.Sprintf("/subscriptions/%s/providers/%s/locations/%s", sub.ID, ns, region)
				pager := client.NewListPager(scope, nil)
				for pager.More() {
					page, err := pager.NextPage(gctx)
					if err != nil {
						// Most (namespace, region) pairs the proxy doesn't serve
						// (RP unregistered, quota unsupported in region) — skip
						// that combo, never fail the whole scanner.
						if isSkippableScanError(err) {
							return nil
						}
						return fmt.Errorf("armquota:Quota.List %s/%s: %w", ns, region, err)
					}
					batch, _ := azTrackedRows(sub, scanID, TypeQuotaLimit, page.Value, quotaExtract(scope, region))
					if len(batch) == 0 {
						continue
					}
					mu.Lock()
					allBatch = append(allBatch, batch...)
					mu.Unlock()
				}
				return nil
			})
		}
	}
	if err := g.Wait(); err != nil {
		return 0, 0, err
	}
	if len(allBatch) == 0 {
		return 0, 0, nil
	}
	n, err := st.UpsertResources(allBatch)
	if err != nil {
		return 0, 0, fmt.Errorf("upsert quota limits: %w", err)
	}
	return len(allBatch), n, nil
}

// quotaExtract maps a Microsoft.Quota limit to azTrackedBase. The proxy returns
// a stable ARM ID (…/Microsoft.Quota/quotas/{name}); when absent it is
// synthesized from the scope + resource name so the natural key (and thus the
// version chain) stays stable across scans. Region is the scope's location —
// the SDK item carries none. Quotas are neither RG- nor parent-scoped, so no
// hierarchy pairs are emitted (azTrackedRows only pairs IDs under a resource
// group).
func quotaExtract(scope, region string) func(*armquota.CurrentQuotaLimitBase) azTrackedBase {
	return func(q *armquota.CurrentQuotaLimitBase) azTrackedBase {
		name := quotaResourceName(q)
		id := sv(q.ID)
		if id == "" {
			if name == "" {
				return azTrackedBase{}
			}
			id = scope + "/providers/Microsoft.Quota/quotas/" + name
		}
		// managed: quotas materialize automatically per subscription and cannot be
		// deleted — hidden from default list/graph unless --include-managed (kept
		// consistent with aws:servicequotas:quota).
		return azTrackedBase{id: id, name: name, location: region, managed: true, full: q}
	}
}

// quotaResourceName prefers the resource-provider's quota name
// (Properties.Name.Value, e.g. "standardDDv4Family") over the wrapper resource
// name, falling back to the latter.
func quotaResourceName(q *armquota.CurrentQuotaLimitBase) string {
	if q.Properties != nil && q.Properties.Name != nil {
		if v := sv(q.Properties.Name.Value); v != "" {
			return v
		}
	}
	return sv(q.Name)
}

package azure

import (
	"context"
	"fmt"
	"sync"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/quota/armquota"
	"github.com/icearp/disco-cli/internal/providers/azure/azureregions"
	"github.com/icearp/disco-cli/store"
	"golang.org/x/sync/errgroup"
	"golang.org/x/sync/semaphore"
)

func init() {
	// No registerType. A quota limit is a value, not a resource: nothing
	// provisions it, it has no graph edges, and it is queried by namespace and
	// by proximity to the limit. It lands in the `quotas` table instead.
	//
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
// Microsoft.Quota proxy, storing them in the `quotas` table. The proxy is
// scope-addressed — one List per (provider-namespace, location) — so this fans
// out the cartesian product of quotaProviderNamespaces × azureregions.Regions,
// bounded by maxConcurrentFanout.
//
// Each limit is stored limit-only, so the version chain bumps only on an actual
// quota change (grant/reduction) — churn-free change-over-time history.
// CurrentQuotaLimitBase carries no usage, etag or systemData timestamp (it omits
// ProxyResource/SystemData), and armquota.Properties holds only Limit, Name,
// ResourceType, Unit, QuotaPeriod and IsQuotaApplicable — so the only
// theoretical churn source is the opaque RP-specific Properties.Properties bag,
// empty for the compute/network/ML namespaces scanned here.
//
// Unlike AWS this scanner is not opt-in, so every Azure scan records quotas.
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
		allBatch []*store.Quota
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
					var batch []*store.Quota
					for _, item := range page.Value {
						if q, ok := quotaRow(sub, scanID, ns, region, item); ok {
							batch = append(batch, q)
						}
					}
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
	n, err := st.UpsertQuotas(allBatch)
	if err != nil {
		return 0, 0, fmt.Errorf("upsert quota limits: %w", err)
	}
	return len(allBatch), n, nil
}

// quotaRow maps one Microsoft.Quota limit onto store.Quota, or returns
// (nil,false) when it cannot be addressed.
//
// The identity is (provider, subscription, region, namespace, quota name). The
// resource-provider's own quota name (Properties.Name.Value, e.g.
// "standardDDv4Family") is the quota code; the ARM ID is not the identity but
// is preserved in the attributes remainder alongside everything else the proxy
// reported. Region is the scope's location — the SDK item carries none.
//
// IsQuotaApplicable maps to adjustable: it is Azure's statement of whether a
// quota can be requested for this resource. A limit it reports as
// non-applicable moves only when Microsoft moves it.
func quotaRow(sub *subscription, scanID, namespace, region string, q *armquota.CurrentQuotaLimitBase) (*store.Quota, bool) {
	code := quotaResourceName(q)
	if code == "" {
		return nil, false
	}

	out := &store.Quota{
		Provider:       "azure",
		AccountID:      sub.ID,
		AccountName:    &sub.Name,
		Region:         region,
		ServiceCode:    namespace,
		QuotaCode:      code,
		Name:           quotaDisplayName(q, code),
		Value:          quotaLimitValue(q),
		AttributesJSON: mustJSON(q),
		DiscoveredBy:   scanID,
	}
	if q.Properties != nil {
		if q.Properties.IsQuotaApplicable != nil {
			out.Adjustable = *q.Properties.IsQuotaApplicable
		}
		if unit := sv(q.Properties.Unit); unit != "" {
			out.Unit = &unit
		}
	}
	return out, true
}

// quotaLimitValue extracts the numeric limit. Limit is a polymorphic JSON
// object; only LimitObject carries a value, and anything else leaves the column
// NULL rather than inventing a zero — a limit of nothing and an unreported limit
// are different facts.
func quotaLimitValue(q *armquota.CurrentQuotaLimitBase) *float64 {
	if q.Properties == nil {
		return nil
	}
	lo, ok := q.Properties.Limit.(*armquota.LimitObject)
	if !ok || lo == nil || lo.Value == nil {
		return nil
	}
	v := float64(*lo.Value)
	return &v
}

// quotaDisplayName prefers the resource provider's localized display name,
// falling back to the quota code so the column is never empty.
func quotaDisplayName(q *armquota.CurrentQuotaLimitBase, code string) string {
	if q.Properties != nil && q.Properties.Name != nil {
		if v := sv(q.Properties.Name.LocalizedValue); v != "" {
			return v
		}
	}
	return code
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

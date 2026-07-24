package aws

import "github.com/icearp/disco-cli/store"

func init() {
	registerResolver(resolveBillingViews)
}

// resolveBillingViews is an intentional no-op audit stub: BillingViewListElement
// carries no cross-resource ARNs at list-payload fidelity — OwnerAccountId /
// SourceAccountId are bare 12-digit account IDs (not ARNs); HealthStatus,
// BillingViewType, Name, Description are scalars. Source-view → derived-view
// edges live behind `billing:ListSourceViewsForBillingView`, a per-view
// fan-out warranting its own session — deferred.
func resolveBillingViews(_ *account, _ *store.Store) error {
	return nil
}

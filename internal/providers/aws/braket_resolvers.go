package aws

import "github.com/icearp/disco-cli/store"

func init() {
	registerResolver(resolveBraketSpendingLimits)
}

// resolveBraketSpendingLimits is an intentional no-op audit stub: SpendingLimitSummary's
// only ARN-bearing field, `DeviceArn`, points at AWS-managed Braket quantum devices (e.g.
// `arn:aws:braket:::device/qpu/...`) — not modeled as a disco type (public, unscanned,
// AWS-owned). Emitting an edge to an unscanned target would FK-blow `relationships.to_id`.
// If an `aws:braket:device` scanner lands, wire `spending-limit → device` (uses) here.
func resolveBraketSpendingLimits(_ *account, _ *store.Store) error {
	return nil
}

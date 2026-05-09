package aws

import "codeberg.org/icearp/disco/store"

func init() {
	registerResolver(resolveBraketSpendingLimits)
}

// resolveBraketSpendingLimits is intentionally a no-op audit stub.
// SpendingLimitSummary's only ARN-bearing field is `DeviceArn`, which points
// at AWS-managed Braket quantum devices (e.g.
// `arn:aws:braket:::device/qpu/...`). Those devices are not modeled as a
// disco type — they are public, unscanned, AWS-owned. Emitting an edge to
// an unscanned target would FK-blow on `relationships.to_id`. If a future
// `aws:braket:device` scanner lands, wire `spending-limit → device` (uses)
// here.
func resolveBraketSpendingLimits(_ *account, _ *store.Store) error {
	return nil
}

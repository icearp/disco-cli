package aws

import "github.com/icearp/disco-cli/store"

func init() { registerResolver(resolveBcmPricingCalculatorRelationships) }

// resolveBcmPricingCalculatorRelationships is a no-op audit-stub.
//
// Natural edge: bill-scenario → Cost Explorer cost category via
// `CostCategoryGroupSharingPreferenceArn`, but disco doesn't yet model
// `aws:ce:cost-category`. Bill-scenario rows still upsert; this resolver
// registers only so the registration test stays uniform. Wire the edge
// once Cost Explorer cost-category scanning lands.
func resolveBcmPricingCalculatorRelationships(acct *account, st *store.Store) error {
	_ = acct
	_ = st
	return nil
}

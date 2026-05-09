package aws

import "codeberg.org/icearp/disco/store"

func init() { registerResolver(resolveBcmPricingCalculatorRelationships) }

// resolveBcmPricingCalculatorRelationships is a no-op audit-stub.
//
// The natural edge here is bill-scenario → Cost Explorer cost category via
// `CostCategoryGroupSharingPreferenceArn`, but disco does not yet model
// `aws:ce:cost-category` resources. Bill-scenario rows still upsert; this
// resolver registers so the resolver-registration test stays uniform.
// Wire the edge once Cost Explorer cost-category scanning lands.
func resolveBcmPricingCalculatorRelationships(acct *account, st *store.Store) error {
	_ = acct
	_ = st
	return nil
}

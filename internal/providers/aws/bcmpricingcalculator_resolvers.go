package aws

import "github.com/icearp/disco-cli/store"

func init() {
	registerResolver(resolveBcmPricingCalculatorRelationships)
	registerResolver(
		noopBcmPCHierarchy,
		EdgeDecl{TypeBcmPricingCalculatorBillScenario, TypeBcmPricingCalculatorBillScenarioCommitmentModification, store.RelContains},
		EdgeDecl{TypeBcmPricingCalculatorBillScenario, TypeBcmPricingCalculatorBillScenarioUsageModification, store.RelContains},
		EdgeDecl{TypeBcmPricingCalculatorWorkloadEstimate, TypeBcmPricingCalculatorWorkloadEstimateUsage, store.RelContains},
	)
}

// noopBcmPCHierarchy only carries the EdgeDecl metadata for the contains edges
// the scanner records itself (bcmPCChildFanOut, through upsertContained).
func noopBcmPCHierarchy(_ *account, _ *store.Store) error {
	return nil
}

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

package azure

import "testing"

// TestLeafTypesNotResolverSources guards against marking a type as leaf
// (Leaf: true on the scanner's emits decl) when a resolver actually emits
// edges from it. Such a misclassification silently hides the type from
// `disco coverage resolvers --missing` without removing the resolver.
func TestLeafTypesNotResolverSources(t *testing.T) {
	sources := map[string]bool{}
	for _, e := range CollectResolverEdges() {
		sources[e.Source] = true
	}
	for _, decl := range CollectEmits() {
		if !decl.Leaf {
			continue
		}
		if sources[decl.DiscoType] {
			t.Errorf("emits[%q] flagged Leaf: true but type appears as resolver source — drop the Leaf flag or remove the resolver", decl.DiscoType)
		}
	}
}

// TestSyntheticLimitedToCrossScopeStubs enforces the narrowed Synthetic
// definition. Azure has no fabricated cross-scope stubs today (its Entra
// identities and SQL/network proxy children are all SDK-scanned), so NO emit
// may carry Synthetic: true — such types must use Uncatalogued instead.
func TestSyntheticLimitedToCrossScopeStubs(t *testing.T) {
	allowed := map[string]bool{}
	for _, decl := range CollectEmits() {
		if decl.Synthetic && !allowed[decl.DiscoType] {
			t.Errorf("emits[%q] flagged Synthetic: true but is not a cross-scope stub — use Uncatalogued for SDK-scanned types absent from the registry", decl.DiscoType)
		}
	}
}

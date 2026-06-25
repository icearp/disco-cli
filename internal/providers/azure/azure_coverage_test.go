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

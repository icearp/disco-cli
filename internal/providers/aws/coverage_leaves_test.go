package aws

import (
	"testing"
)

// TestLeafTypesNotResolverSources guards against marking a type as leaf
// when a resolver actually emits edges from it. Such a misclassification
// silently hides the type from `disco coverage --missing-resolvers`
// without removing the resolver — bug-attractant.
func TestLeafTypesNotResolverSources(t *testing.T) {
	sources := map[string]bool{}
	for _, e := range CollectResolverEdges() {
		sources[e.Source] = true
	}
	for ltype := range leafTypes {
		if sources[ltype] {
			t.Errorf("leafTypes[%q] = true but type appears as resolver source — drop the leaf flag or remove the resolver", ltype)
		}
	}
}

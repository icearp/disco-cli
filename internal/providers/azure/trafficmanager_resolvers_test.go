package azure

import (
	"testing"

	"codeberg.org/icearp/disco/internal/store"
)

// TestResolveTrafficManagerRelationships verifies a TM profile derives a
// -[uses]-> edge to each AzureEndpoint backend whose targetResourceId
// matches a known resource. ExternalEndpoints (FQDN target, no
// targetResourceId) produce no edge.
func TestResolveTrafficManagerRelationships(t *testing.T) {
	st := newTestStore(t)
	sub := newTestSubscription("sub-tm")

	pipNativeID := "/subscriptions/sub-tm/resourceGroups/Net/providers/Microsoft.Network/publicIPAddresses/pip1"
	pipID := upsertTestResource(t, st, "azure", sub.ID, TypeNetworkPublicIPAddress, pipNativeID, "eastus", "{}")

	profileAttrs := `{"properties":{"endpoints":[{"name":"azure-ep","type":"Microsoft.Network/trafficManagerProfiles/azureEndpoints","properties":{"targetResourceId":"` + pipNativeID + `"}},{"name":"ext-ep","type":"Microsoft.Network/trafficManagerProfiles/externalEndpoints","properties":{"target":"www.contoso.com"}}]}}`
	profileID := upsertTestResource(t, st, "azure", sub.ID, TypeNetworkTrafficManagerProfile,
		"/subscriptions/sub-tm/resourceGroups/RG/providers/Microsoft.Network/trafficManagerProfiles/profile1", "global", profileAttrs)

	if err := resolveTrafficManagerRelationships(sub, st); err != nil {
		t.Fatalf("resolveTrafficManagerRelationships: %v", err)
	}
	rels, _ := st.RelationshipsFrom(profileID)
	if len(rels) != 1 || rels[0].ToID != pipID || rels[0].Kind != store.RelUses {
		t.Errorf("expected profile -[uses]-> pip, got %+v", rels)
	}
}

package azure

import (
	"testing"

	"codeberg.org/icearp/disco/store"
)

// TestResolveContainerAppEnvironments verifies app -[attached-to]-> env and
// env -[attached-to]-> VNet edges (via vnetConfiguration.infrastructureSubnetId).
func TestResolveContainerAppEnvironments(t *testing.T) {
	st := newTestStore(t)
	sub := newTestSubscription("sub-ca")

	vnetNativeID := "/subscriptions/sub-ca/resourceGroups/Net/providers/Microsoft.Network/virtualNetworks/vnet1"
	subnetNativeID := vnetNativeID + "/subnets/cae-infra"
	vnetID := upsertTestResource(t, st, "azure", sub.ID, TypeNetworkVirtualNetwork, vnetNativeID, "eastus", "{}")

	envNativeID := "/subscriptions/sub-ca/resourceGroups/RG/providers/Microsoft.App/managedEnvironments/cae"
	envAttrs := `{"properties":{"vnetConfiguration":{"infrastructureSubnetId":"` + subnetNativeID + `"}}}`
	envID := upsertTestResource(t, st, "azure", sub.ID, TypeAppContainersManagedEnvironment, envNativeID, "eastus", envAttrs)

	appNativeID := "/subscriptions/sub-ca/resourceGroups/RG/providers/Microsoft.App/containerApps/myapp"
	// Mixed casing on env reference.
	appAttrs := `{"properties":{"managedEnvironmentId":"/SUBSCRIPTIONS/SUB-CA/RESOURCEGROUPS/RG/PROVIDERS/MICROSOFT.APP/MANAGEDENVIRONMENTS/CAE"}}`
	appID := upsertTestResource(t, st, "azure", sub.ID, TypeAppContainersContainerApp, appNativeID, "eastus", appAttrs)

	if err := resolveContainerAppEnvironments(sub, st); err != nil {
		t.Fatalf("resolveContainerAppEnvironments: %v", err)
	}

	appRels, _ := st.RelationshipsFrom(appID)
	if len(appRels) != 1 || appRels[0].ToID != envID || appRels[0].Kind != store.RelAttachedTo {
		t.Errorf("expected app->env, got %+v", appRels)
	}
	envRels, _ := st.RelationshipsFrom(envID)
	if len(envRels) != 1 || envRels[0].ToID != vnetID || envRels[0].Kind != store.RelAttachedTo {
		t.Errorf("expected env->vnet, got %+v", envRels)
	}
}

// TestResolveContainerAppRegistries verifies app -[uses]-> ACR via the
// configuration.registries[].server FQDN, parsing leading subdomain against
// the per-sub registry-name index.
func TestResolveContainerAppRegistries(t *testing.T) {
	st := newTestStore(t)
	sub := newTestSubscription("sub-ca")

	regID := upsertTestResource(t, st, "azure", sub.ID, TypeContainerRegistryRegistry,
		"/subscriptions/sub-ca/resourceGroups/RG/providers/Microsoft.ContainerRegistry/registries/myreg", "eastus", "{}")

	appID := upsertTestResource(t, st, "azure", sub.ID, TypeAppContainersContainerApp,
		"/subscriptions/sub-ca/resourceGroups/RG/providers/Microsoft.App/containerApps/app",
		"eastus",
		`{"properties":{"configuration":{"registries":[{"server":"MYREG.azurecr.io"}]}}}`)

	if err := resolveContainerAppRegistries(sub, st); err != nil {
		t.Fatalf("resolveContainerAppRegistries: %v", err)
	}
	rels, _ := st.RelationshipsFrom(appID)
	if len(rels) != 1 || rels[0].ToID != regID || rels[0].Kind != store.RelUses {
		t.Errorf("expected app -[uses]-> acr, got %+v", rels)
	}
}

// TestResolveContainerInstanceVNets verifies ACI group -[attached-to]-> VNet
// via subnetIds[].id.
func TestResolveContainerInstanceVNets(t *testing.T) {
	st := newTestStore(t)
	sub := newTestSubscription("sub-aci")

	vnetNativeID := "/subscriptions/sub-aci/resourceGroups/Net/providers/Microsoft.Network/virtualNetworks/vnet"
	subnetNativeID := vnetNativeID + "/subnets/aci"
	vnetID := upsertTestResource(t, st, "azure", sub.ID, TypeNetworkVirtualNetwork, vnetNativeID, "eastus", "{}")

	groupID := upsertTestResource(t, st, "azure", sub.ID, TypeContainerInstanceContainerGroup,
		"/subscriptions/sub-aci/resourceGroups/RG/providers/Microsoft.ContainerInstance/containerGroups/g",
		"eastus",
		`{"properties":{"subnetIds":[{"id":"`+subnetNativeID+`"}]}}`)

	if err := resolveContainerInstanceVNets(sub, st); err != nil {
		t.Fatalf("resolveContainerInstanceVNets: %v", err)
	}
	rels, _ := st.RelationshipsFrom(groupID)
	if len(rels) != 1 || rels[0].ToID != vnetID || rels[0].Kind != store.RelAttachedTo {
		t.Errorf("expected aci -[attached-to]-> vnet, got %+v", rels)
	}
}

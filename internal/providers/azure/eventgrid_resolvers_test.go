package azure

import (
	"testing"

	"codeberg.org/icearp/disco/internal/store"
)

// TestResolveEventGridRelationships covers the four edges the resolver emits:
// ES → topic (attached-to via properties.topic), ES → destination (uses via
// properties.destination.properties.resourceId), ES → dead-letter storage
// (uses via properties.deadLetterDestination.properties.resourceId), and
// system-topic → source (uses via properties.source). Also exercises the PE-
// style trim path: a Service Bus topic destination resourceId carries the
// topic suffix `…/topics/foo`; the per-sub index keys on the namespace, so the
// resolver progressively strips trailing /-segments to recover the parent.
func TestResolveEventGridRelationships(t *testing.T) {
	st := newTestStore(t)
	sub := newTestSubscription("sub-eg")

	topicID := upsertTestResource(t, st, "azure", sub.ID, TypeEventGridTopic,
		"/subscriptions/sub-eg/resourceGroups/RG/providers/Microsoft.EventGrid/topics/topic1", "eastus", "{}")
	stoID := upsertTestResource(t, st, "azure", sub.ID, TypeStorageStorageAccount,
		"/subscriptions/sub-eg/resourceGroups/RG/providers/Microsoft.Storage/storageAccounts/dlq", "eastus", "{}")
	sbnsID := upsertTestResource(t, st, "azure", sub.ID, TypeServiceBusNamespace,
		"/subscriptions/sub-eg/resourceGroups/RG/providers/Microsoft.ServiceBus/namespaces/sbns", "eastus", "{}")
	srcVMID := upsertTestResource(t, st, "azure", sub.ID, TypeComputeVirtualMachine,
		"/subscriptions/sub-eg/resourceGroups/RG/providers/Microsoft.Compute/virtualMachines/vm1", "eastus", "{}")

	esAttrs := `{"properties":{
		"topic":"/subscriptions/sub-eg/resourceGroups/RG/providers/Microsoft.EventGrid/topics/topic1",
		"destination":{"endpointType":"ServiceBusTopic","properties":{"resourceId":"/subscriptions/sub-eg/resourceGroups/RG/providers/Microsoft.ServiceBus/namespaces/sbns/topics/foo"}},
		"deadLetterDestination":{"endpointType":"StorageBlob","properties":{"resourceId":"/subscriptions/sub-eg/resourceGroups/RG/providers/Microsoft.Storage/storageAccounts/dlq","blobContainerName":"dlq-container"}}
	}}`
	esID := upsertTestResource(t, st, "azure", sub.ID, TypeEventGridEventSubscription,
		"/subscriptions/sub-eg/providers/Microsoft.EventGrid/eventSubscriptions/es1", "", esAttrs)

	stAttrs := `{"properties":{"source":"/subscriptions/sub-eg/resourceGroups/RG/providers/Microsoft.Compute/virtualMachines/vm1","topicType":"Microsoft.Resources.ResourceGroups"}}`
	systID := upsertTestResource(t, st, "azure", sub.ID, TypeEventGridSystemTopic,
		"/subscriptions/sub-eg/resourceGroups/RG/providers/Microsoft.EventGrid/systemTopics/sys1", "global", stAttrs)

	if err := resolveEventGridRelationships(sub, st); err != nil {
		t.Fatalf("resolveEventGridRelationships: %v", err)
	}

	rels, _ := st.RelationshipsFrom(esID)
	hits := map[string]string{}
	for _, r := range rels {
		hits[r.ToID] = r.Kind
	}
	if hits[topicID] != store.RelAttachedTo {
		t.Errorf("missing ES → topic attached-to, got %v", rels)
	}
	if hits[sbnsID] != store.RelUses {
		t.Errorf("missing ES → SB namespace uses (trim path), got %v", rels)
	}
	if hits[stoID] != store.RelUses {
		t.Errorf("missing ES → storage dlq uses, got %v", rels)
	}

	srels, _ := st.RelationshipsFrom(systID)
	if len(srels) != 1 || srels[0].ToID != srcVMID || srels[0].Kind != store.RelUses {
		t.Errorf("system-topic → source: expected uses to %s, got %v", srcVMID, srels)
	}
}

// TestResolveEventGridRelationships_Empty asserts no panic on missing attrs.
func TestResolveEventGridRelationships_Empty(t *testing.T) {
	st := newTestStore(t)
	sub := newTestSubscription("sub-eg-empty")
	upsertTestResource(t, st, "azure", sub.ID, TypeEventGridEventSubscription,
		"/subscriptions/sub-eg-empty/providers/Microsoft.EventGrid/eventSubscriptions/es-bare", "", "{}")
	if err := resolveEventGridRelationships(sub, st); err != nil {
		t.Fatalf("empty resolveEventGridRelationships: %v", err)
	}
}

package gcp

import (
	"testing"

	"github.com/icearp/disco-cli/store"
	compute "google.golang.org/api/compute/v1"
)

func TestResolveNetworkEndpointGroupRelationships_NetworkAndSubnetwork(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("my-project")

	netSelfLink := "https://www.googleapis.com/compute/v1/projects/my-project/global/networks/net-1"
	subnetSelfLink := "https://www.googleapis.com/compute/v1/projects/my-project/regions/us-central1/subnetworks/sub-1"
	netID := upsertTestResource(t, st, "gcp", p.ID, TypeComputeNetwork, netSelfLink, "", "{}")
	subnetID := upsertTestResource(t, st, "gcp", p.ID, TypeComputeSubnet, subnetSelfLink, "us-central1", "{}")

	negSelfLink := "https://www.googleapis.com/compute/v1/projects/my-project/zones/us-central1-a/networkEndpointGroups/neg-1"
	negID := upsertTestResource(t, st, "gcp", p.ID, TypeComputeNetworkEndpointGroup, negSelfLink, "us-central1-a",
		marshalAttrs(t, &compute.NetworkEndpointGroup{
			Name:       "neg-1",
			SelfLink:   negSelfLink,
			Network:    netSelfLink,
			Subnetwork: subnetSelfLink,
		}))

	if err := resolveNetworkEndpointGroupRelationships(p, st); err != nil {
		t.Fatalf("resolveNetworkEndpointGroupRelationships: %v", err)
	}
	rels, err := st.RelationshipsFrom(negID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	want := map[string]bool{netID: false, subnetID: false}
	for _, rel := range rels {
		if _, ok := want[rel.ToID]; !ok {
			t.Fatalf("unexpected edge target %s", rel.ToID)
		}
		if rel.Kind != store.RelAttachedTo {
			t.Errorf("got kind %s, want %s", rel.Kind, store.RelAttachedTo)
		}
		want[rel.ToID] = true
	}
	for id, seen := range want {
		if !seen {
			t.Errorf("missing edge to %s", id)
		}
	}
}

func TestResolveNetworkEndpointGroupRelationships_CloudRunAndCloudFunction(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("my-project")

	runID := upsertTestResource(t, st, "gcp", p.ID, TypeCloudRunSvc,
		"projects/my-project/locations/us-central1/services/my-run-svc", "us-central1", "{}")
	fnID := upsertTestResource(t, st, "gcp", p.ID, TypeCloudFunction,
		"projects/my-project/locations/us-central1/functions/my-func", "us-central1", "{}")

	negSelfLink := "https://www.googleapis.com/compute/v1/projects/my-project/regions/us-central1/networkEndpointGroups/neg-run"
	runNegID := upsertTestResource(t, st, "gcp", p.ID, TypeComputeRegionNetworkEndpointGroup, negSelfLink, "us-central1",
		marshalAttrs(t, &compute.NetworkEndpointGroup{
			Name:     "neg-run",
			SelfLink: negSelfLink,
			CloudRun: &compute.NetworkEndpointGroupCloudRun{Service: "my-run-svc"},
		}))

	fnNegSelfLink := "https://www.googleapis.com/compute/v1/projects/my-project/regions/us-central1/networkEndpointGroups/neg-fn"
	fnNegID := upsertTestResource(t, st, "gcp", p.ID, TypeComputeRegionNetworkEndpointGroup, fnNegSelfLink, "us-central1",
		marshalAttrs(t, &compute.NetworkEndpointGroup{
			Name:          "neg-fn",
			SelfLink:      fnNegSelfLink,
			CloudFunction: &compute.NetworkEndpointGroupCloudFunction{Function: "my-func"},
		}))

	if err := resolveNetworkEndpointGroupRelationships(p, st); err != nil {
		t.Fatalf("resolveNetworkEndpointGroupRelationships: %v", err)
	}

	rels, err := st.RelationshipsFrom(runNegID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 1 || rels[0].ToID != runID || rels[0].Kind != store.RelUses {
		t.Errorf("got %+v, want →run service uses", rels)
	}

	fnRels, err := st.RelationshipsFrom(fnNegID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(fnRels) != 1 || fnRels[0].ToID != fnID || fnRels[0].Kind != store.RelUses {
		t.Errorf("got %+v, want →cloud function uses", fnRels)
	}
}

func TestResolveNetworkEndpointGroupRelationships_UnscannedTargetsSkipped(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("my-project")

	negSelfLink := "https://www.googleapis.com/compute/v1/projects/my-project/regions/us-central1/networkEndpointGroups/neg-1"
	negID := upsertTestResource(t, st, "gcp", p.ID, TypeComputeRegionNetworkEndpointGroup, negSelfLink, "us-central1",
		marshalAttrs(t, &compute.NetworkEndpointGroup{
			Name:     "neg-1",
			SelfLink: negSelfLink,
			Network:  "https://www.googleapis.com/compute/v1/projects/my-project/global/networks/not-scanned",
			CloudRun: &compute.NetworkEndpointGroupCloudRun{Service: "not-scanned"},
		}))

	if err := resolveNetworkEndpointGroupRelationships(p, st); err != nil {
		t.Fatalf("resolveNetworkEndpointGroupRelationships: %v", err)
	}
	rels, err := st.RelationshipsFrom(negID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 0 {
		t.Errorf("want no edges for unscanned targets, got %+v", rels)
	}
}

func TestResolveNetworkEndpointGroupRelationships_EmptyProjectNoResources(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("my-project")
	if err := resolveNetworkEndpointGroupRelationships(p, st); err != nil {
		t.Fatalf("resolveNetworkEndpointGroupRelationships on empty project: %v", err)
	}
}

func TestResolvePacketMirroringRelationships_AllTargets(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("my-project")

	netSelfLink := "https://www.googleapis.com/compute/v1/projects/my-project/global/networks/net-1"
	instSelfLink := "https://www.googleapis.com/compute/v1/projects/my-project/zones/us-central1-a/instances/inst-1"
	subnetSelfLink := "https://www.googleapis.com/compute/v1/projects/my-project/regions/us-central1/subnetworks/sub-1"
	fwdSelfLink := "https://www.googleapis.com/compute/v1/projects/my-project/regions/us-central1/forwardingRules/fr-1"

	netID := upsertTestResource(t, st, "gcp", p.ID, TypeComputeNetwork, netSelfLink, "", "{}")
	instID := upsertTestResource(t, st, "gcp", p.ID, TypeComputeInstance, instSelfLink, "us-central1", "{}")
	subnetID := upsertTestResource(t, st, "gcp", p.ID, TypeComputeSubnet, subnetSelfLink, "us-central1", "{}")
	fwdID := upsertTestResource(t, st, "gcp", p.ID, TypeComputeForwardingRule, fwdSelfLink, "us-central1", "{}")

	pmSelfLink := "https://www.googleapis.com/compute/v1/projects/my-project/regions/us-central1/packetMirrorings/pm-1"
	pmID := upsertTestResource(t, st, "gcp", p.ID, TypeComputePacketMirroring, pmSelfLink, "us-central1",
		marshalAttrs(t, &compute.PacketMirroring{
			Name:     "pm-1",
			SelfLink: pmSelfLink,
			Network:  &compute.PacketMirroringNetworkInfo{Url: netSelfLink},
			MirroredResources: &compute.PacketMirroringMirroredResourceInfo{
				Instances:   []*compute.PacketMirroringMirroredResourceInfoInstanceInfo{{Url: instSelfLink}},
				Subnetworks: []*compute.PacketMirroringMirroredResourceInfoSubnetInfo{{Url: subnetSelfLink}},
			},
			CollectorIlb: &compute.PacketMirroringForwardingRuleInfo{Url: fwdSelfLink},
		}))

	if err := resolvePacketMirroringRelationships(p, st); err != nil {
		t.Fatalf("resolvePacketMirroringRelationships: %v", err)
	}
	rels, err := st.RelationshipsFrom(pmID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	want := map[string]bool{netID: false, instID: false, subnetID: false, fwdID: false}
	for _, rel := range rels {
		if _, ok := want[rel.ToID]; !ok {
			t.Fatalf("unexpected edge target %s", rel.ToID)
		}
		want[rel.ToID] = true
	}
	for id, seen := range want {
		if !seen {
			t.Errorf("missing edge to %s", id)
		}
	}
}

func TestResolvePacketMirroringRelationships_NoAttrsNoPanic(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("my-project")

	pmSelfLink := "https://www.googleapis.com/compute/v1/projects/my-project/regions/us-central1/packetMirrorings/pm-1"
	pmID := upsertTestResource(t, st, "gcp", p.ID, TypeComputePacketMirroring, pmSelfLink, "us-central1",
		marshalAttrs(t, &compute.PacketMirroring{Name: "pm-1", SelfLink: pmSelfLink}))

	if err := resolvePacketMirroringRelationships(p, st); err != nil {
		t.Fatalf("resolvePacketMirroringRelationships: %v", err)
	}
	rels, err := st.RelationshipsFrom(pmID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 0 {
		t.Errorf("want no edges when optional fields are unset, got %+v", rels)
	}
}

func TestResolvePacketMirroringRelationships_EmptyProjectNoResources(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("my-project")
	if err := resolvePacketMirroringRelationships(p, st); err != nil {
		t.Fatalf("resolvePacketMirroringRelationships on empty project: %v", err)
	}
}

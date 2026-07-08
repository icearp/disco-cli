package gcp

import (
	"testing"

	"codeberg.org/icearp/disco/store"
	kms "google.golang.org/api/cloudkms/v1"
	compute "google.golang.org/api/compute/v1"
	container "google.golang.org/api/container/v1"
)

func TestResolveGKEClusterRelationships_AllTargets(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("proj-1")

	netSelfLink := "https://www.googleapis.com/compute/v1/projects/proj-1/global/networks/net-1"
	netID := upsertTestResource(t, st, "gcp", p.ID, TypeComputeNetwork, netSelfLink, "",
		marshalAttrs(t, &compute.Network{SelfLink: netSelfLink}))

	subSelfLink := "https://www.googleapis.com/compute/v1/projects/proj-1/regions/us-central1/subnetworks/sub-1"
	subID := upsertTestResource(t, st, "gcp", p.ID, TypeComputeSubnet, subSelfLink, "us-central1",
		marshalAttrs(t, &compute.Subnetwork{SelfLink: subSelfLink}))

	saID := upsertTestResource(t, st, "gcp", p.ID, TypeIAMServiceAccount, "projects/proj-1/serviceAccounts/gke-sa@proj-1.iam.gserviceaccount.com", "",
		marshalAttrs(t, map[string]string{"email": "gke-sa@proj-1.iam.gserviceaccount.com"}))

	keyNative := "projects/proj-1/locations/global/keyRings/ring-1/cryptoKeys/key-1"
	keyID := upsertTestResource(t, st, "gcp", p.ID, TypeKMSCryptoKey, keyNative, "global",
		marshalAttrs(t, &kms.CryptoKey{Name: keyNative}))

	clusterSelfLink := "https://container.googleapis.com/v1/projects/proj-1/locations/us-central1/clusters/clust-1"
	clusterID := upsertTestResource(t, st, "gcp", p.ID, TypeGKECluster, clusterSelfLink, "us-central1",
		marshalAttrs(t, &container.Cluster{
			Name:       "clust-1",
			Network:    "net-1",
			Subnetwork: "sub-1",
			NodeConfig: &container.NodeConfig{
				ServiceAccount: "gke-sa@proj-1.iam.gserviceaccount.com",
			},
			DatabaseEncryption: &container.DatabaseEncryption{KeyName: keyNative},
		}))

	if err := resolveGKEClusterRelationships(p, st); err != nil {
		t.Fatalf("resolveGKEClusterRelationships: %v", err)
	}
	rels, err := st.RelationshipsFrom(clusterID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 4 {
		t.Fatalf("expected 4 edges, got %d: %+v", len(rels), rels)
	}
	want := map[string]bool{netID: false, subID: false, saID: false, keyID: false}
	for _, r := range rels {
		if _, ok := want[r.ToID]; !ok {
			t.Errorf("unexpected edge target %q", r.ToID)
			continue
		}
		want[r.ToID] = true
	}
	for id, hit := range want {
		if !hit {
			t.Errorf("missing edge to %q", id)
		}
	}
}

func TestResolveGKEClusterRelationships_UnscannedTargetsSkipped(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("proj-1")

	clusterSelfLink := "https://container.googleapis.com/v1/projects/proj-1/locations/us-central1/clusters/clust-1"
	clusterID := upsertTestResource(t, st, "gcp", p.ID, TypeGKECluster, clusterSelfLink, "us-central1",
		marshalAttrs(t, &container.Cluster{
			Name:       "clust-1",
			Network:    "not-scanned-net",
			Subnetwork: "not-scanned-sub",
			NodeConfig: &container.NodeConfig{
				ServiceAccount: "not-scanned@proj-1.iam.gserviceaccount.com",
			},
			DatabaseEncryption: &container.DatabaseEncryption{
				KeyName: "projects/proj-1/locations/global/keyRings/ring-1/cryptoKeys/not-scanned",
			},
		}))

	if err := resolveGKEClusterRelationships(p, st); err != nil {
		t.Fatalf("resolveGKEClusterRelationships: %v", err)
	}
	rels, err := st.RelationshipsFrom(clusterID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 0 {
		t.Errorf("want no edges for unscanned targets, got %+v", rels)
	}
}

func TestResolveGKEClusterRelationships_DefaultServiceAccountSentinelNoMatch(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("proj-1")

	clusterSelfLink := "https://container.googleapis.com/v1/projects/proj-1/locations/us-central1/clusters/clust-1"
	clusterID := upsertTestResource(t, st, "gcp", p.ID, TypeGKECluster, clusterSelfLink, "us-central1",
		marshalAttrs(t, &container.Cluster{
			Name:       "clust-1",
			NodeConfig: &container.NodeConfig{ServiceAccount: "default"},
		}))

	if err := resolveGKEClusterRelationships(p, st); err != nil {
		t.Fatalf("resolveGKEClusterRelationships: %v", err)
	}
	rels, err := st.RelationshipsFrom(clusterID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 0 {
		t.Errorf("want no edge for the \"default\" SA sentinel, got %+v", rels)
	}
}

func TestResolveGKEClusterRelationships_NilConfigSkipped(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("proj-1")

	clusterSelfLink := "https://container.googleapis.com/v1/projects/proj-1/locations/us-central1/clusters/clust-1"
	clusterID := upsertTestResource(t, st, "gcp", p.ID, TypeGKECluster, clusterSelfLink, "us-central1",
		marshalAttrs(t, &container.Cluster{Name: "clust-1"}))

	if err := resolveGKEClusterRelationships(p, st); err != nil {
		t.Fatalf("resolveGKEClusterRelationships: %v", err)
	}
	rels, err := st.RelationshipsFrom(clusterID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 0 {
		t.Errorf("want no edges when nodeConfig/databaseEncryption are nil and network/subnetwork are empty, got %+v", rels)
	}
}

func TestResolveGKEClusterRelationships_EmptyProjectNoResources(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("proj-1")
	if err := resolveGKEClusterRelationships(p, st); err != nil {
		t.Fatalf("resolveGKEClusterRelationships on empty project: %v", err)
	}
}

func TestResolveGKENodePoolRelationships_ServiceAccountMatch(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("proj-1")

	saID := upsertTestResource(t, st, "gcp", p.ID, TypeIAMServiceAccount, "projects/proj-1/serviceAccounts/pool-sa@proj-1.iam.gserviceaccount.com", "",
		marshalAttrs(t, map[string]string{"email": "pool-sa@proj-1.iam.gserviceaccount.com"}))

	npSelfLink := "https://container.googleapis.com/v1/projects/proj-1/locations/us-central1/clusters/clust-1/nodePools/np-1"
	npID := upsertTestResource(t, st, "gcp", p.ID, TypeGKENodePool, npSelfLink, "us-central1",
		marshalAttrs(t, &container.NodePool{
			Name:   "np-1",
			Config: &container.NodeConfig{ServiceAccount: "pool-sa@proj-1.iam.gserviceaccount.com"},
		}))

	if err := resolveGKENodePoolRelationships(p, st); err != nil {
		t.Fatalf("resolveGKENodePoolRelationships: %v", err)
	}
	rels, err := st.RelationshipsFrom(npID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 1 || rels[0].ToID != saID || rels[0].Kind != store.RelUses {
		t.Errorf("want nodePool->SA uses edge, got %+v", rels)
	}
}

func TestResolveGKENodePoolRelationships_NilConfigSkipped(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("proj-1")

	npSelfLink := "https://container.googleapis.com/v1/projects/proj-1/locations/us-central1/clusters/clust-1/nodePools/np-1"
	npID := upsertTestResource(t, st, "gcp", p.ID, TypeGKENodePool, npSelfLink, "us-central1",
		marshalAttrs(t, &container.NodePool{Name: "np-1"}))

	if err := resolveGKENodePoolRelationships(p, st); err != nil {
		t.Fatalf("resolveGKENodePoolRelationships: %v", err)
	}
	rels, err := st.RelationshipsFrom(npID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 0 {
		t.Errorf("want no edges when config is nil, got %+v", rels)
	}
}

func TestResolveGKENodePoolRelationships_UnscannedServiceAccountSkipped(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("proj-1")

	npSelfLink := "https://container.googleapis.com/v1/projects/proj-1/locations/us-central1/clusters/clust-1/nodePools/np-1"
	npID := upsertTestResource(t, st, "gcp", p.ID, TypeGKENodePool, npSelfLink, "us-central1",
		marshalAttrs(t, &container.NodePool{
			Name:   "np-1",
			Config: &container.NodeConfig{ServiceAccount: "not-scanned@proj-1.iam.gserviceaccount.com"},
		}))

	if err := resolveGKENodePoolRelationships(p, st); err != nil {
		t.Fatalf("resolveGKENodePoolRelationships: %v", err)
	}
	rels, err := st.RelationshipsFrom(npID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 0 {
		t.Errorf("want no edge for unscanned service account, got %+v", rels)
	}
}

func TestResolveGKENodePoolRelationships_EmptyProjectNoResources(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("proj-1")
	if err := resolveGKENodePoolRelationships(p, st); err != nil {
		t.Fatalf("resolveGKENodePoolRelationships on empty project: %v", err)
	}
}

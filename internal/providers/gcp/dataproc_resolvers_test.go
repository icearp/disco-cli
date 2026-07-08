package gcp

import (
	"testing"

	kms "google.golang.org/api/cloudkms/v1"
	compute "google.golang.org/api/compute/v1"
	dataproc "google.golang.org/api/dataproc/v1"
	storage "google.golang.org/api/storage/v1"
)

func TestResolveDataprocClusterRelationships_AllTargets(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("proj-1")

	netSelfLink := "https://www.googleapis.com/compute/v1/projects/proj-1/global/networks/net-1"
	netID := upsertTestResource(t, st, "gcp", p.ID, TypeComputeNetwork, netSelfLink, "",
		marshalAttrs(t, &compute.Network{SelfLink: netSelfLink}))

	subSelfLink := "https://www.googleapis.com/compute/v1/projects/proj-1/regions/us-central1/subnetworks/sub-1"
	subID := upsertTestResource(t, st, "gcp", p.ID, TypeComputeSubnet, subSelfLink, "us-central1",
		marshalAttrs(t, &compute.Subnetwork{SelfLink: subSelfLink}))

	saID := upsertTestResource(t, st, "gcp", p.ID, TypeIAMServiceAccount, "projects/proj-1/serviceAccounts/dataproc-sa@proj-1.iam.gserviceaccount.com", "",
		marshalAttrs(t, map[string]string{"email": "dataproc-sa@proj-1.iam.gserviceaccount.com"}))

	keyNative := "projects/proj-1/locations/us-central1/keyRings/ring-1/cryptoKeys/key-1"
	keyID := upsertTestResource(t, st, "gcp", p.ID, TypeKMSCryptoKey, keyNative, "us-central1",
		marshalAttrs(t, &kms.CryptoKey{Name: keyNative}))

	bucketSelfLink := "https://www.googleapis.com/storage/v1/b/dataproc-staging-1"
	bucketID := upsertTestResource(t, st, "gcp", p.ID, TypeStorageBucket, bucketSelfLink, "",
		marshalAttrs(t, &storage.Bucket{Name: "dataproc-staging-1", SelfLink: bucketSelfLink}))

	clusterID := upsertTestResource(t, st, "gcp", p.ID, TypeDataprocCluster, "projects/proj-1/regions/us-central1/clusters/clust-1", "us-central1",
		marshalAttrs(t, &dataproc.Cluster{
			ClusterName: "clust-1",
			Config: &dataproc.ClusterConfig{
				ConfigBucket: "dataproc-staging-1",
				GceClusterConfig: &dataproc.GceClusterConfig{
					NetworkUri:     "projects/proj-1/global/networks/net-1",
					SubnetworkUri:  "us-central1/sub-1",
					ServiceAccount: "dataproc-sa@proj-1.iam.gserviceaccount.com",
				},
				EncryptionConfig: &dataproc.EncryptionConfig{
					GcePdKmsKeyName: keyNative,
				},
			},
		}))

	if err := resolveDataprocClusterRelationships(p, st); err != nil {
		t.Fatalf("resolveDataprocClusterRelationships: %v", err)
	}

	rels, err := st.RelationshipsFrom(clusterID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 5 {
		t.Fatalf("expected 5 edges, got %d: %+v", len(rels), rels)
	}
	want := map[string]bool{netID: false, subID: false, saID: false, keyID: false, bucketID: false}
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

func TestResolveDataprocClusterRelationships_UnscannedTargetsSkipped(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("proj-1")

	clusterID := upsertTestResource(t, st, "gcp", p.ID, TypeDataprocCluster, "projects/proj-1/regions/us-central1/clusters/clust-1", "us-central1",
		marshalAttrs(t, &dataproc.Cluster{
			ClusterName: "clust-1",
			Config: &dataproc.ClusterConfig{
				ConfigBucket: "not-scanned-bucket",
				GceClusterConfig: &dataproc.GceClusterConfig{
					NetworkUri:     "projects/proj-1/global/networks/not-scanned",
					SubnetworkUri:  "us-central1/not-scanned",
					ServiceAccount: "not-scanned@proj-1.iam.gserviceaccount.com",
				},
				EncryptionConfig: &dataproc.EncryptionConfig{
					KmsKey: "projects/proj-1/locations/us-central1/keyRings/ring-1/cryptoKeys/not-scanned",
				},
			},
		}))

	if err := resolveDataprocClusterRelationships(p, st); err != nil {
		t.Fatalf("resolveDataprocClusterRelationships: %v", err)
	}

	rels, err := st.RelationshipsFrom(clusterID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 0 {
		t.Errorf("want no edges for unscanned targets, got %+v", rels)
	}
}

func TestResolveDataprocClusterRelationships_NilConfigSkipped(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("proj-1")

	clusterID := upsertTestResource(t, st, "gcp", p.ID, TypeDataprocCluster, "projects/proj-1/regions/us-central1/clusters/clust-1", "us-central1",
		marshalAttrs(t, &dataproc.Cluster{ClusterName: "clust-1"}))

	if err := resolveDataprocClusterRelationships(p, st); err != nil {
		t.Fatalf("resolveDataprocClusterRelationships: %v", err)
	}

	rels, err := st.RelationshipsFrom(clusterID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 0 {
		t.Errorf("want no edges when config is nil, got %+v", rels)
	}
}

func TestResolveDataprocClusterRelationships_EmptyProjectNoResources(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("proj-1")
	if err := resolveDataprocClusterRelationships(p, st); err != nil {
		t.Fatalf("resolveDataprocClusterRelationships on empty project: %v", err)
	}
}

func TestResolveDataprocJobRelationships_ToClusterByRegionAndBareName(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("proj-1")

	clusterID := upsertTestResource(t, st, "gcp", p.ID, TypeDataprocCluster, "projects/proj-1/regions/us-central1/clusters/clust-1", "us-central1",
		marshalAttrs(t, &dataproc.Cluster{ClusterName: "clust-1"}))

	jobID := upsertTestResource(t, st, "gcp", p.ID, TypeDataprocJob, "projects/proj-1/regions/us-central1/jobs/job-1", "us-central1",
		marshalAttrs(t, &dataproc.Job{
			Placement: &dataproc.JobPlacement{ClusterName: "clust-1"},
		}))

	if err := resolveDataprocJobRelationships(p, st); err != nil {
		t.Fatalf("resolveDataprocJobRelationships: %v", err)
	}

	rels, err := st.RelationshipsFrom(jobID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 1 || rels[0].ToID != clusterID || rels[0].Kind != "uses" {
		t.Errorf("want job->cluster edge, got %+v", rels)
	}
}

func TestResolveDataprocJobRelationships_WrongRegionNoMatch(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("proj-1")

	upsertTestResource(t, st, "gcp", p.ID, TypeDataprocCluster, "projects/proj-1/regions/us-west1/clusters/clust-1", "us-west1",
		marshalAttrs(t, &dataproc.Cluster{ClusterName: "clust-1"}))

	jobID := upsertTestResource(t, st, "gcp", p.ID, TypeDataprocJob, "projects/proj-1/regions/us-central1/jobs/job-1", "us-central1",
		marshalAttrs(t, &dataproc.Job{
			Placement: &dataproc.JobPlacement{ClusterName: "clust-1"},
		}))

	if err := resolveDataprocJobRelationships(p, st); err != nil {
		t.Fatalf("resolveDataprocJobRelationships: %v", err)
	}

	rels, err := st.RelationshipsFrom(jobID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 0 {
		t.Errorf("want no edge when cluster exists only in a different region, got %+v", rels)
	}
}

func TestResolveDataprocJobRelationships_EmptyProjectNoResources(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("proj-1")
	if err := resolveDataprocJobRelationships(p, st); err != nil {
		t.Fatalf("resolveDataprocJobRelationships on empty project: %v", err)
	}
}

package gcp

import (
	"testing"

	"codeberg.org/icearp/disco/store"
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

// dataprocFamilyFixture seeds the network/subnetwork/serviceAccount/
// cryptoKey/bucket targets shared by the Wave R19 execution-config-shaped
// resolver tests (Batch/Session/SessionTemplate) and returns their IDs plus
// a matching *dataproc.ExecutionConfig fixture.
func dataprocFamilyFixture(t *testing.T, st *store.Store, p *project) (ids map[string]string, ec *dataproc.ExecutionConfig) {
	t.Helper()
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

	return map[string]string{"net": netID, "sub": subID, "sa": saID, "key": keyID, "bucket": bucketID},
		&dataproc.ExecutionConfig{
			NetworkUri:     "projects/proj-1/global/networks/net-1",
			SubnetworkUri:  "us-central1/sub-1",
			ServiceAccount: "dataproc-sa@proj-1.iam.gserviceaccount.com",
			KmsKey:         keyNative,
			StagingBucket:  "dataproc-staging-1",
		}
}

func assertDataprocFamilyEdges(t *testing.T, st *store.Store, fromID string, ids map[string]string) {
	t.Helper()
	rels, err := st.RelationshipsFrom(fromID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 5 {
		t.Fatalf("expected 5 edges, got %d: %+v", len(rels), rels)
	}
	want := map[string]bool{ids["net"]: false, ids["sub"]: false, ids["sa"]: false, ids["key"]: false, ids["bucket"]: false}
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

func TestResolveDataprocBatchRelationships_AllTargets(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("proj-1")
	ids, ec := dataprocFamilyFixture(t, st, p)

	batchID := upsertTestResource(t, st, "gcp", p.ID, TypeDataprocBatch, "projects/proj-1/locations/us-central1/batches/batch-1", "us-central1",
		marshalAttrs(t, &dataproc.Batch{
			Name:              "projects/proj-1/locations/us-central1/batches/batch-1",
			EnvironmentConfig: &dataproc.EnvironmentConfig{ExecutionConfig: ec},
		}))

	if err := resolveDataprocBatchRelationships(p, st); err != nil {
		t.Fatalf("resolveDataprocBatchRelationships: %v", err)
	}
	assertDataprocFamilyEdges(t, st, batchID, ids)
}

func TestResolveDataprocBatchRelationships_UnscannedTargetsSkipped(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("proj-1")

	batchID := upsertTestResource(t, st, "gcp", p.ID, TypeDataprocBatch, "projects/proj-1/locations/us-central1/batches/batch-1", "us-central1",
		marshalAttrs(t, &dataproc.Batch{
			Name: "projects/proj-1/locations/us-central1/batches/batch-1",
			EnvironmentConfig: &dataproc.EnvironmentConfig{ExecutionConfig: &dataproc.ExecutionConfig{
				NetworkUri:     "projects/proj-1/global/networks/not-scanned",
				SubnetworkUri:  "us-central1/not-scanned",
				ServiceAccount: "not-scanned@proj-1.iam.gserviceaccount.com",
				KmsKey:         "projects/proj-1/locations/us-central1/keyRings/ring-1/cryptoKeys/not-scanned",
				StagingBucket:  "not-scanned-bucket",
			}},
		}))

	if err := resolveDataprocBatchRelationships(p, st); err != nil {
		t.Fatalf("resolveDataprocBatchRelationships: %v", err)
	}
	rels, err := st.RelationshipsFrom(batchID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 0 {
		t.Errorf("want no edges for unscanned targets, got %+v", rels)
	}
}

func TestResolveDataprocBatchRelationships_NilEnvironmentConfigSkipped(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("proj-1")

	batchID := upsertTestResource(t, st, "gcp", p.ID, TypeDataprocBatch, "projects/proj-1/locations/us-central1/batches/batch-1", "us-central1",
		marshalAttrs(t, &dataproc.Batch{Name: "projects/proj-1/locations/us-central1/batches/batch-1"}))

	if err := resolveDataprocBatchRelationships(p, st); err != nil {
		t.Fatalf("resolveDataprocBatchRelationships: %v", err)
	}
	rels, err := st.RelationshipsFrom(batchID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 0 {
		t.Errorf("want no edges when environmentConfig is nil, got %+v", rels)
	}
}

func TestResolveDataprocBatchRelationships_EmptyProjectNoResources(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("proj-1")
	if err := resolveDataprocBatchRelationships(p, st); err != nil {
		t.Fatalf("resolveDataprocBatchRelationships on empty project: %v", err)
	}
}

func TestResolveDataprocSessionTemplateRelationships_AllTargets(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("proj-1")
	ids, ec := dataprocFamilyFixture(t, st, p)

	tmplID := upsertTestResource(t, st, "gcp", p.ID, TypeDataprocSessionTemplate, "projects/proj-1/locations/us-central1/sessionTemplates/tmpl-1", "us-central1",
		marshalAttrs(t, &dataproc.SessionTemplate{
			Name:              "projects/proj-1/locations/us-central1/sessionTemplates/tmpl-1",
			EnvironmentConfig: &dataproc.EnvironmentConfig{ExecutionConfig: ec},
		}))

	if err := resolveDataprocSessionTemplateRelationships(p, st); err != nil {
		t.Fatalf("resolveDataprocSessionTemplateRelationships: %v", err)
	}
	assertDataprocFamilyEdges(t, st, tmplID, ids)
}

func TestResolveDataprocSessionTemplateRelationships_UnscannedTargetsSkipped(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("proj-1")

	tmplID := upsertTestResource(t, st, "gcp", p.ID, TypeDataprocSessionTemplate, "projects/proj-1/locations/us-central1/sessionTemplates/tmpl-1", "us-central1",
		marshalAttrs(t, &dataproc.SessionTemplate{
			Name: "projects/proj-1/locations/us-central1/sessionTemplates/tmpl-1",
			EnvironmentConfig: &dataproc.EnvironmentConfig{ExecutionConfig: &dataproc.ExecutionConfig{
				NetworkUri:     "projects/proj-1/global/networks/not-scanned",
				SubnetworkUri:  "us-central1/not-scanned",
				ServiceAccount: "not-scanned@proj-1.iam.gserviceaccount.com",
				KmsKey:         "projects/proj-1/locations/us-central1/keyRings/ring-1/cryptoKeys/not-scanned",
				StagingBucket:  "not-scanned-bucket",
			}},
		}))

	if err := resolveDataprocSessionTemplateRelationships(p, st); err != nil {
		t.Fatalf("resolveDataprocSessionTemplateRelationships: %v", err)
	}
	rels, err := st.RelationshipsFrom(tmplID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 0 {
		t.Errorf("want no edges for unscanned targets, got %+v", rels)
	}
}

func TestResolveDataprocSessionTemplateRelationships_NilEnvironmentConfigSkipped(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("proj-1")

	tmplID := upsertTestResource(t, st, "gcp", p.ID, TypeDataprocSessionTemplate, "projects/proj-1/locations/us-central1/sessionTemplates/tmpl-1", "us-central1",
		marshalAttrs(t, &dataproc.SessionTemplate{Name: "projects/proj-1/locations/us-central1/sessionTemplates/tmpl-1"}))

	if err := resolveDataprocSessionTemplateRelationships(p, st); err != nil {
		t.Fatalf("resolveDataprocSessionTemplateRelationships: %v", err)
	}
	rels, err := st.RelationshipsFrom(tmplID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 0 {
		t.Errorf("want no edges when environmentConfig is nil, got %+v", rels)
	}
}

func TestResolveDataprocSessionTemplateRelationships_EmptyProjectNoResources(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("proj-1")
	if err := resolveDataprocSessionTemplateRelationships(p, st); err != nil {
		t.Fatalf("resolveDataprocSessionTemplateRelationships on empty project: %v", err)
	}
}

func TestResolveDataprocSessionRelationships_AllTargetsAndTemplateBareName(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("proj-1")
	ids, ec := dataprocFamilyFixture(t, st, p)

	tmplID := upsertTestResource(t, st, "gcp", p.ID, TypeDataprocSessionTemplate, "projects/proj-1/locations/us-central1/sessionTemplates/tmpl-1", "us-central1",
		marshalAttrs(t, &dataproc.SessionTemplate{Name: "projects/proj-1/locations/us-central1/sessionTemplates/tmpl-1"}))

	sessID := upsertTestResource(t, st, "gcp", p.ID, TypeDataprocSession, "projects/proj-1/locations/us-central1/sessions/sess-1", "us-central1",
		marshalAttrs(t, &dataproc.Session{
			Name:              "projects/proj-1/locations/us-central1/sessions/sess-1",
			EnvironmentConfig: &dataproc.EnvironmentConfig{ExecutionConfig: ec},
			SessionTemplate:   "projects/proj-1/locations/us-central1/sessionTemplates/tmpl-1",
		}))

	if err := resolveDataprocSessionRelationships(p, st); err != nil {
		t.Fatalf("resolveDataprocSessionRelationships: %v", err)
	}
	rels, err := st.RelationshipsFrom(sessID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 6 {
		t.Fatalf("expected 6 edges (5 family + template), got %d: %+v", len(rels), rels)
	}
	want := map[string]bool{ids["net"]: false, ids["sub"]: false, ids["sa"]: false, ids["key"]: false, ids["bucket"]: false, tmplID: false}
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

func TestResolveDataprocSessionRelationships_TemplateFullURLNormalized(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("proj-1")

	tmplID := upsertTestResource(t, st, "gcp", p.ID, TypeDataprocSessionTemplate, "projects/proj-1/locations/us-central1/sessionTemplates/tmpl-1", "us-central1",
		marshalAttrs(t, &dataproc.SessionTemplate{Name: "projects/proj-1/locations/us-central1/sessionTemplates/tmpl-1"}))

	sessID := upsertTestResource(t, st, "gcp", p.ID, TypeDataprocSession, "projects/proj-1/locations/us-central1/sessions/sess-1", "us-central1",
		marshalAttrs(t, &dataproc.Session{
			Name:            "projects/proj-1/locations/us-central1/sessions/sess-1",
			SessionTemplate: "https://www.googleapis.com/compute/v1/projects/proj-1/locations/us-central1/sessionTemplates/tmpl-1",
		}))

	if err := resolveDataprocSessionRelationships(p, st); err != nil {
		t.Fatalf("resolveDataprocSessionRelationships: %v", err)
	}
	rels, err := st.RelationshipsFrom(sessID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 1 || rels[0].ToID != tmplID || rels[0].Kind != store.RelUses {
		t.Errorf("want session->sessionTemplate edge (URL-form normalized), got %+v", rels)
	}
}

func TestResolveDataprocSessionRelationships_UnscannedTemplateSkipped(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("proj-1")

	sessID := upsertTestResource(t, st, "gcp", p.ID, TypeDataprocSession, "projects/proj-1/locations/us-central1/sessions/sess-1", "us-central1",
		marshalAttrs(t, &dataproc.Session{
			Name:            "projects/proj-1/locations/us-central1/sessions/sess-1",
			SessionTemplate: "projects/proj-1/locations/us-central1/sessionTemplates/not-scanned",
		}))

	if err := resolveDataprocSessionRelationships(p, st); err != nil {
		t.Fatalf("resolveDataprocSessionRelationships: %v", err)
	}
	rels, err := st.RelationshipsFrom(sessID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 0 {
		t.Errorf("want no edge for unscanned session template, got %+v", rels)
	}
}

func TestResolveDataprocSessionRelationships_EmptyProjectNoResources(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("proj-1")
	if err := resolveDataprocSessionRelationships(p, st); err != nil {
		t.Fatalf("resolveDataprocSessionRelationships on empty project: %v", err)
	}
}

func TestResolveDataprocWorkflowTemplateRelationships_ManagedClusterAndOwnEncryption(t *testing.T) {
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

	clusterKeyNative := "projects/proj-1/locations/us-central1/keyRings/ring-1/cryptoKeys/cluster-key"
	clusterKeyID := upsertTestResource(t, st, "gcp", p.ID, TypeKMSCryptoKey, clusterKeyNative, "us-central1",
		marshalAttrs(t, &kms.CryptoKey{Name: clusterKeyNative}))

	workflowKeyNative := "projects/proj-1/locations/us-central1/keyRings/ring-1/cryptoKeys/workflow-key"
	workflowKeyID := upsertTestResource(t, st, "gcp", p.ID, TypeKMSCryptoKey, workflowKeyNative, "us-central1",
		marshalAttrs(t, &kms.CryptoKey{Name: workflowKeyNative}))

	bucketSelfLink := "https://www.googleapis.com/storage/v1/b/dataproc-staging-1"
	bucketID := upsertTestResource(t, st, "gcp", p.ID, TypeStorageBucket, bucketSelfLink, "",
		marshalAttrs(t, &storage.Bucket{Name: "dataproc-staging-1", SelfLink: bucketSelfLink}))

	wtID := upsertTestResource(t, st, "gcp", p.ID, TypeDataprocWorkflowTemplate, "projects/proj-1/regions/us-central1/workflowTemplates/wt-1", "us-central1",
		marshalAttrs(t, &dataproc.WorkflowTemplate{
			Name: "projects/proj-1/regions/us-central1/workflowTemplates/wt-1",
			Placement: &dataproc.WorkflowTemplatePlacement{
				ManagedCluster: &dataproc.ManagedCluster{
					ClusterName: "managed-clust",
					Config: &dataproc.ClusterConfig{
						ConfigBucket: "dataproc-staging-1",
						GceClusterConfig: &dataproc.GceClusterConfig{
							NetworkUri:     "projects/proj-1/global/networks/net-1",
							SubnetworkUri:  "us-central1/sub-1",
							ServiceAccount: "dataproc-sa@proj-1.iam.gserviceaccount.com",
						},
						EncryptionConfig: &dataproc.EncryptionConfig{GcePdKmsKeyName: clusterKeyNative},
					},
				},
			},
			EncryptionConfig: &dataproc.GoogleCloudDataprocV1WorkflowTemplateEncryptionConfig{KmsKey: workflowKeyNative},
		}))

	if err := resolveDataprocWorkflowTemplateRelationships(p, st); err != nil {
		t.Fatalf("resolveDataprocWorkflowTemplateRelationships: %v", err)
	}
	rels, err := st.RelationshipsFrom(wtID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 6 {
		t.Fatalf("expected 6 edges (5 managed-cluster + own encryption key), got %d: %+v", len(rels), rels)
	}
	want := map[string]bool{netID: false, subID: false, saID: false, clusterKeyID: false, bucketID: false, workflowKeyID: false}
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

func TestResolveDataprocWorkflowTemplateRelationships_ClusterSelectorNoManagedCluster(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("proj-1")

	wtID := upsertTestResource(t, st, "gcp", p.ID, TypeDataprocWorkflowTemplate, "projects/proj-1/regions/us-central1/workflowTemplates/wt-1", "us-central1",
		marshalAttrs(t, &dataproc.WorkflowTemplate{
			Name: "projects/proj-1/regions/us-central1/workflowTemplates/wt-1",
			Placement: &dataproc.WorkflowTemplatePlacement{
				ClusterSelector: &dataproc.ClusterSelector{ClusterLabels: map[string]string{"env": "prod"}},
			},
		}))

	if err := resolveDataprocWorkflowTemplateRelationships(p, st); err != nil {
		t.Fatalf("resolveDataprocWorkflowTemplateRelationships: %v", err)
	}
	rels, err := st.RelationshipsFrom(wtID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 0 {
		t.Errorf("want no edges when placement uses clusterSelector (label matcher, not a resource ref), got %+v", rels)
	}
}

func TestResolveDataprocWorkflowTemplateRelationships_EmptyProjectNoResources(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("proj-1")
	if err := resolveDataprocWorkflowTemplateRelationships(p, st); err != nil {
		t.Fatalf("resolveDataprocWorkflowTemplateRelationships on empty project: %v", err)
	}
}

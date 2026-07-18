package gcp

import (
	"testing"

	"codeberg.org/icearp/disco/store"
	compute "google.golang.org/api/compute/v1"
	"google.golang.org/api/storage/v1"
)

func TestResolveBackendBucketRelationships_ByName(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("proj-1")

	bucketName := "my-bucket"
	bucketNativeID := "https://www.googleapis.com/storage/v1/b/my-bucket"
	if _, err := st.UpsertResource(&store.Resource{
		Provider: "gcp", AccountID: p.ID, Type: TypeStorageBucket, NativeID: bucketNativeID,
		Name: &bucketName, AttributesJSON: marshalAttrs(t, &storage.Bucket{Name: bucketName}),
		DiscoveredBy: testScanID,
	}); err != nil {
		t.Fatalf("upsert bucket: %v", err)
	}
	bucketID := store.ResourceID("gcp", p.ID, bucketNativeID)

	bbID := upsertTestResource(t, st, "gcp", p.ID, TypeComputeBackendBucket, "projects/proj-1/global/backendBuckets/bb-1", "",
		marshalAttrs(t, &compute.BackendBucket{SelfLink: "projects/proj-1/global/backendBuckets/bb-1", Name: "bb-1", BucketName: "my-bucket"}))

	if err := resolveBackendBucketRelationships(p, st); err != nil {
		t.Fatalf("resolveBackendBucketRelationships: %v", err)
	}

	rels, err := st.RelationshipsFrom(bbID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 1 || rels[0].ToID != bucketID || rels[0].Kind != "uses" {
		t.Errorf("want single uses edge to storage bucket, got %+v", rels)
	}
}

func TestResolveBackendBucketRelationships_UnscannedBucketSkipped(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("proj-1")

	bbID := upsertTestResource(t, st, "gcp", p.ID, TypeComputeBackendBucket, "projects/proj-1/global/backendBuckets/bb-1", "",
		marshalAttrs(t, &compute.BackendBucket{SelfLink: "projects/proj-1/global/backendBuckets/bb-1", Name: "bb-1", BucketName: "not-scanned-bucket"}))

	if err := resolveBackendBucketRelationships(p, st); err != nil {
		t.Fatalf("resolveBackendBucketRelationships: %v", err)
	}

	rels, err := st.RelationshipsFrom(bbID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 0 {
		t.Errorf("want no edges for unscanned bucket reference, got %+v", rels)
	}
}

func TestResolveBackendBucketRelationships_EmptyProjectNoResources(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("proj-1")
	if err := resolveBackendBucketRelationships(p, st); err != nil {
		t.Fatalf("resolveBackendBucketRelationships on empty project: %v", err)
	}
}

func TestResolveTargetPoolRelationships_FullChain(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("proj-1")

	instID := upsertTestResource(t, st, "gcp", p.ID, TypeComputeInstance, "projects/proj-1/zones/us-central1-a/instances/inst-1", "us-central1-a",
		marshalAttrs(t, &compute.Instance{SelfLink: "projects/proj-1/zones/us-central1-a/instances/inst-1", Name: "inst-1"}))
	// TargetPool.HealthChecks only ever holds legacy HttpHealthCheck
	// self-links ("Only legacy HttpHealthChecks are supported" per the SDK
	// doc comment) — not the modern HealthCheck type.
	hcID := upsertTestResource(t, st, "gcp", p.ID, TypeComputeHTTPHealthCheck, "projects/proj-1/global/httpHealthChecks/hc-1", "",
		marshalAttrs(t, &compute.HttpHealthCheck{SelfLink: "projects/proj-1/global/httpHealthChecks/hc-1", Name: "hc-1"}))
	backupID := upsertTestResource(t, st, "gcp", p.ID, TypeComputeTargetPool, "projects/proj-1/regions/us-central1/targetPools/tp-backup", "us-central1",
		marshalAttrs(t, &compute.TargetPool{SelfLink: "projects/proj-1/regions/us-central1/targetPools/tp-backup", Name: "tp-backup"}))

	tpID := upsertTestResource(t, st, "gcp", p.ID, TypeComputeTargetPool, "projects/proj-1/regions/us-central1/targetPools/tp-1", "us-central1",
		marshalAttrs(t, &compute.TargetPool{
			SelfLink:     "projects/proj-1/regions/us-central1/targetPools/tp-1",
			Name:         "tp-1",
			Instances:    []string{"projects/proj-1/zones/us-central1-a/instances/inst-1"},
			HealthChecks: []string{"projects/proj-1/global/httpHealthChecks/hc-1"},
			BackupPool:   "projects/proj-1/regions/us-central1/targetPools/tp-backup",
		}))

	if err := resolveTargetPoolRelationships(p, st); err != nil {
		t.Fatalf("resolveTargetPoolRelationships: %v", err)
	}

	rels, err := st.RelationshipsFrom(tpID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	want := map[string]string{instID: "attached-to", hcID: "uses", backupID: "attached-to"}
	got := map[string]string{}
	for _, r := range rels {
		got[r.ToID] = r.Kind
	}
	for id, kind := range want {
		if got[id] != kind {
			t.Errorf("edge to %s want kind %q, got %q (all: %+v)", id, kind, got[id], rels)
		}
	}
	if len(rels) != len(want) {
		t.Errorf("want exactly %d edges, got %d: %+v", len(want), len(rels), rels)
	}
}

func TestResolveTargetPoolRelationships_EmptyProjectNoResources(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("proj-1")
	if err := resolveTargetPoolRelationships(p, st); err != nil {
		t.Fatalf("resolveTargetPoolRelationships on empty project: %v", err)
	}
}

func TestResolveTargetInstanceRelationships_FullChain(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("proj-1")

	instID := upsertTestResource(t, st, "gcp", p.ID, TypeComputeInstance, "projects/proj-1/zones/us-central1-a/instances/inst-1", "us-central1-a",
		marshalAttrs(t, &compute.Instance{SelfLink: "projects/proj-1/zones/us-central1-a/instances/inst-1", Name: "inst-1"}))
	netID := upsertTestResource(t, st, "gcp", p.ID, TypeComputeNetwork, "projects/proj-1/global/networks/net-1", "",
		marshalAttrs(t, &compute.Network{SelfLink: "projects/proj-1/global/networks/net-1", Name: "net-1"}))

	tiID := upsertTestResource(t, st, "gcp", p.ID, TypeComputeTargetInstance, "projects/proj-1/zones/us-central1-a/targetInstances/ti-1", "us-central1-a",
		marshalAttrs(t, &compute.TargetInstance{
			SelfLink: "projects/proj-1/zones/us-central1-a/targetInstances/ti-1",
			Name:     "ti-1",
			Instance: "projects/proj-1/zones/us-central1-a/instances/inst-1",
			Network:  "projects/proj-1/global/networks/net-1",
		}))

	if err := resolveTargetInstanceRelationships(p, st); err != nil {
		t.Fatalf("resolveTargetInstanceRelationships: %v", err)
	}

	rels, err := st.RelationshipsFrom(tiID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	got := map[string]string{}
	for _, r := range rels {
		got[r.ToID] = r.Kind
	}
	if got[instID] != "attached-to" || got[netID] != "attached-to" {
		t.Errorf("want attached-to edges to instance+network, got %+v", rels)
	}
	if len(rels) != 2 {
		t.Errorf("want exactly 2 edges, got %d: %+v", len(rels), rels)
	}
}

func TestResolveTargetInstanceRelationships_EmptyProjectNoResources(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("proj-1")
	if err := resolveTargetInstanceRelationships(p, st); err != nil {
		t.Fatalf("resolveTargetInstanceRelationships on empty project: %v", err)
	}
}

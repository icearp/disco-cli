package gcp

import (
	"testing"

	"github.com/icearp/disco-cli/store"
	kms "google.golang.org/api/cloudkms/v1"
	compute "google.golang.org/api/compute/v1"
	dataflow "google.golang.org/api/dataflow/v1b3"
	pubsub "google.golang.org/api/pubsub/v1"
)

func TestResolveDataflowJobRelationships_AllTargets(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("proj-1")

	netSelfLink := "https://www.googleapis.com/compute/v1/projects/proj-1/global/networks/net-1"
	netID := upsertTestResource(t, st, "gcp", p.ID, TypeComputeNetwork, netSelfLink, "",
		marshalAttrs(t, &compute.Network{SelfLink: netSelfLink}))

	subSelfLink := "https://www.googleapis.com/compute/v1/projects/proj-1/regions/us-central1/subnetworks/sub-1"
	subID := upsertTestResource(t, st, "gcp", p.ID, TypeComputeSubnet, subSelfLink, "us-central1",
		marshalAttrs(t, &compute.Subnetwork{SelfLink: subSelfLink}))

	saID := upsertTestResource(t, st, "gcp", p.ID, TypeIAMServiceAccount, "projects/proj-1/serviceAccounts/df-sa@proj-1.iam.gserviceaccount.com", "",
		marshalAttrs(t, map[string]string{"email": "df-sa@proj-1.iam.gserviceaccount.com"}))

	keyNative := "projects/proj-1/locations/us-central1/keyRings/ring-1/cryptoKeys/key-1"
	keyID := upsertTestResource(t, st, "gcp", p.ID, TypeKMSCryptoKey, keyNative, "us-central1",
		marshalAttrs(t, &kms.CryptoKey{Name: keyNative}))

	jobID := upsertTestResource(t, st, "gcp", p.ID, TypeDataflowJob, "projects/proj-1/locations/us-central1/jobs/job-1", "us-central1",
		marshalAttrs(t, &dataflow.Job{
			Id: "job-1",
			Environment: &dataflow.Environment{
				ServiceAccountEmail: "df-sa@proj-1.iam.gserviceaccount.com",
				ServiceKmsKeyName:   keyNative,
				WorkerPools: []*dataflow.WorkerPool{
					{Network: "net-1", Subnetwork: "regions/us-central1/subnetworks/sub-1"},
				},
			},
		}))

	if err := resolveDataflowJobRelationships(p, st); err != nil {
		t.Fatalf("resolveDataflowJobRelationships: %v", err)
	}
	rels, err := st.RelationshipsFrom(jobID)
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

func TestResolveDataflowJobRelationships_UnscannedTargetsSkipped(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("proj-1")

	jobID := upsertTestResource(t, st, "gcp", p.ID, TypeDataflowJob, "projects/proj-1/locations/us-central1/jobs/job-1", "us-central1",
		marshalAttrs(t, &dataflow.Job{
			Id: "job-1",
			Environment: &dataflow.Environment{
				ServiceAccountEmail: "not-scanned@proj-1.iam.gserviceaccount.com",
				ServiceKmsKeyName:   "projects/proj-1/locations/us-central1/keyRings/ring-1/cryptoKeys/not-scanned",
				WorkerPools: []*dataflow.WorkerPool{
					{Network: "not-scanned-net", Subnetwork: "regions/us-central1/subnetworks/not-scanned-sub"},
				},
			},
		}))

	if err := resolveDataflowJobRelationships(p, st); err != nil {
		t.Fatalf("resolveDataflowJobRelationships: %v", err)
	}
	rels, err := st.RelationshipsFrom(jobID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 0 {
		t.Errorf("want no edges for unscanned targets, got %+v", rels)
	}
}

func TestResolveDataflowJobRelationships_NilEnvironmentSkipped(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("proj-1")

	jobID := upsertTestResource(t, st, "gcp", p.ID, TypeDataflowJob, "projects/proj-1/locations/us-central1/jobs/job-1", "us-central1",
		marshalAttrs(t, &dataflow.Job{Id: "job-1"}))

	if err := resolveDataflowJobRelationships(p, st); err != nil {
		t.Fatalf("resolveDataflowJobRelationships: %v", err)
	}
	rels, err := st.RelationshipsFrom(jobID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 0 {
		t.Errorf("want no edges when environment is nil, got %+v", rels)
	}
}

func TestResolveDataflowJobRelationships_EmptyProjectNoResources(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("proj-1")
	if err := resolveDataflowJobRelationships(p, st); err != nil {
		t.Fatalf("resolveDataflowJobRelationships on empty project: %v", err)
	}
}

func TestResolveDataflowSnapshotRelationships_AllTargets(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("proj-1")

	jobID := upsertTestResource(t, st, "gcp", p.ID, TypeDataflowJob, "projects/proj-1/locations/us-central1/jobs/job-1", "us-central1",
		marshalAttrs(t, &dataflow.Job{Id: "job-1"}))

	topicNative := "projects/proj-1/topics/topic-1"
	topicID := upsertTestResource(t, st, "gcp", p.ID, TypePubSubTopic, topicNative, "",
		marshalAttrs(t, &pubsub.Topic{Name: topicNative}))

	snapID := upsertTestResource(t, st, "gcp", p.ID, TypeDataflowSnapshot, "projects/proj-1/locations/us-central1/snapshots/snap-1", "us-central1",
		marshalAttrs(t, &dataflow.Snapshot{
			Id:          "snap-1",
			SourceJobId: "job-1",
			PubsubMetadata: []*dataflow.PubsubSnapshotMetadata{
				{TopicName: topicNative},
			},
		}))

	if err := resolveDataflowSnapshotRelationships(p, st); err != nil {
		t.Fatalf("resolveDataflowSnapshotRelationships: %v", err)
	}
	rels, err := st.RelationshipsFrom(snapID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 2 {
		t.Fatalf("expected 2 edges, got %d: %+v", len(rels), rels)
	}
	want := map[string]bool{jobID: false, topicID: false}
	for _, r := range rels {
		if r.Kind != store.RelUses {
			t.Errorf("unexpected kind %q", r.Kind)
		}
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

func TestResolveDataflowSnapshotRelationships_WrongRegionNoMatch(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("proj-1")

	upsertTestResource(t, st, "gcp", p.ID, TypeDataflowJob, "projects/proj-1/locations/us-west1/jobs/job-1", "us-west1",
		marshalAttrs(t, &dataflow.Job{Id: "job-1"}))

	snapID := upsertTestResource(t, st, "gcp", p.ID, TypeDataflowSnapshot, "projects/proj-1/locations/us-central1/snapshots/snap-1", "us-central1",
		marshalAttrs(t, &dataflow.Snapshot{Id: "snap-1", SourceJobId: "job-1"}))

	if err := resolveDataflowSnapshotRelationships(p, st); err != nil {
		t.Fatalf("resolveDataflowSnapshotRelationships: %v", err)
	}
	rels, err := st.RelationshipsFrom(snapID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 0 {
		t.Errorf("want no edge when job exists only in a different region, got %+v", rels)
	}
}

func TestResolveDataflowSnapshotRelationships_UnscannedTopicSkipped(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("proj-1")

	snapID := upsertTestResource(t, st, "gcp", p.ID, TypeDataflowSnapshot, "projects/proj-1/locations/us-central1/snapshots/snap-1", "us-central1",
		marshalAttrs(t, &dataflow.Snapshot{
			Id: "snap-1",
			PubsubMetadata: []*dataflow.PubsubSnapshotMetadata{
				{TopicName: "projects/proj-1/topics/not-scanned"},
			},
		}))

	if err := resolveDataflowSnapshotRelationships(p, st); err != nil {
		t.Fatalf("resolveDataflowSnapshotRelationships: %v", err)
	}
	rels, err := st.RelationshipsFrom(snapID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 0 {
		t.Errorf("want no edge for unscanned topic, got %+v", rels)
	}
}

func TestResolveDataflowSnapshotRelationships_EmptyProjectNoResources(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("proj-1")
	if err := resolveDataflowSnapshotRelationships(p, st); err != nil {
		t.Fatalf("resolveDataflowSnapshotRelationships on empty project: %v", err)
	}
}

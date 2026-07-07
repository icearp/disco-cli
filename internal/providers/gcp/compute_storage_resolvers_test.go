package gcp

import (
	"testing"

	compute "google.golang.org/api/compute/v1"
)

func TestResolveComputeStorageLineageRelationships_FullChain(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("proj-1")

	imageID := upsertTestResource(t, st, "gcp", p.ID, TypeComputeImage, "projects/proj-1/global/images/base-image", "",
		marshalAttrs(t, &compute.Image{SelfLink: "projects/proj-1/global/images/base-image", Name: "base-image"}))
	instantSnapGroupID := upsertTestResource(t, st, "gcp", p.ID, TypeComputeInstantSnapshotGroup, "projects/proj-1/zones/us-central1-a/instantSnapshotGroups/g1", "us-central1-a",
		marshalAttrs(t, &compute.InstantSnapshotGroup{SelfLink: "projects/proj-1/zones/us-central1-a/instantSnapshotGroups/g1", Name: "g1"}))
	keyID := upsertTestResource(t, st, "gcp", p.ID, TypeKMSCryptoKey, "projects/proj-1/locations/us/keyRings/kr/cryptoKeys/k1", "",
		marshalAttrs(t, map[string]string{"name": "projects/proj-1/locations/us/keyRings/kr/cryptoKeys/k1"}))

	diskSelfLink := "projects/proj-1/zones/us-central1-a/disks/disk-1"
	diskID := upsertTestResource(t, st, "gcp", p.ID, TypeComputeDisk, diskSelfLink, "us-central1-a",
		marshalAttrs(t, &compute.Disk{
			SelfLink:          diskSelfLink,
			Name:              "disk-1",
			SourceImage:       "projects/proj-1/global/images/base-image",
			DiskEncryptionKey: &compute.CustomerEncryptionKey{KmsKeyName: "projects/proj-1/locations/us/keyRings/kr/cryptoKeys/k1/cryptoKeyVersions/2"},
		}))

	instantSnapSelfLink := "projects/proj-1/zones/us-central1-a/instantSnapshots/is-1"
	instantSnapID := upsertTestResource(t, st, "gcp", p.ID, TypeComputeInstantSnapshot, instantSnapSelfLink, "us-central1-a",
		marshalAttrs(t, &compute.InstantSnapshot{
			SelfLink:                   instantSnapSelfLink,
			Name:                       "is-1",
			SourceDisk:                 diskSelfLink,
			SourceInstantSnapshotGroup: "projects/proj-1/zones/us-central1-a/instantSnapshotGroups/g1",
		}))

	if err := resolveComputeStorageLineageRelationships(p, st); err != nil {
		t.Fatalf("resolveComputeStorageLineageRelationships: %v", err)
	}

	assertEdge := func(from, to, kind string) {
		t.Helper()
		rels, err := st.RelationshipsFrom(from)
		if err != nil {
			t.Fatalf("RelationshipsFrom(%s): %v", from, err)
		}
		for _, r := range rels {
			if r.ToID == to && r.Kind == kind {
				return
			}
		}
		t.Errorf("missing edge %s -[%s]-> %s (got %+v)", from, kind, to, rels)
	}

	assertEdge(diskID, imageID, "attached-to")
	assertEdge(diskID, keyID, "uses")
	assertEdge(instantSnapID, diskID, "attached-to")
	assertEdge(instantSnapID, instantSnapGroupID, "attached-to")

	// Negative space: the disk has no snapshot/instance source, so it must
	// carry no edges beyond the two asserted above.
	rels, err := st.RelationshipsFrom(diskID)
	if err != nil {
		t.Fatalf("RelationshipsFrom(diskID): %v", err)
	}
	if len(rels) != 2 {
		t.Errorf("want exactly 2 edges from disk, got %d: %+v", len(rels), rels)
	}
}

func TestResolveComputeStorageLineageRelationships_InstantSnapshotGroupResourcePolicy(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("proj-1")

	policySelfLink := "projects/proj-1/regions/us-central1/resourcePolicies/rp-1"
	policyID := upsertTestResource(t, st, "gcp", p.ID, TypeComputeResourcePolicy, policySelfLink, "us-central1",
		marshalAttrs(t, map[string]string{"selfLink": policySelfLink, "name": "rp-1"}))

	groupSelfLink := "projects/proj-1/regions/us-central1/instantSnapshotGroups/g1"
	groupID := upsertTestResource(t, st, "gcp", p.ID, TypeComputeRegionInstantSnapshotGroup, groupSelfLink, "us-central1",
		marshalAttrs(t, &compute.InstantSnapshotGroup{
			SelfLink:               groupSelfLink,
			Name:                   "g1",
			SourceConsistencyGroup: policySelfLink,
		}))

	if err := resolveComputeStorageLineageRelationships(p, st); err != nil {
		t.Fatalf("resolveComputeStorageLineageRelationships: %v", err)
	}

	rels, err := st.RelationshipsFrom(groupID)
	if err != nil {
		t.Fatalf("RelationshipsFrom(groupID): %v", err)
	}
	found := false
	for _, r := range rels {
		if r.ToID == policyID && r.Kind == "uses" {
			found = true
		}
	}
	if !found {
		t.Errorf("missing edge %s -[uses]-> %s (got %+v)", groupID, policyID, rels)
	}
}

func TestResolveComputeStorageLineageRelationships_InstantSnapshotGroupNoSourceConsistencyGroup(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("proj-1")

	groupSelfLink := "projects/proj-1/regions/us-central1/instantSnapshotGroups/g1"
	groupID := upsertTestResource(t, st, "gcp", p.ID, TypeComputeRegionInstantSnapshotGroup, groupSelfLink, "us-central1",
		marshalAttrs(t, &compute.InstantSnapshotGroup{SelfLink: groupSelfLink, Name: "g1"}))

	if err := resolveComputeStorageLineageRelationships(p, st); err != nil {
		t.Fatalf("resolveComputeStorageLineageRelationships: %v", err)
	}

	rels, err := st.RelationshipsFrom(groupID)
	if err != nil {
		t.Fatalf("RelationshipsFrom(groupID): %v", err)
	}
	if len(rels) != 0 {
		t.Errorf("want no edges when sourceConsistencyGroup is unset, got %+v", rels)
	}
}

func TestResolveComputeStorageLineageRelationships_UnscannedSourceSkipped(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("proj-1")

	diskSelfLink := "projects/proj-1/zones/us-central1-a/disks/disk-1"
	diskID := upsertTestResource(t, st, "gcp", p.ID, TypeComputeDisk, diskSelfLink, "us-central1-a",
		marshalAttrs(t, &compute.Disk{
			SelfLink:    diskSelfLink,
			Name:        "disk-1",
			SourceImage: "projects/other-proj/global/images/not-scanned",
		}))

	if err := resolveComputeStorageLineageRelationships(p, st); err != nil {
		t.Fatalf("resolveComputeStorageLineageRelationships: %v", err)
	}

	rels, err := st.RelationshipsFrom(diskID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 0 {
		t.Errorf("want no edges for unscanned source-image reference, got %+v", rels)
	}
}

func TestResolveComputeStorageLineageRelationships_EmptyProjectNoResources(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("proj-1")

	if err := resolveComputeStorageLineageRelationships(p, st); err != nil {
		t.Fatalf("resolveComputeStorageLineageRelationships on empty project: %v", err)
	}
}

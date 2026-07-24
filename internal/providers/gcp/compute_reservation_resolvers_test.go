package gcp

import (
	"testing"

	"github.com/icearp/disco-cli/store"
	compute "google.golang.org/api/compute/v1"
)

func TestResolveReservationRelationships_CommitmentAndLinkedCommitments(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("my-project")

	commitSelfLink := "https://www.googleapis.com/compute/v1/projects/my-project/regions/us-central1/commitments/commit-1"
	linkedSelfLink := "https://www.googleapis.com/compute/v1/projects/my-project/regions/us-central1/commitments/commit-2"
	commitID := upsertTestResource(t, st, "gcp", p.ID, TypeComputeRegionCommitment, commitSelfLink, "us-central1", "{}")
	linkedID := upsertTestResource(t, st, "gcp", p.ID, TypeComputeRegionCommitment, linkedSelfLink, "us-central1", "{}")

	resSelfLink := "https://www.googleapis.com/compute/v1/projects/my-project/zones/us-central1-a/reservations/res-1"
	resID := upsertTestResource(t, st, "gcp", p.ID, TypeComputeReservation, resSelfLink, "us-central1-a",
		marshalAttrs(t, &compute.Reservation{
			Name:              "res-1",
			SelfLink:          resSelfLink,
			Commitment:        commitSelfLink,
			LinkedCommitments: []string{linkedSelfLink},
		}))

	if err := resolveReservationRelationships(p, st); err != nil {
		t.Fatalf("resolveReservationRelationships: %v", err)
	}
	rels, err := st.RelationshipsFrom(resID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	want := map[string]bool{commitID: false, linkedID: false}
	for _, rel := range rels {
		if _, ok := want[rel.ToID]; !ok {
			t.Fatalf("unexpected edge target %s", rel.ToID)
		}
		if rel.Kind != store.RelUses {
			t.Errorf("got kind %s, want %s", rel.Kind, store.RelUses)
		}
		want[rel.ToID] = true
	}
	for id, seen := range want {
		if !seen {
			t.Errorf("missing edge to %s", id)
		}
	}
}

func TestResolveReservationRelationships_UnscannedCommitmentSkipped(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("my-project")

	resSelfLink := "https://www.googleapis.com/compute/v1/projects/my-project/zones/us-central1-a/reservations/res-1"
	resID := upsertTestResource(t, st, "gcp", p.ID, TypeComputeReservation, resSelfLink, "us-central1-a",
		marshalAttrs(t, &compute.Reservation{
			Name:       "res-1",
			SelfLink:   resSelfLink,
			Commitment: "https://www.googleapis.com/compute/v1/projects/my-project/regions/us-central1/commitments/not-scanned",
		}))

	if err := resolveReservationRelationships(p, st); err != nil {
		t.Fatalf("resolveReservationRelationships: %v", err)
	}
	rels, err := st.RelationshipsFrom(resID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 0 {
		t.Errorf("want no edge for unscanned commitment, got %+v", rels)
	}
}

func TestResolveReservationRelationships_EmptyProjectNoResources(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("my-project")
	if err := resolveReservationRelationships(p, st); err != nil {
		t.Fatalf("resolveReservationRelationships on empty project: %v", err)
	}
}

func TestResolveFutureReservationRelationships_BareCommitmentName(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("my-project")

	commitSelfLink := "https://www.googleapis.com/compute/v1/projects/my-project/regions/us-central1/commitments/commit-1"
	commitID := upsertTestResource(t, st, "gcp", p.ID, TypeComputeRegionCommitment, commitSelfLink, "us-central1", "{}")

	frSelfLink := "https://www.googleapis.com/compute/v1/projects/my-project/regions/us-central1/futureReservations/fr-1"
	frID := upsertTestResource(t, st, "gcp", p.ID, TypeComputeFutureReservation, frSelfLink, "us-central1",
		marshalAttrs(t, &compute.FutureReservation{
			Name:           "fr-1",
			SelfLink:       frSelfLink,
			CommitmentInfo: &compute.FutureReservationCommitmentInfo{CommitmentName: "commit-1"},
		}))

	if err := resolveFutureReservationRelationships(p, st); err != nil {
		t.Fatalf("resolveFutureReservationRelationships: %v", err)
	}
	rels, err := st.RelationshipsFrom(frID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 1 || rels[0].ToID != commitID || rels[0].Kind != store.RelUses {
		t.Errorf("got %+v, want →commitment uses", rels)
	}
}

func TestResolveFutureReservationRelationships_UnmatchedCommitmentNameSkipped(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("my-project")

	commitSelfLink := "https://www.googleapis.com/compute/v1/projects/my-project/regions/us-central1/commitments/commit-1"
	upsertTestResource(t, st, "gcp", p.ID, TypeComputeRegionCommitment, commitSelfLink, "us-central1", "{}")

	frSelfLink := "https://www.googleapis.com/compute/v1/projects/my-project/regions/us-central1/futureReservations/fr-1"
	frID := upsertTestResource(t, st, "gcp", p.ID, TypeComputeFutureReservation, frSelfLink, "us-central1",
		marshalAttrs(t, &compute.FutureReservation{
			Name:           "fr-1",
			SelfLink:       frSelfLink,
			CommitmentInfo: &compute.FutureReservationCommitmentInfo{CommitmentName: "not-scanned"},
		}))

	if err := resolveFutureReservationRelationships(p, st); err != nil {
		t.Fatalf("resolveFutureReservationRelationships: %v", err)
	}
	rels, err := st.RelationshipsFrom(frID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 0 {
		t.Errorf("want no edge for unmatched commitment name, got %+v", rels)
	}
}

func TestResolveFutureReservationRelationships_NoCommitmentInfoNoPanic(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("my-project")

	frSelfLink := "https://www.googleapis.com/compute/v1/projects/my-project/regions/us-central1/futureReservations/fr-1"
	frID := upsertTestResource(t, st, "gcp", p.ID, TypeComputeFutureReservation, frSelfLink, "us-central1",
		marshalAttrs(t, &compute.FutureReservation{Name: "fr-1", SelfLink: frSelfLink}))

	if err := resolveFutureReservationRelationships(p, st); err != nil {
		t.Fatalf("resolveFutureReservationRelationships: %v", err)
	}
	rels, err := st.RelationshipsFrom(frID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 0 {
		t.Errorf("want no edge when commitmentInfo is unset, got %+v", rels)
	}
}

func TestResolveFutureReservationRelationships_EmptyProjectNoResources(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("my-project")
	if err := resolveFutureReservationRelationships(p, st); err != nil {
		t.Fatalf("resolveFutureReservationRelationships on empty project: %v", err)
	}
}

package gcp

import (
	"errors"
	"net/http"
	"testing"

	"github.com/icearp/disco-cli/store"
	"google.golang.org/api/compute/v1"
)

func TestScanComputeAutoscalers_Fake(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("my-project")

	zonalLink := "https://www.googleapis.com/compute/v1/projects/my-project/zones/us-central1-a/autoscalers/as-z"
	regionLink := "https://www.googleapis.com/compute/v1/projects/my-project/regions/us-central1/autoscalers/as-r"
	page := compute.AutoscalerAggregatedList{
		Items: map[string]compute.AutoscalersScopedList{
			"zones/us-central1-a": {Autoscalers: []*compute.Autoscaler{{
				Name: "as-z", SelfLink: zonalLink,
				Zone: "https://www.googleapis.com/compute/v1/projects/my-project/zones/us-central1-a",
			}}},
			"regions/us-central1": {Autoscalers: []*compute.Autoscaler{{
				Name: "as-r", SelfLink: regionLink,
				Region: "https://www.googleapis.com/compute/v1/projects/my-project/regions/us-central1",
			}}},
		},
	}
	srv := fakeGCPServer(t, map[string]string{
		"/projects/my-project/aggregated/autoscalers": marshalAttrs(t, page),
	})
	svc := fakeComputeService(t, srv)

	total, inserted, err := scanComputeAutoscalers(t.Context(), svc, p, st, testScanID)
	if err != nil {
		t.Fatalf("scanComputeAutoscalers: %v", err)
	}
	if total != 2 || inserted != 2 {
		t.Fatalf("counts: got total=%d inserted=%d, want 2/2", total, inserted)
	}
	z, err := st.GetResource(store.ResourceID("gcp", p.ID, zonalLink))
	if err != nil {
		t.Fatalf("GetResource(zonal): %v", err)
	}
	if z.Zone == nil || *z.Zone != "us-central1-a" {
		t.Errorf("zonal autoscaler zone: got %v, want us-central1-a", z.Zone)
	}
	if z.Region == nil || *z.Region != "us-central1" {
		t.Errorf("zonal autoscaler region: got %v, want us-central1", z.Region)
	}
	r, err := st.GetResource(store.ResourceID("gcp", p.ID, regionLink))
	if err != nil {
		t.Fatalf("GetResource(region): %v", err)
	}
	if r.Zone != nil {
		t.Errorf("region autoscaler zone: got %v, want nil", *r.Zone)
	}
	if r.Region == nil || *r.Region != "us-central1" {
		t.Errorf("region autoscaler region: got %v, want us-central1", r.Region)
	}
}

func TestScanComputeAutoscalers_PermissionDenied(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("my-project")

	body := `{"error":{"code":403,"message":"caller is missing compute.autoscalers.list","errors":[{"reason":"forbidden"}]}}`
	srv := fakeGCPServerStatus(t, http.StatusForbidden, body)
	svc := fakeComputeService(t, srv)

	total, inserted, err := scanComputeAutoscalers(t.Context(), svc, p, st, testScanID)
	if err != nil {
		t.Fatalf("scanComputeAutoscalers (denied): expected nil error, got %v", err)
	}
	if total != 0 || inserted != 0 {
		t.Fatalf("counts: got total=%d inserted=%d, want 0/0", total, inserted)
	}
}

func TestScanComputeAutoscalers_APINotEnabled(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("my-project")

	body := `{"error":{"code":403,"message":"Compute Engine API has not been used in project my-project before or it is disabled.","errors":[{"reason":"accessNotConfigured"}]}}`
	srv := fakeGCPServerStatus(t, http.StatusForbidden, body)
	svc := fakeComputeService(t, srv)

	_, _, err := scanComputeAutoscalers(t.Context(), svc, p, st, testScanID)
	if !errors.Is(err, errServiceDisabled) {
		t.Fatalf("scanComputeAutoscalers: expected errServiceDisabled sentinel, got %v", err)
	}
}

// TestScanComputeReservations_NestedFanout exercises the full 3-level
// list-then-fanout pipeline: Reservation -> ReservationBlock ->
// ReservationSubBlock, each level fed by the previous level's discovered
// refs rather than a fixture the test hand-assembles.
func TestScanComputeReservations_NestedFanout(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("my-project")

	resSelfLink := "https://www.googleapis.com/compute/v1/projects/my-project/zones/us-central1-a/reservations/res1"
	resPage := compute.ReservationAggregatedList{
		Items: map[string]compute.ReservationsScopedList{
			"zones/us-central1-a": {Reservations: []*compute.Reservation{{
				Name: "res1", SelfLink: resSelfLink,
				Zone: "https://www.googleapis.com/compute/v1/projects/my-project/zones/us-central1-a",
			}}},
		},
	}
	blockSelfLink := "https://www.googleapis.com/compute/v1/projects/my-project/zones/us-central1-a/reservations/res1/reservationBlocks/block1"
	blockPage := compute.ReservationBlocksListResponse{
		Items: []*compute.ReservationBlock{{Name: "block1", SelfLink: blockSelfLink}},
	}
	subBlockSelfLink := "https://www.googleapis.com/compute/v1/projects/my-project/zones/us-central1-a/reservations/res1/reservationBlocks/block1/reservationSubBlocks/sub1"
	subBlockPage := compute.ReservationSubBlocksListResponse{
		Items: []*compute.ReservationSubBlock{{Name: "sub1", SelfLink: subBlockSelfLink}},
	}

	srv := fakeGCPServer(t, map[string]string{
		"/projects/my-project/aggregated/reservations":                                                             marshalAttrs(t, resPage),
		"/projects/my-project/zones/us-central1-a/reservations/res1/reservationBlocks":                             marshalAttrs(t, blockPage),
		"/projects/my-project/zones/us-central1-a/reservations/res1/reservationBlocks/block1/reservationSubBlocks": marshalAttrs(t, subBlockPage),
	})
	svc := fakeComputeService(t, srv)

	total, inserted, err := scanComputeReservations(t.Context(), svc, p, st, testScanID)
	if err != nil {
		t.Fatalf("scanComputeReservations: %v", err)
	}
	if total != 3 || inserted != 3 {
		t.Fatalf("counts: got total=%d inserted=%d, want 3/3 (reservation+block+subblock)", total, inserted)
	}
	if _, err := st.GetResource(store.ResourceID("gcp", p.ID, resSelfLink)); err != nil {
		t.Errorf("GetResource(reservation): %v", err)
	}
	if _, err := st.GetResource(store.ResourceID("gcp", p.ID, blockSelfLink)); err != nil {
		t.Errorf("GetResource(block): %v", err)
	}
	if _, err := st.GetResource(store.ResourceID("gcp", p.ID, subBlockSelfLink)); err != nil {
		t.Errorf("GetResource(subblock): %v", err)
	}

	resID := store.ResourceID("gcp", p.ID, resSelfLink)
	blockID := store.ResourceID("gcp", p.ID, blockSelfLink)
	subBlockID := store.ResourceID("gcp", p.ID, subBlockSelfLink)

	resRels, err := st.RelationshipsFrom(resID, store.RelContains)
	if err != nil {
		t.Fatalf("RelationshipsFrom(reservation): %v", err)
	}
	if len(resRels) != 1 || resRels[0].ToID != blockID {
		t.Errorf("reservation contains: got %+v, want single edge to block %q", resRels, blockID)
	}

	blockRels, err := st.RelationshipsFrom(blockID, store.RelContains)
	if err != nil {
		t.Fatalf("RelationshipsFrom(block): %v", err)
	}
	if len(blockRels) != 1 || blockRels[0].ToID != subBlockID {
		t.Errorf("block contains: got %+v, want single edge to subblock %q", blockRels, subBlockID)
	}
}

func TestScanComputeReservations_NoRefsSkipsFanout(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("my-project")

	page := compute.ReservationAggregatedList{Items: map[string]compute.ReservationsScopedList{}}
	srv := fakeGCPServer(t, map[string]string{
		"/projects/my-project/aggregated/reservations": marshalAttrs(t, page),
	})
	svc := fakeComputeService(t, srv)

	total, inserted, err := scanComputeReservations(t.Context(), svc, p, st, testScanID)
	if err != nil {
		t.Fatalf("scanComputeReservations: %v", err)
	}
	if total != 0 || inserted != 0 {
		t.Fatalf("counts: got total=%d inserted=%d, want 0/0", total, inserted)
	}
}

func TestScanComputeFutureReservations_Fake(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("my-project")

	selfLink := "https://www.googleapis.com/compute/v1/projects/my-project/zones/us-central1-a/futureReservations/fr1"
	page := compute.FutureReservationsAggregatedListResponse{
		Items: map[string]compute.FutureReservationsScopedList{
			"zones/us-central1-a": {FutureReservations: []*compute.FutureReservation{{
				Name: "fr1", SelfLink: selfLink,
				Zone: "https://www.googleapis.com/compute/v1/projects/my-project/zones/us-central1-a",
			}}},
		},
	}
	srv := fakeGCPServer(t, map[string]string{
		"/projects/my-project/aggregated/futureReservations": marshalAttrs(t, page),
	})
	svc := fakeComputeService(t, srv)

	total, inserted, err := scanComputeFutureReservations(t.Context(), svc, p, st, testScanID)
	if err != nil {
		t.Fatalf("scanComputeFutureReservations: %v", err)
	}
	if total != 1 || inserted != 1 {
		t.Fatalf("counts: got total=%d inserted=%d, want 1/1", total, inserted)
	}
	got, err := st.GetResource(store.ResourceID("gcp", p.ID, selfLink))
	if err != nil {
		t.Fatalf("GetResource: %v", err)
	}
	if got.Zone == nil || *got.Zone != "us-central1-a" {
		t.Errorf("future reservation zone: got %v, want us-central1-a", got.Zone)
	}
}

func TestScanComputeRegionCommitments_Fake(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("my-project")

	selfLink := "https://www.googleapis.com/compute/v1/projects/my-project/regions/us-central1/commitments/c1"
	page := compute.CommitmentAggregatedList{
		Items: map[string]compute.CommitmentsScopedList{
			"regions/us-central1": {Commitments: []*compute.Commitment{{Name: "c1", SelfLink: selfLink}}},
		},
	}
	srv := fakeGCPServer(t, map[string]string{
		"/projects/my-project/aggregated/commitments": marshalAttrs(t, page),
	})
	svc := fakeComputeService(t, srv)

	total, inserted, err := scanComputeRegionCommitments(t.Context(), svc, p, st, testScanID)
	if err != nil {
		t.Fatalf("scanComputeRegionCommitments: %v", err)
	}
	if total != 1 || inserted != 1 {
		t.Fatalf("counts: got total=%d inserted=%d, want 1/1", total, inserted)
	}
	got, err := st.GetResource(store.ResourceID("gcp", p.ID, selfLink))
	if err != nil {
		t.Fatalf("GetResource: %v", err)
	}
	if got.Region == nil || *got.Region != "us-central1" {
		t.Errorf("region commitment region: got %v, want us-central1", got.Region)
	}
}

func TestScanComputeResourcePolicies_Fake(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("my-project")

	selfLink := "https://www.googleapis.com/compute/v1/projects/my-project/regions/us-central1/resourcePolicies/rp1"
	page := compute.ResourcePolicyAggregatedList{
		Items: map[string]compute.ResourcePoliciesScopedList{
			"regions/us-central1": {ResourcePolicies: []*compute.ResourcePolicy{{Name: "rp1", SelfLink: selfLink}}},
		},
	}
	srv := fakeGCPServer(t, map[string]string{
		"/projects/my-project/aggregated/resourcePolicies": marshalAttrs(t, page),
	})
	svc := fakeComputeService(t, srv)

	total, inserted, err := scanComputeResourcePolicies(t.Context(), svc, p, st, testScanID)
	if err != nil {
		t.Fatalf("scanComputeResourcePolicies: %v", err)
	}
	if total != 1 || inserted != 1 {
		t.Fatalf("counts: got total=%d inserted=%d, want 1/1", total, inserted)
	}
	got, err := st.GetResource(store.ResourceID("gcp", p.ID, selfLink))
	if err != nil {
		t.Fatalf("GetResource: %v", err)
	}
	if got.Region == nil || *got.Region != "us-central1" {
		t.Errorf("resource policy region: got %v, want us-central1", got.Region)
	}
}

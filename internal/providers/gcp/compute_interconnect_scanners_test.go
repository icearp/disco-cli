package gcp

import (
	"errors"
	"net/http"
	"testing"

	"codeberg.org/icearp/disco/store"
	"google.golang.org/api/compute/v1"
)

func TestScanComputeInterconnects_Fake(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("my-project")

	selfLink := "https://www.googleapis.com/compute/v1/projects/my-project/global/interconnects/ic1"
	page := compute.InterconnectList{Items: []*compute.Interconnect{{Name: "ic1", SelfLink: selfLink}}}
	srv := fakeGCPServer(t, map[string]string{
		"/projects/my-project/global/interconnects": marshalAttrs(t, page),
	})
	svc := fakeComputeService(t, srv)

	total, inserted, err := scanComputeInterconnects(t.Context(), svc, p, st, testScanID)
	if err != nil {
		t.Fatalf("scanComputeInterconnects: %v", err)
	}
	if total != 1 || inserted != 1 {
		t.Fatalf("counts: got total=%d inserted=%d, want 1/1", total, inserted)
	}
	if _, err := st.GetResource(store.ResourceID("gcp", p.ID, TypeComputeInterconnect, selfLink)); err != nil {
		t.Errorf("GetResource: %v", err)
	}
}

func TestScanComputeInterconnects_PermissionDenied(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("my-project")

	body := `{"error":{"code":403,"message":"caller is missing compute.interconnects.list","errors":[{"reason":"forbidden"}]}}`
	srv := fakeGCPServerStatus(t, http.StatusForbidden, body)
	svc := fakeComputeService(t, srv)

	total, inserted, err := scanComputeInterconnects(t.Context(), svc, p, st, testScanID)
	if err != nil {
		t.Fatalf("scanComputeInterconnects (denied): expected nil error, got %v", err)
	}
	if total != 0 || inserted != 0 {
		t.Fatalf("counts: got total=%d inserted=%d, want 0/0", total, inserted)
	}
}

func TestScanComputeInterconnects_APINotEnabled(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("my-project")

	body := `{"error":{"code":403,"message":"Compute Engine API has not been used in project my-project before or it is disabled.","errors":[{"reason":"accessNotConfigured"}]}}`
	srv := fakeGCPServerStatus(t, http.StatusForbidden, body)
	svc := fakeComputeService(t, srv)

	_, _, err := scanComputeInterconnects(t.Context(), svc, p, st, testScanID)
	if !errors.Is(err, errServiceDisabled) {
		t.Fatalf("scanComputeInterconnects: expected errServiceDisabled sentinel, got %v", err)
	}
}

func TestScanComputeInterconnectAttachments_Fake(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("my-project")

	selfLink := "https://www.googleapis.com/compute/v1/projects/my-project/regions/us-central1/interconnectAttachments/ia1"
	page := compute.InterconnectAttachmentAggregatedList{
		Items: map[string]compute.InterconnectAttachmentsScopedList{
			"regions/us-central1": {InterconnectAttachments: []*compute.InterconnectAttachment{{Name: "ia1", SelfLink: selfLink}}},
		},
	}
	srv := fakeGCPServer(t, map[string]string{
		"/projects/my-project/aggregated/interconnectAttachments": marshalAttrs(t, page),
	})
	svc := fakeComputeService(t, srv)

	total, inserted, err := scanComputeInterconnectAttachments(t.Context(), svc, p, st, testScanID)
	if err != nil {
		t.Fatalf("scanComputeInterconnectAttachments: %v", err)
	}
	if total != 1 || inserted != 1 {
		t.Fatalf("counts: got total=%d inserted=%d, want 1/1", total, inserted)
	}
	got, err := st.GetResource(store.ResourceID("gcp", p.ID, TypeComputeInterconnectAttachment, selfLink))
	if err != nil {
		t.Fatalf("GetResource: %v", err)
	}
	if got.Region == nil || *got.Region != "us-central1" {
		t.Errorf("interconnect attachment region: got %v, want us-central1", got.Region)
	}
}

func TestScanComputeInterconnectGroups_Fake(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("my-project")

	selfLink := "https://www.googleapis.com/compute/v1/projects/my-project/global/interconnectGroups/ig1"
	page := compute.InterconnectGroupsListResponse{Items: []*compute.InterconnectGroup{{Name: "ig1", SelfLink: selfLink}}}
	srv := fakeGCPServer(t, map[string]string{
		"/projects/my-project/global/interconnectGroups": marshalAttrs(t, page),
	})
	svc := fakeComputeService(t, srv)

	total, inserted, err := scanComputeInterconnectGroups(t.Context(), svc, p, st, testScanID)
	if err != nil {
		t.Fatalf("scanComputeInterconnectGroups: %v", err)
	}
	if total != 1 || inserted != 1 {
		t.Fatalf("counts: got total=%d inserted=%d, want 1/1", total, inserted)
	}
}

func TestScanComputeInterconnectAttachmentGroups_Fake(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("my-project")

	selfLink := "https://www.googleapis.com/compute/v1/projects/my-project/global/interconnectAttachmentGroups/iag1"
	page := compute.InterconnectAttachmentGroupsListResponse{Items: []*compute.InterconnectAttachmentGroup{{Name: "iag1", SelfLink: selfLink}}}
	srv := fakeGCPServer(t, map[string]string{
		"/projects/my-project/global/interconnectAttachmentGroups": marshalAttrs(t, page),
	})
	svc := fakeComputeService(t, srv)

	total, inserted, err := scanComputeInterconnectAttachmentGroups(t.Context(), svc, p, st, testScanID)
	if err != nil {
		t.Fatalf("scanComputeInterconnectAttachmentGroups: %v", err)
	}
	if total != 1 || inserted != 1 {
		t.Fatalf("counts: got total=%d inserted=%d, want 1/1", total, inserted)
	}
}

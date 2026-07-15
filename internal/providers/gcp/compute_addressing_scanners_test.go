package gcp

import (
	"errors"
	"net/http"
	"testing"

	"codeberg.org/icearp/disco/store"
	"google.golang.org/api/compute/v1"
)

func TestScanComputeAddresses_Fake(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("my-project")

	addrSelfLink := "https://www.googleapis.com/compute/v1/projects/my-project/regions/us-central1/addresses/addr1"
	page := compute.AddressAggregatedList{
		Items: map[string]compute.AddressesScopedList{
			"regions/us-central1": {
				Addresses: []*compute.Address{{Name: "addr1", SelfLink: addrSelfLink, Status: "RESERVED"}},
			},
		},
	}
	srv := fakeGCPServer(t, map[string]string{
		"/projects/my-project/aggregated/addresses": marshalAttrs(t, page),
	})
	svc := fakeComputeService(t, srv)

	total, inserted, err := scanComputeAddresses(t.Context(), svc, p, st, testScanID)
	if err != nil {
		t.Fatalf("scanComputeAddresses: %v", err)
	}
	if total != 1 || inserted != 1 {
		t.Fatalf("counts: got total=%d inserted=%d, want 1/1", total, inserted)
	}

	id := store.ResourceID("gcp", p.ID, addrSelfLink)
	got, err := st.GetResource(id)
	if err != nil {
		t.Fatalf("GetResource: %v", err)
	}
	if got.Region == nil || *got.Region != "us-central1" {
		t.Errorf("address region: got %v, want us-central1", got.Region)
	}
}

func TestScanComputeAddresses_PermissionDenied(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("my-project")

	body := `{"error":{"code":403,"message":"caller is missing compute.addresses.list","errors":[{"reason":"forbidden"}]}}`
	srv := fakeGCPServerStatus(t, http.StatusForbidden, body)
	svc := fakeComputeService(t, srv)

	total, inserted, err := scanComputeAddresses(t.Context(), svc, p, st, testScanID)
	if err != nil {
		t.Fatalf("scanComputeAddresses (denied): expected nil error, got %v", err)
	}
	if total != 0 || inserted != 0 {
		t.Fatalf("counts: got total=%d inserted=%d, want 0/0", total, inserted)
	}
}

func TestScanComputeAddresses_APINotEnabled(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("my-project")

	body := `{"error":{"code":403,"message":"Compute Engine API has not been used in project my-project before or it is disabled.","errors":[{"reason":"accessNotConfigured"}]}}`
	srv := fakeGCPServerStatus(t, http.StatusForbidden, body)
	svc := fakeComputeService(t, srv)

	_, _, err := scanComputeAddresses(t.Context(), svc, p, st, testScanID)
	if !errors.Is(err, errServiceDisabled) {
		t.Fatalf("scanComputeAddresses: expected errServiceDisabled sentinel, got %v", err)
	}
}

func TestScanComputeGlobalAddresses_Fake(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("my-project")

	addrSelfLink := "https://www.googleapis.com/compute/v1/projects/my-project/global/addresses/gaddr1"
	page := compute.AddressList{Items: []*compute.Address{{Name: "gaddr1", SelfLink: addrSelfLink}}}
	srv := fakeGCPServer(t, map[string]string{
		"/projects/my-project/global/addresses": marshalAttrs(t, page),
	})
	svc := fakeComputeService(t, srv)

	total, inserted, err := scanComputeGlobalAddresses(t.Context(), svc, p, st, testScanID)
	if err != nil {
		t.Fatalf("scanComputeGlobalAddresses: %v", err)
	}
	if total != 1 || inserted != 1 {
		t.Fatalf("counts: got total=%d inserted=%d, want 1/1", total, inserted)
	}
}

func TestScanComputePublicAdvertisedPrefixes_Fake(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("my-project")

	papSelfLink := "https://www.googleapis.com/compute/v1/projects/my-project/global/publicAdvertisedPrefixes/pap1"
	page := compute.PublicAdvertisedPrefixList{Items: []*compute.PublicAdvertisedPrefix{{Name: "pap1", SelfLink: papSelfLink}}}
	srv := fakeGCPServer(t, map[string]string{
		"/projects/my-project/global/publicAdvertisedPrefixes": marshalAttrs(t, page),
	})
	svc := fakeComputeService(t, srv)

	total, inserted, err := scanComputePublicAdvertisedPrefixes(t.Context(), svc, p, st, testScanID)
	if err != nil {
		t.Fatalf("scanComputePublicAdvertisedPrefixes: %v", err)
	}
	if total != 1 || inserted != 1 {
		t.Fatalf("counts: got total=%d inserted=%d, want 1/1", total, inserted)
	}
}

func TestScanComputePublicDelegatedPrefixes_Fake(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("my-project")

	globalSelfLink := "https://www.googleapis.com/compute/v1/projects/my-project/global/publicDelegatedPrefixes/pdp-global"
	regionalSelfLink := "https://www.googleapis.com/compute/v1/projects/my-project/regions/us-central1/publicDelegatedPrefixes/pdp-regional"
	page := compute.PublicDelegatedPrefixAggregatedList{
		Items: map[string]compute.PublicDelegatedPrefixesScopedList{
			"global": {
				PublicDelegatedPrefixes: []*compute.PublicDelegatedPrefix{{Name: "pdp-global", SelfLink: globalSelfLink}},
			},
			"regions/us-central1": {
				PublicDelegatedPrefixes: []*compute.PublicDelegatedPrefix{{Name: "pdp-regional", SelfLink: regionalSelfLink}},
			},
		},
	}
	srv := fakeGCPServer(t, map[string]string{
		"/projects/my-project/aggregated/publicDelegatedPrefixes": marshalAttrs(t, page),
	})
	svc := fakeComputeService(t, srv)

	total, inserted, err := scanComputePublicDelegatedPrefixes(t.Context(), svc, p, st, testScanID)
	if err != nil {
		t.Fatalf("scanComputePublicDelegatedPrefixes: %v", err)
	}
	if total != 2 || inserted != 2 {
		t.Fatalf("counts: got total=%d inserted=%d, want 2/2", total, inserted)
	}

	globalID := store.ResourceID("gcp", p.ID, globalSelfLink)
	got, err := st.GetResource(globalID)
	if err != nil {
		t.Fatalf("GetResource(global): %v", err)
	}
	if got.Region != nil {
		t.Errorf("global prefix should have no region, got %v", got.Region)
	}

	regionalID := store.ResourceID("gcp", p.ID, regionalSelfLink)
	got2, err := st.GetResource(regionalID)
	if err != nil {
		t.Fatalf("GetResource(regional): %v", err)
	}
	if got2.Region == nil || *got2.Region != "us-central1" {
		t.Errorf("regional prefix region: got %v, want us-central1", got2.Region)
	}
}

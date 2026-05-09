package gcp

import (
	"net/http"
	"strings"
	"testing"

	"codeberg.org/icearp/disco/store"
	"google.golang.org/api/compute/v1"
)

// TestScanForwardingRules_Fake exercises the scanner end-to-end against an
// httptest-backed *compute.Service. Verifies the runPaginated pipeline,
// AggregatedList scope-key flattening (scopedListRegion), and resource
// upsert + project-closure emission. Mirrors the in-process fake-server
// pattern recommended in googleapis/google-cloud-go/testing.md, applied to
// the HTTP discovery client.
func TestScanForwardingRules_Fake(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("my-project")

	frSelfLink := "https://www.googleapis.com/compute/v1/projects/my-project/regions/us-central1/forwardingRules/fr1"

	page := compute.ForwardingRuleAggregatedList{
		Items: map[string]compute.ForwardingRulesScopedList{
			"regions/us-central1": {
				ForwardingRules: []*compute.ForwardingRule{{
					Name:      "fr1",
					IPAddress: "10.0.0.1",
					SelfLink:  frSelfLink,
				}},
			},
		},
	}

	srv := fakeGCPServer(t, map[string]string{
		"/projects/my-project/aggregated/forwardingRules": marshalAttrs(t, page),
	})
	svc := fakeComputeService(t, srv)

	total, inserted, err := scanForwardingRules(t.Context(), svc, p, st, testScanID)
	if err != nil {
		t.Fatalf("scanForwardingRules: %v", err)
	}
	if total != 1 || inserted != 1 {
		t.Fatalf("counts: got total=%d inserted=%d, want 1/1", total, inserted)
	}

	id := store.ResourceID("gcp", p.ID, TypeComputeForwardingRule, frSelfLink)
	got, err := st.GetResource(id)
	if err != nil {
		t.Fatalf("GetResource: %v", err)
	}
	if got.Name == nil || *got.Name != "fr1" {
		t.Errorf("forwarding rule name: got %v, want fr1", got.Name)
	}
	if got.Region == nil || *got.Region != "us-central1" {
		t.Errorf("forwarding rule region: got %v, want us-central1", got.Region)
	}
}

// TestScanForwardingRules_PermissionDenied verifies a real 403 is downgraded
// to a ScanWarning (not propagated) by skipIfDenied — the message shape used
// here lacks the "has not been used in project" sentinel marker, so this
// exercises the warning path rather than the service-disabled sentinel.
func TestScanForwardingRules_PermissionDenied(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("my-project")

	body := `{"error":{"code":403,"message":"caller is missing compute.forwardingRules.list","errors":[{"reason":"forbidden","message":"missing permission"}]}}`
	srv := fakeGCPServerStatus(t, http.StatusForbidden, body)
	svc := fakeComputeService(t, srv)

	total, inserted, err := scanForwardingRules(t.Context(), svc, p, st, testScanID)
	if err != nil {
		t.Fatalf("scanForwardingRules (denied): expected nil error, got %v", err)
	}
	if total != 0 || inserted != 0 {
		t.Fatalf("counts: got total=%d inserted=%d, want 0/0", total, inserted)
	}
}

// TestScanForwardingRules_APINotEnabled verifies an API-not-enabled 403 is
// translated into the errServiceDisabled sentinel by skipIfDenied →
// markServiceDisabled. The dispatch loop in scanProject (untested here)
// detects the sentinel via errors.Is and renders "(service disabled)".
func TestScanForwardingRules_APINotEnabled(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("my-project")

	body := `{"error":{"code":403,"message":"Compute Engine API has not been used in project my-project before or it is disabled.","errors":[{"reason":"accessNotConfigured"}]}}`
	srv := fakeGCPServerStatus(t, http.StatusForbidden, body)
	svc := fakeComputeService(t, srv)

	_, _, err := scanForwardingRules(t.Context(), svc, p, st, testScanID)
	if err == nil {
		t.Fatalf("scanForwardingRules: expected errServiceDisabled sentinel, got nil")
	}
	if !strings.Contains(err.Error(), "gcp service not enabled") {
		t.Errorf("expected service-disabled sentinel, got %v", err)
	}
}

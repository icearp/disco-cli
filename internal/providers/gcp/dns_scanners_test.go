package gcp

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"codeberg.org/icearp/disco/store"
	"google.golang.org/api/dns/v1"
	"google.golang.org/api/option"
)

// fakeDNSService builds a *dns.Service pointed at the fake server. Unlike
// cloudkms/cloudresourcemanager, dns's BasePath is version-less but its route
// templates embed the full "dns/v1/" prefix (not just "v1/") — route keys
// below need that exact prefix.
func fakeDNSService(t *testing.T, srv *httptest.Server) *dns.Service {
	t.Helper()
	svc, err := dns.NewService(
		t.Context(),
		option.WithEndpoint(srv.URL),
		option.WithHTTPClient(srv.Client()),
		option.WithoutAuthentication(),
	)
	if err != nil {
		t.Fatalf("dns.NewService: %v", err)
	}
	return svc
}

func TestScanCloudDNS_FullFanout(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("my-project")

	zoneName := "zone1"
	zoneNativeID := "projects/my-project/managedZones/zone1"
	zoneID := store.ResourceID("gcp", p.ID, zoneNativeID)

	rpName := "rp1"
	rpNativeID := "projects/my-project/responsePolicies/rp1"
	rpID := store.ResourceID("gcp", p.ID, rpNativeID)

	routes := map[string]string{
		"/dns/v1/projects/my-project/managedZones": marshalAttrs(t, dns.ManagedZonesListResponse{
			ManagedZones: []*dns.ManagedZone{{Name: zoneName}},
		}),
		"/dns/v1/projects/my-project/managedZones/zone1/rrsets": marshalAttrs(t, dns.ResourceRecordSetsListResponse{
			Rrsets: []*dns.ResourceRecordSet{{Name: "www.example.com.", Type: "A"}},
		}),
		"/dns/v1/projects/my-project/managedZones/zone1/dnsKeys": marshalAttrs(t, dns.DnsKeysListResponse{
			DnsKeys: []*dns.DnsKey{{Id: "1", Type: "keySigning", Algorithm: "rsasha256"}},
		}),
		"/dns/v1/projects/my-project/policies": marshalAttrs(t, dns.PoliciesListResponse{
			Policies: []*dns.Policy{{Name: "pol1"}},
		}),
		"/dns/v1/projects/my-project/responsePolicies": marshalAttrs(t, dns.ResponsePoliciesListResponse{
			ResponsePolicies: []*dns.ResponsePolicy{{ResponsePolicyName: rpName}},
		}),
		"/dns/v1/projects/my-project/responsePolicies/rp1/rules": marshalAttrs(t, dns.ResponsePolicyRulesListResponse{
			ResponsePolicyRules: []*dns.ResponsePolicyRule{{RuleName: "rule1", DnsName: "internal.example.com."}},
		}),
	}
	srv := fakeGCPServer(t, routes)
	svc := fakeDNSService(t, srv)

	total, inserted, err := scanCloudDNSWithClient(t.Context(), svc, p, st, testScanID)
	if err != nil {
		t.Fatalf("scanCloudDNSWithClient: %v", err)
	}
	if total != 6 || inserted != 6 {
		t.Fatalf("counts: got total=%d inserted=%d, want 6/6 (zone+rrset+dnskey+policy+responsepolicy+rule)", total, inserted)
	}

	rrsetID := store.ResourceID("gcp", p.ID, zoneNativeID+"/rrsets/A/www.example.com.")
	dnsKeyID := store.ResourceID("gcp", p.ID, zoneNativeID+"/dnsKeys/1")
	ruleID := store.ResourceID("gcp", p.ID, rpNativeID+"/rules/rule1")

	assertParent := func(childID, wantParentID string) {
		t.Helper()
		rels, err := st.RelationshipsFrom(wantParentID, store.RelContains)
		if err != nil {
			t.Fatalf("RelationshipsFrom(%s): %v", wantParentID, err)
		}
		for _, r := range rels {
			if r.ToID == childID {
				return
			}
		}
		t.Errorf("%s not found as child of %s; got %+v", childID, wantParentID, rels)
	}
	assertParent(rrsetID, zoneID)
	assertParent(dnsKeyID, zoneID)
	assertParent(ruleID, rpID)
}

// TestScanCloudDNS_ZonePartialDenyContinues denies exactly the
// ResourceRecordSets.List call for a zone while DnsKeys.List for the same
// zone succeeds, guarding against a regression where the two-sequential-
// list-calls fallthrough in phase 2 aborts the DnsKeys call instead of
// falling through to it after a permission-denied record-sets response.
func TestScanCloudDNS_ZonePartialDenyContinues(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("my-project")

	zoneNativeID := "projects/my-project/managedZones/zone1"
	zoneID := store.ResourceID("gcp", p.ID, zoneNativeID)

	base := "/dns/v1/projects/my-project/managedZones/zone1"
	deniedBody := `{"error":{"code":403,"message":"caller is missing dns.resourceRecordSets.list","errors":[{"reason":"forbidden"}]}}`
	routes := map[string]string{
		"/dns/v1/projects/my-project/managedZones": marshalAttrs(t, dns.ManagedZonesListResponse{
			ManagedZones: []*dns.ManagedZone{{Name: "zone1"}},
		}),
		base + "/dnsKeys": marshalAttrs(t, dns.DnsKeysListResponse{
			DnsKeys: []*dns.DnsKey{{Id: "1", Type: "keySigning", Algorithm: "rsasha256"}},
		}),
		"/dns/v1/projects/my-project/policies":         marshalAttrs(t, dns.PoliciesListResponse{}),
		"/dns/v1/projects/my-project/responsePolicies": marshalAttrs(t, dns.ResponsePoliciesListResponse{}),
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == base+"/rrsets" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(deniedBody))
			return
		}
		body, ok := routes[r.URL.Path]
		if !ok {
			t.Errorf("unrouted GCP fake hit: %s %s", r.Method, r.URL.Path)
			http.Error(w, `{"error":{"code":404,"message":"no fake route"}}`, http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	svc := fakeDNSService(t, srv)

	total, inserted, err := scanCloudDNSWithClient(t.Context(), svc, p, st, testScanID)
	if err != nil {
		t.Fatalf("scanCloudDNSWithClient: %v", err)
	}
	// zone (1) + dnsKey (1); rrsets denied, policies/responsePolicies empty.
	if total != 2 || inserted != 2 {
		t.Fatalf("counts: got total=%d inserted=%d, want 2/2 (zone+dnskey — rrsets denied)", total, inserted)
	}

	dnsKeyID := store.ResourceID("gcp", p.ID, zoneNativeID+"/dnsKeys/1")
	rels, err := st.RelationshipsFrom(zoneID, store.RelContains)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	var found bool
	for _, r := range rels {
		if r.ToID == dnsKeyID {
			found = true
		}
	}
	if !found {
		t.Errorf("dnsKey %s not found as child of zone %s after rrsets denied; got %+v", dnsKeyID, zoneID, rels)
	}
}

func TestScanCloudDNS_PermissionDenied(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("my-project")

	body := `{"error":{"code":403,"message":"caller is missing dns permission","errors":[{"reason":"forbidden"}]}}`
	srv := fakeGCPServerStatus(t, http.StatusForbidden, body)
	svc := fakeDNSService(t, srv)

	total, inserted, err := scanCloudDNSWithClient(t.Context(), svc, p, st, testScanID)
	if err != nil {
		t.Fatalf("scanCloudDNSWithClient (denied): expected nil error, got %v", err)
	}
	if total != 0 || inserted != 0 {
		t.Fatalf("counts: got total=%d inserted=%d, want 0/0", total, inserted)
	}
}

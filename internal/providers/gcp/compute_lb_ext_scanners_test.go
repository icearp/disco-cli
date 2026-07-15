package gcp

import (
	"errors"
	"net/http"
	"testing"

	"codeberg.org/icearp/disco/store"
	"google.golang.org/api/compute/v1"
)

func TestScanComputeGlobalForwardingRules_Fake(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("my-project")

	selfLink := "https://www.googleapis.com/compute/v1/projects/my-project/global/forwardingRules/gfr1"
	page := compute.ForwardingRuleList{Items: []*compute.ForwardingRule{{Name: "gfr1", SelfLink: selfLink}}}
	srv := fakeGCPServer(t, map[string]string{
		"/projects/my-project/global/forwardingRules": marshalAttrs(t, page),
	})
	svc := fakeComputeService(t, srv)

	total, inserted, err := scanComputeGlobalForwardingRules(t.Context(), svc, p, st, testScanID)
	if err != nil {
		t.Fatalf("scanComputeGlobalForwardingRules: %v", err)
	}
	if total != 1 || inserted != 1 {
		t.Fatalf("counts: got total=%d inserted=%d, want 1/1", total, inserted)
	}
	if _, err := st.GetResource(store.ResourceID("gcp", p.ID, selfLink)); err != nil {
		t.Errorf("GetResource: %v", err)
	}
}

func TestScanComputeHealthChecks_Fake(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("my-project")

	globalLink := "https://www.googleapis.com/compute/v1/projects/my-project/global/healthChecks/hc-g"
	regionLink := "https://www.googleapis.com/compute/v1/projects/my-project/regions/us-central1/healthChecks/hc-r"
	page := compute.HealthChecksAggregatedList{
		Items: map[string]compute.HealthChecksScopedList{
			"global":              {HealthChecks: []*compute.HealthCheck{{Name: "hc-g", SelfLink: globalLink}}},
			"regions/us-central1": {HealthChecks: []*compute.HealthCheck{{Name: "hc-r", SelfLink: regionLink}}},
		},
	}
	srv := fakeGCPServer(t, map[string]string{
		"/projects/my-project/aggregated/healthChecks": marshalAttrs(t, page),
	})
	svc := fakeComputeService(t, srv)

	total, inserted, err := scanComputeHealthChecks(t.Context(), svc, p, st, testScanID)
	if err != nil {
		t.Fatalf("scanComputeHealthChecks: %v", err)
	}
	if total != 2 || inserted != 2 {
		t.Fatalf("counts: got total=%d inserted=%d, want 2/2", total, inserted)
	}
	g, err := st.GetResource(store.ResourceID("gcp", p.ID, globalLink))
	if err != nil {
		t.Fatalf("GetResource(global): %v", err)
	}
	if g.Region != nil {
		t.Errorf("global health check region: got %v, want nil", *g.Region)
	}
	r, err := st.GetResource(store.ResourceID("gcp", p.ID, regionLink))
	if err != nil {
		t.Fatalf("GetResource(region): %v", err)
	}
	if r.Region == nil || *r.Region != "us-central1" {
		t.Errorf("region health check region: got %v, want us-central1", r.Region)
	}
}

func TestScanComputeHealthChecks_PermissionDenied(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("my-project")

	body := `{"error":{"code":403,"message":"caller is missing compute.healthChecks.list","errors":[{"reason":"forbidden"}]}}`
	srv := fakeGCPServerStatus(t, http.StatusForbidden, body)
	svc := fakeComputeService(t, srv)

	total, inserted, err := scanComputeHealthChecks(t.Context(), svc, p, st, testScanID)
	if err != nil {
		t.Fatalf("scanComputeHealthChecks (denied): expected nil error, got %v", err)
	}
	if total != 0 || inserted != 0 {
		t.Fatalf("counts: got total=%d inserted=%d, want 0/0", total, inserted)
	}
}

func TestScanComputeHealthChecks_APINotEnabled(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("my-project")

	body := `{"error":{"code":403,"message":"Compute Engine API has not been used in project my-project before or it is disabled.","errors":[{"reason":"accessNotConfigured"}]}}`
	srv := fakeGCPServerStatus(t, http.StatusForbidden, body)
	svc := fakeComputeService(t, srv)

	_, _, err := scanComputeHealthChecks(t.Context(), svc, p, st, testScanID)
	if !errors.Is(err, errServiceDisabled) {
		t.Fatalf("scanComputeHealthChecks: expected errServiceDisabled sentinel, got %v", err)
	}
}

func TestScanComputeRegionCompositeHealthChecks_Fake(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("my-project")

	selfLink := "https://www.googleapis.com/compute/v1/projects/my-project/regions/us-central1/compositeHealthChecks/chc1"
	page := compute.CompositeHealthCheckAggregatedList{
		Items: map[string]compute.CompositeHealthChecksScopedList{
			"regions/us-central1": {CompositeHealthChecks: []*compute.CompositeHealthCheck{{Name: "chc1", SelfLink: selfLink}}},
		},
	}
	srv := fakeGCPServer(t, map[string]string{
		"/projects/my-project/aggregated/compositeHealthChecks": marshalAttrs(t, page),
	})
	svc := fakeComputeService(t, srv)

	total, inserted, err := scanComputeRegionCompositeHealthChecks(t.Context(), svc, p, st, testScanID)
	if err != nil {
		t.Fatalf("scanComputeRegionCompositeHealthChecks: %v", err)
	}
	if total != 1 || inserted != 1 {
		t.Fatalf("counts: got total=%d inserted=%d, want 1/1", total, inserted)
	}
	got, err := st.GetResource(store.ResourceID("gcp", p.ID, selfLink))
	if err != nil {
		t.Fatalf("GetResource: %v", err)
	}
	if got.Region == nil || *got.Region != "us-central1" {
		t.Errorf("region composite health check region: got %v, want us-central1", got.Region)
	}
}

func TestScanComputeRegionCompositeHealthChecks_SkipsGlobalScope(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("my-project")

	page := compute.CompositeHealthCheckAggregatedList{
		Items: map[string]compute.CompositeHealthChecksScopedList{
			"global": {CompositeHealthChecks: []*compute.CompositeHealthCheck{{Name: "should-be-skipped", SelfLink: "x"}}},
		},
	}
	srv := fakeGCPServer(t, map[string]string{
		"/projects/my-project/aggregated/compositeHealthChecks": marshalAttrs(t, page),
	})
	svc := fakeComputeService(t, srv)

	total, inserted, err := scanComputeRegionCompositeHealthChecks(t.Context(), svc, p, st, testScanID)
	if err != nil {
		t.Fatalf("scanComputeRegionCompositeHealthChecks: %v", err)
	}
	if total != 0 || inserted != 0 {
		t.Fatalf("counts: got total=%d inserted=%d, want 0/0 (global-scope rows must be skipped)", total, inserted)
	}
}

func TestScanComputeRegionHealthAggregationPolicies_Fake(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("my-project")

	selfLink := "https://www.googleapis.com/compute/v1/projects/my-project/regions/us-central1/healthAggregationPolicies/hap1"
	page := compute.HealthAggregationPolicyAggregatedList{
		Items: map[string]compute.HealthAggregationPoliciesScopedList{
			"regions/us-central1": {HealthAggregationPolicies: []*compute.HealthAggregationPolicy{{Name: "hap1", SelfLink: selfLink}}},
		},
	}
	srv := fakeGCPServer(t, map[string]string{
		"/projects/my-project/aggregated/healthAggregationPolicies": marshalAttrs(t, page),
	})
	svc := fakeComputeService(t, srv)

	total, inserted, err := scanComputeRegionHealthAggregationPolicies(t.Context(), svc, p, st, testScanID)
	if err != nil {
		t.Fatalf("scanComputeRegionHealthAggregationPolicies: %v", err)
	}
	if total != 1 || inserted != 1 {
		t.Fatalf("counts: got total=%d inserted=%d, want 1/1", total, inserted)
	}
	got, err := st.GetResource(store.ResourceID("gcp", p.ID, selfLink))
	if err != nil {
		t.Fatalf("GetResource: %v", err)
	}
	if got.Region == nil || *got.Region != "us-central1" {
		t.Errorf("region health aggregation policy region: got %v, want us-central1", got.Region)
	}
}

func TestScanComputeRegionHealthCheckServices_Fake(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("my-project")

	selfLink := "https://www.googleapis.com/compute/v1/projects/my-project/regions/us-central1/healthCheckServices/hcs1"
	page := compute.HealthCheckServiceAggregatedList{
		Items: map[string]compute.HealthCheckServicesScopedList{
			"regions/us-central1": {Resources: []*compute.HealthCheckService{{Name: "hcs1", SelfLink: selfLink}}},
		},
	}
	srv := fakeGCPServer(t, map[string]string{
		"/projects/my-project/aggregated/healthCheckServices": marshalAttrs(t, page),
	})
	svc := fakeComputeService(t, srv)

	total, inserted, err := scanComputeRegionHealthCheckServices(t.Context(), svc, p, st, testScanID)
	if err != nil {
		t.Fatalf("scanComputeRegionHealthCheckServices: %v", err)
	}
	if total != 1 || inserted != 1 {
		t.Fatalf("counts: got total=%d inserted=%d, want 1/1", total, inserted)
	}
	got, err := st.GetResource(store.ResourceID("gcp", p.ID, selfLink))
	if err != nil {
		t.Fatalf("GetResource: %v", err)
	}
	if got.Region == nil || *got.Region != "us-central1" {
		t.Errorf("region health check service region: got %v, want us-central1", got.Region)
	}
}

func TestScanComputeRegionHealthSources_Fake(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("my-project")

	selfLink := "https://www.googleapis.com/compute/v1/projects/my-project/regions/us-central1/healthSources/hs1"
	page := compute.HealthSourceAggregatedList{
		Items: map[string]compute.HealthSourcesScopedList{
			"regions/us-central1": {HealthSources: []*compute.HealthSource{{Name: "hs1", SelfLink: selfLink}}},
		},
	}
	srv := fakeGCPServer(t, map[string]string{
		"/projects/my-project/aggregated/healthSources": marshalAttrs(t, page),
	})
	svc := fakeComputeService(t, srv)

	total, inserted, err := scanComputeRegionHealthSources(t.Context(), svc, p, st, testScanID)
	if err != nil {
		t.Fatalf("scanComputeRegionHealthSources: %v", err)
	}
	if total != 1 || inserted != 1 {
		t.Fatalf("counts: got total=%d inserted=%d, want 1/1", total, inserted)
	}
	if _, err := st.GetResource(store.ResourceID("gcp", p.ID, selfLink)); err != nil {
		t.Errorf("GetResource: %v", err)
	}
}

func TestScanComputeRegionNotificationEndpoints_Fake(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("my-project")

	selfLink := "https://www.googleapis.com/compute/v1/projects/my-project/regions/us-central1/notificationEndpoints/ne1"
	page := compute.NotificationEndpointAggregatedList{
		Items: map[string]compute.NotificationEndpointsScopedList{
			"regions/us-central1": {Resources: []*compute.NotificationEndpoint{{Name: "ne1", SelfLink: selfLink}}},
		},
	}
	srv := fakeGCPServer(t, map[string]string{
		"/projects/my-project/aggregated/notificationEndpoints": marshalAttrs(t, page),
	})
	svc := fakeComputeService(t, srv)

	total, inserted, err := scanComputeRegionNotificationEndpoints(t.Context(), svc, p, st, testScanID)
	if err != nil {
		t.Fatalf("scanComputeRegionNotificationEndpoints: %v", err)
	}
	if total != 1 || inserted != 1 {
		t.Fatalf("counts: got total=%d inserted=%d, want 1/1", total, inserted)
	}
	got, err := st.GetResource(store.ResourceID("gcp", p.ID, selfLink))
	if err != nil {
		t.Fatalf("GetResource: %v", err)
	}
	if got.Region == nil || *got.Region != "us-central1" {
		t.Errorf("region notification endpoint region: got %v, want us-central1", got.Region)
	}
}

func TestScanComputeHttpHealthChecks_Fake(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("my-project")

	selfLink := "https://www.googleapis.com/compute/v1/projects/my-project/global/httpHealthChecks/hhc1"
	page := compute.HttpHealthCheckList{Items: []*compute.HttpHealthCheck{{Name: "hhc1", SelfLink: selfLink}}}
	srv := fakeGCPServer(t, map[string]string{
		"/projects/my-project/global/httpHealthChecks": marshalAttrs(t, page),
	})
	svc := fakeComputeService(t, srv)

	total, inserted, err := scanComputeHttpHealthChecks(t.Context(), svc, p, st, testScanID)
	if err != nil {
		t.Fatalf("scanComputeHttpHealthChecks: %v", err)
	}
	if total != 1 || inserted != 1 {
		t.Fatalf("counts: got total=%d inserted=%d, want 1/1", total, inserted)
	}
}

func TestScanComputeHttpsHealthChecks_Fake(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("my-project")

	selfLink := "https://www.googleapis.com/compute/v1/projects/my-project/global/httpsHealthChecks/hshc1"
	page := compute.HttpsHealthCheckList{Items: []*compute.HttpsHealthCheck{{Name: "hshc1", SelfLink: selfLink}}}
	srv := fakeGCPServer(t, map[string]string{
		"/projects/my-project/global/httpsHealthChecks": marshalAttrs(t, page),
	})
	svc := fakeComputeService(t, srv)

	total, inserted, err := scanComputeHttpsHealthChecks(t.Context(), svc, p, st, testScanID)
	if err != nil {
		t.Fatalf("scanComputeHttpsHealthChecks: %v", err)
	}
	if total != 1 || inserted != 1 {
		t.Fatalf("counts: got total=%d inserted=%d, want 1/1", total, inserted)
	}
}

func TestScanComputeSslCertificates_Fake(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("my-project")

	globalLink := "https://www.googleapis.com/compute/v1/projects/my-project/global/sslCertificates/cert-g"
	regionLink := "https://www.googleapis.com/compute/v1/projects/my-project/regions/us-central1/sslCertificates/cert-r"
	page := compute.SslCertificateAggregatedList{
		Items: map[string]compute.SslCertificatesScopedList{
			"global":              {SslCertificates: []*compute.SslCertificate{{Name: "cert-g", SelfLink: globalLink}}},
			"regions/us-central1": {SslCertificates: []*compute.SslCertificate{{Name: "cert-r", SelfLink: regionLink}}},
		},
	}
	srv := fakeGCPServer(t, map[string]string{
		"/projects/my-project/aggregated/sslCertificates": marshalAttrs(t, page),
	})
	svc := fakeComputeService(t, srv)

	total, inserted, err := scanComputeSslCertificates(t.Context(), svc, p, st, testScanID)
	if err != nil {
		t.Fatalf("scanComputeSslCertificates: %v", err)
	}
	if total != 2 || inserted != 2 {
		t.Fatalf("counts: got total=%d inserted=%d, want 2/2", total, inserted)
	}
	g, err := st.GetResource(store.ResourceID("gcp", p.ID, globalLink))
	if err != nil {
		t.Fatalf("GetResource(global): %v", err)
	}
	if g.Region != nil {
		t.Errorf("global ssl certificate region: got %v, want nil", *g.Region)
	}
	r, err := st.GetResource(store.ResourceID("gcp", p.ID, regionLink))
	if err != nil {
		t.Fatalf("GetResource(region): %v", err)
	}
	if r.Region == nil || *r.Region != "us-central1" {
		t.Errorf("region ssl certificate region: got %v, want us-central1", r.Region)
	}
}

func TestScanComputeSslPolicies_Fake(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("my-project")

	globalLink := "https://www.googleapis.com/compute/v1/projects/my-project/global/sslPolicies/pol-g"
	regionLink := "https://www.googleapis.com/compute/v1/projects/my-project/regions/us-central1/sslPolicies/pol-r"
	page := compute.SslPoliciesAggregatedList{
		Items: map[string]compute.SslPoliciesScopedList{
			"global":              {SslPolicies: []*compute.SslPolicy{{Name: "pol-g", SelfLink: globalLink}}},
			"regions/us-central1": {SslPolicies: []*compute.SslPolicy{{Name: "pol-r", SelfLink: regionLink}}},
		},
	}
	srv := fakeGCPServer(t, map[string]string{
		"/projects/my-project/aggregated/sslPolicies": marshalAttrs(t, page),
	})
	svc := fakeComputeService(t, srv)

	total, inserted, err := scanComputeSslPolicies(t.Context(), svc, p, st, testScanID)
	if err != nil {
		t.Fatalf("scanComputeSslPolicies: %v", err)
	}
	if total != 2 || inserted != 2 {
		t.Fatalf("counts: got total=%d inserted=%d, want 2/2", total, inserted)
	}
	g, err := st.GetResource(store.ResourceID("gcp", p.ID, globalLink))
	if err != nil {
		t.Fatalf("GetResource(global): %v", err)
	}
	if g.Region != nil {
		t.Errorf("global ssl policy region: got %v, want nil", *g.Region)
	}
	r, err := st.GetResource(store.ResourceID("gcp", p.ID, regionLink))
	if err != nil {
		t.Fatalf("GetResource(region): %v", err)
	}
	if r.Region == nil || *r.Region != "us-central1" {
		t.Errorf("region ssl policy region: got %v, want us-central1", r.Region)
	}
}

func TestScanComputeTargetSslProxies_Fake(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("my-project")

	selfLink := "https://www.googleapis.com/compute/v1/projects/my-project/global/targetSslProxies/tsp1"
	page := compute.TargetSslProxyList{Items: []*compute.TargetSslProxy{{Name: "tsp1", SelfLink: selfLink}}}
	srv := fakeGCPServer(t, map[string]string{
		"/projects/my-project/global/targetSslProxies": marshalAttrs(t, page),
	})
	svc := fakeComputeService(t, srv)

	total, inserted, err := scanComputeTargetSslProxies(t.Context(), svc, p, st, testScanID)
	if err != nil {
		t.Fatalf("scanComputeTargetSslProxies: %v", err)
	}
	if total != 1 || inserted != 1 {
		t.Fatalf("counts: got total=%d inserted=%d, want 1/1", total, inserted)
	}
}

func TestScanComputeTargetTcpProxies_Fake(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("my-project")

	globalLink := "https://www.googleapis.com/compute/v1/projects/my-project/global/targetTcpProxies/tp-g"
	regionLink := "https://www.googleapis.com/compute/v1/projects/my-project/regions/us-central1/targetTcpProxies/tp-r"
	page := compute.TargetTcpProxyAggregatedList{
		Items: map[string]compute.TargetTcpProxiesScopedList{
			"global":              {TargetTcpProxies: []*compute.TargetTcpProxy{{Name: "tp-g", SelfLink: globalLink}}},
			"regions/us-central1": {TargetTcpProxies: []*compute.TargetTcpProxy{{Name: "tp-r", SelfLink: regionLink}}},
		},
	}
	srv := fakeGCPServer(t, map[string]string{
		"/projects/my-project/aggregated/targetTcpProxies": marshalAttrs(t, page),
	})
	svc := fakeComputeService(t, srv)

	total, inserted, err := scanComputeTargetTcpProxies(t.Context(), svc, p, st, testScanID)
	if err != nil {
		t.Fatalf("scanComputeTargetTcpProxies: %v", err)
	}
	if total != 2 || inserted != 2 {
		t.Fatalf("counts: got total=%d inserted=%d, want 2/2", total, inserted)
	}
	g, err := st.GetResource(store.ResourceID("gcp", p.ID, globalLink))
	if err != nil {
		t.Fatalf("GetResource(global): %v", err)
	}
	if g.Region != nil {
		t.Errorf("global target tcp proxy region: got %v, want nil", *g.Region)
	}
	r, err := st.GetResource(store.ResourceID("gcp", p.ID, regionLink))
	if err != nil {
		t.Fatalf("GetResource(region): %v", err)
	}
	if r.Region == nil || *r.Region != "us-central1" {
		t.Errorf("region target tcp proxy region: got %v, want us-central1", r.Region)
	}
}

func TestScanComputeTargetGrpcProxies_Fake(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("my-project")

	selfLink := "https://www.googleapis.com/compute/v1/projects/my-project/global/targetGrpcProxies/tgp1"
	page := compute.TargetGrpcProxyList{Items: []*compute.TargetGrpcProxy{{Name: "tgp1", SelfLink: selfLink}}}
	srv := fakeGCPServer(t, map[string]string{
		"/projects/my-project/global/targetGrpcProxies": marshalAttrs(t, page),
	})
	svc := fakeComputeService(t, srv)

	total, inserted, err := scanComputeTargetGrpcProxies(t.Context(), svc, p, st, testScanID)
	if err != nil {
		t.Fatalf("scanComputeTargetGrpcProxies: %v", err)
	}
	if total != 1 || inserted != 1 {
		t.Fatalf("counts: got total=%d inserted=%d, want 1/1", total, inserted)
	}
}

func TestScanComputeTargetInstances_Fake(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("my-project")

	selfLink := "https://www.googleapis.com/compute/v1/projects/my-project/zones/us-central1-a/targetInstances/ti1"
	page := compute.TargetInstanceAggregatedList{
		Items: map[string]compute.TargetInstancesScopedList{
			"zones/us-central1-a": {
				TargetInstances: []*compute.TargetInstance{{
					Name:     "ti1",
					SelfLink: selfLink,
					Zone:     "https://www.googleapis.com/compute/v1/projects/my-project/zones/us-central1-a",
				}},
			},
		},
	}
	srv := fakeGCPServer(t, map[string]string{
		"/projects/my-project/aggregated/targetInstances": marshalAttrs(t, page),
	})
	svc := fakeComputeService(t, srv)

	total, inserted, err := scanComputeTargetInstances(t.Context(), svc, p, st, testScanID)
	if err != nil {
		t.Fatalf("scanComputeTargetInstances: %v", err)
	}
	if total != 1 || inserted != 1 {
		t.Fatalf("counts: got total=%d inserted=%d, want 1/1", total, inserted)
	}
	got, err := st.GetResource(store.ResourceID("gcp", p.ID, selfLink))
	if err != nil {
		t.Fatalf("GetResource: %v", err)
	}
	if got.Zone == nil || *got.Zone != "us-central1-a" {
		t.Errorf("target instance zone: got %v, want us-central1-a", got.Zone)
	}
	if got.Region == nil || *got.Region != "us-central1" {
		t.Errorf("target instance region: got %v, want us-central1", got.Region)
	}
}

func TestScanComputeTargetPools_Fake(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("my-project")

	selfLink := "https://www.googleapis.com/compute/v1/projects/my-project/regions/us-central1/targetPools/tp1"
	page := compute.TargetPoolAggregatedList{
		Items: map[string]compute.TargetPoolsScopedList{
			"regions/us-central1": {TargetPools: []*compute.TargetPool{{Name: "tp1", SelfLink: selfLink}}},
		},
	}
	srv := fakeGCPServer(t, map[string]string{
		"/projects/my-project/aggregated/targetPools": marshalAttrs(t, page),
	})
	svc := fakeComputeService(t, srv)

	total, inserted, err := scanComputeTargetPools(t.Context(), svc, p, st, testScanID)
	if err != nil {
		t.Fatalf("scanComputeTargetPools: %v", err)
	}
	if total != 1 || inserted != 1 {
		t.Fatalf("counts: got total=%d inserted=%d, want 1/1", total, inserted)
	}
	got, err := st.GetResource(store.ResourceID("gcp", p.ID, selfLink))
	if err != nil {
		t.Fatalf("GetResource: %v", err)
	}
	if got.Region == nil || *got.Region != "us-central1" {
		t.Errorf("target pool region: got %v, want us-central1", got.Region)
	}
}

func TestScanComputeTargetPools_SkipsEmptyScope(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("my-project")

	page := compute.TargetPoolAggregatedList{
		Items: map[string]compute.TargetPoolsScopedList{
			"regions/us-central1": {},
		},
	}
	srv := fakeGCPServer(t, map[string]string{
		"/projects/my-project/aggregated/targetPools": marshalAttrs(t, page),
	})
	svc := fakeComputeService(t, srv)

	total, inserted, err := scanComputeTargetPools(t.Context(), svc, p, st, testScanID)
	if err != nil {
		t.Fatalf("scanComputeTargetPools: %v", err)
	}
	if total != 0 || inserted != 0 {
		t.Fatalf("counts: got total=%d inserted=%d, want 0/0", total, inserted)
	}
}

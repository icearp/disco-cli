package gcp

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	cloudquotas "cloud.google.com/go/cloudquotas/apiv1"
	"cloud.google.com/go/cloudquotas/apiv1/cloudquotaspb"
	"github.com/icearp/disco-cli/store"
	"google.golang.org/api/option"
	serviceusage "google.golang.org/api/serviceusage/v1"
	"google.golang.org/protobuf/encoding/protojson"
)

// fakeServiceUsageService builds a *serviceusage.Service pointed at the fake
// server. Route templates embed the full "v1/" prefix.
func fakeServiceUsageService(t *testing.T, srv *httptest.Server) *serviceusage.Service {
	t.Helper()
	svc, err := serviceusage.NewService(
		t.Context(),
		option.WithEndpoint(srv.URL),
		option.WithHTTPClient(srv.Client()),
		option.WithoutAuthentication(),
	)
	if err != nil {
		t.Fatalf("serviceusage.NewService: %v", err)
	}
	return svc
}

// fakeCloudQuotasClient builds a REST *cloudquotas.Client pointed at the fake
// server. The GAPIC REST transport takes the same option.WithEndpoint path as
// the Discovery clients because its default endpoint carries no path prefix.
func fakeCloudQuotasClient(t *testing.T, srv *httptest.Server) *cloudquotas.Client {
	t.Helper()
	c, err := cloudquotas.NewRESTClient(
		t.Context(),
		option.WithEndpoint(srv.URL),
		option.WithHTTPClient(srv.Client()),
		option.WithoutAuthentication(),
	)
	if err != nil {
		t.Fatalf("cloudquotas.NewRESTClient: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}

// enabledServicesBody is the Service Usage list response naming services as
// Config.Name, the field the scanner reads.
func enabledServicesBody(t *testing.T, names ...string) string {
	t.Helper()
	resp := serviceusage.ListServicesResponse{}
	for _, n := range names {
		resp.Services = append(resp.Services, &serviceusage.GoogleApiServiceusageV1Service{
			Name:   "projects/123/services/" + n,
			State:  "ENABLED",
			Config: &serviceusage.GoogleApiServiceusageV1ServiceConfig{Name: n},
		})
	}
	return marshalAttrs(t, resp)
}

// quotaInfosBody encodes a ListQuotaInfos page with protojson, the codec the
// REST client decodes with — so a field-name drift across an SDK bump surfaces
// here rather than as a silently empty scan.
func quotaInfosBody(t *testing.T, infos ...*cloudquotaspb.QuotaInfo) string {
	t.Helper()
	b, err := protojson.Marshal(&cloudquotaspb.ListQuotaInfosResponse{QuotaInfos: infos})
	if err != nil {
		t.Fatalf("protojson.Marshal: %v", err)
	}
	return string(b)
}

func quotaInfosPath(project, service string) string {
	return "/v1/projects/" + project + "/locations/global/services/" + service + "/quotaInfos"
}

// cpusQuotaInfo is one rate-free allocation quota carrying three dimension
// sets: two regional (one of them zone-scoped) and one undimensioned.
func cpusQuotaInfo() *cloudquotaspb.QuotaInfo {
	return &cloudquotaspb.QuotaInfo{
		Name:              "projects/123/locations/global/services/compute.googleapis.com/quotaInfos/CpusPerProjectPerRegion",
		QuotaId:           "CpusPerProjectPerRegion",
		Metric:            "compute.googleapis.com/cpus",
		Service:           "compute.googleapis.com",
		MetricDisplayName: "CPUs",
		QuotaDisplayName:  "CPUs per project per region",
		MetricUnit:        "1/{project}/{region}",
		ContainerType:     cloudquotaspb.QuotaInfo_PROJECT,
		Dimensions:        []string{"region"},
		DimensionsInfos: []*cloudquotaspb.DimensionsInfo{
			{
				Dimensions:          map[string]string{"region": "us-central1"},
				Details:             &cloudquotaspb.QuotaDetails{Value: 24},
				ApplicableLocations: []string{"us-central1"},
			},
			{
				Dimensions: map[string]string{"region": "us-east1", "zone": "us-east1-b"},
				Details:    &cloudquotaspb.QuotaDetails{Value: 8},
			},
			{
				Details: &cloudquotaspb.QuotaDetails{Value: 4},
			},
		},
	}
}

func TestScanCloudQuotas_RowPerDimensionsInfo(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("proj1")

	srv := fakeGCPServer(t, map[string]string{
		"/v1/projects/proj1/services":                     enabledServicesBody(t, "compute.googleapis.com"),
		quotaInfosPath("proj1", "compute.googleapis.com"): quotaInfosBody(t, cpusQuotaInfo()),
	})

	total, inserted, err := scanCloudQuotasWithClients(t.Context(), p, st, testScanID,
		fakeServiceUsageService(t, srv), fakeCloudQuotasClient(t, srv))
	if err != nil {
		t.Fatalf("scanCloudQuotasWithClients: %v", err)
	}
	// One QuotaInfo, three DimensionsInfos: the value lives per dimension set,
	// so a single quota code produces three rows.
	if total != 3 || inserted != 3 {
		t.Fatalf("counts: got total=%d inserted=%d, want 3/3", total, inserted)
	}

	quotas, err := st.ListQuotas(store.QuotaFilter{Providers: []string{"gcp"}})
	if err != nil {
		t.Fatalf("ListQuotas: %v", err)
	}
	if len(quotas) != 3 {
		t.Fatalf("ListQuotas: got %d rows, want 3", len(quotas))
	}

	byRegion := map[string]store.Quota{}
	roots := map[string]bool{}
	for _, q := range quotas {
		byRegion[q.Region] = q
		roots[q.ID] = true
	}
	if len(roots) != 3 {
		t.Errorf("root ids: got %d distinct, want 3 (dimension_key must be part of the key)", len(roots))
	}

	central, ok := byRegion["us-central1"]
	if !ok {
		t.Fatalf("no us-central1 row; got regions %v", byRegion)
	}
	if central.DimensionKey != "" {
		t.Errorf("us-central1 dimension_key = %q, want empty (region is its own column)", central.DimensionKey)
	}
	if central.Value == nil || *central.Value != 24 {
		t.Errorf("us-central1 value = %v, want 24", central.Value)
	}
	if central.AvailabilityZone != nil {
		t.Errorf("us-central1 availability_zone = %v, want nil", *central.AvailabilityZone)
	}

	east, ok := byRegion["us-east1"]
	if !ok {
		t.Fatalf("no us-east1 row; got regions %v", byRegion)
	}
	if east.DimensionKey != "zone=us-east1-b" {
		t.Errorf("us-east1 dimension_key = %q, want %q", east.DimensionKey, "zone=us-east1-b")
	}
	if east.AvailabilityZone == nil || *east.AvailabilityZone != "us-east1-b" {
		t.Errorf("us-east1 availability_zone = %v, want us-east1-b", east.AvailabilityZone)
	}

	// An undimensioned dimension set is the 'global' region sentinel, not a
	// missing region.
	global, ok := byRegion["global"]
	if !ok {
		t.Fatalf("no global row; got regions %v", byRegion)
	}
	if global.DimensionKey != "" {
		t.Errorf("global dimension_key = %q, want empty", global.DimensionKey)
	}

	for _, q := range quotas {
		if q.ServiceCode != "compute.googleapis.com" {
			t.Errorf("service_code = %q, want compute.googleapis.com", q.ServiceCode)
		}
		if q.QuotaCode != "CpusPerProjectPerRegion" {
			t.Errorf("quota_code = %q", q.QuotaCode)
		}
		if q.Name != "CPUs per project per region" {
			t.Errorf("name = %q, want the display name", q.Name)
		}
		if q.SubAccountType == nil || *q.SubAccountType != "Project" {
			t.Errorf("sub_account_type = %v, want Project", q.SubAccountType)
		}
		// Cloud Quotas reports no default, so the column is uniformly NULL and
		// can never split a chain.
		if q.DefaultValue != nil {
			t.Errorf("default_value = %v, want nil", *q.DefaultValue)
		}
		// IsFixed false => adjustable.
		if !q.Adjustable {
			t.Errorf("adjustable = false, want true for a non-fixed quota")
		}
		// Allocation quota: no refresh interval, so no rate window.
		if q.PeriodUnit != nil || q.PeriodValue != nil {
			t.Errorf("period = (%v,%v), want both nil", q.PeriodUnit, q.PeriodValue)
		}
		if q.ResourceType != nil {
			t.Errorf("resource_type = %v, want nil (GCP quotas are metric-scoped)", *q.ResourceType)
		}
	}
}

// TestQuotaDimensionKey_StableAcrossEncodings pins the property a single-run
// assertion cannot see: Dimensions is a Go map, so an insertion-order encoding
// passes any one comparison against a literal and still re-keys every row on
// the next scan. Two independently built maps must encode byte-equal.
func TestQuotaDimensionKey_StableAcrossEncodings(t *testing.T) {
	a := map[string]string{"zone": "us-east1-b", "provider": "Example Org", "region": "us-east1"}
	b := map[string]string{"region": "us-east1", "provider": "Example Org", "zone": "us-east1-b"}

	first, second := quotaDimensionKey(a), quotaDimensionKey(b)
	if first != second {
		t.Fatalf("encodings differ: %q vs %q", first, second)
	}
	// Repeat over the same map: Go randomizes range order per iteration, so a
	// sort-free encoding fails here even when both maps were built the same
	// way.
	for range 32 {
		if got := quotaDimensionKey(a); got != first {
			t.Fatalf("unstable across calls: %q then %q", first, got)
		}
	}
	if first != "provider=Example Org,zone=us-east1-b" {
		t.Errorf("dimension key = %q, want sorted pairs without region", first)
	}
}

// TestQuotaDimensionKey_EscapesSeparators guards the collision store.QuotaID
// cannot see: it joins components with "|", and this encoding joins with ","
// and "=", so an unescaped separator in a provider value would let two
// distinct quotas hash to one root.
func TestQuotaDimensionKey_EscapesSeparators(t *testing.T) {
	if got, want := quotaDimensionKey(map[string]string{"a": "x", "b|c": "y"}), "a=x,b%7Cc=y"; got != want {
		t.Errorf("pipe not escaped: got %q, want %q", got, want)
	}
	collide1 := quotaDimensionKey(map[string]string{"a": "x=1"})
	collide2 := quotaDimensionKey(map[string]string{"a=x": "1"})
	if collide1 == collide2 {
		t.Errorf("distinct dimension maps encode identically: %q", collide1)
	}
	if got := quotaDimensionKey(map[string]string{"a": "50%"}); got != "a=50%25" {
		t.Errorf("percent not escaped: got %q", got)
	}
}

func TestParseGCPRefreshInterval(t *testing.T) {
	// The PG CHECK on period_unit accepts exactly these; SQLite carries no such
	// constraint, so this set is the only guard disco's own tests provide.
	allowed := map[string]bool{
		"microsecond": true, "millisecond": true, "second": true,
		"minute": true, "hour": true, "day": true, "week": true,
	}

	cases := []struct {
		in    string
		unit  string
		value int
		ok    bool
	}{
		{"minute", "minute", 1, true},
		{"day", "day", 1, true},
		{"10 seconds", "second", 10, true},
		{"1 hour", "hour", 1, true},
		{"", "", 0, false},          // allocation quota: no rate window
		{"month", "", 0, false},     // outside the seven the column accepts
		{"0 seconds", "", 0, false}, // a zero-length window is not a window
		{"-5 minutes", "", 0, false},
		{"1 day 12 hours", "", 0, false}, // compound: no single unit to store
		{"every minute", "", 0, false},
		{"seconds 10", "", 0, false},
	}
	for _, c := range cases {
		unit, value, ok := parseGCPRefreshInterval(c.in)
		if ok != c.ok || unit != c.unit || value != c.value {
			t.Errorf("parseGCPRefreshInterval(%q) = (%q,%d,%v), want (%q,%d,%v)",
				c.in, unit, value, ok, c.unit, c.value, c.ok)
		}
		// The discriminating assertion: anything accepted must be storable.
		if ok && !allowed[unit] {
			t.Errorf("parseGCPRefreshInterval(%q) accepted %q, which the period_unit CHECK rejects", c.in, unit)
		}
	}
}

// TestScanCloudQuotas_RescanDoesNotVersion is the property C1 bought: the
// change comparison reads typed columns, not the attributes blob, so a moving
// field inside attributes must refresh in place rather than split the chain.
func TestScanCloudQuotas_RescanDoesNotVersion(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("proj1")

	run := func(mutate func(*cloudquotaspb.QuotaInfo)) (int, int) {
		t.Helper()
		qi := cpusQuotaInfo()
		mutate(qi)
		srv := fakeGCPServer(t, map[string]string{
			"/v1/projects/proj1/services":                     enabledServicesBody(t, "compute.googleapis.com"),
			quotaInfosPath("proj1", "compute.googleapis.com"): quotaInfosBody(t, qi),
		})
		total, inserted, err := scanCloudQuotasWithClients(t.Context(), p, st, testScanID,
			fakeServiceUsageService(t, srv), fakeCloudQuotasClient(t, srv))
		if err != nil {
			t.Fatalf("scanCloudQuotasWithClients: %v", err)
		}
		return total, inserted
	}

	if _, inserted := run(func(*cloudquotaspb.QuotaInfo) {}); inserted != 3 {
		t.Fatalf("first scan inserted %d, want 3", inserted)
	}
	// Second scan reports the same limits with a rollout flag flipped — a
	// gauge-shaped field that lives only in attributes.
	total, inserted := run(func(qi *cloudquotaspb.QuotaInfo) {
		qi.DimensionsInfos[0].Details.RolloutInfo = &cloudquotaspb.RolloutInfo{OngoingRollout: true}
	})
	if total != 3 {
		t.Fatalf("second scan total = %d, want 3", total)
	}
	if inserted != 0 {
		t.Errorf("second scan inserted %d new versions, want 0", inserted)
	}

	quotas, err := st.ListQuotas(store.QuotaFilter{Providers: []string{"gcp"}})
	if err != nil {
		t.Fatalf("ListQuotas: %v", err)
	}
	if len(quotas) != 3 {
		t.Errorf("current rows = %d, want 3 (a re-key would strand predecessors)", len(quotas))
	}
}

// TestScanCloudQuotas_ChangedLimitVersions is the other half: a real limit
// change must split the chain, so the rescan test above cannot pass by the
// scanner writing nothing.
func TestScanCloudQuotas_ChangedLimitVersions(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("proj1")

	run := func(value int64) int {
		t.Helper()
		qi := cpusQuotaInfo()
		qi.DimensionsInfos[0].Details.Value = value
		srv := fakeGCPServer(t, map[string]string{
			"/v1/projects/proj1/services":                     enabledServicesBody(t, "compute.googleapis.com"),
			quotaInfosPath("proj1", "compute.googleapis.com"): quotaInfosBody(t, qi),
		})
		_, inserted, err := scanCloudQuotasWithClients(t.Context(), p, st, testScanID,
			fakeServiceUsageService(t, srv), fakeCloudQuotasClient(t, srv))
		if err != nil {
			t.Fatalf("scanCloudQuotasWithClients: %v", err)
		}
		return inserted
	}

	run(24)
	if inserted := run(48); inserted != 1 {
		t.Errorf("raised limit inserted %d versions, want 1", inserted)
	}
}

// TestScanCloudQuotas_PerServiceDenialAggregates pins two behaviours at once:
// one unreadable service does not cost the project the others, and the
// failures collapse to a single warning rather than one per service.
func TestScanCloudQuotas_PerServiceDenialAggregates(t *testing.T) {
	st := newTestStore(t)
	var warnings []store.ScanWarning
	st.OnWarn = func(w store.ScanWarning) { warnings = append(warnings, w) }
	p := newTestProject("proj1")

	deniedBody := `{"error":{"code":403,"message":"caller is missing cloudquotas.quotas.get","errors":[{"reason":"forbidden"}]}}`
	routes := map[string]string{
		"/v1/projects/proj1/services":                     enabledServicesBody(t, "compute.googleapis.com", "iam.googleapis.com", "run.googleapis.com"),
		quotaInfosPath("proj1", "compute.googleapis.com"): quotaInfosBody(t, cpusQuotaInfo()),
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if body, ok := routes[r.URL.Path]; ok {
			_, _ = w.Write([]byte(body))
			return
		}
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(deniedBody))
	}))
	t.Cleanup(srv.Close)

	total, inserted, err := scanCloudQuotasWithClients(t.Context(), p, st, testScanID,
		fakeServiceUsageService(t, srv), fakeCloudQuotasClient(t, srv))
	if err != nil {
		t.Fatalf("scanCloudQuotasWithClients: %v", err)
	}
	if total != 3 || inserted != 3 {
		t.Errorf("counts: got total=%d inserted=%d, want 3/3 — the readable service must still land", total, inserted)
	}
	if len(warnings) != 1 {
		t.Fatalf("warnings: got %d, want 1 aggregated entry", len(warnings))
	}
	if warnings[0].Service != quotaServiceName || warnings[0].Scope != p.ID {
		t.Errorf("warning scope: got %+v", warnings[0])
	}
}

// TestScanCloudQuotas_WholeAPIDisabledIsNotAWarning: every enabled service
// failing on the same project fact is one project condition, not tens of
// service problems, so it escalates to the sentinel the dispatcher renders as
// "(project: disabled)".
func TestScanCloudQuotas_WholeAPIDisabledIsNotAWarning(t *testing.T) {
	st := newTestStore(t)
	var warnings []store.ScanWarning
	st.OnWarn = func(w store.ScanWarning) { warnings = append(warnings, w) }
	p := newTestProject("proj1")

	notEnabled := `{"error":{"code":403,"message":"Cloud Quotas API has not been used in project 123 before or it is disabled.","errors":[{"reason":"accessNotConfigured"}]}}`
	listBody := enabledServicesBody(t, "compute.googleapis.com", "iam.googleapis.com")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/v1/projects/proj1/services" {
			_, _ = w.Write([]byte(listBody))
			return
		}
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(notEnabled))
	}))
	t.Cleanup(srv.Close)

	_, _, err := scanCloudQuotasWithClients(t.Context(), p, st, testScanID,
		fakeServiceUsageService(t, srv), fakeCloudQuotasClient(t, srv))
	if !errors.Is(err, errServiceDisabled) {
		t.Fatalf("err = %v, want the service-disabled sentinel", err)
	}
	if len(warnings) != 0 {
		t.Errorf("warnings: got %d, want 0 — the sentinel replaces the warning", len(warnings))
	}
}

package aws

import (
	"context"
	"errors"
	"strconv"
	"sync/atomic"
	"testing"

	"codeberg.org/icearp/disco/store"
	"github.com/aws/aws-sdk-go-v2/service/servicequotas"
	sqtypes "github.com/aws/aws-sdk-go-v2/service/servicequotas/types"
	"golang.org/x/time/rate"
)

// noRateLimit returns an unthrottled pacer so unit tests don't pace their calls at
// the production 10 req/s. rate.Inf makes Wait return immediately; burst is ignored.
func noRateLimit() *pacer { return newPacer(rate.Inf, 0) }

// stubServiceQuotas is an in-memory serviceQuotasAPI for unit tests. ListServices
// returns the service codes; ListServiceQuotas serves per-code quota slices, both
// paginated by an integer NextToken offset. servicesErr / quotaErr inject failures.
type stubServiceQuotas struct {
	services     []string
	servicesPage int
	servicesErr  error
	quotas       map[string][]sqtypes.ServiceQuota
	quotaPage    int
	quotaErr     map[string]error // keyed by ServiceCode
	gotMaxResult atomic.Int32     // a MaxResults seen on ListServiceQuotas (race-free under fanout)
}

func encodeTok(n int) *string { s := strconv.Itoa(n); return &s }

func decodeTok(t *string) int {
	if t == nil {
		return 0
	}
	n, _ := strconv.Atoi(*t)
	return n
}

func (s *stubServiceQuotas) ListServices(_ context.Context, in *servicequotas.ListServicesInput, _ ...func(*servicequotas.Options)) (*servicequotas.ListServicesOutput, error) {
	if s.servicesErr != nil {
		return nil, s.servicesErr
	}
	start := decodeTok(in.NextToken)
	end := len(s.services)
	if s.servicesPage > 0 && start+s.servicesPage < end {
		end = start + s.servicesPage
	}
	out := &servicequotas.ListServicesOutput{}
	for _, c := range s.services[start:end] {
		out.Services = append(out.Services, sqtypes.ServiceInfo{ServiceCode: &c})
	}
	if end < len(s.services) {
		out.NextToken = encodeTok(end)
	}
	return out, nil
}

func (s *stubServiceQuotas) ListServiceQuotas(_ context.Context, in *servicequotas.ListServiceQuotasInput, _ ...func(*servicequotas.Options)) (*servicequotas.ListServiceQuotasOutput, error) {
	if in.MaxResults != nil {
		s.gotMaxResult.Store(*in.MaxResults)
	}
	code := ""
	if in.ServiceCode != nil {
		code = *in.ServiceCode
	}
	if e, ok := s.quotaErr[code]; ok {
		return nil, e
	}
	all := s.quotas[code]
	start := decodeTok(in.NextToken)
	end := len(all)
	if s.quotaPage > 0 && start+s.quotaPage < end {
		end = start + s.quotaPage
	}
	out := &servicequotas.ListServiceQuotasOutput{Quotas: all[start:end]}
	if end < len(all) {
		out.NextToken = encodeTok(end)
	}
	return out, nil
}

func regionalQuota(region, sc, qc, name string, value float64, adjustable bool) sqtypes.ServiceQuota {
	arn := "arn:aws:servicequotas:" + region + ":" + testAccountID + ":" + sc + "/" + qc
	return sqtypes.ServiceQuota{
		ServiceCode: &sc, QuotaCode: &qc, QuotaName: &name, Value: &value,
		Adjustable: adjustable, GlobalQuota: false, QuotaArn: &arn,
	}
}

func globalQuota(sc, qc, name string, value float64, adjustable bool) sqtypes.ServiceQuota {
	arn := "arn:aws:servicequotas:::" + sc + "/" + qc
	return sqtypes.ServiceQuota{
		ServiceCode: &sc, QuotaCode: &qc, QuotaName: &name, Value: &value,
		Adjustable: adjustable, GlobalQuota: true, QuotaArn: &arn,
	}
}

func listQuotaRows(t *testing.T, st *store.Store) []store.Resource {
	t.Helper()
	got, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, Types: []string{TypeServiceQuota}, IncludeManaged: true, Limit: 1000,
	})
	if err != nil {
		t.Fatalf("ListResources: %v", err)
	}
	return got
}

// TestScanServiceQuotas_PersistsAdjustableOnly is the happy path: adjustable quotas
// across two service codes persist (with multi-page pagination exercised), while a
// non-adjustable quota is dropped.
func TestScanServiceQuotas_PersistsAdjustableOnly(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	region := "us-east-1"

	stub := &stubServiceQuotas{
		services:     []string{"ec2", "lambda"},
		servicesPage: 1, // two pages → exercises ListServices pagination
		quotaPage:    1, // one quota per page → exercises ListServiceQuotas pagination
		quotas: map[string][]sqtypes.ServiceQuota{
			"ec2": {
				regionalQuota(region, "ec2", "L-0EA8095F", "VPCs per Region", 5, true),
				regionalQuota(region, "ec2", "L-FIXED", "Fixed limit", 10, false), // dropped
			},
			"lambda": {
				regionalQuota(region, "lambda", "L-B99A9384", "Concurrent executions", 1000, true),
			},
		},
	}

	total, inserted, err := scanServiceQuotasWithClient(context.Background(), stub, acct, region, st, testScanID, noRateLimit())
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != 2 || inserted != 2 {
		t.Errorf("total=%d inserted=%d, want 2/2 (non-adjustable dropped)", total, inserted)
	}

	if got := stub.gotMaxResult.Load(); got != 100 {
		t.Errorf("ListServiceQuotas MaxResults = %d, want 100 (page-count guard)", got)
	}

	rows := listQuotaRows(t, st)
	if len(rows) != 2 {
		t.Fatalf("got %d rows, want 2", len(rows))
	}
	for _, r := range rows {
		if r.Type != TypeServiceQuota {
			t.Errorf("type = %q, want %q", r.Type, TypeServiceQuota)
		}
		if !r.ManagedByProvider {
			t.Errorf("%s: ManagedByProvider = false, want true", r.NativeID)
		}
		if r.Region == nil || *r.Region != region {
			t.Errorf("%s: region = %v, want %q", r.NativeID, r.Region, region)
		}
		if r.Name == nil || *r.Name == "" {
			t.Errorf("%s: empty Name", r.NativeID)
		}
	}
}

// TestScanServiceQuotas_UnsupportedCodeSkipped: a code that returns
// NoSuchResourceException / IllegalArgumentException / AccessDenied is dropped
// silently; sibling codes still persist and the scan succeeds.
func TestScanServiceQuotas_UnsupportedCodeSkipped(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	region := "us-east-1"

	stub := &stubServiceQuotas{
		services: []string{"ec2", "fsx", "rds", "iam"},
		quotas: map[string][]sqtypes.ServiceQuota{
			"ec2": {regionalQuota(region, "ec2", "L-1", "VPCs", 5, true)},
		},
		quotaErr: map[string]error{
			"fsx": apiErr("NoSuchResourceException", "unsupported here"),
			"rds": apiErr("AccessDeniedException", "no read"),
			"iam": apiErr("IllegalArgumentException", "bad code"),
		},
	}

	total, inserted, err := scanServiceQuotasWithClient(context.Background(), stub, acct, region, st, testScanID, noRateLimit())
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != 1 || inserted != 1 {
		t.Errorf("total=%d inserted=%d, want 1/1 (3 codes skipped)", total, inserted)
	}
}

// TestScanServiceQuotas_EmptyService: a code with zero quotas yields no rows, no error.
func TestScanServiceQuotas_EmptyService(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	stub := &stubServiceQuotas{
		services: []string{"ec2"},
		quotas:   map[string][]sqtypes.ServiceQuota{"ec2": {}},
	}

	total, inserted, err := scanServiceQuotasWithClient(context.Background(), stub, acct, "us-east-1", st, testScanID, noRateLimit())
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != 0 || inserted != 0 {
		t.Errorf("total=%d inserted=%d, want 0/0", total, inserted)
	}
}

// TestScanServiceQuotas_SynthesizedNativeID: a quota lacking QuotaArn falls back to
// the region-less synthesized id; one missing both ARN and codes is dropped.
func TestScanServiceQuotas_SynthesizedNativeID(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	region := "us-east-1"

	noArn := regionalQuota(region, "ec2", "L-SYNTH", "Synth", 7, true)
	noArn.QuotaArn = nil                                                      // force synthesized id
	noCode := sqtypes.ServiceQuota{QuotaName: sp("orphan"), Adjustable: true} // no arn, no codes → dropped

	stub := &stubServiceQuotas{
		services: []string{"ec2"},
		quotas:   map[string][]sqtypes.ServiceQuota{"ec2": {noArn, noCode}},
	}

	total, inserted, err := scanServiceQuotasWithClient(context.Background(), stub, acct, region, st, testScanID, noRateLimit())
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != 1 || inserted != 1 {
		t.Fatalf("total=%d inserted=%d, want 1/1 (codeless quota dropped)", total, inserted)
	}
	rows := listQuotaRows(t, st)
	if len(rows) != 1 || rows[0].NativeID != "aws:servicequotas:ec2:L-SYNTH" {
		t.Fatalf("NativeID = %q, want aws:servicequotas:ec2:L-SYNTH", rows[0].NativeID)
	}
}

// TestScanServiceQuotas_GenuineErrorContinues: a non-skippable per-code error is
// reported (not propagated) and sibling codes still persist.
func TestScanServiceQuotas_GenuineErrorContinues(t *testing.T) {
	st := newTestStore(t)
	var reported store.ScanError
	st.OnError = func(e store.ScanError) { reported = e }
	acct := newTestAccount(testAccountID)
	region := "us-east-1"

	stub := &stubServiceQuotas{
		services: []string{"ec2", "broken"},
		quotas: map[string][]sqtypes.ServiceQuota{
			"ec2": {regionalQuota(region, "ec2", "L-1", "VPCs", 5, true)},
		},
		quotaErr: map[string]error{"broken": errors.New("network kaput")},
	}

	total, inserted, err := scanServiceQuotasWithClient(context.Background(), stub, acct, region, st, testScanID, noRateLimit())
	if err != nil {
		t.Fatalf("scan returned err, want nil (collect-and-continue): %v", err)
	}
	if total != 1 || inserted != 1 {
		t.Errorf("total=%d inserted=%d, want 1/1", total, inserted)
	}
	if reported.Service != "servicequotas:ListServiceQuotas" {
		t.Errorf("reported.Service = %q, want servicequotas:ListServiceQuotas", reported.Service)
	}
}

// TestScanServiceQuotas_ListServicesAccessDenied: an AccessDenied on ListServices
// warns and returns nil with no rows.
func TestScanServiceQuotas_ListServicesAccessDenied(t *testing.T) {
	st := newTestStore(t)
	var warned store.ScanWarning
	st.OnWarn = func(w store.ScanWarning) { warned = w }
	acct := newTestAccount(testAccountID)

	stub := &stubServiceQuotas{servicesErr: apiErr("AccessDenied", "denied")}

	total, inserted, err := scanServiceQuotasWithClient(context.Background(), stub, acct, "us-east-1", st, testScanID, noRateLimit())
	if err != nil {
		t.Fatalf("scan returned err: %v", err)
	}
	if total != 0 || inserted != 0 {
		t.Errorf("total=%d inserted=%d, want 0/0", total, inserted)
	}
	if warned.Service != "servicequotas:ListServices" {
		t.Errorf("warned.Service = %q, want servicequotas:ListServices", warned.Service)
	}
}

// TestScanServiceQuotas_GlobalHomeRegionElection: a global quota is recorded once,
// at the elected home region, under the region-less "global" sentinel — and dropped
// in every other region. us-east-1 is preferred; otherwise the min scanned region.
func TestScanServiceQuotas_GlobalHomeRegionElection(t *testing.T) {
	mkStub := func() *stubServiceQuotas {
		return &stubServiceQuotas{
			services: []string{"route53"},
			quotas: map[string][]sqtypes.ServiceQuota{
				"route53": {globalQuota("route53", "L-4EA4796A", "Hosted zones", 500, true)},
			},
		}
	}

	t.Run("emitted at us-east-1, dropped at us-west-2", func(t *testing.T) {
		acct := &account{ID: testAccountID, Name: "Test Account", Regions: []string{"us-east-1", "us-west-2"}}

		stEast := newTestStore(t)
		if _, _, err := scanServiceQuotasWithClient(context.Background(), mkStub(), acct, "us-east-1", stEast, testScanID, noRateLimit()); err != nil {
			t.Fatalf("east scan: %v", err)
		}
		rows := listQuotaRows(t, stEast)
		if len(rows) != 1 {
			t.Fatalf("east: got %d rows, want 1", len(rows))
		}
		if rows[0].Region == nil || *rows[0].Region != "global" {
			t.Errorf("east: region = %v, want global", rows[0].Region)
		}

		stWest := newTestStore(t)
		if _, _, err := scanServiceQuotasWithClient(context.Background(), mkStub(), acct, "us-west-2", stWest, testScanID, noRateLimit()); err != nil {
			t.Fatalf("west scan: %v", err)
		}
		if rows := listQuotaRows(t, stWest); len(rows) != 0 {
			t.Errorf("west: got %d rows, want 0 (global dropped at non-home region)", len(rows))
		}
	})

	t.Run("fallback to min region when us-east-1 not scanned", func(t *testing.T) {
		acct := &account{ID: testAccountID, Name: "Test Account", Regions: []string{"us-west-2", "eu-west-1"}}
		// min(us-west-2, eu-west-1) == eu-west-1 → home.
		st := newTestStore(t)
		if _, _, err := scanServiceQuotasWithClient(context.Background(), mkStub(), acct, "eu-west-1", st, testScanID, noRateLimit()); err != nil {
			t.Fatalf("scan: %v", err)
		}
		if rows := listQuotaRows(t, st); len(rows) != 1 {
			t.Errorf("got %d rows, want 1 (global emitted at elected min home eu-west-1)", len(rows))
		}
	})
}

// TestScanServiceQuotas_LimitOnlyChurnFree is the change-over-time contract:
// re-scanning an unchanged limit produces NO new version; a changed limit splits
// a new version. Drives the real scanner + mustJSON serialization, so it would
// also catch a volatile field sneaking into the stored attributes.
func TestScanServiceQuotas_LimitOnlyChurnFree(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	region := "us-east-1"
	arn := "arn:aws:servicequotas:" + region + ":" + testAccountID + ":ec2/L-1"

	value := 5.0
	scan := func() {
		t.Helper()
		stub := &stubServiceQuotas{
			services: []string{"ec2"},
			quotas: map[string][]sqtypes.ServiceQuota{
				"ec2": {regionalQuota(region, "ec2", "L-1", "VPCs", value, true)},
			},
		}
		if _, _, err := scanServiceQuotasWithClient(context.Background(), stub, acct, region, st, testScanID, noRateLimit()); err != nil {
			t.Fatalf("scan: %v", err)
		}
	}
	rootID := store.ResourceID("aws", acct.ID, TypeServiceQuota, arn)
	versionCount := func() int {
		t.Helper()
		v, err := st.GetResourceVersions(rootID)
		if err != nil {
			t.Fatalf("GetResourceVersions: %v", err)
		}
		return len(v)
	}

	scan()
	if n := versionCount(); n != 1 {
		t.Fatalf("after first scan: got %d versions, want 1", n)
	}
	scan() // identical limit → no churn
	if n := versionCount(); n != 1 {
		t.Fatalf("after unchanged rescan: got %d versions, want 1 (limit-only must be churn-free)", n)
	}
	value = 10.0 // quota raised
	scan()
	if n := versionCount(); n != 2 {
		t.Fatalf("after limit change: got %d versions, want 2", n)
	}
}

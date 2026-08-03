package aws

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/servicequotas"
	sqtypes "github.com/aws/aws-sdk-go-v2/service/servicequotas/types"
	"github.com/aws/smithy-go"
	"github.com/icearp/disco-cli/store"
	"golang.org/x/time/rate"
)

// noRateLimit returns an unthrottled pacer so tests skip the production 10 req/s pace;
// rate.Inf makes Wait return immediately, burst ignored.
func noRateLimit() *pacer { return newPacer(rate.Inf, 0) }

// stubServiceQuotas is an in-memory serviceQuotasAPI for unit tests. ListServices
// returns the service codes; ListServiceQuotas serves per-code quota slices, both
// paginated by an integer NextToken offset. ListAWSDefaultServiceQuotas serves the
// per-code defaults. servicesErr / quotaErr / defaultsErr inject failures.
type stubServiceQuotas struct {
	services     []string
	servicesPage int
	servicesErr  error
	quotas       map[string][]sqtypes.ServiceQuota
	quotaPage    int
	quotaErr     map[string]error // keyed by ServiceCode
	defaults     map[string][]sqtypes.ServiceQuota
	defaultsErr  error
	gotMaxResult atomic.Int32 // MaxResults seen on ListServiceQuotas (race-free under fanout)
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

func (s *stubServiceQuotas) ListAWSDefaultServiceQuotas(_ context.Context, in *servicequotas.ListAWSDefaultServiceQuotasInput, _ ...func(*servicequotas.Options)) (*servicequotas.ListAWSDefaultServiceQuotasOutput, error) {
	if s.defaultsErr != nil {
		return nil, s.defaultsErr
	}
	code := ""
	if in.ServiceCode != nil {
		code = *in.ServiceCode
	}
	return &servicequotas.ListAWSDefaultServiceQuotasOutput{Quotas: s.defaults[code]}, nil
}

func regionalQuota(region, sc, qc, name string, value float64, adjustable bool) sqtypes.ServiceQuota {
	arn := "arn:aws:servicequotas:" + region + ":" + testAccountID + ":" + sc + "/" + qc
	desc := "The maximum number of " + name + "."
	return sqtypes.ServiceQuota{
		ServiceCode: &sc, QuotaCode: &qc, QuotaName: &name, Value: &value,
		Adjustable: adjustable, GlobalQuota: false, QuotaArn: &arn, Description: &desc,
	}
}

func globalQuota(sc, qc, name string, value float64, adjustable bool) sqtypes.ServiceQuota {
	arn := "arn:aws:servicequotas:::" + sc + "/" + qc
	return sqtypes.ServiceQuota{
		ServiceCode: &sc, QuotaCode: &qc, QuotaName: &name, Value: &value,
		Adjustable: adjustable, GlobalQuota: true, QuotaArn: &arn,
	}
}

func listQuotaRows(t *testing.T, st *store.Store) []store.Quota {
	t.Helper()
	got, err := st.ListQuotas(store.QuotaFilter{Providers: []string{"aws"}, Limit: 1000})
	if err != nil {
		t.Fatalf("ListQuotas: %v", err)
	}
	return got
}

// TestScanServiceQuotas_PersistsAdjustableAndFixed is the happy path: quotas
// across two service codes persist (with multi-page pagination exercised), and
// the non-adjustable one is kept rather than dropped — a hard ceiling AWS moves
// on its own is the signal this table exists to record.
func TestScanServiceQuotas_PersistsAdjustableAndFixed(t *testing.T) {
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
				regionalQuota(region, "ec2", "L-FIXED", "Fixed limit", 10, false),
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
	if total != 3 || inserted != 3 {
		t.Errorf("total=%d inserted=%d, want 3/3 (non-adjustable is recorded, not dropped)", total, inserted)
	}

	if got := stub.gotMaxResult.Load(); got != 100 {
		t.Errorf("ListServiceQuotas MaxResults = %d, want 100 (page-count guard)", got)
	}

	rows := listQuotaRows(t, st)
	if len(rows) != 3 {
		t.Fatalf("got %d rows, want 3", len(rows))
	}
	for _, r := range rows {
		if r.Region != region {
			t.Errorf("%s: region = %q, want %q", r.QuotaCode, r.Region, region)
		}
		if r.Name == "" {
			t.Errorf("%s: empty Name", r.QuotaCode)
		}
		if r.Value == nil {
			t.Errorf("%s: nil Value", r.QuotaCode)
		}
		// AWS populates Description on every quota it reports; it is the one
		// field that says what a limit governs, which no combination of
		// service code, quota code and unit conveys.
		if r.Description == nil || *r.Description == "" {
			t.Errorf("%s: empty Description", r.QuotaCode)
		}
	}

	fixed, err := st.GetQuota(store.QuotaID("aws", acct.ID, region, "ec2", "L-FIXED"))
	if err != nil {
		t.Fatalf("GetQuota: %v", err)
	}
	if fixed == nil {
		t.Fatal("non-adjustable quota was dropped")
	}
	if fixed.Adjustable {
		t.Error("adjustable flag did not round-trip as false")
	}

	// The split is only real if nothing lands in resources any more.
	res, err := st.ListResources(store.ResourceFilter{IncludeManaged: true, Limit: 100})
	if err != nil {
		t.Fatalf("ListResources: %v", err)
	}
	if len(res) != 0 {
		t.Fatalf("quota scan wrote %d rows into resources, want 0", len(res))
	}
}

// The AWS default is what makes applied-versus-default answerable: on an
// adjustable quota a divergence is an increase the customer requested, and on a
// non-adjustable one it means AWS moved a hard ceiling.
func TestScanServiceQuotas_RecordsAWSDefault(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	region := "us-east-1"

	stub := &stubServiceQuotas{
		services: []string{"ec2"},
		quotas: map[string][]sqtypes.ServiceQuota{
			"ec2": {
				regionalQuota(region, "ec2", "L-RAISED", "Raised limit", 64, true),
				regionalQuota(region, "ec2", "L-UNKNOWN", "No default published", 7, true),
			},
		},
		defaults: map[string][]sqtypes.ServiceQuota{
			"ec2": {regionalQuota(region, "ec2", "L-RAISED", "Raised limit", 5, true)},
		},
	}

	if _, _, err := scanServiceQuotasWithClient(context.Background(), stub, acct, region, st, testScanID, noRateLimit()); err != nil {
		t.Fatalf("scan: %v", err)
	}

	raised, err := st.GetQuota(store.QuotaID("aws", acct.ID, region, "ec2", "L-RAISED"))
	if err != nil || raised == nil {
		t.Fatalf("GetQuota raised: %v", err)
	}
	if raised.DefaultValue == nil || *raised.DefaultValue != 5 {
		t.Errorf("default value = %v, want 5", raised.DefaultValue)
	}
	if raised.Value == nil || *raised.Value != 64 {
		t.Errorf("applied value = %v, want 64", raised.Value)
	}

	// A quota with no published default keeps a NULL column, which reads as
	// unknown rather than as "the default is zero".
	unknown, err := st.GetQuota(store.QuotaID("aws", acct.ID, region, "ec2", "L-UNKNOWN"))
	if err != nil || unknown == nil {
		t.Fatalf("GetQuota unknown: %v", err)
	}
	if unknown.DefaultValue != nil {
		t.Errorf("default value = %v, want NULL when AWS publishes none", *unknown.DefaultValue)
	}

	only, err := st.ListQuotas(store.QuotaFilter{RaisedOnly: true})
	if err != nil {
		t.Fatalf("ListQuotas raised: %v", err)
	}
	if len(only) != 1 || only[0].QuotaCode != "L-RAISED" {
		t.Fatalf("RaisedOnly returned %d rows: %+v", len(only), only)
	}
}

// The defaults call is a convenience, not a precondition. Losing it must not
// cost us the applied limit, which is the more important of the two.
func TestScanServiceQuotas_DefaultsFailureDegrades(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	region := "us-east-1"

	stub := &stubServiceQuotas{
		services:    []string{"ec2"},
		quotas:      map[string][]sqtypes.ServiceQuota{"ec2": {regionalQuota(region, "ec2", "L-1", "VPCs", 5, true)}},
		defaultsErr: apiErr("AccessDeniedException", "no read"),
	}

	total, inserted, err := scanServiceQuotasWithClient(context.Background(), stub, acct, region, st, testScanID, noRateLimit())
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != 1 || inserted != 1 {
		t.Fatalf("total=%d inserted=%d, want 1/1 despite the defaults call failing", total, inserted)
	}
	got, err := st.GetQuota(store.QuotaID("aws", acct.ID, region, "ec2", "L-1"))
	if err != nil || got == nil {
		t.Fatalf("GetQuota: %v", err)
	}
	if got.DefaultValue != nil {
		t.Errorf("default value = %v, want NULL when the defaults call failed", *got.DefaultValue)
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

// Identity is (provider, account, region, service code, quota code), so a quota
// missing either code cannot be addressed and is dropped. The ARN is no longer
// the key — it survives in the attributes remainder.
func TestScanServiceQuotas_CodelessQuotaDropped(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	region := "us-east-1"

	noArn := regionalQuota(region, "ec2", "L-SYNTH", "Synth", 7, true)
	noArn.QuotaArn = nil                                                      // the ARN is not the identity
	noCode := sqtypes.ServiceQuota{QuotaName: sp("orphan"), Adjustable: true} // no codes → dropped

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
	if len(rows) != 1 || rows[0].QuotaCode != "L-SYNTH" {
		t.Fatalf("QuotaCode = %+v, want L-SYNTH", rows)
	}
	if rows[0].ID != store.QuotaID("aws", acct.ID, region, "ec2", "L-SYNTH") {
		t.Fatalf("row id %q is not the natural-key hash", rows[0].ID)
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
		if rows[0].Region != "global" {
			t.Errorf("east: region = %q, want global", rows[0].Region)
		}
		if !rows[0].GlobalQuota {
			t.Error("east: GlobalQuota flag did not round-trip")
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
// a new version. Uses the real scanner + mustJSON serialization, so it also
// catches a volatile field sneaking into the stored attributes — which is what
// would make a catalogue of this size unscannable.
func TestScanServiceQuotas_LimitOnlyChurnFree(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	region := "us-east-1"

	value := 5.0
	adjustable := true
	scan := func() {
		t.Helper()
		stub := &stubServiceQuotas{
			services: []string{"ec2"},
			quotas: map[string][]sqtypes.ServiceQuota{
				"ec2": {regionalQuota(region, "ec2", "L-1", "VPCs", value, adjustable)},
			},
		}
		if _, _, err := scanServiceQuotasWithClient(context.Background(), stub, acct, region, st, testScanID, noRateLimit()); err != nil {
			t.Fatalf("scan: %v", err)
		}
	}
	rootID := store.QuotaID("aws", acct.ID, region, "ec2", "L-1")
	versionCount := func() int {
		t.Helper()
		v, err := st.GetQuotaVersions(rootID)
		if err != nil {
			t.Fatalf("GetQuotaVersions: %v", err)
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
	// AWS making a hard limit adjustable is itself a change worth a version.
	adjustable = false
	scan()
	if n := versionCount(); n != 3 {
		t.Fatalf("after an adjustability change: got %d versions, want 3", n)
	}
}

// TestScanServiceQuotas_ReportsDefaultsFailure pins that a failed
// ListAWSDefaultServiceQuotas is reported rather than swallowed, while the
// applied limits are still recorded.
//
// Both halves matter and they pull in opposite directions. Losing the applied
// limit because the default was unreadable would discard the more important of
// the two, so degradation is deliberate. But a SILENT degradation writes NULL
// into default_value, and the store compares DefaultValue when deciding whether
// a quota changed -- so the next scan that reads the default records a version
// bump on a limit that never moved. That is how ~91k spurious versions and
// 107 MB appeared on a real tenant, presenting as genuine change in the "limits
// that changed" view. The scan record is the only place the cause is findable.
func TestScanServiceQuotas_ReportsDefaultsFailure(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	region := "us-east-1"

	errs, warns := collectReports(st)

	stub := &stubServiceQuotas{
		services:    []string{"ec2"},
		defaultsErr: &smithy.GenericAPIError{Code: "ThrottlingException", Message: "Rate exceeded"},
		quotas: map[string][]sqtypes.ServiceQuota{
			"ec2": {regionalQuota(region, "ec2", "L-0EA8095F", "VPCs per Region", 5, true)},
		},
	}

	total, _, err := scanServiceQuotasWithClient(context.Background(), stub, acct, region, st, testScanID, noRateLimit())
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	// The limit is still recorded. A defaults failure must not cost the applied value.
	if total != 1 {
		t.Errorf("total = %d, want 1 (the applied limit survives a defaults failure)", total)
	}

	if len(*errs) == 0 {
		t.Fatal("defaults failure was swallowed; no ScanError reported")
	}
	if got := (*errs)[0].Service; got != "servicequotas:ListAWSDefaultServiceQuotas" {
		t.Errorf("reported service = %q; want the defaults operation, so the "+
			"failing call is identifiable without reading the scanner", got)
	}
	// ONE entry, not one per service code: a systematic failure hits all 251 of
	// them in each of 18 regions, and 4,500 near identical rows would bury every
	// other error in the scan rather than shout louder.
	if len(*errs) != 1 {
		t.Errorf("got %d errors, want exactly 1 aggregated entry for the region", len(*errs))
	}
	if !strings.Contains((*errs)[0].Scope, region) {
		t.Errorf("scope = %q, want it to name the region it aggregates", (*errs)[0].Scope)
	}
	// The count and the underlying error both survive aggregation, or the entry
	// says a failure happened without saying how bad or why.
	if msg := (*errs)[0].Message; !strings.Contains(msg, "Rate exceeded") || !strings.Contains(msg, "1 service") {
		t.Errorf("message = %q, want it to carry the failure count and the first error", msg)
	}
	if len(*warns) != 0 {
		t.Errorf("throttling reported as a warning as well as an error: %+v", *warns)
	}
}

// TestScanServiceQuotas_DefaultsAccessDeniedWarnsNotErrors covers the case a
// missing IAM grant produces. servicequotas:ListAWSDefaultServiceQuotas is a
// separate action from ListServiceQuotas, so a policy can allow the limits and
// deny the defaults -- which would null every default on every scan forever.
// It is a warning rather than an error because nothing is broken, but it must
// not be silent.
func TestScanServiceQuotas_DefaultsAccessDeniedWarnsNotErrors(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	region := "us-east-1"

	errs, warns := collectReports(st)

	stub := &stubServiceQuotas{
		services:    []string{"ec2"},
		defaultsErr: &smithy.GenericAPIError{Code: "AccessDeniedException", Message: "User: ... not authorized"},
		quotas: map[string][]sqtypes.ServiceQuota{
			"ec2": {regionalQuota(region, "ec2", "L-0EA8095F", "VPCs per Region", 5, true)},
		},
	}

	if _, _, err := scanServiceQuotasWithClient(context.Background(), stub, acct, region, st, testScanID, noRateLimit()); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if len(*errs) != 0 {
		t.Errorf("access denied raised as a hard error: %+v", *errs)
	}
	if len(*warns) == 0 {
		t.Fatal("access denied on the defaults call was silent")
	}
}

// TestScanServiceQuotas_NoPublishedDefaultsIsSilent pins the third outcome. A
// service with no published defaults answers NoSuchResource, which is an answer
// and not a failure -- reporting it would put a row on the scan record for every
// such service and train operators to ignore the ones that matter.
func TestScanServiceQuotas_NoPublishedDefaultsIsSilent(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	region := "us-east-1"

	errs, warns := collectReports(st)

	stub := &stubServiceQuotas{
		services:    []string{"ec2"},
		defaultsErr: &smithy.GenericAPIError{Code: "NoSuchResourceException", Message: "no defaults"},
		quotas: map[string][]sqtypes.ServiceQuota{
			"ec2": {regionalQuota(region, "ec2", "L-0EA8095F", "VPCs per Region", 5, true)},
		},
	}

	if _, _, err := scanServiceQuotasWithClient(context.Background(), stub, acct, region, st, testScanID, noRateLimit()); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if len(*errs) != 0 || len(*warns) != 0 {
		t.Errorf("a service with no published defaults was not silent: errs=%+v warns=%+v", *errs, *warns)
	}
}

// collectReports wires the store's report callbacks into slices a test can
// assert on.
//
// The mutex is not decorative: scanServiceQuotasWithClient fans out over service
// codes with up to sqWorkers goroutines, and every one of them can reach these
// callbacks, so an unsynchronized append is a data race that -race would catch
// only on the runs where the timing lands. Production wiring locks for the same
// reason.
func collectReports(st *store.Store) (*[]store.ScanError, *[]store.ScanWarning) {
	var (
		mu    sync.Mutex
		errs  []store.ScanError
		warns []store.ScanWarning
	)
	st.OnError = func(e store.ScanError) {
		mu.Lock()
		defer mu.Unlock()
		errs = append(errs, e)
	}
	st.OnWarn = func(w store.ScanWarning) {
		mu.Lock()
		defer mu.Unlock()
		warns = append(warns, w)
	}
	return &errs, &warns
}

// TestScanServiceQuotas_DefaultsFailuresAggregatePerRegion is the test the
// aggregation exists for: many service codes failing produce ONE scan-record
// entry, carrying the count.
//
// Without it the natural implementation reports inside the per-code fan-out,
// and a systematic failure -- throttling, a missing grant -- writes one row per
// code per region. At 251 services across 18 regions that is ~4,500 near
// identical entries in a single scan, which buries every other error and bloats
// the stored payload. One entry that says how many is strictly more useful.
func TestScanServiceQuotas_DefaultsFailuresAggregatePerRegion(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	region := "us-east-1"
	errs, _ := collectReports(st)

	codes := []string{"ec2", "lambda", "s3", "iam", "rds"}
	quotas := map[string][]sqtypes.ServiceQuota{}
	for _, c := range codes {
		quotas[c] = []sqtypes.ServiceQuota{regionalQuota(region, c, "L-1", "A limit", 5, true)}
	}
	stub := &stubServiceQuotas{
		services:    codes,
		defaultsErr: &smithy.GenericAPIError{Code: "ThrottlingException", Message: "Rate exceeded"},
		quotas:      quotas,
	}

	total, _, err := scanServiceQuotasWithClient(context.Background(), stub, acct, region, st, testScanID, noRateLimit())
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != len(codes) {
		t.Errorf("total = %d, want %d (every applied limit survives)", total, len(codes))
	}
	if len(*errs) != 1 {
		t.Fatalf("got %d scan errors for %d failing service codes, want 1 aggregated entry",
			len(*errs), len(codes))
	}
	// The count is what makes one entry as informative as the flood it replaces.
	if msg := (*errs)[0].Message; !strings.Contains(msg, "5 service") {
		t.Errorf("message = %q, want it to report how many codes failed", msg)
	}
}

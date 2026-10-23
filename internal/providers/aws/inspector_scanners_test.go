package aws

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/inspector2"
	i2types "github.com/aws/aws-sdk-go-v2/service/inspector2/types"
	"github.com/icearp/disco-cli/store"
)

var i2TestTags = map[string]string{"env": "prod", "blank": ""}

func i2FilterARN(id string) string {
	return "arn:aws:inspector2:" + testRegion + ":" + testAccountID + ":owner/" + testAccountID + "/filter/" + id
}

func i2CisARN(id string) string {
	return "arn:aws:inspector2:" + testRegion + ":" + testAccountID + ":owner/" + testAccountID + "/cis-configuration/" + id
}

func i2Filter(id string, tags map[string]string) i2types.Filter {
	at := time.Date(2026, 9, 2, 8, 0, 0, 0, time.UTC)
	return i2types.Filter{
		Arn: sdkaws.String(i2FilterARN(id)), Name: sdkaws.String("flt-" + id), Action: i2types.FilterActionSuppress,
		OwnerId: sdkaws.String(testAccountID), Reason: sdkaws.String("accepted risk"), CreatedAt: &at, UpdatedAt: &at,
		Criteria: &i2types.FilterCriteria{AwsAccountId: []i2types.StringFilter{{Comparison: i2types.StringComparisonEquals, Value: sdkaws.String(testAccountID)}}},
		Tags:     tags,
	}
}

func i2Cis(id string, tags map[string]string) i2types.CisScanConfiguration {
	return i2types.CisScanConfiguration{
		ScanConfigurationArn: sdkaws.String(i2CisARN(id)), ScanName: sdkaws.String("cis-" + id),
		OwnerId: sdkaws.String(testAccountID), SecurityLevel: i2types.CisSecurityLevelLevel1,
		Targets: &i2types.CisTargets{AccountIds: []string{testAccountID}},
		Tags:    tags,
	}
}

// i2Handlers answers every op scanInspector2 calls. ListFilters and
// ListCisScanConfigurations serve two pages: a tagged element and one without
// an ARN, then an untagged element; any other request input fails the test.
// Every other op serves one row.
func i2Handlers(t *testing.T) map[string]func(any) (any, error) {
	arn := func(kind string) *string {
		return sdkaws.String("arn:aws:inspector2:" + testRegion + ":" + testAccountID + ":owner/" + testAccountID + "/" + kind + "/x")
	}
	return map[string]func(any) (any, error){
		"ListFilters": func(in any) (any, error) {
			req := in.(*inspector2.ListFiltersInput)
			if sdkaws.ToInt32(req.MaxResults) != 100 || req.Action != "" || req.Arns != nil {
				t.Errorf("ListFilters input = %+v; want MaxResults 100 and no filter", req)
			}
			switch sv(req.NextToken) {
			case "":
				return &inspector2.ListFiltersOutput{
					Filters:   []i2types.Filter{i2Filter("f1", i2TestTags), {Name: sdkaws.String("flt-noarn")}},
					NextToken: sdkaws.String("p2"),
				}, nil
			case "p2":
				return &inspector2.ListFiltersOutput{Filters: []i2types.Filter{i2Filter("f2", nil)}}, nil
			}
			t.Errorf("ListFilters token = %q", sv(req.NextToken))
			return &inspector2.ListFiltersOutput{}, nil
		},
		"ListCisScanConfigurations": func(in any) (any, error) {
			req := in.(*inspector2.ListCisScanConfigurationsInput)
			if sdkaws.ToInt32(req.MaxResults) != 100 || req.FilterCriteria != nil || req.SortBy != "" || req.SortOrder != "" {
				t.Errorf("ListCisScanConfigurations input = %+v; want MaxResults 100 and no filter", req)
			}
			switch sv(req.NextToken) {
			case "":
				return &inspector2.ListCisScanConfigurationsOutput{
					ScanConfigurations: []i2types.CisScanConfiguration{i2Cis("c1", i2TestTags), {ScanName: sdkaws.String("cis-noarn")}},
					NextToken:          sdkaws.String("p2"),
				}, nil
			case "p2":
				return &inspector2.ListCisScanConfigurationsOutput{ScanConfigurations: []i2types.CisScanConfiguration{i2Cis("c2", nil)}}, nil
			}
			t.Errorf("ListCisScanConfigurations token = %q", sv(req.NextToken))
			return &inspector2.ListCisScanConfigurationsOutput{}, nil
		},
		"ListMembers": func(any) (any, error) {
			return &inspector2.ListMembersOutput{Members: []i2types.Member{{AccountId: sdkaws.String("222222222222")}}}, nil
		},
		"ListCodeSecurityIntegrations": func(any) (any, error) {
			return &inspector2.ListCodeSecurityIntegrationsOutput{
				Integrations: []i2types.CodeSecurityIntegrationSummary{{IntegrationArn: arn("codesecurity-integration")}},
			}, nil
		},
		"ListCodeSecurityScanConfigurations": func(any) (any, error) {
			return &inspector2.ListCodeSecurityScanConfigurationsOutput{
				Configurations: []i2types.CodeSecurityScanConfigurationSummary{{ScanConfigurationArn: arn("codesecurity-configuration")}},
			}, nil
		},
		"ListConnectors": func(any) (any, error) {
			return &inspector2.ListConnectorsOutput{Items: []i2types.Connector{i2Connector("c1")}}, nil
		},
		"ListDelegatedAdminAccounts": func(any) (any, error) {
			return &inspector2.ListDelegatedAdminAccountsOutput{
				DelegatedAdminAccounts: []i2types.DelegatedAdminAccount{i2DelegatedAdmin(testI2Admin)},
			}, nil
		},
	}
}

// i2AllRows is what i2Handlers stores: two filters, two CIS scan
// configurations and one row of every other type.
const i2AllRows = 9

// i2Order is the order scanInspector2 calls its ops in.
var i2Order = []string{
	"ListFilters", "ListMembers", "ListCisScanConfigurations", "ListCodeSecurityIntegrations",
	"ListCodeSecurityScanConfigurations", "ListConnectors", "ListDelegatedAdminAccounts",
}

// i2TagOps are the listings whose elements carry tags.
var i2TagOps = []struct {
	op, label, typ, page1Native, skippedKey string
}{
	{"ListFilters", "inspector2:ListFilters", TypeInspector2Filter, i2FilterARN("f1"), "flt-noarn"},
	{"ListCisScanConfigurations", "inspector2:ListCisScanConfigurations", TypeInspector2CisScanConfiguration, i2CisARN("c1"), "cis-noarn"},
}

type i2Scan struct {
	total, inserted int
	err             error
	warnings        []store.ScanWarning
	called          []string
}

// opWarns is the messages of the warnings reported under op.
func (r i2Scan) opWarns(op string) []string {
	var msgs []string
	for _, w := range r.warnings {
		if w.Service == op {
			msgs = append(msgs, w.Message)
		}
	}
	return msgs
}

// runInspector2 runs scanInspector2 against handlers, recording the warnings
// and the ops called, in first-call order.
func runInspector2(ctx context.Context, t *testing.T, st *store.Store, handlers map[string]func(context.Context, any) (any, error)) i2Scan {
	t.Helper()
	var (
		r  i2Scan
		mu sync.Mutex
	)
	st.OnWarn = func(w store.ScanWarning) { r.warnings = append(r.warnings, w) }
	wrapped := map[string]func(context.Context, any) (any, error){}
	for op, fn := range handlers {
		wrapped[op] = func(ctx context.Context, in any) (any, error) {
			mu.Lock()
			if !slices.Contains(r.called, op) {
				r.called = append(r.called, op)
			}
			mu.Unlock()
			return fn(ctx, in)
		}
	}
	acct := newTestAccount(testAccountID)
	acct.cfg = opStubConfigCtx(wrapped, errors.New("unexpected op"))
	r.total, r.inserted, r.err = scanInspector2(ctx, acct, testRegion, st, testScanID)
	return r
}

func runInspector2Plain(t *testing.T, st *store.Store, handlers map[string]func(any) (any, error)) i2Scan {
	t.Helper()
	return runInspector2(context.Background(), t, st, ignoreCtx(handlers))
}

func TestScanInspector2_StoresFiltersAndCisConfigsWithTags(t *testing.T) {
	st := newTestStore(t)
	r := runInspector2Plain(t, st, i2Handlers(t))
	if r.err != nil {
		t.Fatalf("err = %v", r.err)
	}
	if r.total != i2AllRows || r.inserted != i2AllRows {
		t.Errorf("total, inserted = %d, %d; want %d, %d", r.total, r.inserted, i2AllRows, i2AllRows)
	}
	for _, o := range i2TagOps {
		if msgs := r.opWarns(o.label); len(msgs) != 1 || !strings.Contains(msgs[0], o.skippedKey) {
			t.Errorf("%s warnings = %q; want one naming %q", o.label, msgs, o.skippedKey)
		}
	}
	if len(r.warnings) != len(i2TagOps) {
		t.Errorf("warnings = %+v; want one per tagged listing", r.warnings)
	}

	rows := []struct {
		typ, native, name string
		element           any
		tags              map[string]string
	}{
		{TypeInspector2Filter, i2FilterARN("f1"), "flt-f1", i2Filter("f1", i2TestTags), i2TestTags},
		{TypeInspector2Filter, i2FilterARN("f2"), "flt-f2", i2Filter("f2", nil), nil},
		{TypeInspector2CisScanConfiguration, i2CisARN("c1"), "cis-c1", i2Cis("c1", i2TestTags), i2TestTags},
		{TypeInspector2CisScanConfiguration, i2CisARN("c2"), "cis-c2", i2Cis("c2", nil), nil},
	}
	for _, w := range rows {
		got, ok := rowsByNativeID(t, st, w.typ)[w.native]
		if !ok {
			t.Errorf("%s row %s missing", w.typ, w.native)
			continue
		}
		if sv(got.Name) != w.name || sv(got.Region) != testRegion || got.Status != nil {
			t.Errorf("%s = name %q region %q status %v; want %s, %s, nil", w.native, sv(got.Name), sv(got.Region), got.Status, w.name, testRegion)
		}
		assertJSONEqual(t, w.native+" attributes", got.AttributesJSON, mustJSON(w.element))
		if w.tags != nil {
			assertTagsMap(t, got.TagsJSON, w.tags)
		} else if got.TagsJSON != nil {
			t.Errorf("%s tags = %s; want none", w.native, *got.TagsJSON)
		}
	}
}

// A denied later page keeps the pages read before it and is warned about.
func TestScanInspector2_DeniedOnSecondPageKeepsFirst(t *testing.T) {
	denied := apiErr("AccessDeniedException", "User is not authorized to perform: inspector2:List")
	for _, o := range i2TagOps {
		t.Run(o.op, func(t *testing.T) {
			st := newTestStore(t)
			h := i2Handlers(t)
			failPage2(h, o.op, denied)
			r := runInspector2Plain(t, st, h)
			if r.err != nil {
				t.Fatalf("err = %v; want nil", r.err)
			}
			if r.total != i2AllRows-1 || r.inserted != i2AllRows-1 {
				t.Errorf("total, inserted = %d, %d; want %d", r.total, r.inserted, i2AllRows-1)
			}
			// The denial, and the first page's element without an ARN.
			if got := r.opWarns(o.label); len(got) != 2 {
				t.Errorf("%s warnings = %q; want 2", o.label, got)
			}
			if got := nativeIDs(rowsByNativeID(t, st, o.typ)); !slices.Equal(got, []string{o.page1Native}) {
				t.Errorf("%s rows = %v; want only %s", o.typ, got, o.page1Native)
			}
		})
	}
}

// A later page's error is returned after the pages read before it are
// stored, and every later phase still runs.
func TestScanInspector2_ErrorOnSecondPageKeepsFirstAndLaterPhases(t *testing.T) {
	cases := []struct {
		name    string
		err     error
		outcome serviceOutcome
	}{
		{"hard", apiErr("ValidationException", "boom"), outcomeError},
		{"transient", apiErr("ThrottlingException", "Rate exceeded"), outcomeTransient},
	}
	for _, o := range i2TagOps {
		for _, c := range cases {
			t.Run(o.op+"/"+c.name, func(t *testing.T) {
				st := newTestStore(t)
				h := i2Handlers(t)
				failPage2(h, o.op, c.err)
				r := runInspector2Plain(t, st, h)
				if got := classifyServiceError(r.err); got != c.outcome {
					t.Fatalf("classifyServiceError(%v) = %v; want %v", r.err, got, c.outcome)
				}
				if r.total != i2AllRows-1 || r.inserted != i2AllRows-1 {
					t.Errorf("total, inserted = %d, %d; want %d", r.total, r.inserted, i2AllRows-1)
				}
				if !slices.Equal(r.called, i2Order) {
					t.Errorf("ops called = %v; want %v", r.called, i2Order)
				}
				if got := nativeIDs(rowsByNativeID(t, st, o.typ)); !slices.Equal(got, []string{o.page1Native}) {
					t.Errorf("%s rows = %v; want only %s", o.typ, got, o.page1Native)
				}
			})
		}
	}
}

// A first-page error of either listing is returned, and every other phase
// still stores its rows.
func TestScanInspector2_FirstPageErrorRunsOtherPhases(t *testing.T) {
	boom := apiErr("ValidationException", "boom")
	for _, o := range i2TagOps {
		t.Run(o.op, func(t *testing.T) {
			st := newTestStore(t)
			h := i2Handlers(t)
			h[o.op] = func(any) (any, error) { return nil, boom }
			r := runInspector2Plain(t, st, h)
			if !errors.Is(r.err, boom) {
				t.Fatalf("err = %v; want the %s error", r.err, o.op)
			}
			if want := i2AllRows - 2; r.total != want || r.inserted != want {
				t.Errorf("total, inserted = %d, %d; want %d", r.total, r.inserted, want)
			}
			if !slices.Equal(r.called, i2Order) {
				t.Errorf("ops called = %v; want %v", r.called, i2Order)
			}
		})
	}
}

// A not-enabled CIS listing skips the code-security and connector phases;
// the delegated-admin phase still runs and the other phases' rows keep their
// counts.
func TestScanInspector2_CisNotEnabledSkipsEnabledOnlyPhases(t *testing.T) {
	st := newTestStore(t)
	h := i2Handlers(t)
	h["ListCisScanConfigurations"] = func(any) (any, error) {
		return nil, apiErr("AccessDeniedException", "Invoking account is not enabled")
	}
	r := runInspector2Plain(t, st, h)
	if r.err != nil {
		t.Fatalf("err = %v; want nil", r.err)
	}
	want := []string{"ListFilters", "ListMembers", "ListCisScanConfigurations", "ListDelegatedAdminAccounts"}
	if !slices.Equal(r.called, want) {
		t.Errorf("ops called = %v; want %v", r.called, want)
	}
	if r.total != 4 || r.inserted != 4 {
		t.Errorf("total, inserted = %d, %d; want 4 (filters, member, delegated admin)", r.total, r.inserted)
	}
	if got := r.opWarns("inspector2:ListCisScanConfigurations"); len(got) != 0 {
		t.Errorf("CIS warnings = %q; want none", got)
	}
}

// The not-enabled denial on a later CIS page is a denial: the first page is
// kept, it is warned about, and no phase is skipped.
func TestScanInspector2_CisNotEnabledOnSecondPageIsDenial(t *testing.T) {
	st := newTestStore(t)
	h := i2Handlers(t)
	failPage2(h, "ListCisScanConfigurations", apiErr("AccessDeniedException", "Invoking account is not enabled"))
	r := runInspector2Plain(t, st, h)
	if r.err != nil {
		t.Fatalf("err = %v; want nil", r.err)
	}
	if r.total != i2AllRows-1 || r.inserted != i2AllRows-1 {
		t.Errorf("total, inserted = %d, %d; want %d", r.total, r.inserted, i2AllRows-1)
	}
	if !slices.Equal(r.called, i2Order) {
		t.Errorf("ops called = %v; want %v", r.called, i2Order)
	}
	if got := r.opWarns("inspector2:ListCisScanConfigurations"); len(got) != 2 {
		t.Errorf("CIS warnings = %q; want the denial and the element without an ARN", got)
	}
}

// Another phase's failure is returned rather than hidden behind the
// not-enabled sentinel, even when no row was stored.
func TestScanInspector2_SentinelDoesNotHidePhaseError(t *testing.T) {
	st := newTestStore(t)
	boom := apiErr("ValidationException", "boom")
	h := i2Handlers(t)
	h["ListFilters"] = func(any) (any, error) { return nil, boom }
	h["ListMembers"] = func(any) (any, error) { return &inspector2.ListMembersOutput{}, nil }
	h["ListCisScanConfigurations"] = func(any) (any, error) {
		return nil, apiErr("AccessDeniedException", "Invoking account is not enabled")
	}
	h["ListDelegatedAdminAccounts"] = func(any) (any, error) { return &inspector2.ListDelegatedAdminAccountsOutput{}, nil }
	r := runInspector2Plain(t, st, h)
	if !errors.Is(r.err, boom) || errors.Is(r.err, errServiceDisabled) {
		t.Fatalf("err = %v; want the ListFilters error without the sentinel", r.err)
	}
	if r.total != 0 || r.inserted != 0 {
		t.Errorf("total, inserted = %d, %d; want 0", r.total, r.inserted)
	}
}

// A rescan whose rows are all already stored inserts none, but they were still
// listed: the not-enabled sentinel must not discard that real total.
func TestScanInspector2_SentinelDroppedOnRescanOfStoredRows(t *testing.T) {
	st := newTestStore(t)
	h := i2Handlers(t)
	h["ListMembers"] = func(any) (any, error) { return &inspector2.ListMembersOutput{}, nil }
	h["ListCisScanConfigurations"] = func(any) (any, error) {
		return nil, apiErr("AccessDeniedException", "Invoking account is not enabled")
	}
	h["ListDelegatedAdminAccounts"] = func(any) (any, error) { return &inspector2.ListDelegatedAdminAccountsOutput{}, nil }
	if r := runInspector2Plain(t, st, h); r.err != nil || r.total != 2 || r.inserted != 2 {
		t.Fatalf("first scan = total %d inserted %d err %v; want 2, 2, nil", r.total, r.inserted, r.err)
	}
	r := runInspector2Plain(t, st, h)
	if r.err != nil {
		t.Fatalf("rescan err = %v; want nil", r.err)
	}
	if r.total != 2 || r.inserted != 0 {
		t.Errorf("rescan total, inserted = %d, %d; want 2, 0", r.total, r.inserted)
	}
}

// A failed write stops the scan: no later phase runs, and no warning claims
// rows the write lost.
func TestScanInspector2_StoreWriteStops(t *testing.T) {
	for _, o := range i2TagOps {
		t.Run(o.op, func(t *testing.T) {
			st := newTestStore(t)
			refuseStoreWrites(t, st, o.typ)
			r := runInspector2Plain(t, st, i2Handlers(t))
			if !errors.Is(r.err, store.ErrStoreWrite) {
				t.Fatalf("err = %v; want the store write failure", r.err)
			}
			if got := r.opWarns(o.label); len(got) != 0 {
				t.Errorf("%s warnings = %q; want none after a failed write", o.label, got)
			}
			want := i2Order[:slices.Index(i2Order, o.op)+1]
			if !slices.Equal(r.called, want) {
				t.Errorf("ops called = %v; want only %v", r.called, want)
			}
		})
	}
}

// A ctx that ends mid-listing stops it: the stop is returned and the page read
// before it is stored.
func TestScanInspector2_ContextStopReturned(t *testing.T) {
	st := newTestStore(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	handlers := map[string]func(context.Context, any) (any, error){}
	for op, fn := range i2Handlers(t) {
		handlers[op] = func(ctx context.Context, in any) (any, error) {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			out, err := fn(in)
			if op == "ListFilters" {
				cancel()
			}
			return out, err
		}
	}
	r := runInspector2(ctx, t, st, handlers)
	if !errors.Is(r.err, context.Canceled) {
		t.Fatalf("err = %v; want context.Canceled", r.err)
	}
	if r.total != 1 || r.inserted != 1 {
		t.Errorf("total, inserted = %d, %d; want 1", r.total, r.inserted)
	}
	if got := nativeIDs(rowsByNativeID(t, st, TypeInspector2Filter)); !slices.Equal(got, []string{i2FilterARN("f1")}) {
		t.Errorf("filters = %v; want only the first page's %s", got, i2FilterARN("f1"))
	}
	if n := len(rowsByNativeID(t, st, TypeInspector2CisScanConfiguration)); n != 0 {
		t.Errorf("CIS rows = %d after ctx ended; want 0", n)
	}
}

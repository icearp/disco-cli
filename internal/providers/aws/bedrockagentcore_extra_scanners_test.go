package aws

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	bacdata "github.com/aws/aws-sdk-go-v2/service/bedrockagentcore"
	bacdatatypes "github.com/aws/aws-sdk-go-v2/service/bedrockagentcore/types"
	bactypes "github.com/aws/aws-sdk-go-v2/service/bedrockagentcorecontrol/types"
	"github.com/icearp/disco-cli/store"
)

// stubBACData implements bedrockAgentCoreDataAPI with the same paging
// convention as stubBAC (see stubPage).
type stubBACData struct {
	abPages [][]bacdatatypes.ABTestSummary
	abErr   error
}

func (s *stubBACData) ListABTests(_ context.Context, in *bacdata.ListABTestsInput, _ ...func(*bacdata.Options)) (*bacdata.ListABTestsOutput, error) {
	items, next, err := stubPage(s.abPages, in.NextToken, s.abErr)
	if err != nil {
		return nil, err
	}
	return &bacdata.ListABTestsOutput{AbTests: items, NextToken: next}, nil
}

var bacTestTime = time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)

// bacWant is the stored row a scanner must produce for one listed element.
type bacWant struct {
	nativeID, rtype, name, status, attrKey, attrVal string
	createdAt                                       bool
}

func assertBACRow(t *testing.T, st *store.Store, w bacWant) {
	t.Helper()
	r, err := st.GetResource(store.ResourceID("aws", testAccountID, w.nativeID))
	if err != nil {
		t.Fatalf("row %s missing: %v", w.nativeID, err)
	}
	if r.Type != w.rtype {
		t.Errorf("%s Type = %q; want %q", w.nativeID, r.Type, w.rtype)
	}
	if sv(r.Region) != testRegion {
		t.Errorf("%s Region = %q; want %q", w.nativeID, sv(r.Region), testRegion)
	}
	if sv(r.Name) != w.name {
		t.Errorf("%s Name = %q; want %q", w.nativeID, sv(r.Name), w.name)
	}
	if sv(r.Status) != w.status {
		t.Errorf("%s Status = %q; want %q", w.nativeID, sv(r.Status), w.status)
	}
	if w.createdAt && sv(r.CreatedAt) != sv(tp(&bacTestTime)) {
		t.Errorf("%s CreatedAt = %q; want %q", w.nativeID, sv(r.CreatedAt), sv(tp(&bacTestTime)))
	}
	var attrs map[string]any
	if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
		t.Fatalf("%s attrs: %v", w.nativeID, err)
	}
	if got, _ := attrs[w.attrKey].(string); got != w.attrVal {
		t.Errorf("%s attrs[%s] = %q; want %q", w.nativeID, w.attrKey, got, w.attrVal)
	}
}

func countWarnings(st *store.Store) *int {
	n := 0
	st.OnWarn = func(store.ScanWarning) { n++ }
	return &n
}

func bacCapacityProvider(id string) bactypes.CapacityProviderSummary {
	arn := bacARN(testRegion, testAccountID, "capacity-provider", id)
	if id == "" {
		arn = ""
	}
	return bactypes.CapacityProviderSummary{
		CapacityProviderArn: &arn, CapacityProviderId: sdkaws.String(id), Name: sdkaws.String("cp-" + id),
		Status: bactypes.CapacityProviderStatusReady,
	}
}

func bacConsentPortal(id string) bactypes.ConsentPortalSummary {
	arn := bacARN(testRegion, testAccountID, "consent-portal", id)
	if id == "" {
		arn = ""
	}
	return bactypes.ConsentPortalSummary{
		ConsentPortalArn: &arn, ConsentPortalId: sdkaws.String(id), Name: sdkaws.String("portal-" + id),
		Status: bactypes.ConsentPortalStatusActive, CreatedAt: &bacTestTime,
	}
}

func bacABTest(id string) bacdatatypes.ABTestSummary {
	arn := bacARN(testRegion, testAccountID, "ab-test", id)
	if id == "" {
		arn = ""
	}
	return bacdatatypes.ABTestSummary{
		AbTestArn: &arn, AbTestId: sdkaws.String(id), Name: sdkaws.String("ab-" + id),
		Status: bacdatatypes.ABTestStatusActive, CreatedAt: &bacTestTime,
	}
}

// Account-wide lists: both pages stored, an element without an ARN dropped,
// and each scanner's ARN, name, status and created time carried to the row.
func TestScanBACAccountLists_TwoPages(t *testing.T) {
	cases := []struct {
		name string
		run  func(st *store.Store) (int, int, error)
		want []bacWant
	}{
		{
			name: "capacity providers",
			run: func(st *store.Store) (int, int, error) {
				stub := &stubBAC{capacityPages: [][]bactypes.CapacityProviderSummary{
					{bacCapacityProvider("a"), bacCapacityProvider("")}, {bacCapacityProvider("b")},
				}}
				return scanBACCapacityProviders(context.Background(), stub, newTestAccount(testAccountID), testRegion, st, testScanID)
			},
			want: []bacWant{
				{bacARN(testRegion, testAccountID, "capacity-provider", "a"), TypeBedrockAgentCoreCapacityProvider, "cp-a", "READY", "CapacityProviderId", "a", false},
				{bacARN(testRegion, testAccountID, "capacity-provider", "b"), TypeBedrockAgentCoreCapacityProvider, "cp-b", "READY", "CapacityProviderId", "b", false},
			},
		},
		{
			name: "consent portals",
			run: func(st *store.Store) (int, int, error) {
				stub := &stubBAC{portalPages: [][]bactypes.ConsentPortalSummary{
					{bacConsentPortal("a"), bacConsentPortal("")}, {bacConsentPortal("b")},
				}}
				return scanBACConsentPortals(context.Background(), stub, newTestAccount(testAccountID), testRegion, st, testScanID)
			},
			want: []bacWant{
				{bacARN(testRegion, testAccountID, "consent-portal", "a"), TypeBedrockAgentCoreConsentPortal, "portal-a", "ACTIVE", "ConsentPortalId", "a", true},
				{bacARN(testRegion, testAccountID, "consent-portal", "b"), TypeBedrockAgentCoreConsentPortal, "portal-b", "ACTIVE", "ConsentPortalId", "b", true},
			},
		},
		{
			name: "ab tests",
			run: func(st *store.Store) (int, int, error) {
				stub := &stubBACData{abPages: [][]bacdatatypes.ABTestSummary{
					{bacABTest("a"), bacABTest("")}, {bacABTest("b")},
				}}
				return scanBACABTests(context.Background(), stub, newTestAccount(testAccountID), testRegion, st, testScanID)
			},
			want: []bacWant{
				{bacARN(testRegion, testAccountID, "ab-test", "a"), TypeBedrockAgentCoreABTest, "ab-a", "ACTIVE", "AbTestId", "a", true},
				{bacARN(testRegion, testAccountID, "ab-test", "b"), TypeBedrockAgentCoreABTest, "ab-b", "ACTIVE", "AbTestId", "b", true},
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			total, _, err := tc.run(st)
			if err != nil {
				t.Fatalf("scan: %v", err)
			}
			if total != len(tc.want) {
				t.Errorf("total = %d; want %d", total, len(tc.want))
			}
			for _, w := range tc.want {
				assertBACRow(t, st, w)
			}
		})
	}
}

// Account-wide lists route errors through bacListSkip: empty is not an error,
// a denial warns and yields nil, any other code propagates.
func TestScanBACAccountLists_Errors(t *testing.T) {
	scanners := map[string]func(st *store.Store, err error) (int, int, error){
		"capacity providers": func(st *store.Store, err error) (int, int, error) {
			return scanBACCapacityProviders(context.Background(), &stubBAC{capacityErr: err}, newTestAccount(testAccountID), testRegion, st, testScanID)
		},
		"consent portals": func(st *store.Store, err error) (int, int, error) {
			return scanBACConsentPortals(context.Background(), &stubBAC{portalErr: err}, newTestAccount(testAccountID), testRegion, st, testScanID)
		},
		"ab tests": func(st *store.Store, err error) (int, int, error) {
			return scanBACABTests(context.Background(), &stubBACData{abErr: err}, newTestAccount(testAccountID), testRegion, st, testScanID)
		},
	}
	cases := []struct {
		name      string
		err       error
		wantCode  string
		wantWarns int
	}{
		{"empty", nil, "", 0},
		{"access denied warns", apiErr("AccessDeniedException", "User: x is not authorized to perform: y"), "", 1},
		{"other error propagates", apiErr("ValidationException", "bad"), "ValidationException", 0},
	}
	for name, run := range scanners {
		for _, tc := range cases {
			t.Run(name+"/"+tc.name, func(t *testing.T) {
				st := newTestStore(t)
				warns := countWarnings(st)
				total, _, err := run(st, tc.err)
				if tc.wantCode == "" && err != nil {
					t.Fatalf("err = %v; want nil", err)
				}
				if tc.wantCode != "" && !isAPIErrorCode(err, tc.wantCode) {
					t.Fatalf("err = %v; want code %s", err, tc.wantCode)
				}
				if total != 0 {
					t.Errorf("total = %d; want 0", total)
				}
				if *warns != tc.wantWarns {
					t.Errorf("warnings = %d; want %d", *warns, tc.wantWarns)
				}
			})
		}
	}
}

func bacRule(id string) bactypes.GatewayRuleDetail {
	return bactypes.GatewayRuleDetail{
		RuleId: sdkaws.String(id), Priority: sdkaws.Int32(1),
		Status: bactypes.GatewayRuleStatusActive, CreatedAt: &bacTestTime,
	}
}

func bacRateLimit(id string) bactypes.GatewayRateLimitDetail {
	return bactypes.GatewayRateLimitDetail{
		RateLimitId: sdkaws.String(id), Status: bactypes.GatewayRateLimitStatusActive, CreatedAt: &bacTestTime,
	}
}

var bacTestGateways = []bacGateway{
	{id: "gw-a", arn: bacARN(testRegion, testAccountID, "gateway", "gw-a")},
	{id: "gw-b", arn: bacARN(testRegion, testAccountID, "gateway", "gw-b")},
	{id: "gw-c", arn: bacARN(testRegion, testAccountID, "gateway", "gw-c")},
}

// bacGatewayChildCase drives the two per-gateway fan-out scanners through the
// same scenarios: each gateway id maps to its pages of child ids and an
// optional error served after them.
type bacGatewayChildCase struct {
	name, seg, rtype, idKey string
	run                     func(st *store.Store, pages map[string][][]string, errs map[string]error) (int, int, error)
}

func bacGatewayChildCases() []bacGatewayChildCase {
	acct := newTestAccount(testAccountID)
	return []bacGatewayChildCase{
		{
			name: "rules", seg: "/rule/", rtype: TypeBedrockAgentCoreGatewayRule, idKey: "RuleId",
			run: func(st *store.Store, pages map[string][][]string, errs map[string]error) (int, int, error) {
				stub := &stubBAC{rulePages: map[string][][]bactypes.GatewayRuleDetail{}, ruleErr: errs}
				for gid, ps := range pages {
					for _, ids := range ps {
						var page []bactypes.GatewayRuleDetail
						for _, id := range ids {
							page = append(page, bacRule(id))
						}
						stub.rulePages[gid] = append(stub.rulePages[gid], page)
					}
				}
				return scanBACGatewayRules(context.Background(), stub, acct, testRegion, st, testScanID, bacTestGateways)
			},
		},
		{
			name: "rate limits", seg: "/rate-limit/", rtype: TypeBedrockAgentCoreGatewayRateLimit, idKey: "RateLimitId",
			run: func(st *store.Store, pages map[string][][]string, errs map[string]error) (int, int, error) {
				stub := &stubBAC{limitPages: map[string][][]bactypes.GatewayRateLimitDetail{}, limitErr: errs}
				for gid, ps := range pages {
					for _, ids := range ps {
						var page []bactypes.GatewayRateLimitDetail
						for _, id := range ids {
							page = append(page, bacRateLimit(id))
						}
						stub.limitPages[gid] = append(stub.limitPages[gid], page)
					}
				}
				return scanBACGatewayRateLimits(context.Background(), stub, acct, testRegion, st, testScanID, bacTestGateways)
			},
		},
	}
}

// Every gateway is queried, both pages of one gateway are stored, an element
// without an id is dropped, and a gateway deleted since it was listed
// (ResourceNotFoundException) is skipped without failing its siblings.
func TestScanBACGatewayChildren_FanOut(t *testing.T) {
	for _, tc := range bacGatewayChildCases() {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := countWarnings(st)
			total, _, err := tc.run(st,
				map[string][][]string{"gw-a": {{"x1", ""}, {"x2"}}, "gw-c": {{"x3"}}},
				map[string]error{"gw-b": apiErr("ResourceNotFoundException", "gone")})
			if err != nil {
				t.Fatalf("scan: %v", err)
			}
			if total != 3 {
				t.Errorf("total = %d; want 3", total)
			}
			if *warns != 0 {
				t.Errorf("warnings = %d; want 0", *warns)
			}
			for _, c := range []struct{ gid, id string }{{"gw-a", "x1"}, {"gw-a", "x2"}, {"gw-c", "x3"}} {
				assertBACRow(t, st, bacWant{
					nativeID: bacARN(testRegion, testAccountID, "gateway", c.gid) + tc.seg + c.id,
					rtype:    tc.rtype, name: c.id, status: "ACTIVE", attrKey: tc.idKey, attrVal: c.id, createdAt: true,
				})
			}
		})
	}
}

// gw-c always serves x3, so a skipped gateway must not stop its siblings. In
// the "after a page" cases gw-a serves x1 before failing, and that row must
// be kept.
func TestScanBACGatewayChildren_Errors(t *testing.T) {
	deny := apiErr("AccessDeniedException", "User: x is not authorized to perform: y")
	cases := []struct {
		name      string
		pagesA    [][]string
		errs      map[string]error
		wantCode  string
		wantWarns int
		wantIDs   []string
	}{
		{"no gateways have other children", nil, nil, "", 0, []string{"x3"}},
		{"denial warns once and siblings still run", nil, map[string]error{"gw-a": deny, "gw-b": deny}, "", 1, []string{"x3"}},
		{"denial after a page keeps that page", [][]string{{"x1"}}, map[string]error{"gw-a": deny}, "", 1, []string{"x1", "x3"}},
		{"region gap skips silently", nil, map[string]error{"gw-a": apiErr("UnknownOperationException", "gap"), "gw-b": apiErr("AuthorizerConfigurationException", "gap")}, "", 0, []string{"x3"}},
		{"region gap after a page keeps that page", [][]string{{"x1"}}, map[string]error{"gw-a": apiErr("UnknownOperationException", "gap")}, "", 0, []string{"x1", "x3"}},
		{"other error propagates", nil, map[string]error{"gw-a": apiErr("ValidationException", "bad")}, "ValidationException", 0, nil},
	}
	for _, sc := range bacGatewayChildCases() {
		for _, tc := range cases {
			t.Run(sc.name+"/"+tc.name, func(t *testing.T) {
				st := newTestStore(t)
				warns := countWarnings(st)
				pages := map[string][][]string{"gw-c": {{"x3"}}}
				if tc.pagesA != nil {
					pages["gw-a"] = tc.pagesA
				}
				total, _, err := sc.run(st, pages, tc.errs)
				if tc.wantCode == "" && err != nil {
					t.Fatalf("err = %v; want nil", err)
				}
				if tc.wantCode != "" && !isAPIErrorCode(err, tc.wantCode) {
					t.Fatalf("err = %v; want code %s", err, tc.wantCode)
				}
				if total != len(tc.wantIDs) {
					t.Errorf("total = %d; want %d", total, len(tc.wantIDs))
				}
				if *warns != tc.wantWarns {
					t.Errorf("warnings = %d; want %d", *warns, tc.wantWarns)
				}
				gid := map[string]string{"x1": "gw-a", "x3": "gw-c"}
				for _, id := range tc.wantIDs {
					nativeID := bacARN(testRegion, testAccountID, "gateway", gid[id]) + sc.seg + id
					if _, err := st.GetResource(store.ResourceID("aws", testAccountID, nativeID)); err != nil {
						t.Errorf("row %s missing: %v", nativeID, err)
					}
				}
			})
		}
	}
}

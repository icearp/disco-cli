package aws

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net"
	"net/http"
	"reflect"
	"slices"
	"sync"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/securitylake"
	sltypes "github.com/aws/aws-sdk-go-v2/service/securitylake/types"
	smithyhttp "github.com/aws/smithy-go/transport/http"
	"github.com/icearp/disco-cli/store"
)

// TestIsSecurityLakeNotEnabled pins the three phrasings AWS uses for one
// account state, and the denials that must keep warning.
//
// The account-state cases are not hypothetical: with Security Lake off
// everywhere, ListSubscribers answered the delegated-administrator phrasing in
// us-east-1 / us-east-2 / eu-west-1 / ap-northeast-1 and
// ResourceNotFoundException in eu-west-2. Only the access-denied variants were
// matched, so that single region turned an account-level fact into a hard scan
// error and marked every scan partial.
func TestIsSecurityLakeNotEnabled(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		// The eu-west-2 shape. A ResourceNotFoundException, not an access
		// denial, which is why the access-denied check cannot gate every branch.
		{
			"not enabled in any region (ResourceNotFoundException)",
			apiErr("ResourceNotFoundException", "The request failed because Security Lake isn't enabled for your account in any Regions. Enable Security Lake for your account and then try again."),
			true,
		},
		{
			// The full shape the SDK hands the scanner: the typed exception inside
			// a 404 transport response, inside the scanner's own fmt.Errorf wrap.
			"the same, as the SDK actually delivers it",
			fmt.Errorf("securitylake:ListSubscribers: %w", &smithyhttp.ResponseError{
				Response: &smithyhttp.Response{Response: &http.Response{StatusCode: 404}},
				Err:      apiErr("ResourceNotFoundException", "The request failed because Security Lake isn't enabled for your account in any Regions."),
			}),
			true,
		},

		// The two access-denied phrasings, unchanged by this fix.
		{"delegated administrator prerequisite", apiErr("AccessDeniedException", "To perform this operation, your account must be a delegated Security Lake administrator account or a standalone Security Lake account."), true},
		{"never onboarded", apiErr("AccessDeniedException", "Your account is not authorized to perform this operation"), true},
		{
			// Wrapped the way the SDK actually ships it, for the same reason the
			// ResourceNotFoundException case above is: a helper reading err.Error()
			// instead of the typed message passes the bare case and fails this one.
			"delegated administrator prerequisite, wrapped",
			fmt.Errorf("securitylake:ListDataLakes: %w", &smithyhttp.ResponseError{
				Response: &smithyhttp.Response{Response: &http.Response{StatusCode: 403}},
				Err:      apiErr("AccessDeniedException", "To perform this operation, your account must be a delegated Security Lake administrator account."),
			}),
			true,
		},

		// The negative half.
		{
			"real IAM denial names the action and must still warn",
			apiErr("AccessDeniedException", "User: arn:aws:iam::1:role/scanner is not authorized to perform: securitylake:ListSubscribers"),
			false,
		},
		{
			// ResourceNotFoundException is a common AWS code. Matching it on the
			// code alone would silently swallow a genuine missing-resource error,
			// which is a real gap the operator needs to see.
			"unrelated ResourceNotFoundException",
			apiErr("ResourceNotFoundException", "Subscriber not found: sub-123"),
			false,
		},
		{"unrelated error", errors.New("boom"), false},
		{"nil", nil, false},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := isSecurityLakeNotEnabled(c.err); got != c.want {
				t.Errorf("isSecurityLakeNotEnabled(%v) = %v, want %v", c.err, got, c.want)
			}
		})
	}
}

const (
	slTestLakeARN       = "arn:aws:securitylake:us-east-1:123456789012:data-lake/default"
	slTestSubscriberARN = "arn:aws:securitylake:us-east-1:123456789012:subscriber/s1"
	slTestMemberAccount = "210987654321"
)

var (
	slTestDenied = apiErr("AccessDeniedException", "User: arn:aws:iam::123456789012:user/x is not authorized to perform: securitylake:GetDataLakeSources")
	errSLTest    = errors.New("boom")

	slTestVPCFlowID = slTestLakeARN + "/source/" + testAccountID + "/VPC_FLOW"
	slTestCustomID  = slTestLakeARN + "/source/" + slTestMemberAccount + "/my-custom-source"
	slTestRoute53ID = slTestLakeARN + "/source/" + slTestMemberAccount + "/ROUTE53"
	// slTestMemberVPCFlowID shares its source name with slTestVPCFlowID, so
	// only the account tells their log source entries apart.
	slTestMemberVPCFlowID = slTestLakeARN + "/source/" + slTestMemberAccount + "/VPC_FLOW"
)

// slTestSources are the data lake sources slHandlers returns, by the NativeID
// each must be stored under: an AWS-native source collecting, a custom source
// misconfigured on page one, and an AWS-native source not collecting on page two.
var slTestSources = map[string]sltypes.DataLakeSource{
	slTestVPCFlowID: {
		Account: ptrStr(testAccountID), SourceName: ptrStr("VPC_FLOW"),
		SourceStatuses: []sltypes.DataLakeSourceStatus{{Resource: ptrStr("vpc-1"), Status: sltypes.SourceCollectionStatusCollecting}},
	},
	slTestCustomID: {
		Account: ptrStr(slTestMemberAccount), SourceName: ptrStr("my-custom-source"), EventClasses: []string{"NETWORK_ACTIVITY"},
		SourceStatuses: []sltypes.DataLakeSourceStatus{{Resource: ptrStr("s3://lake/ext/my-custom-source/"), Status: sltypes.SourceCollectionStatusMisconfigured}},
	},
	slTestMemberVPCFlowID: {
		Account: ptrStr(slTestMemberAccount), SourceName: ptrStr("VPC_FLOW"),
		SourceStatuses: []sltypes.DataLakeSourceStatus{{Resource: ptrStr("vpc-3"), Status: sltypes.SourceCollectionStatusCollecting}},
	},
	slTestRoute53ID: {
		Account: ptrStr(slTestMemberAccount), SourceName: ptrStr("ROUTE53"),
		SourceStatuses: []sltypes.DataLakeSourceStatus{{Resource: ptrStr("vpc-2"), Status: sltypes.SourceCollectionStatusNotCollecting}},
	},
}

// slTestLogSources are the ListLogSources entries slHandlers returns, by the
// NativeID of the data lake source row each must be embedded in: VPC_FLOW at
// two versions, the custom source with its provider and Glue ARNs, and ROUTE53.
var slTestLogSources = map[string][]slLogSource{
	slTestVPCFlowID: {
		{AwsLogSource: &sltypes.AwsLogSourceResource{SourceName: sltypes.AwsLogSourceNameVpcFlow, SourceVersion: ptrStr("1.0")}},
		{AwsLogSource: &sltypes.AwsLogSourceResource{SourceName: sltypes.AwsLogSourceNameVpcFlow, SourceVersion: ptrStr("2.0")}},
	},
	slTestCustomID: {{CustomLogSource: &sltypes.CustomLogSourceResource{
		SourceName: ptrStr("my-custom-source"), SourceVersion: ptrStr("v3"),
		Provider: &sltypes.CustomLogSourceProvider{
			RoleArn:  ptrStr("arn:aws:iam::210987654321:role/custom-writer"),
			Location: ptrStr("s3://lake/ext/my-custom-source/"),
		},
		Attributes: &sltypes.CustomLogSourceAttributes{
			CrawlerArn:  ptrStr("arn:aws:glue:us-east-1:123456789012:crawler/my-custom-source"),
			DatabaseArn: ptrStr("arn:aws:glue:us-east-1:123456789012:database/lake_db"),
			TableArn:    ptrStr("arn:aws:glue:us-east-1:123456789012:table/lake_db/my_custom_source"),
		},
	}}},
	slTestRoute53ID:       {{AwsLogSource: &sltypes.AwsLogSourceResource{SourceName: sltypes.AwsLogSourceNameRoute53, SourceVersion: ptrStr("2.0")}}},
	slTestMemberVPCFlowID: {{AwsLogSource: &sltypes.AwsLogSourceResource{SourceName: sltypes.AwsLogSourceNameVpcFlow, SourceVersion: ptrStr("latest")}}},
}

// slLogSourceMember wraps an slTestLogSources entry back into the SDK union.
func slLogSourceMember(e slLogSource) sltypes.LogSourceResource {
	if e.CustomLogSource != nil {
		return &sltypes.LogSourceResourceMemberCustomLogSource{Value: *e.CustomLogSource}
	}
	return &sltypes.LogSourceResourceMemberAwsLogSource{Value: *e.AwsLogSource}
}

// slLogSourcePages is slTestLogSources as two ListLogSources pages: the
// scanned account's sources, then the member account's.
func slLogSourcePages() [2]*securitylake.ListLogSourcesOutput {
	var own, member []sltypes.LogSourceResource
	for _, e := range slTestLogSources[slTestVPCFlowID] {
		own = append(own, slLogSourceMember(e))
	}
	for _, id := range []string{slTestCustomID, slTestRoute53ID, slTestMemberVPCFlowID} {
		for _, e := range slTestLogSources[id] {
			member = append(member, slLogSourceMember(e))
		}
	}
	return [2]*securitylake.ListLogSourcesOutput{
		{Sources: []sltypes.LogSource{{Account: ptrStr(testAccountID), Region: ptrStr(testRegion), Sources: own}}, NextToken: ptrStr("p2")},
		{Sources: []sltypes.LogSource{{Account: ptrStr(slTestMemberAccount), Region: ptrStr(testRegion), Sources: member}}},
	}
}

// slCalls records each GetDataLakeSources and ListLogSources call's page
// size. Handlers share it with the test, so it is locked.
type slCalls struct {
	mu            sync.Mutex
	maxResults    []int32
	logMaxResults []int32
}

func (c *slCalls) recordLogSources(in any) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.logMaxResults = append(c.logMaxResults, *in.(*securitylake.ListLogSourcesInput).MaxResults)
}

func (c *slCalls) logSourcesCalls() []int32 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]int32(nil), c.logMaxResults...)
}

func (c *slCalls) recordSources(in any) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.maxResults = append(c.maxResults, *in.(*securitylake.GetDataLakeSourcesInput).MaxResults)
}

func (c *slCalls) sourcesCalls() []int32 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]int32(nil), c.maxResults...)
}

// slHandlers answers ListDataLakes and ListSubscribers with one row each,
// GetDataLakeSources with slTestSources over two pages, and ListLogSources with
// slTestLogSources over two pages.
func slHandlers(calls *slCalls) map[string]func(any) (any, error) {
	return map[string]func(any) (any, error){
		"ListDataLakes": func(any) (any, error) {
			return &securitylake.ListDataLakesOutput{DataLakes: []sltypes.DataLakeResource{{
				DataLakeArn: ptrStr(slTestLakeARN), Region: ptrStr(testRegion), CreateStatus: sltypes.DataLakeStatusCompleted,
			}}}, nil
		},
		"ListSubscribers": func(any) (any, error) {
			return &securitylake.ListSubscribersOutput{Subscribers: []sltypes.SubscriberResource{{
				SubscriberArn: ptrStr(slTestSubscriberARN), SubscriberName: ptrStr("s1"),
			}}}, nil
		},
		"ListLogSources": func(in any) (any, error) {
			calls.recordLogSources(in)
			pages := slLogSourcePages()
			if in.(*securitylake.ListLogSourcesInput).NextToken == nil {
				return pages[0], nil
			}
			return pages[1], nil
		},
		"GetDataLakeSources": func(in any) (any, error) {
			calls.recordSources(in)
			if in.(*securitylake.GetDataLakeSourcesInput).NextToken == nil {
				return &securitylake.GetDataLakeSourcesOutput{
					DataLakeArn:     ptrStr(slTestLakeARN),
					DataLakeSources: []sltypes.DataLakeSource{slTestSources[slTestVPCFlowID], slTestSources[slTestCustomID]},
					NextToken:       ptrStr("p2"),
				}, nil
			}
			return &securitylake.GetDataLakeSourcesOutput{
				DataLakeArn:     ptrStr(slTestLakeARN),
				DataLakeSources: []sltypes.DataLakeSource{slTestSources[slTestRoute53ID], slTestSources[slTestMemberVPCFlowID]},
			}, nil
		},
	}
}

// failSecondLogSourcesPage makes the second ListLogSources page fail with err.
func failSecondLogSourcesPage(h map[string]func(any) (any, error), err error) {
	first := h["ListLogSources"]
	h["ListLogSources"] = func(in any) (any, error) {
		if in.(*securitylake.ListLogSourcesInput).NextToken != nil {
			return nil, err
		}
		return first(in)
	}
}

// slRowAttrs decodes a data lake source row's attributes.
func slRowAttrs(t *testing.T, r store.Resource) slDataLakeSourceAttrs {
	t.Helper()
	var a slDataLakeSourceAttrs
	if err := json.Unmarshal([]byte(r.AttributesJSON), &a); err != nil {
		t.Fatalf("row %q attributes: %v", r.NativeID, err)
	}
	return a
}

func scanSecurityLakeTopLevel(st *store.Store, handlers map[string]func(any) (any, error)) (int, int, error) {
	acct := newTestAccount(testAccountID)
	acct.cfg = opStubConfig(handlers, slTestDenied)
	return scanSecurityLake(context.Background(), acct, testRegion, st, testScanID)
}

// failSecondSourcesPage makes the second GetDataLakeSources page fail with err.
func failSecondSourcesPage(h map[string]func(any) (any, error), err error) {
	first := h["GetDataLakeSources"]
	h["GetDataLakeSources"] = func(in any) (any, error) {
		if in.(*securitylake.GetDataLakeSourcesInput).NextToken != nil {
			return nil, err
		}
		return first(in)
	}
}

func TestScanSecurityLake_StoresEveryDataLakeSource(t *testing.T) {
	st := newTestStore(t)
	calls := &slCalls{}
	var warnings []store.ScanWarning
	st.OnWarn = func(w store.ScanWarning) { warnings = append(warnings, w) }
	total, inserted, err := scanSecurityLakeTopLevel(st, slHandlers(calls))
	if err != nil {
		t.Fatalf("scanSecurityLake: %v", err)
	}
	if len(warnings) != 0 {
		t.Errorf("warnings = %+v, want none", warnings)
	}
	if got := calls.logSourcesCalls(); !reflect.DeepEqual(got, []int32{100, 100}) {
		t.Errorf("ListLogSources MaxResults per call = %v, want [100 100] (two pages at the model cap)", got)
	}
	// One data lake, one subscriber, four data lake sources.
	if total != 6 || inserted != 6 {
		t.Errorf("scanSecurityLake = (%d, %d), want (6, 6)", total, inserted)
	}
	if got := calls.sourcesCalls(); !reflect.DeepEqual(got, []int32{100, 100}) {
		t.Errorf("GetDataLakeSources MaxResults per call = %v, want [100 100] (two pages at the model cap)", got)
	}

	rows := rowsByNativeID(t, st, TypeSecurityLakeDataLakeSource)
	if len(rows) != len(slTestSources) {
		t.Errorf("data lake source rows = %d, want %d", len(rows), len(slTestSources))
	}
	for id, want := range slTestSources {
		r, ok := rows[id]
		if !ok {
			t.Errorf("no data lake source row %q", id)
			continue
		}
		if r.AccountID != testAccountID || sv(r.Region) != testRegion || sv(r.Name) != sv(want.SourceName) {
			t.Errorf("row %q = account %q region %q name %q; want %q %q %q",
				id, r.AccountID, sv(r.Region), sv(r.Name), testAccountID, testRegion, sv(want.SourceName))
		}
		got := slRowAttrs(t, r)
		if !reflect.DeepEqual(got.DataLakeSource, want) {
			t.Errorf("row %q attributes = %+v, want the SDK element %+v", id, got.DataLakeSource, want)
		}
		if !reflect.DeepEqual(got.LogSources, slTestLogSources[id]) {
			t.Errorf("row %q LogSources = %s, want %s", id, mustJSON(got.LogSources), mustJSON(slTestLogSources[id]))
		}
		assertContainsNative(t, st, slTestLakeARN, id)
	}
	assertContainsEdgesDeclared(t, st)
}

// A source whose data lake was not stored this scan is stored unlinked, with
// no warning about a missing hierarchy parent. When ListDataLakes answered
// without the data lake GetDataLakeSources named, one warning says so; when
// ListDataLakes could not be read, only its own denial is warned.
func TestScanSecurityLake_DataLakeSourceUnlinkedWithoutStoredLake(t *testing.T) {
	cases := []struct {
		name      string
		dataLakes func(any) (any, error)
		wantWarns map[string]int
	}{
		{
			"data lake not listed",
			func(any) (any, error) { return &securitylake.ListDataLakesOutput{}, nil },
			map[string]int{slDataLakeSourcesOp: 1},
		},
		{
			"data lakes not readable",
			func(any) (any, error) { return nil, slTestDenied },
			map[string]int{slDataLakesOp: 1},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			h := slHandlers(&slCalls{})
			h["ListDataLakes"] = c.dataLakes
			warnings := map[string]int{}
			st.OnWarn = func(w store.ScanWarning) { warnings[w.Service]++ }
			if _, _, err := scanSecurityLakeTopLevel(st, h); err != nil {
				t.Fatalf("scanSecurityLake: %v", err)
			}
			if !reflect.DeepEqual(warnings, c.wantWarns) {
				t.Errorf("warnings by op = %v, want %v", warnings, c.wantWarns)
			}
			if rows := rowsByNativeID(t, st, TypeSecurityLakeDataLakeSource); len(rows) != len(slTestSources) {
				t.Errorf("data lake source rows = %d, want %d", len(rows), len(slTestSources))
			}
			rels, err := st.RelationshipsFrom(store.ResourceID("aws", testAccountID, slTestLakeARN), store.RelContains)
			if err != nil {
				t.Fatalf("RelationshipsFrom: %v", err)
			}
			if len(rels) != 0 {
				t.Errorf("contains edges from an unstored data lake = %v, want none", rels)
			}
		})
	}
}

func TestSLDataLakeSourceNativeID_Unique(t *testing.T) {
	const otherLake = "arn:aws:securitylake:eu-west-1:123456789012:data-lake/default"
	if got, want := slDataLakeSourceNativeID(slTestLakeARN, testAccountID, "VPC_FLOW"), slTestVPCFlowID; got != want {
		t.Errorf("slDataLakeSourceNativeID = %q, want %q", got, want)
	}
	ids := map[string]string{}
	for _, c := range []struct{ name, lake, account, source string }{
		{"base", slTestLakeARN, testAccountID, "VPC_FLOW"},
		{"other source account", slTestLakeARN, slTestMemberAccount, "VPC_FLOW"},
		{"other source name", slTestLakeARN, testAccountID, "ROUTE53"},
		{"other region's data lake", otherLake, testAccountID, "VPC_FLOW"},
	} {
		id := slDataLakeSourceNativeID(c.lake, c.account, c.source)
		if prev, dup := ids[id]; dup {
			t.Errorf("%s and %s share NativeID %q", prev, c.name, id)
		}
		ids[id] = c.name
	}
}

// Entries disco cannot identify are reported in one warning and not stored;
// the identifiable siblings are stored.
func TestScanSecurityLake_UnidentifiableDataLakeSourcesWarn(t *testing.T) {
	st := newTestStore(t)
	h := slHandlers(&slCalls{})
	h["GetDataLakeSources"] = func(in any) (any, error) {
		if in.(*securitylake.GetDataLakeSourcesInput).NextToken == nil {
			return &securitylake.GetDataLakeSourcesOutput{
				DataLakeArn: ptrStr(slTestLakeARN),
				DataLakeSources: []sltypes.DataLakeSource{
					slTestSources[slTestVPCFlowID],
					{SourceName: ptrStr("SH_FINDINGS")},
					{Account: ptrStr(testAccountID)},
					slTestSources[slTestVPCFlowID],
				},
				NextToken: ptrStr("p2"),
			}, nil
		}
		// No data lake ARN: no id can be built for the page's source.
		return &securitylake.GetDataLakeSourcesOutput{DataLakeSources: []sltypes.DataLakeSource{slTestSources[slTestRoute53ID]}}, nil
	}
	warnings := countOpWarnings(st, slDataLakeSourcesOp)
	total, _, err := scanSecurityLakeTopLevel(st, h)
	if err != nil {
		t.Fatalf("scanSecurityLake: %v", err)
	}
	if total != 3 {
		t.Errorf("total = %d, want 3 (data lake, subscriber, one source)", total)
	}
	rows := rowsByNativeID(t, st, TypeSecurityLakeDataLakeSource)
	if _, ok := rows[slTestVPCFlowID]; !ok || len(rows) != 1 {
		t.Errorf("data lake source rows = %v, want only %q", rows, slTestVPCFlowID)
	}
	if *warnings != 1 {
		t.Errorf("%s warnings = %d, want 1", slDataLakeSourcesOp, *warnings)
	}
}

func TestScanSecurityLake_DataLakeSourcesEmpty(t *testing.T) {
	st := newTestStore(t)
	h := slHandlers(&slCalls{})
	h["GetDataLakeSources"] = func(any) (any, error) { return &securitylake.GetDataLakeSourcesOutput{}, nil }
	total, _, err := scanSecurityLakeTopLevel(st, h)
	if err != nil {
		t.Fatalf("scanSecurityLake: %v", err)
	}
	if total != 2 {
		t.Errorf("total = %d, want 2 (data lake, subscriber)", total)
	}
	if rows := rowsByNativeID(t, st, TypeSecurityLakeDataLakeSource); len(rows) != 0 {
		t.Errorf("data lake source rows = %v, want none", rows)
	}
}

// When the data lake listing shows Security Lake is not enabled, the scan
// reports the service disabled and makes no source call.
func TestScanSecurityLake_NotEnabledMakesNoSourcesCall(t *testing.T) {
	st := newTestStore(t)
	calls := &slCalls{}
	h := slHandlers(calls)
	h["ListDataLakes"] = func(any) (any, error) {
		return nil, apiErr("AccessDeniedException", "Your account is not authorized to perform this operation")
	}
	if _, _, err := scanSecurityLakeTopLevel(st, h); !errors.Is(err, errServiceDisabled) {
		t.Fatalf("err = %v, want errServiceDisabled", err)
	}
	if got := calls.sourcesCalls(); len(got) != 0 {
		t.Errorf("GetDataLakeSources calls = %d, want 0", len(got))
	}
}

func TestScanSecurityLake_DataLakeSourcesErrors(t *testing.T) {
	const firstPageRows = 2
	cases := []struct {
		name            string
		firstPage       bool
		err             error
		wantErr         error
		wantRows        int
		wantWarnings    int
		wantLogWarnings int
	}{
		// Every case whose listing is cut short has one more warning: the
		// ListLogSources entries of the sources it did not reach are counted.
		{"denied on first page warns", true, slTestDenied, nil, 0, 2, 0},
		{"denied on second page keeps first page", false, slTestDenied, nil, firstPageRows, 2, 0},
		{"not found on a later page counts unmatched log sources", false, apiErr("ResourceNotFoundException", "data lake not found"), nil, firstPageRows, 1, 0},
		// No data lake on the first page lists no sources, so every log source
		// is reported as unmatched instead of dropped unseen.
		{"not found on first page reports log sources", true, apiErr("ResourceNotFoundException", "data lake not found"), nil, 0, 0, 1},
		{"not enabled on first page reports log sources", true, apiErr("AccessDeniedException", "You must be a delegated Security Lake administrator account"), nil, 0, 0, 1},
		{"conflict warns", false, apiErr("ConflictException", "conflict"), nil, firstPageRows, 2, 0},
		{"other error is returned", false, errSLTest, errSLTest, firstPageRows, 1, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			h := slHandlers(&slCalls{})
			if c.firstPage {
				h["GetDataLakeSources"] = func(any) (any, error) { return nil, c.err }
			} else {
				failSecondSourcesPage(h, c.err)
			}
			var warnings, logWarnings int
			st.OnWarn = func(w store.ScanWarning) {
				switch w.Service {
				case slDataLakeSourcesOp:
					warnings++
				case slLogSourcesOp:
					logWarnings++
				}
			}
			_, _, err := scanSecurityLakeTopLevel(st, h)
			switch {
			case c.wantErr == nil && err != nil:
				t.Fatalf("err = %v, want nil", err)
			case c.wantErr != nil && !errors.Is(err, c.wantErr):
				t.Fatalf("err = %v, want %v", err, c.wantErr)
			case c.wantErr != nil && classifyServiceError(err) != outcomeError:
				t.Errorf("classifyServiceError(%v) = %v, want outcomeError", err, classifyServiceError(err))
			}
			if rows := rowsByNativeID(t, st, TypeSecurityLakeDataLakeSource); len(rows) != c.wantRows {
				t.Errorf("data lake source rows = %d, want %d", len(rows), c.wantRows)
			}
			if warnings != c.wantWarnings {
				t.Errorf("%s warnings = %d, want %d", slDataLakeSourcesOp, warnings, c.wantWarnings)
			}
			// The sources a cut-short listing missed are not reported as
			// unmatched under ListLogSources.
			if logWarnings != c.wantLogWarnings {
				t.Errorf("%s warnings = %d, want %d", slLogSourcesOp, logWarnings, c.wantLogWarnings)
			}
		})
	}
}

// A service repeating its page token ends the listing instead of looping.
func TestScanSecurityLake_DataLakeSourcesStopOnRepeatedToken(t *testing.T) {
	st := newTestStore(t)
	calls := &slCalls{}
	h := slHandlers(calls)
	h["GetDataLakeSources"] = func(in any) (any, error) {
		calls.recordSources(in)
		if len(calls.sourcesCalls()) > 2 {
			return nil, errSLTest
		}
		return &securitylake.GetDataLakeSourcesOutput{NextToken: ptrStr("same")}, nil
	}
	if _, _, err := scanSecurityLakeTopLevel(st, h); err != nil {
		t.Fatalf("scanSecurityLake: %v", err)
	}
	if got := len(calls.sourcesCalls()); got != 2 {
		t.Errorf("GetDataLakeSources calls = %d, want 2", got)
	}
}

// The data lake source phase runs after the older phases whatever they
// returned: its hard error leaves their rows stored, and theirs leaves its.
func TestScanSecurityLake_DataLakeSourcesPhaseIndependent(t *testing.T) {
	t.Run("sources error keeps older phases", func(t *testing.T) {
		st := newTestStore(t)
		h := slHandlers(&slCalls{})
		h["GetDataLakeSources"] = func(any) (any, error) { return nil, errSLTest }
		total, _, err := scanSecurityLakeTopLevel(st, h)
		if !errors.Is(err, errSLTest) {
			t.Fatalf("err = %v, want the GetDataLakeSources error", err)
		}
		if total != 2 {
			t.Errorf("total = %d, want 2", total)
		}
		for _, typ := range []string{TypeSecurityLakeDataLake, TypeSecurityLakeSubscriber} {
			if rows := rowsByNativeID(t, st, typ); len(rows) != 1 {
				t.Errorf("%s rows = %d, want 1", typ, len(rows))
			}
		}
	})
	for _, op := range []string{"ListDataLakes", "ListSubscribers"} {
		t.Run(op+" error keeps sources", func(t *testing.T) {
			st := newTestStore(t)
			h := slHandlers(&slCalls{})
			h[op] = func(any) (any, error) { return nil, errSLTest }
			if _, _, err := scanSecurityLakeTopLevel(st, h); !errors.Is(err, errSLTest) {
				t.Fatalf("err = %v, want the %s error", err, op)
			}
			if rows := rowsByNativeID(t, st, TypeSecurityLakeDataLakeSource); len(rows) != len(slTestSources) {
				t.Errorf("data lake source rows = %d, want %d", len(rows), len(slTestSources))
			}
		})
	}
}

// A phase's transient error is demoted to a warning, so it cannot make the
// dispatcher read the other side's hard error as transient.
func TestScanSecurityLake_TransientPhaseDoesNotMaskHardError(t *testing.T) {
	throttle := apiErr("ThrottlingException", "Rate exceeded")
	cases := []struct {
		name, transientOp, hardOp, warnOp string
		wantWarnings                      int
	}{
		{"throttled data lake phase", "ListDataLakes", "GetDataLakeSources", slDataLakesOp, 1},
		{"throttled older phase", "ListSubscribers", "GetDataLakeSources", slSubscribersOp, 1},
		// The throttle, and the count of log source entries the cut-short
		// listing left unmatched.
		{"throttled sources phase", "GetDataLakeSources", "ListSubscribers", slDataLakeSourcesOp, 2},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			h := slHandlers(&slCalls{})
			h[c.transientOp] = func(any) (any, error) { return nil, throttle }
			h[c.hardOp] = func(any) (any, error) { return nil, errSLTest }
			warnings := countOpWarnings(st, c.warnOp)
			_, _, err := scanSecurityLakeTopLevel(st, h)
			if !errors.Is(err, errSLTest) {
				t.Fatalf("err = %v, want the %s error", err, c.hardOp)
			}
			if got := classifyServiceError(err); got != outcomeError {
				t.Errorf("classifyServiceError(%v) = %v, want outcomeError", err, got)
			}
			if *warnings != c.wantWarnings {
				t.Errorf("%s warnings = %d, want %d", c.warnOp, *warnings, c.wantWarnings)
			}
		})
	}
}

// A throttled ListDataLakes is only a warning, so it does not skip the
// subscribers: they are listed and stored as after an access denial.
func TestScanSecurityLake_ThrottledDataLakesStillListsSubscribers(t *testing.T) {
	st := newTestStore(t)
	h := slHandlers(&slCalls{})
	h["ListDataLakes"] = func(any) (any, error) { return nil, apiErr("ThrottlingException", "Rate exceeded") }
	warnings := countOpWarnings(st, slDataLakesOp)
	if _, _, err := scanSecurityLakeTopLevel(st, h); err != nil {
		t.Fatalf("scanSecurityLake: %v", err)
	}
	if _, ok := rowsByNativeID(t, st, TypeSecurityLakeSubscriber)[slTestSubscriberARN]; !ok {
		t.Errorf("no subscriber row %q after a throttled ListDataLakes", slTestSubscriberARN)
	}
	if *warnings != 1 {
		t.Errorf("%s warnings = %d, want 1", slDataLakesOp, *warnings)
	}
}

// A custom source row carries the provider role, location and Glue ARNs that
// only ListLogSources returns, under LogSources[].CustomLogSource.
func TestScanSecurityLake_CustomSourceEnrichment(t *testing.T) {
	st := newTestStore(t)
	if _, _, err := scanSecurityLakeTopLevel(st, slHandlers(&slCalls{})); err != nil {
		t.Fatalf("scanSecurityLake: %v", err)
	}
	r, ok := rowsByNativeID(t, st, TypeSecurityLakeDataLakeSource)[slTestCustomID]
	if !ok {
		t.Fatalf("no data lake source row %q", slTestCustomID)
	}
	var attrs struct {
		SourceName string
		LogSources []struct {
			CustomLogSource struct {
				SourceVersion string
				Provider      struct{ RoleArn, Location string }
				Attributes    struct{ CrawlerArn, DatabaseArn, TableArn string }
			}
		}
	}
	if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
		t.Fatalf("attributes: %v", err)
	}
	want := slTestLogSources[slTestCustomID][0].CustomLogSource
	if attrs.SourceName != "my-custom-source" || len(attrs.LogSources) != 1 {
		t.Fatalf("attributes = %s, want top-level SourceName and one LogSources entry", r.AttributesJSON)
	}
	got := attrs.LogSources[0].CustomLogSource
	for field, pair := range map[string][2]string{
		"SourceVersion":          {got.SourceVersion, sv(want.SourceVersion)},
		"Provider.RoleArn":       {got.Provider.RoleArn, sv(want.Provider.RoleArn)},
		"Provider.Location":      {got.Provider.Location, sv(want.Provider.Location)},
		"Attributes.CrawlerArn":  {got.Attributes.CrawlerArn, sv(want.Attributes.CrawlerArn)},
		"Attributes.DatabaseArn": {got.Attributes.DatabaseArn, sv(want.Attributes.DatabaseArn)},
		"Attributes.TableArn":    {got.Attributes.TableArn, sv(want.Attributes.TableArn)},
	} {
		if pair[0] != pair[1] || pair[1] == "" {
			t.Errorf("LogSources[0].CustomLogSource.%s = %q, want %q", field, pair[0], pair[1])
		}
	}
}

// Every version ListLogSources lists for one source is embedded in its row.
func TestScanSecurityLake_EmbedsEveryLogSourceVersion(t *testing.T) {
	st := newTestStore(t)
	if _, _, err := scanSecurityLakeTopLevel(st, slHandlers(&slCalls{})); err != nil {
		t.Fatalf("scanSecurityLake: %v", err)
	}
	r, ok := rowsByNativeID(t, st, TypeSecurityLakeDataLakeSource)[slTestVPCFlowID]
	if !ok {
		t.Fatalf("no data lake source row %q", slTestVPCFlowID)
	}
	var versions []string
	for _, e := range slRowAttrs(t, r).LogSources {
		if e.AwsLogSource == nil {
			t.Fatalf("LogSources entry %s, want an AwsLogSource", mustJSON(e))
		}
		versions = append(versions, sv(e.AwsLogSource.SourceVersion))
	}
	slices.Sort(versions)
	if !reflect.DeepEqual(versions, []string{"1.0", "2.0"}) {
		t.Errorf("VPC_FLOW LogSources versions = %v, want [1.0 2.0]", versions)
	}
}

// A ListLogSources entry no data lake source row carries is reported in one
// warning; the matched entries are still embedded.
func TestScanSecurityLake_UnmatchedLogSourceWarns(t *testing.T) {
	st := newTestStore(t)
	h := slHandlers(&slCalls{})
	listed := h["ListLogSources"]
	h["ListLogSources"] = func(in any) (any, error) {
		out, err := listed(in)
		if in.(*securitylake.ListLogSourcesInput).NextToken != nil {
			page := *out.(*securitylake.ListLogSourcesOutput)
			page.Sources = append(page.Sources, sltypes.LogSource{
				Account: ptrStr(testAccountID), Region: ptrStr(testRegion),
				Sources: []sltypes.LogSourceResource{&sltypes.LogSourceResourceMemberAwsLogSource{
					Value: sltypes.AwsLogSourceResource{SourceName: sltypes.AwsLogSourceNameWaf, SourceVersion: ptrStr("2.0")},
				}},
			})
			return &page, err
		}
		return out, err
	}
	warnings := countOpWarnings(st, slLogSourcesOp)
	if _, _, err := scanSecurityLakeTopLevel(st, h); err != nil {
		t.Fatalf("scanSecurityLake: %v", err)
	}
	if *warnings != 1 {
		t.Errorf("%s warnings = %d, want 1", slLogSourcesOp, *warnings)
	}
	rows := rowsByNativeID(t, st, TypeSecurityLakeDataLakeSource)
	if len(rows) != len(slTestSources) {
		t.Errorf("data lake source rows = %d, want %d", len(rows), len(slTestSources))
	}
	if got := slRowAttrs(t, rows[slTestRoute53ID]).LogSources; !reflect.DeepEqual(got, slTestLogSources[slTestRoute53ID]) {
		t.Errorf("ROUTE53 LogSources = %s, want %s", mustJSON(got), mustJSON(slTestLogSources[slTestRoute53ID]))
	}
}

// A failed ListLogSources keeps every data lake source row, with LogSources
// null rather than a partial list; only an unmodeled error is returned.
func TestScanSecurityLake_LogSourcesErrorKeepsRows(t *testing.T) {
	cases := []struct {
		name         string
		firstPage    bool
		err          error
		wantErr      error
		wantWarnings int
	}{
		{"denied warns", true, apiErr("AccessDeniedException", "User: arn:aws:iam::123456789012:user/x is not authorized to perform: securitylake:ListLogSources"), nil, 1},
		{"not found is skipped silently", false, apiErr("ResourceNotFoundException", "data lake not found"), nil, 0},
		{"not enabled is skipped silently", true, apiErr("AccessDeniedException", "You must be a delegated Security Lake administrator account"), nil, 0},
		{"conflict warns", false, apiErr("ConflictException", "conflict"), nil, 1},
		{"other error is returned", false, errSLTest, errSLTest, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			h := slHandlers(&slCalls{})
			if c.firstPage {
				h["ListLogSources"] = func(any) (any, error) { return nil, c.err }
			} else {
				failSecondLogSourcesPage(h, c.err)
			}
			warnings := countOpWarnings(st, slLogSourcesOp)
			total, _, err := scanSecurityLakeTopLevel(st, h)
			switch {
			case c.wantErr == nil && err != nil:
				t.Fatalf("err = %v, want nil", err)
			case c.wantErr != nil && !errors.Is(err, c.wantErr):
				t.Fatalf("err = %v, want %v", err, c.wantErr)
			case c.wantErr != nil && classifyServiceError(err) != outcomeError:
				t.Errorf("classifyServiceError(%v) = %v, want outcomeError", err, classifyServiceError(err))
			}
			if total != 6 {
				t.Errorf("total = %d, want 6", total)
			}
			rows := rowsByNativeID(t, st, TypeSecurityLakeDataLakeSource)
			if len(rows) != len(slTestSources) {
				t.Errorf("data lake source rows = %d, want %d", len(rows), len(slTestSources))
			}
			for id, r := range rows {
				if got := slRowAttrs(t, r).LogSources; got != nil {
					t.Errorf("row %q LogSources = %s, want null", id, mustJSON(got))
				}
			}
			if *warnings != c.wantWarnings {
				t.Errorf("%s warnings = %d, want %d", slLogSourcesOp, *warnings, c.wantWarnings)
			}
		})
	}
}

// A source ListLogSources lists no entry for carries an empty LogSources list,
// which tells it apart from a failed listing (null).
func TestScanSecurityLake_NoLogSourceEntriesIsEmptyList(t *testing.T) {
	st := newTestStore(t)
	h := slHandlers(&slCalls{})
	h["ListLogSources"] = func(any) (any, error) { return &securitylake.ListLogSourcesOutput{}, nil }
	if _, _, err := scanSecurityLakeTopLevel(st, h); err != nil {
		t.Fatalf("scanSecurityLake: %v", err)
	}
	rows := rowsByNativeID(t, st, TypeSecurityLakeDataLakeSource)
	if len(rows) != len(slTestSources) {
		t.Errorf("data lake source rows = %d, want %d", len(rows), len(slTestSources))
	}
	for id, r := range rows {
		if got := slRowAttrs(t, r).LogSources; got == nil || len(got) != 0 {
			t.Errorf("row %q LogSources = %#v, want an empty non-null list", id, got)
		}
	}
}

// A service repeating its ListLogSources page token ends the listing instead
// of looping.
func TestScanSecurityLake_LogSourcesStopOnRepeatedToken(t *testing.T) {
	st := newTestStore(t)
	calls := &slCalls{}
	h := slHandlers(calls)
	h["ListLogSources"] = func(in any) (any, error) {
		calls.recordLogSources(in)
		if len(calls.logSourcesCalls()) > 2 {
			return nil, errSLTest
		}
		return &securitylake.ListLogSourcesOutput{NextToken: ptrStr("same")}, nil
	}
	if _, _, err := scanSecurityLakeTopLevel(st, h); err != nil {
		t.Fatalf("scanSecurityLake: %v", err)
	}
	if got := len(calls.logSourcesCalls()); got != 2 {
		t.Errorf("ListLogSources calls = %d, want 2", got)
	}
}

// A hard GetDataLakeSources error stays a hard error when ListLogSources fails
// transiently in the same scan; the transient is warned under its own op.
func TestScanSecurityLake_TransientLogSourcesDoesNotMaskSourcesError(t *testing.T) {
	st := newTestStore(t)
	h := slHandlers(&slCalls{})
	h["GetDataLakeSources"] = func(any) (any, error) { return nil, apiErr("BadRequestException", "bad") }
	h["ListLogSources"] = func(any) (any, error) { return nil, &net.OpError{Op: "dial", Err: errSLTest} }
	warnings := map[string]int{}
	st.OnWarn = func(w store.ScanWarning) { warnings[w.Service]++ }
	_, _, err := scanSecurityLakeTopLevel(st, h)
	if !isAPIErrorCode(err, "BadRequestException") {
		t.Fatalf("err = %v, want the GetDataLakeSources BadRequestException", err)
	}
	if got := classifyServiceError(err); got != outcomeError {
		t.Errorf("classifyServiceError(%v) = %v, want outcomeError", err, got)
	}
	if !reflect.DeepEqual(warnings, map[string]int{slLogSourcesOp: 1}) {
		t.Errorf("warnings by op = %v, want one under %s", warnings, slLogSourcesOp)
	}
}

// slAppendLogSource adds el to the second ListLogSources page.
func slAppendLogSource(h map[string]func(any) (any, error), el sltypes.LogSource) {
	listed := h["ListLogSources"]
	h["ListLogSources"] = func(in any) (any, error) {
		out, err := listed(in)
		if in.(*securitylake.ListLogSourcesInput).NextToken == nil {
			return out, err
		}
		page := *out.(*securitylake.ListLogSourcesOutput)
		page.Sources = append(append([]sltypes.LogSource{}, page.Sources...), el)
		return &page, err
	}
}

// slScanLogSourceWarnings scans with h and returns the ListLogSources warnings
// and the stored data lake source rows.
func slScanLogSourceWarnings(t *testing.T, h map[string]func(any) (any, error)) (int, map[string]store.Resource) {
	t.Helper()
	st := newTestStore(t)
	warnings := countOpWarnings(st, slLogSourcesOp)
	if _, _, err := scanSecurityLakeTopLevel(st, h); err != nil {
		t.Fatalf("scanSecurityLake: %v", err)
	}
	return *warnings, rowsByNativeID(t, st, TypeSecurityLakeDataLakeSource)
}

// An entry of a union member this SDK does not know is reported, not dropped
// unseen.
func TestScanSecurityLake_UnknownLogSourceMemberWarns(t *testing.T) {
	h := slHandlers(&slCalls{})
	slAppendLogSource(h, sltypes.LogSource{
		Account: ptrStr(testAccountID), Region: ptrStr(testRegion),
		Sources: []sltypes.LogSourceResource{&sltypes.UnknownUnionMember{Tag: "futureLogSource", Value: []byte("{}")}},
	})
	warnings, rows := slScanLogSourceWarnings(t, h)
	if warnings != 1 {
		t.Errorf("%s warnings = %d, want 1", slLogSourcesOp, warnings)
	}
	if got := slRowAttrs(t, rows[slTestVPCFlowID]).LogSources; !reflect.DeepEqual(got, slTestLogSources[slTestVPCFlowID]) {
		t.Errorf("VPC_FLOW LogSources = %s, want %s", mustJSON(got), mustJSON(slTestLogSources[slTestVPCFlowID]))
	}
}

// An element for another Region is skipped with a warning, not merged into
// this region's rows.
func TestScanSecurityLake_OtherRegionLogSourceSkipped(t *testing.T) {
	h := slHandlers(&slCalls{})
	slAppendLogSource(h, sltypes.LogSource{
		Account: ptrStr(testAccountID), Region: ptrStr("eu-west-1"),
		Sources: []sltypes.LogSourceResource{&sltypes.LogSourceResourceMemberAwsLogSource{
			Value: sltypes.AwsLogSourceResource{SourceName: sltypes.AwsLogSourceNameVpcFlow, SourceVersion: ptrStr("latest")},
		}},
	})
	warnings, rows := slScanLogSourceWarnings(t, h)
	if warnings != 1 {
		t.Errorf("%s warnings = %d, want 1", slLogSourcesOp, warnings)
	}
	if got := slRowAttrs(t, rows[slTestVPCFlowID]).LogSources; !reflect.DeepEqual(got, slTestLogSources[slTestVPCFlowID]) {
		t.Errorf("VPC_FLOW LogSources = %s, want only this region's %s", mustJSON(got), mustJSON(slTestLogSources[slTestVPCFlowID]))
	}
}

// An element without an account is filed under the scanning account, and one
// without a Region under the scanned region.
func TestScanSecurityLake_LogSourceWithoutAccountIsScanningAccounts(t *testing.T) {
	h := slHandlers(&slCalls{})
	entry := sltypes.AwsLogSourceResource{SourceName: sltypes.AwsLogSourceNameVpcFlow, SourceVersion: ptrStr("latest")}
	slAppendLogSource(h, sltypes.LogSource{
		Sources: []sltypes.LogSourceResource{&sltypes.LogSourceResourceMemberAwsLogSource{Value: entry}},
	})
	warnings, rows := slScanLogSourceWarnings(t, h)
	if warnings != 0 {
		t.Errorf("%s warnings = %d, want 0", slLogSourcesOp, warnings)
	}
	want := append(append([]slLogSource{}, slTestLogSources[slTestVPCFlowID]...), slLogSource{AwsLogSource: &entry})
	if got := slRowAttrs(t, rows[slTestVPCFlowID]).LogSources; !reflect.DeepEqual(got, want) {
		t.Errorf("VPC_FLOW LogSources = %s, want %s", mustJSON(got), mustJSON(want))
	}
}

// Each row carries its entries in a fixed order whatever order ListLogSources
// gives them in, so a reordered answer does not version the row.
func TestScanSecurityLake_LogSourcesOrderIsFixed(t *testing.T) {
	h := slHandlers(&slCalls{})
	listed := h["ListLogSources"]
	h["ListLogSources"] = func(in any) (any, error) {
		out, err := listed(in)
		page := *out.(*securitylake.ListLogSourcesOutput)
		page.Sources = nil
		for _, ls := range out.(*securitylake.ListLogSourcesOutput).Sources {
			ls.Sources = slices.Clone(ls.Sources)
			slices.Reverse(ls.Sources)
			page.Sources = append(page.Sources, ls)
		}
		return &page, err
	}
	_, rows := slScanLogSourceWarnings(t, h)
	for id, want := range slTestLogSources {
		if got := slRowAttrs(t, rows[id]).LogSources; !reflect.DeepEqual(got, want) {
			t.Errorf("row %q LogSources = %s, want %s", id, mustJSON(got), mustJSON(want))
		}
	}
}

// A listing cut short after page one still reports the unknown log source
// member and counts the entries it left unmatched, instead of dropping both
// with no warning.
func TestScanSecurityLake_CutShortListingReportsLogSources(t *testing.T) {
	st := newTestStore(t)
	h := slHandlers(&slCalls{})
	failSecondSourcesPage(h, apiErr("ResourceNotFoundException", "data lake not found"))
	slAppendLogSource(h, sltypes.LogSource{
		Account: ptrStr(testAccountID), Region: ptrStr(testRegion),
		Sources: []sltypes.LogSourceResource{&sltypes.UnknownUnionMember{Tag: "futureLogSource", Value: []byte("{}")}},
	})
	warnings := map[string]int{}
	st.OnWarn = func(w store.ScanWarning) { warnings[w.Service]++ }
	if _, _, err := scanSecurityLakeTopLevel(st, h); err != nil {
		t.Fatalf("scanSecurityLake: %v", err)
	}
	want := map[string]int{slLogSourcesOp: 1, slDataLakeSourcesOp: 1}
	if !reflect.DeepEqual(warnings, want) {
		t.Errorf("warnings by op = %v, want %v", warnings, want)
	}
}

// The subscribers' not-enabled answer is the service's state only when no
// other phase stored a row or failed; otherwise it is a warning, and a hard
// error elsewhere stays a hard error.
func TestScanSecurityLake_SubscribersNotEnabled(t *testing.T) {
	notEnabled := apiErr("AccessDeniedException", "You must be a delegated Security Lake administrator account")
	empty := map[string]func(any) (any, error){
		"ListDataLakes":      func(any) (any, error) { return &securitylake.ListDataLakesOutput{}, nil },
		"GetDataLakeSources": func(any) (any, error) { return &securitylake.GetDataLakeSourcesOutput{}, nil },
		"ListLogSources":     func(any) (any, error) { return &securitylake.ListLogSourcesOutput{}, nil },
	}
	cases := []struct {
		name         string
		overrides    map[string]func(any) (any, error)
		wantNilErr   bool
		wantOutcome  serviceOutcome
		wantWarnings int
	}{
		{"nothing else stored or failed", empty, false, outcomeDisabled, 0},
		{"rows stored elsewhere", map[string]func(any) (any, error){}, true, 0, 1},
		{"hard sources error", withSL(empty, "GetDataLakeSources", apiErr("BadRequestException", "bad")), false, outcomeError, 1},
		{"hard log sources error", withSL(empty, "ListLogSources", errSLTest), false, outcomeError, 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			h := slHandlers(&slCalls{})
			h["ListSubscribers"] = func(any) (any, error) { return nil, notEnabled }
			for op, fn := range c.overrides {
				h[op] = fn
			}
			warnings := countOpWarnings(st, slSubscribersOp)
			_, _, err := scanSecurityLakeTopLevel(st, h)
			switch {
			case c.wantNilErr && err != nil:
				t.Errorf("err = %v, want nil", err)
			case !c.wantNilErr && (err == nil || classifyServiceError(err) != c.wantOutcome):
				t.Errorf("err = %v, want one classified %v", err, c.wantOutcome)
			}
			if *warnings != c.wantWarnings {
				t.Errorf("%s warnings = %d, want %d", slSubscribersOp, *warnings, c.wantWarnings)
			}
		})
	}
}

// withSL returns a copy of handlers whose op fails with err.
func withSL(handlers map[string]func(any) (any, error), op string, err error) map[string]func(any) (any, error) {
	out := maps.Clone(handlers)
	out[op] = func(any) (any, error) { return nil, err }
	return out
}

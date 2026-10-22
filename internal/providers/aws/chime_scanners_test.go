package aws

import (
	"context"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	ci "github.com/aws/aws-sdk-go-v2/service/chimesdkidentity"
	citypes "github.com/aws/aws-sdk-go-v2/service/chimesdkidentity/types"
	cp "github.com/aws/aws-sdk-go-v2/service/chimesdkmediapipelines"
	cptypes "github.com/aws/aws-sdk-go-v2/service/chimesdkmediapipelines/types"
	cm "github.com/aws/aws-sdk-go-v2/service/chimesdkmessaging"
	cmtypes "github.com/aws/aws-sdk-go-v2/service/chimesdkmessaging/types"
	cv "github.com/aws/aws-sdk-go-v2/service/chimesdkvoice"
	cvtypes "github.com/aws/aws-sdk-go-v2/service/chimesdkvoice/types"
	"github.com/icearp/disco-cli/store"
)

const (
	chimeTestSMAARN    = "arn:aws:chime:" + testRegion + ":" + testAccountID + ":sma/sma-1"
	chimeTestGroupARN1 = "arn:aws:chime:" + testRegion + ":" + testAccountID + ":vcg/g-1"
	chimeTestGroupARN2 = "arn:aws:chime:" + testRegion + ":" + testAccountID + ":vcg/g-2"
)

var chimeTestCreated = time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC)

// chimeOpLog records the operations a stubbed scan called, in order.
type chimeOpLog struct {
	mu  sync.Mutex
	ops []string
}

func (l *chimeOpLog) add(op string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.ops = append(l.ops, op)
}

// chimeHandlers answers every chime list op the scanner calls: one SIP media
// application, two pages of SIP rules (the second with an id-less rule) and two
// pages of voice connector groups, everything else empty. Every call and its
// MaxResults is recorded.
func chimeHandlers(log *chimeOpLog, maxResults map[string]int32) map[string]func(in any) (any, error) {
	rec := func(op string, out any) func(in any) (any, error) {
		return func(any) (any, error) { log.add(op); return out, nil }
	}
	return map[string]func(in any) (any, error){
		"ListAppInstances":                         rec("ListAppInstances", &ci.ListAppInstancesOutput{}),
		"ListMediaPipelines":                       rec("ListMediaPipelines", &cp.ListMediaPipelinesOutput{}),
		"ListMediaInsightsPipelineConfigurations":  rec("ListMediaInsightsPipelineConfigurations", &cp.ListMediaInsightsPipelineConfigurationsOutput{}),
		"ListMediaPipelineKinesisVideoStreamPools": rec("ListMediaPipelineKinesisVideoStreamPools", &cp.ListMediaPipelineKinesisVideoStreamPoolsOutput{}),
		"ListSipMediaApplications": rec("ListSipMediaApplications", &cv.ListSipMediaApplicationsOutput{
			SipMediaApplications: []cvtypes.SipMediaApplication{{SipMediaApplicationArn: ptrStr(chimeTestSMAARN)}},
		}),
		"ListVoiceConnectors":     rec("ListVoiceConnectors", &cv.ListVoiceConnectorsOutput{}),
		"ListVoiceProfileDomains": rec("ListVoiceProfileDomains", &cv.ListVoiceProfileDomainsOutput{}),
		"ListSipRules": func(in any) (any, error) {
			log.add("ListSipRules")
			i := in.(*cv.ListSipRulesInput)
			maxResults["ListSipRules"] = sdkaws.ToInt32(i.MaxResults)
			if i.NextToken == nil {
				return &cv.ListSipRulesOutput{
					SipRules: []cvtypes.SipRule{{
						SipRuleId: ptrStr("rule-1"), Name: ptrStr("inbound"), CreatedTimestamp: &chimeTestCreated,
						TriggerType: cvtypes.SipRuleTriggerTypeToPhoneNumber, TriggerValue: ptrStr("+15555550100"),
						TargetApplications: []cvtypes.SipRuleTargetApplication{{SipMediaApplicationId: ptrStr("sma-1"), AwsRegion: ptrStr(testRegion)}},
					}},
					NextToken: ptrStr("p2"),
				}, nil
			}
			return &cv.ListSipRulesOutput{SipRules: []cvtypes.SipRule{{SipRuleId: ptrStr("rule-2")}, {Name: ptrStr("no-id")}}}, nil
		},
		"ListVoiceConnectorGroups": func(in any) (any, error) {
			log.add("ListVoiceConnectorGroups")
			i := in.(*cv.ListVoiceConnectorGroupsInput)
			maxResults["ListVoiceConnectorGroups"] = sdkaws.ToInt32(i.MaxResults)
			if i.NextToken == nil {
				return &cv.ListVoiceConnectorGroupsOutput{
					VoiceConnectorGroups: []cvtypes.VoiceConnectorGroup{{
						VoiceConnectorGroupArn: ptrStr(chimeTestGroupARN1), VoiceConnectorGroupId: ptrStr("g-1"),
						Name: ptrStr("failover"), CreatedTimestamp: &chimeTestCreated,
						VoiceConnectorItems: []cvtypes.VoiceConnectorItem{{VoiceConnectorId: ptrStr("vc-1"), Priority: sdkaws.Int32(1)}},
					}},
					NextToken: ptrStr("p2"),
				}, nil
			}
			return &cv.ListVoiceConnectorGroupsOutput{VoiceConnectorGroups: []cvtypes.VoiceConnectorGroup{
				{VoiceConnectorGroupArn: ptrStr(chimeTestGroupARN2), VoiceConnectorGroupId: ptrStr("g-2")},
			}}, nil
		},
	}
}

// runChime runs the registered chime scanner against handlers. An unstubbed op
// fails with a real IAM denial.
func runChime(t *testing.T, st *store.Store, handlers map[string]func(in any) (any, error)) (int, int, error) {
	t.Helper()
	acct := newTestAccount(testAccountID)
	acct.cfg = opStubConfig(handlers, apiErr("AccessDeniedException", "User: x is not authorized to perform: chime:unstubbed"))
	return scanChime(context.Background(), acct, testRegion, st, testScanID)
}

func chimeSipRuleID(id string) string { return chimeSipRuleNativeID(testRegion, testAccountID, id) }

func TestScanChime_SipRulesAndGroupsStoreEveryPageAfterEveryOtherPhase(t *testing.T) {
	st := newTestStore(t)
	var log chimeOpLog
	maxResults := map[string]int32{}
	total, inserted, err := runChime(t, st, chimeHandlers(&log, maxResults))
	if err != nil {
		t.Fatalf("scanChime: %v", err)
	}
	// One SIP media application, two SIP rules, two groups.
	if total != 5 || inserted != 5 {
		t.Errorf("total, inserted = %d, %d, want 5, 5", total, inserted)
	}

	rules := rowsByNativeID(t, st, TypeChimeSipRule)
	if len(rules) != 2 {
		t.Fatalf("stored %d SIP rules, want 2: %v", len(rules), rules)
	}
	r, ok := rules["arn:aws:chime:us-east-1:"+testAccountID+":sip-rule/rule-1"]
	if !ok {
		t.Fatalf("rule-1 missing; got %v", rules)
	}
	if _, ok := rules[chimeSipRuleID("rule-2")]; !ok {
		t.Errorf("page-2 rule-2 missing; got %v", rules)
	}
	if sv(r.Name) != "inbound" || sv(r.Region) != testRegion || sv(r.CreatedAt) != "2026-03-04T05:06:07Z" {
		t.Errorf("rule-1 name, region, created = %q, %q, %q", sv(r.Name), sv(r.Region), sv(r.CreatedAt))
	}
	if !strings.Contains(r.AttributesJSON, `"SipMediaApplicationId":"sma-1"`) {
		t.Errorf("rule-1 attributes lack the target application: %s", r.AttributesJSON)
	}

	groups := rowsByNativeID(t, st, TypeChimeVoiceConnectorGroup)
	if len(groups) != 2 {
		t.Fatalf("stored %d groups, want 2: %v", len(groups), groups)
	}
	g, ok := groups[chimeTestGroupARN1]
	if !ok {
		t.Fatalf("g-1 missing; got %v", groups)
	}
	if _, ok := groups[chimeTestGroupARN2]; !ok {
		t.Errorf("page-2 g-2 missing; got %v", groups)
	}
	if sv(g.Name) != "failover" || sv(g.Region) != testRegion || sv(g.CreatedAt) != "2026-03-04T05:06:07Z" {
		t.Errorf("g-1 name, region, created = %q, %q, %q", sv(g.Name), sv(g.Region), sv(g.CreatedAt))
	}
	if !strings.Contains(g.AttributesJSON, `"VoiceConnectorId":"vc-1"`) {
		t.Errorf("g-1 attributes lack the voice connector item: %s", g.AttributesJSON)
	}
	if len(rowsByNativeID(t, st, TypeChimeSipMediaApplication)) != 1 {
		t.Error("the SIP media application from the older voice phase is missing")
	}

	for _, op := range []string{"ListSipRules", "ListVoiceConnectorGroups"} {
		if maxResults[op] != 100 {
			t.Errorf("%s MaxResults = %d, want 100", op, maxResults[op])
		}
	}
	// Each new op is called twice (two pages) and only after every older op.
	n := len(log.ops)
	if n < 4 || strings.Join(log.ops[n-4:], ",") != "ListSipRules,ListSipRules,ListVoiceConnectorGroups,ListVoiceConnectorGroups" {
		t.Errorf("call order = %v, want the SIP-rule then group pages last", log.ops)
	}
}

func TestScanChime_EmptyRoutingListsStoreNothing(t *testing.T) {
	st := newTestStore(t)
	var log chimeOpLog
	h := chimeHandlers(&log, map[string]int32{})
	h["ListSipRules"] = func(any) (any, error) { return &cv.ListSipRulesOutput{}, nil }
	h["ListVoiceConnectorGroups"] = func(any) (any, error) { return &cv.ListVoiceConnectorGroupsOutput{}, nil }
	total, _, err := runChime(t, st, h)
	if err != nil {
		t.Fatalf("scanChime: %v", err)
	}
	if total != 1 {
		t.Errorf("total = %d, want 1 (the SIP media application)", total)
	}
	if n := len(rowsByNativeID(t, st, TypeChimeSipRule)) + len(rowsByNativeID(t, st, TypeChimeVoiceConnectorGroup)); n != 0 {
		t.Errorf("stored %d routing rows, want 0", n)
	}
}

// TestScanChime_RoutingDenials pins that a real denial of either new op warns
// once and keeps the rows of earlier pages, and that Chime's feature-gap
// ForbiddenException is skipped silently.
func TestScanChime_RoutingDenials(t *testing.T) {
	realDeny := apiErr("ForbiddenException", "User: x is not authorized to perform: chime:ListSipRules")
	featureGap := apiErr("ForbiddenException", "This feature is not available in this region")
	for _, tc := range []struct {
		name      string
		err       error
		onPage2   bool
		wantWarns int
		wantRows  int
	}{
		{name: "deny first page", err: realDeny, wantWarns: 1, wantRows: 0},
		{name: "deny second page keeps first", err: realDeny, onPage2: true, wantWarns: 1, wantRows: 1},
		{name: "feature gap", err: featureGap, wantWarns: 0, wantRows: 0},
	} {
		for _, op := range []string{"ListSipRules", "ListVoiceConnectorGroups"} {
			t.Run(tc.name+"/"+op, func(t *testing.T) {
				st := newTestStore(t)
				warns := countOpWarnings(st, "chime:"+op)
				var log chimeOpLog
				h := chimeHandlers(&log, map[string]int32{})
				h[op] = chimeFailPage(h[op], tc.onPage2, tc.err)
				if _, _, err := runChime(t, st, h); err != nil {
					t.Fatalf("scanChime: %v", err)
				}
				if *warns != tc.wantWarns {
					t.Errorf("%s warnings = %d, want %d", op, *warns, tc.wantWarns)
				}
				typ, other := TypeChimeSipRule, TypeChimeVoiceConnectorGroup
				if op == "ListVoiceConnectorGroups" {
					typ, other = other, typ
				}
				if got := len(rowsByNativeID(t, st, typ)); got != tc.wantRows {
					t.Errorf("stored %d %s rows, want %d", got, typ, tc.wantRows)
				}
				if got := len(rowsByNativeID(t, st, other)); got != 2 {
					t.Errorf("sibling phase stored %d %s rows, want 2", got, other)
				}
			})
		}
	}
}

// chimeFailPage makes h fail with err on its first page, or on its second page
// when onPage2 is set.
func chimeFailPage(h func(in any) (any, error), onPage2 bool, err error) func(in any) (any, error) {
	return func(in any) (any, error) {
		var token *string
		switch i := in.(type) {
		case *cv.ListSipRulesInput:
			token = i.NextToken
		case *cv.ListVoiceConnectorGroupsInput:
			token = i.NextToken
		}
		if (token != nil) == onPage2 {
			return nil, err
		}
		return h(in)
	}
}

func TestScanChime_RoutingRegionGapSkipsSilentlyOnFirstPageOnly(t *testing.T) {
	nxdomain := &net.DNSError{Err: "no such host", Name: "voice-chime.us-east-1.amazonaws.com", IsNotFound: true}
	unknownOp := apiErr("UnknownOperationException", "")
	for _, tc := range []struct {
		name      string
		err       error
		onPage2   bool
		wantErr   bool
		wantWarns int
	}{
		{name: "nxdomain", err: nxdomain},
		{name: "unknown operation", err: unknownOp},
		{name: "unknown operation after a page", err: unknownOp, onPage2: true, wantErr: true},
		// Returned as a plain error: an NXDOMAIN would mark all of aws:chime
		// unavailable with 0 counts.
		{name: "nxdomain after a page", err: nxdomain, onPage2: true, wantErr: true},
	} {
		for _, op := range []string{"ListSipRules", "ListVoiceConnectorGroups"} {
			t.Run(tc.name+"/"+op, func(t *testing.T) {
				st := newTestStore(t)
				var warned int
				st.OnWarn = func(store.ScanWarning) { warned++ }
				var log chimeOpLog
				h := chimeHandlers(&log, map[string]int32{})
				h[op] = chimeFailPage(h[op], tc.onPage2, tc.err)
				total, _, err := runChime(t, st, h)
				if gotErr := err != nil; gotErr != tc.wantErr {
					t.Fatalf("scanChime err = %v, want error %v", err, tc.wantErr)
				}
				if tc.wantErr && classifyServiceError(err) != outcomeError {
					t.Errorf("err = %v, want it classified outcomeError", err)
				}
				if tc.err == unknownOp && tc.wantErr && !isAPIErrorCode(err, "UnknownOperationException") {
					t.Errorf("err = %v, want the UnknownOperationException returned", err)
				}
				if warned != tc.wantWarns {
					t.Errorf("recorded %d warnings, want %d", warned, tc.wantWarns)
				}
				// SIP media application + the sibling phase's 2 rows, plus this
				// phase's first page when the gap hit the second.
				want := 3
				if tc.onPage2 {
					want = 4
				}
				if total != want {
					t.Errorf("total = %d, want %d", total, want)
				}
			})
		}
	}
}

// TestScanChime_RoutingErrorPropagatesAndSiblingsStillStore pins that an
// unexpected error from either new op is returned, while the other phase and
// the older phases still store their rows.
func TestScanChime_RoutingErrorPropagatesAndSiblingsStillStore(t *testing.T) {
	for _, op := range []string{"ListSipRules", "ListVoiceConnectorGroups"} {
		t.Run(op, func(t *testing.T) {
			st := newTestStore(t)
			var log chimeOpLog
			h := chimeHandlers(&log, map[string]int32{})
			h[op] = chimeFailPage(h[op], false, apiErr("BadRequestException", "boom"))
			total, _, err := runChime(t, st, h)
			if !isAPIErrorCode(err, "BadRequestException") {
				t.Fatalf("err = %v, want the BadRequestException", err)
			}
			if total != 3 {
				t.Errorf("total = %d, want 3 (SIP media application + sibling phase's 2 rows)", total)
			}
		})
	}
}

// TestScanChime_RoutingTransientIsDemoted pins that a transient failure of one
// new phase becomes a warning, so the other phase's hard error still
// classifies the service as an error.
func TestScanChime_RoutingTransientIsDemoted(t *testing.T) {
	for _, tc := range []struct{ transientOp, hardOp string }{
		{"ListSipRules", "ListVoiceConnectorGroups"},
		{"ListVoiceConnectorGroups", "ListSipRules"},
	} {
		t.Run(tc.transientOp, func(t *testing.T) {
			st := newTestStore(t)
			warns := countOpWarnings(st, "chime:"+tc.transientOp)
			var log chimeOpLog
			h := chimeHandlers(&log, map[string]int32{})
			h[tc.transientOp] = chimeFailPage(h[tc.transientOp], false, apiErr("ServiceUnavailableException", "busy"))
			h[tc.hardOp] = chimeFailPage(h[tc.hardOp], false, apiErr("BadRequestException", "boom"))
			_, _, err := runChime(t, st, h)
			if got := classifyServiceError(err); got != outcomeError {
				t.Errorf("classifyServiceError = %v, want outcomeError (err %v)", got, err)
			}
			if *warns != 1 {
				t.Errorf("%s warnings = %d, want 1", tc.transientOp, *warns)
			}
		})
	}
}

// TestScanChime_RoutingThrottleKeepsRowsAndIsTransient pins that Chime's
// modeled throttle, once the retries are spent, keeps the rows read before it
// and, alone, is returned as transient, so the scan is marked partial.
func TestScanChime_RoutingThrottleKeepsRowsAndIsTransient(t *testing.T) {
	for _, op := range []string{"ListSipRules", "ListVoiceConnectorGroups"} {
		t.Run(op, func(t *testing.T) {
			st := newTestStore(t)
			warns := countOpWarnings(st, "chime:"+op)
			var log chimeOpLog
			h := chimeHandlers(&log, map[string]int32{})
			h[op] = chimeFailPage(h[op], true, apiErr("ThrottledClientException", "rate exceeded"))
			total, _, err := runChime(t, st, h)
			if got := classifyServiceError(err); got != outcomeTransient {
				t.Fatalf("classifyServiceError = %v, want outcomeTransient (err %v)", got, err)
			}
			if *warns != 0 {
				t.Errorf("%s warnings = %d, want 0", op, *warns)
			}
			// SIP media application, the sibling phase's 2 rows, this phase's page 1.
			if total != 4 {
				t.Errorf("total = %d, want 4", total)
			}
		})
	}
}

func TestScanChime_VoiceConnectorGroupWithoutARNWarnsOnce(t *testing.T) {
	st := newTestStore(t)
	warns := countOpWarnings(st, "chime:ListVoiceConnectorGroups")
	var log chimeOpLog
	h := chimeHandlers(&log, map[string]int32{})
	h["ListVoiceConnectorGroups"] = func(any) (any, error) {
		return &cv.ListVoiceConnectorGroupsOutput{VoiceConnectorGroups: []cvtypes.VoiceConnectorGroup{
			{VoiceConnectorGroupId: ptrStr("g-a")},
			{VoiceConnectorGroupArn: ptrStr(chimeTestGroupARN1)},
			{VoiceConnectorGroupId: ptrStr("g-b")},
		}}, nil
	}
	if _, _, err := runChime(t, st, h); err != nil {
		t.Fatalf("scanChime: %v", err)
	}
	if groups := rowsByNativeID(t, st, TypeChimeVoiceConnectorGroup); len(groups) != 1 {
		t.Errorf("stored %d groups, want only the one with an ARN: %v", len(groups), groups)
	}
	if *warns != 1 {
		t.Errorf("warnings = %d, want 1 for both ARN-less groups", *warns)
	}
}

func TestChimeSipRuleNativeID(t *testing.T) {
	got := chimeSipRuleNativeID("eu-west-2", "123456789012", "abc-123")
	if want := "arn:aws:chime:eu-west-2:123456789012:sip-rule/abc-123"; got != want {
		t.Errorf("chimeSipRuleNativeID = %q, want %q", got, want)
	}
}

// Each of scanChime's four clients retries Chime's ThrottledClientException,
// which the SDK retryer does not retry by itself: a single throttle costs no
// rows. The older phases drop a non-denial error silently, so an unretried
// throttle shows as the missing row.
func TestScanChime_ThrottledClientExceptionIsRetried(t *testing.T) {
	const aiARN = "arn:aws:chime:us-east-1:123456789012:app-instance/ai-1"
	appInstances := func(any) (any, error) {
		return &ci.ListAppInstancesOutput{AppInstances: []citypes.AppInstanceSummary{{AppInstanceArn: ptrStr(aiARN)}}}, nil
	}
	for _, tc := range []struct {
		op, typ string
		page    func(any) (any, error)
	}{
		{"ListAppInstances", TypeChimeAppInstance, appInstances},
		{"ListChannelFlows", TypeChimeChannelFlow, func(any) (any, error) {
			return &cm.ListChannelFlowsOutput{ChannelFlows: []cmtypes.ChannelFlowSummary{{ChannelFlowArn: ptrStr(aiARN + "/channel-flow/cf-1")}}}, nil
		}},
		{"ListMediaPipelines", TypeChimeMediaPipeline, func(any) (any, error) {
			return &cp.ListMediaPipelinesOutput{MediaPipelines: []cptypes.MediaPipelineSummary{{MediaPipelineArn: ptrStr("arn:aws:chime:us-east-1:123456789012:media-pipeline/mp-1")}}}, nil
		}},
		{"ListSipMediaApplications", TypeChimeSipMediaApplication, func(any) (any, error) {
			return &cv.ListSipMediaApplicationsOutput{SipMediaApplications: []cvtypes.SipMediaApplication{{SipMediaApplicationArn: ptrStr(chimeTestSMAARN)}}}, nil
		}},
	} {
		t.Run(tc.op, func(t *testing.T) {
			st := newTestStore(t)
			calls := 0
			handlers := map[string]func(any) (any, error){
				"ListAppInstances": appInstances,
				tc.op: func(in any) (any, error) {
					calls++
					if calls == 1 {
						return nil, apiErr("ThrottledClientException", "rate exceeded")
					}
					return tc.page(in)
				},
			}
			acct := newTestAccount(testAccountID)
			acct.cfg = lmRetryingConfig(handlers)
			if _, _, err := scanChime(context.Background(), acct, testRegion, st, testScanID); err != nil {
				t.Fatalf("scanChime: %v", err)
			}
			if calls != 2 || len(rowsByNativeID(t, st, tc.typ)) != 1 {
				t.Errorf("%s calls = %d, %s rows = %d; want 2 calls and the retried page's row", tc.op, calls, tc.typ, len(rowsByNativeID(t, st, tc.typ)))
			}
		})
	}
}

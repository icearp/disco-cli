package aws

import (
	"context"
	"encoding/json"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/aws/retry"
	"github.com/aws/aws-sdk-go-v2/service/socialmessaging"
	socialmessagingtypes "github.com/aws/aws-sdk-go-v2/service/socialmessaging/types"
	"github.com/aws/smithy-go"
	"github.com/icearp/disco-cli/store"
)

const (
	smTestWABA1     = "arn:aws:social-messaging:us-east-1:123456789012:waba/1111"
	smTestWABA2     = "arn:aws:social-messaging:us-east-1:123456789012:waba/2222"
	smTestPhone     = "arn:aws:social-messaging:us-east-1:123456789012:phone-number-id/phone-number-id-1"
	smTestFlowA     = smTestWABA1 + "/flow/101"
	smTestFlowB     = smTestWABA1 + "/flow/102"
	smTestFlowC     = smTestWABA2 + "/flow/201"
	smTestTemplateA = smTestWABA1 + "/message-template/501"
	smTestTemplateB = smTestWABA1 + "/message-template/502"
	smTestTemplateC = smTestWABA2 + "/message-template/601"
)

var smDenied = apiErr("AccessDeniedException", "User: arn:aws:iam::123456789012:role/r is not authorized to perform: social-messaging:ListWhatsAppFlows")

// smHandlers answers every op the social-messaging scan calls: two WABAs
// (waba-1 COMPLETE, waba-2 with status2), one phone number on waba-1, flows and
// templates for waba-1 over two pages (the flows with an element lacking its
// id) and one each for waba-2. Every child list call is recorded in calls as
// "<Op> <waba id>"; a child call without the documented page size fails.
func smHandlers(calls *parentCalls, status2 socialmessagingtypes.RegistrationStatus) map[string]func(in any) (any, error) {
	return map[string]func(in any) (any, error){
		"ListLinkedWhatsAppBusinessAccounts": func(any) (any, error) {
			return &socialmessaging.ListLinkedWhatsAppBusinessAccountsOutput{LinkedAccounts: []socialmessagingtypes.LinkedWhatsAppBusinessAccountSummary{
				{Arn: sdkaws.String(smTestWABA1), Id: sdkaws.String("waba-1"), WabaName: sdkaws.String("one"), RegistrationStatus: socialmessagingtypes.RegistrationStatusComplete},
				{Arn: sdkaws.String(smTestWABA2), Id: sdkaws.String("waba-2"), WabaName: sdkaws.String("two"), RegistrationStatus: status2},
			}}, nil
		},
		"GetLinkedWhatsAppBusinessAccount": func(in any) (any, error) {
			id := sdkaws.ToString(in.(*socialmessaging.GetLinkedWhatsAppBusinessAccountInput).Id)
			acct := &socialmessagingtypes.LinkedWhatsAppBusinessAccount{Arn: sdkaws.String(smTestWABA2)}
			if id == "waba-1" {
				acct = &socialmessagingtypes.LinkedWhatsAppBusinessAccount{Arn: sdkaws.String(smTestWABA1), PhoneNumbers: []socialmessagingtypes.WhatsAppPhoneNumberSummary{{Arn: sdkaws.String(smTestPhone)}}}
			}
			return &socialmessaging.GetLinkedWhatsAppBusinessAccountOutput{Account: acct}, nil
		},
		"ListWhatsAppFlows": func(in any) (any, error) {
			p := in.(*socialmessaging.ListWhatsAppFlowsInput)
			calls.add("ListWhatsAppFlows " + sdkaws.ToString(p.Id))
			if sdkaws.ToInt32(p.MaxResults) != socialMessagingPageLimit {
				return nil, apiErr("InvalidParametersException", "maxResults")
			}
			flow := func(id, name string) socialmessagingtypes.MetaFlowSummary {
				return socialmessagingtypes.MetaFlowSummary{
					FlowId: sdkaws.String(id), FlowName: sdkaws.String(name), FlowStatus: sdkaws.String("PUBLISHED"),
					FlowCategories: []socialmessagingtypes.MetaFlowCategory{socialmessagingtypes.MetaFlowCategorySignUp},
				}
			}
			switch {
			case sdkaws.ToString(p.Id) == "waba-2":
				return &socialmessaging.ListWhatsAppFlowsOutput{Flows: []socialmessagingtypes.MetaFlowSummary{flow("201", "c")}}, nil
			case p.NextToken == nil:
				return &socialmessaging.ListWhatsAppFlowsOutput{Flows: []socialmessagingtypes.MetaFlowSummary{flow("101", "a"), {FlowName: sdkaws.String("no id")}}, NextToken: sdkaws.String("p2")}, nil
			}
			return &socialmessaging.ListWhatsAppFlowsOutput{Flows: []socialmessagingtypes.MetaFlowSummary{flow("102", "b")}}, nil
		},
		"ListWhatsAppMessageTemplates": func(in any) (any, error) {
			p := in.(*socialmessaging.ListWhatsAppMessageTemplatesInput)
			calls.add("ListWhatsAppMessageTemplates " + sdkaws.ToString(p.Id))
			if sdkaws.ToInt32(p.MaxResults) != socialMessagingPageLimit {
				return nil, apiErr("InvalidParametersException", "maxResults")
			}
			tmpl := func(id, name string) socialmessagingtypes.TemplateSummary {
				return socialmessagingtypes.TemplateSummary{
					MetaTemplateId: sdkaws.String(id), TemplateName: sdkaws.String(name), TemplateStatus: sdkaws.String("APPROVED"),
					TemplateLanguage: sdkaws.String("en_US"),
				}
			}
			switch {
			case sdkaws.ToString(p.Id) == "waba-2":
				return &socialmessaging.ListWhatsAppMessageTemplatesOutput{Templates: []socialmessagingtypes.TemplateSummary{tmpl("601", "z")}}, nil
			case p.NextToken == nil:
				return &socialmessaging.ListWhatsAppMessageTemplatesOutput{Templates: []socialmessagingtypes.TemplateSummary{tmpl("501", "x")}, NextToken: sdkaws.String("p2")}, nil
			}
			return &socialmessaging.ListWhatsAppMessageTemplatesOutput{Templates: []socialmessagingtypes.TemplateSummary{tmpl("502", "y")}}, nil
		},
	}
}

func scanSocialMessagingTopLevel(t *testing.T, st *store.Store, handlers map[string]func(in any) (any, error)) (int, error) {
	t.Helper()
	acct := newTestAccount(testAccountID)
	acct.cfg = opStubConfig(handlers, smDenied)
	total, _, err := scanSocialMessaging(context.Background(), acct, testRegion, st, testScanID)
	return total, err
}

func TestScanSocialMessaging_StoresFlowsAndTemplates(t *testing.T) {
	st := newTestStore(t)
	var calls parentCalls
	total, err := scanSocialMessagingTopLevel(t, st, smHandlers(&calls, socialmessagingtypes.RegistrationStatusComplete))
	if err != nil {
		t.Fatalf("scan: %v", err)
	}

	// 2 WABAs + 1 phone number + 3 flows + 3 templates.
	if total != 9 {
		t.Errorf("total = %d, want 9", total)
	}
	wantCalls := []string{
		"ListWhatsAppFlows waba-1", "ListWhatsAppFlows waba-2",
		"ListWhatsAppMessageTemplates waba-1", "ListWhatsAppMessageTemplates waba-2",
	}
	if got := calls.distinct(); !slices.Equal(got, wantCalls) {
		t.Errorf("child calls = %v, want %v", got, wantCalls)
	}

	flows := rowsByNativeID(t, st, TypeSocialMessagingWhatsAppFlow)
	if got, want := nativeIDs(flows), []string{smTestFlowA, smTestFlowB, smTestFlowC}; !slices.Equal(got, want) {
		t.Fatalf("flow rows = %v, want %v", got, want)
	}
	f := flows[smTestFlowA]
	if f.Type != TypeSocialMessagingWhatsAppFlow || sdkaws.ToString(f.Name) != "a" || sdkaws.ToString(f.Status) != "PUBLISHED" || sdkaws.ToString(f.Region) != testRegion {
		t.Errorf("flow row = type %q name %q status %q region %q", f.Type, sdkaws.ToString(f.Name), sdkaws.ToString(f.Status), sdkaws.ToString(f.Region))
	}
	var flowAttrs struct{ FlowCategories []string }
	if err := json.Unmarshal([]byte(f.AttributesJSON), &flowAttrs); err != nil || !slices.Equal(flowAttrs.FlowCategories, []string{"SIGN_UP"}) {
		t.Errorf("flow attrs FlowCategories = %v (err %v), want [SIGN_UP]", flowAttrs.FlowCategories, err)
	}

	templates := rowsByNativeID(t, st, TypeSocialMessagingWhatsAppMessageTemplate)
	if got, want := nativeIDs(templates), []string{smTestTemplateA, smTestTemplateB, smTestTemplateC}; !slices.Equal(got, want) {
		t.Fatalf("template rows = %v, want %v", got, want)
	}
	tm := templates[smTestTemplateA]
	if tm.Type != TypeSocialMessagingWhatsAppMessageTemplate || sdkaws.ToString(tm.Name) != "x" || sdkaws.ToString(tm.Status) != "APPROVED" || sdkaws.ToString(tm.Region) != testRegion {
		t.Errorf("template row = type %q name %q status %q region %q", tm.Type, sdkaws.ToString(tm.Name), sdkaws.ToString(tm.Status), sdkaws.ToString(tm.Region))
	}
	var tmplAttrs struct{ TemplateLanguage string }
	if err := json.Unmarshal([]byte(tm.AttributesJSON), &tmplAttrs); err != nil || tmplAttrs.TemplateLanguage != "en_US" {
		t.Errorf("template attrs TemplateLanguage = %q (err %v), want en_US", tmplAttrs.TemplateLanguage, err)
	}

	assertContainsNative(t, st, smTestWABA1, smTestFlowA)
	assertContainsNative(t, st, smTestWABA1, smTestFlowB)
	assertContainsNative(t, st, smTestWABA2, smTestFlowC)
	assertContainsNative(t, st, smTestWABA1, smTestTemplateA)
	assertContainsNative(t, st, smTestWABA2, smTestTemplateC)
}

func TestScanSocialMessaging_EmptyChildren(t *testing.T) {
	st := newTestStore(t)
	warns := countOpWarnings(st, socialMessagingListTemplatesOp)
	var calls parentCalls
	h := smHandlers(&calls, socialmessagingtypes.RegistrationStatusComplete)
	h["ListWhatsAppFlows"] = func(any) (any, error) { return &socialmessaging.ListWhatsAppFlowsOutput{}, nil }
	h["ListWhatsAppMessageTemplates"] = func(any) (any, error) { return &socialmessaging.ListWhatsAppMessageTemplatesOutput{}, nil }
	total, err := scanSocialMessagingTopLevel(t, st, h)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != 3 {
		t.Errorf("total = %d, want 3 (2 WABAs + 1 phone number)", total)
	}
	for _, typ := range []string{TypeSocialMessagingWhatsAppFlow, TypeSocialMessagingWhatsAppMessageTemplate} {
		if n := len(rowsByNativeID(t, st, typ)); n != 0 {
			t.Errorf("%s rows = %d, want 0", typ, n)
		}
	}
	if *warns != 0 {
		t.Errorf("template warnings = %d, want 0", *warns)
	}
}

// A WABA unlinked since listing, denied, refused by Meta, failing transiently
// after retries, or refusing input while its registration is incomplete drops
// only its own children: unlinked is silent, the others warn once per op.
// Any other error, including InvalidParametersException from a complete WABA,
// fails that op's phase and is returned; the other new phase and the older
// phases keep their rows.
func TestScanSocialMessaging_FailedChildParent(t *testing.T) {
	type phase struct {
		op, typ   string
		rows      []string // rows when every WABA succeeds
		waba2Rows []string // rows when only waba-2 succeeds
	}
	flows := phase{"ListWhatsAppFlows", TypeSocialMessagingWhatsAppFlow, []string{smTestFlowA, smTestFlowB, smTestFlowC}, []string{smTestFlowC}}
	templates := phase{"ListWhatsAppMessageTemplates", TypeSocialMessagingWhatsAppMessageTemplate, []string{smTestTemplateA, smTestTemplateB, smTestTemplateC}, []string{smTestTemplateC}}
	invalid := apiErr("InvalidParametersException", "bad")
	for _, ph := range []struct{ failing, other phase }{{flows, templates}, {templates, flows}} {
		for _, tc := range []struct {
			name      string
			status2   socialmessagingtypes.RegistrationStatus
			fail      map[string]error
			wantCode  string // "" means the scan returns nil
			wantRows  []string
			wantWarns int
		}{
			{"waba unlinked", "COMPLETE", map[string]error{"waba-1": apiErr("ResourceNotFoundException", "gone")}, "", ph.failing.waba2Rows, 0},
			{"denied everywhere", "COMPLETE", map[string]error{"waba-1": smDenied, "waba-2": smDenied}, "", nil, 1},
			{"meta denied everywhere", "COMPLETE", map[string]error{"waba-1": apiErr("AccessDeniedByMetaException", "no"), "waba-2": apiErr("AccessDeniedByMetaException", "no")}, "", nil, 1},
			{"throttled once", "COMPLETE", map[string]error{"waba-1": apiErr("ThrottlingException", "slow down")}, "", ph.failing.waba2Rows, 1},
			{"incomplete waba refuses input", "INCOMPLETE", map[string]error{"waba-2": invalid}, "", ph.failing.rows[:2], 1},
			{"complete waba refuses input", "COMPLETE", map[string]error{"waba-2": invalid}, "InvalidParametersException", nil, 0},
			{"throttled 429 once", "COMPLETE", map[string]error{"waba-1": apiErr("ThrottledRequestException", "slow down")}, "", ph.failing.waba2Rows, 1},
			{"meta dependency failing once", "COMPLETE", map[string]error{"waba-1": apiErr("DependencyException", "meta down")}, "", ph.failing.waba2Rows, 1},
			{"two retryable codes", "COMPLETE", map[string]error{"waba-1": apiErr("DependencyException", "meta down"), "waba-2": apiErr("InternalServiceException", "oops")}, "", nil, 2},
			{"internal error everywhere", "COMPLETE", map[string]error{"waba-1": apiErr("InternalServiceException", "oops"), "waba-2": apiErr("InternalServiceException", "oops")}, "", nil, 1},
			{"other error", "COMPLETE", map[string]error{"waba-1": apiErr("ConflictException", "conflict")}, "ConflictException", nil, 0},
		} {
			t.Run(ph.failing.op+"/"+tc.name, func(t *testing.T) {
				st := newTestStore(t)
				warns := countOpWarnings(st, "social-messaging:"+ph.failing.op)
				var calls parentCalls
				h := smHandlers(&calls, tc.status2)
				next := h[ph.failing.op]
				h[ph.failing.op] = func(in any) (any, error) {
					var id string
					switch p := in.(type) {
					case *socialmessaging.ListWhatsAppFlowsInput:
						id = sdkaws.ToString(p.Id)
					case *socialmessaging.ListWhatsAppMessageTemplatesInput:
						id = sdkaws.ToString(p.Id)
					}
					if err, ok := tc.fail[id]; ok {
						return nil, err
					}
					return next(in)
				}
				_, err := scanSocialMessagingTopLevel(t, st, h)
				if tc.wantCode == "" {
					if err != nil {
						t.Fatalf("err = %v, want nil", err)
					}
				} else {
					assertAPICode(t, err, tc.wantCode)
				}
				if got := nativeIDs(rowsByNativeID(t, st, ph.failing.typ)); !slices.Equal(got, tc.wantRows) {
					t.Errorf("%s rows = %v, want %v", ph.failing.typ, got, tc.wantRows)
				}
				if *warns != tc.wantWarns {
					t.Errorf("warnings = %d, want %d", *warns, tc.wantWarns)
				}
				if got := nativeIDs(rowsByNativeID(t, st, ph.other.typ)); !slices.Equal(got, ph.other.rows) {
					t.Errorf("%s rows = %v, want %v; one new phase must not stop the other", ph.other.typ, got, ph.other.rows)
				}
				if _, ok := rowsByNativeID(t, st, TypeSocialMessagingPhoneNumberID)[smTestPhone]; !ok {
					t.Error("phone number missing; a new phase must not stop the older phases")
				}
			})
		}
	}
}

// A hard error in an older phase after the WABAs were stored still lets the
// new phases run over them, and is returned.
func TestScanSocialMessaging_OlderPhaseErrorStillRunsNewPhases(t *testing.T) {
	st := newTestStore(t)
	var calls parentCalls
	h := smHandlers(&calls, socialmessagingtypes.RegistrationStatusComplete)
	h["GetLinkedWhatsAppBusinessAccount"] = func(any) (any, error) { return nil, apiErr("InvalidParametersException", "bad") }
	total, err := scanSocialMessagingTopLevel(t, st, h)
	assertAPICode(t, err, "InvalidParametersException")
	if got := nativeIDs(rowsByNativeID(t, st, TypeSocialMessagingWhatsAppFlow)); !slices.Equal(got, []string{smTestFlowA, smTestFlowB, smTestFlowC}) {
		t.Errorf("flow rows = %v, want all three", got)
	}
	if got := nativeIDs(rowsByNativeID(t, st, TypeSocialMessagingWhatsAppMessageTemplate)); !slices.Equal(got, []string{smTestTemplateA, smTestTemplateB, smTestTemplateC}) {
		t.Errorf("template rows = %v, want all three", got)
	}
	// 2 WABAs + 3 flows + 3 templates; no phone number.
	if total != 8 {
		t.Errorf("total = %d, want 8", total)
	}
}

// A transient error in an older phase is demoted to a warning so that a new
// phase's hard error still reaches the dispatcher as a hard error.
func TestScanSocialMessaging_OlderPhaseTransientDoesNotMaskHardError(t *testing.T) {
	for _, op := range []string{"ListWhatsAppFlows", "ListWhatsAppMessageTemplates"} {
		t.Run(op, func(t *testing.T) {
			st := newTestStore(t)
			warns := countOpWarnings(st, "social-messaging")
			var calls parentCalls
			h := smHandlers(&calls, socialmessagingtypes.RegistrationStatusComplete)
			h["GetLinkedWhatsAppBusinessAccount"] = func(any) (any, error) { return nil, apiErr("ThrottlingException", "slow down") }
			h[op] = func(any) (any, error) { return nil, apiErr("ConflictException", "conflict") }
			_, err := scanSocialMessagingTopLevel(t, st, h)
			assertAPICode(t, err, "ConflictException")
			if got := classifyServiceError(err); got != outcomeError {
				t.Errorf("classifyServiceError(%v) = %v, want outcomeError", err, got)
			}
			if *warns != 1 {
				t.Errorf("warnings = %d, want 1 for the demoted transient", *warns)
			}
		})
	}
}

// A WABA listing that fails stores nothing and queries no children.
func TestScanSocialMessaging_WABAListDeniedSkipsChildren(t *testing.T) {
	st := newTestStore(t)
	var calls parentCalls
	h := smHandlers(&calls, socialmessagingtypes.RegistrationStatusComplete)
	delete(h, "ListLinkedWhatsAppBusinessAccounts")
	if _, err := scanSocialMessagingTopLevel(t, st, h); err != nil {
		t.Fatalf("err = %v, want nil (denial warns)", err)
	}
	if got := calls.distinct(); len(got) != 0 {
		t.Errorf("child calls = %v, want none", got)
	}
}

// A child listing whose next page repeats the token just sent stops there
// instead of looping. The handlers fail after a few calls so a regression
// fails the test rather than hanging it.
func TestScanSocialMessaging_RepeatedTokenStops(t *testing.T) {
	for _, tc := range []struct{ op, typ string }{
		{"ListWhatsAppFlows", TypeSocialMessagingWhatsAppFlow},
		{"ListWhatsAppMessageTemplates", TypeSocialMessagingWhatsAppMessageTemplate},
	} {
		t.Run(tc.op, func(t *testing.T) {
			st := newTestStore(t)
			var calls parentCalls
			h := smHandlers(&calls, socialmessagingtypes.RegistrationStatusComplete)
			var n atomic.Int32
			h[tc.op] = func(in any) (any, error) {
				call := n.Add(1)
				if call > 6 {
					return nil, apiErr("ConflictException", "token loop")
				}
				id := sdkaws.String(strconv.Itoa(int(call)))
				switch in.(type) {
				case *socialmessaging.ListWhatsAppFlowsInput:
					return &socialmessaging.ListWhatsAppFlowsOutput{
						Flows:     []socialmessagingtypes.MetaFlowSummary{{FlowId: id, FlowName: sdkaws.String("loop")}},
						NextToken: sdkaws.String("same"),
					}, nil
				case *socialmessaging.ListWhatsAppMessageTemplatesInput:
					return &socialmessaging.ListWhatsAppMessageTemplatesOutput{
						Templates: []socialmessagingtypes.TemplateSummary{{MetaTemplateId: id, TemplateName: sdkaws.String("loop")}},
						NextToken: sdkaws.String("same"),
					}, nil
				}
				return nil, nil
			}
			if _, err := scanSocialMessagingTopLevel(t, st, h); err != nil {
				t.Fatalf("err = %v, want nil", err)
			}
			// Two WABAs, each: first page, then one page that repeats the token; both
			// pages' rows are stored.
			if got := n.Load(); got != 4 {
				t.Errorf("%s calls = %d, want 4", tc.op, got)
			}
			if got := len(rowsByNativeID(t, st, tc.typ)); got != 4 {
				t.Errorf("%s rows = %d, want 4", tc.typ, got)
			}
		})
	}
}

// Templates without MetaTemplateId are not stored; however many there are,
// across WABAs, one warning reports them.
func TestScanSocialMessaging_TemplateWithoutIDWarnsOnce(t *testing.T) {
	st := newTestStore(t)
	warns := countOpWarnings(st, socialMessagingListTemplatesOp)
	var calls parentCalls
	h := smHandlers(&calls, socialmessagingtypes.RegistrationStatusComplete)
	h["ListWhatsAppMessageTemplates"] = func(in any) (any, error) {
		id := sdkaws.ToString(in.(*socialmessaging.ListWhatsAppMessageTemplatesInput).Id)
		out := []socialmessagingtypes.TemplateSummary{{TemplateName: sdkaws.String("no id")}}
		if id == "waba-1" {
			out = append(out, socialmessagingtypes.TemplateSummary{MetaTemplateId: sdkaws.String("501"), TemplateName: sdkaws.String("x")})
		}
		return &socialmessaging.ListWhatsAppMessageTemplatesOutput{Templates: out}, nil
	}
	if _, err := scanSocialMessagingTopLevel(t, st, h); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if got := nativeIDs(rowsByNativeID(t, st, TypeSocialMessagingWhatsAppMessageTemplate)); !slices.Equal(got, []string{smTestTemplateA}) {
		t.Errorf("template rows = %v, want [%s]", got, smTestTemplateA)
	}
	if *warns != 1 {
		t.Errorf("template warnings = %d, want 1", *warns)
	}
}

// A skipped WABA's warning names the error code, how many WABAs it skipped
// and the first of them.
func TestScanSocialMessaging_SkipWarningNamesCodeCountFirst(t *testing.T) {
	for _, code := range []string{"ThrottledRequestException", "DependencyException", "InternalServiceException", "AccessDeniedByMetaException", "InvalidParametersException"} {
		t.Run(code, func(t *testing.T) {
			st := newTestStore(t)
			var msgs []string
			st.OnWarn = func(w store.ScanWarning) {
				if w.Service == socialMessagingListFlowsOp {
					msgs = append(msgs, w.Message)
				}
			}
			var calls parentCalls
			h := smHandlers(&calls, socialmessagingtypes.RegistrationStatusIncomplete)
			next := h["ListWhatsAppFlows"]
			h["ListWhatsAppFlows"] = func(in any) (any, error) {
				if sdkaws.ToString(in.(*socialmessaging.ListWhatsAppFlowsInput).Id) == "waba-2" {
					return nil, apiErr(code, "refused")
				}
				return next(in)
			}
			if _, err := scanSocialMessagingTopLevel(t, st, h); err != nil {
				t.Fatalf("err = %v, want nil", err)
			}
			want := "1 WhatsApp Business Account(s) skipped with " + code + ";"
			if len(msgs) != 1 || !strings.HasPrefix(msgs[0], want) || !strings.Contains(msgs[0], "first waba-2:") {
				t.Errorf("warnings = %q, want one starting %q naming first waba-2", msgs, want)
			}
		})
	}
}

// The client retries the service's 429 throttle code; the SDK alone does not.
func TestSocialMessagingClientOptions_RetriesThrottledRequest(t *testing.T) {
	cfg := sdkaws.Config{Region: testRegion, Retryer: func() sdkaws.Retryer { return retry.NewStandard() }}
	r := socialmessaging.NewFromConfig(cfg, socialMessagingClientOptions(testRegion)).Options().Retryer
	if !r.IsErrorRetryable(&smithy.GenericAPIError{Code: "ThrottledRequestException"}) {
		t.Error("ThrottledRequestException not retryable, want retryable")
	}
	if r.IsErrorRetryable(&smithy.GenericAPIError{Code: "ConflictException"}) {
		t.Error("ConflictException retryable, want not retryable")
	}
}

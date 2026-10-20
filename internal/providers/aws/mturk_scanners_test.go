package aws

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strconv"
	"sync"
	"testing"
	"time"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/mturk"
	mturktypes "github.com/aws/aws-sdk-go-v2/service/mturk/types"
)

const (
	mturkTestIDA = "3AAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
	mturkTestIDB = "3BBBBBBBBBBBBBBBBBBBBBBBBBBBBB"
	mturkTestIDC = "3CCCCCCCCCCCCCCCCCCCCCCCCCCCCC"
)

func mturkTestNativeID(id string) string {
	return "arn:aws:mturk-requester:us-east-1:" + testAccountID + ":qualification-type/" + id
}

// mturkCalls records every ListQualificationTypes input the stub answered.
type mturkCalls struct {
	mu  sync.Mutex
	ins []mturk.ListQualificationTypesInput
}

func (c *mturkCalls) add(in *mturk.ListQualificationTypesInput) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.ins = append(c.ins, *in)
}

// mturkPages answers ListQualificationTypes with pages[i] for the i-th call,
// chaining them by NextToken "t1", "t2", ...; a call whose token does not
// match the expected one fails, so the scanner must forward the token.
func mturkPages(t *testing.T, calls *mturkCalls, pages ...[]mturktypes.QualificationType) map[string]func(in any) (any, error) {
	t.Helper()
	return map[string]func(in any) (any, error){
		"ListQualificationTypes": func(in any) (any, error) {
			req := in.(*mturk.ListQualificationTypesInput)
			calls.mu.Lock()
			i := len(calls.ins)
			calls.mu.Unlock()
			calls.add(req)
			if want := mturkToken(i); sdkaws.ToString(req.NextToken) != want {
				t.Errorf("call %d NextToken = %q, want %q", i+1, sdkaws.ToString(req.NextToken), want)
			}
			if i >= len(pages) {
				t.Fatalf("ListQualificationTypes called %d times, want %d", i+1, len(pages))
			}
			out := &mturk.ListQualificationTypesOutput{QualificationTypes: pages[i]}
			if i+1 < len(pages) {
				out.NextToken = sdkaws.String(mturkToken(i + 1))
			}
			return out, nil
		},
	}
}

func mturkToken(i int) string {
	if i == 0 {
		return ""
	}
	return "t" + strconv.Itoa(i)
}

func runMTurk(t *testing.T, handlers map[string]func(in any) (any, error), denyErr error) (total, inserted int, err error) {
	t.Helper()
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	acct.cfg = opStubConfig(handlers, denyErr)
	return scanMTurk(context.Background(), acct, "", st, testScanID)
}

func TestScanMTurk_TwoPagesStoredWithOwnedFilter(t *testing.T) {
	created := time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC)
	var calls mturkCalls
	h := mturkPages(t, &calls,
		[]mturktypes.QualificationType{
			{
				QualificationTypeId:     sdkaws.String(mturkTestIDA),
				Name:                    sdkaws.String("english-test"),
				QualificationTypeStatus: mturktypes.QualificationTypeStatusActive,
				CreationTime:            &created,
				IsRequestable:           sdkaws.Bool(true),
				Keywords:                sdkaws.String("english,grammar"),
			},
			{Name: sdkaws.String("idless")},
		},
		[]mturktypes.QualificationType{
			{QualificationTypeId: sdkaws.String(mturkTestIDB), Name: sdkaws.String("internal-flag"), IsRequestable: sdkaws.Bool(false)},
		},
	)
	var regions parentCalls
	st := newTestStore(t)
	warns := countOpWarnings(st, "mturk-requester:ListQualificationTypes")
	acct := newTestAccount(testAccountID)
	acct.cfg = recordCallRegions(opStubConfig(h, apiErr("AccessDeniedException", "unstubbed op")), &regions)
	acct.cfg.Region = "eu-west-1"

	total, inserted, err := scanMTurk(context.Background(), acct, "", st, testScanID)
	if err != nil {
		t.Fatalf("scanMTurk: %v", err)
	}
	if total != 2 || inserted != 2 {
		t.Errorf("total, inserted = %d, %d; want 2, 2", total, inserted)
	}
	if *warns != 1 {
		t.Errorf("warnings = %d, want 1 for the element without a QualificationTypeId", *warns)
	}
	if got := regions.distinct(); !slices.Equal(got, []string{"us-east-1"}) {
		t.Errorf("calls went to regions %v, want [us-east-1]", got)
	}
	if len(calls.ins) != 2 {
		t.Fatalf("ListQualificationTypes calls = %d, want 2", len(calls.ins))
	}
	for i, in := range calls.ins {
		if in.MustBeOwnedByCaller == nil || !*in.MustBeOwnedByCaller {
			t.Errorf("call %d MustBeOwnedByCaller = %v, want true", i+1, in.MustBeOwnedByCaller)
		}
		if in.MustBeRequestable == nil || *in.MustBeRequestable {
			t.Errorf("call %d MustBeRequestable = %v, want false", i+1, in.MustBeRequestable)
		}
		if sdkaws.ToInt32(in.MaxResults) != 100 {
			t.Errorf("call %d MaxResults = %v, want 100", i+1, in.MaxResults)
		}
	}

	rows := rowsByNativeID(t, st, TypeMTurkQualificationType)
	if len(rows) != 2 {
		t.Fatalf("stored %d rows, want 2: %v", len(rows), rows)
	}
	a, ok := rows[mturkTestNativeID(mturkTestIDA)]
	if !ok {
		t.Fatalf("row %s missing; have %v", mturkTestNativeID(mturkTestIDA), rows)
	}
	if a.Type != TypeMTurkQualificationType {
		t.Errorf("Type = %q", a.Type)
	}
	if sdkaws.ToString(a.Region) != "us-east-1" {
		t.Errorf("Region = %v, want us-east-1", sdkaws.ToString(a.Region))
	}
	if sdkaws.ToString(a.Name) != "english-test" {
		t.Errorf("Name = %v, want english-test", sdkaws.ToString(a.Name))
	}
	if sdkaws.ToString(a.Status) != "Active" {
		t.Errorf("Status = %v, want Active", sdkaws.ToString(a.Status))
	}
	if sdkaws.ToString(a.CreatedAt) != "2026-03-04T05:06:07Z" {
		t.Errorf("CreatedAt = %v, want 2026-03-04T05:06:07Z", sdkaws.ToString(a.CreatedAt))
	}
	var attrs mturktypes.QualificationType
	if err := json.Unmarshal([]byte(a.AttributesJSON), &attrs); err != nil {
		t.Fatalf("attrs: %v", err)
	}
	if sdkaws.ToString(attrs.QualificationTypeId) != mturkTestIDA || sdkaws.ToString(attrs.Keywords) != "english,grammar" {
		t.Errorf("attrs = %s", a.AttributesJSON)
	}
	b, ok := rows[mturkTestNativeID(mturkTestIDB)]
	if !ok {
		t.Fatalf("second-page row %s missing", mturkTestNativeID(mturkTestIDB))
	}
	if b.Status != nil {
		t.Errorf("Status = %q, want nil for an empty enum", *b.Status)
	}
}

func TestScanMTurk_Empty(t *testing.T) {
	var calls mturkCalls
	st := newTestStore(t)
	warns := countOpWarnings(st, "mturk-requester:ListQualificationTypes")
	acct := newTestAccount(testAccountID)
	acct.cfg = opStubConfig(mturkPages(t, &calls, nil), apiErr("AccessDeniedException", "unstubbed op"))
	total, inserted, err := scanMTurk(context.Background(), acct, "", st, testScanID)
	if err != nil || total != 0 || inserted != 0 {
		t.Errorf("got (%d, %d, %v), want (0, 0, nil)", total, inserted, err)
	}
	if len(calls.ins) != 1 {
		t.Errorf("calls = %d, want 1", len(calls.ins))
	}
	if *warns != 0 {
		t.Errorf("warnings = %d, want 0", *warns)
	}
}

func TestScanMTurk_DeniedWarns(t *testing.T) {
	st := newTestStore(t)
	warns := countOpWarnings(st, "mturk-requester:ListQualificationTypes")
	acct := newTestAccount(testAccountID)
	acct.cfg = opStubConfig(nil, apiErr("AccessDeniedException", "User: arn:aws:iam::123456789012:role/r is not authorized to perform: mechanicalturk:ListQualificationTypes"))
	total, inserted, err := scanMTurk(context.Background(), acct, "", st, testScanID)
	if err != nil || total != 0 || inserted != 0 {
		t.Errorf("got (%d, %d, %v), want (0, 0, nil)", total, inserted, err)
	}
	if *warns != 1 {
		t.Errorf("warnings = %d, want 1", *warns)
	}
}

// When the user selects the service, a RequestError stays a hard error.
func TestScanMTurk_RequestErrorFails(t *testing.T) {
	reqErr := &mturktypes.RequestError{Message: sdkaws.String("bad request"), TurkErrorCode: sdkaws.String("AWS.MechanicalTurk.X")}
	h := map[string]func(in any) (any, error){
		"ListQualificationTypes": func(any) (any, error) { return nil, reqErr },
	}
	_, _, err := runMTurk(t, h, apiErr("AccessDeniedException", "unstubbed op"))
	var got *mturktypes.RequestError
	if !errors.As(err, &got) {
		t.Fatalf("err = %v, want a RequestError", err)
	}
	if o := classifyServiceError(err); o != outcomeError {
		t.Errorf("classifyServiceError = %v, want outcomeError", o)
	}
}

// Rows from pages listed before a failing page stay stored and counted.
func TestScanMTurk_LaterPageErrorKeepsEarlierRows(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	n := 0
	acct.cfg = opStubConfig(map[string]func(in any) (any, error){
		"ListQualificationTypes": func(any) (any, error) {
			n++
			if n == 1 {
				return &mturk.ListQualificationTypesOutput{
					QualificationTypes: []mturktypes.QualificationType{{QualificationTypeId: sdkaws.String(mturkTestIDC)}},
					NextToken:          sdkaws.String("t1"),
				}, nil
			}
			return nil, &mturktypes.RequestError{Message: sdkaws.String("bad token")}
		},
	}, apiErr("AccessDeniedException", "unstubbed op"))
	total, _, err := scanMTurk(context.Background(), acct, "", st, testScanID)
	if err == nil {
		t.Fatal("err = nil, want the second page's error")
	}
	if total != 1 {
		t.Errorf("total = %d, want 1", total)
	}
	if _, ok := rowsByNativeID(t, st, TypeMTurkQualificationType)[mturkTestNativeID(mturkTestIDC)]; !ok {
		t.Error("first-page row not stored")
	}
}

func TestScanMTurk_RepeatedNextTokenFails(t *testing.T) {
	errLooped := errors.New("scanner kept following the repeated token")
	calls := 0
	h := map[string]func(in any) (any, error){
		"ListQualificationTypes": func(any) (any, error) {
			calls++
			if calls > 2 {
				return nil, errLooped
			}
			return &mturk.ListQualificationTypesOutput{NextToken: sdkaws.String("same")}, nil
		},
	}
	_, _, err := runMTurk(t, h, apiErr("AccessDeniedException", "unstubbed op"))
	if !errors.Is(err, errRepeatedNextToken) {
		t.Errorf("err = %v, want errRepeatedNextToken", err)
	}
	if calls != 2 {
		t.Errorf("calls = %d, want 2", calls)
	}
}

// aws:mturk is global and opt-in: a default scan leaves it out, and selecting
// it with --services runs it.
func TestScanMTurk_RegisteredGlobalOptIn(t *testing.T) {
	var entry *serviceEntry
	for i := range registeredServices {
		if registeredServices[i].name == "aws:mturk" {
			entry = &registeredServices[i]
		}
	}
	if entry == nil || !entry.global || !entry.optIn {
		t.Fatalf("aws:mturk entry = %+v; want registered, global, opt-in", entry)
	}
	if slices.Contains(filteredServiceNames(nil, false), "aws:mturk") {
		t.Error("default scan must exclude opt-in aws:mturk")
	}
	if got := filteredServiceNames([]string{"aws:mturk"}, false); !slices.Equal(got, []string{"aws:mturk"}) {
		t.Errorf("--services aws:mturk selected %v, want [aws:mturk]", got)
	}
}

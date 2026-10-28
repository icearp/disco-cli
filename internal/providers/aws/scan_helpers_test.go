package aws

import (
	"context"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"maps"
	"net"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/icearp/disco-cli/store"
)

func TestDemoteTransient_Outcomes(t *testing.T) {
	for _, tc := range []struct {
		name      string
		err       error
		kept      bool // the error is returned unchanged rather than dropped
		wantWarns int
	}{
		{"transient API error is a warning", apiErr("ServiceUnavailable", "try later"), false, 1},
		{"dial error is a warning", &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("connection refused")}, false, 1},
		{"NXDOMAIN is left to the dispatcher", &net.DNSError{Err: "no such host", Name: "autoscaling.us-east-1.amazonaws.com", IsNotFound: true}, true, 0},
		{"hard error is returned", apiErr("ValidationException", "bad"), true, 0},
		{"store write beside transient is returned", errors.Join(apiErr("ServiceUnavailable", "x"), fmt.Errorf("%w: upsert resources: %w", store.ErrStoreWrite, errors.New("disk full"))), true, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := countOpWarnings(st, "op")
			got := demoteTransient(st, "op", newTestAccount(testAccountID), testRegion, tc.err)
			switch {
			case tc.kept && !errors.Is(got, tc.err):
				t.Errorf("demoteTransient(%v) = %v, want it returned unchanged", tc.err, got)
			case !tc.kept && got != nil:
				t.Errorf("demoteTransient(%v) = %v, want nil", tc.err, got)
			}
			if *warns != tc.wantWarns {
				t.Errorf("warnings = %d, want %d", *warns, tc.wantWarns)
			}
		})
	}
}

// skipFanOutParents are three parents whose id and ARN differ, so a warning
// shows which of the two its label names.
var skipFanOutParents = []childParent{
	{id: "env-ok", arn: "arn:aws:evs:us-east-1:123456789012:environment/env-ok"},
	{id: "env-slow1", arn: "arn:aws:evs:us-east-1:123456789012:environment/env-slow1"},
	{id: "env-slow2", arn: "arn:aws:evs:us-east-1:123456789012:environment/env-slow2"},
}

// seedSkipFanOutParents stores the parents so their children can be linked.
func seedSkipFanOutParents(t *testing.T, st *store.Store) {
	t.Helper()
	rows := make([]*store.Resource, len(skipFanOutParents))
	for i, p := range skipFanOutParents {
		rows[i] = &store.Resource{Provider: "aws", AccountID: testAccountID, Type: TypeEVSEnvironment, NativeID: p.arn, DiscoveredBy: testScanID}
	}
	if _, err := st.UpsertResources(rows); err != nil {
		t.Fatalf("seed parents: %v", err)
	}
}

func skipFanOutChild(p childParent) *store.Resource {
	return &store.Resource{Provider: "aws", AccountID: testAccountID, Type: TypeEVSEnvironmentHost, NativeID: p.arn + "/host/h1", DiscoveredBy: testScanID}
}

// recordWarnings collects every scan warning st reports.
func recordWarnings(st *store.Store) *[]store.ScanWarning {
	var (
		mu    sync.Mutex
		warns []store.ScanWarning
	)
	st.OnWarn = func(w store.ScanWarning) {
		mu.Lock()
		warns = append(warns, w)
		mu.Unlock()
	}
	return &warns
}

// skipWarningCount reads the leading parent count of a skip warning, after
// skipIfTransient's "transient: " prefix when present.
var skipWarningCount = regexp.MustCompile(`^(?:transient: )?(\d+) parent\(s\) skipped`)

// The first skipped parent is either slow one, whichever goroutine records
// first, so a warning is matched on what both labels share.
const (
	firstByID  = "env-slow"
	firstByARN = "arn:aws:evs:us-east-1:123456789012:environment/env-slow"
)

// assertOneSkipWarning fails unless warns is one warning for op whose leading
// count is count and whose first skipped parent starts with first.
func assertOneSkipWarning(t *testing.T, warns []store.ScanWarning, op string, count int, first string) {
	t.Helper()
	if len(warns) != 1 {
		t.Fatalf("warnings = %+v, want exactly one", warns)
	}
	w := warns[0]
	if w.Service != op || w.Scope != testAccountID+"/"+testRegion {
		t.Errorf("warning service/scope = %q/%q, want %q/%q", w.Service, w.Scope, op, testAccountID+"/"+testRegion)
	}
	m := skipWarningCount.FindStringSubmatch(w.Message)
	if m == nil || m[1] != strconv.Itoa(count) {
		t.Errorf("warning message %q, want leading count %d", w.Message, count)
	}
	if !strings.Contains(w.Message, ", first "+first) {
		t.Errorf("warning message %q, want the first parent named as %s…", w.Message, first)
	}
}

// A denial or a listed not-found about another account's ARN, or one naming
// no account, is a gap; the same answer about an own or unparseable ARN, or
// any other error, is not.
func TestOtherAccountGap_Outcomes(t *testing.T) {
	const other = "210987654321"
	notFound := []string{"ResourceNotFoundException"}
	denied := apiErr("AccessDeniedException", "denied")
	gone := apiErr("ResourceNotFoundException", "gone")
	own := arnFor("aws", "omics", testRegion, testAccountID, "workflow/1")
	foreign := arnFor("aws", "omics", testRegion, other, "workflow/1")
	for _, c := range []struct {
		name     string
		arn      string
		err      error
		notFound []string
		want     bool
	}{
		{"own denied", own, denied, notFound, false},
		{"own not found", own, gone, notFound, false},
		{"other denied", foreign, denied, notFound, true},
		{"other not found", foreign, gone, notFound, true},
		{"other not found, op lists none", foreign, gone, nil, false},
		{"other validation", foreign, apiErr("ValidationException", "bad"), notFound, false},
		{"other no error", foreign, nil, notFound, false},
		{"unparseable ARN denied", "not-an-arn", denied, notFound, false},
		{"unknown partition denied", arnFor("aws-xx", "omics", "xx-west-1", other, "workflow/1"), denied, notFound, false},
		{"empty account denied", arnFor("aws", "omics", testRegion, "", "workflow/1"), denied, notFound, true},
		{"empty account not found", arnFor("aws", "omics", testRegion, "", "workflow/1"), gone, notFound, true},
		{"own gov-partition not found", arnFor("aws-us-gov", "omics", "us-gov-west-1", testAccountID, "workflow/1"), gone, notFound, false},
		{"other gov-partition not found", arnFor("aws-us-gov", "omics", "us-gov-west-1", other, "workflow/1"), gone, notFound, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := otherAccountGap(c.arn, testAccountID, c.err, c.notFound); got != c.want {
				t.Errorf("otherAccountGap(%q, %q, %v, %v) = %v; want %v", c.arn, testAccountID, c.err, c.notFound, got, c.want)
			}
		})
	}
}

func TestChildFanOutSkipTransient_Outcomes(t *testing.T) {
	for _, tc := range []struct {
		name      string
		label     func(childParent) string
		slowErr   error
		wantErr   bool
		wantRows  int
		wantFirst string // "" when no warning is wanted
	}{
		{"transient parents skipped, named by ARN", byParentARN, apiErr("ThrottlingException", "slow down"), false, 1, firstByARN},
		{"transient parents skipped, named by id", byParentID, apiErr("ThrottlingException", "slow down"), false, 1, firstByID},
		{"other error fails the fan-out", byParentARN, apiErr("InternalBadThing", "boom"), true, 0, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			seedSkipFanOutParents(t, st)
			warns := recordWarnings(st)
			total, _, err := childFanOutSkipTransient(context.Background(), st, newTestAccount(testAccountID), testRegion, "evs:ListX",
				skipFanOutParents, func(error) bool { return false }, tc.label,
				func(_ context.Context, p childParent) ([]*store.Resource, error) {
					if p.id == "env-ok" {
						return []*store.Resource{skipFanOutChild(p)}, nil
					}
					return []*store.Resource{skipFanOutChild(p)}, tc.slowErr
				})
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tc.wantErr)
			}
			if got := len(rowsByNativeID(t, st, TypeEVSEnvironmentHost)); got != tc.wantRows || total != tc.wantRows {
				t.Errorf("stored %d rows, total %d; want %d", got, total, tc.wantRows)
			}
			if tc.wantFirst != "" {
				assertOneSkipWarning(t, *warns, "evs:ListX", 2, tc.wantFirst)
			} else if len(*warns) != 0 {
				t.Errorf("warnings = %+v, want none", *warns)
			}
		})
	}
}

func TestChildFanOutSkipNotReady_Outcomes(t *testing.T) {
	const what, why = "widget(s)", "the widget may be warming up"
	for _, tc := range []struct {
		name      string
		label     func(childParent) string
		slowErr   error
		wantErr   bool
		wantRows  int
		wantFirst string // "" when no warning is wanted
	}{
		{"not-ready parents skipped, named by id", byParentID, apiErr("ValidationException", "not ready"), false, 1, firstByID},
		{"not-ready parents skipped, named by ARN", byParentARN, apiErr("ValidationException", "not ready"), false, 1, firstByARN},
		{"gone parents skipped silently", byParentID, apiErr("ResourceNotFoundException", "gone"), false, 1, ""},
		{"other error fails the fan-out", byParentID, apiErr("InternalBadThing", "boom"), true, 0, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			seedSkipFanOutParents(t, st)
			warns := recordWarnings(st)
			isGone := func(err error) bool { return isAPIErrorCode(err, "ResourceNotFoundException") }
			total, _, err := childFanOutSkipNotReady(context.Background(), st, newTestAccount(testAccountID), testRegion, "evs:ListX",
				skipFanOutParents, isGone, tc.label, what, why,
				func(_ context.Context, p childParent) ([]*store.Resource, error) {
					if p.id == "env-ok" {
						return []*store.Resource{skipFanOutChild(p)}, nil
					}
					return nil, tc.slowErr
				})
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tc.wantErr)
			}
			if got := len(rowsByNativeID(t, st, TypeEVSEnvironmentHost)); got != tc.wantRows || total != tc.wantRows {
				t.Errorf("stored %d rows, total %d; want %d", got, total, tc.wantRows)
			}
			if tc.wantFirst == "" {
				if len(*warns) != 0 {
					t.Errorf("warnings = %+v, want none", *warns)
				}
				return
			}
			if len(*warns) != 1 {
				t.Fatalf("warnings = %+v, want exactly one", *warns)
			}
			w := (*warns)[0]
			if w.Service != "evs:ListX" || w.Scope != testAccountID+"/"+testRegion {
				t.Errorf("warning service/scope = %q/%q, want evs:ListX/%s/%s", w.Service, w.Scope, testAccountID, testRegion)
			}
			// ", first env-slow" cannot match an ARN-named parent, so each
			// label case fails if the other key is used.
			for _, want := range []string{"2 " + what + " skipped", why, ", first " + tc.wantFirst} {
				if !strings.Contains(w.Message, want) {
					t.Errorf("warning message %q, want it to contain %q", w.Message, want)
				}
			}
		})
	}
}

func TestChildFanOutStoredSkipTransient_KeepsRowsStoredBeforeTheError(t *testing.T) {
	st := newTestStore(t)
	seedSkipFanOutParents(t, st)
	warns := recordWarnings(st)
	total, inserted, err := childFanOutStoredSkipTransient(context.Background(), st, newTestAccount(testAccountID), testRegion, "evs:ListX",
		skipFanOutParents, func(error) bool { return false }, byParentARN,
		func(_ context.Context, p childParent) (int, int, error) {
			parentID := store.ResourceID("aws", testAccountID, p.arn)
			n, i, err := upsertContained(st, "evs:ListX", []containedChild{{row: skipFanOutChild(p), parentID: parentID}})
			if err != nil || p.id == "env-ok" {
				return n, i, err
			}
			return n, i, apiErr("ThrottlingException", "slow down")
		})
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if total != 3 || inserted != 3 {
		t.Errorf("total, inserted = %d, %d; want 3, 3", total, inserted)
	}
	if got := len(rowsByNativeID(t, st, TypeEVSEnvironmentHost)); got != 3 {
		t.Errorf("stored %d rows, want 3", got)
	}
	assertOneSkipWarning(t, *warns, "evs:ListX", 2, firstByARN)
}

// Each not-ready code skips the parents refused with it, keeping the rows they
// stored before the error, with one warning naming that code; a code not
// listed fails the fan-out.
func TestChildFanOutStoredSkipNotReady_Outcomes(t *testing.T) {
	codes := []string{"ValidationException", "ConflictException"}
	for _, tc := range []struct {
		code    string
		wantErr bool
	}{
		{code: "ValidationException"},
		{code: "ConflictException"},
		{code: "InternalBadThing", wantErr: true},
	} {
		t.Run(tc.code, func(t *testing.T) {
			st := newTestStore(t)
			seedSkipFanOutParents(t, st)
			warns := recordWarnings(st)
			total, inserted, err := childFanOutStoredSkipNotReady(context.Background(), st, newTestAccount(testAccountID), testRegion, "evs:ListX",
				skipFanOutParents, func(error) bool { return false }, byParentARN, "parent(s)", "not ready", codes,
				func(_ context.Context, p childParent) (int, int, error) {
					parentID := store.ResourceID("aws", testAccountID, p.arn)
					n, i, err := upsertContained(st, "evs:ListX", []containedChild{{row: skipFanOutChild(p), parentID: parentID}})
					if err != nil || p.id == "env-ok" {
						return n, i, err
					}
					return n, i, apiErr(tc.code, "refused")
				})
			if tc.wantErr {
				if !isAPIErrorCode(err, tc.code) {
					t.Errorf("err = %v; want %s", err, tc.code)
				}
				return
			}
			if err != nil {
				t.Fatalf("err = %v, want nil", err)
			}
			if total != 3 || inserted != 3 {
				t.Errorf("total, inserted = %d, %d; want 3, 3", total, inserted)
			}
			if got := len(rowsByNativeID(t, st, TypeEVSEnvironmentHost)); got != 3 {
				t.Errorf("stored %d rows, want 3", got)
			}
			assertOneSkipWarning(t, *warns, "evs:ListX", 2, firstByARN)
			if len(*warns) == 1 && !strings.Contains((*warns)[0].Message, "skipped with "+tc.code) {
				t.Errorf("warning message %q, want it to name %s", (*warns)[0].Message, tc.code)
			}
		})
	}
}

func TestNotReadyTally_WarnsOnceForValidationExceptionOnly(t *testing.T) {
	st := newTestStore(t)
	warns := recordWarnings(st)
	acct := newTestAccount(testAccountID)
	var tally notReadyTally
	if tally.drop("a", apiErr("ThrottlingException", "slow")) || tally.drop("b", nil) {
		t.Fatal("drop accepted an error other than ValidationException")
	}
	tally.warnSkipped(st, "evs:ListX", acct, testRegion, "parent(s)", "not ready")
	if len(*warns) != 0 {
		t.Fatalf("warnings = %+v, want none before any skip", *warns)
	}
	for _, p := range skipFanOutParents[1:] {
		if !tally.drop(p.arn, apiErr("ValidationException", "not ready")) {
			t.Fatalf("drop(%s, ValidationException) = false, want true", p.arn)
		}
	}
	tally.warnSkipped(st, "evs:ListX", acct, testRegion, "parent(s)", "not ready")
	assertOneSkipWarning(t, *warns, "evs:ListX", 2, firstByARN)
}

// incompleteSiblingWait is how long a sibling of a parent whose listing ended
// incomplete waits for a cancellation that must not come. A passing run does
// not depend on it; only a regression (siblings cancelled) needs the cancel to
// land within it, which it does in microseconds.
const incompleteSiblingWait = 200 * time.Millisecond

// siblingWatch lets the siblings of a failing parent count whether the
// fan-out cancels them. errgroup keeps only its first error, so a sibling's
// context.Canceled would never reach the fan-out's result.
type siblingWatch struct {
	failed    chan struct{}
	cancelled atomic.Int32
}

// await waits for the failing parent to return, then counts a cancellation
// of ctx within incompleteSiblingWait.
func (w *siblingWatch) await(ctx context.Context) {
	<-w.failed
	select {
	case <-ctx.Done():
		w.cancelled.Add(1)
	case <-time.After(incompleteSiblingWait):
	}
}

// A parent whose listing ended on a repeated token keeps the rows it read
// and does not cancel its siblings; the fan-out stores every row and returns
// the repeat.
func TestChildFanOut_IncompleteListingKeepsRowsAndSiblings(t *testing.T) {
	st := newTestStore(t)
	seedSkipFanOutParents(t, st)
	w := &siblingWatch{failed: make(chan struct{})}
	total, _, err := childFanOut(context.Background(), st, newTestAccount(testAccountID), testRegion, "evs:ListX",
		skipFanOutParents, func(error) bool { return false },
		func(ctx context.Context, p childParent) ([]*store.Resource, error) {
			if p.id == "env-ok" {
				defer close(w.failed)
				return []*store.Resource{skipFanOutChild(p)}, fmt.Errorf("page 2: %w", errRepeatedNextToken)
			}
			w.await(ctx)
			return []*store.Resource{skipFanOutChild(p)}, nil
		})
	if !errors.Is(err, errRepeatedNextToken) {
		t.Fatalf("err = %v; want errRepeatedNextToken", err)
	}
	if n := w.cancelled.Load(); n != 0 {
		t.Errorf("%d siblings cancelled; want 0", n)
	}
	if got := len(rowsByNativeID(t, st, TypeEVSEnvironmentHost)); got != 3 || total != 3 {
		t.Errorf("stored %d rows, total %d; want every parent's row, 3", got, total)
	}
}

// A parent whose listing ended on a repeated token does not cancel its
// siblings, and the repeat is returned with every parent's counts.
func TestChildFanOutStored_IncompleteListingKeepsSiblings(t *testing.T) {
	st := newTestStore(t)
	w := &siblingWatch{failed: make(chan struct{})}
	total, _, err := childFanOutStored(context.Background(), st, newTestAccount(testAccountID), testRegion, "evs:ListX",
		skipFanOutParents, func(error) bool { return false },
		func(ctx context.Context, p childParent) (int, int, error) {
			if p.id == "env-ok" {
				defer close(w.failed)
				return 1, 1, fmt.Errorf("page 2: %w", errRepeatedNextToken)
			}
			w.await(ctx)
			return 1, 1, nil
		})
	if !errors.Is(err, errRepeatedNextToken) {
		t.Fatalf("err = %v; want errRepeatedNextToken", err)
	}
	if n := w.cancelled.Load(); n != 0 {
		t.Errorf("%d siblings cancelled; want 0", n)
	}
	if total != 3 {
		t.Errorf("total = %d; want 3", total)
	}
}

// A transient error joined with a listing that ended incomplete stays an
// error: demoting the join to a warning would report the scan complete.
func TestDemoteTransient_KeepsIncompleteListingJoin(t *testing.T) {
	for name, incomplete := range map[string]error{"repeated token": errRepeatedNextToken, "truncated without token": errTruncatedWithoutToken} {
		t.Run(name, func(t *testing.T) {
			st := newTestStore(t)
			warns := recordWarnings(st)
			joined := errors.Join(fmt.Errorf("op: %w", incomplete), apiErr("ThrottlingException", "slow down"))
			got := demoteTransient(st, "op", newTestAccount(testAccountID), testRegion, joined)
			if !errors.Is(got, incomplete) {
				t.Errorf("demoteTransient = %v; want the join returned", got)
			}
			if len(*warns) != 0 {
				t.Errorf("warnings = %+v; want none", *warns)
			}
		})
	}
}

// runScanners: a scanner whose listing ended incomplete does not cancel the
// others, and its error is returned with every scanner's counts.
func TestRunScanners_IncompleteListingKeepsSiblings(t *testing.T) {
	w := &siblingWatch{failed: make(chan struct{})}
	total, _, err := runScanners(context.Background(),
		func(context.Context) (int, int, error) {
			defer close(w.failed)
			return 1, 1, fmt.Errorf("page 2: %w", errRepeatedNextToken)
		},
		func(ctx context.Context) (int, int, error) {
			w.await(ctx)
			return 1, 1, nil
		})
	if !errors.Is(err, errRepeatedNextToken) {
		t.Fatalf("err = %v; want errRepeatedNextToken", err)
	}
	if n := w.cancelled.Load(); n != 0 {
		t.Errorf("%d siblings cancelled; want 0", n)
	}
	if total != 2 {
		t.Errorf("total = %d; want 2", total)
	}
}

// An incomplete listing joined with a failed store write is a hard error, not
// an incomplete listing: the parent's rows are dropped by the hard-error rule
// rather than written to a store that has already failed.
func TestChildFanOut_IncompleteListingWithStoreWriteFails(t *testing.T) {
	st := newTestStore(t)
	seedSkipFanOutParents(t, st)
	p := skipFanOutParents[0]
	total, _, err := childFanOut(context.Background(), st, newTestAccount(testAccountID), testRegion, "evs:ListX",
		[]childParent{p}, func(error) bool { return false },
		func(context.Context, childParent) ([]*store.Resource, error) {
			return []*store.Resource{skipFanOutChild(p)}, errors.Join(
				fmt.Errorf("page 2: %w", errRepeatedNextToken), fmt.Errorf("page 1: %w", store.ErrStoreWrite))
		})
	if !errors.Is(err, store.ErrStoreWrite) {
		t.Fatalf("err = %v; want store.ErrStoreWrite", err)
	}
	if got := len(rowsByNativeID(t, st, TypeEVSEnvironmentHost)); got != 0 || total != 0 {
		t.Errorf("stored %d rows, total %d; want the fan-out failed with none", got, total)
	}
}

// A silent dispatch sentinel is not a hard member: the throttle beside it is
// kept, so the join is not read as the sentinel, which would discard the
// stored rows' counts and hide the throttle, and no warning is recorded.
func TestPhaseErrsJoin_SilentSentinelBesideThrottle(t *testing.T) {
	for _, tc := range []struct {
		name     string
		sentinel error
	}{
		{"nxdomain", &net.DNSError{Err: "no such host", Name: "svc.us-east-1.amazonaws.com", IsNotFound: true}},
		{"disabled", markServiceDisabled(apiErr("AccessDeniedException", "not enabled"))},
		{"not entitled", markServiceNotEntitled(apiErr("AccessDeniedException", "not eligible"))},
		{"unavailable", markServiceUnavailable(apiErr("UnsupportedRegionException", "not here"))},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := countOpWarnings(st, "svc:Throttled")
			var errs phaseErrs
			errs.add("svc:Silent", tc.sentinel)
			errs.add("svc:Throttled", apiErr("ThrottlingException", "Rate exceeded"))
			err := errs.join(st, newTestAccount(testAccountID), testRegion)
			switch got := classifyServiceError(err); got {
			case outcomeError, outcomeTransient:
			default:
				t.Errorf("classifyServiceError(%v) = %v, want outcomeError or outcomeTransient", err, got)
			}
			if *warns != 0 {
				t.Errorf("svc:Throttled warnings = %d, want 0", *warns)
			}
		})
	}
}

// A transient error joined with a hard one stays an error, whichever side
// comes first: demoting the join would drop the hard error with it.
func TestDemoteTransient_KeepsHardErrorJoin(t *testing.T) {
	hard := apiErr("ValidationException", "bad")
	for name, joined := range map[string]error{
		"transient first": errors.Join(apiErr("ThrottlingException", "slow down"), hard),
		"hard first":      errors.Join(hard, apiErr("ThrottlingException", "slow down")),
	} {
		t.Run(name, func(t *testing.T) {
			st := newTestStore(t)
			warns := recordWarnings(st)
			got := demoteTransient(st, "op", newTestAccount(testAccountID), testRegion, joined)
			if !errors.Is(got, hard) {
				t.Errorf("demoteTransient(%v) = %v; want the join returned", joined, got)
			}
			if len(*warns) != 0 {
				t.Errorf("warnings = %+v; want none", *warns)
			}
		})
	}
}

// A closure write that fails after the children were stored returns their
// counts with the error, so the dispatcher reports the rows that exist.
func TestUpsertContained_ClosureFailureKeepsCounts(t *testing.T) {
	st := newTestStore(t)
	refuseClosureWrites(t, st)
	parent := skipFanOutParents[0]
	child := skipFanOutChild(parent)
	total, inserted, err := upsertContained(st, "op", []containedChild{{row: child, parentID: store.ResourceID("aws", testAccountID, parent.arn)}})
	if !errors.Is(err, store.ErrStoreWrite) {
		t.Errorf("err = %v; want the closure's store write failure", err)
	}
	if total != 1 || inserted != 1 {
		t.Errorf("total, inserted = %d, %d; want 1, 1 for the stored child", total, inserted)
	}
	if n := len(rowsByNativeID(t, st, child.Type)); n != 1 {
		t.Errorf("child rows = %d; want 1", n)
	}
}

// A failed store read is a hard error even when the driver's cause looks like
// a network glitch, as a dropped Postgres connection does.
func TestStoreReadError_NeverTransient(t *testing.T) {
	for _, cause := range []error{io.ErrUnexpectedEOF, &net.OpError{Op: "read", Net: "tcp", Err: errors.New("connection reset")}} {
		err := storeReadError("list rows", cause)
		if !errors.Is(err, errStoreRead) {
			t.Errorf("storeReadError(%v) = %v; want errStoreRead", cause, err)
		}
		if got := classifyServiceError(err); got != outcomeError {
			t.Errorf("classifyServiceError(storeReadError(%v)) = %v; want outcomeError", cause, got)
		}
		// The cause is severed from the chain, so no errors.Is/As predicate
		// (isEndpointUnreachable, skip tallies) reads it as a cloud glitch.
		if errors.Is(err, cause) || isEndpointUnreachable(err) {
			t.Errorf("storeReadError(%v) = %v; want the cause out of the error chain", cause, err)
		}
	}
}

// demoteBeside demotes err only beside a hard error in rest: alone, or beside
// transient errors only, it is returned, so the scan is marked partial.
func TestDemoteBeside_DemotesOnlyBesideHardError(t *testing.T) {
	throttle := apiErr("ThrottlingException", "slow down")
	hard := apiErr("ValidationException", "bad")
	for _, tc := range []struct {
		name     string
		rest     []error
		want     serviceOutcome
		wantWarn int
	}{
		{"alone", nil, outcomeTransient, 0},
		{"beside nil", []error{nil}, outcomeTransient, 0},
		{"beside a transient", []error{apiErr("RequestLimitExceeded", "slow")}, outcomeTransient, 0},
		{"beside a hard error", []error{hard}, outcomeError, 1},
		{"beside a disabled sentinel", []error{markServiceDisabled(apiErr("AccessDeniedException", "not enabled"))}, outcomeError, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := countOpWarnings(st, "op")
			got := demoteBeside(st, "op", newTestAccount(testAccountID), testRegion, throttle, tc.rest...)
			if classifyServiceError(got) != tc.want {
				t.Errorf("demoteBeside(..., %v) = %v (outcome %v); want outcome %v", tc.rest, got, classifyServiceError(got), tc.want)
			}
			if *warns != tc.wantWarn {
				t.Errorf("warnings = %d; want %d", *warns, tc.wantWarn)
			}
		})
	}
}

// TestTransientDemotersAreKnown guards the H1 class of bug: a scanner that
// calls demoteTransient or skipIfTransient on a phase's error turns it into a
// warning even when it is the only failure, so a throttled scan reads as
// complete. Phase errors are joined through phaseErrs or demoteBeside, which
// demote only beside a hard error. Each function still calling either directly
// is listed with its reason, keyed file:function; a new caller fails, and so
// does a listed one that no longer calls it.
func TestTransientDemotersAreKnown(t *testing.T) {
	known := map[string]map[string]bool{
		"demoteTransient": {
			// Enrichment only: the tapes are stored from ListTapes either way.
			"storagegateway_scanners.go:describeSGWTapeArchives": true,
		},
		// Per-item tallies: one warning names every item whose read was
		// still throttled after the SDK's retries. Most keep the listed row
		// unenriched; backup selections, Security Hub associations and
		// subnet CIDR reservations instead skip that parent's children.
		// Either way the scan is not marked partial: whether a per-item
		// throttle should be is an open question for the user.
		"skipIfTransient": {
			"backup_scanners.go:warn":                                   true,
			"cloudtrail_scanners.go:scanCloudTrailEventDataStores":      true,
			"cloudtrail_scanners.go:warn":                               true,
			"cloudtrail_extended_scanners.go:settle":                    true,
			"connect_scanners.go:warnKept":                              true,
			"customerprofiles_scanners.go:scanCPDomains":                true,
			"ec2_network_extra_scanners.go:scanSubnetCidrReservations":  true,
			"kafka_children_scanners.go:scanKafkaChannels":              true,
			"mq_scanners.go:warn":                                       true,
			"ec2_ipam_resolver_scanners.go:scanIPAMPrefixListResolvers": true,
			"fms_scanners.go:warnFMSListingOnly":                        true,
			"organizations_scanners.go:scanOrgAccounts":                 true,
			"pipes_scanners.go:scanPipes":                               true,
			"securityhub_scanners.go:warn":                              true,
			"wafv2_scanners.go:scanWAFv2WebACLs":                        true,
			"wafv2_extended_scanners.go:scanWAFv2WebACLAssociations":    true,
		},
	}
	names, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	found := map[string]map[string]bool{}
	for _, name := range names {
		if strings.HasSuffix(name, "_test.go") || name == "scan_helpers.go" {
			continue
		}
		f, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		for _, d := range f.Decls {
			fn, ok := d.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				if id, ok := call.Fun.(*ast.Ident); ok && known[id.Name] != nil {
					if found[id.Name] == nil {
						found[id.Name] = map[string]bool{}
					}
					found[id.Name][name+":"+fn.Name.Name] = true
				}
				return true
			})
		}
	}
	for callee, callers := range found {
		for caller := range callers {
			if !known[callee][caller] {
				t.Errorf("%s calls %s; join phase errors through phaseErrs or demoteBeside", caller, callee)
			}
		}
	}
	for callee, callers := range known {
		for caller := range callers {
			if !found[callee][caller] {
				t.Errorf("%s is listed but no longer calls %s; drop it from the list", caller, callee)
			}
		}
	}
}

// storeReadRows warns about a denied listing, and runs warn, only after the
// rows are written; a failed write is returned at once with neither, since
// both would describe rows the write lost.
func TestStoreReadRows_FailedWriteStopsBeforeWarnings(t *testing.T) {
	denied := apiErr("AccessDeniedException", "User: x is not authorized to perform: svc:ListX")
	rows := func() []*store.Resource {
		return []*store.Resource{{Provider: "aws", AccountID: testAccountID, Type: TypeNetworkFirewallProxy, NativeID: "x", DiscoveredBy: testScanID}}
	}
	t.Run("write succeeds", func(t *testing.T) {
		st := newTestStore(t)
		warns := countWarnings(st)
		warned := false
		total, _, err := storeReadRows(st, newTestAccount(testAccountID), testRegion, "svc:ListX", rows(), denied, func() { warned = true })
		if err != nil || total != 1 || !warned || *warns != 1 {
			t.Errorf("err %v total %d warned %v warnings %d; want nil, 1, true, 1 (the denial)", err, total, warned, *warns)
		}
	})
	t.Run("write fails", func(t *testing.T) {
		st, closeStore := openClosingStore(t)
		warns := countWarnings(st)
		closeStore()
		warned := false
		_, _, err := storeReadRows(st, newTestAccount(testAccountID), testRegion, "svc:ListX", rows(), denied, func() { warned = true })
		if !errors.Is(err, store.ErrStoreWrite) {
			t.Fatalf("err = %v; want store.ErrStoreWrite", err)
		}
		if warned || *warns != 0 {
			t.Errorf("warned %v warnings %d after a failed write; want none", warned, *warns)
		}
	})
}

// readEachKey returns each key's result in its key's slot, whichever read
// finished first: key 0's read finishes only after key 1's.
func TestReadEachKey_ResultsInKeyOrder(t *testing.T) {
	keyOneDone := make(chan struct{})
	got, err := readEachKey(context.Background(), []int{0, 1, 2}, func(_ context.Context, k int) string {
		switch k {
		case 0:
			<-keyOneDone
		case 1:
			defer close(keyOneDone)
		}
		return strconv.Itoa(k)
	})
	if err != nil || !slices.Equal(got, []string{"0", "1", "2"}) {
		t.Errorf("readEachKey = %q, %v; want [0 1 2], nil", got, err)
	}
}

// Once ctx is done no key is read, and the error says the keys were not
// read.
func TestReadEachKey_StopsWhenContextDone(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	// A free slot and a done ctx are both ready, and select picks between them
	// at random; repeating makes a read that slips through a near certainty.
	for range 64 {
		var reads atomic.Int32
		got, err := readEachKey(ctx, []int{0, 1, 2}, func(_ context.Context, k int) int {
			reads.Add(1)
			return k
		})
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("err = %v, want context.Canceled", err)
		}
		if n := reads.Load(); n != 0 || len(got) != 0 {
			t.Fatalf("reads = %d, results = %v; want none", n, got)
		}
	}
}

// When ctx ends while reads are running, readEachKey returns only after they
// finish. The reads cancel ctx and then wait to be released, so a return
// before the release is a return with reads still running.
func TestReadEachKey_WaitsForRunningReadsWhenContextEnds(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	release := make(chan struct{})
	var running atomic.Int32
	done := make(chan error)
	go func() {
		_, err := readEachKey(ctx, make([]int, 2*fanoutMed), func(context.Context, int) int {
			running.Add(1)
			defer running.Add(-1)
			cancel()
			<-release
			return 0
		})
		done <- err
	}()
	// The bound only limits how long a wrong return is looked for; the right
	// code cannot return before the release whatever the timing.
	select {
	case <-done:
		close(release)
		t.Fatal("readEachKey returned while its reads were still running")
	case <-time.After(50 * time.Millisecond):
	}
	close(release)
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want context.Canceled", err)
	}
	if n := running.Load(); n != 0 {
		t.Errorf("%d reads still running after readEachKey returned", n)
	}
}

// At most fanoutMed reads run at once, and that many do. Every read blocks
// until released; once fanoutMed are running the test gives a cap that is too
// high time to start more, and a cap that is too low never gets that far.
func TestReadEachKey_RunsFanoutMedReadsAtOnce(t *testing.T) {
	release := make(chan struct{})
	var running atomic.Int32
	done := make(chan error)
	go func() {
		_, err := readEachKey(context.Background(), make([]int, 3*fanoutMed), func(context.Context, int) int {
			running.Add(1)
			defer running.Add(-1)
			<-release
			return 0
		})
		done <- err
	}()
	defer func() {
		close(release)
		if err := <-done; err != nil {
			t.Errorf("readEachKey: %v", err)
		}
	}()
	deadline := time.After(5 * time.Second)
	for running.Load() < fanoutMed {
		select {
		case <-deadline:
			t.Fatalf("running reads = %d after 5s, want %d", running.Load(), fanoutMed)
		case <-time.After(time.Millisecond):
		}
	}
	// The wait only bounds how long extra reads are looked for; the right
	// code never starts one whatever the timing.
	time.Sleep(50 * time.Millisecond)
	if n := running.Load(); n != fanoutMed {
		t.Errorf("running reads = %d, want %d", n, fanoutMed)
	}
}

const (
	tagTestListOp = "svc:ListThings"
	tagTestOp     = "svc:ListTagsForResource"
)

var (
	tagTestNotFound = apiErr("ResourceNotFoundException", "Thing x not found")
	tagTestDenied   = apiErr("AccessDeniedException", "User: x is not authorized to perform: svc:ListTagsForResource")
	tagTestThrottle = apiErr("ThrottlingException", "Rate exceeded")
	tagTestInvalid  = apiErr("ValidationException", "bad ARN")
)

// tagTestRow is a listed thing's row, keyed by its id.
func tagTestRow(id string) (*store.Resource, string, string) {
	region := testRegion
	return &store.Resource{Provider: "aws", AccountID: testAccountID, Type: TypeNetworkFirewallProxy, NativeID: id, Region: &region, DiscoveredBy: testScanID}, "", ""
}

// tagTestReader answers each row's tag read from reads, keyed by NativeID.
func tagTestReader(reads map[string]rowTagRead) rowTagReader {
	return rowTagReader{
		op: tagTestOp, notFound: []string{"ResourceNotFoundException"},
		read: func(_ context.Context, r *store.Resource) (*string, error) {
			got := reads[r.NativeID]
			return got.tags, got.err
		},
	}
}

// storeTaggedThings stores ids, listed on one page, through
// storeTokenListTags with reads as the tag reads.
func storeTaggedThings(ctx context.Context, st *store.Store, ids []string, reads map[string]rowTagRead) (*phaseTotals, bool) {
	var p phaseTotals
	stop := storeTokenListTags(ctx, st, newTestAccount(testAccountID), testRegion, tagTestListOp,
		func(context.Context, *string) ([]string, *string, error) { return ids, nil, nil },
		tagTestRow, tagTestReader(reads), &p)
	return &p, stop
}

// Each tag read outcome decides what becomes of its rows, and leaves the
// sibling row, whose read succeeds, stored with its tags.
func TestStoreTokenListTags_ReadOutcomes(t *testing.T) {
	sibling := rowTagRead{tags: mapTagsJSON(map[string]string{"team": "s"})}
	cases := []struct {
		name       string
		read       rowTagRead
		wantStored bool
		wantTags   map[string]string
		wantWarns  int
		wantErr    serviceOutcome
		wantCode   string
	}{
		{name: "tags read", read: rowTagRead{tags: mapTagsJSON(map[string]string{"env": "prod"})}, wantStored: true, wantTags: map[string]string{"env": "prod"}},
		{name: "no tags", read: rowTagRead{}, wantStored: true},
		{name: "not found", read: rowTagRead{err: tagTestNotFound}},
		{name: "denied", read: rowTagRead{err: tagTestDenied}, wantStored: true, wantWarns: 1},
		{name: "transient", read: rowTagRead{err: tagTestThrottle}, wantStored: true, wantErr: outcomeTransient, wantCode: "ThrottlingException"},
		{name: "other error", read: rowTagRead{err: tagTestInvalid}, wantStored: true, wantErr: outcomeError, wantCode: "ValidationException"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			tagWarns := recordWarnings(st)
			p, stop := storeTaggedThings(context.Background(), st, []string{"x1", "x2", "s"},
				map[string]rowTagRead{"x1": c.read, "x2": c.read, "s": sibling})
			_, _, err := p.result(st, newTestAccount(testAccountID), testRegion)
			if stop {
				t.Fatal("stop = true; want false (the write succeeded)")
			}
			if c.wantCode == "" {
				if err != nil {
					t.Fatalf("err = %v; want nil", err)
				}
			} else {
				assertAPICode(t, err, c.wantCode)
				if got := classifyServiceError(err); got != c.wantErr {
					t.Errorf("classifyServiceError = %v; want %v", got, c.wantErr)
				}
			}
			rows := rowsByNativeID(t, st, TypeNetworkFirewallProxy)
			wantTotal := 1
			for _, id := range []string{"x1", "x2"} {
				r, ok := rows[id]
				if ok != c.wantStored {
					t.Fatalf("%s stored = %v; want %v", id, ok, c.wantStored)
				}
				if !ok {
					continue
				}
				wantTotal++
				if c.wantTags == nil {
					if r.TagsJSON != nil {
						t.Errorf("%s TagsJSON = %s; want nil", id, *r.TagsJSON)
					}
				} else {
					assertTagsMap(t, r.TagsJSON, c.wantTags)
				}
			}
			if p.total != wantTotal {
				t.Errorf("total = %d; want %d", p.total, wantTotal)
			}
			assertTagsMap(t, rows["s"].TagsJSON, map[string]string{"team": "s"})
			var warns []store.ScanWarning
			for _, w := range *tagWarns {
				if w.Service == tagTestOp {
					warns = append(warns, w)
				}
			}
			if len(warns) != c.wantWarns {
				t.Fatalf("%s warnings = %v; want %d", tagTestOp, warns, c.wantWarns)
			}
			if c.wantWarns > 0 && warns[0].Scope != testAccountID+"/"+testRegion {
				t.Errorf("warning scope = %q; want %q", warns[0].Scope, testAccountID+"/"+testRegion)
			}
		})
	}
}

// A transient tag read error stays an error alone, and is demoted to one
// warning under the tag op beside another phase's hard error.
func TestStoreTokenListTags_TransientDemotedBesideHardError(t *testing.T) {
	st := newTestStore(t)
	warns := countOpWarnings(st, tagTestOp)
	p, _ := storeTaggedThings(context.Background(), st, []string{"x"}, map[string]rowTagRead{"x": {err: tagTestThrottle}})
	p.addPhase("svc:ListOthers", 0, 0, tagTestInvalid)
	_, _, err := p.result(st, newTestAccount(testAccountID), testRegion)
	if got := classifyServiceError(err); got != outcomeError {
		t.Errorf("classifyServiceError = %v; want outcomeError", got)
	}
	if isAPIErrorCode(err, "ThrottlingException") || errors.Is(err, tagTestThrottle) {
		t.Errorf("err = %v; want the throttle demoted out of it", err)
	}
	if *warns != 1 {
		t.Errorf("%s warnings = %d; want 1 (the demoted throttle)", tagTestOp, *warns)
	}
}

// A failed write sends no warning, since the denial or the demoted throttle
// would describe rows the write lost; the tag reads' errors, the throttle
// included, are returned beside it.
func TestStoreTokenListTags_FailedWriteSendsNoWarning(t *testing.T) {
	st, closeStore := openClosingStore(t)
	warns := countWarnings(st)
	closeStore()
	p, stop := storeTaggedThings(context.Background(), st, []string{"denied", "invalid", "throttled"},
		map[string]rowTagRead{"denied": {err: tagTestDenied}, "invalid": {err: tagTestInvalid}, "throttled": {err: tagTestThrottle}})
	_, _, err := p.result(st, newTestAccount(testAccountID), testRegion)
	if !stop || !errors.Is(err, store.ErrStoreWrite) {
		t.Fatalf("stop %v, err %v; want true, store.ErrStoreWrite", stop, err)
	}
	if !errors.Is(err, tagTestInvalid) || !errors.Is(err, tagTestThrottle) {
		t.Errorf("err = %v; want the ValidationException and ThrottlingException tag read errors kept", err)
	}
	if *warns != 0 {
		t.Errorf("warnings = %d after a failed write; want 0", *warns)
	}
}

// Once ctx has ended no tag is read: every row is stored without tags and the
// stop is returned.
func TestStoreTokenListTags_ContextEndedKeepsRows(t *testing.T) {
	st := newTestStore(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var p phaseTotals
	var reads atomic.Int32
	tags := tagTestReader(nil)
	tags.read = func(context.Context, *store.Resource) (*string, error) {
		reads.Add(1)
		return mapTagsJSON(map[string]string{"k": "v"}), nil
	}
	storeTokenListTags(ctx, st, newTestAccount(testAccountID), testRegion, tagTestListOp,
		func(context.Context, *string) ([]string, *string, error) { return []string{"a", "b"}, nil, nil },
		tagTestRow, tags, &p)
	_, _, err := p.result(st, newTestAccount(testAccountID), testRegion)
	if !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v; want context.Canceled", err)
	}
	if n := reads.Load(); n != 0 {
		t.Errorf("tag reads = %d after ctx ended; want 0", n)
	}
	rows := rowsByNativeID(t, st, TypeNetworkFirewallProxy)
	for _, id := range []string{"a", "b"} {
		r, ok := rows[id]
		if !ok {
			t.Fatalf("%s not stored; want it kept without tags", id)
		}
		if r.TagsJSON != nil {
			t.Errorf("%s TagsJSON = %s; want nil", id, *r.TagsJSON)
		}
	}
}

// A row the tag read no longer finds is neither stored nor returned as a
// parent; a stored row without the child key is warned about and returned as
// no parent.
func TestStoreParentListTags_DroppedRowIsNoParent(t *testing.T) {
	st := newTestStore(t)
	noID := countOpWarnings(st, tagTestListOp)
	var p phaseTotals
	keys := map[string]string{"kept": "k1", "gone": "k2", "nokey": ""}
	parents, stop := storeParentListTags(context.Background(), st, newTestAccount(testAccountID), testRegion, tagTestListOp, "things", "ThingId",
		func(context.Context, *string) ([]string, *string, error) {
			return []string{"kept", "gone", "nokey"}, nil, nil
		},
		tagTestRow, func(id string) string { return keys[id] },
		tagTestReader(map[string]rowTagRead{"gone": {err: tagTestNotFound}}), &p)
	if _, _, err := p.result(st, newTestAccount(testAccountID), testRegion); stop || err != nil {
		t.Fatalf("stop %v, err %v; want false, nil", stop, err)
	}
	if want := []childParent{{id: "k1", arn: "kept"}}; !slices.Equal(parents, want) {
		t.Errorf("parents = %v; want %v", parents, want)
	}
	rows := rowsByNativeID(t, st, TypeNetworkFirewallProxy)
	if _, ok := rows["gone"]; ok || len(rows) != 2 {
		t.Errorf("stored %v; want kept and nokey only", slices.Sorted(maps.Keys(rows)))
	}
	if *noID != 1 {
		t.Errorf("%s warnings = %d; want 1 (nokey stored without its child key)", tagTestListOp, *noID)
	}
}

// A hard error and a throttle from the same tag op: the hard error alone is
// returned, and the throttle is one warning under the tag op.
func TestStoreTokenListTags_HardTagErrorDemotesSameOpThrottle(t *testing.T) {
	st := newTestStore(t)
	warns := recordWarnings(st)
	p, _ := storeTaggedThings(context.Background(), st, []string{"invalid", "throttled"},
		map[string]rowTagRead{"invalid": {err: tagTestInvalid}, "throttled": {err: tagTestThrottle}})
	_, _, err := p.result(st, newTestAccount(testAccountID), testRegion)
	assertAPICode(t, err, "ValidationException")
	if errors.Is(err, tagTestThrottle) {
		t.Errorf("err = %v; want the throttle demoted out of it", err)
	}
	var got []store.ScanWarning
	for _, w := range *warns {
		if w.Service == tagTestOp {
			got = append(got, w)
		}
	}
	if len(got) != 1 || got[0].Scope != testAccountID+"/"+testRegion {
		t.Errorf("%s warnings = %v; want one scoped %s/%s", tagTestOp, got, testAccountID, testRegion)
	}
}

// When ctx ends during the fan-out, the reads it cut short and the rows never
// read add one stop member between them, and every row is kept.
func TestStoreTokenListTags_ContextEndedMidFanOut(t *testing.T) {
	for _, n := range []int{fanoutMed, 3 * fanoutMed} {
		t.Run(strconv.Itoa(n)+" rows", func(t *testing.T) {
			st := newTestStore(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var started atomic.Int32
			tags := tagTestReader(nil)
			// The last of the first fanoutMed reads to start cancels ctx, so
			// every one of them is in flight when it ends.
			tags.read = func(ctx context.Context, _ *store.Resource) (*string, error) {
				if started.Add(1) == fanoutMed {
					cancel()
				}
				<-ctx.Done()
				return nil, fmt.Errorf("operation error: %w", ctx.Err())
			}
			ids := make([]string, n)
			for i := range ids {
				ids[i] = "r" + strconv.Itoa(i)
			}
			var p phaseTotals
			storeTokenListTags(ctx, st, newTestAccount(testAccountID), testRegion, tagTestListOp,
				func(context.Context, *string) ([]string, *string, error) { return ids, nil, nil },
				tagTestRow, tags, &p)
			_, _, err := p.result(st, newTestAccount(testAccountID), testRegion)
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("err = %v; want context.Canceled", err)
			}
			if j, ok := err.(interface{ Unwrap() []error }); !ok || len(j.Unwrap()) != 1 {
				t.Errorf("err = %v; want exactly one stop member", err)
			}
			if got := len(rowsByNativeID(t, st, TypeNetworkFirewallProxy)); got != n {
				t.Errorf("stored %d rows; want all %d", got, n)
			}
		})
	}
}

// A tag read error through storeParentListTags keeps the row and its parent
// and is returned.
func TestStoreParentListTags_TagErrorReturned(t *testing.T) {
	st := newTestStore(t)
	var p phaseTotals
	parents, _ := storeParentListTags(context.Background(), st, newTestAccount(testAccountID), testRegion, tagTestListOp, "things", "ThingId",
		func(context.Context, *string) ([]string, *string, error) { return []string{"invalid"}, nil, nil },
		tagTestRow, func(id string) string { return id },
		tagTestReader(map[string]rowTagRead{"invalid": {err: tagTestInvalid}}), &p)
	_, _, err := p.result(st, newTestAccount(testAccountID), testRegion)
	assertAPICode(t, err, "ValidationException")
	if want := []childParent{{id: "invalid", arn: "invalid"}}; !slices.Equal(parents, want) {
		t.Errorf("parents = %v; want %v", parents, want)
	}
}

// storeRowsTags stores a hand-built batch with its tags, runs the caller's
// warn only after a successful write, and returns the tag errors.
func TestStoreRowsTags(t *testing.T) {
	rows := func() []*store.Resource {
		a, _, _ := tagTestRow("a")
		b, _, _ := tagTestRow("b")
		return []*store.Resource{a, b}
	}
	tags := tagTestReader(map[string]rowTagRead{"a": {tags: mapTagsJSON(map[string]string{"k": "v"})}, "b": {err: tagTestInvalid}})
	t.Run("write succeeds", func(t *testing.T) {
		st := newTestStore(t)
		warned := 0
		var p phaseTotals
		if storeRowsTags(context.Background(), st, newTestAccount(testAccountID), testRegion, tagTestListOp, rows(), nil, func() { warned++ }, tags, &p) {
			t.Fatal("storeWriteFailed = true; want false")
		}
		_, _, err := p.result(st, newTestAccount(testAccountID), testRegion)
		assertAPICode(t, err, "ValidationException")
		got := rowsByNativeID(t, st, TypeNetworkFirewallProxy)
		assertTagsMap(t, got["a"].TagsJSON, map[string]string{"k": "v"})
		if _, ok := got["b"]; !ok || p.total != 2 || warned != 1 {
			t.Errorf("b stored %v, total %d, warn ran %d; want true, 2, 1", ok, p.total, warned)
		}
	})
	t.Run("write fails", func(t *testing.T) {
		st, closeStore := openClosingStore(t)
		closeStore()
		warned := 0
		var p phaseTotals
		if !storeRowsTags(context.Background(), st, newTestAccount(testAccountID), testRegion, tagTestListOp, rows(), nil, func() { warned++ }, tags, &p) {
			t.Fatal("storeWriteFailed = false; want true")
		}
		_, _, err := p.result(st, newTestAccount(testAccountID), testRegion)
		if !errors.Is(err, store.ErrStoreWrite) || !errors.Is(err, tagTestInvalid) || warned != 0 {
			t.Errorf("err %v, warn ran %d; want store.ErrStoreWrite with the tag error, 0", err, warned)
		}
	})
}

// An NXDOMAIN on a tag read, after the listing reached the endpoint, is a
// hard error of this scan, not a region gap that would discard the counts of
// the rows stored.
func TestStoreTokenListTags_NXDOMAINIsHard(t *testing.T) {
	st := newTestStore(t)
	dns := &net.DNSError{Err: "no such host", Name: "svc.us-east-1.amazonaws.com", IsNotFound: true}
	p, _ := storeTaggedThings(context.Background(), st, []string{"a", "b"}, map[string]rowTagRead{"a": {err: dns}})
	_, _, err := p.result(st, newTestAccount(testAccountID), testRegion)
	if got := classifyServiceError(err); got != outcomeError {
		t.Errorf("classifyServiceError = %v; want outcomeError", got)
	}
	if _, ok := rowsByNativeID(t, st, TypeNetworkFirewallProxy)["a"]; !ok || p.total != 2 {
		t.Errorf("a stored %v, total %d; want true, 2", ok, p.total)
	}
}

// storeRowsTags returns the listing's error with the rows, and sends the tag
// denial warning only after a successful write, nil warn included.
func TestStoreRowsTags_ListErrAndDenial(t *testing.T) {
	listErr := apiErr("InvalidNextTokenException", "bad token")
	rows := func() []*store.Resource {
		a, _, _ := tagTestRow("a")
		return []*store.Resource{a}
	}
	tags := tagTestReader(map[string]rowTagRead{"a": {err: tagTestDenied}})
	t.Run("write succeeds", func(t *testing.T) {
		st := newTestStore(t)
		warns := countOpWarnings(st, tagTestOp)
		var p phaseTotals
		storeRowsTags(context.Background(), st, newTestAccount(testAccountID), testRegion, tagTestListOp, rows(), listErr, nil, tags, &p)
		_, _, err := p.result(st, newTestAccount(testAccountID), testRegion)
		if !errors.Is(err, listErr) {
			t.Errorf("err = %v; want the listing error", err)
		}
		if *warns != 1 {
			t.Errorf("%s warnings = %d; want 1 (the denial)", tagTestOp, *warns)
		}
	})
	t.Run("write fails", func(t *testing.T) {
		st, closeStore := openClosingStore(t)
		warns := countWarnings(st)
		closeStore()
		var p phaseTotals
		storeRowsTags(context.Background(), st, newTestAccount(testAccountID), testRegion, tagTestListOp, rows(), nil, nil, tags, &p)
		if *warns != 0 {
			t.Errorf("warnings = %d after a failed write; want 0", *warns)
		}
	})
}

// storeParentListTags files a tag throttle under the tag op, where it is
// demoted beside a hard error; after a failed write it returns no parents and
// warns about none without the child key.
func TestStoreParentListTags_ThrottleUnderTagOpAndFailedWrite(t *testing.T) {
	list := func(context.Context, *string) ([]string, *string, error) { return []string{"x", "nokey"}, nil, nil }
	key := func(id string) string {
		if id == "nokey" {
			return ""
		}
		return id
	}
	tags := tagTestReader(map[string]rowTagRead{"x": {err: tagTestThrottle}})
	t.Run("throttle", func(t *testing.T) {
		st := newTestStore(t)
		warns := countOpWarnings(st, tagTestOp)
		var p phaseTotals
		storeParentListTags(context.Background(), st, newTestAccount(testAccountID), testRegion, tagTestListOp, "things", "ThingId", list, tagTestRow, key, tags, &p)
		p.addPhase("svc:ListOthers", 0, 0, tagTestInvalid)
		if _, _, err := p.result(st, newTestAccount(testAccountID), testRegion); errors.Is(err, tagTestThrottle) {
			t.Errorf("err = %v; want the throttle demoted", err)
		}
		if *warns != 1 {
			t.Errorf("%s warnings = %d; want 1", tagTestOp, *warns)
		}
	})
	t.Run("write fails", func(t *testing.T) {
		st, closeStore := openClosingStore(t)
		warns := countWarnings(st)
		closeStore()
		var p phaseTotals
		parents, stop := storeParentListTags(context.Background(), st, newTestAccount(testAccountID), testRegion, tagTestListOp, "things", "ThingId", list, tagTestRow, key, tagTestReader(nil), &p)
		if !stop || parents != nil || *warns != 0 {
			t.Errorf("stop %v, parents %v, warnings %d; want true, none, 0", stop, parents, *warns)
		}
	})
}

// resultLateNXDOMAIN flattens an NXDOMAIN once rows were stored, so the
// dispatcher keeps their counts; with nothing stored the region stays
// unavailable, and a failed write is left for the dispatcher to see.
func TestPhaseTotalsResultLateNXDOMAIN(t *testing.T) {
	nx := &net.DNSError{Err: "no such host", Name: "svc.us-east-1.amazonaws.com", IsNotFound: true}
	acct := newTestAccount(testAccountID)
	for _, tc := range []struct {
		name    string
		total   int
		err     error
		outcome serviceOutcome
		write   bool
	}{
		{"after rows", 1, nx, outcomeError, false},
		{"nothing stored", 0, nx, outcomeUnavailable, false},
		{"failed write", 1, errors.Join(fmt.Errorf("%w: refused", store.ErrStoreWrite), nx), outcomeStoreWrite, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			p := phaseTotals{total: tc.total}
			p.errs.add("svc:List", tc.err)
			total, _, err := p.resultLateNXDOMAIN(st, acct, testRegion)
			if total != tc.total {
				t.Errorf("total = %d; want %d", total, tc.total)
			}
			if got := classifyServiceError(err); got != tc.outcome {
				t.Errorf("classifyServiceError = %v; want %v (err %v)", got, tc.outcome, err)
			}
			if tc.write && !isDNSNotFound(err) {
				t.Errorf("err = %v; want the NXDOMAIN beside the failed write left as is", err)
			}
		})
	}
}

package aws

import (
	"context"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"net"
	"path/filepath"
	"regexp"
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
			"ec2_network_extra_scanners.go:scanSubnetCidrReservations":  true,
			"kafka_children_scanners.go:scanKafkaChannels":              true,
			"ec2_ipam_resolver_scanners.go:scanIPAMPrefixListResolvers": true,
			"fms_scanners.go:warnFMSListingOnly":                        true,
			"organizations_scanners.go:scanOrgAccounts":                 true,
			"securityhub_scanners.go:warn":                              true,
			"wafv2_scanners.go:scanWAFv2WebACLs":                        true,
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

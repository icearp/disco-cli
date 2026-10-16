package aws

import (
	"context"
	"errors"
	"fmt"
	"net"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"

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

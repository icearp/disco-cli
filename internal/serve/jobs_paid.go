//go:build paid

package serve

import (
	"context"
	"errors"
	"sync/atomic"

	"codeberg.org/icearp/disco/internal/scanrun"
	"codeberg.org/icearp/disco/internal/store"
)

// ErrScanInProgress is returned by Runner.Submit when a scan is already
// running. The handler maps this to 409 scan_in_progress. The container is
// one-shot, so callers should provision a fresh task rather than retry.
var ErrScanInProgress = errors.New("scan already in progress")

// Runner is the one-shot scan executor for a single Fargate container.
// At most one scan runs in-process at a time (`inFlight` atomic guard);
// a second Submit while one is running returns ErrScanInProgress.
//
// `done` is closed when the scan goroutine finishes — the cmd-level main
// loop selects on this channel to trigger graceful shutdown + process
// exit. Callers must NOT submit after `done` is closed.
type Runner struct {
	st       *store.Store
	inFlight atomic.Bool
	done     chan struct{}
	// completedScanID, captured for telemetry / shutdown logging.
	completedScanID atomic.Value // string
	// completedErr captures the run error so cmd can log a friendly summary
	// before exit. nil = succeeded.
	completedErr atomic.Value // error
}

// NewRunner returns a fresh Runner pinned to st.
func NewRunner(st *store.Store) *Runner {
	return &Runner{st: st, done: make(chan struct{})}
}

// Submit allocates a scan row synchronously (so the 202 reply carries a
// concrete scan_id), then runs scanners in the background. Returns
// ErrScanInProgress if a scan is already running in this container.
//
// On scan completion (success or failure) `r.done` is closed; the
// process-level main loop is expected to call srv.Shutdown and exit.
func (r *Runner) Submit(ctx context.Context, req scanrun.Request) (string, error) {
	if !r.inFlight.CompareAndSwap(false, true) {
		return "", ErrScanInProgress
	}
	alloc, err := scanrun.Allocate(r.st, req)
	if err != nil {
		// Allocation failed (unknown provider, store error). Reset the
		// in-flight guard so a corrected retry can proceed against this
		// same container — but in practice Lambda will start a fresh
		// task, so this branch is for local-dev convenience.
		r.inFlight.Store(false)
		return "", err
	}
	go func() {
		defer close(r.done)
		bg := context.WithoutCancel(ctx)
		if execErr := scanrun.Execute(bg, r.st, alloc); execErr != nil {
			r.completedErr.Store(execErr)
		}
		r.completedScanID.Store(alloc.ScanID)
	}()
	return alloc.ScanID, nil
}

// Done returns a channel closed once the scan goroutine exits.
func (r *Runner) Done() <-chan struct{} { return r.done }

// CompletedScanID returns the ID of the scan run by this Runner, or ""
// if Done has not yet fired.
func (r *Runner) CompletedScanID() string {
	v := r.completedScanID.Load()
	if v == nil {
		return ""
	}
	return v.(string)
}

// CompletedErr returns the error returned by scanrun.Run, or nil on
// success or before Done fires.
func (r *Runner) CompletedErr() error {
	v := r.completedErr.Load()
	if v == nil {
		return nil
	}
	return v.(error)
}

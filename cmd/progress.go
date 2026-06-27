package cmd

import (
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"
)

// spinnerFrames is the braille spinner cycle for the transient progress line.
var spinnerFrames = []rune{'⠋', '⠙', '⠹', '⠸', '⠼', '⠴', '⠦', '⠧', '⠇', '⠏'}

// progress renders scan progress to stderr. Permanent per-service / resolve
// lines stream through line(); when enabled (an interactive terminal), a
// transient bottom line animates a spinner so the user can see the scan is
// alive even while a single slow service or the resolve phase is in flight.
// The denominator (services × scopes) isn't knowable up front — Azure
// subscriptions and GCP projects are discovered at scan time — so the spinner
// is an honest liveness indicator (elapsed + completed-unit count), not a %.
//
// All stderr writes funnel through the mutex so the concurrent OnServiceComplete
// callers and the ticker goroutine never garble each other's output. When
// disabled (non-TTY / CI, --no-progress, or --quiet), line() is a plain
// newline-terminated write and no goroutine runs — identical to having no
// spinner at all, keeping CI logs clean and greppable.
type progress struct {
	w       io.Writer
	start   time.Time
	enabled bool

	mu        sync.Mutex
	frame     int
	lastWidth int // display width of the drawn transient line, for \r-overwrite clears

	done     atomic.Int64 // completed (service, scope) units, shown in the spinner
	stopCh   chan struct{}
	stopOnce sync.Once
	wg       sync.WaitGroup
}

// newProgress builds a progress printer over w. When enabled it starts a ticker
// goroutine that animates the spinner until stop() is called.
func newProgress(w io.Writer, start time.Time, enabled bool) *progress {
	p := &progress{w: w, start: start, enabled: enabled, stopCh: make(chan struct{})}
	if enabled {
		p.wg.Go(p.run)
	}
	return p
}

// incDone bumps the completed-unit counter shown in the spinner. Cheap/atomic so
// it runs even under --quiet (keeps the count honest if the spinner ever draws).
func (p *progress) incDone() { p.done.Add(1) }

// line prints a permanent line, clearing the transient spinner first and
// redrawing it after so the spinner stays pinned to the bottom.
func (p *progress) line(s string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.clear()
	_, _ = fmt.Fprintln(p.w, s)
	p.draw()
}

// run animates the spinner on a ticker until stopped.
func (p *progress) run() {
	t := time.NewTicker(120 * time.Millisecond)
	defer t.Stop()
	for {
		select {
		case <-p.stopCh:
			return
		case <-t.C:
			p.mu.Lock()
			p.frame++
			p.draw()
			p.mu.Unlock()
		}
	}
}

// draw writes the transient spinner line in place. Caller holds mu. No-op when
// disabled.
func (p *progress) draw() {
	if !p.enabled {
		return
	}
	status := fmt.Sprintf("%c scanning… %s · %d done",
		spinnerFrames[p.frame%len(spinnerFrames)],
		time.Since(p.start).Round(time.Second), p.done.Load())
	_, _ = fmt.Fprint(p.w, "\r"+status)
	p.lastWidth = utf8.RuneCountInString(status)
}

// clear erases the current transient line via carriage-return + space overwrite
// (no ANSI, so it's portable to non-VT Windows consoles). Caller holds mu.
func (p *progress) clear() {
	if p.lastWidth == 0 {
		return
	}
	_, _ = fmt.Fprint(p.w, "\r"+strings.Repeat(" ", p.lastWidth)+"\r")
	p.lastWidth = 0
}

// stop halts the ticker and clears the transient line. Idempotent; must be
// called before any other writer touches stderr (warnings/errors/summary).
func (p *progress) stop() {
	p.stopOnce.Do(func() {
		close(p.stopCh)
		p.wg.Wait()
		p.mu.Lock()
		p.clear()
		p.mu.Unlock()
	})
}

// isTerminal reports whether w is an interactive character device (a real
// terminal), gating the spinner. A *bytes.Buffer (tests) or a pipe/file (CI,
// redirected stderr) is not, so the spinner stays off there. Uses the stdlib
// ModeCharDevice idiom — no x/term / go-isatty dependency.
func isTerminal(w io.Writer) bool {
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	fi, err := f.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}

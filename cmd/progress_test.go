package cmd

import (
	"bytes"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestProgress_DisabledIsPlain pins the non-TTY/CI contract: a disabled
// printer emits each line as a plain newline-terminated write, no carriage
// return or spinner glyph — keeps redirected stderr/CI logs clean.
func TestProgress_Disabled_PlainLines(t *testing.T) {
	var buf bytes.Buffer
	p := newProgress(&buf, time.Now(), false)
	p.incDone()
	p.line("  [0s]    ec2  global  (1 total, 1 new, 0 changed)")
	p.stop()

	got := buf.String()
	if want := "  [0s]    ec2  global  (1 total, 1 new, 0 changed)\n"; got != want {
		t.Fatalf("disabled output = %q; want exactly %q", got, want)
	}
	if strings.ContainsAny(got, "\r") {
		t.Errorf("disabled output must not contain a carriage return: %q", got)
	}
	for _, f := range spinnerFrames {
		if strings.ContainsRune(got, f) {
			t.Errorf("disabled output must not contain spinner glyph %q: %q", f, got)
		}
	}
}

// TestProgress_EnabledDrawsSpinner verifies the enabled printer animates a
// transient, carriage-return-prefixed spinner line carrying elapsed time +
// completed-unit count, and that a permanent line clears then redraws it.
func TestProgress_Enabled_DrawsAndClears(t *testing.T) {
	var buf bytes.Buffer
	// Construct disabled (no ticker goroutine racing the buffer), then force
	// enabled to drive draw/line/clear synchronously and deterministically.
	p := newProgress(&buf, time.Now(), false)
	p.enabled = true

	p.incDone()
	p.draw()
	if s := buf.String(); !strings.Contains(s, "\r") || !strings.Contains(s, "scanning…") || !strings.Contains(s, "1 done") {
		t.Fatalf("draw() = %q; want a \\r spinner line with 'scanning…' and '1 done'", s)
	}

	buf.Reset()
	p.line("  perma line")
	s := buf.String()
	if !strings.Contains(s, "  perma line\n") {
		t.Errorf("line() must print the permanent line: %q", s)
	}
	if !strings.HasPrefix(s, "\r") {
		t.Errorf("line() must clear the transient spinner first (leading \\r): %q", s)
	}
	if !strings.Contains(s, "scanning…") {
		t.Errorf("line() must redraw the spinner after the permanent line: %q", s)
	}

	// stop() clears the transient line (trailing \r) and is idempotent.
	buf.Reset()
	p.stop()
	if !strings.Contains(buf.String(), "\r") {
		t.Errorf("stop() should clear the transient line via \\r: %q", buf.String())
	}
	p.stop() // must not panic on second call
}

// TestProgress_ConcurrentSafe exercises the production path under -race: an
// enabled printer runs its ticker goroutine while many goroutines call
// line()/incDone() concurrently (mirrors RunScanners fanning OnServiceComplete
// across scanners). All stderr writes must serialize through the mutex.
func TestProgress_ConcurrentSafe(t *testing.T) {
	var buf bytes.Buffer
	p := newProgress(&buf, time.Now(), true) // starts the ticker goroutine

	var wg sync.WaitGroup
	for i := range 50 {
		wg.Go(func() {
			p.incDone()
			p.line(fmt.Sprintf("  line %d", i))
		})
	}
	wg.Wait()
	p.stop()

	if got := p.done.Load(); got != 50 {
		t.Errorf("done count = %d; want 50", got)
	}
}

// TestIsTerminal_BufferIsNotTTY guards the spinner gate: a non-*os.File writer
// (tests, redirected stderr) is never treated as a terminal.
func TestIsTerminal_BufferIsNotTTY(t *testing.T) {
	if isTerminal(&bytes.Buffer{}) {
		t.Error("a *bytes.Buffer must not be reported as a terminal")
	}
}

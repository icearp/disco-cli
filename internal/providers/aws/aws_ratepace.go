package aws

import (
	"context"
	"fmt"
	"os"
	"sync/atomic"
	"time"

	"codeberg.org/icearp/disco/store"
	"golang.org/x/time/rate"
)

// pacer caps a per-(account,region,service) fan-out at a fixed req/s ceiling.
// Size the worker count ABOVE rate × worst-case-latency so this limiter — not the
// worker semaphore — bounds throughput: a fixed semaphore of N only reaches
// N÷latency req/s, under-filling a low-TPS bucket whenever control-plane latency
// is high (the regression that motivated this helper).
//
// Use a pacer ONLY for a high-call-count fan-out against a low *documented*
// per-second API limit. Latency-bound scanners (a handful of calls each) want the
// fanout* concurrency tiers in aws_concurrency.go instead — a limiter there is a
// no-op at best, a regression at worst. Sole user: scanServiceQuotas; an audit of
// every other AWS fan-out found no clear fit (most are low-cardinality, per-parent
// throttled, or better consolidated than paced — see aws/CLAUDE.md "Rate-paced fan-out").
type pacer struct {
	lim   *rate.Limiter
	calls atomic.Int64
}

func newPacer(rps rate.Limit, burst int) *pacer {
	return &pacer{lim: rate.NewLimiter(rps, burst)}
}

// wait paces one request and tallies it for the saturation report. A cancelled ctx
// surfaces as an error so the caller can stop cleanly (return what it has, no error
// row) rather than firing a doomed request.
func (p *pacer) wait(ctx context.Context) error {
	if err := p.lim.Wait(ctx); err != nil {
		return err
	}
	p.calls.Add(1)
	return nil
}

// reportRateDebug emits a one-line saturation report (calls, elapsed, observed
// req/s) when DISCO_SCAN_RATE_DEBUG is set, so a live run can confirm the fan-out
// sits at its intended ceiling. Silent otherwise — zero cost beyond an env read.
func reportRateDebug(st *store.Store, service, scope string, p *pacer, start time.Time) {
	if os.Getenv("DISCO_SCAN_RATE_DEBUG") == "" {
		return
	}
	calls := p.calls.Load()
	elapsed := time.Since(start).Seconds()
	var rps float64
	if elapsed > 0 {
		rps = float64(calls) / elapsed
	}
	st.ReportWarning(store.ScanWarning{
		Provider: "aws",
		Service:  service,
		Scope:    scope,
		Message:  fmt.Sprintf("%d calls in %.1fs = %.1f req/s", calls, elapsed, rps),
	})
}

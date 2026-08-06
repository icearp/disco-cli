package aws

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/servicequotas"
	sqtypes "github.com/aws/aws-sdk-go-v2/service/servicequotas/types"
	"github.com/icearp/disco-cli/store"
	"golang.org/x/sync/semaphore"
	"golang.org/x/time/rate"
)

const (
	// sqReqPerSec is the documented steady limit PER OPERATION, PER REGION -- not
	// a budget shared across the three calls this scanner makes, and not an
	// account-wide one.
	//
	// Both halves of that are checkable from the scanner's own output, which is
	// the point: Service Quotas publishes its own API limits as quotas.
	// L-65470577 "Throttle rate for ListServiceQuotas", L-71DCD22A "Throttle rate
	// for ListAWSDefaultServiceQuotas" and L-E3924FE5 "Throttle rate for
	// ListServices" are three distinct quotas, each 10, and each is reported
	// SEPARATELY IN EVERY REGION (17 of them on the account this was measured on)
	// rather than once under the region-less "global" sentinel -- which is how
	// that same table records L-0C8306D7 "Active requests per account". Regional
	// reporting is what makes them per-region buckets.
	//
	// Pacing all three through ONE limiter therefore spent a single budget against
	// three meters and ran each call below its allowance; see [sqPacers]. To
	// disprove any of this, re-run scripts/sql/check-servicequotas-api-limits.sql
	// against a scanned account in the SaaS, or `disco quotas --service
	// servicequotas` locally.
	sqReqPerSec = rate.Limit(10)
	sqBurst     = 10 // documented burst allowance
	// sqWorkers ≥ rate × worst-case latency (~3s): enough concurrency that the rate
	// limiter — not the semaphore — bounds throughput. A fixed cap of 10 only reaches
	// 10 req/s when calls take ~1s; at higher latency it under-utilizes the bucket —
	// the regression this sizing avoids.
	sqWorkers = 30
)

func init() {
	// No registerType. A service quota is a limit value, not a resource: it has
	// no graph edges, nothing provisions it, and it is queried by service and
	// by proximity to the limit. It lands in the `quotas` table instead, which
	// also keeps the provider's catalogue size — nine rows in ten, historically
	// — from dictating how the inventory read path performs.
	//
	// global:false (default) — the harness dispatches this scanner once per
	// enabled region; it fans out over service codes within that region.
	// optIn:true — the scan is far slower than any resource scanner, so it is
	// excluded from a default `disco scan aws`. Select it with
	// --include-service-quotas or --services aws:servicequotas.
	registerService(serviceEntry{
		name:  "aws:servicequotas",
		optIn: true,
		fn:    scanServiceQuotas,
	})
}

// sqPacers holds one rate limiter per Service Quotas operation, because AWS
// meters them separately.
//
// The previous shape was a single *pacer shared by all three calls. That is the
// intuitive reading of "the API allows 10 req/s" and it is wrong: each operation
// carries its own 10 req/s quota, so one shared limiter spent one budget on
// three meters. The scanner makes one ListServiceQuotas and one
// ListAWSDefaultServiceQuotas per service code, so those two were each getting
// about half the rate they were entitled to.
//
// Splitting them changes no per-operation pressure -- each limiter still sits at
// exactly the documented ceiling -- while letting the defaults pass overlap the
// limits pass instead of queueing behind it.
//
// ListServices is called once per region and could not saturate anything; it has
// its own limiter so that a future caller cannot silently borrow from the
// per-code budgets.
type sqPacers struct {
	services *pacer
	quotas   *pacer
	defaults *pacer
}

func newSQPacers() *sqPacers {
	return &sqPacers{
		services: newPacer(sqReqPerSec, sqBurst),
		quotas:   newPacer(sqReqPerSec, sqBurst),
		defaults: newPacer(sqReqPerSec, sqBurst),
	}
}

// serviceQuotasAPI is the narrow Service Quotas surface used by the scanner
// body. *servicequotas.Client satisfies it; tests inject a hand-rolled stub.
type serviceQuotasAPI interface {
	ListServices(context.Context, *servicequotas.ListServicesInput, ...func(*servicequotas.Options)) (*servicequotas.ListServicesOutput, error)
	ListServiceQuotas(context.Context, *servicequotas.ListServiceQuotasInput, ...func(*servicequotas.Options)) (*servicequotas.ListServiceQuotasOutput, error)
	ListAWSDefaultServiceQuotas(context.Context, *servicequotas.ListAWSDefaultServiceQuotasInput, ...func(*servicequotas.Options)) (*servicequotas.ListAWSDefaultServiceQuotasOutput, error)
}

// scanServiceQuotas records service-quota limits (not usage) for one region into
// the `quotas` table. ServiceQuota carries no usage value, etag, or timestamp,
// so each row is limit-only and its version bumps only on a real limit change —
// clean change-over-time history, no churn.
//
// Both adjustable and non-adjustable limits are recorded. Non-adjustable is the
// more interesting class, not an afterthought: a hard ceiling moves only when
// AWS moves it, with no customer request and no notification, so its version
// history is the only way to see that happen.
//
// Enumerates every service code via ListServices, then fans ListServiceQuotas
// out over them bounded by sqWorkers and paced by a per-region rate limiter at
// the documented 10 req/s ceiling (sqReqPerSec). The limiter — not the worker
// count — holds the ceiling, so throughput stays ~10 req/s regardless of
// control-plane latency with no throttling overshoot. MaxResults=100 (the API
// max, set per call below) keeps the page count near one-per-service, so the
// wall-time floor stays at calls÷10req/s. The whole scanner is opt-in
// (optIn:true); see --include-service-quotas.
func scanServiceQuotas(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := servicequotas.NewFromConfig(acct.cfg, func(o *servicequotas.Options) { o.Region = region })
	return scanServiceQuotasWithClient(ctx, client, acct, region, st, scanID, newSQPacers())
}

func scanServiceQuotasWithClient(ctx context.Context, client serviceQuotasAPI, acct *account, region string, st *store.Store, scanID string, pacers *sqPacers) (total, inserted int, err error) {
	start := time.Now()
	codes, err := listQuotaServiceCodes(ctx, client, pacers.services)
	if err != nil {
		if isAccessDenied(err) {
			return 0, 0, skipIfAccessDenied(st, "servicequotas:ListServices", acct.ID, region, err)
		}
		return 0, 0, fmt.Errorf("servicequotas:ListServices: %w", err)
	}
	home := homeGlobalRegion(acct)

	var (
		mu              sync.Mutex
		batch           []*store.Quota
		missingDefaults defaultsOutcome
	)
	sem := semaphore.NewWeighted(sqWorkers)
	var wg sync.WaitGroup
	for _, code := range codes {
		wg.Go(func() {
			if err := sem.Acquire(ctx, 1); err != nil {
				return
			}
			defer sem.Release(1)
			rows, derr := listQuotasForCode(ctx, client, code, acct, region, home, scanID, &missingDefaults, pacers)
			if derr != nil {
				// Collect-and-continue: one bad service code must not drop the
				// rows already gathered for the others in this region.
				st.ReportError(store.ScanError{
					Provider: "aws",
					Service:  "servicequotas:ListServiceQuotas",
					Scope:    acct.ID + "/" + region + "/" + code,
					Message:  derr.Error(),
				})
				return
			}
			if len(rows) == 0 {
				return
			}
			mu.Lock()
			batch = append(batch, rows...)
			mu.Unlock()
		})
	}
	wg.Wait()
	missingDefaults.report(st, acct.ID, region)
	// One line per operation: a shared line could not show WHICH meter is
	// saturated, which is the only question the report exists to answer.
	reportRateDebug(st, "servicequotas:ListServices", region, pacers.services, start)
	reportRateDebug(st, "servicequotas:ListServiceQuotas", region, pacers.quotas, start)
	reportRateDebug(st, "servicequotas:ListAWSDefaultServiceQuotas", region, pacers.defaults, start)

	if len(batch) == 0 {
		return 0, 0, nil
	}
	n, err := st.UpsertQuotas(batch)
	if err != nil {
		return 0, 0, fmt.Errorf("upsert service quotas: %w", err)
	}
	return len(batch), n, nil
}

// listQuotaServiceCodes enumerates every service code Service Quotas knows about
// in this region (manual NextToken pagination so the test stub needs one method).
//
// Paced on its own limiter. It is a handful of calls and could not saturate
// anything, but ListServices carries its own documented 10 req/s quota, so
// pacing it here keeps every call this scanner makes accounted for on the meter
// it actually bills to.
//
// Cancellation is an ERROR here, unlike in the per-code calls below, and the
// asymmetry is deliberate. This list decides which service codes the region
// scans at all, so returning a truncated one with a nil error would report a
// scan that silently covered fewer services as a complete scan. A partial page
// of QUOTAS is a smaller loss than a partial page of SERVICES.
func listQuotaServiceCodes(ctx context.Context, client serviceQuotasAPI, pacer *pacer) ([]string, error) {
	var codes []string
	in := &servicequotas.ListServicesInput{MaxResults: sdkaws.Int32(100)}
	for {
		if err := pacer.wait(ctx); err != nil {
			return nil, err
		}
		out, err := client.ListServices(ctx, in)
		if err != nil {
			return nil, err
		}
		for i := range out.Services {
			if c := sv(out.Services[i].ServiceCode); c != "" {
				codes = append(codes, c)
			}
		}
		if out.NextToken == nil {
			return codes, nil
		}
		in.NextToken = out.NextToken
	}
}

// listQuotasForCode pages the applied quotas for one service code, pairing each
// with the AWS default so applied-versus-default is answerable without a second
// query at read time. A code that is unsupported / not subscribed in this region
// returns NoSuchResourceException (or IllegalArgumentException for a malformed
// code); both are skipped silently, mirroring Azure's isSkippableScanError.
// QuotaAppliedAtLevel is left unset so the proxy returns ACCOUNT-level quotas
// (ALL would add churny per-resource rows).
func listQuotasForCode(ctx context.Context, client serviceQuotasAPI, code string, acct *account, region, home, scanID string, missing *defaultsOutcome, pacers *sqPacers) ([]*store.Quota, error) {
	defaults, derr := listQuotaDefaultsForCode(ctx, client, code, pacers.defaults)
	missing.record(code, derr)

	var rows []*store.Quota
	in := &servicequotas.ListServiceQuotasInput{ServiceCode: &code, MaxResults: sdkaws.Int32(100)}
	for {
		// Pace each request (incl. paginated follow-ups) on the ListServiceQuotas
		// meter specifically -- the defaults pass bills to its own, see
		// [sqPacers]. ctx cancellation surfaces as a clean stop, not an error row.
		if err := pacers.quotas.wait(ctx); err != nil {
			return rows, nil
		}
		out, err := client.ListServiceQuotas(ctx, in)
		if err != nil {
			if isAccessDenied(err) || isAPIErrorCode(err, "NoSuchResourceException", "IllegalArgumentException") {
				return rows, nil
			}
			return nil, fmt.Errorf("servicequotas:ListServiceQuotas %s/%s: %w", code, region, err)
		}
		for i := range out.Quotas {
			if r, ok := quotaRow(acct, region, home, scanID, out.Quotas[i], defaults); ok {
				rows = append(rows, r)
			}
		}
		if out.NextToken == nil {
			return rows, nil
		}
		in.NextToken = out.NextToken
	}
}

// defaultsOutcome accumulates ListAWSDefaultServiceQuotas failures across the
// per-service-code fan-out so the scan record carries ONE entry per region
// instead of one per code.
//
// The aggregation is the point, not tidiness. A systematic failure -- throttling,
// a missing grant -- fails EVERY service code, and this scanner fans out over
// 251 of them in each of 18 regions. Reporting per code would put ~4,500 near
// identical rows on a single scan, which is not a louder signal than one row but
// a quieter one: it buries every other error in the scan and inflates the stored
// error payload. Counting them and reporting once keeps the failure visible and
// the record readable.
//
// Safe for concurrent use; report is called after the fan-out has joined.
// The service code travels with the kept error. Aggregating without it left a
// one-failure report saying only "1 service code(s)" -- which names the count
// and hides the only field that makes it actionable.
//
// "first" is arrival order across a concurrent fan-out, so the kept pair is an
// EXAMPLE, not a stable or meaningful choice. report says so.
type defaultsOutcome struct {
	mu        sync.Mutex
	failures  int
	denied    error  // first AccessDenied to arrive, if any
	deniedFor string // its service code
	first     error  // first failure of any other kind to arrive
	firstFor  string // its service code
}

// record notes one service code's failure. A nil error is a success and is
// ignored, so callers need not branch.
func (d *defaultsOutcome) record(code string, err error) {
	if err == nil {
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	d.failures++
	switch {
	case isAccessDenied(err):
		if d.denied == nil {
			d.denied, d.deniedFor = err, code
		}
	case d.first == nil:
		d.first, d.firstFor = err, code
	}
}

// report puts at most one entry on the scan record for the whole region.
//
// AccessDenied goes through skipIfAccessDenied, which drops the region-gap and
// not-entitled cases and warns otherwise: a missing
// servicequotas:ListAWSDefaultServiceQuotas grant is a policy fix, not a fault,
// but it must not be silent -- it would null every default on every scan.
// Anything else is a genuine error.
func (d *defaultsOutcome) report(st *store.Store, acctID, region string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	const op = "servicequotas:ListAWSDefaultServiceQuotas"
	if d.denied != nil {
		_ = skipIfAccessDenied(st, op, acctID, region, fmt.Errorf("%s: %w", d.deniedFor, d.denied))
	}
	if d.first == nil {
		return
	}
	st.ReportError(store.ScanError{
		Provider: "aws",
		Service:  op,
		Scope:    acctID + "/" + region,
		Message: fmt.Sprintf("default quota values unavailable for %d service code(s); example (%s): %v",
			d.failures, d.firstFor, d.first),
	})
}

// listQuotaDefaultsForCode returns the AWS default value per quota code for one
// service, and the error that stopped it. A code absent from the map has no
// default this call could read.
//
// This doubles the API calls the scanner makes, which is why it is worth
// stating what it buys: on an adjustable quota a divergence from the default is
// an increase the customer requested, and on a non-adjustable one it means AWS
// moved a hard ceiling. Neither is derivable from the applied value alone, and
// recovering it later would mean this same call anyway.
//
// THE ERROR IS RETURNED, NOT SWALLOWED, and the distinction matters more than it
// looks. The applied limit is still recorded either way -- refusing to store it
// because the default was unavailable would lose the more important of the two --
// but a silent failure leaves default_value NULL, and NULL is not a neutral
// outcome: [currentQuotaRow.unchanged] compares DefaultValue, so the next scan
// that DOES read the default records a version bump on a limit that never moved.
// On a six-figure quota table that is ~180 MB of fabricated history per
// occurrence, and it presents as real change in the "limits that changed" view.
// An unreported failure therefore does not degrade gracefully; it manufactures
// data. Returning it is what makes the cause findable from the scan record
// instead of from a heap size.
//
// The caller aggregates: see [defaultsOutcome] for why this must not report
// per service code itself.
func listQuotaDefaultsForCode(ctx context.Context, client serviceQuotasAPI, code string, pacer *pacer) (map[string]float64, error) {
	defaults := map[string]float64{}
	in := &servicequotas.ListAWSDefaultServiceQuotasInput{ServiceCode: &code, MaxResults: sdkaws.Int32(100)}
	for {
		// Cancellation is the scan stopping, not this call failing. It would
		// otherwise be counted once per remaining service code and reported as
		// the reason defaults are missing, which it is not.
		if err := pacer.wait(ctx); err != nil {
			return defaults, nil
		}
		out, err := client.ListAWSDefaultServiceQuotas(ctx, in)
		if err != nil {
			// A service that publishes no defaults answers NoSuchResource. That
			// is an answer, not a failure, and is silent for the same reason
			// listQuotasForCode skips it silently.
			if isAPIErrorCode(err, "NoSuchResourceException", "IllegalArgumentException") {
				return defaults, nil
			}
			return defaults, err
		}
		for i := range out.Quotas {
			qc, v := sv(out.Quotas[i].QuotaCode), out.Quotas[i].Value
			if qc != "" && v != nil {
				defaults[qc] = *v
			}
		}
		if out.NextToken == nil {
			return defaults, nil
		}
		in.NextToken = out.NextToken
	}
}

// quotaRow builds a store.Quota for one limit, or (nil,false) to drop it.
//
// Identity is (provider, account, region, service code, quota code), so a quota
// missing either code cannot be addressed and is dropped. The QuotaArn is not
// the identity — it is preserved in the attributes remainder alongside every
// other field AWS reported.
//
// Global quotas are returned identically from every region; they are recorded
// once — at the elected home region, under the region-less "global" sentinel —
// so the row's identity never depends on which region emitted it.
func quotaRow(acct *account, region, home, scanID string, q sqtypes.ServiceQuota, defaults map[string]float64) (*store.Quota, bool) {
	serviceCode, quotaCode := sv(q.ServiceCode), sv(q.QuotaCode)
	if serviceCode == "" || quotaCode == "" {
		return nil, false
	}
	if q.GlobalQuota {
		if region != home {
			return nil, false
		}
		region = *regionGlobal
	}

	out := &store.Quota{
		Provider:       "aws",
		AccountID:      acct.ID,
		AccountName:    &acct.Name,
		Region:         region,
		ServiceCode:    serviceCode,
		QuotaCode:      quotaCode,
		DimensionKey:   quotaDimensionKey(q),
		Name:           sv(q.QuotaName),
		Value:          q.Value,
		Adjustable:     q.Adjustable,
		SubAccountType: sp(quotaSubAccountType),
		AttributesJSON: mustJSON(q),
		DiscoveredBy:   scanID,
	}
	if desc := sv(q.Description); desc != "" {
		out.Description = &desc
	}
	if name := sv(q.ServiceName); name != "" {
		out.ServiceName = &name
	}
	if unit := sv(q.Unit); unit != "" {
		out.Unit = &unit
	}
	if q.QuotaContext != nil {
		if rt := sv(q.QuotaContext.ContextScopeType); rt != "" {
			out.ResourceType = &rt
		}
	}
	if q.Period != nil {
		if unit := quotaPeriodUnit(q.Period.PeriodUnit); unit != "" {
			out.PeriodUnit = &unit
		}
		if q.Period.PeriodValue != nil {
			v := int(*q.Period.PeriodValue)
			out.PeriodValue = &v
		}
	}
	if def, ok := defaults[quotaCode]; ok {
		out.DefaultValue = &def
	}
	return out, true
}

// quotaSubAccountType is FOCUS SubAccountType for every AWS quota: the
// container an account_id names is an AWS account, always. Azure writes
// "Subscription" and GCP will write "Project".
const quotaSubAccountType = "Account"

// quotaDimensionKey returns the dimension this limit's value belongs to, or ""
// when the limit is undimensioned.
//
// AWS spells "every resource" as the literal "*", which is the ABSENCE of a
// dimension, not a dimension named "*". Storing it verbatim would put all 1,875
// context-carrying rows on a new natural key and strand their predecessors as
// current rows no later scan could reach.
func quotaDimensionKey(q sqtypes.ServiceQuota) string {
	if q.QuotaContext == nil {
		return ""
	}
	if id := sv(q.QuotaContext.ContextId); id != "*" {
		return id
	}
	return ""
}

// quotaPeriodUnit normalizes AWS's SCREAMING_CASE period unit to the lowercase
// singular the column stores, so an AWS "SECOND" and an Azure "PT1S" land on
// the same value. An unrecognized unit returns "" rather than being stored
// as-is: the column carries a CHECK constraint, and a silent NULL beats a
// migration-time failure on a value AWS added after this shipped.
func quotaPeriodUnit(u sqtypes.PeriodUnit) string {
	switch u {
	case sqtypes.PeriodUnitMicrosecond, sqtypes.PeriodUnitMillisecond,
		sqtypes.PeriodUnitSecond, sqtypes.PeriodUnitMinute,
		sqtypes.PeriodUnitHour, sqtypes.PeriodUnitDay, sqtypes.PeriodUnitWeek:
		return strings.ToLower(string(u))
	default:
		return ""
	}
}

// homeGlobalRegion elects the single region that emits global quotas: us-east-1
// (AWS's commercial home region for global quota increases) when scanned,
// otherwise the lexicographically smallest scanned region so globals are still
// recorded when --regions excludes us-east-1. The elected row is region-less, so
// the choice only governs which scan invocation writes it — never the row itself.
func homeGlobalRegion(acct *account) string {
	if len(acct.Regions) == 0 || slices.Contains(acct.Regions, "us-east-1") {
		return "us-east-1"
	}
	return slices.Min(acct.Regions)
}

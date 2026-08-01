package aws

import (
	"context"
	"fmt"
	"slices"
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
	sqReqPerSec = rate.Limit(10) // documented ListServiceQuotas steady limit, per region per account
	sqBurst     = 10             // documented burst allowance
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
	pacer := newPacer(sqReqPerSec, sqBurst)
	return scanServiceQuotasWithClient(ctx, client, acct, region, st, scanID, pacer)
}

func scanServiceQuotasWithClient(ctx context.Context, client serviceQuotasAPI, acct *account, region string, st *store.Store, scanID string, pacer *pacer) (total, inserted int, err error) {
	start := time.Now()
	codes, err := listQuotaServiceCodes(ctx, client)
	if err != nil {
		if isAccessDenied(err) {
			return 0, 0, skipIfAccessDenied(st, "servicequotas:ListServices", acct.ID, region, err)
		}
		return 0, 0, fmt.Errorf("servicequotas:ListServices: %w", err)
	}
	home := homeGlobalRegion(acct)

	var (
		mu    sync.Mutex
		batch []*store.Quota
	)
	sem := semaphore.NewWeighted(sqWorkers)
	var wg sync.WaitGroup
	for _, code := range codes {
		wg.Go(func() {
			if err := sem.Acquire(ctx, 1); err != nil {
				return
			}
			defer sem.Release(1)
			rows, derr := listQuotasForCode(ctx, client, code, acct, region, home, scanID, pacer)
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
	reportRateDebug(st, "servicequotas", region, pacer, start)

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
func listQuotaServiceCodes(ctx context.Context, client serviceQuotasAPI) ([]string, error) {
	var codes []string
	in := &servicequotas.ListServicesInput{MaxResults: sdkaws.Int32(100)}
	for {
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
func listQuotasForCode(ctx context.Context, client serviceQuotasAPI, code string, acct *account, region, home, scanID string, pacer *pacer) ([]*store.Quota, error) {
	defaults := listQuotaDefaultsForCode(ctx, client, code, pacer)

	var rows []*store.Quota
	in := &servicequotas.ListServiceQuotasInput{ServiceCode: &code, MaxResults: sdkaws.Int32(100)}
	for {
		// Pace each request (incl. paginated follow-ups) to the per-region 10 req/s
		// bucket; ctx cancellation surfaces as a clean stop, not an error row.
		if err := pacer.wait(ctx); err != nil {
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

// listQuotaDefaultsForCode returns the AWS default value per quota code for one
// service, or nil when they cannot be read.
//
// This doubles the API calls the scanner makes, which is why it is worth
// stating what it buys: on an adjustable quota a divergence from the default is
// an increase the customer requested, and on a non-adjustable one it means AWS
// moved a hard ceiling. Neither is derivable from the applied value alone, and
// recovering it later would mean this same call anyway.
//
// Failures degrade rather than propagate. A missing default leaves the column
// NULL, which reads as unknown; refusing to record the applied limit because the
// default was unavailable would lose the more important of the two.
func listQuotaDefaultsForCode(ctx context.Context, client serviceQuotasAPI, code string, pacer *pacer) map[string]float64 {
	defaults := map[string]float64{}
	in := &servicequotas.ListAWSDefaultServiceQuotasInput{ServiceCode: &code, MaxResults: sdkaws.Int32(100)}
	for {
		if err := pacer.wait(ctx); err != nil {
			return defaults
		}
		out, err := client.ListAWSDefaultServiceQuotas(ctx, in)
		if err != nil {
			return defaults
		}
		for i := range out.Quotas {
			qc, v := sv(out.Quotas[i].QuotaCode), out.Quotas[i].Value
			if qc != "" && v != nil {
				defaults[qc] = *v
			}
		}
		if out.NextToken == nil {
			return defaults
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
		Name:           sv(q.QuotaName),
		Value:          q.Value,
		Adjustable:     q.Adjustable,
		GlobalQuota:    q.GlobalQuota,
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
	if level := string(q.QuotaAppliedAtLevel); level != "" {
		out.AppliedLevel = &level
	}
	if def, ok := defaults[quotaCode]; ok {
		out.DefaultValue = &def
	}
	return out, true
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

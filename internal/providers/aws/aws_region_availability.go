package aws

import (
	"context"
	"strings"
	"sync"

	"codeberg.org/icearp/disco/store"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	"golang.org/x/sync/errgroup"
)

// Region scoping pre-filters each regional service to the regions where AWS
// actually offers it, so dormant (service × region) cells are never dispatched.
// The source of truth is AWS's own SSM global-infrastructure catalog — a public
// parameter tree under /aws/service/global-infrastructure. Because it is AWS's
// authoritative availability data, a region it omits for a service is one where
// that service's API genuinely isn't reachable (we'd NXDOMAIN / error anyway), so
// trusting it cannot lose coverage the API would have served. The only failure
// mode that could is a wrong service-code mapping; that is contained by the
// fail-open design (an unknown / divergent code is scanned everywhere) plus the
// unique endpoint-prefix convention behind regionAvailabilityCode.

// ssmRegionAvailabilityAPI is the test seam for the global-infrastructure lookup.
// *ssm.Client satisfies it; tests inject a stub.
type ssmRegionAvailabilityAPI interface {
	GetParametersByPath(context.Context, *ssm.GetParametersByPathInput, ...func(*ssm.Options)) (*ssm.GetParametersByPathOutput, error)
}

// regionAvailabilityCodeOverrides maps a disco service name (the registerService
// "name" field) to its AWS global-infrastructure service code, for the services
// whose code diverges from the derived default (the name minus the "aws:" prefix
// — e.g. "aws:code-build" derives "code-build" but the catalog code is
// "codebuild", "aws:directory-service" → "ds"). Intentionally empty by default:
// pure derivation already optimises every service whose name matches its code,
// and a divergent name simply isn't found in the catalog (→ fail-open, scanned
// everywhere). Populating an entry here UNLOCKS scoping for a divergent service —
// only add a mapping verified against the live catalog (see the validation step
// in plans/), since a wrong override is the one way to skip a region the service
// actually serves.
var regionAvailabilityCodeOverrides = map[string]string{}

// regionAvailabilityCode returns the AWS global-infrastructure service code for a
// disco service name: an explicit override if present, else the name minus its
// "aws:" prefix.
func regionAvailabilityCode(name string) string {
	if code, ok := regionAvailabilityCodeOverrides[name]; ok {
		return code
	}
	return strings.TrimPrefix(name, "aws:")
}

// serviceAvailableInRegion reports whether a service should be scanned in a
// region. This is the load-bearing fail-open decision: scan (true) unless the
// code is KNOWN to the catalog (present with ≥1 region) AND region is absent from
// its set. A nil map, an unknown/divergent code, or an empty set all yield true,
// so any gap in the availability data degrades to "scan it anyway".
func serviceAvailableInRegion(availByCode map[string]map[string]bool, code, region string) bool {
	regions, known := availByCode[code]
	if !known || len(regions) == 0 {
		return true
	}
	return regions[region]
}

// loadServiceRegionAvailability resolves, for each distinct service code, the set
// of regions where AWS offers it, by paging the SSM global-infrastructure tree
// at /aws/service/global-infrastructure/services/<code>/regions. Lookups run
// concurrently (bounded by fanoutMed) and fail open per code: a code whose lookup
// errors or returns no parameters is omitted from the result (→ scanned
// everywhere). The returned error is the first access-denied seen — the caller
// uses it only to warn that the optimisation is off; it is never fatal.
func loadServiceRegionAvailability(ctx context.Context, client ssmRegionAvailabilityAPI, codes []string) (map[string]map[string]bool, error) {
	var (
		mu      sync.Mutex
		out     = make(map[string]map[string]bool, len(codes))
		denyErr error
		g, gctx = errgroup.WithContext(ctx)
	)
	g.SetLimit(fanoutMed)
	for _, code := range codes {
		g.Go(func() error {
			path := "/aws/service/global-infrastructure/services/" + code + "/regions"
			regions := map[string]bool{}
			pager := ssm.NewGetParametersByPathPaginator(client, &ssm.GetParametersByPathInput{
				Path: &path,
			}, func(o *ssm.GetParametersByPathPaginatorOptions) { o.Limit = 10 })
			for pager.HasMorePages() {
				page, err := pager.NextPage(gctx)
				if err != nil {
					// Fail open: this code just won't be scoped. Record the first
					// access-denied so the caller can note the optimisation is off.
					if isAccessDenied(err) {
						mu.Lock()
						if denyErr == nil {
							denyErr = err
						}
						mu.Unlock()
					}
					return nil
				}
				for _, p := range page.Parameters {
					if r := sv(p.Value); r != "" {
						regions[r] = true
					}
				}
			}
			if len(regions) > 0 {
				mu.Lock()
				out[code] = regions
				mu.Unlock()
			}
			return nil
		})
	}
	_ = g.Wait() // per-code errors are swallowed (fail-open); none propagate
	return out, denyErr
}

// buildRegionAvailability populates acct.availByCode from the SSM global-infra
// catalog, so scanRegion can skip services AWS doesn't offer in a region. No-op
// (leaves availByCode nil → full fail-open) when scoping is disabled or only one
// region is scanned (nothing to scope). The SSM client is pinned to us-east-1,
// where the commercial-partition global-infra parameters live. A blanket
// access-denied (no data at all) warns once that the optimisation is off.
func buildRegionAvailability(ctx context.Context, acct *account, services []string, st *store.Store, kept []string) {
	if acct.regionScopeDisabled || len(kept) <= 1 {
		return
	}
	codes := distinctRegionAvailabilityCodes(filteredServices(services))
	if len(codes) == 0 {
		return
	}
	client := ssm.NewFromConfig(acct.cfg, func(o *ssm.Options) { o.Region = "us-east-1" })
	avail, denyErr := loadServiceRegionAvailability(ctx, client, codes)
	if len(avail) == 0 {
		if denyErr != nil {
			st.ReportWarning(store.ScanWarning{
				Provider: "aws", Service: "preflight:region-scope", Scope: acct.ID,
				Message: "could not read service region availability (ssm:GetParametersByPath denied); scanning all configured regions",
			})
		}
		return
	}
	acct.availByCode = avail
}

// distinctRegionAvailabilityCodes returns the deduped global-infrastructure codes
// for a set of regional services, ready for loadServiceRegionAvailability.
func distinctRegionAvailabilityCodes(services []serviceEntry) []string {
	seen := map[string]bool{}
	var codes []string
	for _, svc := range services {
		if svc.global {
			continue
		}
		code := regionAvailabilityCode(svc.name)
		if code != "" && !seen[code] {
			seen[code] = true
			codes = append(codes, code)
		}
	}
	return codes
}

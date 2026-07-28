package aws

import (
	"context"
	"strings"
	"sync"

	"github.com/aws/aws-sdk-go-v2/service/ssm"
	"github.com/icearp/disco-cli/regions"
	"github.com/icearp/disco-cli/store"
	"golang.org/x/sync/errgroup"
)

// Region scoping pre-filters each regional service to the regions AWS actually
// offers it in, so dormant (service × region) cells are never dispatched. Two
// independent sources answer that question, and serviceAvailableInRegion
// combines them:
//
//   - The SDK region table, via the regions package. Generated from the endpoint
//     table aws-sdk-go-v2 embeds in each service package, so it is keyed by the
//     SDK PACKAGE a scanner imports and needs no name-to-code mapping at all. It
//     ships with the binary: free to consult, but it can lag a region launch.
//   - The SSM global-infrastructure catalog below, a public parameter tree under
//     /aws/service/global-infrastructure. Live, so it reflects a launch
//     immediately, but reaching it costs a paged API call per service code and
//     depends on the scanned account granting ssm:GetParametersByPath.
//
// Both are AWS's own data, so a region either one lists is genuinely served. The
// design is fail-open throughout: a service neither source has an opinion on is
// scanned everywhere, and a stale shipped table is corrected by the live one.

// ssmRegionAvailabilityAPI is the test seam for the global-infrastructure lookup.
// *ssm.Client satisfies it; tests inject a stub.
type ssmRegionAvailabilityAPI interface {
	GetParametersByPath(context.Context, *ssm.GetParametersByPathInput, ...func(*ssm.Options)) (*ssm.GetParametersByPathOutput, error)
}

// regionAvailabilityCodeOverrides maps a disco service name (the registerService
// "name" field) to its AWS global-infrastructure service code, for services whose
// code diverges from the derived default (name minus the "aws:" prefix — e.g.
// "aws:code-build" derives "code-build" but the catalog code is "codebuild",
// "aws:directory-service" → "ds"). Empty, and expected to stay that way: the SDK
// region table covers the divergent-name services without any mapping (it joins
// on the imported SDK package, not the name), which is why 83 of 297 services
// being unreachable by derived code no longer costs them their scoping.
//
// Populating an entry here UNLOCKS the catalog for that service. Only add a
// mapping verified against the live catalog — a wrong override is the one way to
// skip a region the service actually serves.
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
// region, combining the shipped SDK table with the live SSM catalog. name is the
// disco service name ("aws:cassandra"); code is its global-infrastructure code.
//
// The rule: scan when EITHER source affirmatively lists the region. Skip only
// when at least one source has an opinion and none of them lists it. When
// neither has an opinion — a nil catalog map, an unknown or divergent code, a
// service whose scanner imports no SDK package — scan.
//
// Taking the union rather than either source alone is what makes a stale shipped
// table safe: a region AWS launched after the pinned SDK release is missing from
// the generated table but present in the catalog the moment it opens, and the
// disagreement resolves toward scanning. The reverse — the catalog omitting
// something the SDK ships — resolves the same way.
func serviceAvailableInRegion(availByCode map[string]map[string]bool, name, code, region string) bool {
	sdkKnown, sdkAvail := regions.ServiceAvailable(name, region)
	if sdkKnown && sdkAvail {
		return true
	}
	catalog, catalogKnown := availByCode[code]
	if len(catalog) == 0 {
		catalogKnown = false
	}
	if catalogKnown && catalog[region] {
		return true
	}
	return !sdkKnown && !catalogKnown
}

// loadServiceRegionAvailability resolves the region set where AWS offers each
// distinct service code, by paging the SSM global-infrastructure tree at
// /aws/service/global-infrastructure/services/<code>/regions. Lookups run
// concurrently (bounded by fanoutScope) and fail open per code: a code whose
// lookup errors or returns no parameters is omitted from the result (→ scanned
// everywhere). The returned error is the first access-denied seen — the caller
// uses it only to warn that the optimisation is off; it is never fatal.
func loadServiceRegionAvailability(ctx context.Context, client ssmRegionAvailabilityAPI, codes []string) (map[string]map[string]bool, error) {
	var (
		mu      sync.Mutex
		out     = make(map[string]map[string]bool, len(codes))
		denyErr error
		g, gctx = errgroup.WithContext(ctx)
	)
	g.SetLimit(fanoutScope)
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
					// Fail open: this code stays unscoped. Record the first
					// access-denied so the caller can flag the optimisation as off.
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
// region is scanned. The SSM client is pinned to us-east-1, where the
// commercial-partition global-infra parameters live. A blanket access-denied (no
// data at all) warns once that the optimisation is off.
func buildRegionAvailability(ctx context.Context, acct *account, services []string, st *store.Store, kept []string) {
	if acct.regionScopeDisabled || len(kept) <= 1 {
		return
	}
	codes := distinctRegionAvailabilityCodes(filteredServices(services, acct.includeServiceQuotas))
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

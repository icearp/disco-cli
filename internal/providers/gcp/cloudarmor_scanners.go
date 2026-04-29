package gcp

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/internal/store"
	"google.golang.org/api/compute/v1"
)

func init() {
	registerService(serviceEntry{
		name: "gcp:cloudarmor",
		fn:   scanCloudArmor,
		emits: []coverage.TypeDecl{
			{Service: "compute", DiscoType: TypeComputeSecurityPolicy},
		},
	})
}

// scanCloudArmor discovers Cloud Armor security policies. Uses
// AggregatedList so global + every regional policy come back in one
// paginated walk. Rules embedded in each policy as `rules[]` survive into
// AttributesJSON; the rule scanner fan-out is intentionally not its own
// service — rule cardinality is bounded (per-policy max ~200) and the rules
// are meaningless without the policy that owns them.
func scanCloudArmor(ctx context.Context, p *project, st *store.Store, scanID string) (total, inserted int, err error) {
	opts := clientOptions(ctx, providerCfg{})
	svc, err := compute.NewService(ctx, opts...)
	if err != nil {
		return 0, 0, fmt.Errorf("compute client: %w", err)
	}
	return runPaginated(ctx, st, p, "compute:securityPolicies.aggregatedList",
		svc.SecurityPolicies.AggregatedList(p.ID),
		func(page *compute.SecurityPoliciesAggregatedList) (int, int, error) {
			var batch []*store.Resource
			for scope, items := range page.Items {
				region := scopedListRegion(scope)
				for _, sp := range items.SecurityPolicies {
					name := sp.Name
					batch = append(batch, &store.Resource{
						Provider:       "gcp",
						AccountID:      p.ID,
						AccountName:    &p.Name,
						Type:           TypeComputeSecurityPolicy,
						NativeID:       sp.SelfLink,
						Name:           &name,
						Region:         strp(region),
						CreatedAt:      strp(sp.CreationTimestamp),
						AttributesJSON: mustJSON(sp),
						DiscoveredBy:   scanID,
					})
				}
			}
			return upsertWithProjClosure(p, st, batch)
		})
}

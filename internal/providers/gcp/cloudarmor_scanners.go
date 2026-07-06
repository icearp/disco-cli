package gcp

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/store"
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
// AggregatedList so global + every regional policy return in one paginated
// walk. Rules embedded per policy as `rules[]` survive into AttributesJSON;
// not split into their own scanner — cardinality is bounded (~200/policy)
// and rules are meaningless without their owning policy.
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

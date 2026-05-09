package gcp

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/store"
	"google.golang.org/api/dataproc/v1"
)

func init() {
	registerService(serviceEntry{
		name: "gcp:dataproc",
		fn:   scanDataproc,
		emits: []coverage.TypeDecl{
			{Service: "dataproc", DiscoType: TypeDataprocCluster},
		},
	})
}

// scanDataproc discovers Dataproc clusters in every enabled region of the
// project. Dataproc has no aggregated/wildcard endpoint, so we delegate
// per-region fan-out to gcpRegionFanoutScan.
func scanDataproc(ctx context.Context, p *project, st *store.Store, scanID string) (total, inserted int, err error) {
	opts := clientOptions(ctx, providerCfg{})
	svc, err := dataproc.NewService(ctx, opts...)
	if err != nil {
		return 0, 0, fmt.Errorf("dataproc client: %w", err)
	}
	return gcpRegionFanoutScan(
		ctx, p, st, fanoutMed, "dataproc:clusters.list",
		func(region string) pager[dataproc.ListClustersResponse] {
			return svc.Projects.Regions.Clusters.List(p.ID, region)
		},
		func(page *dataproc.ListClustersResponse) []*dataproc.Cluster { return page.Clusters },
		func(c *dataproc.Cluster, region string) *store.Resource {
			if c == nil || c.ClusterName == "" {
				return nil
			}
			name := c.ClusterName
			reg := region
			return &store.Resource{
				Provider:       "gcp",
				AccountID:      p.ID,
				AccountName:    &p.Name,
				Type:           TypeDataprocCluster,
				NativeID:       fmt.Sprintf("projects/%s/regions/%s/clusters/%s", p.ID, reg, c.ClusterName),
				Name:           &name,
				Region:         &reg,
				AttributesJSON: mustJSON(c),
				DiscoveredBy:   scanID,
			}
		},
	)
}

// fanoutMed is the per-region concurrency cap for region fan-out scanners.
// Tuned to match GCP's typical regional-API quotas (~100 req/s per region)
// while keeping total in-flight call count modest.
const fanoutMed = 10

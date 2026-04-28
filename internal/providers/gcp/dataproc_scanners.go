package gcp

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/store"
	"google.golang.org/api/dataproc/v1"
)

func init() { registerService(serviceEntry{name: "gcp:dataproc", fn: scanDataproc}) }

// scanDataproc discovers Dataproc clusters in every enabled region of the
// project. Dataproc has no aggregated/wildcard endpoint, so we enumerate
// regions via gcpRegions and fan out one per-region list call bounded by
// forEachItem (concurrency = fanoutMed).
func scanDataproc(ctx context.Context, p *project, st *store.Store, scanID string) (total, inserted int, err error) {
	regions, err := gcpRegions(ctx, p)
	if err != nil {
		return 0, 0, err
	}
	if len(regions) == 0 {
		return 0, 0, nil
	}
	opts := clientOptions(ctx, providerCfg{})
	svc, err := dataproc.NewService(ctx, opts...)
	if err != nil {
		return 0, 0, fmt.Errorf("dataproc client: %w", err)
	}

	type pageBatch struct {
		batch []*store.Resource
	}
	var (
		acc []*store.Resource
	)
	if err := forEachItem(ctx, fanoutMed, regions, func(gctx context.Context, region string) error {
		err := svc.Projects.Regions.Clusters.List(p.ID, region).Pages(gctx, func(page *dataproc.ListClustersResponse) error {
			for _, c := range page.Clusters {
				if c == nil || c.ClusterName == "" {
					continue
				}
				name := c.ClusterName
				nativeID := fmt.Sprintf("projects/%s/regions/%s/clusters/%s", p.ID, region, c.ClusterName)
				r := &store.Resource{
					Provider:       "gcp",
					AccountID:      p.ID,
					AccountName:    &p.Name,
					Type:           TypeDataprocCluster,
					NativeID:       nativeID,
					Name:           &name,
					Region:         &region,
					AttributesJSON: mustJSON(c),
					DiscoveredBy:   scanID,
				}
				acc = append(acc, r)
			}
			return nil
		})
		if err != nil {
			if isPermissionDenied(err) {
				return skipIfDenied(st, "dataproc:clusters.list", p.ID+"/"+region, err)
			}
			return err
		}
		return nil
	}); err != nil {
		return 0, 0, err
	}
	if len(acc) == 0 {
		return 0, 0, nil
	}
	t, n, perr := upsertWithProjClosure(p, st, acc)
	return t, n, perr
}

// fanoutMed is the per-region concurrency cap for region fan-out scanners.
// Tuned to match GCP's typical regional-API quotas (~100 req/s per region)
// while keeping total in-flight call count modest.
const fanoutMed = 10

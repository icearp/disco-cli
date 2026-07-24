package gcp

import (
	"context"
	"fmt"
	"sync"

	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
	"google.golang.org/api/container/v1"
)

func init() {
	registerType(restype.Descriptor{Type: TypeGKECluster, Service: "container", Upstream: "container.googleapis.com/Cluster"})
	registerType(restype.Descriptor{Type: TypeGKENodePool, Service: "container", Upstream: "container.googleapis.com/NodePool"})
	registerService(serviceEntry{
		name: "gcp:gke",
		fn:   scanGKE,
	})
}

// maxConcurrentGKENodePoolFanout caps the per-Cluster NodePool fan-out.
const maxConcurrentGKENodePoolFanout = 10

// scanGKE discovers GKE clusters for a project across all locations, then
// fans out per cluster for NodePools.
func scanGKE(ctx context.Context, p *project, st *store.Store, scanID string) (total, inserted int, err error) {
	opts := clientOptions(ctx, providerCfg{})
	svc, err := container.NewService(ctx, opts...)
	if err != nil {
		return 0, 0, fmt.Errorf("container client: %w", err)
	}
	return scanGKEWithClient(ctx, svc, p, st, scanID)
}

// scanGKEWithClient is the test seam for scanGKE — takes the pre-built
// client directly so tests can point it at a fake server.
func scanGKEWithClient(ctx context.Context, svc *container.Service, p *project, st *store.Store, scanID string) (total, inserted int, err error) {
	// "-" as location returns clusters across all locations.
	parent := fmt.Sprintf("projects/%s/locations/-", p.ID)
	out, err := svc.Projects.Locations.Clusters.List(parent).Context(ctx).Do()
	if err != nil {
		if isPermissionDenied(err) {
			return 0, 0, skipIfDenied(st, "container:clusters.list", p.ID, err)
		}
		return 0, 0, fmt.Errorf("container:clusters.list %s: %w", p.ID, err)
	}
	type clusterRef struct {
		location string
		name     string
		id       string
	}
	var batch []*store.Resource
	var clusterRefs []clusterRef
	for _, c := range out.Clusters {
		if c == nil || c.Name == "" {
			continue
		}
		name := c.Name
		region := c.Location
		status := c.Status
		clusterRefs = append(clusterRefs, clusterRef{
			location: c.Location,
			name:     c.Name,
			id:       store.ResourceID("gcp", p.ID, c.SelfLink),
		})
		r := &store.Resource{
			Provider:       "gcp",
			AccountID:      p.ID,
			AccountName:    &p.Name,
			Type:           TypeGKECluster,
			NativeID:       c.SelfLink,
			Name:           &name,
			Region:         &region,
			CreatedAt:      strp(c.CreateTime),
			Status:         &status,
			AttributesJSON: mustJSON(c),
			DiscoveredBy:   scanID,
		}
		if len(c.ResourceLabels) > 0 {
			s := mustJSON(c.ResourceLabels)
			r.TagsJSON = &s
		}
		batch = append(batch, r)
	}
	if len(batch) > 0 {
		n, err := st.UpsertResources(batch)
		if err != nil {
			return 0, 0, fmt.Errorf("upsert GKE clusters: %w", err)
		}
		total += len(batch)
		inserted += n
	}

	// Per-Cluster fan-out — NodePools. Nested under a fan-out that only runs
	// after Clusters.List (above) already proved the container API enabled
	// for this project — never let a nested isAPINotEnabled-shaped error
	// escalate to the whole-service disabled sentinel.
	var mu sync.Mutex
	err = forEachItem(ctx, maxConcurrentGKENodePoolFanout, clusterRefs, func(gctx context.Context, c clusterRef) error {
		clusterParent := fmt.Sprintf("projects/%s/locations/%s/clusters/%s", p.ID, c.location, c.name)
		out, nerr := svc.Projects.Locations.Clusters.NodePools.List(clusterParent).Context(gctx).Do()
		if nerr != nil {
			if isPermissionDenied(nerr) {
				_ = skipIfDenied(st, "container:nodePools.list", p.ID, nerr)
				return nil
			}
			return nerr
		}
		npbatch := make([]*store.Resource, 0, len(out.NodePools))
		for _, np := range out.NodePools {
			if np == nil || np.Name == "" {
				continue
			}
			name := np.Name
			status := np.Status
			npbatch = append(npbatch, &store.Resource{
				Provider:       "gcp",
				AccountID:      p.ID,
				AccountName:    &p.Name,
				Type:           TypeGKENodePool,
				NativeID:       np.SelfLink,
				Name:           &name,
				Region:         strp(c.location),
				Status:         &status,
				AttributesJSON: mustJSON(np),
				DiscoveredBy:   scanID,
			})
		}
		mu.Lock()
		defer mu.Unlock()
		nt, nn, uerr := upsertWithParent(st, npbatch, c.id)
		if uerr != nil {
			return uerr
		}
		total += nt
		inserted += nn
		return nil
	})
	if err != nil {
		return total, inserted, err
	}
	return total, inserted, nil
}

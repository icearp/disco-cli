package gcp

import (
	"context"
	"fmt"
	"strings"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/store"
	"google.golang.org/api/compute/v1"
)

func init() {
	registerService(serviceEntry{
		name: "gcp:compute",
		fn:   scanCompute,
		emits: []coverage.TypeDecl{
			{Service: "compute", DiscoType: TypeComputeInstance},
			{Service: "compute", DiscoType: TypeComputeNetwork},
			{Service: "compute", DiscoType: TypeComputeSubnet},
			{Service: "compute", DiscoType: TypeComputeFirewall},
		},
	})
}

// scanCompute discovers Compute Engine instances, VPC networks, subnetworks,
// and firewalls. Uses AggregatedList for instances/subnetworks so all zones
// are covered in a single API call.
func scanCompute(ctx context.Context, p *project, st *store.Store, scanID string) (total, inserted int, err error) {
	opts := clientOptions(ctx, providerCfg{})
	svc, err := compute.NewService(ctx, opts...)
	if err != nil {
		return 0, 0, fmt.Errorf("compute client: %w", err)
	}

	for _, sub := range []func() (int, int, error){
		func() (int, int, error) { return scanComputeInstances(ctx, svc, p, st, scanID) },
		func() (int, int, error) { return scanComputeNetworks(ctx, svc, p, st, scanID) },
		func() (int, int, error) { return scanComputeSubnetworks(ctx, svc, p, st, scanID) },
		func() (int, int, error) { return scanComputeFirewalls(ctx, svc, p, st, scanID) },
	} {
		t, n, err := sub()
		if err != nil {
			return total, inserted, err
		}
		total += t
		inserted += n
	}
	return total, inserted, nil
}

func scanComputeInstances(ctx context.Context, svc *compute.Service, p *project, st *store.Store, scanID string) (int, int, error) {
	projParentID := store.ResourceID("gcp", p.ID, TypeProject, p.ID)
	return runPaginated(ctx, st, p, "compute:instances.aggregatedList",
		svc.Instances.AggregatedList(p.ID),
		func(page *compute.InstanceAggregatedList) (int, int, error) {
			var batch []*store.Resource
			for _, items := range page.Items {
				for _, inst := range items.Instances {
					status := inst.Status
					r := &store.Resource{
						Provider:       "gcp",
						AccountID:      p.ID,
						AccountName:    &p.Name,
						Type:           TypeComputeInstance,
						NativeID:       inst.SelfLink,
						Name:           &inst.Name,
						CreatedAt:      strp(inst.CreationTimestamp),
						Status:         &status,
						AttributesJSON: mustJSON(inst),
						DiscoveredBy:   scanID,
					}
					if inst.Zone != "" {
						zone := lastSegment(inst.Zone)
						r.Zone = &zone
						region := zoneToRegion(zone)
						r.Region = &region
					}
					batch = append(batch, r)
				}
			}
			if len(batch) == 0 {
				return 0, 0, nil
			}
			n, err := st.UpsertResources(batch)
			if err != nil {
				return 0, 0, fmt.Errorf("upsert compute instances: %w", err)
			}
			pairs := make([][2]string, 0, len(batch))
			for _, r := range batch {
				pairs = append(pairs, [2]string{
					store.ResourceID(r.Provider, r.AccountID, r.Type, r.NativeID),
					projParentID,
				})
			}
			if err := st.RecordHierarchyBatch(pairs); err != nil {
				return len(batch), n, fmt.Errorf("closure compute instances: %w", err)
			}
			return len(batch), n, nil
		})
}

func scanComputeNetworks(ctx context.Context, svc *compute.Service, p *project, st *store.Store, scanID string) (int, int, error) {
	return runPaginated(ctx, st, p, "compute:networks.list",
		svc.Networks.List(p.ID),
		func(page *compute.NetworkList) (int, int, error) {
			batch := make([]*store.Resource, 0, len(page.Items))
			for _, net := range page.Items {
				batch = append(batch, &store.Resource{
					Provider:       "gcp",
					AccountID:      p.ID,
					AccountName:    &p.Name,
					Type:           TypeComputeNetwork,
					NativeID:       net.SelfLink,
					Name:           &net.Name,
					CreatedAt:      strp(net.CreationTimestamp),
					AttributesJSON: mustJSON(net),
					DiscoveredBy:   scanID,
				})
			}
			if len(batch) == 0 {
				return 0, 0, nil
			}
			n, e := st.UpsertResources(batch)
			if e != nil {
				return 0, 0, fmt.Errorf("upsert compute networks: %w", e)
			}
			return len(batch), n, nil
		})
}

func scanComputeSubnetworks(ctx context.Context, svc *compute.Service, p *project, st *store.Store, scanID string) (int, int, error) {
	return runPaginated(ctx, st, p, "compute:subnetworks.aggregatedList",
		svc.Subnetworks.AggregatedList(p.ID),
		func(page *compute.SubnetworkAggregatedList) (int, int, error) {
			var batch []*store.Resource
			for _, items := range page.Items {
				for _, sn := range items.Subnetworks {
					region := lastSegment(sn.Region)
					batch = append(batch, &store.Resource{
						Provider:       "gcp",
						AccountID:      p.ID,
						AccountName:    &p.Name,
						Type:           TypeComputeSubnet,
						NativeID:       sn.SelfLink,
						Name:           &sn.Name,
						Region:         &region,
						CreatedAt:      strp(sn.CreationTimestamp),
						AttributesJSON: mustJSON(sn),
						DiscoveredBy:   scanID,
					})
				}
			}
			if len(batch) == 0 {
				return 0, 0, nil
			}
			n, e := st.UpsertResources(batch)
			if e != nil {
				return 0, 0, fmt.Errorf("upsert compute subnetworks: %w", e)
			}
			return len(batch), n, nil
		})
}

func scanComputeFirewalls(ctx context.Context, svc *compute.Service, p *project, st *store.Store, scanID string) (int, int, error) {
	return runPaginated(ctx, st, p, "compute:firewalls.list",
		svc.Firewalls.List(p.ID),
		func(page *compute.FirewallList) (int, int, error) {
			batch := make([]*store.Resource, 0, len(page.Items))
			for _, fw := range page.Items {
				batch = append(batch, &store.Resource{
					Provider:       "gcp",
					AccountID:      p.ID,
					AccountName:    &p.Name,
					Type:           TypeComputeFirewall,
					NativeID:       fw.SelfLink,
					Name:           &fw.Name,
					CreatedAt:      strp(fw.CreationTimestamp),
					AttributesJSON: mustJSON(fw),
					DiscoveredBy:   scanID,
				})
			}
			if len(batch) == 0 {
				return 0, 0, nil
			}
			n, e := st.UpsertResources(batch)
			if e != nil {
				return 0, 0, fmt.Errorf("upsert compute firewalls: %w", e)
			}
			return len(batch), n, nil
		})
}

// lastSegment returns the last path component of a GCP resource URL/name.
// e.g. ".../zones/us-central1-a" → "us-central1-a"
func lastSegment(s string) string {
	if i := strings.LastIndexByte(s, '/'); i >= 0 {
		return s[i+1:]
	}
	return s
}

// zoneToRegion trims the trailing zone suffix from a zone name.
// e.g. "us-central1-a" → "us-central1"
func zoneToRegion(zone string) string {
	if i := strings.LastIndexByte(zone, '-'); i >= 0 {
		return zone[:i]
	}
	return zone
}

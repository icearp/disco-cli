package gcp

import (
	"context"
	"fmt"

	"codeburg.org/icearp/disco/internal/store"
	"google.golang.org/api/compute/v1"
)

// scanCompute discovers Compute Engine instances, VPC networks, subnetworks,
// and firewalls. Uses AggregatedList for instances/subnetworks so all zones
// are covered in a single API call.
func scanCompute(ctx context.Context, p *project, st *store.Store, scanID string) error {
	opts := clientOptions(ctx, providerCfg{})
	svc, err := compute.NewService(ctx, opts...)
	if err != nil {
		return fmt.Errorf("compute client: %w", err)
	}

	if err := scanComputeInstances(ctx, svc, p, st, scanID); err != nil {
		return err
	}
	if err := scanComputeNetworks(ctx, svc, p, st, scanID); err != nil {
		return err
	}
	if err := scanComputeSubnetworks(ctx, svc, p, st, scanID); err != nil {
		return err
	}
	return scanComputeFirewalls(ctx, svc, p, st, scanID)
}

func scanComputeInstances(ctx context.Context, svc *compute.Service, p *project, st *store.Store, scanID string) error {
	projParentID := store.ResourceID("gcp", p.ID, "gcp:cloudresourcemanager:project", p.ID)

	req := svc.Instances.AggregatedList(p.ID)
	return req.Pages(ctx, func(page *compute.InstanceAggregatedList) error {
		var batch []*store.Resource
		for _, items := range page.Items {
			for _, inst := range items.Instances {
				status := inst.Status
				r := &store.Resource{
					Provider:       "gcp",
					AccountID:      p.ID,
					AccountName:    &p.Name,
					Type:           "gcp:compute:instance",
					NativeID:       inst.SelfLink,
					Name:           &inst.Name,
					Status:         &status,
					AttributesJSON: mustJSON(inst),
					ScanID:         scanID,
					ParentID:       &projParentID,
				}
				// Zone is embedded in the self-link; extract for the region field.
				if inst.Zone != "" {
					zone := lastSegment(inst.Zone)
					r.Zone = &zone
					// Region is the zone without the last letter (e.g. us-central1-a → us-central1).
					region := zoneToRegion(zone)
					r.Region = &region
				}
				batch = append(batch, r)
			}
		}
		if len(batch) == 0 {
			return nil
		}
		if err := st.UpsertResources(batch); err != nil {
			return fmt.Errorf("upsert compute instances: %w", err)
		}
		// Populate closure table for each instance → project.
		for _, r := range batch {
			if r.ParentID != nil {
				instanceID := store.ResourceID(r.Provider, r.AccountID, r.Type, r.NativeID)
				_ = st.AddToHierarchyClosure(instanceID, *r.ParentID)
			}
		}
		return nil
	})
}

func scanComputeNetworks(ctx context.Context, svc *compute.Service, p *project, st *store.Store, scanID string) error {
	projParentID := store.ResourceID("gcp", p.ID, "gcp:cloudresourcemanager:project", p.ID)

	req := svc.Networks.List(p.ID)
	return req.Pages(ctx, func(page *compute.NetworkList) error {
		var batch []*store.Resource
		for _, net := range page.Items {
			r := &store.Resource{
				Provider:       "gcp",
				AccountID:      p.ID,
				AccountName:    &p.Name,
				Type:           "gcp:compute:network",
				NativeID:       net.SelfLink,
				Name:           &net.Name,
				AttributesJSON: mustJSON(net),
				ScanID:         scanID,
				ParentID:       &projParentID,
			}
			batch = append(batch, r)
		}
		if len(batch) == 0 {
			return nil
		}
		return st.UpsertResources(batch)
	})
}

func scanComputeSubnetworks(ctx context.Context, svc *compute.Service, p *project, st *store.Store, scanID string) error {
	projParentID := store.ResourceID("gcp", p.ID, "gcp:cloudresourcemanager:project", p.ID)

	req := svc.Subnetworks.AggregatedList(p.ID)
	return req.Pages(ctx, func(page *compute.SubnetworkAggregatedList) error {
		var batch []*store.Resource
		for _, items := range page.Items {
			for _, sn := range items.Subnetworks {
				region := lastSegment(sn.Region)
				r := &store.Resource{
					Provider:       "gcp",
					AccountID:      p.ID,
					AccountName:    &p.Name,
					Type:           "gcp:compute:subnetwork",
					NativeID:       sn.SelfLink,
					Name:           &sn.Name,
					Region:         &region,
					AttributesJSON: mustJSON(sn),
					ScanID:         scanID,
					ParentID:       &projParentID,
				}
				batch = append(batch, r)
			}
		}
		if len(batch) == 0 {
			return nil
		}
		return st.UpsertResources(batch)
	})
}

func scanComputeFirewalls(ctx context.Context, svc *compute.Service, p *project, st *store.Store, scanID string) error {
	projParentID := store.ResourceID("gcp", p.ID, "gcp:cloudresourcemanager:project", p.ID)

	req := svc.Firewalls.List(p.ID)
	return req.Pages(ctx, func(page *compute.FirewallList) error {
		var batch []*store.Resource
		for _, fw := range page.Items {
			r := &store.Resource{
				Provider:       "gcp",
				AccountID:      p.ID,
				AccountName:    &p.Name,
				Type:           "gcp:compute:firewall",
				NativeID:       fw.SelfLink,
				Name:           &fw.Name,
				AttributesJSON: mustJSON(fw),
				ScanID:         scanID,
				ParentID:       &projParentID,
			}
			batch = append(batch, r)
		}
		if len(batch) == 0 {
			return nil
		}
		return st.UpsertResources(batch)
	})
}

// lastSegment returns the last path component of a GCP resource URL/name.
// e.g. ".../zones/us-central1-a" → "us-central1-a"
func lastSegment(s string) string {
	for i := len(s) - 1; i >= 0; i-- {
		if s[i] == '/' {
			return s[i+1:]
		}
	}
	return s
}

// zoneToRegion trims the trailing zone letter from a zone name.
// e.g. "us-central1-a" → "us-central1"
func zoneToRegion(zone string) string {
	for i := len(zone) - 1; i >= 0; i-- {
		if zone[i] == '-' {
			return zone[:i]
		}
	}
	return zone
}

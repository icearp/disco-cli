package gcp

import (
	"context"
	"fmt"
	"strings"

	"codeburg.org/icearp/disco/internal/store"
	"google.golang.org/api/compute/v1"
)

func init() { registerService(serviceEntry{name: "gcp:compute", fn: scanCompute}) }

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
		if isPermissionDenied(err) {
			return skipIfDenied("compute:instances.aggregatedList", p.ID, err)
		}
		return err
	}
	if err := scanComputeNetworks(ctx, svc, p, st, scanID); err != nil {
		if isPermissionDenied(err) {
			return skipIfDenied("compute:networks.list", p.ID, err)
		}
		return err
	}
	if err := scanComputeSubnetworks(ctx, svc, p, st, scanID); err != nil {
		if isPermissionDenied(err) {
			return skipIfDenied("compute:subnetworks.aggregatedList", p.ID, err)
		}
		return err
	}
	if err := scanComputeFirewalls(ctx, svc, p, st, scanID); err != nil {
		if isPermissionDenied(err) {
			return skipIfDenied("compute:firewalls.list", p.ID, err)
		}
		return err
	}
	return nil
}

func scanComputeInstances(ctx context.Context, svc *compute.Service, p *project, st *store.Store, scanID string) error {
	projParentID := store.ResourceID("gcp", p.ID, TypeProject, p.ID)

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
					Type:           TypeComputeInstance,
					NativeID:       inst.SelfLink,
					Name:           &inst.Name,
					CreatedAt:      strp(inst.CreationTimestamp),
					Status:         &status,
					AttributesJSON: mustJSON(inst),
					DiscoveredBy:         scanID,
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
		var pairs [][2]string
		for _, r := range batch {
			instanceID := store.ResourceID(r.Provider, r.AccountID, r.Type, r.NativeID)
			pairs = append(pairs, [2]string{instanceID, projParentID})
		}
		if err := st.BatchAddToHierarchyClosure(pairs); err != nil {
			return fmt.Errorf("closure compute instances: %w", err)
		}
		return nil
	})
}

func scanComputeNetworks(ctx context.Context, svc *compute.Service, p *project, st *store.Store, scanID string) error {
	req := svc.Networks.List(p.ID)
	return req.Pages(ctx, func(page *compute.NetworkList) error {
		var batch []*store.Resource
		for _, net := range page.Items {
			r := &store.Resource{
				Provider:       "gcp",
				AccountID:      p.ID,
				AccountName:    &p.Name,
				Type:           TypeComputeNetwork,
				NativeID:       net.SelfLink,
				Name:           &net.Name,
				CreatedAt:      strp(net.CreationTimestamp),
				AttributesJSON: mustJSON(net),
				DiscoveredBy:         scanID,
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
					Type:           TypeComputeSubnet,
					NativeID:       sn.SelfLink,
					Name:           &sn.Name,
					Region:         &region,
					CreatedAt:      strp(sn.CreationTimestamp),
					AttributesJSON: mustJSON(sn),
					DiscoveredBy:         scanID,
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
	req := svc.Firewalls.List(p.ID)
	return req.Pages(ctx, func(page *compute.FirewallList) error {
		var batch []*store.Resource
		for _, fw := range page.Items {
			r := &store.Resource{
				Provider:       "gcp",
				AccountID:      p.ID,
				AccountName:    &p.Name,
				Type:           TypeComputeFirewall,
				NativeID:       fw.SelfLink,
				Name:           &fw.Name,
				CreatedAt:      strp(fw.CreationTimestamp),
				AttributesJSON: mustJSON(fw),
				DiscoveredBy:         scanID,
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

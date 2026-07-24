package gcp

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/icearp/disco-cli/internal/util"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerResolver(resolveComputeInstanceRelationships,
		EdgeDecl{TypeComputeInstance, TypeComputeNetwork, store.RelAttachedTo},
		EdgeDecl{TypeComputeInstance, TypeComputeSubnet, store.RelAttachedTo},
		EdgeDecl{TypeComputeInstance, TypeComputeDisk, store.RelAttachedTo},
		EdgeDecl{TypeComputeInstance, TypeComputeRegionDisk, store.RelAttachedTo},
	)
	registerResolver(resolveSubnetworkRelationships,
		EdgeDecl{TypeComputeSubnet, TypeComputeNetwork, store.RelAttachedTo},
	)
	registerResolver(resolveInstanceGroupManagerRelationships,
		EdgeDecl{TypeComputeInstanceGroupManager, TypeComputeInstanceTemplate, store.RelUses},
		EdgeDecl{TypeComputeInstanceGroupManager, TypeComputeRegionInstanceTemplate, store.RelUses},
		EdgeDecl{TypeComputeInstanceGroupManager, TypeComputeInstanceGroup, store.RelUses},
		EdgeDecl{TypeComputeInstanceGroupManager, TypeComputeRegionInstanceGroup, store.RelUses},
		EdgeDecl{TypeComputeRegionInstanceGroupManager, TypeComputeInstanceTemplate, store.RelUses},
		EdgeDecl{TypeComputeRegionInstanceGroupManager, TypeComputeRegionInstanceTemplate, store.RelUses},
		EdgeDecl{TypeComputeRegionInstanceGroupManager, TypeComputeInstanceGroup, store.RelUses},
		EdgeDecl{TypeComputeRegionInstanceGroupManager, TypeComputeRegionInstanceGroup, store.RelUses},
	)
}

func resolveComputeInstanceRelationships(p *project, st *store.Store) error {
	instances, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"gcp"}, AccountID: p.ID, Types: []string{TypeComputeInstance},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	for _, r := range instances {
		var attrs struct {
			NetworkInterfaces []struct {
				Network    string `json:"network"`
				Subnetwork string `json:"subnetwork"`
			} `json:"networkInterfaces"`
			Disks []struct {
				Source string `json:"source"`
			} `json:"disks"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		for _, nic := range attrs.NetworkInterfaces {
			if nic.Network != "" {
				netID := store.ResourceID("gcp", p.ID, nic.Network)
				if err := st.UpsertRelationship(r.ID, netID, store.RelAttachedTo, "directed", nil); err != nil {
					return fmt.Errorf("upsert instance→network relationship: %w", err)
				}
			}
			if nic.Subnetwork != "" {
				snID := store.ResourceID("gcp", p.ID, nic.Subnetwork)
				if err := st.UpsertRelationship(r.ID, snID, store.RelAttachedTo, "directed", nil); err != nil {
					return fmt.Errorf("upsert instance→subnetwork relationship: %w", err)
				}
			}
		}
		for _, disk := range attrs.Disks {
			if disk.Source == "" {
				continue
			}
			diskID := store.ResourceID("gcp", p.ID, disk.Source)
			if err := st.UpsertRelationship(r.ID, diskID, store.RelAttachedTo, "directed", nil); err != nil {
				return fmt.Errorf("upsert instance→disk relationship: %w", err)
			}
		}
	}
	return nil
}

// selfLinkIsRegional reports whether a Compute self-link's scope segment is
// regional (".../regions/{region}/...") rather than zonal or global. Used to
// pick between a resource's zonal/global and regional disco-type variants
// (Disk vs RegionDisk, InstanceTemplate vs RegionInstanceTemplate, etc.).
func selfLinkIsRegional(selfLink string) bool {
	return strings.Contains(selfLink, "/regions/")
}

// computeDiskTypeForSelfLink picks the disco type matching a disk self-link's
// scope segment. Attached disks are almost always zonal in practice; this
// only matters when an instance references a regional (replicated) disk.
func computeDiskTypeForSelfLink(selfLink string) string {
	if selfLinkIsRegional(selfLink) {
		return TypeComputeRegionDisk
	}
	return TypeComputeDisk
}

// resolveInstanceGroupManagerRelationships wires each (Region)InstanceGroupManager
// to the (Region)InstanceTemplate it deploys and the (Region)InstanceGroup it
// manages — both are self-link fields embedded directly on the IGM resource,
// no extra API call needed.
func resolveInstanceGroupManagerRelationships(p *project, st *store.Store) error {
	igms, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"gcp"}, AccountID: p.ID,
		Types: []string{TypeComputeInstanceGroupManager, TypeComputeRegionInstanceGroupManager},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	for _, r := range igms {
		var attrs struct {
			InstanceTemplate string `json:"instanceTemplate"`
			InstanceGroup    string `json:"instanceGroup"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if attrs.InstanceTemplate != "" {
			tplID := store.ResourceID("gcp", p.ID, attrs.InstanceTemplate)
			if err := st.UpsertRelationship(r.ID, tplID, store.RelUses, "directed", nil); err != nil {
				return fmt.Errorf("upsert igm→instance-template relationship: %w", err)
			}
		}
		if attrs.InstanceGroup != "" {
			igID := store.ResourceID("gcp", p.ID, attrs.InstanceGroup)
			if err := st.UpsertRelationship(r.ID, igID, store.RelUses, "directed", nil); err != nil {
				return fmt.Errorf("upsert igm→instance-group relationship: %w", err)
			}
		}
	}
	return nil
}

func resolveSubnetworkRelationships(p *project, st *store.Store) error {
	subnets, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"gcp"}, AccountID: p.ID, Types: []string{TypeComputeSubnet},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	for _, r := range subnets {
		var attrs struct {
			Network string `json:"network"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if attrs.Network != "" {
			netID := store.ResourceID("gcp", p.ID, attrs.Network)
			if err := st.UpsertRelationship(r.ID, netID, store.RelAttachedTo, "directed", nil); err != nil {
				return fmt.Errorf("upsert subnetwork→network relationship: %w", err)
			}
		}
	}
	return nil
}

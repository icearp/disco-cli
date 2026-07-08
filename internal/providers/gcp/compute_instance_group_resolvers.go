package gcp

import (
	"encoding/json"
	"fmt"

	"codeberg.org/icearp/disco/internal/util"
	"codeberg.org/icearp/disco/store"
)

// Resolver Wave R2 (part 1) of the resolver-implementation backlog
// (ROADMAP.md "Resolver buildout"): InstanceGroup/InstanceTemplate network +
// lineage edges. InstanceGroup carries a single network/subnetwork pair
// directly; InstanceTemplate embeds a full InstanceProperties block mirroring
// the fields resolveComputeInstanceRelationships already reads off a live
// Instance (network interfaces, disks, service accounts).
func init() {
	registerResolver(resolveInstanceGroupRelationships,
		EdgeDecl{TypeComputeInstanceGroup, TypeComputeNetwork, store.RelAttachedTo},
		EdgeDecl{TypeComputeInstanceGroup, TypeComputeSubnet, store.RelAttachedTo},
		EdgeDecl{TypeComputeRegionInstanceGroup, TypeComputeNetwork, store.RelAttachedTo},
		EdgeDecl{TypeComputeRegionInstanceGroup, TypeComputeSubnet, store.RelAttachedTo},
	)
	registerResolver(resolveInstanceTemplateRelationships,
		EdgeDecl{TypeComputeInstanceTemplate, TypeComputeNetwork, store.RelAttachedTo},
		EdgeDecl{TypeComputeInstanceTemplate, TypeComputeSubnet, store.RelAttachedTo},
		EdgeDecl{TypeComputeInstanceTemplate, TypeComputeImage, store.RelAttachedTo},
		EdgeDecl{TypeComputeInstanceTemplate, TypeComputeSnapshot, store.RelAttachedTo},
		EdgeDecl{TypeComputeInstanceTemplate, TypeComputeRegionSnapshot, store.RelAttachedTo},
		EdgeDecl{TypeComputeInstanceTemplate, TypeComputeResourcePolicy, store.RelUses},
		EdgeDecl{TypeComputeInstanceTemplate, TypeIAMServiceAccount, store.RelUses},
		EdgeDecl{TypeComputeRegionInstanceTemplate, TypeComputeNetwork, store.RelAttachedTo},
		EdgeDecl{TypeComputeRegionInstanceTemplate, TypeComputeSubnet, store.RelAttachedTo},
		EdgeDecl{TypeComputeRegionInstanceTemplate, TypeComputeImage, store.RelAttachedTo},
		EdgeDecl{TypeComputeRegionInstanceTemplate, TypeComputeSnapshot, store.RelAttachedTo},
		EdgeDecl{TypeComputeRegionInstanceTemplate, TypeComputeRegionSnapshot, store.RelAttachedTo},
		EdgeDecl{TypeComputeRegionInstanceTemplate, TypeComputeResourcePolicy, store.RelUses},
		EdgeDecl{TypeComputeRegionInstanceTemplate, TypeIAMServiceAccount, store.RelUses},
	)
}

func resolveInstanceGroupRelationships(p *project, st *store.Store) error {
	groups, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"gcp"}, AccountID: p.ID,
		Types: []string{TypeComputeInstanceGroup, TypeComputeRegionInstanceGroup},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(groups) == 0 {
		return nil
	}
	scanned, err := scannedIDSet(p, st, TypeComputeNetwork, TypeComputeSubnet)
	if err != nil {
		return err
	}
	for _, r := range groups {
		var attrs struct {
			Network    string `json:"network"`
			Subnetwork string `json:"subnetwork"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if err := upsertIfScanned(st, scanned, r.ID, "gcp", p.ID, TypeComputeNetwork, attrs.Network, store.RelAttachedTo); err != nil {
			return err
		}
		if err := upsertIfScanned(st, scanned, r.ID, "gcp", p.ID, TypeComputeSubnet, attrs.Subnetwork, store.RelAttachedTo); err != nil {
			return err
		}
	}
	return nil
}

func resolveInstanceTemplateRelationships(p *project, st *store.Store) error {
	tpls, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"gcp"}, AccountID: p.ID,
		Types: []string{TypeComputeInstanceTemplate, TypeComputeRegionInstanceTemplate},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(tpls) == 0 {
		return nil
	}
	scanned, err := scannedIDSet(p, st,
		TypeComputeNetwork, TypeComputeSubnet, TypeComputeImage,
		TypeComputeSnapshot, TypeComputeRegionSnapshot,
	)
	if err != nil {
		return err
	}
	saByEmail, err := buildSAEmailIndex(p, st)
	if err != nil {
		return err
	}
	rpByName, err := buildResourcePolicyNameIndex(p, st)
	if err != nil {
		return err
	}
	for _, r := range tpls {
		var attrs struct {
			Properties struct {
				NetworkInterfaces []struct {
					Network    string `json:"network"`
					Subnetwork string `json:"subnetwork"`
				} `json:"networkInterfaces"`
				Disks []struct {
					InitializeParams *struct {
						SourceImage    string `json:"sourceImage"`
						SourceSnapshot string `json:"sourceSnapshot"`
					} `json:"initializeParams"`
				} `json:"disks"`
				ServiceAccounts []struct {
					Email string `json:"email"`
				} `json:"serviceAccounts"`
				ResourcePolicies []string `json:"resourcePolicies"`
			} `json:"properties"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		for _, nic := range attrs.Properties.NetworkInterfaces {
			if err := upsertIfScanned(st, scanned, r.ID, "gcp", p.ID, TypeComputeNetwork, nic.Network, store.RelAttachedTo); err != nil {
				return err
			}
			if err := upsertIfScanned(st, scanned, r.ID, "gcp", p.ID, TypeComputeSubnet, nic.Subnetwork, store.RelAttachedTo); err != nil {
				return err
			}
		}
		for _, disk := range attrs.Properties.Disks {
			if disk.InitializeParams == nil {
				continue
			}
			if err := upsertIfScanned(st, scanned, r.ID, "gcp", p.ID, TypeComputeImage, disk.InitializeParams.SourceImage, store.RelAttachedTo); err != nil {
				return err
			}
			if snap := disk.InitializeParams.SourceSnapshot; snap != "" {
				snapType := computeSnapshotTypeForSelfLink(snap)
				if err := upsertIfScanned(st, scanned, r.ID, "gcp", p.ID, snapType, snap, store.RelAttachedTo); err != nil {
					return err
				}
			}
		}
		// InstanceProperties.ResourcePolicies is documented as "(names, not
		// URLs)" — unlike every other source/network field on this struct,
		// which are full self-links. Look up by bare name, not ResourceID.
		for _, rpName := range attrs.Properties.ResourcePolicies {
			rpID, ok := rpByName[rpName]
			if !ok {
				continue
			}
			if err := st.UpsertRelationship(r.ID, rpID, store.RelUses, "directed", nil); err != nil {
				return fmt.Errorf("upsert instance-template→resource-policy: %w", err)
			}
		}
		for _, sa := range attrs.Properties.ServiceAccounts {
			if sa.Email == "" {
				continue
			}
			saID, ok := saByEmail[sa.Email]
			if !ok {
				continue
			}
			if err := st.UpsertRelationship(r.ID, saID, store.RelUses, "directed", nil); err != nil {
				return fmt.Errorf("upsert instance-template→service-account: %w", err)
			}
		}
	}
	return nil
}

// buildResourcePolicyNameIndex maps a ResourcePolicy's bare name (the last
// self-link segment) to its resource ID. Most compute fields reference a
// ResourcePolicy by full self-link, but InstanceProperties.ResourcePolicies
// is the documented exception ("names, not URLs"), so callers reading that
// field need a name-keyed lookup rather than the usual self-link ResourceID
// construction. Ambiguous if two ResourcePolicies share a name across
// regions in the same project (last one wins) — acceptable: GCP resource
// policy names are typically unique per project in practice.
func buildResourcePolicyNameIndex(p *project, st *store.Store) (map[string]string, error) {
	rps, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"gcp"}, AccountID: p.ID, Types: []string{TypeComputeResourcePolicy},
		Limit: util.AllResources,
	})
	if err != nil {
		return nil, err
	}
	idx := make(map[string]string, len(rps))
	for _, rp := range rps {
		idx[lastSegment(rp.NativeID)] = rp.ID
	}
	return idx, nil
}

// upsertIfScanned upserts an edge from fromID to the resource named by
// selfLink (of disco type rtype), when non-empty and present in scanned.
// Shared by resolvers that read a self-link field which may name a resource
// never scanned in this project (cross-project reference, or deleted).
func upsertIfScanned(st *store.Store, scanned map[string]bool, fromID, provider, projectID, rtype, selfLink, kind string) error {
	if selfLink == "" {
		return nil
	}
	toID := store.ResourceID(provider, projectID, rtype, selfLink)
	if !scanned[toID] {
		return nil
	}
	if err := st.UpsertRelationship(fromID, toID, kind, "directed", nil); err != nil {
		return fmt.Errorf("upsert %s -[%s]-> %s: %w", fromID, kind, toID, err)
	}
	return nil
}

// upsertIfScannedAny is upsertIfScanned for fields whose target disco type is
// genuinely ambiguous from the field alone (e.g. a "group" self-link that may
// name an InstanceGroup, a zonal NEG, or a regional/global NEG). Since a
// given self-link belongs to exactly one type in practice, the first
// candidate found in scanned wins; no candidate matching is a normal skip
// (cross-project reference, deleted resource, or unscanned target type).
func upsertIfScannedAny(st *store.Store, scanned map[string]bool, fromID, provider, projectID string, rtypes []string, selfLink, kind string) error {
	if selfLink == "" {
		return nil
	}
	for _, rtype := range rtypes {
		toID := store.ResourceID(provider, projectID, rtype, selfLink)
		if scanned[toID] {
			if err := st.UpsertRelationship(fromID, toID, kind, "directed", nil); err != nil {
				return fmt.Errorf("upsert %s -[%s]-> %s: %w", fromID, kind, toID, err)
			}
			return nil
		}
	}
	return nil
}

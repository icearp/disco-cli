package azure

import (
	"encoding/json"
	"fmt"
	"strings"

	"codeberg.org/icearp/disco/internal/util"
	"codeberg.org/icearp/disco/store"
)

func init() {
	registerResolver(
		resolveTrafficManagerRelationships,
		EdgeDecl{Source: TypeNetworkTrafficManagerProfile, Target: TypeNetworkPublicIPAddress, Kind: store.RelUses},
		EdgeDecl{Source: TypeNetworkTrafficManagerProfile, Target: TypeAppServiceSite, Kind: store.RelUses},
		EdgeDecl{Source: TypeNetworkTrafficManagerProfile, Target: TypeNetworkTrafficManagerProfile, Kind: store.RelUses},
	)
}

// resolveTrafficManagerRelationships derives profile -[uses]-> backend
// resource edges via embedded properties.endpoints[].properties.targetResourceId.
// Endpoints of type ExternalEndpoints / NestedEndpoints carry an FQDN target
// instead of an ARM ID — those are skipped here. Per-sub lowercased
// NativeID index covers any backend type the targetResourceId might point
// at (Public IP, App Service site, another Traffic Manager profile, etc.).
func resolveTrafficManagerRelationships(sub *subscription, st *store.Store) error {
	profiles, err := st.ListResources(store.ResourceFilter{
		Provider: "azure", AccountID: sub.ID,
		Types: []string{TypeNetworkTrafficManagerProfile},
		Limit: util.AllResources,
	})
	if err != nil || len(profiles) == 0 {
		return err
	}
	all, err := st.ListResources(store.ResourceFilter{
		Provider: "azure", AccountID: sub.ID, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	resourceIndex := make(map[string]string, len(all))
	for _, r := range all {
		resourceIndex[strings.ToLower(r.NativeID)] = r.ID
	}

	for _, p := range profiles {
		var attrs struct {
			Properties *struct {
				Endpoints []struct {
					Properties *struct {
						TargetResourceID *string `json:"targetResourceId"`
					} `json:"properties"`
				} `json:"endpoints"`
			} `json:"properties"`
		}
		if err := json.Unmarshal([]byte(p.AttributesJSON), &attrs); err != nil || attrs.Properties == nil {
			continue
		}
		seen := map[string]bool{}
		for _, ep := range attrs.Properties.Endpoints {
			if ep.Properties == nil || ep.Properties.TargetResourceID == nil {
				continue
			}
			key := strings.ToLower(*ep.Properties.TargetResourceID)
			if seen[key] {
				continue
			}
			seen[key] = true
			toID, ok := resourceIndex[key]
			if !ok || toID == p.ID {
				continue
			}
			if err := st.UpsertRelationship(p.ID, toID, store.RelUses, "directed", nil); err != nil {
				return fmt.Errorf("upsert tm-profile→target: %w", err)
			}
		}
	}
	return nil
}

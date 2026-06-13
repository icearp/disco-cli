package azure

import (
	"encoding/json"
	"fmt"
	"strings"

	"codeberg.org/icearp/disco/internal/util"
	"codeberg.org/icearp/disco/store"
)

func init() { registerResolver(resolvePrivateEndpointRelationships) }

// resolvePrivateEndpointRelationships derives two edge classes per PE:
//   - PE -[attached-to]-> subnet's parent VNet (via properties.subnet.id)
//   - PE -[attached-to]-> target resource (via privateLinkServiceConnections[].privateLinkServiceId
//     and manualPrivateLinkServiceConnections[].privateLinkServiceId)
//
// The target resource is matched against a per-sub lowercased NativeID index
// covering every Azure resource — Private Link is provider-agnostic on the
// target side, so any future scanner that stores its native ARM ID picks up
// PE edges automatically.
func resolvePrivateEndpointRelationships(sub *subscription, st *store.Store) error {
	pes, err := st.ListResources(store.ResourceFilter{
		Provider: "azure", AccountID: sub.ID,
		Types: []string{TypeNetworkPrivateEndpoint},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(pes) == 0 {
		return nil
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

	for _, pe := range pes {
		var attrs struct {
			Properties *struct {
				Subnet *struct {
					ID *string `json:"id"`
				} `json:"subnet"`
				PrivateLinkServiceConnections []struct {
					Properties *struct {
						PrivateLinkServiceID *string `json:"privateLinkServiceId"`
					} `json:"properties"`
				} `json:"privateLinkServiceConnections"`
				ManualPrivateLinkServiceConnections []struct {
					Properties *struct {
						PrivateLinkServiceID *string `json:"privateLinkServiceId"`
					} `json:"properties"`
				} `json:"manualPrivateLinkServiceConnections"`
			} `json:"properties"`
		}
		if err := json.Unmarshal([]byte(pe.AttributesJSON), &attrs); err != nil || attrs.Properties == nil {
			continue
		}

		// PE → VNet (via subnet path).
		if attrs.Properties.Subnet != nil && attrs.Properties.Subnet.ID != nil {
			if vnetID := vnetIDFromSubnetID(*attrs.Properties.Subnet.ID); vnetID != "" {
				vnetResourceID := store.ResourceID("azure", sub.ID, TypeNetworkVirtualNetwork, vnetID)
				if _, err := st.GetResource(vnetResourceID); err == nil {
					if err := st.UpsertRelationship(pe.ID, vnetResourceID, store.RelAttachedTo, "directed", nil); err != nil {
						return fmt.Errorf("upsert pe→vnet: %w", err)
					}
				}
			}
		}

		// PE → target resource (across both auto and manual connection lists).
		seen := map[string]bool{}
		emit := func(targetID *string) error {
			if targetID == nil {
				return nil
			}
			// Private link IDs sometimes carry a sub-resource suffix (e.g.
			// /subscriptions/.../storageAccounts/foo/blobServices/default).
			// Try the full ID first, then progressively trim path segments
			// from the right — the first match wins.
			candidate := *targetID
			for candidate != "" {
				key := strings.ToLower(candidate)
				if toID, ok := resourceIndex[key]; ok && toID != pe.ID && !seen[key] {
					seen[key] = true
					return st.UpsertRelationship(pe.ID, toID, store.RelAttachedTo, "directed", nil)
				}
				idx := strings.LastIndex(candidate, "/")
				if idx <= 0 {
					return nil
				}
				candidate = candidate[:idx]
			}
			return nil
		}
		for _, c := range attrs.Properties.PrivateLinkServiceConnections {
			if c.Properties == nil {
				continue
			}
			if err := emit(c.Properties.PrivateLinkServiceID); err != nil {
				return fmt.Errorf("upsert pe→target: %w", err)
			}
		}
		for _, c := range attrs.Properties.ManualPrivateLinkServiceConnections {
			if c.Properties == nil {
				continue
			}
			if err := emit(c.Properties.PrivateLinkServiceID); err != nil {
				return fmt.Errorf("upsert pe→target (manual): %w", err)
			}
		}
	}
	return nil
}

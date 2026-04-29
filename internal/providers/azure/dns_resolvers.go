package azure

import (
	"encoding/json"
	"fmt"
	"strings"

	"codeberg.org/icearp/disco/internal/store"
	"codeberg.org/icearp/disco/internal/util"
)

func init() { registerResolver(resolveDNSRelationships) }

// resolveDNSRelationships derives private-DNS-zone vnet-link
// -[attached-to]-> VNet edges via properties.virtualNetwork.id. Match is
// case-insensitive against a per-sub VNet NativeID index. The link's hierarchy
// to its parent private zone is established at scan time via
// RecordHierarchyBatch (no resolver edge needed).
func resolveDNSRelationships(sub *subscription, st *store.Store) error {
	links, err := st.ListResources(store.ResourceFilter{
		Provider: "azure", AccountID: sub.ID,
		Types: []string{TypeDNSPrivateZoneVNetLink},
		Limit: util.AllResources,
	})
	if err != nil || len(links) == 0 {
		return err
	}
	vnets, err := st.ListResources(store.ResourceFilter{
		Provider: "azure", AccountID: sub.ID,
		Types: []string{TypeNetworkVirtualNetwork},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	vnetIndex := make(map[string]string, len(vnets))
	for _, v := range vnets {
		vnetIndex[strings.ToLower(v.NativeID)] = v.ID
	}

	for _, l := range links {
		var attrs struct {
			Properties *struct {
				VirtualNetwork *struct {
					ID *string `json:"id"`
				} `json:"virtualNetwork"`
			} `json:"properties"`
		}
		if err := json.Unmarshal([]byte(l.AttributesJSON), &attrs); err != nil || attrs.Properties == nil || attrs.Properties.VirtualNetwork == nil || attrs.Properties.VirtualNetwork.ID == nil {
			continue
		}
		toID, ok := vnetIndex[strings.ToLower(*attrs.Properties.VirtualNetwork.ID)]
		if !ok {
			continue
		}
		if err := st.UpsertRelationship(l.ID, toID, store.RelAttachedTo, "directed", nil); err != nil {
			return fmt.Errorf("upsert privatedns-vnetlink→vnet: %w", err)
		}
	}
	return nil
}

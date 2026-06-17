package azure

import (
	"encoding/json"
	"fmt"
	"strings"

	"codeberg.org/icearp/disco/internal/util"
	"codeberg.org/icearp/disco/store"
)

func init() { registerResolver(resolveKustoRelationships) }

// resolveKustoRelationships derives Azure Data Explorer cluster edges:
//   - cluster -[uses]-> Key Vault via the CMK keyVaultProperties.keyVaultUri (vault root)
//   - cluster -[attached-to]-> VNet via virtualNetworkConfiguration.subnetId
func resolveKustoRelationships(sub *subscription, st *store.Store) error {
	clusters, err := st.ListResources(store.ResourceFilter{
		Provider: "azure", AccountID: sub.ID,
		Types: []string{TypeKustoCluster},
		Limit: util.AllResources,
	})
	if err != nil || len(clusters) == 0 {
		return err
	}
	vaultByName, err := vaultNameIndex(sub, st)
	if err != nil {
		return err
	}
	vnetByID, err := nativeIDIndex(sub, st, TypeNetworkVirtualNetwork)
	if err != nil {
		return err
	}
	for _, c := range clusters {
		var attrs struct {
			Properties *struct {
				KeyVaultProperties *struct {
					KeyVaultURI *string `json:"keyVaultUri"`
				} `json:"keyVaultProperties"`
				VirtualNetworkConfiguration *struct {
					SubnetID *string `json:"subnetId"`
				} `json:"virtualNetworkConfiguration"`
			} `json:"properties"`
		}
		if err := json.Unmarshal([]byte(c.AttributesJSON), &attrs); err != nil || attrs.Properties == nil {
			continue
		}
		if kv := attrs.Properties.KeyVaultProperties; kv != nil && kv.KeyVaultURI != nil {
			if name := vaultNameFromVaultURI(*kv.KeyVaultURI); name != "" {
				if toID, ok := vaultByName[strings.ToLower(name)]; ok {
					if err := st.UpsertRelationship(c.ID, toID, store.RelUses, "directed", nil); err != nil {
						return fmt.Errorf("upsert kusto→keyvault: %w", err)
					}
				}
			}
		}
		if vnc := attrs.Properties.VirtualNetworkConfiguration; vnc != nil && vnc.SubnetID != nil {
			if err := upsertVNetAttachment(st, c.ID, *vnc.SubnetID, vnetByID); err != nil {
				return err
			}
		}
	}
	return nil
}

// upsertVNetAttachment resolves a subnet ARM ID to its parent VNet and emits a
// from -[attached-to]-> VNet edge when that VNet is in vnetByID (a lowercased
// NativeID → resource-ID index). ARM IDs are case-insensitive, so both the
// index and the probe are lowercased. Shared by resolvers carrying subnet refs
// (kusto, appplatform, hdinsight).
func upsertVNetAttachment(st *store.Store, fromID, subnetID string, vnetByID map[string]string) error {
	vnetID := vnetIDFromSubnetID(subnetID)
	if vnetID == "" {
		return nil
	}
	toID, ok := vnetByID[strings.ToLower(vnetID)]
	if !ok {
		return nil
	}
	if err := st.UpsertRelationship(fromID, toID, store.RelAttachedTo, "directed", nil); err != nil {
		return fmt.Errorf("upsert →vnet: %w", err)
	}
	return nil
}

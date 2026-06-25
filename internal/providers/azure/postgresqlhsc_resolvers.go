package azure

import (
	"encoding/json"
	"fmt"
	"strings"

	"codeberg.org/icearp/disco/internal/util"
	"codeberg.org/icearp/disco/store"
)

func init() {
	registerResolver(resolvePostgreSQLHSCRelationships,
		EdgeDecl{Source: TypePostgreSQLServerGroupV2, Target: TypeKeyVaultVault, Kind: store.RelUses},
	)
}

// resolvePostgreSQLHSCRelationships wires a Cosmos DB for PostgreSQL (Citus)
// cluster to its CMK Key Vault via properties.dataEncryption.primaryKeyUri
// (a full Key Vault key URI).
func resolvePostgreSQLHSCRelationships(sub *subscription, st *store.Store) error {
	clusters, err := st.ListResources(store.ResourceFilter{
		Provider: "azure", AccountID: sub.ID,
		Types: []string{TypePostgreSQLServerGroupV2},
		Limit: util.AllResources,
	})
	if err != nil || len(clusters) == 0 {
		return err
	}
	vaultByName, err := vaultNameIndex(sub, st)
	if err != nil {
		return err
	}
	for _, c := range clusters {
		var attrs struct {
			Properties *struct {
				DataEncryption *struct {
					PrimaryKeyURI *string `json:"primaryKeyUri"`
				} `json:"dataEncryption"`
			} `json:"properties"`
		}
		if err := json.Unmarshal([]byte(c.AttributesJSON), &attrs); err != nil ||
			attrs.Properties == nil || attrs.Properties.DataEncryption == nil {
			continue
		}
		if url := attrs.Properties.DataEncryption.PrimaryKeyURI; url != nil {
			if name := vaultNameFromKeyURI(*url); name != "" {
				if toID, ok := vaultByName[strings.ToLower(name)]; ok {
					if err := st.UpsertRelationship(c.ID, toID, store.RelUses, "directed", nil); err != nil {
						return fmt.Errorf("upsert postgresqlhsc→keyvault: %w", err)
					}
				}
			}
		}
	}
	return nil
}

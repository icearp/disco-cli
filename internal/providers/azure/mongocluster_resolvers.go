package azure

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/icearp/disco-cli/internal/util"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerResolver(resolveMongoClusterRelationships,
		EdgeDecl{Source: TypeMongoCluster, Target: TypeKeyVaultVault, Kind: store.RelUses},
	)
}

// resolveMongoClusterRelationships wires a Cosmos DB for MongoDB (vCore)
// cluster to its CMK Key Vault via
// properties.encryption.customerManagedKeyEncryption.keyEncryptionKeyUrl
// (a full Key Vault key URI).
func resolveMongoClusterRelationships(sub *subscription, st *store.Store) error {
	clusters, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"azure"}, AccountID: sub.ID,
		Types: []string{TypeMongoCluster},
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
				Encryption *struct {
					CustomerManagedKeyEncryption *struct {
						KeyEncryptionKeyURL *string `json:"keyEncryptionKeyUrl"`
					} `json:"customerManagedKeyEncryption"`
				} `json:"encryption"`
			} `json:"properties"`
		}
		if err := json.Unmarshal([]byte(c.AttributesJSON), &attrs); err != nil || attrs.Properties == nil ||
			attrs.Properties.Encryption == nil || attrs.Properties.Encryption.CustomerManagedKeyEncryption == nil {
			continue
		}
		if url := attrs.Properties.Encryption.CustomerManagedKeyEncryption.KeyEncryptionKeyURL; url != nil {
			if name := vaultNameFromKeyURI(*url); name != "" {
				if toID, ok := vaultByName[strings.ToLower(name)]; ok {
					if err := st.UpsertRelationship(c.ID, toID, store.RelUses, "directed", nil); err != nil {
						return fmt.Errorf("upsert mongocluster→keyvault: %w", err)
					}
				}
			}
		}
	}
	return nil
}

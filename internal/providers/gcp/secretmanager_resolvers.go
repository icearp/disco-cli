package gcp

import (
	"encoding/json"
	"fmt"

	"codeberg.org/icearp/disco/internal/util"
	"codeberg.org/icearp/disco/store"
)

func init() {
	registerResolver(resolveSecretRelationships,
		EdgeDecl{TypeSecret, TypeKMSCryptoKey, store.RelUses},
	)
}

// resolveSecretRelationships derives secret -[uses]-> cryptoKey CMEK edges.
// Secrets express CMEK in three places depending on replication mode:
//
//  1. replication.automatic.customerManagedEncryption.kmsKeyName
//     (single-region or auto-replicated secret with a single CMEK key)
//  2. replication.userManaged.replicas[].customerManagedEncryption.kmsKeyName
//     (per-replica CMEK; usually a different key per location)
//  3. top-level customerManagedEncryption.kmsKeyName
//     (regional secrets — newer regional API surface)
//
// All three paths flow into the same edge emitter, deduped per (secret, key).
// Cross-project key references are skipped (FK-safe).
//
// Secret → IAM policy edges deferred — Secret Manager exposes
// `secrets.getIamPolicy` per-secret (N extra calls per project); the
// project-scope IAM policy resource (R4.1) already captures the broad
// principal → secret access pattern via role grants like
// roles/secretmanager.secretAccessor.
func resolveSecretRelationships(p *project, st *store.Store) error {
	keys, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"gcp"}, AccountID: p.ID, Types: []string{TypeKMSCryptoKey},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(keys) == 0 {
		return nil
	}
	keyIDByNative := make(map[string]string, len(keys))
	for _, k := range keys {
		keyIDByNative[k.NativeID] = k.ID
	}

	secrets, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"gcp"}, AccountID: p.ID, Types: []string{TypeSecret},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	for _, s := range secrets {
		var attrs struct {
			CustomerManagedEncryption struct {
				KmsKeyName string `json:"kmsKeyName"`
			} `json:"customerManagedEncryption"`
			Replication struct {
				Automatic struct {
					CustomerManagedEncryption struct {
						KmsKeyName string `json:"kmsKeyName"`
					} `json:"customerManagedEncryption"`
				} `json:"automatic"`
				UserManaged struct {
					Replicas []struct {
						CustomerManagedEncryption struct {
							KmsKeyName string `json:"kmsKeyName"`
						} `json:"customerManagedEncryption"`
					} `json:"replicas"`
				} `json:"userManaged"`
			} `json:"replication"`
		}
		if err := json.Unmarshal([]byte(s.AttributesJSON), &attrs); err != nil {
			continue
		}
		seen := make(map[string]bool, 4)
		emit := func(rawKey string) error {
			keyName := stripCryptoKeyVersion(rawKey)
			if keyName == "" || seen[keyName] {
				return nil
			}
			seen[keyName] = true
			keyID, ok := keyIDByNative[keyName]
			if !ok {
				return nil
			}
			if err := st.UpsertRelationship(s.ID, keyID, store.RelUses, "directed", nil); err != nil {
				return fmt.Errorf("upsert secret→cryptoKey: %w", err)
			}
			return nil
		}
		if err := emit(attrs.CustomerManagedEncryption.KmsKeyName); err != nil {
			return err
		}
		if err := emit(attrs.Replication.Automatic.CustomerManagedEncryption.KmsKeyName); err != nil {
			return err
		}
		for _, rep := range attrs.Replication.UserManaged.Replicas {
			if err := emit(rep.CustomerManagedEncryption.KmsKeyName); err != nil {
				return err
			}
		}
	}
	return nil
}

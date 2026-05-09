package gcp

import (
	"encoding/json"
	"fmt"

	"codeberg.org/icearp/disco/store"
	"codeberg.org/icearp/disco/internal/util"
)

func init() { registerResolver(resolveBigQueryRelationships) }

// resolveBigQueryRelationships derives dataset -[uses]-> cryptoKey CMEK
// edges from `defaultEncryptionConfiguration.kmsKeyName`. The dataset CMEK
// applies to every newly-created table within unless overridden — useful
// rule-engine pivot for "all data in this dataset is CMEK-encrypted with
// rotation interval N".
//
// Per-table CMEK edges deferred — table list-shape doesn't include
// encryption config; would need a Tables.Get fan-out per table, an
// expensive call we defer until rule-engine demand justifies it.
// Authorized-views (one dataset granting another query access) deferred —
// stored under `access[].view` on the dataset Get response, but the edge
// shape is dataset → dataset which is rare to query graph-style.
func resolveBigQueryRelationships(p *project, st *store.Store) error {
	keys, err := st.ListResources(store.ResourceFilter{
		Provider: "gcp", AccountID: p.ID, Types: []string{TypeKMSCryptoKey},
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

	dss, err := st.ListResources(store.ResourceFilter{
		Provider: "gcp", AccountID: p.ID, Types: []string{TypeBQDataset},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	for _, d := range dss {
		var a struct {
			DefaultEncryptionConfiguration struct {
				KmsKeyName string `json:"kmsKeyName"`
			} `json:"defaultEncryptionConfiguration"`
		}
		if err := json.Unmarshal([]byte(d.AttributesJSON), &a); err != nil {
			continue
		}
		key := stripCryptoKeyVersion(a.DefaultEncryptionConfiguration.KmsKeyName)
		if key == "" {
			continue
		}
		keyID, ok := keyIDByNative[key]
		if !ok {
			continue
		}
		if err := st.UpsertRelationship(d.ID, keyID, store.RelUses, "directed", nil); err != nil {
			return fmt.Errorf("upsert dataset→cryptoKey: %w", err)
		}
	}
	return nil
}

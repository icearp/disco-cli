package gcp

import (
	"encoding/json"
	"fmt"
	"strings"

	"codeberg.org/icearp/disco/internal/util"
	"codeberg.org/icearp/disco/store"
)

func init() {
	registerResolver(resolveKMSRelationships,
		EdgeDecl{TypeStorageBucket, TypeKMSCryptoKey, store.RelUses},
	)
}

// resolveKMSRelationships derives bucket → cryptoKey CMEK edges. Full CMEK
// matrix (BigQuery dataset, Compute disk, SQL instance, Pub/Sub topic, Secret
// Manager) deferred — Storage is the highest-volume CMEK consumer today and
// exercises the lookup path; remaining services land alongside their scanners.
func resolveKMSRelationships(p *project, st *store.Store) error {
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

	buckets, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"gcp"}, AccountID: p.ID, Types: []string{TypeStorageBucket},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	for _, b := range buckets {
		var attrs struct {
			Encryption struct {
				DefaultKmsKeyName string `json:"defaultKmsKeyName"`
			} `json:"encryption"`
		}
		if err := json.Unmarshal([]byte(b.AttributesJSON), &attrs); err != nil {
			continue
		}
		// defaultKmsKeyName includes a "/cryptoKeyVersions/N" suffix when
		// pinned to a version; KMS NativeIDs end at the cryptoKey level, so
		// strip the suffix before lookup.
		keyName := stripCryptoKeyVersion(attrs.Encryption.DefaultKmsKeyName)
		if keyName == "" {
			continue
		}
		keyID, ok := keyIDByNative[keyName]
		if !ok {
			// Cross-project key references skipped — would FK-violate.
			continue
		}
		if err := st.UpsertRelationship(b.ID, keyID, store.RelUses, "directed", nil); err != nil {
			return fmt.Errorf("upsert bucket→cryptoKey: %w", err)
		}
	}
	return nil
}

// stripCryptoKeyVersion returns the cryptoKey-level resource name when the
// input is either a cryptoKey name or a cryptoKeyVersion name.
func stripCryptoKeyVersion(s string) string {
	if before, _, ok := strings.Cut(s, "/cryptoKeyVersions/"); ok {
		return before
	}
	return s
}

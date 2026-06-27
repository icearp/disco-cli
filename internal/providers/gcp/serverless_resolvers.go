package gcp

import (
	"encoding/json"
	"fmt"

	"codeberg.org/icearp/disco/internal/util"
	"codeberg.org/icearp/disco/store"
)

func init() { registerResolver(resolveServerlessRelationships) }

// resolveServerlessRelationships derives runtime-identity edges for the
// serverless surfaces:
//
//   - cloudFunction -[uses]-> service-account (via serviceConfig.serviceAccountEmail)
//   - cloudFunction -[uses]-> cryptoKey       (via kmsKeyName)
//   - run.service   -[uses]-> service-account (via template.serviceAccount)
//
// VPC-connector edges deferred — connectors are a separate scanner surface
// (`vpcaccess.googleapis.com`) that hasn't landed yet. EventTrigger →
// Pub/Sub topic / Cloud Storage bucket edges deferred — Pub/Sub topic
// scanner (R4.11) lands next iteration; storage edge would need parsing
// EventTrigger.eventFilters[]. SA edges land now because the SA index
// shape from R4.1 is reused.
func resolveServerlessRelationships(p *project, st *store.Store) error {
	saByEmail, err := buildSAEmailIndex(p, st)
	if err != nil {
		return err
	}

	// KMS index for function CMEK.
	keys, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"gcp"}, AccountID: p.ID, Types: []string{TypeKMSCryptoKey},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	keyIDByNative := make(map[string]string, len(keys))
	for _, k := range keys {
		keyIDByNative[k.NativeID] = k.ID
	}

	// Cloud Functions.
	fns, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"gcp"}, AccountID: p.ID, Types: []string{TypeCloudFunction},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	for _, f := range fns {
		var a struct {
			KmsKeyName    string `json:"kmsKeyName"`
			ServiceConfig struct {
				ServiceAccountEmail string `json:"serviceAccountEmail"`
			} `json:"serviceConfig"`
		}
		if err := json.Unmarshal([]byte(f.AttributesJSON), &a); err != nil {
			continue
		}
		if a.ServiceConfig.ServiceAccountEmail != "" {
			if saID, ok := saByEmail[a.ServiceConfig.ServiceAccountEmail]; ok {
				if err := st.UpsertRelationship(f.ID, saID, store.RelUses, "directed", nil); err != nil {
					return fmt.Errorf("upsert function→SA: %w", err)
				}
			}
		}
		if a.KmsKeyName != "" {
			keyName := stripCryptoKeyVersion(a.KmsKeyName)
			if keyID, ok := keyIDByNative[keyName]; ok {
				if err := st.UpsertRelationship(f.ID, keyID, store.RelUses, "directed", nil); err != nil {
					return fmt.Errorf("upsert function→cryptoKey: %w", err)
				}
			}
		}
	}

	// Cloud Run services.
	runs, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"gcp"}, AccountID: p.ID, Types: []string{TypeCloudRunSvc},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	for _, s := range runs {
		var a struct {
			Template struct {
				ServiceAccount string `json:"serviceAccount"`
			} `json:"template"`
		}
		if err := json.Unmarshal([]byte(s.AttributesJSON), &a); err != nil {
			continue
		}
		if a.Template.ServiceAccount == "" {
			continue
		}
		saID, ok := saByEmail[a.Template.ServiceAccount]
		if !ok {
			continue
		}
		if err := st.UpsertRelationship(s.ID, saID, store.RelUses, "directed", nil); err != nil {
			return fmt.Errorf("upsert run.service→SA: %w", err)
		}
	}
	return nil
}

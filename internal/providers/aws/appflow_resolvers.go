package aws

import (
	"encoding/json"
	"fmt"

	"codeberg.org/icearp/disco/internal/store"
	"codeberg.org/icearp/disco/internal/util"
)

func init() { registerResolver(resolveAppFlowRelationships) }

// resolveAppFlowRelationships emits flow → KMS edges via FlowDefinition's
// per-flow `kmsArn` field. Connector-profile / source / destination edges
// are deferred until the per-(flow, profile) describe fan-out lands —
// FlowDefinition itself carries only the connector-type discriminator
// (e.g. "S3", "Salesforce") plus a connector-profile name, not target ARNs.
func resolveAppFlowRelationships(acct *account, st *store.Store) error {
	flows, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeAppFlowFlow},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(flows) == 0 {
		return nil
	}
	kmsIdx, err := loadKMSResolveIndex(acct, st)
	if err != nil {
		return err
	}
	type attrs struct {
		KmsArn *string `json:"KmsArn"`
	}
	for _, f := range flows {
		var a attrs
		if err := json.Unmarshal([]byte(f.AttributesJSON), &a); err != nil {
			continue
		}
		region := sv(f.Region)
		if keyID, ok := kmsIdx.resolveKMSKeyID(sv(a.KmsArn), region, acct.ID); ok {
			if err := st.UpsertRelationship(f.ID, keyID, store.RelUses, "directed", nil); err != nil {
				return fmt.Errorf("upsert appflow→kms: %w", err)
			}
		}
	}
	return nil
}

package aws

import (
	"encoding/json"
	"fmt"

	"codeberg.org/icearp/disco/internal/util"
	"codeberg.org/icearp/disco/store"
)

func init() {
	registerResolver(
		resolveAppFlowRelationships,
		EdgeDecl{TypeAppFlowFlow, TypeKMSKey, store.RelUses},
	)
	registerResolver(
		resolveAppFlowConnectorProfileRelationships,
		EdgeDecl{TypeAppFlowConnectorProfile, TypeSecretsManagerSecret, store.RelUses},
	)
}

// resolveAppFlowConnectorProfileRelationships wires each connector profile to
// the Secrets Manager secret holding its credentials (CredentialsArn).
// sanitize.go's denylist redacts scalars under `credential`-substring keys,
// but its shape-bounded ARN allowlist preserves the value verbatim, so it
// survives scrubbing and is readable here.
func resolveAppFlowConnectorProfileRelationships(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeAppFlowConnectorProfile}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	secretSet, err := scannedIDSet(acct, st, TypeSecretsManagerSecret)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			CredentialsArn *string `json:"CredentialsArn"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		ca := sv(attrs.CredentialsArn)
		if ca == "" {
			continue
		}
		tgt := store.ResourceID("aws", acct.ID, ca)
		if !secretSet[tgt] {
			continue
		}
		if err := st.UpsertRelationship(r.ID, tgt, store.RelUses, "directed", nil); err != nil {
			return fmt.Errorf("upsert appflow connector-profile→secret: %w", err)
		}
	}
	return nil
}

// resolveAppFlowRelationships emits flow → KMS edges via FlowDefinition's
// per-flow `kmsArn` field. Connector-profile / source / destination edges
// are deferred until the per-(flow, profile) describe fan-out lands —
// FlowDefinition carries only the connector-type discriminator
// (e.g. "S3", "Salesforce") plus a connector-profile name, not target ARNs.
func resolveAppFlowRelationships(acct *account, st *store.Store) error {
	flows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeAppFlowFlow},
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

package aws

import (
	"encoding/json"
	"fmt"
	"strings"

	"codeberg.org/icearp/disco/internal/util"
	"codeberg.org/icearp/disco/store"
)

func init() {
	registerResolver(
		resolveSecretsManagerKMS,
		EdgeDecl{TypeSecretsManagerSecret, TypeKMSKey, store.RelUses},
	)
	registerResolver(
		resolveSecretsManagerRotation,
		EdgeDecl{TypeSecretsManagerSecret, TypeLambdaFunction, store.RelUses},
	)
	registerResolver(
		resolveSecretsManagerReplication,
		EdgeDecl{TypeSecretsManagerSecret, TypeSecretsManagerSecret, store.RelAttachedTo},
	)
}

// resolveSecretsManagerReplication links each replica secret to its primary.
// A secret is a replica when PrimaryRegion differs from its own region; the
// primary's ARN is derived by swapping the region segment of the secret's ARN.
func resolveSecretsManagerReplication(acct *account, st *store.Store) error {
	secrets, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"},
		AccountID: acct.ID,
		Types:     []string{TypeSecretsManagerSecret},
		Limit:     util.AllResources,
	})
	if err != nil {
		return err
	}
	for _, s := range secrets {
		var attrs struct {
			PrimaryRegion *string `json:"PrimaryRegion"`
		}
		if err := json.Unmarshal([]byte(s.AttributesJSON), &attrs); err != nil {
			continue
		}
		primary := sv(attrs.PrimaryRegion)
		own := sv(s.Region)
		if primary == "" || own == "" || primary == own {
			continue
		}
		// Secret ARN: arn:aws:secretsmanager:<region>:<acct>:secret:<name>-<6chars>
		parts := strings.SplitN(s.NativeID, ":", 6)
		if len(parts) < 6 || parts[0] != "arn" {
			continue
		}
		parts[3] = primary
		primaryARN := strings.Join(parts, ":")
		primaryID := store.ResourceID("aws", acct.ID, TypeSecretsManagerSecret, primaryARN)
		if err := st.UpsertRelationship(s.ID, primaryID, store.RelAttachedTo, "directed", nil); err != nil {
			return fmt.Errorf("upsert secret→primary-secret: %w", err)
		}
	}
	return nil
}

// resolveSecretsManagerRotation links each secret with rotation enabled to its
// rotation Lambda. RotationLambdaARN is absent when rotation isn't enabled.
func resolveSecretsManagerRotation(acct *account, st *store.Store) error {
	secrets, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"},
		AccountID: acct.ID,
		Types:     []string{TypeSecretsManagerSecret},
		Limit:     util.AllResources,
	})
	if err != nil {
		return err
	}
	for _, s := range secrets {
		var attrs struct {
			RotationLambdaARN *string `json:"RotationLambdaARN"`
		}
		if err := json.Unmarshal([]byte(s.AttributesJSON), &attrs); err != nil || sv(attrs.RotationLambdaARN) == "" {
			continue
		}
		fnID := store.ResourceID("aws", acct.ID, TypeLambdaFunction, *attrs.RotationLambdaARN)
		if err := st.UpsertRelationship(s.ID, fnID, store.RelUses, "directed", nil); err != nil {
			return fmt.Errorf("upsert secret→rotation-lambda: %w", err)
		}
	}
	return nil
}

// resolveSecretsManagerKMS links each secret to its encrypting KMS key.
// KmsKeyID is omitted for the AWS-managed default key — skip since disco
// doesn't scan AWS-managed keys.
func resolveSecretsManagerKMS(acct *account, st *store.Store) error {
	secrets, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"},
		AccountID: acct.ID,
		Types:     []string{TypeSecretsManagerSecret},
		Limit:     util.AllResources,
	})
	if err != nil {
		return err
	}
	keys, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"},
		AccountID: acct.ID,
		Types:     []string{TypeKMSKey},
		Limit:     util.AllResources,
	})
	if err != nil {
		return err
	}
	knownKey := make(map[string]bool, len(keys))
	for _, k := range keys {
		knownKey[k.ID] = true
	}
	for _, s := range secrets {
		var attrs struct {
			KmsKeyID *string `json:"KmsKeyID"`
		}
		if err := json.Unmarshal([]byte(s.AttributesJSON), &attrs); err != nil || attrs.KmsKeyID == nil || *attrs.KmsKeyID == "" {
			continue
		}
		keyID := store.ResourceID("aws", acct.ID, TypeKMSKey, *attrs.KmsKeyID)
		if !knownKey[keyID] {
			continue
		}
		if err := st.UpsertRelationship(s.ID, keyID, store.RelUses, "directed", nil); err != nil {
			return fmt.Errorf("upsert secret→kms: %w", err)
		}
	}
	return nil
}

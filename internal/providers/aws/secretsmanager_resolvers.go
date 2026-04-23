package aws

import (
	"encoding/json"
	"fmt"

	"codeberg.org/icearp/disco/internal/store"
	"codeberg.org/icearp/disco/internal/util"
)

func init() {
	registerResolver(resolveSecretsManagerKMS)
	registerResolver(resolveSecretsManagerRotation)
}

// resolveSecretsManagerRotation links each secret with rotation enabled to the
// Lambda function that performs the rotation. RotationLambdaARN is absent for
// secrets without automatic rotation.
func resolveSecretsManagerRotation(acct *account, st *store.Store) error {
	secrets, err := st.ListResources(store.ResourceFilter{
		Provider:  "aws",
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

// resolveSecretsManagerKMS links each secret to the KMS key that encrypts it.
// KmsKeyId is omitted when the AWS-managed default key is used — skip in that
// case since disco doesn't scan AWS-managed keys.
func resolveSecretsManagerKMS(acct *account, st *store.Store) error {
	secrets, err := st.ListResources(store.ResourceFilter{
		Provider:  "aws",
		AccountID: acct.ID,
		Types:     []string{TypeSecretsManagerSecret},
		Limit:     util.AllResources,
	})
	if err != nil {
		return err
	}
	keys, err := st.ListResources(store.ResourceFilter{
		Provider:  "aws",
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
			KmsKeyId *string `json:"KmsKeyId"`
		}
		if err := json.Unmarshal([]byte(s.AttributesJSON), &attrs); err != nil || attrs.KmsKeyId == nil || *attrs.KmsKeyId == "" {
			continue
		}
		keyID := store.ResourceID("aws", acct.ID, TypeKMSKey, *attrs.KmsKeyId)
		if !knownKey[keyID] {
			continue
		}
		if err := st.UpsertRelationship(s.ID, keyID, store.RelUses, "directed", nil); err != nil {
			return fmt.Errorf("upsert secret→kms: %w", err)
		}
	}
	return nil
}

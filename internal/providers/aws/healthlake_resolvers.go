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
		resolveHealthLakeDatastoreRefs,
		EdgeDecl{TypeHealthLakeFHIRDatastore, TypeKMSKey, store.RelUses},
		EdgeDecl{TypeHealthLakeFHIRDatastore, TypeLambdaFunction, store.RelUses},
	)
}

// resolveHealthLakeDatastoreRefs wires each FHIR datastore to its CMEK
// (SseConfiguration.KmsEncryptionConfig.KmsKeyId) and SMART-on-FHIR
// authorizer Lambda (IdentityProviderConfiguration.IdpLambdaArn).
func resolveHealthLakeDatastoreRefs(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeHealthLakeFHIRDatastore}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	idx, err := loadKMSResolveIndex(acct, st)
	if err != nil {
		return err
	}
	lambdaSet, err := scannedIDSet(acct, st, TypeLambdaFunction)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			SseConfiguration *struct {
				KmsEncryptionConfig *struct {
					KmsKeyID *string `json:"KmsKeyId"`
				} `json:"KmsEncryptionConfig"`
			} `json:"SseConfiguration"`
			IdentityProviderConfiguration *struct {
				IdpLambdaArn *string `json:"IdpLambdaArn"`
			} `json:"IdentityProviderConfiguration"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if attrs.SseConfiguration != nil && attrs.SseConfiguration.KmsEncryptionConfig != nil {
			if ref := sv(attrs.SseConfiguration.KmsEncryptionConfig.KmsKeyID); ref != "" {
				if keyID, ok := idx.resolveKMSKeyID(ref, sv(r.Region), acct.ID); ok {
					if err := st.UpsertRelationship(r.ID, keyID, store.RelUses, "directed", nil); err != nil {
						return fmt.Errorf("upsert health-lake→kms: %w", err)
					}
				}
			}
		}
		if attrs.IdentityProviderConfiguration != nil {
			if larn := sv(attrs.IdentityProviderConfiguration.IdpLambdaArn); strings.Contains(larn, ":lambda:") {
				tgt := store.ResourceID("aws", acct.ID, larn)
				if lambdaSet[tgt] {
					if err := st.UpsertRelationship(r.ID, tgt, store.RelUses, "directed", nil); err != nil {
						return fmt.Errorf("upsert health-lake→lambda: %w", err)
					}
				}
			}
		}
	}
	return nil
}

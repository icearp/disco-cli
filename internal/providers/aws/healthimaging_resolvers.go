package aws

import (
	"encoding/json"
	"fmt"
	"strings"

	"codeberg.org/icearp/disco/internal/store"
	"codeberg.org/icearp/disco/internal/util"
)

func init() {
	registerResolver(resolveHealthImagingDatastoreRefs,
		EdgeDecl{TypeHealthImagingDatastore, TypeKMSKey, store.RelUses},
		EdgeDecl{TypeHealthImagingDatastore, TypeLambdaFunction, store.RelUses},
	)
}

// resolveHealthImagingDatastoreRefs wires each datastore to its CMEK and
// optional Lambda authorizer. Get-body shape carries KmsKeyArn +
// LambdaAuthorizerArn directly.
func resolveHealthImagingDatastoreRefs(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeHealthImagingDatastore}, Limit: util.AllResources,
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
			KmsKeyArn           *string `json:"KmsKeyArn"`
			LambdaAuthorizerArn *string `json:"LambdaAuthorizerArn"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if ref := sv(attrs.KmsKeyArn); ref != "" {
			if keyID, ok := idx.resolveKMSKeyID(ref, sv(r.Region), acct.ID); ok {
				if err := st.UpsertRelationship(r.ID, keyID, store.RelUses, "directed", nil); err != nil {
					return fmt.Errorf("upsert health-imaging→kms: %w", err)
				}
			}
		}
		if larn := sv(attrs.LambdaAuthorizerArn); strings.Contains(larn, ":lambda:") {
			tgt := store.ResourceID("aws", acct.ID, TypeLambdaFunction, larn)
			if lambdaSet[tgt] {
				if err := st.UpsertRelationship(r.ID, tgt, store.RelUses, "directed", nil); err != nil {
					return fmt.Errorf("upsert health-imaging→lambda: %w", err)
				}
			}
		}
	}
	return nil
}

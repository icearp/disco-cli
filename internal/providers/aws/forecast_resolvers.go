package aws

import (
	"encoding/json"
	"fmt"

	"github.com/icearp/disco-cli/internal/util"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerResolver(
		resolveForecastDatasetRefs,
		EdgeDecl{TypeForecastDataset, TypeKMSKey, store.RelUses},
		EdgeDecl{TypeForecastDataset, TypeIAMRole, store.RelAssumes},
	)
	registerResolver(
		resolveForecastDatasetGroupMembers,
		EdgeDecl{TypeForecastDatasetGroup, TypeForecastDataset, store.RelContains},
	)
}

// resolveForecastDatasetRefs wires each dataset to its CMK + IAM role
// (EncryptionConfig.{KMSKeyArn,RoleArn}), both from the DescribeDataset body
// scanForecastDatasets fans out per row.
func resolveForecastDatasetRefs(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeForecastDataset}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	kmsIdx, err := loadKMSResolveIndex(acct, st)
	if err != nil {
		return err
	}
	roleSet, err := scannedIDSet(acct, st, TypeIAMRole)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			EncryptionConfig *struct {
				KMSKeyArn *string `json:"KMSKeyArn"`
				RoleArn   *string `json:"RoleArn"`
			} `json:"EncryptionConfig"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if attrs.EncryptionConfig == nil {
			continue
		}
		if k := sv(attrs.EncryptionConfig.KMSKeyArn); k != "" {
			if keyID, ok := kmsIdx.resolveKMSKeyID(k, sv(r.Region), acct.ID); ok {
				if err := st.UpsertRelationship(r.ID, keyID, store.RelUses, "directed", nil); err != nil {
					return fmt.Errorf("upsert forecast dataset→kms: %w", err)
				}
			}
		}
		if ra := sv(attrs.EncryptionConfig.RoleArn); ra != "" {
			tgt := store.ResourceID("aws", acct.ID, ra)
			if roleSet[tgt] {
				if err := st.UpsertRelationship(r.ID, tgt, store.RelAssumes, "directed", nil); err != nil {
					return fmt.Errorf("upsert forecast dataset→role: %w", err)
				}
			}
		}
	}
	return nil
}

// resolveForecastDatasetGroupMembers wires each dataset group to its datasets
// (DatasetArns[]) via UpsertRelationship, not RecordHierarchyBatch — datasets
// exist independently of any group.
func resolveForecastDatasetGroupMembers(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeForecastDatasetGroup}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	dsSet, err := scannedIDSet(acct, st, TypeForecastDataset)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			DatasetArns []string `json:"DatasetArns"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		for _, arn := range attrs.DatasetArns {
			if arn == "" {
				continue
			}
			tgt := store.ResourceID("aws", acct.ID, arn)
			if !dsSet[tgt] {
				continue
			}
			if err := st.UpsertRelationship(r.ID, tgt, store.RelContains, "directed", nil); err != nil {
				return fmt.Errorf("upsert forecast group→dataset: %w", err)
			}
		}
	}
	return nil
}

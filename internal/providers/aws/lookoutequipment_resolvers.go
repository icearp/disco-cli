package aws

import (
	"encoding/json"
	"fmt"

	"codeberg.org/icearp/disco/internal/util"
	"codeberg.org/icearp/disco/store"
)

func init() {
	registerResolver(
		resolveLookoutEquipmentSchedulerRefs,
		EdgeDecl{TypeLookoutEquipmentInferenceScheduler, TypeIAMRole, store.RelAssumes},
		EdgeDecl{TypeLookoutEquipmentInferenceScheduler, TypeKMSKey, store.RelUses},
		EdgeDecl{TypeLookoutEquipmentInferenceScheduler, TypeS3Bucket, store.RelUses},
	)
	registerResolver(
		resolveLookoutEquipmentModelVersionRefs,
		EdgeDecl{TypeLookoutEquipmentModelVersion, TypeLookoutEquipmentModel, store.RelAttachedTo},
	)
}

// resolveLookoutEquipmentModelVersionRefs wires each model version to its
// parent model via the ModelArn on the version summary. The version's own
// NativeID is synthesized {modelArn}/version/{n}, but ModelArn is
// authoritative for the parent lookup.
func resolveLookoutEquipmentModelVersionRefs(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeLookoutEquipmentModelVersion}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	modelSet, err := scannedIDSet(acct, st, TypeLookoutEquipmentModel)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			ModelArn *string `json:"ModelArn"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		marn := sv(attrs.ModelArn)
		if marn == "" {
			continue
		}
		tgt := store.ResourceID("aws", acct.ID, marn)
		if !modelSet[tgt] {
			continue
		}
		if err := st.UpsertRelationship(r.ID, tgt, store.RelAttachedTo, "directed", nil); err != nil {
			return fmt.Errorf("upsert le model-version→model: %w", err)
		}
	}
	return nil
}

// resolveLookoutEquipmentSchedulerRefs wires each inference scheduler to its
// IAM role (RoleArn), KMS keys (ServerSideKmsKeyID + output-config KmsKeyID),
// and S3 buckets (input + output bucket). ModelArn refs a LookoutEquipment ML
// model, unscanned by disco — ref skipped.
func resolveLookoutEquipmentSchedulerRefs(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeLookoutEquipmentInferenceScheduler}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	roleSet, err := scannedIDSet(acct, st, TypeIAMRole)
	if err != nil {
		return err
	}
	kmsIdx, err := loadKMSResolveIndex(acct, st)
	if err != nil {
		return err
	}
	bucketSet, err := scannedIDSet(acct, st, TypeS3Bucket)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			RoleArn                *string `json:"RoleArn"`
			ServerSideKmsKeyID     *string `json:"ServerSideKmsKeyId"`
			DataInputConfiguration *struct {
				S3InputConfiguration *struct {
					Bucket *string `json:"Bucket"`
				} `json:"S3InputConfiguration"`
			} `json:"DataInputConfiguration"`
			DataOutputConfiguration *struct {
				KmsKeyID              *string `json:"KmsKeyId"`
				S3OutputConfiguration *struct {
					Bucket *string `json:"Bucket"`
				} `json:"S3OutputConfiguration"`
			} `json:"DataOutputConfiguration"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		region := sv(r.Region)
		if ra := sv(attrs.RoleArn); ra != "" {
			tgt := store.ResourceID("aws", acct.ID, ra)
			if roleSet[tgt] {
				if err := st.UpsertRelationship(r.ID, tgt, store.RelAssumes, "directed", nil); err != nil {
					return fmt.Errorf("upsert le scheduler→role: %w", err)
				}
			}
		}
		var kmsRefs []string
		kmsRefs = append(kmsRefs, sv(attrs.ServerSideKmsKeyID))
		if attrs.DataOutputConfiguration != nil {
			kmsRefs = append(kmsRefs, sv(attrs.DataOutputConfiguration.KmsKeyID))
		}
		for _, kref := range kmsRefs {
			if kref == "" {
				continue
			}
			if keyID, ok := kmsIdx.resolveKMSKeyID(kref, region, acct.ID); ok {
				if err := st.UpsertRelationship(r.ID, keyID, store.RelUses, "directed", nil); err != nil {
					return fmt.Errorf("upsert le scheduler→kms: %w", err)
				}
			}
		}
		var buckets []string
		if attrs.DataInputConfiguration != nil && attrs.DataInputConfiguration.S3InputConfiguration != nil {
			buckets = append(buckets, sv(attrs.DataInputConfiguration.S3InputConfiguration.Bucket))
		}
		if attrs.DataOutputConfiguration != nil && attrs.DataOutputConfiguration.S3OutputConfiguration != nil {
			buckets = append(buckets, sv(attrs.DataOutputConfiguration.S3OutputConfiguration.Bucket))
		}
		seen := map[string]struct{}{}
		for _, b := range buckets {
			if b == "" {
				continue
			}
			if _, ok := seen[b]; ok {
				continue
			}
			seen[b] = struct{}{}
			bARN := "arn:aws:s3:::" + b
			tgt := store.ResourceID("aws", acct.ID, bARN)
			if !bucketSet[tgt] {
				continue
			}
			if err := st.UpsertRelationship(r.ID, tgt, store.RelUses, "directed", nil); err != nil {
				return fmt.Errorf("upsert le scheduler→s3: %w", err)
			}
		}
	}
	return nil
}

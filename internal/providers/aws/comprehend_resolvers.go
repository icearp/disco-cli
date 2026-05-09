package aws

import (
	"encoding/json"
	"fmt"

	"codeberg.org/icearp/disco/store"
	"codeberg.org/icearp/disco/internal/util"
)

func init() {
	registerResolver(
		resolveComprehendDocumentClassifierRefs,
		EdgeDecl{TypeComprehendDocumentClassifier, TypeIAMRole, store.RelUses},
		EdgeDecl{TypeComprehendDocumentClassifier, TypeKMSKey, store.RelUses},
		EdgeDecl{TypeComprehendDocumentClassifier, TypeEC2Subnet, store.RelAttachedTo},
		EdgeDecl{TypeComprehendDocumentClassifier, TypeEC2SecurityGroup, store.RelAttachedTo},
	)
	registerResolver(
		resolveComprehendFlywheelRefs,
		EdgeDecl{TypeComprehendFlywheel, TypeIAMRole, store.RelUses},
		EdgeDecl{TypeComprehendFlywheel, TypeComprehendDocumentClassifier, store.RelUses},
	)
}

// resolveComprehendDocumentClassifierRefs wires each document-classifier to
// its IAM data-access role, KMS keys, and VPC subnets/SGs.
func resolveComprehendDocumentClassifierRefs(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeComprehendDocumentClassifier}, Limit: util.AllResources,
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
	subSet, err := scannedIDSet(acct, st, TypeEC2Subnet)
	if err != nil {
		return err
	}
	sgSet, err := scannedIDSet(acct, st, TypeEC2SecurityGroup)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			DataAccessRoleArn *string `json:"DataAccessRoleArn"`
			ModelKmsKeyID     *string `json:"ModelKmsKeyId"`
			VolumeKmsKeyID    *string `json:"VolumeKmsKeyId"`
			VpcConfig         *struct {
				SecurityGroupIDs []string `json:"SecurityGroupIds"`
				Subnets          []string `json:"Subnets"`
			} `json:"VpcConfig"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		region := sv(r.Region)
		if role := sv(attrs.DataAccessRoleArn); role != "" {
			tgtID := store.ResourceID("aws", acct.ID, TypeIAMRole, role)
			if roleSet[tgtID] {
				if err := st.UpsertRelationship(r.ID, tgtID, store.RelUses, "directed", nil); err != nil {
					return fmt.Errorf("upsert comprehend dc→role: %w", err)
				}
			}
		}
		for _, k := range []*string{attrs.ModelKmsKeyID, attrs.VolumeKmsKeyID} {
			ref := sv(k)
			if ref == "" {
				continue
			}
			if keyID, ok := kmsIdx.resolveKMSKeyID(ref, region, acct.ID); ok {
				if err := st.UpsertRelationship(r.ID, keyID, store.RelUses, "directed", nil); err != nil {
					return fmt.Errorf("upsert comprehend dc→kms: %w", err)
				}
			}
		}
		if attrs.VpcConfig != nil {
			for _, sid := range attrs.VpcConfig.Subnets {
				if sid == "" {
					continue
				}
				tgtID := store.ResourceID("aws", acct.ID, TypeEC2Subnet, ec2ARN(region, acct.ID, "subnet", sid))
				if !subSet[tgtID] {
					continue
				}
				if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
					return fmt.Errorf("upsert comprehend dc→subnet: %w", err)
				}
			}
			for _, gid := range attrs.VpcConfig.SecurityGroupIDs {
				if gid == "" {
					continue
				}
				tgtID := store.ResourceID("aws", acct.ID, TypeEC2SecurityGroup, ec2ARN(region, acct.ID, "security-group", gid))
				if !sgSet[tgtID] {
					continue
				}
				if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
					return fmt.Errorf("upsert comprehend dc→sg: %w", err)
				}
			}
		}
	}
	return nil
}

// resolveComprehendFlywheelRefs wires each flywheel to its data-access role
// and the document-classifier exposed via ActiveModelArn.
func resolveComprehendFlywheelRefs(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeComprehendFlywheel}, Limit: util.AllResources,
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
	dcSet, err := scannedIDSet(acct, st, TypeComprehendDocumentClassifier)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			DataAccessRoleArn *string `json:"DataAccessRoleArn"`
			ActiveModelArn    *string `json:"ActiveModelArn"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if role := sv(attrs.DataAccessRoleArn); role != "" {
			tgtID := store.ResourceID("aws", acct.ID, TypeIAMRole, role)
			if roleSet[tgtID] {
				if err := st.UpsertRelationship(r.ID, tgtID, store.RelUses, "directed", nil); err != nil {
					return fmt.Errorf("upsert comprehend fw→role: %w", err)
				}
			}
		}
		if m := sv(attrs.ActiveModelArn); m != "" {
			tgtID := store.ResourceID("aws", acct.ID, TypeComprehendDocumentClassifier, m)
			if dcSet[tgtID] {
				if err := st.UpsertRelationship(r.ID, tgtID, store.RelUses, "directed", nil); err != nil {
					return fmt.Errorf("upsert comprehend fw→dc: %w", err)
				}
			}
		}
	}
	return nil
}

package aws

import (
	"encoding/json"
	"fmt"

	"github.com/icearp/disco-cli/internal/util"
	"github.com/icearp/disco-cli/store"
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
	registerResolver(
		resolveComprehendEntityRecognizerRefs,
		EdgeDecl{TypeComprehendEntityRecognizer, TypeIAMRole, store.RelUses},
		EdgeDecl{TypeComprehendEntityRecognizer, TypeKMSKey, store.RelUses},
		EdgeDecl{TypeComprehendEntityRecognizer, TypeEC2Subnet, store.RelAttachedTo},
		EdgeDecl{TypeComprehendEntityRecognizer, TypeEC2SecurityGroup, store.RelAttachedTo},
		EdgeDecl{TypeComprehendEntityRecognizer, TypeComprehendFlywheel, store.RelAttachedTo},
	)
	registerResolver(
		resolveComprehendEndpointRefs,
		EdgeDecl{TypeComprehendDocumentClassifierEndpoint, TypeComprehendDocumentClassifier, store.RelUses},
		EdgeDecl{TypeComprehendDocumentClassifierEndpoint, TypeIAMRole, store.RelUses},
		EdgeDecl{TypeComprehendEntityRecognizerEndpoint, TypeComprehendEntityRecognizer, store.RelUses},
		EdgeDecl{TypeComprehendEntityRecognizerEndpoint, TypeIAMRole, store.RelUses},
	)
}

// resolveComprehendEntityRecognizerRefs wires each entity-recognizer to its IAM
// data-access role, KMS keys, VPC subnets/SGs and the flywheel that manages it.
func resolveComprehendEntityRecognizerRefs(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeComprehendEntityRecognizer}, Limit: util.AllResources,
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
	fwSet, err := scannedIDSet(acct, st, TypeComprehendFlywheel)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			DataAccessRoleArn *string `json:"DataAccessRoleArn"`
			ModelKmsKeyID     *string `json:"ModelKmsKeyId"`
			VolumeKmsKeyID    *string `json:"VolumeKmsKeyId"`
			FlywheelArn       *string `json:"FlywheelArn"`
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
			tgtID := store.ResourceID("aws", acct.ID, role)
			if roleSet[tgtID] {
				if err := st.UpsertRelationship(r.ID, tgtID, store.RelUses, "directed", nil); err != nil {
					return fmt.Errorf("upsert comprehend er→role: %w", err)
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
					return fmt.Errorf("upsert comprehend er→kms: %w", err)
				}
			}
		}
		if attrs.VpcConfig != nil {
			for _, sid := range attrs.VpcConfig.Subnets {
				if err := comprehendEC2Edge(st, acct.ID, r.ID, subSet, region, "subnet", sid, TypeEC2Subnet); err != nil {
					return err
				}
			}
			for _, gid := range attrs.VpcConfig.SecurityGroupIDs {
				if err := comprehendEC2Edge(st, acct.ID, r.ID, sgSet, region, "security-group", gid, TypeEC2SecurityGroup); err != nil {
					return err
				}
			}
		}
		if fw := sv(attrs.FlywheelArn); fw != "" {
			tgtID := store.ResourceID("aws", acct.ID, fw)
			if fwSet[tgtID] {
				if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
					return fmt.Errorf("upsert comprehend er→flywheel: %w", err)
				}
			}
		}
	}
	return nil
}

// comprehendEC2Edge emits one FK-safe attached-to edge to a VPC subnet/SG.
func comprehendEC2Edge(st *store.Store, acctID, srcID string, tgtSet map[string]bool, region, kind, rawID, tgtType string) error {
	if rawID == "" {
		return nil
	}
	tgtID := store.ResourceID("aws", acctID, ec2ARN(region, acctID, kind, rawID))
	if !tgtSet[tgtID] {
		return nil
	}
	if err := st.UpsertRelationship(srcID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
		return fmt.Errorf("upsert comprehend er→%s: %w", kind, err)
	}
	return nil
}

// resolveComprehendEndpointRefs wires each inference endpoint to the model it
// fronts (ModelArn → document-classifier or entity-recognizer) and its IAM
// data-access role.
func resolveComprehendEndpointRefs(acct *account, st *store.Store) error {
	dcSet, err := scannedIDSet(acct, st, TypeComprehendDocumentClassifier)
	if err != nil {
		return err
	}
	erSet, err := scannedIDSet(acct, st, TypeComprehendEntityRecognizer)
	if err != nil {
		return err
	}
	roleSet, err := scannedIDSet(acct, st, TypeIAMRole)
	if err != nil {
		return err
	}
	for _, pair := range []struct {
		epType, modelType string
		modelSet          map[string]bool
	}{
		{TypeComprehendDocumentClassifierEndpoint, TypeComprehendDocumentClassifier, dcSet},
		{TypeComprehendEntityRecognizerEndpoint, TypeComprehendEntityRecognizer, erSet},
	} {
		rows, err := st.ListResources(store.ResourceFilter{
			Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{pair.epType}, Limit: util.AllResources,
		})
		if err != nil {
			return err
		}
		for _, r := range rows {
			var attrs struct {
				ModelArn          *string `json:"ModelArn"`
				DataAccessRoleArn *string `json:"DataAccessRoleArn"`
			}
			if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
				continue
			}
			if m := sv(attrs.ModelArn); m != "" {
				tgtID := store.ResourceID("aws", acct.ID, m)
				if pair.modelSet[tgtID] {
					if err := st.UpsertRelationship(r.ID, tgtID, store.RelUses, "directed", nil); err != nil {
						return fmt.Errorf("upsert comprehend ep→model: %w", err)
					}
				}
			}
			if role := sv(attrs.DataAccessRoleArn); role != "" {
				tgtID := store.ResourceID("aws", acct.ID, role)
				if roleSet[tgtID] {
					if err := st.UpsertRelationship(r.ID, tgtID, store.RelUses, "directed", nil); err != nil {
						return fmt.Errorf("upsert comprehend ep→role: %w", err)
					}
				}
			}
		}
	}
	return nil
}

// resolveComprehendDocumentClassifierRefs wires each document-classifier to
// its IAM data-access role, KMS keys, and VPC subnets/SGs.
func resolveComprehendDocumentClassifierRefs(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeComprehendDocumentClassifier}, Limit: util.AllResources,
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
			tgtID := store.ResourceID("aws", acct.ID, role)
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
				tgtID := store.ResourceID("aws", acct.ID, ec2ARN(region, acct.ID, "subnet", sid))
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
				tgtID := store.ResourceID("aws", acct.ID, ec2ARN(region, acct.ID, "security-group", gid))
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
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeComprehendFlywheel}, Limit: util.AllResources,
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
			tgtID := store.ResourceID("aws", acct.ID, role)
			if roleSet[tgtID] {
				if err := st.UpsertRelationship(r.ID, tgtID, store.RelUses, "directed", nil); err != nil {
					return fmt.Errorf("upsert comprehend fw→role: %w", err)
				}
			}
		}
		if m := sv(attrs.ActiveModelArn); m != "" {
			tgtID := store.ResourceID("aws", acct.ID, m)
			if dcSet[tgtID] {
				if err := st.UpsertRelationship(r.ID, tgtID, store.RelUses, "directed", nil); err != nil {
					return fmt.Errorf("upsert comprehend fw→dc: %w", err)
				}
			}
		}
	}
	return nil
}

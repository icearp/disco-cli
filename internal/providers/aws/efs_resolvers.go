package aws

import (
	"encoding/json"
	"fmt"

	"codeberg.org/icearp/disco/internal/store"
	"codeberg.org/icearp/disco/internal/util"
)

func init() {
	registerResolver(resolveEFSRelationships)
}

// resolveEFSRelationships runs all EFS sub-resolvers.
func resolveEFSRelationships(acct *account, st *store.Store) error {
	if err := resolveEFSFileSystemRelationships(acct, st); err != nil {
		return err
	}
	if err := resolveEFSMountTargetRelationships(acct, st); err != nil {
		return err
	}
	return resolveEFSAccessPointRelationships(acct, st)
}

// resolveEFSFileSystemRelationships links each file system to its KMS key.
func resolveEFSFileSystemRelationships(acct *account, st *store.Store) error {
	fss, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeEFSFileSystem},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	for _, r := range fss {
		var attrs struct {
			KmsKeyId *string `json:"KmsKeyId"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if sv(attrs.KmsKeyId) == "" {
			continue
		}
		keyID := store.ResourceID("aws", acct.ID, TypeKMSKey, *attrs.KmsKeyId)
		if err := st.UpsertRelationship(r.ID, keyID, store.RelUses, "directed", nil); err != nil {
			return fmt.Errorf("upsert efs-fs→kms: %w", err)
		}
	}
	return nil
}

// resolveEFSMountTargetRelationships links each mount target to its file system
// (contains) and subnet (attached-to).
func resolveEFSMountTargetRelationships(acct *account, st *store.Store) error {
	mts, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeEFSMountTarget},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	for _, r := range mts {
		region := sv(r.Region)
		var attrs struct {
			FileSystemId *string `json:"FileSystemId"`
			SubnetId     *string `json:"SubnetId"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if fsID := sv(attrs.FileSystemId); fsID != "" {
			fsARN := fmt.Sprintf("arn:aws:elasticfilesystem:%s:%s:file-system/%s", region, acct.ID, fsID)
			fsResID := store.ResourceID("aws", acct.ID, TypeEFSFileSystem, fsARN)
			if err := st.UpsertRelationship(fsResID, r.ID, store.RelContains, "directed", nil); err != nil {
				return fmt.Errorf("upsert efs-fs→mt: %w", err)
			}
		}
		if snID := sv(attrs.SubnetId); snID != "" {
			subnetID := store.ResourceID("aws", acct.ID, TypeEC2Subnet, ec2ARN(region, acct.ID, "subnet", snID))
			if err := st.UpsertRelationship(r.ID, subnetID, store.RelAttachedTo, "directed", nil); err != nil {
				return fmt.Errorf("upsert efs-mt→subnet: %w", err)
			}
		}
	}
	return nil
}

// resolveEFSAccessPointRelationships links each access point to its file system
// (contains). File system ID is extracted from the access point ARN.
func resolveEFSAccessPointRelationships(acct *account, st *store.Store) error {
	aps, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeEFSAccessPoint},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	for _, r := range aps {
		region := sv(r.Region)
		var attrs struct {
			FileSystemId *string `json:"FileSystemId"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		fsID := sv(attrs.FileSystemId)
		if fsID == "" {
			// FileSystemId is not embedded in access point ARN; attrs required.
			continue
		}
		fsARN := fmt.Sprintf("arn:aws:elasticfilesystem:%s:%s:file-system/%s", region, acct.ID, fsID)
		fsResID := store.ResourceID("aws", acct.ID, TypeEFSFileSystem, fsARN)
		if err := st.UpsertRelationship(fsResID, r.ID, store.RelContains, "directed", nil); err != nil {
			return fmt.Errorf("upsert efs-fs→ap: %w", err)
		}
	}
	return nil
}

package aws

import (
	"encoding/json"
	"fmt"

	"codeberg.org/icearp/disco/internal/util"
	"codeberg.org/icearp/disco/store"
)

func init() {
	registerResolver(
		resolveFSxBackupRelationships,
		EdgeDecl{TypeFSxBackup, TypeFSxFileSystem, store.RelAttachedTo},
		EdgeDecl{TypeFSxBackup, TypeFSxVolume, store.RelAttachedTo},
		EdgeDecl{TypeFSxBackup, TypeKMSKey, store.RelUses},
	)
	registerResolver(
		resolveFSxFileCacheRelationships,
		EdgeDecl{TypeFSxFileCache, TypeKMSKey, store.RelUses},
	)
}

// resolveFSxBackupRelationships wires each backup to its source file system /
// volume (the embedded SDK structs carry their own ResourceARN) and KMS key.
func resolveFSxBackupRelationships(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeFSxBackup}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	fsSet, err := scannedIDSet(acct, st, TypeFSxFileSystem)
	if err != nil {
		return err
	}
	volSet, err := scannedIDSet(acct, st, TypeFSxVolume)
	if err != nil {
		return err
	}
	kms, err := loadKMSResolveIndex(acct, st)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			KmsKeyID   *string `json:"KmsKeyId"`
			FileSystem *struct {
				ResourceARN *string `json:"ResourceARN"`
			} `json:"FileSystem"`
			Volume *struct {
				ResourceARN *string `json:"ResourceARN"`
			} `json:"Volume"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if attrs.FileSystem != nil {
			if arn := sv(attrs.FileSystem.ResourceARN); arn != "" {
				tgt := store.ResourceID("aws", acct.ID, TypeFSxFileSystem, arn)
				if fsSet[tgt] {
					if err := st.UpsertRelationship(r.ID, tgt, store.RelAttachedTo, "directed", nil); err != nil {
						return fmt.Errorf("upsert fsx backup→file-system: %w", err)
					}
				}
			}
		}
		if attrs.Volume != nil {
			if arn := sv(attrs.Volume.ResourceARN); arn != "" {
				tgt := store.ResourceID("aws", acct.ID, TypeFSxVolume, arn)
				if volSet[tgt] {
					if err := st.UpsertRelationship(r.ID, tgt, store.RelAttachedTo, "directed", nil); err != nil {
						return fmt.Errorf("upsert fsx backup→volume: %w", err)
					}
				}
			}
		}
		if ref := sv(attrs.KmsKeyID); ref != "" {
			if keyID, ok := kms.resolveKMSKeyID(ref, sv(r.Region), acct.ID); ok {
				if err := st.UpsertRelationship(r.ID, keyID, store.RelUses, "directed", nil); err != nil {
					return fmt.Errorf("upsert fsx backup→kms: %w", err)
				}
			}
		}
	}
	return nil
}

// resolveFSxFileCacheRelationships wires each file cache to its KMS key.
func resolveFSxFileCacheRelationships(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeFSxFileCache}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	kms, err := loadKMSResolveIndex(acct, st)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			KmsKeyID *string `json:"KmsKeyId"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if ref := sv(attrs.KmsKeyID); ref != "" {
			if keyID, ok := kms.resolveKMSKeyID(ref, sv(r.Region), acct.ID); ok {
				if err := st.UpsertRelationship(r.ID, keyID, store.RelUses, "directed", nil); err != nil {
					return fmt.Errorf("upsert fsx file-cache→kms: %w", err)
				}
			}
		}
	}
	return nil
}

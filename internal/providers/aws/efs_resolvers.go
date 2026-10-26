package aws

import (
	"cmp"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/icearp/disco-cli/internal/util"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerResolver(
		resolveEFSRelationships,
		EdgeDecl{TypeEFSFileSystem, TypeKMSKey, store.RelUses},
		EdgeDecl{TypeEFSFileSystem, TypeEFSMountTarget, store.RelContains},
		EdgeDecl{TypeEFSMountTarget, TypeEC2Subnet, store.RelAttachedTo},
		EdgeDecl{TypeEFSFileSystem, TypeEFSAccessPoint, store.RelContains},
		// The scanner records file system -> replication configuration as a
		// hierarchy pair; the resolver adds only the destination edge.
		EdgeDecl{TypeEFSFileSystem, TypeEFSReplicationConfiguration, store.RelContains},
		EdgeDecl{TypeEFSReplicationConfiguration, TypeEFSFileSystem, store.RelUses},
	)
}

// resolveEFSRelationships runs all EFS sub-resolvers.
func resolveEFSRelationships(acct *account, st *store.Store) error {
	if err := resolveEFSFileSystemRelationships(acct, st); err != nil {
		return err
	}
	if err := resolveEFSMountTargetRelationships(acct, st); err != nil {
		return err
	}
	if err := resolveEFSAccessPointRelationships(acct, st); err != nil {
		return err
	}
	return resolveEFSReplicationDestinations(acct, st)
}

// resolveEFSReplicationDestinations links each replication configuration to
// its destination file system (uses) when that file system was scanned in
// this account. A cross-account destination is never in the set, so it gets
// no edge.
func resolveEFSReplicationDestinations(acct *account, st *store.Store) error {
	configs, err := listAccountRows(acct, st, TypeEFSReplicationConfiguration)
	if err != nil {
		return err
	}
	if len(configs) == 0 {
		return nil
	}
	fileSystems, err := scannedIDSet(acct, st, TypeEFSFileSystem)
	if err != nil {
		return err
	}
	var decodeErrs []error
	for _, r := range configs {
		var attrs struct {
			Destinations []struct {
				FileSystemID *string `json:"FileSystemId"`
				Region       *string `json:"Region"`
				OwnerID      *string `json:"OwnerId"`
			} `json:"Destinations"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			decodeErrs = append(decodeErrs, fmt.Errorf("decode efs replication configuration %s: %w", r.NativeID, err))
			continue
		}
		for _, d := range attrs.Destinations {
			fsID, region := sv(d.FileSystemID), sv(d.Region)
			if fsID == "" || region == "" {
				continue
			}
			// OwnerId is model-optional. An absent owner is read as this
			// account, on the unverified assumption that AWS omits it only for
			// same-account replication.
			owner := cmp.Or(sv(d.OwnerID), acct.ID)
			target := store.ResourceID("aws", owner, efsFileSystemARN(region, owner, fsID))
			if !fileSystems[target] {
				continue
			}
			if err := st.UpsertRelationship(r.ID, target, store.RelUses, "directed", nil); err != nil {
				return fmt.Errorf("upsert efs-replication→fs: %w", err)
			}
		}
	}
	return errors.Join(decodeErrs...)
}

// resolveEFSFileSystemRelationships links each file system to its KMS key.
func resolveEFSFileSystemRelationships(acct *account, st *store.Store) error {
	fss, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeEFSFileSystem},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	for _, r := range fss {
		var attrs struct {
			KmsKeyID *string `json:"KmsKeyID"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if sv(attrs.KmsKeyID) == "" {
			continue
		}
		keyID := store.ResourceID("aws", acct.ID, *attrs.KmsKeyID)
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
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeEFSMountTarget},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	for _, r := range mts {
		region := sv(r.Region)
		var attrs struct {
			FileSystemID *string `json:"FileSystemID"`
			SubnetID     *string `json:"SubnetID"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if fsID := sv(attrs.FileSystemID); fsID != "" {
			fsARN := efsFileSystemARN(region, acct.ID, fsID)
			fsResID := store.ResourceID("aws", acct.ID, fsARN)
			if err := st.UpsertRelationship(fsResID, r.ID, store.RelContains, "directed", nil); err != nil {
				return fmt.Errorf("upsert efs-fs→mt: %w", err)
			}
		}
		if snID := sv(attrs.SubnetID); snID != "" {
			subnetID := store.ResourceID("aws", acct.ID, ec2ARNIn(acct.partition(), region, acct.ID, "subnet", snID))
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
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeEFSAccessPoint},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	for _, r := range aps {
		region := sv(r.Region)
		var attrs struct {
			FileSystemID *string `json:"FileSystemID"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		fsID := sv(attrs.FileSystemID)
		if fsID == "" {
			// FileSystemID is not embedded in access point ARN; attrs required.
			continue
		}
		fsARN := efsFileSystemARN(region, acct.ID, fsID)
		fsResID := store.ResourceID("aws", acct.ID, fsARN)
		if err := st.UpsertRelationship(fsResID, r.ID, store.RelContains, "directed", nil); err != nil {
			return fmt.Errorf("upsert efs-fs→ap: %w", err)
		}
	}
	return nil
}

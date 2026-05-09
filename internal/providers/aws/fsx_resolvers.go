package aws

import (
	"encoding/json"
	"fmt"

	"codeberg.org/icearp/disco/store"
	"codeberg.org/icearp/disco/internal/util"
)

func init() {
	registerResolver(
		resolveFSxChildrenToFileSystem,
		EdgeDecl{TypeFSxVolume, TypeFSxFileSystem, store.RelAttachedTo},
		EdgeDecl{TypeFSxStorageVirtualMachine, TypeFSxFileSystem, store.RelAttachedTo},
		EdgeDecl{TypeFSxDataRepositoryAssociation, TypeFSxFileSystem, store.RelAttachedTo},
	)
	registerResolver(
		resolveFSxSnapshotToVolume,
		EdgeDecl{TypeFSxSnapshot, TypeFSxVolume, store.RelAttachedTo},
	)
	registerResolver(
		resolveFSxFileSystemRefs,
		EdgeDecl{TypeFSxFileSystem, TypeKMSKey, store.RelUses},
		EdgeDecl{TypeFSxFileSystem, TypeEC2VPC, store.RelAttachedTo},
		EdgeDecl{TypeFSxFileSystem, TypeEC2Subnet, store.RelAttachedTo},
		EdgeDecl{TypeFSxFileSystem, TypeEC2NetworkInterface, store.RelAttachedTo},
	)
}

// resolveFSxFileSystemRefs wires each file-system to its CMEK, VPC, subnets
// and ENIs. DescribeFileSystems already returns the full body so no
// scanner enrichment is needed.
func resolveFSxFileSystemRefs(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeFSxFileSystem}, Limit: util.AllResources,
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
	vpcSet, err := scannedIDSet(acct, st, TypeEC2VPC)
	if err != nil {
		return err
	}
	subnetSet, err := scannedIDSet(acct, st, TypeEC2Subnet)
	if err != nil {
		return err
	}
	eniSet, err := scannedIDSet(acct, st, TypeEC2NetworkInterface)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			KmsKeyID            *string  `json:"KmsKeyId"`
			VpcID               *string  `json:"VpcId"`
			SubnetIDs           []string `json:"SubnetIds"`
			NetworkInterfaceIDs []string `json:"NetworkInterfaceIds"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		region := sv(r.Region)
		if ref := sv(attrs.KmsKeyID); ref != "" {
			if keyID, ok := idx.resolveKMSKeyID(ref, region, acct.ID); ok {
				if err := st.UpsertRelationship(r.ID, keyID, store.RelUses, "directed", nil); err != nil {
					return fmt.Errorf("upsert fsx-fs→kms: %w", err)
				}
			}
		}
		if v := sv(attrs.VpcID); v != "" {
			tgt := store.ResourceID("aws", acct.ID, TypeEC2VPC, ec2ARN(region, acct.ID, "vpc", v))
			if vpcSet[tgt] {
				if err := st.UpsertRelationship(r.ID, tgt, store.RelAttachedTo, "directed", nil); err != nil {
					return fmt.Errorf("upsert fsx-fs→vpc: %w", err)
				}
			}
		}
		for _, sn := range attrs.SubnetIDs {
			tgt := store.ResourceID("aws", acct.ID, TypeEC2Subnet, ec2ARN(region, acct.ID, "subnet", sn))
			if !subnetSet[tgt] {
				continue
			}
			if err := st.UpsertRelationship(r.ID, tgt, store.RelAttachedTo, "directed", nil); err != nil {
				return fmt.Errorf("upsert fsx-fs→subnet: %w", err)
			}
		}
		for _, eni := range attrs.NetworkInterfaceIDs {
			tgt := store.ResourceID("aws", acct.ID, TypeEC2NetworkInterface, ec2ARN(region, acct.ID, "network-interface", eni))
			if !eniSet[tgt] {
				continue
			}
			if err := st.UpsertRelationship(r.ID, tgt, store.RelAttachedTo, "directed", nil); err != nil {
				return fmt.Errorf("upsert fsx-fs→eni: %w", err)
			}
		}
	}
	return nil
}

func fsxARN(region, acct, kind, id string) string {
	return fmt.Sprintf("arn:aws:fsx:%s:%s:%s/%s", region, acct, kind, id)
}

// resolveFSxChildrenToFileSystem wires per-FS children — volumes, storage
// virtual machines and data-repository-associations — to their parent
// file-system via FileSystemID.
func resolveFSxChildrenToFileSystem(acct *account, st *store.Store) error {
	fsSet, err := scannedIDSet(acct, st, TypeFSxFileSystem)
	if err != nil {
		return err
	}
	for _, t := range []string{TypeFSxVolume, TypeFSxStorageVirtualMachine, TypeFSxDataRepositoryAssociation} {
		rows, err := st.ListResources(store.ResourceFilter{
			Provider: "aws", AccountID: acct.ID, Types: []string{t}, Limit: util.AllResources,
		})
		if err != nil {
			return err
		}
		for _, r := range rows {
			var attrs struct {
				FileSystemID *string `json:"FileSystemId"`
			}
			if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
				continue
			}
			fsID := sv(attrs.FileSystemID)
			if fsID == "" {
				continue
			}
			tgtID := store.ResourceID("aws", acct.ID, TypeFSxFileSystem, fsxARN(sv(r.Region), acct.ID, "file-system", fsID))
			if !fsSet[tgtID] {
				continue
			}
			if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
				return fmt.Errorf("upsert fsx %s→fs: %w", t, err)
			}
		}
	}
	return nil
}

// resolveFSxSnapshotToVolume wires each snapshot to the volume it was taken
// from via VolumeID.
func resolveFSxSnapshotToVolume(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeFSxSnapshot}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	volSet, err := scannedIDSet(acct, st, TypeFSxVolume)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			VolumeID *string `json:"VolumeId"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		vid := sv(attrs.VolumeID)
		if vid == "" {
			continue
		}
		tgtID := store.ResourceID("aws", acct.ID, TypeFSxVolume, fsxARN(sv(r.Region), acct.ID, "volume", vid))
		if !volSet[tgtID] {
			continue
		}
		if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
			return fmt.Errorf("upsert fsx snapshot→volume: %w", err)
		}
	}
	return nil
}

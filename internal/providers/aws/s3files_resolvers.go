package aws

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/icearp/disco-cli/internal/util"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerResolver(
		resolveS3FilesFileSystemRefs,
		EdgeDecl{TypeS3FilesFileSystem, TypeS3Bucket, store.RelUses},
		EdgeDecl{TypeS3FilesFileSystem, TypeIAMRole, store.RelAssumes},
	)
	registerResolver(
		resolveS3FilesAccessPointRefs,
		EdgeDecl{TypeS3FilesAccessPoint, TypeS3FilesFileSystem, store.RelAttachedTo},
	)
	registerResolver(
		resolveS3FilesMountTargetRefs,
		EdgeDecl{TypeS3FilesMountTarget, TypeS3FilesFileSystem, store.RelAttachedTo},
		EdgeDecl{TypeS3FilesMountTarget, TypeEC2VPC, store.RelAttachedTo},
		EdgeDecl{TypeS3FilesMountTarget, TypeEC2Subnet, store.RelAttachedTo},
		EdgeDecl{TypeS3FilesMountTarget, TypeEC2NetworkInterface, store.RelAttachedTo},
	)
	registerResolver(
		resolveS3FilesPolicyParent,
		EdgeDecl{TypeS3FilesFileSystemPolicy, TypeS3FilesFileSystem, store.RelAttachedTo},
	)
}

func s3filesFSARN(region, acct, fsID string) string {
	return fmt.Sprintf("arn:aws:s3files:%s:%s:file-system/%s", region, acct, fsID)
}

func resolveS3FilesFileSystemRefs(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeS3FilesFileSystem}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	bucketSet, err := scannedIDSet(acct, st, TypeS3Bucket)
	if err != nil {
		return err
	}
	roleSet, err := scannedIDSet(acct, st, TypeIAMRole)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			Bucket  *string `json:"Bucket"`
			RoleArn *string `json:"RoleArn"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if b := sv(attrs.Bucket); b != "" {
			bArn := "arn:aws:s3:::" + b
			tgt := store.ResourceID("aws", acct.ID, bArn)
			if bucketSet[tgt] {
				if err := st.UpsertRelationship(r.ID, tgt, store.RelUses, "directed", nil); err != nil {
					return fmt.Errorf("upsert s3files file-system→bucket: %w", err)
				}
			}
		}
		if ra := sv(attrs.RoleArn); ra != "" {
			tgt := store.ResourceID("aws", acct.ID, ra)
			if roleSet[tgt] {
				if err := st.UpsertRelationship(r.ID, tgt, store.RelAssumes, "directed", nil); err != nil {
					return fmt.Errorf("upsert s3files file-system→role: %w", err)
				}
			}
		}
	}
	return nil
}

func resolveS3FilesAccessPointRefs(acct *account, st *store.Store) error {
	return s3filesChildToFS(acct, st, TypeS3FilesAccessPoint)
}

// s3filesChildToFS wires an s3files child (access-point, mount-target) to its
// parent file-system via FileSystemId. FK-safe.
func s3filesChildToFS(acct *account, st *store.Store, ttyp string) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{ttyp}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	fsSet, err := scannedIDSet(acct, st, TypeS3FilesFileSystem)
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
		fid := sv(attrs.FileSystemID)
		if fid == "" {
			continue
		}
		fsARN := s3filesFSARN(sv(r.Region), acct.ID, fid)
		tgt := store.ResourceID("aws", acct.ID, fsARN)
		if !fsSet[tgt] {
			continue
		}
		if err := st.UpsertRelationship(r.ID, tgt, store.RelAttachedTo, "directed", nil); err != nil {
			return fmt.Errorf("upsert s3files %s→file-system: %w", ttyp, err)
		}
	}
	return nil
}

func resolveS3FilesMountTargetRefs(acct *account, st *store.Store) error {
	if err := s3filesChildToFS(acct, st, TypeS3FilesMountTarget); err != nil {
		return err
	}
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeS3FilesMountTarget}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
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
			VpcID              *string `json:"VpcId"`
			SubnetID           *string `json:"SubnetId"`
			NetworkInterfaceID *string `json:"NetworkInterfaceId"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		region := sv(r.Region)
		if v := sv(attrs.VpcID); v != "" {
			tgt := store.ResourceID("aws", acct.ID, ec2ARN(region, acct.ID, "vpc", v))
			if vpcSet[tgt] {
				if err := st.UpsertRelationship(r.ID, tgt, store.RelAttachedTo, "directed", nil); err != nil {
					return fmt.Errorf("upsert s3files mount-target→vpc: %w", err)
				}
			}
		}
		if s := sv(attrs.SubnetID); s != "" {
			tgt := store.ResourceID("aws", acct.ID, ec2ARN(region, acct.ID, "subnet", s))
			if subnetSet[tgt] {
				if err := st.UpsertRelationship(r.ID, tgt, store.RelAttachedTo, "directed", nil); err != nil {
					return fmt.Errorf("upsert s3files mount-target→subnet: %w", err)
				}
			}
		}
		if n := sv(attrs.NetworkInterfaceID); n != "" {
			tgt := store.ResourceID("aws", acct.ID, ec2ARN(region, acct.ID, "network-interface", n))
			if eniSet[tgt] {
				if err := st.UpsertRelationship(r.ID, tgt, store.RelAttachedTo, "directed", nil); err != nil {
					return fmt.Errorf("upsert s3files mount-target→eni: %w", err)
				}
			}
		}
	}
	return nil
}

// resolveS3FilesPolicyParent wires file-system-policy to its parent file-
// system via NativeID `<fsARN>/policy` suffix trim.
func resolveS3FilesPolicyParent(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeS3FilesFileSystemPolicy}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	fsSet, err := scannedIDSet(acct, st, TypeS3FilesFileSystem)
	if err != nil {
		return err
	}
	for _, r := range rows {
		parent := strings.TrimSuffix(r.NativeID, "/policy")
		if parent == r.NativeID {
			continue
		}
		tgt := store.ResourceID("aws", acct.ID, parent)
		if !fsSet[tgt] {
			continue
		}
		if err := st.UpsertRelationship(r.ID, tgt, store.RelAttachedTo, "directed", nil); err != nil {
			return fmt.Errorf("upsert s3files policy→file-system: %w", err)
		}
	}
	return nil
}

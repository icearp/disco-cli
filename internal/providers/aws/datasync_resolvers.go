package aws

import (
	"encoding/json"
	"fmt"

	"codeberg.org/icearp/disco/internal/util"
	"codeberg.org/icearp/disco/store"
)

func init() {
	registerResolver(
		resolveDataSyncLocationS3,
		EdgeDecl{TypeDataSyncLocationS3, TypeS3Bucket, store.RelUses},
		EdgeDecl{TypeDataSyncLocationS3, TypeIAMRole, store.RelUses},
		EdgeDecl{TypeDataSyncLocationS3, TypeDataSyncAgent, store.RelUses},
	)
	registerResolver(
		resolveDataSyncLocationEFS,
		EdgeDecl{TypeDataSyncLocationEFS, TypeEFSFileSystem, store.RelAttachedTo},
		EdgeDecl{TypeDataSyncLocationEFS, TypeEFSAccessPoint, store.RelAttachedTo},
		EdgeDecl{TypeDataSyncLocationEFS, TypeIAMRole, store.RelUses},
		EdgeDecl{TypeDataSyncLocationEFS, TypeEC2Subnet, store.RelAttachedTo},
		EdgeDecl{TypeDataSyncLocationEFS, TypeEC2SecurityGroup, store.RelUses},
	)
	registerResolver(
		resolveDataSyncLocationFSxOntap,
		EdgeDecl{TypeDataSyncLocationFSxONTAP, TypeFSxFileSystem, store.RelAttachedTo},
		EdgeDecl{TypeDataSyncLocationFSxONTAP, TypeFSxStorageVirtualMachine, store.RelAttachedTo},
		EdgeDecl{TypeDataSyncLocationFSxONTAP, TypeEC2SecurityGroup, store.RelUses},
	)
	registerResolver(
		resolveDataSyncFSxSGs,
		EdgeDecl{TypeDataSyncLocationFSxLustre, TypeEC2SecurityGroup, store.RelUses},
		EdgeDecl{TypeDataSyncLocationFSxOpenZFS, TypeEC2SecurityGroup, store.RelUses},
		EdgeDecl{TypeDataSyncLocationFSxWindows, TypeEC2SecurityGroup, store.RelUses},
	)
	registerResolver(
		resolveDataSyncOnPremAgents,
		EdgeDecl{TypeDataSyncLocationNFS, TypeDataSyncAgent, store.RelUses},
		EdgeDecl{TypeDataSyncLocationSMB, TypeDataSyncAgent, store.RelUses},
		EdgeDecl{TypeDataSyncLocationHDFS, TypeDataSyncAgent, store.RelUses},
		EdgeDecl{TypeDataSyncLocationAzureBlob, TypeDataSyncAgent, store.RelUses},
		EdgeDecl{TypeDataSyncLocationObjectStorage, TypeDataSyncAgent, store.RelUses},
	)
}

// dsEmitAgents wires location → datasync agent for each ARN in agentARNs.
func dsEmitAgents(srcID string, agentARNs []string, set map[string]bool, st *store.Store, acctID string) error {
	for _, a := range agentARNs {
		if a == "" {
			continue
		}
		tgtID := store.ResourceID("aws", acctID, TypeDataSyncAgent, a)
		if !set[tgtID] {
			continue
		}
		if err := st.UpsertRelationship(srcID, tgtID, store.RelUses, "directed", nil); err != nil {
			return fmt.Errorf("upsert ds loc→agent: %w", err)
		}
	}
	return nil
}

// resolveDataSyncLocationS3 wires location-s3 → S3 bucket, IAM role, agents.
func resolveDataSyncLocationS3(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeDataSyncLocationS3}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	s3Set, err := scannedIDSet(acct, st, TypeS3Bucket)
	if err != nil {
		return err
	}
	roleSet, err := scannedIDSet(acct, st, TypeIAMRole)
	if err != nil {
		return err
	}
	agSet, err := scannedIDSet(acct, st, TypeDataSyncAgent)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			S3BucketArn *string `json:"S3BucketArn"`
			S3Config    *struct {
				BucketAccessRoleArn *string `json:"BucketAccessRoleArn"`
			} `json:"S3Config"`
			AgentArns []string `json:"AgentArns"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if b := sv(attrs.S3BucketArn); b != "" {
			tgtID := store.ResourceID("aws", acct.ID, TypeS3Bucket, b)
			if s3Set[tgtID] {
				if err := st.UpsertRelationship(r.ID, tgtID, store.RelUses, "directed", nil); err != nil {
					return fmt.Errorf("upsert ds s3→bucket: %w", err)
				}
			}
		}
		if attrs.S3Config != nil {
			if role := sv(attrs.S3Config.BucketAccessRoleArn); role != "" {
				tgtID := store.ResourceID("aws", acct.ID, TypeIAMRole, role)
				if roleSet[tgtID] {
					if err := st.UpsertRelationship(r.ID, tgtID, store.RelUses, "directed", nil); err != nil {
						return fmt.Errorf("upsert ds s3→role: %w", err)
					}
				}
			}
		}
		if err := dsEmitAgents(r.ID, attrs.AgentArns, agSet, st, acct.ID); err != nil {
			return err
		}
	}
	return nil
}

// resolveDataSyncLocationEFS wires location-efs → EFS FS, access-point, role,
// subnet, SGs.
func resolveDataSyncLocationEFS(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeDataSyncLocationEFS}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	efsSet, err := scannedIDSet(acct, st, TypeEFSFileSystem)
	if err != nil {
		return err
	}
	apSet, err := scannedIDSet(acct, st, TypeEFSAccessPoint)
	if err != nil {
		return err
	}
	roleSet, err := scannedIDSet(acct, st, TypeIAMRole)
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
			EfsFilesystemArn        *string `json:"EfsFilesystemArn"`
			AccessPointArn          *string `json:"AccessPointArn"`
			FileSystemAccessRoleArn *string `json:"FileSystemAccessRoleArn"`
			Ec2Config               *struct {
				SubnetArn         *string  `json:"SubnetArn"`
				SecurityGroupArns []string `json:"SecurityGroupArns"`
			} `json:"Ec2Config"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if f := sv(attrs.EfsFilesystemArn); f != "" {
			tgtID := store.ResourceID("aws", acct.ID, TypeEFSFileSystem, f)
			if efsSet[tgtID] {
				if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
					return fmt.Errorf("upsert ds efs→fs: %w", err)
				}
			}
		}
		if a := sv(attrs.AccessPointArn); a != "" {
			tgtID := store.ResourceID("aws", acct.ID, TypeEFSAccessPoint, a)
			if apSet[tgtID] {
				if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
					return fmt.Errorf("upsert ds efs→ap: %w", err)
				}
			}
		}
		if role := sv(attrs.FileSystemAccessRoleArn); role != "" {
			tgtID := store.ResourceID("aws", acct.ID, TypeIAMRole, role)
			if roleSet[tgtID] {
				if err := st.UpsertRelationship(r.ID, tgtID, store.RelUses, "directed", nil); err != nil {
					return fmt.Errorf("upsert ds efs→role: %w", err)
				}
			}
		}
		if attrs.Ec2Config != nil {
			if s := sv(attrs.Ec2Config.SubnetArn); s != "" {
				tgtID := store.ResourceID("aws", acct.ID, TypeEC2Subnet, s)
				if subSet[tgtID] {
					if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
						return fmt.Errorf("upsert ds efs→subnet: %w", err)
					}
				}
			}
			for _, sg := range attrs.Ec2Config.SecurityGroupArns {
				if sg == "" {
					continue
				}
				tgtID := store.ResourceID("aws", acct.ID, TypeEC2SecurityGroup, sg)
				if sgSet[tgtID] {
					if err := st.UpsertRelationship(r.ID, tgtID, store.RelUses, "directed", nil); err != nil {
						return fmt.Errorf("upsert ds efs→sg: %w", err)
					}
				}
			}
		}
	}
	return nil
}

// resolveDataSyncLocationFSxOntap wires location-fsx-ontap → FSx FS, SVM, SGs.
func resolveDataSyncLocationFSxOntap(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeDataSyncLocationFSxONTAP}, Limit: util.AllResources,
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
	svmSet, err := scannedIDSet(acct, st, TypeFSxStorageVirtualMachine)
	if err != nil {
		return err
	}
	sgSet, err := scannedIDSet(acct, st, TypeEC2SecurityGroup)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			FsxFilesystemArn         *string  `json:"FsxFilesystemArn"`
			StorageVirtualMachineArn *string  `json:"StorageVirtualMachineArn"`
			SecurityGroupArns        []string `json:"SecurityGroupArns"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if f := sv(attrs.FsxFilesystemArn); f != "" {
			tgtID := store.ResourceID("aws", acct.ID, TypeFSxFileSystem, f)
			if fsSet[tgtID] {
				if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
					return fmt.Errorf("upsert ds fsx-ontap→fs: %w", err)
				}
			}
		}
		if s := sv(attrs.StorageVirtualMachineArn); s != "" {
			tgtID := store.ResourceID("aws", acct.ID, TypeFSxStorageVirtualMachine, s)
			if svmSet[tgtID] {
				if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
					return fmt.Errorf("upsert ds fsx-ontap→svm: %w", err)
				}
			}
		}
		for _, sg := range attrs.SecurityGroupArns {
			if sg == "" {
				continue
			}
			tgtID := store.ResourceID("aws", acct.ID, TypeEC2SecurityGroup, sg)
			if sgSet[tgtID] {
				if err := st.UpsertRelationship(r.ID, tgtID, store.RelUses, "directed", nil); err != nil {
					return fmt.Errorf("upsert ds fsx-ontap→sg: %w", err)
				}
			}
		}
	}
	return nil
}

// resolveDataSyncFSxSGs wires fsx-lustre/openzfs/windows location → SGs from
// SecurityGroupArns. (No FsxFilesystemArn exposed for these subtypes — only
// location-fsx-ontap carries it.)
func resolveDataSyncFSxSGs(acct *account, st *store.Store) error {
	sgSet, err := scannedIDSet(acct, st, TypeEC2SecurityGroup)
	if err != nil {
		return err
	}
	for _, ltype := range []string{
		TypeDataSyncLocationFSxLustre,
		TypeDataSyncLocationFSxOpenZFS,
		TypeDataSyncLocationFSxWindows,
	} {
		rows, err := st.ListResources(store.ResourceFilter{
			Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{ltype}, Limit: util.AllResources,
		})
		if err != nil {
			return err
		}
		for _, r := range rows {
			var attrs struct {
				SecurityGroupArns []string `json:"SecurityGroupArns"`
			}
			if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
				continue
			}
			for _, sg := range attrs.SecurityGroupArns {
				if sg == "" {
					continue
				}
				tgtID := store.ResourceID("aws", acct.ID, TypeEC2SecurityGroup, sg)
				if sgSet[tgtID] {
					if err := st.UpsertRelationship(r.ID, tgtID, store.RelUses, "directed", nil); err != nil {
						return fmt.Errorf("upsert ds %s→sg: %w", ltype, err)
					}
				}
			}
		}
	}
	return nil
}

// resolveDataSyncOnPremAgents wires nfs/smb/hdfs/azure-blob/object-storage →
// datasync agents (AgentArns) from per-subtype Describe enrichment.
func resolveDataSyncOnPremAgents(acct *account, st *store.Store) error {
	agSet, err := scannedIDSet(acct, st, TypeDataSyncAgent)
	if err != nil {
		return err
	}
	for _, ltype := range []string{
		TypeDataSyncLocationNFS,
		TypeDataSyncLocationSMB,
		TypeDataSyncLocationHDFS,
		TypeDataSyncLocationAzureBlob,
		TypeDataSyncLocationObjectStorage,
	} {
		rows, err := st.ListResources(store.ResourceFilter{
			Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{ltype}, Limit: util.AllResources,
		})
		if err != nil {
			return err
		}
		for _, r := range rows {
			var attrs struct {
				AgentArns    []string `json:"AgentArns"`
				OnPremConfig *struct {
					AgentArns []string `json:"AgentArns"`
				} `json:"OnPremConfig"`
			}
			if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
				continue
			}
			arns := attrs.AgentArns
			if attrs.OnPremConfig != nil {
				arns = append(arns, attrs.OnPremConfig.AgentArns...)
			}
			if err := dsEmitAgents(r.ID, arns, agSet, st, acct.ID); err != nil {
				return err
			}
		}
	}
	return nil
}

func init() {
	registerResolver(
		resolveDataSyncAgentRefs,
		EdgeDecl{TypeDataSyncAgent, TypeEC2VPCEndpoint, store.RelAttachedTo},
		EdgeDecl{TypeDataSyncAgent, TypeEC2Subnet, store.RelAttachedTo},
		EdgeDecl{TypeDataSyncAgent, TypeEC2SecurityGroup, store.RelAttachedTo},
	)
	registerResolver(
		resolveDataSyncTaskRefs,
		EdgeDecl{TypeDataSyncTask, TypeLogsLogGroup, store.RelUses},
	)
}

// resolveDataSyncAgentRefs wires PrivateLinkConfig: VPC endpoint id +
// subnet ARNs + SG ARNs.
func resolveDataSyncAgentRefs(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeDataSyncAgent}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	vpceSet, err := scannedIDSet(acct, st, TypeEC2VPCEndpoint)
	if err != nil {
		return err
	}
	subnetSet, err := scannedIDSet(acct, st, TypeEC2Subnet)
	if err != nil {
		return err
	}
	sgSet, err := scannedIDSet(acct, st, TypeEC2SecurityGroup)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			PrivateLinkConfig *struct {
				VpcEndpointID     *string  `json:"VpcEndpointId"`
				SubnetArns        []string `json:"SubnetArns"`
				SecurityGroupArns []string `json:"SecurityGroupArns"`
			} `json:"PrivateLinkConfig"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if attrs.PrivateLinkConfig == nil {
			continue
		}
		region := sv(r.Region)
		if vid := sv(attrs.PrivateLinkConfig.VpcEndpointID); vid != "" {
			tgt := store.ResourceID("aws", acct.ID, TypeEC2VPCEndpoint, ec2ARN(region, acct.ID, "vpc-endpoint", vid))
			if vpceSet[tgt] {
				if err := st.UpsertRelationship(r.ID, tgt, store.RelAttachedTo, "directed", nil); err != nil {
					return fmt.Errorf("upsert datasync-agent→vpce: %w", err)
				}
			}
		}
		for _, sa := range attrs.PrivateLinkConfig.SubnetArns {
			if sa == "" {
				continue
			}
			tgt := store.ResourceID("aws", acct.ID, TypeEC2Subnet, sa)
			if !subnetSet[tgt] {
				continue
			}
			if err := st.UpsertRelationship(r.ID, tgt, store.RelAttachedTo, "directed", nil); err != nil {
				return fmt.Errorf("upsert datasync-agent→subnet: %w", err)
			}
		}
		for _, sg := range attrs.PrivateLinkConfig.SecurityGroupArns {
			if sg == "" {
				continue
			}
			tgt := store.ResourceID("aws", acct.ID, TypeEC2SecurityGroup, sg)
			if !sgSet[tgt] {
				continue
			}
			if err := st.UpsertRelationship(r.ID, tgt, store.RelAttachedTo, "directed", nil); err != nil {
				return fmt.Errorf("upsert datasync-agent→sg: %w", err)
			}
		}
	}
	return nil
}

// resolveDataSyncTaskRefs wires CloudWatchLogGroupArn (strip :* suffix per
// AWS SDK convention) + source/dest location ARNs already covered by
// per-location resolvers.
func resolveDataSyncTaskRefs(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeDataSyncTask}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	lgSet, err := scannedIDSet(acct, st, TypeLogsLogGroup)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			CloudWatchLogGroupArn *string `json:"CloudWatchLogGroupArn"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		lg := sv(attrs.CloudWatchLogGroupArn)
		if lg == "" {
			continue
		}
		// Logs ARNs sometimes carry trailing :* — strip per CLAUDE.md
		// CloudWatch convention before lookup.
		const tail = ":*"
		if len(lg) > len(tail) && lg[len(lg)-len(tail):] == tail {
			lg = lg[:len(lg)-len(tail)]
		}
		tgt := store.ResourceID("aws", acct.ID, TypeLogsLogGroup, lg)
		if !lgSet[tgt] {
			continue
		}
		if err := st.UpsertRelationship(r.ID, tgt, store.RelUses, "directed", nil); err != nil {
			return fmt.Errorf("upsert datasync-task→log-group: %w", err)
		}
	}
	return nil
}

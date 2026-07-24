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
		resolveEMRChildrenToCluster,
		EdgeDecl{TypeEMRStep, TypeEMRCluster, store.RelAttachedTo},
		EdgeDecl{TypeEMRInstanceFleet, TypeEMRCluster, store.RelAttachedTo},
		EdgeDecl{TypeEMRInstanceGroup, TypeEMRCluster, store.RelAttachedTo},
	)
	registerResolver(
		resolveEMRStudioSessionMappingToStudio,
		EdgeDecl{TypeEMRStudioSessionMapping, TypeEMRStudio, store.RelAttachedTo},
	)
	registerResolver(
		resolveEMRStudioVPC,
		EdgeDecl{TypeEMRStudio, TypeEC2VPC, store.RelAttachedTo},
	)
	registerResolver(
		resolveEMRClusterRefs,
		EdgeDecl{TypeEMRCluster, TypeIAMRole, store.RelAssumes},
		EdgeDecl{TypeEMRCluster, TypeKMSKey, store.RelUses},
		EdgeDecl{TypeEMRCluster, TypeEC2Subnet, store.RelAttachedTo},
		EdgeDecl{TypeEMRCluster, TypeEC2SecurityGroup, store.RelAttachedTo},
		EdgeDecl{TypeEMRCluster, TypeEC2KeyPair, store.RelUses},
	)
}

// resolveEMRClusterRefs walks the DescribeCluster body, emitting service/
// auto-scaling role, log-encryption KMS, EC2 subnet, master/slave SG, and
// key-pair edges. ServiceRole/AutoScalingRole are bare role names; build
// `arn:aws:iam::{acct}:role/{name}` for FK-safe lookup.
func resolveEMRClusterRefs(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeEMRCluster}, Limit: util.AllResources,
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
	roleSet, err := scannedIDSet(acct, st, TypeIAMRole)
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
	kpRows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeEC2KeyPair}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	keyByNameRegion := make(map[string]string, len(kpRows))
	for _, kp := range kpRows {
		if kp.Name == nil {
			continue
		}
		keyByNameRegion[sv(kp.Region)+"\x00"+*kp.Name] = kp.ID
	}
	for _, r := range rows {
		var attrs struct {
			ServiceRole           *string `json:"ServiceRole"`
			AutoScalingRole       *string `json:"AutoScalingRole"`
			LogEncryptionKmsKeyID *string `json:"LogEncryptionKmsKeyId"`
			Ec2InstanceAttributes *struct {
				Ec2KeyName                     *string  `json:"Ec2KeyName"`
				Ec2SubnetID                    *string  `json:"Ec2SubnetId"`
				EmrManagedMasterSecurityGroup  *string  `json:"EmrManagedMasterSecurityGroup"`
				EmrManagedSlaveSecurityGroup   *string  `json:"EmrManagedSlaveSecurityGroup"`
				ServiceAccessSecurityGroup     *string  `json:"ServiceAccessSecurityGroup"`
				AdditionalMasterSecurityGroups []string `json:"AdditionalMasterSecurityGroups"`
				AdditionalSlaveSecurityGroups  []string `json:"AdditionalSlaveSecurityGroups"`
			} `json:"Ec2InstanceAttributes"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		region := sv(r.Region)
		for _, role := range []*string{attrs.ServiceRole, attrs.AutoScalingRole} {
			n := sv(role)
			if n == "" {
				continue
			}
			rarn := "arn:aws:iam::" + acct.ID + ":role/" + n
			tgt := store.ResourceID("aws", acct.ID, rarn)
			if !roleSet[tgt] {
				continue
			}
			if err := st.UpsertRelationship(r.ID, tgt, store.RelAssumes, "directed", nil); err != nil {
				return fmt.Errorf("upsert emr-cluster→role: %w", err)
			}
		}
		if ref := sv(attrs.LogEncryptionKmsKeyID); ref != "" {
			if keyID, ok := idx.resolveKMSKeyID(ref, region, acct.ID); ok {
				if err := st.UpsertRelationship(r.ID, keyID, store.RelUses, "directed", nil); err != nil {
					return fmt.Errorf("upsert emr-cluster→kms: %w", err)
				}
			}
		}
		if attrs.Ec2InstanceAttributes != nil {
			ea := attrs.Ec2InstanceAttributes
			if sn := sv(ea.Ec2SubnetID); sn != "" {
				tgt := store.ResourceID("aws", acct.ID, ec2ARN(region, acct.ID, "subnet", sn))
				if subnetSet[tgt] {
					if err := st.UpsertRelationship(r.ID, tgt, store.RelAttachedTo, "directed", nil); err != nil {
						return fmt.Errorf("upsert emr-cluster→subnet: %w", err)
					}
				}
			}
			sgs := []string{
				sv(ea.EmrManagedMasterSecurityGroup),
				sv(ea.EmrManagedSlaveSecurityGroup),
				sv(ea.ServiceAccessSecurityGroup),
			}
			sgs = append(sgs, ea.AdditionalMasterSecurityGroups...)
			sgs = append(sgs, ea.AdditionalSlaveSecurityGroups...)
			for _, sg := range sgs {
				if sg == "" {
					continue
				}
				tgt := store.ResourceID("aws", acct.ID, ec2ARN(region, acct.ID, "security-group", sg))
				if !sgSet[tgt] {
					continue
				}
				if err := st.UpsertRelationship(r.ID, tgt, store.RelAttachedTo, "directed", nil); err != nil {
					return fmt.Errorf("upsert emr-cluster→sg: %w", err)
				}
			}
			if kn := sv(ea.Ec2KeyName); kn != "" {
				if kID, ok := keyByNameRegion[region+"\x00"+kn]; ok {
					if err := st.UpsertRelationship(r.ID, kID, store.RelUses, "directed", nil); err != nil {
						return fmt.Errorf("upsert emr-cluster→keypair: %w", err)
					}
				}
			}
		}
	}
	return nil
}

// resolveEMRStudioVPC wires each EMR Studio to its VPC (StudioSummary.VpcId).
// FK-safe.
func resolveEMRStudioVPC(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeEMRStudio}, Limit: util.AllResources,
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
	for _, r := range rows {
		var attrs struct {
			VpcID *string `json:"VpcId"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		v := sv(attrs.VpcID)
		if v == "" {
			continue
		}
		tgt := store.ResourceID("aws", acct.ID, ec2ARN(sv(r.Region), acct.ID, "vpc", v))
		if !vpcSet[tgt] {
			continue
		}
		if err := st.UpsertRelationship(r.ID, tgt, store.RelAttachedTo, "directed", nil); err != nil {
			return fmt.Errorf("upsert emr studio→vpc: %w", err)
		}
	}
	return nil
}

// emrParentARN trims a `/segment/...` tail off a child NativeID to recover
// the parent ARN.
func emrParentARN(arn, segment string) string {
	i := strings.Index(arn, "/"+segment+"/")
	if i < 0 {
		return ""
	}
	return arn[:i]
}

// resolveEMRChildrenToCluster wires steps, instance-fleets and instance-groups
// to the cluster they belong to via NativeID parent extract.
func resolveEMRChildrenToCluster(acct *account, st *store.Store) error {
	clSet, err := scannedIDSet(acct, st, TypeEMRCluster)
	if err != nil {
		return err
	}
	for _, ct := range []struct{ typ, seg string }{
		{TypeEMRStep, "step"},
		{TypeEMRInstanceFleet, "instance-fleet"},
		{TypeEMRInstanceGroup, "instance-group"},
	} {
		rows, err := st.ListResources(store.ResourceFilter{
			Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{ct.typ}, Limit: util.AllResources,
		})
		if err != nil {
			return err
		}
		for _, r := range rows {
			parent := emrParentARN(r.NativeID, ct.seg)
			if parent == "" {
				continue
			}
			tgtID := store.ResourceID("aws", acct.ID, parent)
			if !clSet[tgtID] {
				continue
			}
			if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
				return fmt.Errorf("upsert emr %s→cluster: %w", ct.typ, err)
			}
		}
	}
	return nil
}

// resolveEMRStudioSessionMappingToStudio wires each session-mapping back to
// its parent studio via the `/identity/...` NativeID tail.
func resolveEMRStudioSessionMappingToStudio(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeEMRStudioSessionMapping}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	stSet, err := scannedIDSet(acct, st, TypeEMRStudio)
	if err != nil {
		return err
	}
	for _, r := range rows {
		parent := emrParentARN(r.NativeID, "identity")
		if parent == "" {
			continue
		}
		tgtID := store.ResourceID("aws", acct.ID, parent)
		if !stSet[tgtID] {
			continue
		}
		if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
			return fmt.Errorf("upsert emr session-mapping→studio: %w", err)
		}
	}
	return nil
}

package aws

import (
	"encoding/json"
	"fmt"

	"codeberg.org/icearp/disco/internal/store"
	"codeberg.org/icearp/disco/internal/util"
)

func init() {
	registerResolver(resolveRedshiftClusterTargets)
	registerResolver(resolveRedshiftSubnetGroupTargets)
}

// redshiftClusterAttrs mirrors the verbatim Cluster fields used by the
// resolver. PascalCase tags match mustJSON of the SDK v2 struct.
type redshiftClusterAttrs struct {
	ClusterSubnetGroupName *string `json:"ClusterSubnetGroupName"`
	KmsKeyId               *string `json:"KmsKeyId"`
	VpcId                  *string `json:"VpcId"`
	VpcSecurityGroups      []struct {
		VpcSecurityGroupId *string `json:"VpcSecurityGroupId"`
	} `json:"VpcSecurityGroups"`
	IamRoles []struct {
		IamRoleArn *string `json:"IamRoleArn"`
	} `json:"IamRoles"`
}

// resolveRedshiftClusterTargets emits the cluster's outbound edges:
//   - cluster → subnet-group (uses)
//   - cluster → KMS key (uses) via KmsKeyId
//   - cluster → VPC (attached-to) via VpcId
//   - cluster → security group (uses) per VpcSecurityGroups[]
//   - cluster → IAM role (assumes) per IamRoles[]
//
// FK-safe via per-type id sets + KMS resolve index. Cross-account refs
// and AWS-managed default keys (`alias/aws/*`) skip silently.
func resolveRedshiftClusterTargets(acct *account, st *store.Store) error {
	clusters, err := st.ListResources(store.ResourceFilter{
		Provider:  "aws",
		AccountID: acct.ID,
		Types:     []string{TypeRedshiftCluster},
		Limit:     util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(clusters) == 0 {
		return nil
	}

	subnetGroupIDs, err := resourceIDSet(st, acct.ID, TypeRedshiftSubnetGroup)
	if err != nil {
		return err
	}
	vpcIDs, err := resourceIDSet(st, acct.ID, TypeEC2VPC)
	if err != nil {
		return err
	}
	sgIDs, err := resourceIDSet(st, acct.ID, TypeEC2SecurityGroup)
	if err != nil {
		return err
	}
	roleIDs, err := resourceIDSet(st, acct.ID, TypeIAMRole)
	if err != nil {
		return err
	}
	kmsIdx, err := loadKMSResolveIndex(acct, st)
	if err != nil {
		return err
	}

	for _, c := range clusters {
		var attrs redshiftClusterAttrs
		if err := json.Unmarshal([]byte(c.AttributesJSON), &attrs); err != nil {
			continue
		}
		region := ""
		if c.Region != nil {
			region = *c.Region
		}

		if name := sv(attrs.ClusterSubnetGroupName); name != "" {
			sgARN := redshiftSubnetGroupARN(region, acct.ID, name)
			sgID := store.ResourceID("aws", acct.ID, TypeRedshiftSubnetGroup, sgARN)
			if _, ok := subnetGroupIDs[sgID]; ok {
				if err := st.UpsertRelationship(c.ID, sgID, store.RelUses, "directed", nil); err != nil {
					return fmt.Errorf("upsert redshift cluster→subnet-group: %w", err)
				}
			}
		}

		if vpcID := sv(attrs.VpcId); vpcID != "" {
			vpcARN := ec2ARN(region, acct.ID, "vpc", vpcID)
			vID := store.ResourceID("aws", acct.ID, TypeEC2VPC, vpcARN)
			if _, ok := vpcIDs[vID]; ok {
				if err := st.UpsertRelationship(c.ID, vID, store.RelAttachedTo, "directed", nil); err != nil {
					return fmt.Errorf("upsert redshift cluster→vpc: %w", err)
				}
			}
		}

		for _, sg := range attrs.VpcSecurityGroups {
			id := sv(sg.VpcSecurityGroupId)
			if id == "" {
				continue
			}
			sgARN := ec2ARN(region, acct.ID, "security-group", id)
			rID := store.ResourceID("aws", acct.ID, TypeEC2SecurityGroup, sgARN)
			if _, ok := sgIDs[rID]; ok {
				if err := st.UpsertRelationship(c.ID, rID, store.RelUses, "directed", nil); err != nil {
					return fmt.Errorf("upsert redshift cluster→sg: %w", err)
				}
			}
		}

		for _, r := range attrs.IamRoles {
			arn := sv(r.IamRoleArn)
			if arn == "" {
				continue
			}
			rID := store.ResourceID("aws", acct.ID, TypeIAMRole, arn)
			if _, ok := roleIDs[rID]; ok {
				if err := st.UpsertRelationship(c.ID, rID, store.RelAssumes, "directed", nil); err != nil {
					return fmt.Errorf("upsert redshift cluster→iam role: %w", err)
				}
			}
		}

		if keyRef := sv(attrs.KmsKeyId); keyRef != "" {
			if keyID, ok := kmsIdx.resolveKMSKeyID(keyRef, region, acct.ID); ok {
				if err := st.UpsertRelationship(c.ID, keyID, store.RelUses, "directed", nil); err != nil {
					return fmt.Errorf("upsert redshift cluster→kms: %w", err)
				}
			}
		}
	}
	return nil
}

// redshiftSubnetGroupAttrs mirrors the verbatim ClusterSubnetGroup fields
// used by the resolver.
type redshiftSubnetGroupAttrs struct {
	VpcId   *string `json:"VpcId"`
	Subnets []struct {
		SubnetIdentifier *string `json:"SubnetIdentifier"`
	} `json:"Subnets"`
}

// resolveRedshiftSubnetGroupTargets emits subnet-group → VPC (attached-to)
// + subnet-group → subnet (contains) per Subnets[]. FK-safe via VPC + subnet
// id sets.
func resolveRedshiftSubnetGroupTargets(acct *account, st *store.Store) error {
	groups, err := st.ListResources(store.ResourceFilter{
		Provider:  "aws",
		AccountID: acct.ID,
		Types:     []string{TypeRedshiftSubnetGroup},
		Limit:     util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(groups) == 0 {
		return nil
	}

	vpcIDs, err := resourceIDSet(st, acct.ID, TypeEC2VPC)
	if err != nil {
		return err
	}
	subnetIDs, err := resourceIDSet(st, acct.ID, TypeEC2Subnet)
	if err != nil {
		return err
	}

	for _, g := range groups {
		var attrs redshiftSubnetGroupAttrs
		if err := json.Unmarshal([]byte(g.AttributesJSON), &attrs); err != nil {
			continue
		}
		region := ""
		if g.Region != nil {
			region = *g.Region
		}

		if vpcID := sv(attrs.VpcId); vpcID != "" {
			vpcARN := ec2ARN(region, acct.ID, "vpc", vpcID)
			vID := store.ResourceID("aws", acct.ID, TypeEC2VPC, vpcARN)
			if _, ok := vpcIDs[vID]; ok {
				if err := st.UpsertRelationship(g.ID, vID, store.RelAttachedTo, "directed", nil); err != nil {
					return fmt.Errorf("upsert redshift subnet-group→vpc: %w", err)
				}
			}
		}

		for _, s := range attrs.Subnets {
			id := sv(s.SubnetIdentifier)
			if id == "" {
				continue
			}
			subARN := ec2ARN(region, acct.ID, "subnet", id)
			sID := store.ResourceID("aws", acct.ID, TypeEC2Subnet, subARN)
			if _, ok := subnetIDs[sID]; ok {
				if err := st.UpsertRelationship(g.ID, sID, store.RelContains, "directed", nil); err != nil {
					return fmt.Errorf("upsert redshift subnet-group→subnet: %w", err)
				}
			}
		}
	}
	return nil
}

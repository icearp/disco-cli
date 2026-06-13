package aws

import (
	"encoding/json"
	"fmt"
	"strings"

	"codeberg.org/icearp/disco/internal/util"
	"codeberg.org/icearp/disco/store"
)

func init() {
	registerResolver(
		resolveRedshiftClusterTargets,
		EdgeDecl{TypeRedshiftCluster, TypeRedshiftSubnetGroup, store.RelUses},
		EdgeDecl{TypeRedshiftCluster, TypeEC2VPC, store.RelAttachedTo},
		EdgeDecl{TypeRedshiftCluster, TypeEC2SecurityGroup, store.RelUses},
		EdgeDecl{TypeRedshiftCluster, TypeIAMRole, store.RelAssumes},
		EdgeDecl{TypeRedshiftCluster, TypeKMSKey, store.RelUses},
	)
	registerResolver(
		resolveRedshiftSubnetGroupTargets,
		EdgeDecl{TypeRedshiftSubnetGroup, TypeEC2VPC, store.RelAttachedTo},
		EdgeDecl{TypeRedshiftSubnetGroup, TypeEC2Subnet, store.RelContains},
	)
	registerResolver(
		resolveRedshiftIntegrationRefs,
		EdgeDecl{TypeRedshiftIntegration, TypeRDSDBCluster, store.RelUses},
		EdgeDecl{TypeRedshiftIntegration, TypeRedshiftCluster, store.RelUses},
		EdgeDecl{TypeRedshiftIntegration, TypeRedshiftServerlessNamespace, store.RelUses},
		EdgeDecl{TypeRedshiftIntegration, TypeKMSKey, store.RelUses},
	)
}

// resolveRedshiftIntegrationRefs wires each zero-ETL integration to its
// source (RDS / Redshift cluster) and target (Redshift cluster or Serverless
// namespace) plus the optional CMK. Source/target ARN dispatch by service
// segment. Mirrors resolveRDSIntegrationRefs in rds_resolvers.go.
func resolveRedshiftIntegrationRefs(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeRedshiftIntegration}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	rdsClusterSet, err := scannedIDSet(acct, st, TypeRDSDBCluster)
	if err != nil {
		return err
	}
	rsClusterSet, err := scannedIDSet(acct, st, TypeRedshiftCluster)
	if err != nil {
		return err
	}
	nsSet, err := scannedIDSet(acct, st, TypeRedshiftServerlessNamespace)
	if err != nil {
		return err
	}
	kmsIdx, err := loadKMSResolveIndex(acct, st)
	if err != nil {
		return err
	}
	dispatch := func(arn string) (string, string, bool) {
		switch {
		case strings.Contains(arn, ":rds:") && strings.Contains(arn, ":cluster:"):
			id := store.ResourceID("aws", acct.ID, TypeRDSDBCluster, arn)
			return id, TypeRDSDBCluster, rdsClusterSet[id]
		case strings.Contains(arn, ":redshift:") && strings.Contains(arn, ":cluster:"):
			id := store.ResourceID("aws", acct.ID, TypeRedshiftCluster, arn)
			return id, TypeRedshiftCluster, rsClusterSet[id]
		case strings.Contains(arn, ":redshift-serverless:") && strings.Contains(arn, ":namespace/"):
			id := store.ResourceID("aws", acct.ID, TypeRedshiftServerlessNamespace, arn)
			return id, TypeRedshiftServerlessNamespace, nsSet[id]
		}
		return "", "", false
	}
	for _, r := range rows {
		var attrs struct {
			SourceArn *string `json:"SourceArn"`
			TargetArn *string `json:"TargetArn"`
			KMSKeyID  *string `json:"KMSKeyId"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		for _, a := range []string{sv(attrs.SourceArn), sv(attrs.TargetArn)} {
			if a == "" {
				continue
			}
			if id, _, ok := dispatch(a); ok {
				if err := st.UpsertRelationship(r.ID, id, store.RelUses, "directed", nil); err != nil {
					return fmt.Errorf("upsert redshift integration→endpoint: %w", err)
				}
			}
		}
		if k := sv(attrs.KMSKeyID); k != "" {
			if keyID, ok := kmsIdx.resolveKMSKeyID(k, sv(r.Region), acct.ID); ok {
				if err := st.UpsertRelationship(r.ID, keyID, store.RelUses, "directed", nil); err != nil {
					return fmt.Errorf("upsert redshift integration→kms: %w", err)
				}
			}
		}
	}
	return nil
}

// redshiftClusterAttrs mirrors the verbatim Cluster fields used by the
// resolver. PascalCase tags match mustJSON of the SDK v2 struct.
type redshiftClusterAttrs struct {
	ClusterSubnetGroupName *string `json:"ClusterSubnetGroupName"`
	KmsKeyID               *string `json:"KmsKeyID"`
	VpcID                  *string `json:"VpcID"`
	VpcSecurityGroups      []struct {
		VpcSecurityGroupID *string `json:"VpcSecurityGroupID"`
	} `json:"VpcSecurityGroups"`
	IamRoles []struct {
		IamRoleArn *string `json:"IamRoleArn"`
	} `json:"IamRoles"`
}

// resolveRedshiftClusterTargets emits the cluster's outbound edges:
//   - cluster → subnet-group (uses)
//   - cluster → KMS key (uses) via KmsKeyID
//   - cluster → VPC (attached-to) via VpcID
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

		if vpcID := sv(attrs.VpcID); vpcID != "" {
			vpcARN := ec2ARN(region, acct.ID, "vpc", vpcID)
			vID := store.ResourceID("aws", acct.ID, TypeEC2VPC, vpcARN)
			if _, ok := vpcIDs[vID]; ok {
				if err := st.UpsertRelationship(c.ID, vID, store.RelAttachedTo, "directed", nil); err != nil {
					return fmt.Errorf("upsert redshift cluster→vpc: %w", err)
				}
			}
		}

		for _, sg := range attrs.VpcSecurityGroups {
			id := sv(sg.VpcSecurityGroupID)
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

		if keyRef := sv(attrs.KmsKeyID); keyRef != "" {
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
	VpcID   *string `json:"VpcID"`
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

		if vpcID := sv(attrs.VpcID); vpcID != "" {
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

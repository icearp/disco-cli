package aws

import (
	"encoding/json"
	"fmt"

	"codeberg.org/icearp/disco/store"
	"codeberg.org/icearp/disco/internal/util"
)

func init() {
	registerResolver(
		resolveDAXClusterRefs,
		EdgeDecl{TypeDAXCluster, TypeDAXParameterGroup, store.RelUses},
		EdgeDecl{TypeDAXCluster, TypeDAXSubnetGroup, store.RelAttachedTo},
		EdgeDecl{TypeDAXCluster, TypeEC2SecurityGroup, store.RelAttachedTo},
		EdgeDecl{TypeDAXCluster, TypeIAMRole, store.RelUses},
		EdgeDecl{TypeDAXCluster, TypeSNSTopic, store.RelUses},
	)
	registerResolver(
		resolveDAXSubnetGroupRefs,
		EdgeDecl{TypeDAXSubnetGroup, TypeEC2VPC, store.RelAttachedTo},
		EdgeDecl{TypeDAXSubnetGroup, TypeEC2Subnet, store.RelAttachedTo},
	)
}

func daxARN(region, acct, kind, name string) string {
	return fmt.Sprintf("arn:aws:dax:%s:%s:%s/%s", region, acct, kind, name)
}

// resolveDAXClusterRefs wires each cluster to its parameter group, subnet
// group, security groups, IAM role, and SNS notification topic.
func resolveDAXClusterRefs(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeDAXCluster}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	pgSet, err := scannedIDSet(acct, st, TypeDAXParameterGroup)
	if err != nil {
		return err
	}
	sgSet, err := scannedIDSet(acct, st, TypeDAXSubnetGroup)
	if err != nil {
		return err
	}
	ec2SgSet, err := scannedIDSet(acct, st, TypeEC2SecurityGroup)
	if err != nil {
		return err
	}
	roleSet, err := scannedIDSet(acct, st, TypeIAMRole)
	if err != nil {
		return err
	}
	topicSet, err := scannedIDSet(acct, st, TypeSNSTopic)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			ParameterGroup *struct {
				ParameterGroupName *string `json:"ParameterGroupName"`
			} `json:"ParameterGroup"`
			SubnetGroup    *string `json:"SubnetGroup"`
			SecurityGroups []struct {
				SecurityGroupIdentifier *string `json:"SecurityGroupIdentifier"`
			} `json:"SecurityGroups"`
			IamRoleArn                *string `json:"IamRoleArn"`
			NotificationConfiguration *struct {
				TopicArn *string `json:"TopicArn"`
			} `json:"NotificationConfiguration"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		region := sv(r.Region)
		if attrs.ParameterGroup != nil {
			if n := sv(attrs.ParameterGroup.ParameterGroupName); n != "" {
				tgtID := store.ResourceID("aws", acct.ID, TypeDAXParameterGroup, daxARN(region, acct.ID, "parameter-group", n))
				if pgSet[tgtID] {
					if err := st.UpsertRelationship(r.ID, tgtID, store.RelUses, "directed", nil); err != nil {
						return fmt.Errorf("upsert dax cl→pg: %w", err)
					}
				}
			}
		}
		if n := sv(attrs.SubnetGroup); n != "" {
			tgtID := store.ResourceID("aws", acct.ID, TypeDAXSubnetGroup, daxARN(region, acct.ID, "subnet-group", n))
			if sgSet[tgtID] {
				if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
					return fmt.Errorf("upsert dax cl→sg: %w", err)
				}
			}
		}
		for _, s := range attrs.SecurityGroups {
			if s.SecurityGroupIdentifier == nil {
				continue
			}
			id := *s.SecurityGroupIdentifier
			if id == "" {
				continue
			}
			tgtID := store.ResourceID("aws", acct.ID, TypeEC2SecurityGroup, ec2ARN(region, acct.ID, "security-group", id))
			if !ec2SgSet[tgtID] {
				continue
			}
			if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
				return fmt.Errorf("upsert dax cl→ec2-sg: %w", err)
			}
		}
		if role := sv(attrs.IamRoleArn); role != "" {
			tgtID := store.ResourceID("aws", acct.ID, TypeIAMRole, role)
			if roleSet[tgtID] {
				if err := st.UpsertRelationship(r.ID, tgtID, store.RelUses, "directed", nil); err != nil {
					return fmt.Errorf("upsert dax cl→role: %w", err)
				}
			}
		}
		if attrs.NotificationConfiguration != nil {
			if topic := sv(attrs.NotificationConfiguration.TopicArn); topic != "" {
				tgtID := store.ResourceID("aws", acct.ID, TypeSNSTopic, topic)
				if topicSet[tgtID] {
					if err := st.UpsertRelationship(r.ID, tgtID, store.RelUses, "directed", nil); err != nil {
						return fmt.Errorf("upsert dax cl→topic: %w", err)
					}
				}
			}
		}
	}
	return nil
}

// resolveDAXSubnetGroupRefs wires each subnet-group to its VPC and member
// subnets.
func resolveDAXSubnetGroupRefs(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeDAXSubnetGroup}, Limit: util.AllResources,
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
	subSet, err := scannedIDSet(acct, st, TypeEC2Subnet)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			VpcID   *string `json:"VpcId"`
			Subnets []struct {
				SubnetIdentifier *string `json:"SubnetIdentifier"`
			} `json:"Subnets"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		region := sv(r.Region)
		if v := sv(attrs.VpcID); v != "" {
			tgtID := store.ResourceID("aws", acct.ID, TypeEC2VPC, ec2ARN(region, acct.ID, "vpc", v))
			if vpcSet[tgtID] {
				if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
					return fmt.Errorf("upsert dax sg→vpc: %w", err)
				}
			}
		}
		for _, s := range attrs.Subnets {
			if s.SubnetIdentifier == nil {
				continue
			}
			sid := *s.SubnetIdentifier
			if sid == "" {
				continue
			}
			tgtID := store.ResourceID("aws", acct.ID, TypeEC2Subnet, ec2ARN(region, acct.ID, "subnet", sid))
			if !subSet[tgtID] {
				continue
			}
			if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
				return fmt.Errorf("upsert dax sg→subnet: %w", err)
			}
		}
	}
	return nil
}

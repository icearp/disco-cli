package aws

import (
	"context"
	"encoding/json"
	"math"

	"codeburg.org/icearp/disco/internal/store"
)

// resolveRelationships is phase 2: after all resources are written to the DB,
// derive relationships from the JSON attributes stored on each resource.
// Using ResourceID to compute stable IDs means we never need to read back
// resources just to get their primary keys.
func resolveRelationships(_ context.Context, acct *account, st *store.Store) error {
	if err := resolveInstanceRelationships(acct, st); err != nil {
		return err
	}
	if err := resolveSubnetVPCRelationships(acct, st); err != nil {
		return err
	}
	if err := resolveIGWRelationships(acct, st); err != nil {
		return err
	}
	if err := resolveRDSRelationships(acct, st); err != nil {
		return err
	}
	if err := resolveLambdaRelationships(acct, st); err != nil {
		return err
	}
	if err := resolveEKSRelationships(acct, st); err != nil {
		return err
	}
	return resolveELBRelationships(acct, st)
}

// allResources is a limit large enough to fetch all resources of a type in one
// query without implementing pagination in relationship resolution.
const allResources = uint64(math.MaxUint32)

// instanceAttrs captures the fields we need from an EC2 instance's JSON blob.
type instanceAttrs struct {
	InstanceId          *string `json:"InstanceId"`
	VpcId               *string `json:"VpcId"`
	SubnetId            *string `json:"SubnetId"`
	IamInstanceProfile  *struct {
		Arn *string `json:"Arn"`
	} `json:"IamInstanceProfile"`
	SecurityGroups []struct {
		GroupId *string `json:"GroupId"`
	} `json:"SecurityGroups"`
	BlockDeviceMappings []struct {
		Ebs *struct {
			VolumeId *string `json:"VolumeId"`
		} `json:"Ebs"`
	} `json:"BlockDeviceMappings"`
}

func resolveInstanceRelationships(acct *account, st *store.Store) error {
	instances, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{"aws:ec2:instance"},
		Limit: allResources,
	})
	if err != nil {
		return err
	}
	for _, r := range instances {
		var attrs instanceAttrs
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		// Instance → VPC
		if attrs.VpcId != nil {
			vpcID := store.ResourceID("aws", acct.ID, "aws:ec2:vpc", *attrs.VpcId)
			_ = st.UpsertRelationship(r.ID, vpcID, store.RelAttachedTo, "directed", nil)
		}
		// Instance → Subnet
		if attrs.SubnetId != nil {
			subnetID := store.ResourceID("aws", acct.ID, "aws:ec2:subnet", *attrs.SubnetId)
			_ = st.UpsertRelationship(r.ID, subnetID, store.RelAttachedTo, "directed", nil)
		}
		// Instance → Security Groups
		for _, sg := range attrs.SecurityGroups {
			if sg.GroupId != nil {
				sgID := store.ResourceID("aws", acct.ID, "aws:ec2:security-group", *sg.GroupId)
				_ = st.UpsertRelationship(r.ID, sgID, store.RelUses, "directed", nil)
			}
		}
		// Instance → EBS Volumes (from block device mappings)
		for _, bdm := range attrs.BlockDeviceMappings {
			if bdm.Ebs != nil && bdm.Ebs.VolumeId != nil {
				volID := store.ResourceID("aws", acct.ID, "aws:ec2:volume", *bdm.Ebs.VolumeId)
				_ = st.UpsertRelationship(r.ID, volID, store.RelAttachedTo, "directed", nil)
			}
		}
		// Instance → IAM Role (via instance profile ARN)
		// The instance profile ARN is arn:aws:iam::{account}:instance-profile/{name}.
		// The role ARN is not directly in the instance attrs, so we skip this
		// relationship here; it would require an IAM GetInstanceProfile call.
	}
	return nil
}

func resolveSubnetVPCRelationships(acct *account, st *store.Store) error {
	subnets, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{"aws:ec2:subnet"},
		Limit: allResources,
	})
	if err != nil {
		return err
	}
	for _, r := range subnets {
		var attrs struct {
			VpcId *string `json:"VpcId"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if attrs.VpcId != nil {
			vpcID := store.ResourceID("aws", acct.ID, "aws:ec2:vpc", *attrs.VpcId)
			_ = st.UpsertRelationship(r.ID, vpcID, store.RelAttachedTo, "directed", nil)
		}
	}
	return nil
}

func resolveIGWRelationships(acct *account, st *store.Store) error {
	igws, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{"aws:ec2:internet-gateway"},
		Limit: allResources,
	})
	if err != nil {
		return err
	}
	for _, r := range igws {
		var attrs struct {
			Attachments []struct {
				VpcId *string `json:"VpcId"`
			} `json:"Attachments"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		for _, att := range attrs.Attachments {
			if att.VpcId != nil {
				vpcID := store.ResourceID("aws", acct.ID, "aws:ec2:vpc", *att.VpcId)
				_ = st.UpsertRelationship(r.ID, vpcID, store.RelAttachedTo, "directed", nil)
			}
		}
	}
	return nil
}

func resolveRDSRelationships(acct *account, st *store.Store) error {
	dbs, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{"aws:rds:db-instance"},
		Limit: allResources,
	})
	if err != nil {
		return err
	}
	for _, r := range dbs {
		var attrs struct {
			DBSubnetGroup *struct {
				VpcId *string `json:"VpcId"`
			} `json:"DBSubnetGroup"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if attrs.DBSubnetGroup != nil && attrs.DBSubnetGroup.VpcId != nil {
			vpcID := store.ResourceID("aws", acct.ID, "aws:ec2:vpc", *attrs.DBSubnetGroup.VpcId)
			_ = st.UpsertRelationship(r.ID, vpcID, store.RelAttachedTo, "directed", nil)
		}
	}
	return nil
}

func resolveLambdaRelationships(acct *account, st *store.Store) error {
	fns, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{"aws:lambda:function"},
		Limit: allResources,
	})
	if err != nil {
		return err
	}
	for _, r := range fns {
		var attrs struct {
			Role *string `json:"Role"` // IAM role ARN
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if attrs.Role != nil {
			// IAM role NativeID is the ARN; ResourceID is derived from it.
			roleID := store.ResourceID("aws", acct.ID, "aws:iam:role", *attrs.Role)
			_ = st.UpsertRelationship(r.ID, roleID, store.RelAssumes, "directed", nil)
		}
	}
	return nil
}

func resolveEKSRelationships(acct *account, st *store.Store) error {
	clusters, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{"aws:eks:cluster"},
		Limit: allResources,
	})
	if err != nil {
		return err
	}
	for _, r := range clusters {
		var attrs struct {
			ResourcesVpcConfig *struct {
				VpcId *string `json:"VpcId"`
			} `json:"ResourcesVpcConfig"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if attrs.ResourcesVpcConfig != nil && attrs.ResourcesVpcConfig.VpcId != nil {
			vpcID := store.ResourceID("aws", acct.ID, "aws:ec2:vpc", *attrs.ResourcesVpcConfig.VpcId)
			_ = st.UpsertRelationship(r.ID, vpcID, store.RelAttachedTo, "directed", nil)
		}
	}
	return nil
}

func resolveELBRelationships(acct *account, st *store.Store) error {
	lbs, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID,
		Types: []string{"aws:elasticloadbalancing:load-balancer"},
		Limit: allResources,
	})
	if err != nil {
		return err
	}
	for _, r := range lbs {
		var attrs struct {
			Lb *struct {
				VpcId *string `json:"VpcId"`
			} `json:"lb"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if attrs.Lb != nil && attrs.Lb.VpcId != nil {
			vpcID := store.ResourceID("aws", acct.ID, "aws:ec2:vpc", *attrs.Lb.VpcId)
			_ = st.UpsertRelationship(r.ID, vpcID, store.RelAttachedTo, "directed", nil)
		}
	}
	return nil
}

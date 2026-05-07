package aws

import (
	"encoding/json"
	"fmt"

	"codeberg.org/icearp/disco/internal/store"
	"codeberg.org/icearp/disco/internal/util"
)

func init() {
	registerResolver(
		resolveMemoryDBClusterRefs,
		EdgeDecl{TypeMemoryDBCluster, TypeKMSKey, store.RelUses},
		EdgeDecl{TypeMemoryDBCluster, TypeMemoryDBACL, store.RelUses},
		EdgeDecl{TypeMemoryDBCluster, TypeMemoryDBSubnetGroup, store.RelAttachedTo},
		EdgeDecl{TypeMemoryDBCluster, TypeMemoryDBParameterGroup, store.RelUses},
		EdgeDecl{TypeMemoryDBCluster, TypeMemoryDBMultiRegionCluster, store.RelAttachedTo},
		EdgeDecl{TypeMemoryDBCluster, TypeEC2SecurityGroup, store.RelUses},
		EdgeDecl{TypeMemoryDBCluster, TypeSNSTopic, store.RelUses},
	)
	registerResolver(
		resolveMemoryDBSubnetGroupRefs,
		EdgeDecl{TypeMemoryDBSubnetGroup, TypeEC2VPC, store.RelAttachedTo},
		EdgeDecl{TypeMemoryDBSubnetGroup, TypeEC2Subnet, store.RelAttachedTo},
	)
	registerResolver(
		resolveMemoryDBACLUsers,
		EdgeDecl{TypeMemoryDBACL, TypeMemoryDBUser, store.RelContains},
	)
}

// memDBNameIndex builds a (region, Name) → resourceID map for name-keyed
// MemoryDB cross-references. ACL/subnet-group/parameter-group/multi-region-
// cluster names are unique per (account, region).
func memDBNameIndex(acct *account, st *store.Store, rtype string) (map[string]string, error) {
	rows, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{rtype}, Limit: util.AllResources,
	})
	if err != nil {
		return nil, err
	}
	idx := make(map[string]string, len(rows))
	for _, r := range rows {
		if name := sv(r.Name); name != "" {
			idx[sv(r.Region)+"|"+name] = r.ID
		}
	}
	return idx, nil
}

// resolveMemoryDBClusterRefs walks each cluster's KmsKeyID, ACLName,
// SubnetGroupName, ParameterGroupName, MultiRegionClusterName, SecurityGroups[],
// and SnsTopicArn — every cross-resource ref the DescribeClusters response
// already carries.
func resolveMemoryDBClusterRefs(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeMemoryDBCluster}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	kmsIdx, err := loadKMSResolveIndex(acct, st)
	if err != nil {
		return err
	}
	aclIdx, err := memDBNameIndex(acct, st, TypeMemoryDBACL)
	if err != nil {
		return err
	}
	sgIdx, err := memDBNameIndex(acct, st, TypeMemoryDBSubnetGroup)
	if err != nil {
		return err
	}
	pgIdx, err := memDBNameIndex(acct, st, TypeMemoryDBParameterGroup)
	if err != nil {
		return err
	}
	mrcIdx, err := memDBNameIndex(acct, st, TypeMemoryDBMultiRegionCluster)
	if err != nil {
		return err
	}
	ec2SGSet, err := scannedIDSet(acct, st, TypeEC2SecurityGroup)
	if err != nil {
		return err
	}
	snsSet, err := scannedIDSet(acct, st, TypeSNSTopic)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			KmsKeyID               *string `json:"KmsKeyId"`
			ACLName                *string `json:"ACLName"`
			SubnetGroupName        *string `json:"SubnetGroupName"`
			ParameterGroupName     *string `json:"ParameterGroupName"`
			MultiRegionClusterName *string `json:"MultiRegionClusterName"`
			SnsTopicArn            *string `json:"SnsTopicArn"`
			SecurityGroups         []struct {
				SecurityGroupID *string `json:"SecurityGroupId"`
			} `json:"SecurityGroups"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		region := sv(r.Region)
		emit := func(tgtID, kind string) error {
			if tgtID == "" {
				return nil
			}
			if err := st.UpsertRelationship(r.ID, tgtID, kind, "directed", nil); err != nil {
				return fmt.Errorf("upsert memorydb cluster→%s: %w", kind, err)
			}
			return nil
		}
		if ref := sv(attrs.KmsKeyID); ref != "" {
			if id, ok := kmsIdx.resolveKMSKeyID(ref, region, acct.ID); ok {
				if err := emit(id, store.RelUses); err != nil {
					return err
				}
			}
		}
		if name := sv(attrs.ACLName); name != "" {
			if err := emit(aclIdx[region+"|"+name], store.RelUses); err != nil {
				return err
			}
		}
		if name := sv(attrs.SubnetGroupName); name != "" {
			if err := emit(sgIdx[region+"|"+name], store.RelAttachedTo); err != nil {
				return err
			}
		}
		if name := sv(attrs.ParameterGroupName); name != "" {
			if err := emit(pgIdx[region+"|"+name], store.RelUses); err != nil {
				return err
			}
		}
		if name := sv(attrs.MultiRegionClusterName); name != "" {
			if err := emit(mrcIdx[region+"|"+name], store.RelAttachedTo); err != nil {
				return err
			}
		}
		if topic := sv(attrs.SnsTopicArn); topic != "" {
			tgtID := store.ResourceID("aws", acct.ID, TypeSNSTopic, topic)
			if snsSet[tgtID] {
				if err := emit(tgtID, store.RelUses); err != nil {
					return err
				}
			}
		}
		for _, sg := range attrs.SecurityGroups {
			id := sv(sg.SecurityGroupID)
			if id == "" {
				continue
			}
			tgtID := store.ResourceID("aws", acct.ID, TypeEC2SecurityGroup, ec2ARN(region, acct.ID, "security-group", id))
			if !ec2SGSet[tgtID] {
				continue
			}
			if err := emit(tgtID, store.RelUses); err != nil {
				return err
			}
		}
	}
	return nil
}

// resolveMemoryDBSubnetGroupRefs wires subnet-group → VPC + Subnets[].
func resolveMemoryDBSubnetGroupRefs(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeMemoryDBSubnetGroup}, Limit: util.AllResources,
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
	for _, r := range rows {
		var attrs struct {
			VpcID   *string `json:"VpcId"`
			Subnets []struct {
				Identifier *string `json:"Identifier"`
			} `json:"Subnets"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		region := sv(r.Region)
		if id := sv(attrs.VpcID); id != "" {
			tgtID := store.ResourceID("aws", acct.ID, TypeEC2VPC, ec2ARN(region, acct.ID, "vpc", id))
			if vpcSet[tgtID] {
				if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
					return fmt.Errorf("upsert memorydb subnet-group→vpc: %w", err)
				}
			}
		}
		for _, s := range attrs.Subnets {
			id := sv(s.Identifier)
			if id == "" {
				continue
			}
			tgtID := store.ResourceID("aws", acct.ID, TypeEC2Subnet, ec2ARN(region, acct.ID, "subnet", id))
			if !subnetSet[tgtID] {
				continue
			}
			if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
				return fmt.Errorf("upsert memorydb subnet-group→subnet: %w", err)
			}
		}
	}
	return nil
}

// resolveMemoryDBACLUsers wires each ACL to the MemoryDB users granted access
// (UserNames[] — name lookup against per-region user set).
func resolveMemoryDBACLUsers(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeMemoryDBACL}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	userIdx, err := memDBNameIndex(acct, st, TypeMemoryDBUser)
	if err != nil {
		return err
	}
	var pairs [][2]string
	for _, r := range rows {
		var attrs struct {
			UserNames []string `json:"UserNames"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		region := sv(r.Region)
		for _, name := range attrs.UserNames {
			if name == "" {
				continue
			}
			childID, ok := userIdx[region+"|"+name]
			if !ok {
				continue
			}
			pairs = append(pairs, [2]string{childID, r.ID})
		}
	}
	if len(pairs) == 0 {
		return nil
	}
	return st.RecordHierarchyBatch(pairs)
}

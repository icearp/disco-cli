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

// memDBClusterAttrs mirrors the cluster fields the resolver walks.
type memDBClusterAttrs struct {
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

// memDBClusterTargetSets bundles the FK-safe id sets + KMS index for the
// cluster resolver helpers.
type memDBClusterTargetSets struct {
	kmsIdx                       *kmsResolveIndex
	aclIdx, sgIdx, pgIdx, mrcIdx map[string]string
	ec2SGSet, snsSet             map[string]bool
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
	sets, err := loadMemDBClusterTargetSets(acct, st)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs memDBClusterAttrs
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		region := sv(r.Region)
		if err := emitMemDBKMSEdge(st, acct, r, region, attrs, sets); err != nil {
			return err
		}
		if err := emitMemDBNameEdges(st, r, region, attrs, sets); err != nil {
			return err
		}
		if err := emitMemDBSNSEdge(st, acct, r, attrs, sets); err != nil {
			return err
		}
		if err := emitMemDBSGEdges(st, acct, r, region, attrs, sets); err != nil {
			return err
		}
	}
	return nil
}

func loadMemDBClusterTargetSets(acct *account, st *store.Store) (memDBClusterTargetSets, error) {
	var sets memDBClusterTargetSets
	var err error
	if sets.kmsIdx, err = loadKMSResolveIndex(acct, st); err != nil {
		return sets, err
	}
	if sets.aclIdx, err = memDBNameIndex(acct, st, TypeMemoryDBACL); err != nil {
		return sets, err
	}
	if sets.sgIdx, err = memDBNameIndex(acct, st, TypeMemoryDBSubnetGroup); err != nil {
		return sets, err
	}
	if sets.pgIdx, err = memDBNameIndex(acct, st, TypeMemoryDBParameterGroup); err != nil {
		return sets, err
	}
	if sets.mrcIdx, err = memDBNameIndex(acct, st, TypeMemoryDBMultiRegionCluster); err != nil {
		return sets, err
	}
	if sets.ec2SGSet, err = scannedIDSet(acct, st, TypeEC2SecurityGroup); err != nil {
		return sets, err
	}
	if sets.snsSet, err = scannedIDSet(acct, st, TypeSNSTopic); err != nil {
		return sets, err
	}
	return sets, nil
}

// emitMemDBClusterEdge upserts srcID → tgtID when tgtID is non-empty.
func emitMemDBClusterEdge(st *store.Store, srcID, tgtID, kind string) error {
	if tgtID == "" {
		return nil
	}
	if err := st.UpsertRelationship(srcID, tgtID, kind, "directed", nil); err != nil {
		return fmt.Errorf("upsert memorydb cluster→%s: %w", kind, err)
	}
	return nil
}

func emitMemDBKMSEdge(st *store.Store, acct *account, r store.Resource, region string, attrs memDBClusterAttrs, sets memDBClusterTargetSets) error {
	ref := sv(attrs.KmsKeyID)
	if ref == "" {
		return nil
	}
	id, ok := sets.kmsIdx.resolveKMSKeyID(ref, region, acct.ID)
	if !ok {
		return nil
	}
	return emitMemDBClusterEdge(st, r.ID, id, store.RelUses)
}

func emitMemDBNameEdges(st *store.Store, r store.Resource, region string, attrs memDBClusterAttrs, sets memDBClusterTargetSets) error {
	for _, e := range []struct {
		name string
		idx  map[string]string
		kind string
	}{
		{sv(attrs.ACLName), sets.aclIdx, store.RelUses},
		{sv(attrs.SubnetGroupName), sets.sgIdx, store.RelAttachedTo},
		{sv(attrs.ParameterGroupName), sets.pgIdx, store.RelUses},
		{sv(attrs.MultiRegionClusterName), sets.mrcIdx, store.RelAttachedTo},
	} {
		if e.name == "" {
			continue
		}
		if err := emitMemDBClusterEdge(st, r.ID, e.idx[region+"|"+e.name], e.kind); err != nil {
			return err
		}
	}
	return nil
}

func emitMemDBSNSEdge(st *store.Store, acct *account, r store.Resource, attrs memDBClusterAttrs, sets memDBClusterTargetSets) error {
	topic := sv(attrs.SnsTopicArn)
	if topic == "" {
		return nil
	}
	tgtID := store.ResourceID("aws", acct.ID, TypeSNSTopic, topic)
	if !sets.snsSet[tgtID] {
		return nil
	}
	return emitMemDBClusterEdge(st, r.ID, tgtID, store.RelUses)
}

func emitMemDBSGEdges(st *store.Store, acct *account, r store.Resource, region string, attrs memDBClusterAttrs, sets memDBClusterTargetSets) error {
	for _, sg := range attrs.SecurityGroups {
		id := sv(sg.SecurityGroupID)
		if id == "" {
			continue
		}
		tgtID := store.ResourceID("aws", acct.ID, TypeEC2SecurityGroup, ec2ARN(region, acct.ID, "security-group", id))
		if !sets.ec2SGSet[tgtID] {
			continue
		}
		if err := emitMemDBClusterEdge(st, r.ID, tgtID, store.RelUses); err != nil {
			return err
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

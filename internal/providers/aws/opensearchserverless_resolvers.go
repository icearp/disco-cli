package aws

import (
	"encoding/json"
	"fmt"

	"codeberg.org/icearp/disco/internal/util"
	"codeberg.org/icearp/disco/store"
)

func init() {
	registerResolver(
		resolveOSSCollectionRefs,
		EdgeDecl{TypeOSSCollection, TypeKMSKey, store.RelUses},
		EdgeDecl{TypeOSSCollection, TypeOSSCollectionGroup, store.RelAttachedTo},
	)
}

// resolveOSSCollectionRefs wires each collection to its KMS key (KmsKeyArn) and
// optional collection-group (CollectionGroupName).
func resolveOSSCollectionRefs(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeOSSCollection}, Limit: util.AllResources,
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
	groupRows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeOSSCollectionGroup}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	// index: (region, name) → group resource ID. Groups are per-region.
	groupByRegionName := map[string]string{}
	for _, gr := range groupRows {
		var ga struct {
			Name *string `json:"Name"`
		}
		if err := json.Unmarshal([]byte(gr.AttributesJSON), &ga); err != nil {
			continue
		}
		if n := sv(ga.Name); n != "" {
			groupByRegionName[sv(gr.Region)+"|"+n] = gr.ID
		}
	}
	for _, r := range rows {
		var attrs struct {
			KmsKeyArn           *string `json:"KmsKeyArn"`
			CollectionGroupName *string `json:"CollectionGroupName"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		region := sv(r.Region)
		if k := sv(attrs.KmsKeyArn); k != "" {
			if keyID, ok := kmsIdx.resolveKMSKeyID(k, region, acct.ID); ok {
				if err := st.UpsertRelationship(r.ID, keyID, store.RelUses, "directed", nil); err != nil {
					return fmt.Errorf("upsert oss coll→kms: %w", err)
				}
			}
		}
		if g := sv(attrs.CollectionGroupName); g != "" {
			if gid, ok := groupByRegionName[region+"|"+g]; ok {
				if err := st.UpsertRelationship(r.ID, gid, store.RelAttachedTo, "directed", nil); err != nil {
					return fmt.Errorf("upsert oss coll→cg: %w", err)
				}
			}
		}
	}
	return nil
}

func init() {
	registerResolver(
		resolveOSSVpcEndpointRefs,
		EdgeDecl{TypeOSSVpcEndpoint, TypeEC2VPC, store.RelAttachedTo},
		EdgeDecl{TypeOSSVpcEndpoint, TypeEC2Subnet, store.RelAttachedTo},
		EdgeDecl{TypeOSSVpcEndpoint, TypeEC2SecurityGroup, store.RelAttachedTo},
	)
}

// resolveOSSVpcEndpointRefs wires each OpenSearch Serverless VPC endpoint
// to its VPC + subnets + security groups. BatchGetVpcEndpoint body shape.
func resolveOSSVpcEndpointRefs(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeOSSVpcEndpoint}, Limit: util.AllResources,
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
	sgSet, err := scannedIDSet(acct, st, TypeEC2SecurityGroup)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			VpcID            *string  `json:"VpcId"`
			SubnetIDs        []string `json:"SubnetIds"`
			SecurityGroupIDs []string `json:"SecurityGroupIds"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		region := sv(r.Region)
		if v := sv(attrs.VpcID); v != "" {
			tgt := store.ResourceID("aws", acct.ID, TypeEC2VPC, ec2ARN(region, acct.ID, "vpc", v))
			if vpcSet[tgt] {
				if err := st.UpsertRelationship(r.ID, tgt, store.RelAttachedTo, "directed", nil); err != nil {
					return fmt.Errorf("upsert oss-vpce→vpc: %w", err)
				}
			}
		}
		for _, sn := range attrs.SubnetIDs {
			tgt := store.ResourceID("aws", acct.ID, TypeEC2Subnet, ec2ARN(region, acct.ID, "subnet", sn))
			if !subnetSet[tgt] {
				continue
			}
			if err := st.UpsertRelationship(r.ID, tgt, store.RelAttachedTo, "directed", nil); err != nil {
				return fmt.Errorf("upsert oss-vpce→subnet: %w", err)
			}
		}
		for _, sg := range attrs.SecurityGroupIDs {
			tgt := store.ResourceID("aws", acct.ID, TypeEC2SecurityGroup, ec2ARN(region, acct.ID, "security-group", sg))
			if !sgSet[tgt] {
				continue
			}
			if err := st.UpsertRelationship(r.ID, tgt, store.RelAttachedTo, "directed", nil); err != nil {
				return fmt.Errorf("upsert oss-vpce→sg: %w", err)
			}
		}
	}
	return nil
}

package aws

import (
	"encoding/json"
	"fmt"

	"codeberg.org/icearp/disco/internal/store"
	"codeberg.org/icearp/disco/internal/util"
)

func init() {
	registerResolver(
		resolveDirectoryServiceVpcRefs,
		EdgeDecl{TypeDSMicrosoftAD, TypeEC2VPC, store.RelAttachedTo},
		EdgeDecl{TypeDSMicrosoftAD, TypeEC2Subnet, store.RelAttachedTo},
		EdgeDecl{TypeDSMicrosoftAD, TypeEC2SecurityGroup, store.RelAttachedTo},
		EdgeDecl{TypeDSSimpleAD, TypeEC2VPC, store.RelAttachedTo},
		EdgeDecl{TypeDSSimpleAD, TypeEC2Subnet, store.RelAttachedTo},
		EdgeDecl{TypeDSSimpleAD, TypeEC2SecurityGroup, store.RelAttachedTo},
	)
}

// resolveDirectoryServiceVpcRefs wires Microsoft AD and Simple AD directories
// to the VPC, subnets, and SG they live in via VpcSettings.
func resolveDirectoryServiceVpcRefs(acct *account, st *store.Store) error {
	vpcSet, err := scannedIDSet(acct, st, TypeEC2VPC)
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
	for _, t := range []string{TypeDSMicrosoftAD, TypeDSSimpleAD} {
		rows, err := st.ListResources(store.ResourceFilter{
			Provider: "aws", AccountID: acct.ID, Types: []string{t}, Limit: util.AllResources,
		})
		if err != nil {
			return err
		}
		for _, r := range rows {
			var attrs struct {
				VpcSettings *struct {
					VpcID           *string  `json:"VpcId"`
					SubnetIDs       []string `json:"SubnetIds"`
					SecurityGroupID *string  `json:"SecurityGroupId"`
				} `json:"VpcSettings"`
			}
			if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
				continue
			}
			if attrs.VpcSettings == nil {
				continue
			}
			region := sv(r.Region)
			if v := sv(attrs.VpcSettings.VpcID); v != "" {
				tgtID := store.ResourceID("aws", acct.ID, TypeEC2VPC, ec2ARN(region, acct.ID, "vpc", v))
				if vpcSet[tgtID] {
					if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
						return fmt.Errorf("upsert ds %s→vpc: %w", t, err)
					}
				}
			}
			for _, sid := range attrs.VpcSettings.SubnetIDs {
				if sid == "" {
					continue
				}
				tgtID := store.ResourceID("aws", acct.ID, TypeEC2Subnet, ec2ARN(region, acct.ID, "subnet", sid))
				if !subSet[tgtID] {
					continue
				}
				if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
					return fmt.Errorf("upsert ds %s→subnet: %w", t, err)
				}
			}
			if g := sv(attrs.VpcSettings.SecurityGroupID); g != "" {
				tgtID := store.ResourceID("aws", acct.ID, TypeEC2SecurityGroup, ec2ARN(region, acct.ID, "security-group", g))
				if sgSet[tgtID] {
					if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
						return fmt.Errorf("upsert ds %s→sg: %w", t, err)
					}
				}
			}
		}
	}
	return nil
}

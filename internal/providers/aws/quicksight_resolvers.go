package aws

import (
	"encoding/json"
	"fmt"
	"strings"

	"codeberg.org/icearp/disco/internal/store"
	"codeberg.org/icearp/disco/internal/util"
)

func init() {
	registerResolver(resolveQuickSightVPCConnectionRefs,
		EdgeDecl{TypeQuickSightVPCConnection, TypeEC2VPC, store.RelAttachedTo},
		EdgeDecl{TypeQuickSightVPCConnection, TypeEC2Subnet, store.RelAttachedTo},
		EdgeDecl{TypeQuickSightVPCConnection, TypeEC2SecurityGroup, store.RelUses},
		EdgeDecl{TypeQuickSightVPCConnection, TypeIAMRole, store.RelAssumes},
	)
	registerResolver(resolveQuickSightRefreshScheduleParent,
		EdgeDecl{TypeQuickSightRefreshSchedule, TypeQuickSightDataSet, store.RelAttachedTo},
	)
}

// resolveQuickSightVPCConnectionRefs wires VPC connection → vpc + subnets
// (NetworkInterfaces[].SubnetId) + security groups + IAM role.
func resolveQuickSightVPCConnectionRefs(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeQuickSightVPCConnection},
		Limit: util.AllResources,
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
	roleSet, err := scannedIDSet(acct, st, TypeIAMRole)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			VPCId             *string  `json:"VPCId"`
			SecurityGroupIds  []string `json:"SecurityGroupIds"`
			RoleArn           *string  `json:"RoleArn"`
			NetworkInterfaces []struct {
				SubnetId *string `json:"SubnetId"`
			} `json:"NetworkInterfaces"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		region := sv(r.Region)
		if vpc := sv(attrs.VPCId); vpc != "" {
			vARN := ec2ARN(region, acct.ID, "vpc", vpc)
			tgtID := store.ResourceID("aws", acct.ID, TypeEC2VPC, vARN)
			if vpcSet[tgtID] {
				if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
					return fmt.Errorf("upsert qs vpc-conn→vpc: %w", err)
				}
			}
		}
		for _, ni := range attrs.NetworkInterfaces {
			sid := sv(ni.SubnetId)
			if sid == "" {
				continue
			}
			sARN := ec2ARN(region, acct.ID, "subnet", sid)
			tgtID := store.ResourceID("aws", acct.ID, TypeEC2Subnet, sARN)
			if subnetSet[tgtID] {
				if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
					return fmt.Errorf("upsert qs vpc-conn→subnet: %w", err)
				}
			}
		}
		for _, sg := range attrs.SecurityGroupIds {
			sgARN := ec2ARN(region, acct.ID, "security-group", sg)
			tgtID := store.ResourceID("aws", acct.ID, TypeEC2SecurityGroup, sgARN)
			if sgSet[tgtID] {
				if err := st.UpsertRelationship(r.ID, tgtID, store.RelUses, "directed", nil); err != nil {
					return fmt.Errorf("upsert qs vpc-conn→sg: %w", err)
				}
			}
		}
		if role := sv(attrs.RoleArn); role != "" {
			tgtID := store.ResourceID("aws", acct.ID, TypeIAMRole, role)
			if roleSet[tgtID] {
				if err := st.UpsertRelationship(r.ID, tgtID, store.RelAssumes, "directed", nil); err != nil {
					return fmt.Errorf("upsert qs vpc-conn→role: %w", err)
				}
			}
		}
	}
	return nil
}

// resolveQuickSightRefreshScheduleParent wires each refresh-schedule to its
// parent dataset by parsing the NativeID of shape
// `arn:aws:quicksight:r:a:dataset/{datasetID}/refresh-schedule/{id}`.
func resolveQuickSightRefreshScheduleParent(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeQuickSightRefreshSchedule},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	dsSet, err := scannedIDSet(acct, st, TypeQuickSightDataSet)
	if err != nil {
		return err
	}
	for _, r := range rows {
		const seg = "/refresh-schedule/"
		i := strings.Index(r.NativeID, seg)
		if i < 0 {
			continue
		}
		dsARN := r.NativeID[:i]
		tgtID := store.ResourceID("aws", acct.ID, TypeQuickSightDataSet, dsARN)
		if !dsSet[tgtID] {
			continue
		}
		if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
			return fmt.Errorf("upsert qs refresh-schedule→dataset: %w", err)
		}
	}
	return nil
}

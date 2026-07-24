package aws

import (
	"encoding/json"
	"fmt"

	"github.com/icearp/disco-cli/internal/util"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerResolver(
		resolveCloudHSMClusterNetwork,
		EdgeDecl{TypeCloudHSMCluster, TypeEC2VPC, store.RelAttachedTo},
		EdgeDecl{TypeCloudHSMCluster, TypeEC2SecurityGroup, store.RelUses},
		EdgeDecl{TypeCloudHSMCluster, TypeEC2Subnet, store.RelAttachedTo},
	)
	registerResolver(
		resolveCloudHSMBackupCluster,
		EdgeDecl{TypeCloudHSMBackup, TypeCloudHSMCluster, store.RelAttachedTo},
	)
}

// resolveCloudHSMClusterNetwork wires each cluster to its VPC, security group, and
// subnets (SubnetMapping az→subnet); FK-safe.
func resolveCloudHSMClusterNetwork(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeCloudHSMCluster},
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
	sgSet, err := scannedIDSet(acct, st, TypeEC2SecurityGroup)
	if err != nil {
		return err
	}
	subnetSet, err := scannedIDSet(acct, st, TypeEC2Subnet)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			VpcID         *string           `json:"VpcId"`
			SecurityGroup *string           `json:"SecurityGroup"`
			SubnetMapping map[string]string `json:"SubnetMapping"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		region := sv(r.Region)
		if err := cloudHSMNetEdge(st, acct.ID, r.ID, vpcSet, region, "vpc", sv(attrs.VpcID), TypeEC2VPC, store.RelAttachedTo); err != nil {
			return err
		}
		if err := cloudHSMNetEdge(st, acct.ID, r.ID, sgSet, region, "security-group", sv(attrs.SecurityGroup), TypeEC2SecurityGroup, store.RelUses); err != nil {
			return err
		}
		seen := map[string]bool{}
		for _, subnetID := range attrs.SubnetMapping {
			if subnetID == "" || seen[subnetID] {
				continue
			}
			seen[subnetID] = true
			if err := cloudHSMNetEdge(st, acct.ID, r.ID, subnetSet, region, "subnet", subnetID, TypeEC2Subnet, store.RelAttachedTo); err != nil {
				return err
			}
		}
	}
	return nil
}

// cloudHSMNetEdge emits one FK-safe edge from src to the EC2 resource named by
// rawID; skips empty refs and unscanned targets.
func cloudHSMNetEdge(st *store.Store, acctID, srcID string, tgtSet map[string]bool, region, kind, rawID, tgtType, edgeKind string) error {
	if rawID == "" {
		return nil
	}
	tgtID := store.ResourceID("aws", acctID, ec2ARN(region, acctID, kind, rawID))
	if !tgtSet[tgtID] {
		return nil
	}
	if err := st.UpsertRelationship(srcID, tgtID, edgeKind, "directed", nil); err != nil {
		return fmt.Errorf("upsert cloudhsm cluster→%s: %w", kind, err)
	}
	return nil
}

// resolveCloudHSMBackupCluster wires each backup to its source cluster via the
// ClusterId the backup summary carries, FK-safe.
func resolveCloudHSMBackupCluster(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeCloudHSMBackup},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	clusterSet, err := scannedIDSet(acct, st, TypeCloudHSMCluster)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			ClusterID *string `json:"ClusterId"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		cid := sv(attrs.ClusterID)
		if cid == "" {
			continue
		}
		clusterARN := fmt.Sprintf("arn:aws:cloudhsm:%s:%s:cluster/%s", sv(r.Region), acct.ID, cid)
		tgtID := store.ResourceID("aws", acct.ID, clusterARN)
		if !clusterSet[tgtID] {
			continue
		}
		if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
			return fmt.Errorf("upsert cloudhsm backup→cluster: %w", err)
		}
	}
	return nil
}

package aws

import (
	"encoding/json"
	"fmt"
	"testing"

	"codeberg.org/icearp/disco/store"
	cloudhsmv2types "github.com/aws/aws-sdk-go-v2/service/cloudhsmv2/types"
)

func cloudHSMClusterAttrs(t *testing.T, c cloudhsmv2types.Cluster) string {
	t.Helper()
	b, err := json.Marshal(c)
	if err != nil {
		t.Fatalf("marshal cluster: %v", err)
	}
	return string(b)
}

func TestResolveCloudHSMClusterNetwork(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	vpcID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2VPC, ec2ARN(testRegion, acct.ID, "vpc", "vpc-1"), testRegion, "{}")
	sgID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2SecurityGroup, ec2ARN(testRegion, acct.ID, "security-group", "sg-1"), testRegion, "{}")
	subnetID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2Subnet, ec2ARN(testRegion, acct.ID, "subnet", "subnet-1"), testRegion, "{}")

	attrs := cloudHSMClusterAttrs(t, cloudhsmv2types.Cluster{
		ClusterId:     ptrStr("cluster-abc"),
		VpcId:         ptrStr("vpc-1"),
		SecurityGroup: ptrStr("sg-1"),
		SubnetMapping: map[string]string{"us-east-1a": "subnet-1"},
	})
	clusterARN := fmt.Sprintf("arn:aws:cloudhsm:%s:%s:cluster/cluster-abc", testRegion, acct.ID)
	clusterID := upsertTestResource(t, st, "aws", acct.ID, TypeCloudHSMCluster, clusterARN, testRegion, attrs)

	if err := resolveCloudHSMClusterNetwork(acct, st); err != nil {
		t.Fatalf("resolveCloudHSMClusterNetwork: %v", err)
	}
	rels, _ := st.RelationshipsFrom(clusterID)
	assertRelationship(t, rels, clusterID, vpcID, store.RelAttachedTo)
	assertRelationship(t, rels, clusterID, sgID, store.RelUses)
	assertRelationship(t, rels, clusterID, subnetID, store.RelAttachedTo)
}

// A cluster whose VPC/SG/subnet refs are absent or point at unscanned EC2
// resources emits no edges.
func TestResolveCloudHSMClusterNetwork_NoEdge(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	attrs := cloudHSMClusterAttrs(t, cloudhsmv2types.Cluster{
		ClusterId:     ptrStr("cluster-x"),
		VpcId:         ptrStr("vpc-missing"),
		SecurityGroup: ptrStr("sg-missing"),
		SubnetMapping: map[string]string{"us-east-1a": "subnet-missing"},
	})
	clusterARN := fmt.Sprintf("arn:aws:cloudhsm:%s:%s:cluster/cluster-x", testRegion, acct.ID)
	clusterID := upsertTestResource(t, st, "aws", acct.ID, TypeCloudHSMCluster, clusterARN, testRegion, attrs)

	if err := resolveCloudHSMClusterNetwork(acct, st); err != nil {
		t.Fatalf("resolveCloudHSMClusterNetwork: %v", err)
	}
	rels, _ := st.RelationshipsFrom(clusterID)
	if len(rels) != 0 {
		t.Errorf("emitted %d edges, want 0", len(rels))
	}
}

func TestResolveCloudHSMBackupCluster(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	clusterARN := fmt.Sprintf("arn:aws:cloudhsm:%s:%s:cluster/cluster-abc", testRegion, acct.ID)
	clusterID := upsertTestResource(t, st, "aws", acct.ID, TypeCloudHSMCluster, clusterARN, testRegion, "{}")

	backupARN := fmt.Sprintf("arn:aws:cloudhsm:%s:%s:backup/backup-1", testRegion, acct.ID)
	b, _ := json.Marshal(cloudhsmv2types.Backup{BackupArn: ptrStr(backupARN), ClusterId: ptrStr("cluster-abc")})
	backupID := upsertTestResource(t, st, "aws", acct.ID, TypeCloudHSMBackup, backupARN, testRegion, string(b))

	if err := resolveCloudHSMBackupCluster(acct, st); err != nil {
		t.Fatalf("resolveCloudHSMBackupCluster: %v", err)
	}
	rels, _ := st.RelationshipsFrom(backupID)
	assertRelationship(t, rels, backupID, clusterID, store.RelAttachedTo)
}

// A backup whose ClusterId points at an unscanned cluster, and one with no
// ClusterId, emit no edge.
func TestResolveCloudHSMBackupCluster_NoEdge(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	a1 := fmt.Sprintf("arn:aws:cloudhsm:%s:%s:backup/b1", testRegion, acct.ID)
	b1, _ := json.Marshal(cloudhsmv2types.Backup{BackupArn: ptrStr(a1), ClusterId: ptrStr("cluster-gone")})
	id1 := upsertTestResource(t, st, "aws", acct.ID, TypeCloudHSMBackup, a1, testRegion, string(b1))

	a2 := fmt.Sprintf("arn:aws:cloudhsm:%s:%s:backup/b2", testRegion, acct.ID)
	id2 := upsertTestResource(t, st, "aws", acct.ID, TypeCloudHSMBackup, a2, testRegion, "{}")

	if err := resolveCloudHSMBackupCluster(acct, st); err != nil {
		t.Fatalf("resolveCloudHSMBackupCluster: %v", err)
	}
	for _, id := range []string{id1, id2} {
		rels, _ := st.RelationshipsFrom(id)
		if len(rels) != 0 {
			t.Errorf("row %s emitted %d edges, want 0", id, len(rels))
		}
	}
}

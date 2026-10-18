package aws

import (
	"fmt"
	"testing"

	odbtypes "github.com/aws/aws-sdk-go-v2/service/odb/types"
	"github.com/icearp/disco-cli/store"
)

func TestResolveODBAutonomousDatabaseNetwork(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	netARN := fmt.Sprintf("arn:aws:odb:%s:%s:odb-network/odbnet-1", testRegion, acct.ID)
	netID := upsertTestResource(t, st, "aws", acct.ID, TypeODBOdbNetwork, netARN, testRegion, "{}")
	adbARN := fmt.Sprintf("arn:aws:odb:%s:%s:autonomous-database/adb-1", testRegion, acct.ID)
	adbAttrs := mustJSON(odbtypes.AutonomousDatabaseSummary{
		AutonomousDatabaseArn: &adbARN,
		OdbNetworkArn:         &netARN,
	})
	adbID := upsertTestResource(t, st, "aws", acct.ID, TypeODBAutonomousDatabase, adbARN, testRegion, adbAttrs)
	if err := resolveODBAutonomousDatabaseNetwork(acct, st); err != nil {
		t.Fatalf("resolveODBAutonomousDatabaseNetwork: %v", err)
	}
	rels, _ := st.RelationshipsFrom(adbID)
	assertRelationship(t, rels, adbID, netID, store.RelUses)
}

func TestResolveODBAutonomousDatabaseNetwork_NoAttrs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	adbARN := fmt.Sprintf("arn:aws:odb:%s:%s:autonomous-database/adb-1", testRegion, acct.ID)
	adbID := upsertTestResource(t, st, "aws", acct.ID, TypeODBAutonomousDatabase, adbARN, testRegion, "{}")
	if err := resolveODBAutonomousDatabaseNetwork(acct, st); err != nil {
		t.Fatalf("resolveODBAutonomousDatabaseNetwork: %v", err)
	}
	if rels, _ := st.RelationshipsFrom(adbID); len(rels) != 0 {
		t.Errorf("expected no relationships, got %d", len(rels))
	}
}

func TestResolveODBAutonomousDatabaseBackupParent(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	adbID := "adb-xyz"
	adbARN := fmt.Sprintf("arn:aws:odb:%s:%s:autonomous-database/%s", testRegion, acct.ID, adbID)
	adbAttrs := mustJSON(odbtypes.AutonomousDatabaseSummary{
		AutonomousDatabaseArn: &adbARN,
		AutonomousDatabaseId:  &adbID,
	})
	adbResID := upsertTestResource(t, st, "aws", acct.ID, TypeODBAutonomousDatabase, adbARN, testRegion, adbAttrs)
	bkARN := fmt.Sprintf("arn:aws:odb:%s:%s:autonomous-database-backup/bk-1", testRegion, acct.ID)
	bkAttrs := mustJSON(odbtypes.AutonomousDatabaseBackupSummary{
		AutonomousDatabaseBackupArn: &bkARN,
		AutonomousDatabaseId:        &adbID,
	})
	bkResID := upsertTestResource(t, st, "aws", acct.ID, TypeODBAutonomousDatabaseBackup, bkARN, testRegion, bkAttrs)
	if err := resolveODBAutonomousDatabaseBackupParent(acct, st); err != nil {
		t.Fatalf("resolveODBAutonomousDatabaseBackupParent: %v", err)
	}
	rels, _ := st.RelationshipsFrom(bkResID)
	assertRelationship(t, rels, bkResID, adbResID, store.RelAttachedTo)
}

func TestResolveODBAutonomousDatabaseBackupParent_NoAttrs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	bkARN := fmt.Sprintf("arn:aws:odb:%s:%s:autonomous-database-backup/bk-1", testRegion, acct.ID)
	bkResID := upsertTestResource(t, st, "aws", acct.ID, TypeODBAutonomousDatabaseBackup, bkARN, testRegion, "{}")
	if err := resolveODBAutonomousDatabaseBackupParent(acct, st); err != nil {
		t.Fatalf("resolveODBAutonomousDatabaseBackupParent: %v", err)
	}
	if rels, _ := st.RelationshipsFrom(bkResID); len(rels) != 0 {
		t.Errorf("expected no relationships, got %d", len(rels))
	}
}

func TestResolveODBDbNodeCluster(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	clusterARN := fmt.Sprintf("arn:aws:odb:%s:%s:cloud-vm-cluster/vmc-1", testRegion, acct.ID)
	clusterID := upsertTestResource(t, st, "aws", acct.ID, TypeODBCloudVMCluster, clusterARN, testRegion, "{}")
	nodeNativeID := clusterARN + "/db-node/node-1"
	nodeAttrs := mustJSON(odbtypes.DbNodeSummary{DbNodeId: sp("node-1")})
	nodeID := upsertTestResource(t, st, "aws", acct.ID, TypeODBDbNode, nodeNativeID, testRegion, nodeAttrs)
	if err := resolveODBDbNodeCluster(acct, st); err != nil {
		t.Fatalf("resolveODBDbNodeCluster: %v", err)
	}
	rels, _ := st.RelationshipsFrom(nodeID)
	assertRelationship(t, rels, nodeID, clusterID, store.RelAttachedTo)
}

// odbExadbClusterRefAttrs is an Exascale VM cluster's attributes naming the
// given network and vault.
func odbExadbClusterRefAttrs(id, netARN, vaultARN string) string {
	return mustJSON(odbtypes.ExadbVmClusterSummary{
		ExadbVmClusterId: sp(id), ExadbVmClusterArn: sp(odbExadbClusterARN(id)),
		OdbNetworkArn: sp(netARN), ExascaleDbStorageVaultArn: sp(vaultARN),
	})
}

var odbTestNetARN = fmt.Sprintf("arn:aws:odb:%s:%s:odb-network/odbnet-1", testRegion, testAccountID)

func TestResolveODBExadbVMClusterRefs_WiresNetworkAndVault(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	netID := upsertTestResource(t, st, "aws", acct.ID, TypeODBOdbNetwork, odbTestNetARN, testRegion, "{}")
	vaultID := upsertTestResource(t, st, "aws", acct.ID, TypeODBExascaleDbStorageVault, odbVaultARN("v1"), testRegion, "{}")
	cID := upsertTestResource(t, st, "aws", acct.ID, TypeODBExadbVMCluster, odbExadbClusterARN("c1"), testRegion,
		odbExadbClusterRefAttrs("c1", odbTestNetARN, odbVaultARN("v1")))
	if err := resolveODBExadbVMClusterRefs(acct, st); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	rels := dsRelsFrom(t, st, cID)
	assertRelationship(t, rels, cID, netID, store.RelUses)
	assertRelationship(t, rels, cID, vaultID, store.RelUses)
	if len(rels) != 2 {
		t.Errorf("got %d relationships; want 2 (network, vault)", len(rels))
	}
}

func TestResolveODBExadbVMClusterRefs_NoAttrs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	upsertTestResource(t, st, "aws", acct.ID, TypeODBOdbNetwork, odbTestNetARN, testRegion, "{}")
	cID := upsertTestResource(t, st, "aws", acct.ID, TypeODBExadbVMCluster, odbExadbClusterARN("c1"), testRegion, "{}")
	if err := resolveODBExadbVMClusterRefs(acct, st); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if rels := dsRelsFrom(t, st, cID); len(rels) != 0 {
		t.Errorf("got %d relationships; want none", len(rels))
	}
}

func TestResolveODBExadbVMClusterRefs_UnscannedTargets(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	cID := upsertTestResource(t, st, "aws", acct.ID, TypeODBExadbVMCluster, odbExadbClusterARN("c1"), testRegion,
		odbExadbClusterRefAttrs("c1", odbTestNetARN, odbVaultARN("v1")))
	if err := resolveODBExadbVMClusterRefs(acct, st); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if rels := dsRelsFrom(t, st, cID); len(rels) != 0 {
		t.Errorf("got %d relationships to unscanned targets; want none", len(rels))
	}
}

func TestResolveODBExadbVMClusterRefs_DecodeErrorReturned(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	netID := upsertTestResource(t, st, "aws", acct.ID, TypeODBOdbNetwork, odbTestNetARN, testRegion, "{}")
	// ListResources orders by name, so the bad row "a" is read before the
	// valid row "b", whose edge must survive the decode error.
	upsertTestResourceNamed(t, st, TypeODBExadbVMCluster, odbExadbClusterARN("bad"), testRegion, `["not an object"]`, "a")
	cID := upsertTestResourceNamed(t, st, TypeODBExadbVMCluster, odbExadbClusterARN("c1"), testRegion,
		odbExadbClusterRefAttrs("c1", odbTestNetARN, ""), "b")
	if err := resolveODBExadbVMClusterRefs(acct, st); err == nil {
		t.Fatal("resolve: nil error, want the decode error")
	}
	rels := dsRelsFrom(t, st, cID)
	assertRelationship(t, rels, cID, netID, store.RelUses)
}

func TestResolveODBDbNodeCluster_NoMatch(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	// Cluster not scanned — FK-safe lookup must emit no edge.
	clusterARN := fmt.Sprintf("arn:aws:odb:%s:%s:cloud-vm-cluster/vmc-missing", testRegion, acct.ID)
	nodeNativeID := clusterARN + "/db-node/node-1"
	nodeID := upsertTestResource(t, st, "aws", acct.ID, TypeODBDbNode, nodeNativeID, testRegion, "{}")
	if err := resolveODBDbNodeCluster(acct, st); err != nil {
		t.Fatalf("resolveODBDbNodeCluster: %v", err)
	}
	if rels, _ := st.RelationshipsFrom(nodeID); len(rels) != 0 {
		t.Errorf("expected no relationships, got %d", len(rels))
	}
}

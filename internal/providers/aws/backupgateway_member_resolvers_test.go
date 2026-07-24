package aws

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/icearp/disco-cli/store"
	bgwtypes "github.com/aws/aws-sdk-go-v2/service/backupgateway/types"
)

func bgwAttrs(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("bgwAttrs: %v", err)
	}
	return string(b)
}

func TestResolveBackupGatewayMemberHypervisor(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	hypID := "hype-12345678"
	hypARN := fmt.Sprintf("arn:aws:backup-gateway:%s:%s:hypervisor/%s", testRegion, testAccountID, hypID)
	hID := upsertTestResource(t, st, "aws", acct.ID, TypeBackupGatewayHypervisor, hypARN, testRegion, "{}")

	gwARN := fmt.Sprintf("arn:aws:backup-gateway:%s:%s:gateway/sgw-aaaa1111", testRegion, testAccountID)
	gwID := upsertTestResource(t, st, "aws", acct.ID, TypeBackupGatewayGateway, gwARN, testRegion,
		bgwAttrs(t, bgwtypes.Gateway{GatewayArn: &gwARN, HypervisorId: &hypID}))

	vmARN := fmt.Sprintf("arn:aws:backup-gateway:%s:%s:vm/vm-bbbb2222", testRegion, testAccountID)
	vmID := upsertTestResource(t, st, "aws", acct.ID, TypeBackupGatewayVirtualMachine, vmARN, testRegion,
		bgwAttrs(t, bgwtypes.VirtualMachine{ResourceArn: &vmARN, HypervisorId: &hypID}))

	if err := resolveBackupGatewayMemberHypervisor(acct, st); err != nil {
		t.Fatalf("resolveBackupGatewayMemberHypervisor: %v", err)
	}
	gwRels, _ := st.RelationshipsFrom(gwID)
	assertRelationship(t, gwRels, gwID, hID, store.RelAttachedTo)
	vmRels, _ := st.RelationshipsFrom(vmID)
	assertRelationship(t, vmRels, vmID, hID, store.RelAttachedTo)
}

// A member referencing an unscanned hypervisor (or carrying none) emits no edge.
func TestResolveBackupGatewayMemberHypervisor_Unknown(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	// Seed a hypervisor so the index is non-empty, then a VM pointing elsewhere.
	hypARN := fmt.Sprintf("arn:aws:backup-gateway:%s:%s:hypervisor/hype-present0", testRegion, testAccountID)
	upsertTestResource(t, st, "aws", acct.ID, TypeBackupGatewayHypervisor, hypARN, testRegion, "{}")

	vmARN := fmt.Sprintf("arn:aws:backup-gateway:%s:%s:vm/vm-orphan", testRegion, testAccountID)
	goneHyp := "hype-gone99"
	vmID := upsertTestResource(t, st, "aws", acct.ID, TypeBackupGatewayVirtualMachine, vmARN, testRegion,
		bgwAttrs(t, bgwtypes.VirtualMachine{ResourceArn: &vmARN, HypervisorId: &goneHyp}))

	if err := resolveBackupGatewayMemberHypervisor(acct, st); err != nil {
		t.Fatalf("resolveBackupGatewayMemberHypervisor: %v", err)
	}
	rels, _ := st.RelationshipsFrom(vmID)
	if len(rels) != 0 {
		t.Errorf("member with unknown hypervisor emitted %d edges, want 0", len(rels))
	}
}

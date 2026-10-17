package aws

import (
	"fmt"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/backupgateway"
	"github.com/icearp/disco-cli/store"
)

func TestResolveBackupGatewayPropertyMappingRoles(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	scannedRole := fmt.Sprintf("arn:aws:iam::%s:role/bgw-tags", acct.ID)
	unscannedRole := fmt.Sprintf("arn:aws:iam::%s:role/not-scanned", acct.ID)
	roleID := upsertTestResource(t, st, "aws", acct.ID, TypeIAMRole, scannedRole, "", "{}")

	withRole := upsertTestResource(t, st, "aws", acct.ID, TypeBackupGatewayHypervisorPropertyMapping, "hyp-1/property-mappings", testRegion,
		bgwAttrs(t, backupgateway.GetHypervisorPropertyMappingsOutput{IamRoleArn: &scannedRole}))
	danglingRole := upsertTestResource(t, st, "aws", acct.ID, TypeBackupGatewayHypervisorPropertyMapping, "hyp-2/property-mappings", testRegion,
		bgwAttrs(t, backupgateway.GetHypervisorPropertyMappingsOutput{IamRoleArn: &unscannedRole}))
	noRole := upsertTestResource(t, st, "aws", acct.ID, TypeBackupGatewayHypervisorPropertyMapping, "hyp-3/property-mappings", testRegion,
		bgwAttrs(t, backupgateway.GetHypervisorPropertyMappingsOutput{}))

	if err := resolveBackupGatewayPropertyMappingRoles(acct, st); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	rels, _ := st.RelationshipsFrom(withRole)
	assertRelationship(t, rels, withRole, roleID, store.RelUses)
	for _, id := range []string{danglingRole, noRole} {
		if rels, _ := st.RelationshipsFrom(id); len(rels) != 0 {
			t.Errorf("mapping %s rels = %+v, want none", id, rels)
		}
	}
}

func TestResolveBackupGatewayPropertyMappingRoles_DecodeErrorReturned(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	upsertTestResource(t, st, "aws", acct.ID, TypeBackupGatewayHypervisorPropertyMapping, "hyp-1/property-mappings", testRegion, `{"IamRoleArn":7}`)
	if err := resolveBackupGatewayPropertyMappingRoles(acct, st); err == nil {
		t.Fatal("resolve: nil error, want the decode error")
	}
}

func TestResolveBackupGatewayRelationships(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	kmsARN := fmt.Sprintf("arn:aws:kms:%s:%s:key/key-bgw", testRegion, acct.ID)
	hypARN := fmt.Sprintf("arn:aws:backup-gateway:%s:%s:hypervisor/hyp-1", testRegion, acct.ID)
	attrs := fmt.Sprintf(`{"KmsKeyArn":%q,"Name":"vc1"}`, kmsARN)

	hID := upsertTestResource(t, st, "aws", acct.ID, TypeBackupGatewayHypervisor, hypARN, testRegion, attrs)
	kID := upsertTestResource(t, st, "aws", acct.ID, TypeKMSKey, kmsARN, testRegion, "{}")

	if err := resolveBackupGatewayRelationships(acct, st); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	rels, _ := st.RelationshipsFrom(hID)
	assertRelationship(t, rels, hID, kID, store.RelUses)
}

func TestResolveBackupGatewayRelationships_NoKey(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	hypARN := fmt.Sprintf("arn:aws:backup-gateway:%s:%s:hypervisor/hyp-2", testRegion, acct.ID)
	hID := upsertTestResource(t, st, "aws", acct.ID, TypeBackupGatewayHypervisor, hypARN, testRegion, `{"Name":"vc2"}`)

	if err := resolveBackupGatewayRelationships(acct, st); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	rels, _ := st.RelationshipsFrom(hID)
	if len(rels) != 0 {
		t.Errorf("unexpected rels: %+v", rels)
	}
}

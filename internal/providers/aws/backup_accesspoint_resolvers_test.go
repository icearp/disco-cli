package aws

import (
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	backuptypes "github.com/aws/aws-sdk-go-v2/service/backup/types"
	"github.com/icearp/disco-cli/store"
)

func TestResolveBackupAccessPointRefs(t *testing.T) {
	for _, tc := range []struct {
		name, vaultType string
	}{
		{"standard vault", TypeBackupVault},
		{"air-gapped vault", TypeBackupLogicallyAirGappedVault},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			acct := newTestAccount(testAccountID)
			vaultARN := fmt.Sprintf("arn:aws:backup:%s:%s:backup-vault:v1", testRegion, testAccountID)
			vaultID := upsertTestResource(t, st, "aws", acct.ID, tc.vaultType, vaultARN, testRegion, "{}")
			rpARN := fmt.Sprintf("arn:aws:backup:%s:%s:recovery-point:rp-1", testRegion, testAccountID)
			rpID := upsertTestResource(t, st, "aws", acct.ID, TypeBackupRecoveryPoint, rpARN, testRegion, "{}")
			apARN := fmt.Sprintf("arn:aws:backup:%s:%s:accesspoint/ap1", testRegion, testAccountID)
			apID := upsertTestResource(t, st, "aws", acct.ID, TypeBackupAccessPoint, apARN, testRegion,
				mustJSON(backuptypes.ListAccessPointsMember{AccessPointArn: &apARN, RecoveryPointArn: &rpARN, BackupVaultArn: &vaultARN}))

			if err := resolveBackupAccessPointRefs(acct, st); err != nil {
				t.Fatalf("resolveBackupAccessPointRefs: %v", err)
			}
			rels, err := st.RelationshipsFrom(apID)
			if err != nil {
				t.Fatalf("RelationshipsFrom: %v", err)
			}
			assertRelationship(t, rels, apID, rpID, store.RelAttachedTo)
			assertRelationship(t, rels, apID, vaultID, store.RelAttachedTo)
		})
	}
}

// An access point whose recovery point and vault are unscanned (another
// account's, or deleted) emits no edge.
func TestResolveBackupAccessPointRefs_Unscanned(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	apARN := fmt.Sprintf("arn:aws:backup:%s:%s:accesspoint/ap1", testRegion, testAccountID)
	apID := upsertTestResource(t, st, "aws", acct.ID, TypeBackupAccessPoint, apARN, testRegion,
		mustJSON(backuptypes.ListAccessPointsMember{
			AccessPointArn:   &apARN,
			RecoveryPointArn: ptrStr("arn:aws:backup:us-east-1:999999999999:recovery-point:gone"),
			BackupVaultArn:   ptrStr("arn:aws:backup:us-east-1:999999999999:backup-vault:gone"),
		}))

	if err := resolveBackupAccessPointRefs(acct, st); err != nil {
		t.Fatalf("resolveBackupAccessPointRefs: %v", err)
	}
	rels, err := st.RelationshipsFrom(apID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 0 {
		t.Errorf("edges = %v, want none", rels)
	}
}

func TestResolveBackupAccessPointRefs_NoRefs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	upsertTestResource(t, st, "aws", acct.ID, TypeBackupVault, fmt.Sprintf("arn:aws:backup:%s:%s:backup-vault:v1", testRegion, testAccountID), testRegion, "{}")
	apID := upsertTestResource(t, st, "aws", acct.ID, TypeBackupAccessPoint, "arn:aws:backup:us-east-1:123456789012:accesspoint/ap1", testRegion, "{}")

	if err := resolveBackupAccessPointRefs(acct, st); err != nil {
		t.Fatalf("resolveBackupAccessPointRefs: %v", err)
	}
	rels, err := st.RelationshipsFrom(apID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 0 {
		t.Errorf("edges = %v, want none", rels)
	}
}

// A row whose attributes do not parse is reported, and the rows after it are
// still wired.
func TestResolveBackupAccessPointRefs_MalformedRowReportedOthersWired(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	vaultARN := fmt.Sprintf("arn:aws:backup:%s:%s:backup-vault:v1", testRegion, testAccountID)
	vaultID := upsertTestResource(t, st, "aws", acct.ID, TypeBackupVault, vaultARN, testRegion, "{}")
	// ListResources orders by name, so the malformed row comes first.
	upsert := func(name, attrs string) string {
		t.Helper()
		r := &store.Resource{
			Provider: "aws", AccountID: acct.ID, Type: TypeBackupAccessPoint, Name: &name, Region: ptrStr(testRegion),
			NativeID: fmt.Sprintf("arn:aws:backup:%s:%s:accesspoint/%s", testRegion, testAccountID, name), AttributesJSON: attrs, DiscoveredBy: testScanID,
		}
		if _, err := st.UpsertResources([]*store.Resource{r}); err != nil {
			t.Fatalf("upsert %s: %v", name, err)
		}
		return r.ID
	}
	upsert("a-bad", "{")
	goodID := upsert("b-good", mustJSON(backuptypes.ListAccessPointsMember{BackupVaultArn: &vaultARN}))

	err := resolveBackupAccessPointRefs(acct, st)
	var syntaxErr *json.SyntaxError
	if !errors.As(err, &syntaxErr) {
		t.Errorf("err = %v, want the malformed row's *json.SyntaxError", err)
	}
	rels, rerr := st.RelationshipsFrom(goodID)
	if rerr != nil {
		t.Fatalf("RelationshipsFrom: %v", rerr)
	}
	assertRelationship(t, rels, goodID, vaultID, store.RelAttachedTo)
}

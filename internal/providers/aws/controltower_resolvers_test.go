package aws

import (
	"fmt"
	"testing"

	"codeberg.org/icearp/disco/store"
)

const (
	testCTBaselineID = "abc-baseline-1"
	testCTOUId       = "ou-aaaa-1234567890"
	testCTAcctID     = "555555555555"
)

func ctBaselineARN() string {
	return fmt.Sprintf("arn:aws:controltower:%s:%s:enabledBaseline/%s", testRegion, testAccountID, testCTBaselineID)
}

// TestResolveControlTowerBaselineTarget_OUTarget verifies baseline
// → org-OU edge.
func TestResolveControlTowerBaselineTarget_OUTarget(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	ouARN := fmt.Sprintf("arn:aws:organizations::%s:ou/o-test/%s", testAccountID, testCTOUId)
	ouAttrs := fmt.Sprintf(`{"Id":%q,"Arn":%q}`, testCTOUId, ouARN)
	ouID := upsertTestResource(t, st, "aws", acct.ID, TypeOrganizationsOU, ouARN, "", ouAttrs)

	baselineAttrs := fmt.Sprintf(`{"Arn":%q,"TargetIdentifier":%q,"BaselineIdentifier":"AWSControlTowerBaseline","BaselineVersion":"4.0"}`,
		ctBaselineARN(), ouARN)
	baselineID := upsertTestResource(t, st, "aws", acct.ID, TypeControlTowerEnabledBaseline, ctBaselineARN(), testRegion, baselineAttrs)

	if err := resolveControlTowerBaselineTarget(acct, st); err != nil {
		t.Fatalf("resolveControlTowerBaselineTarget: %v", err)
	}
	rels, err := st.RelationshipsFrom(baselineID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	assertRelationship(t, rels, baselineID, ouID, store.RelAttachedTo)
}

// TestResolveControlTowerBaselineTarget_AccountTarget verifies baseline
// → org-account edge.
func TestResolveControlTowerBaselineTarget_AccountTarget(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	acctARN := fmt.Sprintf("arn:aws:organizations::%s:account/o-test/%s", testAccountID, testCTAcctID)
	acctAttrs := fmt.Sprintf(`{"Id":%q,"Arn":%q}`, testCTAcctID, acctARN)
	acctRowID := upsertTestResource(t, st, "aws", acct.ID, TypeOrganizationsAccount, acctARN, "", acctAttrs)

	baselineAttrs := fmt.Sprintf(`{"Arn":%q,"TargetIdentifier":%q}`, ctBaselineARN(), acctARN)
	baselineID := upsertTestResource(t, st, "aws", acct.ID, TypeControlTowerEnabledBaseline, ctBaselineARN(), testRegion, baselineAttrs)

	if err := resolveControlTowerBaselineTarget(acct, st); err != nil {
		t.Fatalf("resolveControlTowerBaselineTarget: %v", err)
	}
	rels, err := st.RelationshipsFrom(baselineID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	assertRelationship(t, rels, baselineID, acctRowID, store.RelAttachedTo)
}

// TestResolveControlTowerBaselineTarget_FKSafe verifies missing target
// (org tree not scanned) skips without erroring.
func TestResolveControlTowerBaselineTarget_FKSafe(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	baselineAttrs := fmt.Sprintf(`{"TargetIdentifier":"arn:aws:organizations::%s:ou/o-test/ghost"}`, testAccountID)
	baselineID := upsertTestResource(t, st, "aws", acct.ID, TypeControlTowerEnabledBaseline, ctBaselineARN(), testRegion, baselineAttrs)

	if err := resolveControlTowerBaselineTarget(acct, st); err != nil {
		t.Fatalf("resolveControlTowerBaselineTarget: %v", err)
	}
	rels, err := st.RelationshipsFrom(baselineID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 0 {
		t.Errorf("expected zero edges, got %d", len(rels))
	}
}

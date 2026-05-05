package aws

import (
	"fmt"
	"testing"

	"codeberg.org/icearp/disco/internal/store"
)

// TestResolveUXCAccountCustomizationToOrgAccount verifies the uxc singleton
// links to the aws:organizations:account row matching its account ID.
func TestResolveUXCAccountCustomizationToOrgAccount(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	uxcARN := fmt.Sprintf("arn:aws:uxc::%s:account-customization", acct.ID)
	uxcID := upsertTestResource(t, st, "aws", acct.ID, TypeUXCAccountCustomization, uxcARN, testRegion, "{}")

	orgAcctARN := fmt.Sprintf("arn:aws:organizations::%s:account/o-abc/%s", acct.ID, acct.ID)
	orgAcctID := upsertTestResource(t, st, "aws", acct.ID, TypeOrganizationsAccount, orgAcctARN, "",
		fmt.Sprintf(`{"Id":%q,"Arn":%q}`, acct.ID, orgAcctARN))

	if err := resolveUXCAccountCustomizationToOrgAccount(acct, st); err != nil {
		t.Fatalf("resolveUXCAccountCustomizationToOrgAccount: %v", err)
	}
	rels, err := st.RelationshipsFrom(uxcID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	assertRelationship(t, rels, uxcID, orgAcctID, store.RelAttachedTo)
}

// TestResolveUXCAccountCustomizationToOrgAccount_NoOrg verifies the resolver
// is a no-op when no aws:organizations:account row exists for the scanning
// account (standalone account, or org tree not scanned).
func TestResolveUXCAccountCustomizationToOrgAccount_NoOrg(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	uxcARN := fmt.Sprintf("arn:aws:uxc::%s:account-customization", acct.ID)
	uxcID := upsertTestResource(t, st, "aws", acct.ID, TypeUXCAccountCustomization, uxcARN, testRegion, "{}")

	if err := resolveUXCAccountCustomizationToOrgAccount(acct, st); err != nil {
		t.Fatalf("resolveUXCAccountCustomizationToOrgAccount: %v", err)
	}
	rels, err := st.RelationshipsFrom(uxcID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 0 {
		t.Errorf("expected 0 edges with no org account scanned, got %d", len(rels))
	}
}

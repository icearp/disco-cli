package aws

import (
	"fmt"
	"testing"

	"codeberg.org/icearp/disco/store"
)

const (
	testSESDomain    = "example.com"
	testSESConfigSet = "default-tracking"
)

// TestResolveSESEmailIdentityConfigSet_HappyPath verifies that the email
// identity links to its default configuration set when both are scanned.
func TestResolveSESEmailIdentityConfigSet_HappyPath(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	cfgARN := sesConfigurationSetARN(testRegion, testAccountID, testSESConfigSet)
	cfgID := upsertTestResource(t, st, "aws", acct.ID, TypeSESConfigurationSet, cfgARN, testRegion, "{}")

	identARN := sesEmailIdentityARN(testRegion, testAccountID, testSESDomain)
	identAttrs := fmt.Sprintf(`{"ConfigurationSetName":%q,"VerificationStatus":"SUCCESS"}`, testSESConfigSet)
	identID := upsertTestResource(t, st, "aws", acct.ID, TypeSESEmailIdentity, identARN, testRegion, identAttrs)

	if err := resolveSESEmailIdentityConfigSet(acct, st); err != nil {
		t.Fatalf("resolveSESEmailIdentityConfigSet: %v", err)
	}
	rels, err := st.RelationshipsFrom(identID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	assertRelationship(t, rels, identID, cfgID, store.RelUses)
}

// TestResolveSESEmailIdentityConfigSet_FKSafe verifies missing config-set
// targets skip without erroring.
func TestResolveSESEmailIdentityConfigSet_FKSafe(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	identARN := sesEmailIdentityARN(testRegion, testAccountID, testSESDomain)
	identAttrs := fmt.Sprintf(`{"ConfigurationSetName":%q}`, testSESConfigSet)
	identID := upsertTestResource(t, st, "aws", acct.ID, TypeSESEmailIdentity, identARN, testRegion, identAttrs)

	if err := resolveSESEmailIdentityConfigSet(acct, st); err != nil {
		t.Fatalf("resolveSESEmailIdentityConfigSet: %v", err)
	}
	rels, err := st.RelationshipsFrom(identID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 0 {
		t.Errorf("expected zero edges in FK-safe partial-coverage scan, got %d", len(rels))
	}
}

// TestResolveSESEmailIdentityConfigSet_MalformedAttrs ensures invalid attrs
// JSON skips the row rather than aborting the resolver.
func TestResolveSESEmailIdentityConfigSet_MalformedAttrs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	upsertTestResource(t, st, "aws", acct.ID, TypeSESEmailIdentity,
		sesEmailIdentityARN(testRegion, testAccountID, testSESDomain),
		testRegion, `not json`)

	if err := resolveSESEmailIdentityConfigSet(acct, st); err != nil {
		t.Fatalf("resolveSESEmailIdentityConfigSet: %v", err)
	}
}

// TestResolveSESEmailIdentityConfigSet_NoIdentityCfg verifies identities
// without a default config-set don't emit edges or errors.
func TestResolveSESEmailIdentityConfigSet_NoIdentityCfg(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	cfgARN := sesConfigurationSetARN(testRegion, testAccountID, testSESConfigSet)
	upsertTestResource(t, st, "aws", acct.ID, TypeSESConfigurationSet, cfgARN, testRegion, "{}")

	identARN := sesEmailIdentityARN(testRegion, testAccountID, testSESDomain)
	identID := upsertTestResource(t, st, "aws", acct.ID, TypeSESEmailIdentity, identARN, testRegion, `{"VerificationStatus":"SUCCESS"}`)

	if err := resolveSESEmailIdentityConfigSet(acct, st); err != nil {
		t.Fatalf("resolveSESEmailIdentityConfigSet: %v", err)
	}
	rels, err := st.RelationshipsFrom(identID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 0 {
		t.Errorf("expected zero edges, got %d", len(rels))
	}
}

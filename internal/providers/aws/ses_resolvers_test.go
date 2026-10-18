package aws

import (
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	sesv2types "github.com/aws/aws-sdk-go-v2/service/sesv2/types"
	"github.com/icearp/disco-cli/store"
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

// sesIdentityCertAttrs is the stored attributes of an association signing
// with certARN ("" for none).
func sesIdentityCertAttrs(t *testing.T, from, certARN string) string {
	t.Helper()
	c := sesv2types.IdentityCertificate{FromAddress: &from, Status: sesv2types.IdentityCertificateStatusActive}
	if certARN != "" {
		c.CertificateArn = &certARN
	}
	b, err := json.Marshal(c)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(b)
}

// An association links to its ACM certificate only when that certificate is
// scanned; one without a CertificateArn links to nothing.
func TestResolveSESEmailIdentityCertificateACM_ScannedCertOnly(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	certID := upsertTestResource(t, st, "aws", acct.ID, TypeACMCertificate, sesTestCertARN1, testRegion, "{}")
	alice := upsertTestResourceNamed(t, st, TypeSESEmailIdentityCertificate, sesTestAlice, testRegion,
		sesIdentityCertAttrs(t, "alice@example.com", sesTestCertARN1), "alice@example.com")
	bob := upsertTestResourceNamed(t, st, TypeSESEmailIdentityCertificate, sesTestBob, testRegion,
		sesIdentityCertAttrs(t, "bob@example.com", sesTestCertARN2), "bob@example.com")
	ops := upsertTestResourceNamed(t, st, TypeSESEmailIdentityCertificate, sesTestOps, testRegion,
		sesIdentityCertAttrs(t, sesTestAddress, ""), sesTestAddress)

	if err := resolveSESEmailIdentityCertificateACM(acct, st); err != nil {
		t.Fatalf("resolveSESEmailIdentityCertificateACM: %v", err)
	}
	rels, err := st.RelationshipsFrom(alice)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	assertRelationship(t, rels, alice, certID, store.RelUses)
	for _, id := range []string{bob, ops} {
		rels, err := st.RelationshipsFrom(id)
		if err != nil {
			t.Fatalf("RelationshipsFrom: %v", err)
		}
		if len(rels) != 0 {
			t.Errorf("edges from %s = %v, want none (unscanned or absent certificate)", id, rels)
		}
	}
}

// A row that fails to decode is returned as an error and costs a later row
// none of its edges.
func TestResolveSESEmailIdentityCertificateACM_DecodeErrorKeepsOtherRows(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	certID := upsertTestResource(t, st, "aws", acct.ID, TypeACMCertificate, sesTestCertARN1, testRegion, "{}")
	// Named to sort ahead of the good row, so the good row is visited after it.
	upsertTestResourceNamed(t, st, TypeSESEmailIdentityCertificate, sesTestAlice, testRegion, "not json", "a-bad")
	good := upsertTestResourceNamed(t, st, TypeSESEmailIdentityCertificate, sesTestBob, testRegion,
		sesIdentityCertAttrs(t, "bob@example.com", sesTestCertARN1), "b-good")

	err := resolveSESEmailIdentityCertificateACM(acct, st)
	var syntaxErr *json.SyntaxError
	if !errors.As(err, &syntaxErr) {
		t.Fatalf("err = %v, want a JSON decode error", err)
	}
	rels, rerr := st.RelationshipsFrom(good)
	if rerr != nil {
		t.Fatalf("RelationshipsFrom: %v", rerr)
	}
	assertRelationship(t, rels, good, certID, store.RelUses)
}

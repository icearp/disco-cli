package aws

import (
	"encoding/json"
	"errors"
	"testing"

	acmtypes "github.com/aws/aws-sdk-go-v2/service/acm/types"
	"github.com/icearp/disco-cli/store"
)

// TestResolveACMCertificateRelationships_PrivateCA verifies a PRIVATE cert
// emits a uses edge to its referenced Private CA.
func TestResolveACMCertificateRelationships_PrivateCA(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	certARN := "arn:aws:acm:us-east-1:" + testAccountID + ":certificate/abc"
	caARN := "arn:aws:acm-pca:us-east-1:" + testAccountID + ":certificate-authority/def"
	attrs := `{"Type":"PRIVATE","CertificateAuthorityArn":"` + caARN + `"}`

	certID := upsertTestResource(t, st, "aws", acct.ID, TypeACMCertificate, certARN, testRegion, attrs)
	caID := upsertTestResource(t, st, "aws", acct.ID, TypeACMPrivateCA, caARN, testRegion, "{}")

	if err := resolveACMCertificateRelationships(acct, st); err != nil {
		t.Fatalf("resolveACMCertificateRelationships: %v", err)
	}
	rels, err := st.RelationshipsFrom(certID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	assertRelationship(t, rels, certID, caID, store.RelUses)
}

// TestResolveACMCertificateRelationships_PublicNoEdge verifies public certs
// (no CertificateAuthorityArn) emit no edges.
func TestResolveACMCertificateRelationships_PublicNoEdge(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	certARN := "arn:aws:acm:us-east-1:" + testAccountID + ":certificate/public"
	certID := upsertTestResource(t, st, "aws", acct.ID, TypeACMCertificate, certARN, testRegion,
		`{"Type":"AMAZON_ISSUED"}`)

	if err := resolveACMCertificateRelationships(acct, st); err != nil {
		t.Fatalf("resolveACMCertificateRelationships: %v", err)
	}
	rels, _ := st.RelationshipsFrom(certID)
	if len(rels) != 0 {
		t.Errorf("expected 0 rels, got %d", len(rels))
	}
}

func acmEABAttrs(t *testing.T, role string) string {
	t.Helper()
	b, err := json.Marshal(acmtypes.AcmeExternalAccountBindingSummary{AcmeEndpointArn: ptrStr(acmTestEP1), RoleArn: ptrStr(role)})
	if err != nil {
		t.Fatalf("marshal binding: %v", err)
	}
	return string(b)
}

// A binding whose role was scanned uses it; one whose role was not, or that
// names no role, emits nothing.
func TestResolveACMAcmeExternalAccountBindingRoles_ScannedRoleOnly(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	roleID := upsertTestResource(t, st, "aws", acct.ID, TypeIAMRole, acmTestRole, "", "{}")
	withRole := upsertTestResource(t, st, "aws", acct.ID, TypeACMAcmeExternalAccountBinding, acmTestEAB1, testRegion, acmEABAttrs(t, acmTestRole))
	unscanned := upsertTestResource(t, st, "aws", acct.ID, TypeACMAcmeExternalAccountBinding, acmTestEAB2, testRegion,
		acmEABAttrs(t, "arn:aws:iam::123456789012:role/not-scanned"))
	noRole := upsertTestResource(t, st, "aws", acct.ID, TypeACMAcmeExternalAccountBinding, acmTestEP1+"/acme-external-account-binding/eab3", testRegion, "{}")

	if err := resolveACMAcmeExternalAccountBindingRoles(acct, st); err != nil {
		t.Fatalf("resolveACMAcmeExternalAccountBindingRoles: %v", err)
	}
	rels, err := st.RelationshipsFrom(withRole)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	assertRelationship(t, rels, withRole, roleID, store.RelUses)
	for _, id := range []string{unscanned, noRole} {
		if rels, err := st.RelationshipsFrom(id); err != nil || len(rels) != 0 {
			t.Errorf("RelationshipsFrom(%s) = %v, %v; want none", id, rels, err)
		}
	}
}

// A row whose attributes do not parse is reported, and the rows listed before
// and after it are still wired. Names a, b, c fix the listing order
// (ListResources orders by type, then name).
func TestResolveACMAcmeExternalAccountBindingRoles_BadAttrsReportedOthersWired(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	roleID := upsertTestResource(t, st, "aws", acct.ID, TypeIAMRole, acmTestRole, "", "{}")
	before := upsertTestResourceNamed(t, st, TypeACMAcmeExternalAccountBinding, acmTestEAB1, testRegion, acmEABAttrs(t, acmTestRole), "a")
	upsertTestResourceNamed(t, st, TypeACMAcmeExternalAccountBinding, acmTestEAB2, testRegion, `{"RoleArn":7}`, "b")
	after := upsertTestResourceNamed(t, st, TypeACMAcmeExternalAccountBinding, acmTestEP1+"/acme-external-account-binding/eab3", testRegion, acmEABAttrs(t, acmTestRole), "c")

	var typeErr *json.UnmarshalTypeError
	if err := resolveACMAcmeExternalAccountBindingRoles(acct, st); !errors.As(err, &typeErr) {
		t.Errorf("err = %v, want a JSON type error", err)
	}
	for _, id := range []string{before, after} {
		rels, err := st.RelationshipsFrom(id)
		if err != nil {
			t.Fatalf("RelationshipsFrom: %v", err)
		}
		assertRelationship(t, rels, id, roleID, store.RelUses)
	}
}

func acmAccountAttrs(t *testing.T, binding string) string {
	t.Helper()
	b, err := json.Marshal(acmtypes.AcmeAccountSummary{AccountUrl: ptrStr(acmTestURL1), AcmeExternalAccountBindingArn: ptrStr(binding)})
	if err != nil {
		t.Fatalf("marshal account: %v", err)
	}
	return string(b)
}

// An account whose binding was scanned uses it; one whose binding was not,
// or that names none, emits nothing.
func TestResolveACMAcmeAccountBindings_ScannedBindingOnly(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	eabID := upsertTestResource(t, st, "aws", acct.ID, TypeACMAcmeExternalAccountBinding, acmTestEAB1, testRegion, "{}")
	withBinding := upsertTestResource(t, st, "aws", acct.ID, TypeACMAcmeAccount, acmTestAccountID(acmTestEP1, acmTestURL1), testRegion, acmAccountAttrs(t, acmTestEAB1))
	unscanned := upsertTestResource(t, st, "aws", acct.ID, TypeACMAcmeAccount, acmTestAccountID(acmTestEP1, acmTestURL2), testRegion, acmAccountAttrs(t, acmTestEAB2))
	noBinding := upsertTestResource(t, st, "aws", acct.ID, TypeACMAcmeAccount, acmTestAccountID(acmTestEP2, acmTestURL1), testRegion, "{}")

	if err := resolveACMAcmeAccountBindings(acct, st); err != nil {
		t.Fatalf("resolveACMAcmeAccountBindings: %v", err)
	}
	rels, err := st.RelationshipsFrom(withBinding)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	assertRelationship(t, rels, withBinding, eabID, store.RelUses)
	for _, id := range []string{unscanned, noBinding} {
		if rels, err := st.RelationshipsFrom(id); err != nil || len(rels) != 0 {
			t.Errorf("RelationshipsFrom(%s) = %v, %v; want none", id, rels, err)
		}
	}
}

// A row whose attributes do not parse is reported, and the rows listed before
// and after it are still wired. Names a, b, c fix the listing order
// (ListResources orders by type, then name).
func TestResolveACMAcmeAccountBindings_BadAttrsReportedOthersWired(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	eabID := upsertTestResource(t, st, "aws", acct.ID, TypeACMAcmeExternalAccountBinding, acmTestEAB1, testRegion, "{}")
	before := upsertTestResourceNamed(t, st, TypeACMAcmeAccount, acmTestAccountID(acmTestEP1, acmTestURL1), testRegion, acmAccountAttrs(t, acmTestEAB1), "a")
	upsertTestResourceNamed(t, st, TypeACMAcmeAccount, acmTestAccountID(acmTestEP1, acmTestURL2), testRegion, `{"AcmeExternalAccountBindingArn":7}`, "b")
	after := upsertTestResourceNamed(t, st, TypeACMAcmeAccount, acmTestAccountID(acmTestEP2, acmTestURL1), testRegion, acmAccountAttrs(t, acmTestEAB1), "c")

	var typeErr *json.UnmarshalTypeError
	if err := resolveACMAcmeAccountBindings(acct, st); !errors.As(err, &typeErr) {
		t.Errorf("err = %v, want a JSON type error", err)
	}
	for _, id := range []string{before, after} {
		rels, err := st.RelationshipsFrom(id)
		if err != nil {
			t.Fatalf("RelationshipsFrom: %v", err)
		}
		assertRelationship(t, rels, id, eabID, store.RelUses)
	}
}

func TestResolveACMAcmeAccountBindings_NoAccounts(t *testing.T) {
	st := newTestStore(t)
	if err := resolveACMAcmeAccountBindings(newTestAccount(testAccountID), st); err != nil {
		t.Errorf("err = %v, want nil", err)
	}
}

func TestResolveACMAcmeExternalAccountBindingRoles_NoBindings(t *testing.T) {
	st := newTestStore(t)
	if err := resolveACMAcmeExternalAccountBindingRoles(newTestAccount(testAccountID), st); err != nil {
		t.Errorf("err = %v, want nil", err)
	}
}

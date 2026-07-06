package aws

import (
	"testing"

	"codeberg.org/icearp/disco/store"
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

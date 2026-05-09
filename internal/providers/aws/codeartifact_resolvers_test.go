package aws

import (
	"fmt"
	"testing"

	"codeberg.org/icearp/disco/store"
)

func TestResolveCodeArtifactDomainToKMS(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	kARN := fmt.Sprintf("arn:aws:kms:%s:%s:key/abc", testRegion, acct.ID)
	kID := upsertTestResource(t, st, "aws", acct.ID, TypeKMSKey, kARN, testRegion, "{}")
	dARN := codeArtifactDomainARN(testRegion, acct.ID, "d1")
	dID := upsertTestResource(t, st, "aws", acct.ID, TypeCodeArtifactDomain, dARN, testRegion, fmt.Sprintf(`{"EncryptionKey":"%s"}`, kARN))
	if err := resolveCodeArtifactDomainToKMS(acct, st); err != nil {
		t.Fatalf("resolveCodeArtifactDomainToKMS: %v", err)
	}
	rels, _ := st.RelationshipsFrom(dID)
	assertRelationship(t, rels, dID, kID, store.RelUses)
}

func TestResolveCodeArtifactChildrenToDomain(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	dARN := codeArtifactDomainARN(testRegion, acct.ID, "d1")
	dID := upsertTestResource(t, st, "aws", acct.ID, TypeCodeArtifactDomain, dARN, testRegion, "{}")
	rARN := fmt.Sprintf("arn:aws:codeartifact:%s:%s:repository/d1/r1", testRegion, acct.ID)
	rID := upsertTestResource(t, st, "aws", acct.ID, TypeCodeArtifactRepository, rARN, testRegion, `{"DomainName":"d1"}`)
	pgARN := fmt.Sprintf("arn:aws:codeartifact:%s:%s:package-group/d1/g1", testRegion, acct.ID)
	pgID := upsertTestResource(t, st, "aws", acct.ID, TypeCodeArtifactPackageGroup, pgARN, testRegion, `{"DomainName":"d1"}`)
	if err := resolveCodeArtifactChildrenToDomain(acct, st); err != nil {
		t.Fatalf("resolveCodeArtifactChildrenToDomain: %v", err)
	}
	for _, c := range []string{rID, pgID} {
		rels, _ := st.RelationshipsFrom(c)
		assertRelationship(t, rels, c, dID, store.RelAttachedTo)
	}
}

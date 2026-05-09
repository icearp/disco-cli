package aws

import (
	"fmt"
	"testing"

	"codeberg.org/icearp/disco/store"
)

func TestResolvePCACAdConnectorRefs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	caARN := fmt.Sprintf("arn:aws:acm-pca:%s:%s:certificate-authority/ca-1", testRegion, acct.ID)
	caID := upsertTestResource(t, st, "aws", acct.ID, TypeACMPrivateCA, caARN, testRegion, "{}")
	dARN := fmt.Sprintf("arn:aws:ds:%s:%s:directory/d-abc", testRegion, acct.ID)
	dID := upsertTestResource(t, st, "aws", acct.ID, TypeDSMicrosoftAD, dARN, testRegion, `{"DirectoryId":"d-abc"}`)
	sgARN := ec2ARN(testRegion, acct.ID, "security-group", "sg-1")
	sgID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2SecurityGroup, sgARN, testRegion, "{}")
	cARN := fmt.Sprintf("arn:aws:pca-connector-ad:%s:%s:connector/cn-1", testRegion, acct.ID)
	attrs := fmt.Sprintf(`{"CertificateAuthorityArn":"%s","DirectoryId":"d-abc","VpcInformation":{"SecurityGroupIds":["sg-1"]}}`, caARN)
	cID := upsertTestResource(t, st, "aws", acct.ID, TypePCAConnectorADConnector, cARN, testRegion, attrs)
	if err := resolvePCACAdConnectorRefs(acct, st); err != nil {
		t.Fatalf("resolvePCACAdConnectorRefs: %v", err)
	}
	rels, _ := st.RelationshipsFrom(cID)
	assertRelationship(t, rels, cID, caID, store.RelUses)
	assertRelationship(t, rels, cID, dID, store.RelAttachedTo)
	assertRelationship(t, rels, cID, sgID, store.RelUses)
}

func TestResolvePCACAdDirRegRefs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	dARN := fmt.Sprintf("arn:aws:ds:%s:%s:directory/d-abc", testRegion, acct.ID)
	dID := upsertTestResource(t, st, "aws", acct.ID, TypeDSMicrosoftAD, dARN, testRegion, `{"DirectoryId":"d-abc"}`)
	drARN := fmt.Sprintf("arn:aws:pca-connector-ad:%s:%s:directory-registration/d-abc", testRegion, acct.ID)
	drID := upsertTestResource(t, st, "aws", acct.ID, TypePCAConnectorADDirectoryRegistration, drARN, testRegion,
		`{"DirectoryId":"d-abc"}`)
	if err := resolvePCACAdDirRegRefs(acct, st); err != nil {
		t.Fatalf("resolvePCACAdDirRegRefs: %v", err)
	}
	rels, _ := st.RelationshipsFrom(drID)
	assertRelationship(t, rels, drID, dID, store.RelAttachedTo)
}

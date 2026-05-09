package aws

import (
	"fmt"
	"testing"

	"codeberg.org/icearp/disco/store"
)

func TestResolveB2BICapabilityS3(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	cARN := fmt.Sprintf("arn:aws:b2bi:%s:%s:capability/c-1", testRegion, acct.ID)
	bName := "edi-instructions"
	bARN := "arn:aws:s3:::" + bName
	attrs := fmt.Sprintf(`{"InstructionsDocuments":[{"BucketName":%q,"Key":"x.json"}]}`, bName)

	cID := upsertTestResource(t, st, "aws", acct.ID, TypeB2BICapability, cARN, testRegion, attrs)
	bID := upsertTestResource(t, st, "aws", acct.ID, TypeS3Bucket, bARN, testRegion, "{}")

	if err := resolveB2BICapabilityS3(acct, st); err != nil {
		t.Fatalf("resolveB2BICapabilityS3: %v", err)
	}
	rels, _ := st.RelationshipsFrom(cID)
	assertRelationship(t, rels, cID, bID, store.RelUses)
}

func TestResolveB2BIPartnershipRefs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	psARN := fmt.Sprintf("arn:aws:b2bi:%s:%s:partnership/ps-1", testRegion, acct.ID)
	profARN := fmt.Sprintf("arn:aws:b2bi:%s:%s:profile/p-1", testRegion, acct.ID)
	capARN := fmt.Sprintf("arn:aws:b2bi:%s:%s:capability/c-1", testRegion, acct.ID)
	attrs := `{"ProfileId":"p-1","Capabilities":["c-1"]}`

	psID := upsertTestResource(t, st, "aws", acct.ID, TypeB2BIPartnership, psARN, testRegion, attrs)
	pID := upsertTestResource(t, st, "aws", acct.ID, TypeB2BIProfile, profARN, testRegion, "{}")
	cID := upsertTestResource(t, st, "aws", acct.ID, TypeB2BICapability, capARN, testRegion, "{}")

	if err := resolveB2BIPartnershipRefs(acct, st); err != nil {
		t.Fatalf("resolveB2BIPartnershipRefs: %v", err)
	}
	rels, _ := st.RelationshipsFrom(psID)
	assertRelationship(t, rels, psID, pID, store.RelAttachedTo)
	assertRelationship(t, rels, psID, cID, store.RelUses)
}

func TestResolveB2BIProfileLogGroup(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	pARN := fmt.Sprintf("arn:aws:b2bi:%s:%s:profile/p-1", testRegion, acct.ID)
	lgName := "/aws/b2bi/profile/p-1"
	lgARN := logGroupNativeIDFromName(acct.ID, testRegion, lgName)
	attrs := fmt.Sprintf(`{"LogGroupName":%q}`, lgName)

	pID := upsertTestResource(t, st, "aws", acct.ID, TypeB2BIProfile, pARN, testRegion, attrs)
	lID := upsertTestResource(t, st, "aws", acct.ID, TypeLogsLogGroup, lgARN, testRegion, "{}")

	if err := resolveB2BIProfileLogGroup(acct, st); err != nil {
		t.Fatalf("resolveB2BIProfileLogGroup: %v", err)
	}
	rels, _ := st.RelationshipsFrom(pID)
	assertRelationship(t, rels, pID, lID, store.RelUses)
}

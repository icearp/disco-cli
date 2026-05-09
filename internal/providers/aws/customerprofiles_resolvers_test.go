package aws

import (
	"fmt"
	"testing"

	"codeberg.org/icearp/disco/store"
)

func TestCPDomainARNFromChild(t *testing.T) {
	cases := []struct{ in, want string }{
		{"arn:aws:profile:us-east-1:123:domains/d1/object-types/ot1", "arn:aws:profile:us-east-1:123:domains/d1"},
		{"arn:aws:profile:us-east-1:123:domains/d1", ""},
		{"", ""},
	}
	for _, c := range cases {
		if got := cpDomainARNFromChild(c.in); got != c.want {
			t.Errorf("cpDomainARNFromChild(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestResolveCustomerProfilesChildrenToDomain(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	dARN := fmt.Sprintf("arn:aws:profile:%s:%s:domains/d1", testRegion, acct.ID)
	dID := upsertTestResource(t, st, "aws", acct.ID, TypeCPDomain, dARN, testRegion, "{}")
	otARN := dARN + "/object-types/ot1"
	otID := upsertTestResource(t, st, "aws", acct.ID, TypeCPObjectType, otARN, testRegion, "{}")
	if err := resolveCustomerProfilesChildrenToDomain(acct, st); err != nil {
		t.Fatalf("resolveCustomerProfilesChildrenToDomain: %v", err)
	}
	rels, _ := st.RelationshipsFrom(otID)
	assertRelationship(t, rels, otID, dID, store.RelAttachedTo)
}

func TestResolveCPDomainRefs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	domARN := cpARN(testRegion, acct.ID, "dom1")
	keyARN := "arn:aws:kms:us-east-1:" + testAccountID + ":key/k-cp"
	dlqURL := fmt.Sprintf("https://sqs.us-east-1.amazonaws.com/%s/cp-dlq", testAccountID)
	dlqARN := "arn:aws:sqs:us-east-1:" + testAccountID + ":cp-dlq"
	attrs := `{"DefaultEncryptionKey":"` + keyARN + `","DeadLetterQueueUrl":"` + dlqURL + `"}`

	dID := upsertTestResource(t, st, "aws", acct.ID, TypeCPDomain, domARN, testRegion, attrs)
	kID := upsertTestResource(t, st, "aws", acct.ID, TypeKMSKey, keyARN, testRegion, "{}")
	qID := upsertTestResource(t, st, "aws", acct.ID, TypeSQSQueue, dlqARN, testRegion, "{}")

	if err := resolveCPDomainRefs(acct, st); err != nil {
		t.Fatalf("resolveCPDomainRefs: %v", err)
	}
	rels, _ := st.RelationshipsFrom(dID)
	assertRelationship(t, rels, dID, kID, store.RelUses)
	assertRelationship(t, rels, dID, qID, store.RelRoutesTo)
}

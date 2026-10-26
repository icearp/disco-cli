package aws

import (
	"fmt"
	"testing"

	cptypes "github.com/aws/aws-sdk-go-v2/service/customerprofiles/types"
	"github.com/icearp/disco-cli/store"
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

func TestResolveCustomerProfilesPluralChildrenToDomain(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	dARN := fmt.Sprintf("arn:aws:profile:%s:%s:domains/d1", testRegion, acct.ID)
	dID := upsertTestResource(t, st, "aws", acct.ID, TypeCPDomain, dARN, testRegion, "{}")

	cases := []struct {
		rtype, kind string
	}{
		{TypeCustomerProfilesLayouts, "layout"},
		{TypeCustomerProfilesDomainObjectTypes, "object-type"},
		{TypeCustomerProfilesRecommenderFilters, "recommender-filter"},
		{TypeCustomerProfilesRecommenderSchemas, "recommender-schema"},
	}
	childIDs := make([]string, len(cases))
	for i, c := range cases {
		arn := dARN + "/" + c.kind + "/x1"
		childIDs[i] = upsertTestResource(t, st, "aws", acct.ID, c.rtype, arn, testRegion, "{}")
	}

	if err := resolveCustomerProfilesChildrenToDomain(acct, st); err != nil {
		t.Fatalf("resolveCustomerProfilesChildrenToDomain: %v", err)
	}
	for i := range cases {
		rels, _ := st.RelationshipsFrom(childIDs[i])
		assertRelationship(t, rels, childIDs[i], dID, store.RelAttachedTo)
	}
}

func TestResolveCPDomainRefs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	domARN := cpARN("aws", testRegion, acct.ID, "dom1")
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

// TestResolveCPDomainRefs_ListDomainsElement pins that a domain stored as its
// ListDomains element (GetDomain denied or throttled) resolves without error
// and gets no edges, though a key and queue exist to point at.
func TestResolveCPDomainRefs_ListDomainsElement(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	attrs := mustJSON(cptypes.ListDomainItem{DomainName: ptrStr("dom1"), Tags: map[string]string{"team": "crm"}})
	dID := upsertTestResource(t, st, "aws", acct.ID, TypeCPDomain, cpARN("aws", testRegion, acct.ID, "dom1"), testRegion, attrs)
	upsertTestResource(t, st, "aws", acct.ID, TypeKMSKey, "arn:aws:kms:us-east-1:"+testAccountID+":key/k-cp", testRegion, "{}")
	upsertTestResource(t, st, "aws", acct.ID, TypeSQSQueue, "arn:aws:sqs:us-east-1:"+testAccountID+":cp-dlq", testRegion, "{}")

	if err := resolveCPDomainRefs(acct, st); err != nil {
		t.Fatalf("resolveCPDomainRefs: %v", err)
	}
	rels, err := st.RelationshipsFrom(dID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 0 {
		t.Errorf("edges = %+v, want none", rels)
	}
}

package aws

import (
	"fmt"
	"testing"

	"codeberg.org/icearp/disco/internal/store"
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

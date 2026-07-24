package aws

import (
	"fmt"
	"testing"

	"github.com/icearp/disco-cli/store"
)

func TestCasesDomainARNFromChild(t *testing.T) {
	cases := []struct{ in, want string }{
		{"arn:aws:cases:us-east-1:123:domain/d-1/case-rule/cr-1", "arn:aws:cases:us-east-1:123:domain/d-1"},
		{"arn:aws:cases:us-east-1:123:domain/d-1", ""},
		{"", ""},
	}
	for _, c := range cases {
		if got := casesDomainARNFromChild(c.in); got != c.want {
			t.Errorf("casesDomainARNFromChild(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestResolveCasesChildrenToDomain(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	dARN := fmt.Sprintf("arn:aws:cases:%s:%s:domain/d-1", testRegion, acct.ID)
	dID := upsertTestResource(t, st, "aws", acct.ID, TypeCasesDomain, dARN, testRegion, "{}")
	for _, kind := range []struct{ seg, typ string }{
		{"case-rule/cr-1", TypeCasesCaseRule},
		{"field/f-1", TypeCasesField},
		{"layout/l-1", TypeCasesLayout},
		{"template/t-1", TypeCasesTemplate},
	} {
		cARN := dARN + "/" + kind.seg
		cID := upsertTestResource(t, st, "aws", acct.ID, kind.typ, cARN, testRegion, "{}")
		if err := resolveCasesChildrenToDomain(acct, st); err != nil {
			t.Fatalf("resolveCasesChildrenToDomain: %v", err)
		}
		rels, _ := st.RelationshipsFrom(cID)
		assertRelationship(t, rels, cID, dID, store.RelAttachedTo)
	}
}

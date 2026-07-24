package aws

import (
	"testing"

	"github.com/icearp/disco-cli/store"
)

func TestResolveWAFv2Relationships(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	rgARN := "arn:aws:wafv2:us-east-1:" + acct.ID + ":regional/rulegroup/my-rg/abc123"
	ipARN := "arn:aws:wafv2:us-east-1:" + acct.ID + ":regional/ipset/my-ip/def456"
	aclARN := "arn:aws:wafv2:us-east-1:" + acct.ID + ":regional/webacl/my-acl/xyz789"

	aclAttrs := `{"Rules":[` +
		`{"Statement":{"RuleGroupReferenceStatement":{"ARN":"` + rgARN + `"}}},` +
		`{"Statement":{"IPSetReferenceStatement":{"ARN":"` + ipARN + `"}}}` +
		`]}`

	aclID := upsertTestResource(t, st, "aws", acct.ID, TypeWAFv2WebACL, aclARN, testRegion, aclAttrs)
	rgID := upsertTestResource(t, st, "aws", acct.ID, TypeWAFv2RuleGroup, rgARN, testRegion, "{}")
	ipID := upsertTestResource(t, st, "aws", acct.ID, TypeWAFv2IPSet, ipARN, testRegion, "{}")

	if err := resolveWAFv2Relationships(acct, st); err != nil {
		t.Fatalf("resolveWAFv2Relationships: %v", err)
	}

	rels, err := st.RelationshipsFrom(aclID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	assertRelationship(t, rels, aclID, rgID, store.RelUses)
	assertRelationship(t, rels, aclID, ipID, store.RelUses)
}

func TestResolveWAFv2Relationships_NoRules(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	aclARN := "arn:aws:wafv2:us-east-1:" + acct.ID + ":regional/webacl/empty/empty123"
	aclID := upsertTestResource(t, st, "aws", acct.ID, TypeWAFv2WebACL, aclARN, testRegion, "{}")

	if err := resolveWAFv2Relationships(acct, st); err != nil {
		t.Fatalf("resolveWAFv2Relationships: %v", err)
	}

	rels, err := st.RelationshipsFrom(aclID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 0 {
		t.Errorf("expected 0 relationships, got %d", len(rels))
	}
}

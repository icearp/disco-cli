package aws

import (
	"testing"

	"github.com/icearp/disco-cli/store"
)

func TestResolveRTBLinkRoutingRuleToLink(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	linkARN := rtbLinkARN(testRegion, acct.ID, "gw-1", "link-1")
	linkID := upsertTestResource(t, st, "aws", acct.ID, TypeRTBFabricLink, linkARN, testRegion, "{}")
	ruleARN := linkARN + "/routing-rule/rule-1"
	ruleID := upsertTestResource(t, st, "aws", acct.ID, TypeRTBFabricLinkRoutingRule, ruleARN, testRegion,
		`{"RuleId":"rule-1"}`)
	if err := resolveRTBLinkRoutingRuleToLink(acct, st); err != nil {
		t.Fatalf("resolveRTBLinkRoutingRuleToLink: %v", err)
	}
	rels, _ := st.RelationshipsFrom(ruleID)
	assertRelationship(t, rels, ruleID, linkID, store.RelAttachedTo)
}

func TestResolveRTBLinkRoutingRuleToLink_UnscannedLinkSkipped(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	linkARN := rtbLinkARN(testRegion, acct.ID, "gw-1", "missing")
	ruleARN := linkARN + "/routing-rule/rule-1"
	ruleID := upsertTestResource(t, st, "aws", acct.ID, TypeRTBFabricLinkRoutingRule, ruleARN, testRegion,
		`{"RuleId":"rule-1"}`)
	if err := resolveRTBLinkRoutingRuleToLink(acct, st); err != nil {
		t.Fatalf("resolveRTBLinkRoutingRuleToLink: %v", err)
	}
	rels, _ := st.RelationshipsFrom(ruleID)
	if len(rels) != 0 {
		t.Fatalf("expected no relationships, got %d", len(rels))
	}
}

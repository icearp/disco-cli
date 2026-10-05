package aws

import (
	"testing"

	"github.com/icearp/disco-cli/store"
)

func TestResolveBACGatewayChildParents(t *testing.T) {
	gw1 := bacARN(testRegion, testAccountID, "gateway", "gw-1")
	gw2 := bacARN(testRegion, testAccountID, "gateway", "gw-1x")
	cases := []struct {
		name      string
		resolve   func(*account, *store.Store) error
		childType string
		seg       string
	}{
		{"gateway rule", resolveBACGatewayRuleParent, TypeBedrockAgentCoreGatewayRule, "/rule/"},
		{"gateway rate limit", resolveBACGatewayRateLimitParent, TypeBedrockAgentCoreGatewayRateLimit, "/rate-limit/"},
	}
	for _, tc := range cases {
		// A second scanned gateway must not attract the edge: the parent is the
		// one whose NativeID prefixes the child's.
		t.Run(tc.name+"/parent scanned", func(t *testing.T) {
			st := newTestStore(t)
			acct := newTestAccount(testAccountID)
			parentID := upsertTestResource(t, st, "aws", acct.ID, TypeBedrockAgentCoreGateway, gw1, testRegion, `{}`)
			upsertTestResource(t, st, "aws", acct.ID, TypeBedrockAgentCoreGateway, gw2, testRegion, `{}`)
			childNativeID := gw1 + tc.seg + "x1"
			childID := upsertTestResource(t, st, "aws", acct.ID, tc.childType, childNativeID, testRegion, `{}`)
			if err := tc.resolve(acct, st); err != nil {
				t.Fatalf("resolve: %v", err)
			}
			rels, err := st.RelationshipsFrom(childID)
			if err != nil {
				t.Fatalf("RelationshipsFrom: %v", err)
			}
			if len(rels) != 1 || rels[0].ToID != parentID || rels[0].Kind != store.RelAttachedTo {
				t.Errorf("edges from %s = %+v; want exactly one attached-to → %s", childNativeID, rels, parentID)
			}
		})
		t.Run(tc.name+"/parent not scanned", func(t *testing.T) {
			st := newTestStore(t)
			acct := newTestAccount(testAccountID)
			upsertTestResource(t, st, "aws", acct.ID, TypeBedrockAgentCoreGateway, gw2, testRegion, `{}`)
			childNativeID := gw1 + tc.seg + "x1"
			childID := upsertTestResource(t, st, "aws", acct.ID, tc.childType, childNativeID, testRegion, `{}`)
			if err := tc.resolve(acct, st); err != nil {
				t.Fatalf("resolve: %v", err)
			}
			rels, err := st.RelationshipsFrom(childID)
			if err != nil {
				t.Fatalf("RelationshipsFrom: %v", err)
			}
			if len(rels) != 0 {
				t.Errorf("edges from %s = %+v; want none (parent unscanned)", childNativeID, rels)
			}
		})
	}
}

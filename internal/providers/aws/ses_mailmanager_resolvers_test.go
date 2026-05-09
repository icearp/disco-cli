package aws

import (
	"fmt"
	"testing"

	"codeberg.org/icearp/disco/store"
)

func TestResolveMMIngressPointRefs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	rsARN := mmARN(testRegion, acct.ID, "rule-set", "rs-1")
	rsID := upsertTestResource(t, st, "aws", acct.ID, TypeSESMailManagerRuleSet, rsARN, testRegion, "{}")
	tpARN := mmARN(testRegion, acct.ID, "traffic-policy", "tp-1")
	tpID := upsertTestResource(t, st, "aws", acct.ID, TypeSESMailManagerTrafficPolicy, tpARN, testRegion, "{}")
	ipARN := mmARN(testRegion, acct.ID, "ingress-point", "ip-1")
	ipID := upsertTestResource(t, st, "aws", acct.ID, TypeSESMailManagerIngressPoint, ipARN, testRegion,
		`{"RuleSetId":"rs-1","TrafficPolicyId":"tp-1"}`)
	if err := resolveMMIngressPointRefs(acct, st); err != nil {
		t.Fatalf("resolveMMIngressPointRefs: %v", err)
	}
	rels, _ := st.RelationshipsFrom(ipID)
	assertRelationship(t, rels, ipID, rsID, store.RelAttachedTo)
	assertRelationship(t, rels, ipID, tpID, store.RelAttachedTo)
}

func TestResolveMMArchiveKMS(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	keyARN := fmt.Sprintf("arn:aws:kms:%s:%s:key/k-1", testRegion, acct.ID)
	keyID := upsertTestResource(t, st, "aws", acct.ID, TypeKMSKey, keyARN, testRegion, "{}")
	aARN := mmARN(testRegion, acct.ID, "archive", "a-1")
	aID := upsertTestResource(t, st, "aws", acct.ID, TypeSESMailManagerArchive, aARN, testRegion,
		fmt.Sprintf(`{"KmsKeyArn":"%s"}`, keyARN))
	if err := resolveMMArchiveKMS(acct, st); err != nil {
		t.Fatalf("resolveMMArchiveKMS: %v", err)
	}
	rels, _ := st.RelationshipsFrom(aID)
	assertRelationship(t, rels, aID, keyID, store.RelUses)
}

func TestResolveMMAddonInstanceToSubscription(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	subARN := fmt.Sprintf("arn:aws:ses:%s:%s:mailmanager-addon-subscription/sub-1", testRegion, acct.ID)
	subID := upsertTestResource(t, st, "aws", acct.ID, TypeSESMailManagerAddonSubscription, subARN, testRegion,
		`{"AddonSubscriptionId":"sub-1"}`)
	aiARN := fmt.Sprintf("arn:aws:ses:%s:%s:mailmanager-addon-instance/ai-1", testRegion, acct.ID)
	aiID := upsertTestResource(t, st, "aws", acct.ID, TypeSESMailManagerAddonInstance, aiARN, testRegion,
		`{"AddonInstanceId":"ai-1","AddonSubscriptionId":"sub-1"}`)
	if err := resolveMMAddonInstanceToSubscription(acct, st); err != nil {
		t.Fatalf("resolveMMAddonInstanceToSubscription: %v", err)
	}
	rels, _ := st.RelationshipsFrom(aiID)
	assertRelationship(t, rels, aiID, subID, store.RelAttachedTo)
}

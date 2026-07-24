package aws

import (
	"fmt"
	"testing"

	"github.com/icearp/disco-cli/store"
)

func TestResolvePaymentCryptographyAliasToKey(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	kARN := fmt.Sprintf("arn:aws:payment-cryptography:%s:%s:key/k-1", testRegion, acct.ID)
	kID := upsertTestResource(t, st, "aws", acct.ID, TypePaymentCryptographyKey, kARN, testRegion, "{}")
	aARN := fmt.Sprintf("arn:aws:payment-cryptography:%s:%s:alias/myalias", testRegion, acct.ID)
	aID := upsertTestResource(t, st, "aws", acct.ID, TypePaymentCryptographyAlias, aARN, testRegion,
		fmt.Sprintf(`{"AliasName":"alias/myalias","KeyArn":"%s"}`, kARN))
	if err := resolvePaymentCryptographyAliasToKey(acct, st); err != nil {
		t.Fatalf("resolvePaymentCryptographyAliasToKey: %v", err)
	}
	rels, _ := st.RelationshipsFrom(aID)
	assertRelationship(t, rels, aID, kID, store.RelAttachedTo)
}

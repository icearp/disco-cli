package aws

import (
	"fmt"
	"testing"

	"codeberg.org/icearp/disco/store"
)

func TestResolveMQConfigurationAssociationRefs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	bARN := fmt.Sprintf("arn:aws:mq:%s:%s:broker:b-1", testRegion, acct.ID)
	bID := upsertTestResource(t, st, "aws", acct.ID, TypeMQBroker, bARN, testRegion, "{}")
	cARN := fmt.Sprintf("arn:aws:mq:%s:%s:configuration:c-1", testRegion, acct.ID)
	cID := upsertTestResource(t, st, "aws", acct.ID, TypeMQConfiguration, cARN, testRegion, "{}")
	aARN := bARN + "/configuration-association/c-1"
	aID := upsertTestResource(t, st, "aws", acct.ID, TypeMQConfigurationAssociation, aARN, testRegion,
		fmt.Sprintf(`{"Broker":"%s","Configuration":"c-1"}`, bARN))
	if err := resolveMQConfigurationAssociationRefs(acct, st); err != nil {
		t.Fatalf("resolveMQConfigurationAssociationRefs: %v", err)
	}
	rels, _ := st.RelationshipsFrom(aID)
	assertRelationship(t, rels, aID, bID, store.RelAttachedTo)
	assertRelationship(t, rels, aID, cID, store.RelUses)
}

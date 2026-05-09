package aws

import (
	"fmt"
	"testing"

	"codeberg.org/icearp/disco/store"
)

func TestResolveWAFv2LoggingConfigToWebACL(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	waARN := fmt.Sprintf("arn:aws:wafv2:%s:%s:regional/webacl/wa-1/uuid", testRegion, acct.ID)
	waID := upsertTestResource(t, st, "aws", acct.ID, TypeWAFv2WebACL, waARN, testRegion, "{}")
	lcID := upsertTestResource(t, st, "aws", acct.ID, TypeWAFv2LoggingConfiguration, waARN, testRegion, "{}")
	if err := resolveWAFv2LoggingConfigToWebACL(acct, st); err != nil {
		t.Fatalf("resolveWAFv2LoggingConfigToWebACL: %v", err)
	}
	rels, _ := st.RelationshipsFrom(lcID)
	assertRelationship(t, rels, lcID, waID, store.RelAttachedTo)
}

func TestResolveWAFv2WebACLAssociationRefs_ALB(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	waARN := fmt.Sprintf("arn:aws:wafv2:%s:%s:regional/webacl/wa-1/uuid", testRegion, acct.ID)
	waID := upsertTestResource(t, st, "aws", acct.ID, TypeWAFv2WebACL, waARN, testRegion, "{}")
	albARN := fmt.Sprintf("arn:aws:elasticloadbalancing:%s:%s:loadbalancer/app/my-alb/abc1234", testRegion, acct.ID)
	albID := upsertTestResource(t, st, "aws", acct.ID, TypeELBv2LoadBalancer, albARN, testRegion, "{}")
	assocARN := waARN + "/association/" + albARN
	assocID := upsertTestResource(t, st, "aws", acct.ID, TypeWAFv2WebACLAssociation, assocARN, testRegion, "{}")
	if err := resolveWAFv2WebACLAssociationRefs(acct, st); err != nil {
		t.Fatalf("resolveWAFv2WebACLAssociationRefs: %v", err)
	}
	rels, _ := st.RelationshipsFrom(assocID)
	assertRelationship(t, rels, assocID, waID, store.RelAttachedTo)
	assertRelationship(t, rels, assocID, albID, store.RelAttachedTo)
}

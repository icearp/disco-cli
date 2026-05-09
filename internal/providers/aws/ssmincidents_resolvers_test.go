package aws

import (
	"fmt"
	"testing"

	"codeberg.org/icearp/disco/store"
)

func TestResolveSSMIRSetKMS(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	keyARN := fmt.Sprintf("arn:aws:kms:%s:%s:key/k-1", testRegion, acct.ID)
	keyID := upsertTestResource(t, st, "aws", acct.ID, TypeKMSKey, keyARN, testRegion, "{}")
	rsARN := fmt.Sprintf("arn:aws:ssm-incidents::%s:replication-set/r-1", acct.ID)
	attrs := fmt.Sprintf(`{"RegionMap":{"%s":{"SseKmsKeyId":"%s"}}}`, testRegion, keyARN)
	rsID := upsertTestResource(t, st, "aws", acct.ID, TypeSSMIncidentsReplicationSet, rsARN, testRegion, attrs)
	if err := resolveSSMIRSetKMS(acct, st); err != nil {
		t.Fatalf("resolveSSMIRSetKMS: %v", err)
	}
	rels, _ := st.RelationshipsFrom(rsID)
	assertRelationship(t, rels, rsID, keyID, store.RelUses)
}

func TestResolveSSMIResponsePlanRefs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	planRPARN := "arn:aws:ssm-incidents::" + testAccountID + ":response-plan/critical"
	contactARN := "arn:aws:ssm-contacts:us-east-1:" + testAccountID + ":contact/oncall"
	escARN := "arn:aws:ssm-contacts:us-east-1:" + testAccountID + ":contact/escalation-tier1"
	attrs := fmt.Sprintf(`{"Engagements":[%q,%q]}`, contactARN, escARN)

	rpID := upsertTestResource(t, st, "aws", acct.ID, TypeSSMIncidentsResponsePlan, planRPARN, testRegion, attrs)
	cID := upsertTestResource(t, st, "aws", acct.ID, TypeSSMContactsContact, contactARN, testRegion, "{}")
	pID := upsertTestResource(t, st, "aws", acct.ID, TypeSSMContactsPlan, escARN, testRegion, "{}")

	if err := resolveSSMIResponsePlanRefs(acct, st); err != nil {
		t.Fatalf("resolveSSMIResponsePlanRefs: %v", err)
	}
	rels, _ := st.RelationshipsFrom(rpID)
	assertRelationship(t, rels, rpID, cID, store.RelUses)
	assertRelationship(t, rels, rpID, pID, store.RelUses)
}

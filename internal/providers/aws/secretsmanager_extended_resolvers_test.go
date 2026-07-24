package aws

import (
	"fmt"
	"testing"

	"github.com/icearp/disco-cli/store"
)

func TestResolveSecretsManagerResourcePolicyToSecret(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	sARN := fmt.Sprintf("arn:aws:secretsmanager:%s:%s:secret:s1-AbC", testRegion, acct.ID)
	sID := upsertTestResource(t, st, "aws", acct.ID, TypeSecretsManagerSecret, sARN, testRegion, "{}")
	pID := upsertTestResource(t, st, "aws", acct.ID, TypeSecretsManagerResourcePolicy, sARN+"/policy", testRegion, "{}")
	if err := resolveSecretsManagerResourcePolicyToSecret(acct, st); err != nil {
		t.Fatalf("resolveSecretsManagerResourcePolicyToSecret: %v", err)
	}
	rels, _ := st.RelationshipsFrom(pID)
	assertRelationship(t, rels, pID, sID, store.RelAttachedTo)
}

func TestResolveSecretsManagerRotationScheduleRefs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	sARN := fmt.Sprintf("arn:aws:secretsmanager:%s:%s:secret:s1-AbC", testRegion, acct.ID)
	sID := upsertTestResource(t, st, "aws", acct.ID, TypeSecretsManagerSecret, sARN, testRegion, "{}")
	fnARN := fmt.Sprintf("arn:aws:lambda:%s:%s:function:rotator", testRegion, acct.ID)
	fnID := upsertTestResource(t, st, "aws", acct.ID, TypeLambdaFunction, fnARN, testRegion, "{}")
	rsARN := sARN + "/rotation-schedule"
	rsID := upsertTestResource(t, st, "aws", acct.ID, TypeSecretsManagerRotationSchedule, rsARN, testRegion,
		fmt.Sprintf(`{"SecretId":"%s","RotationLambdaARN":"%s"}`, sARN, fnARN))
	if err := resolveSecretsManagerRotationScheduleRefs(acct, st); err != nil {
		t.Fatalf("resolveSecretsManagerRotationScheduleRefs: %v", err)
	}
	rels, _ := st.RelationshipsFrom(rsID)
	assertRelationship(t, rels, rsID, sID, store.RelAttachedTo)
	assertRelationship(t, rels, rsID, fnID, store.RelUses)
}

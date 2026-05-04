package aws

import (
	"fmt"
	"testing"

	"codeberg.org/icearp/disco/internal/store"
)

func TestResolveFISExperimentTemplateRefs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	roleARN := fmt.Sprintf("arn:aws:iam::%s:role/fis-role", acct.ID)
	roleID := upsertTestResource(t, st, "aws", acct.ID, TypeIAMRole, roleARN, "", "{}")
	tplARN := fmt.Sprintf("arn:aws:fis:%s:%s:experiment-template/EXT123", testRegion, acct.ID)
	tplID := upsertTestResource(t, st, "aws", acct.ID, TypeFISExperimentTemplate, tplARN, testRegion,
		fmt.Sprintf(`{"RoleArn":"%s"}`, roleARN))
	if err := resolveFISExperimentTemplateRefs(acct, st); err != nil {
		t.Fatalf("resolveFISExperimentTemplateRefs: %v", err)
	}
	rels, _ := st.RelationshipsFrom(tplID)
	assertRelationship(t, rels, tplID, roleID, store.RelUses)
}

func TestResolveFISTargetAccountConfigRefs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	tplARN := fmt.Sprintf("arn:aws:fis:%s:%s:experiment-template/EXT123", testRegion, acct.ID)
	tplID := upsertTestResource(t, st, "aws", acct.ID, TypeFISExperimentTemplate, tplARN, testRegion, "{}")
	roleARN := fmt.Sprintf("arn:aws:iam::%s:role/target-role", acct.ID)
	roleID := upsertTestResource(t, st, "aws", acct.ID, TypeIAMRole, roleARN, "", "{}")
	tacARN := tplARN + "/target-account-configuration/210987654321"
	tacID := upsertTestResource(t, st, "aws", acct.ID, TypeFISTargetAccountConfiguration, tacARN, testRegion,
		fmt.Sprintf(`{"AccountId":"210987654321","RoleArn":"%s"}`, roleARN))
	if err := resolveFISTargetAccountConfigRefs(acct, st); err != nil {
		t.Fatalf("resolveFISTargetAccountConfigRefs: %v", err)
	}
	rels, _ := st.RelationshipsFrom(tacID)
	assertRelationship(t, rels, tacID, tplID, store.RelAttachedTo)
	assertRelationship(t, rels, tacID, roleID, store.RelUses)
}

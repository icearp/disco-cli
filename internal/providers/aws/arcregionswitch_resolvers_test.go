package aws

import (
	"testing"

	"github.com/icearp/disco-cli/store"
)

func TestResolveARCRegionSwitchPlanRole(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	planARN := "arn:aws:arc-region-switch::" + testAccountID + ":plan/myPlan"
	roleARN := "arn:aws:iam::" + testAccountID + ":role/arc-exec"
	attrs := `{"ExecutionRole":"` + roleARN + `"}`

	pID := upsertTestResource(t, st, "aws", acct.ID, TypeARCRegionSwitchPlan, planARN, testRegion, attrs)
	rID := upsertTestResource(t, st, "aws", acct.ID, TypeIAMRole, roleARN, testRegion, "{}")

	if err := resolveARCRegionSwitchRelationships(acct, st); err != nil {
		t.Fatalf("resolveARCRegionSwitchRelationships: %v", err)
	}
	rels, _ := st.RelationshipsFrom(pID)
	assertRelationship(t, rels, pID, rID, store.RelAssumes)
}

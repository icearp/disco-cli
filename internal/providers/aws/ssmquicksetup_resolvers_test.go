package aws

import (
	"fmt"
	"testing"

	"github.com/icearp/disco-cli/store"
)

func TestResolveSSMQuickSetupConfigManagerRoles(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	mgrARN := "arn:aws:ssm-quicksetup:us-east-1:" + testAccountID + ":configuration-manager/abc123"
	roleARN := "arn:aws:iam::" + testAccountID + ":role/qs-admin"
	attrs := fmt.Sprintf(`{"ConfigurationDefinitions":[{"LocalDeploymentAdministrationRoleArn":%q}]}`, roleARN)

	mID := upsertTestResource(t, st, "aws", acct.ID, TypeSSMQuickSetupConfigurationManager, mgrARN, testRegion, attrs)
	rID := upsertTestResource(t, st, "aws", acct.ID, TypeIAMRole, roleARN, testRegion, "{}")

	if err := resolveSSMQuickSetupConfigManagerRoles(acct, st); err != nil {
		t.Fatalf("resolveSSMQuickSetupConfigManagerRoles: %v", err)
	}
	rels, _ := st.RelationshipsFrom(mID)
	assertRelationship(t, rels, mID, rID, store.RelAssumes)
}

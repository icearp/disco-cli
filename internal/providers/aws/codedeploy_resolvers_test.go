package aws

import (
	"fmt"
	"testing"

	"codeberg.org/icearp/disco/store"
)

func TestResolveCodeDeployDGRefs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	appARN := codeDeployARN(testRegion, acct.ID, "application", "myApp")
	appID := upsertTestResource(t, st, "aws", acct.ID, TypeCodeDeployApplication, appARN, testRegion, "{}")
	dcARN := codeDeployARN(testRegion, acct.ID, "deploymentconfig", "OneAtATime")
	dcID := upsertTestResource(t, st, "aws", acct.ID, TypeCodeDeployDeploymentConfig, dcARN, testRegion, "{}")
	roleARN := fmt.Sprintf("arn:aws:iam::%s:role/cd-svc", acct.ID)
	roleID := upsertTestResource(t, st, "aws", acct.ID, TypeIAMRole, roleARN, "", "{}")
	dgARN := fmt.Sprintf("arn:aws:codedeploy:%s:%s:deploymentgroup:myApp/dg1", testRegion, acct.ID)
	attrs := fmt.Sprintf(`{"ApplicationName":"myApp","DeploymentConfigName":"OneAtATime","ServiceRoleArn":"%s"}`, roleARN)
	dgID := upsertTestResource(t, st, "aws", acct.ID, TypeCodeDeployDeploymentGroup, dgARN, testRegion, attrs)
	if err := resolveCodeDeployDGRefs(acct, st); err != nil {
		t.Fatalf("resolveCodeDeployDGRefs: %v", err)
	}
	rels, _ := st.RelationshipsFrom(dgID)
	assertRelationship(t, rels, dgID, appID, store.RelAttachedTo)
	assertRelationship(t, rels, dgID, dcID, store.RelUses)
	assertRelationship(t, rels, dgID, roleID, store.RelUses)
}

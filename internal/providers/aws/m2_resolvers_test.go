package aws

import (
	"fmt"
	"testing"

	"codeberg.org/icearp/disco/internal/store"
)

func TestResolveM2DeploymentRefs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	appARN := fmt.Sprintf("arn:aws:m2:%s:%s:app/app-1", testRegion, acct.ID)
	appID := upsertTestResource(t, st, "aws", acct.ID, TypeM2Application, appARN, testRegion, "{}")
	envARN := fmt.Sprintf("arn:aws:m2:%s:%s:env/env-1", testRegion, acct.ID)
	envID := upsertTestResource(t, st, "aws", acct.ID, TypeM2Environment, envARN, testRegion, "{}")
	depARN := appARN + "/deployment/d-1"
	depID := upsertTestResource(t, st, "aws", acct.ID, TypeM2Deployment, depARN, testRegion, `{"EnvironmentId":"env-1"}`)
	if err := resolveM2DeploymentRefs(acct, st); err != nil {
		t.Fatalf("resolveM2DeploymentRefs: %v", err)
	}
	rels, _ := st.RelationshipsFrom(depID)
	assertRelationship(t, rels, depID, appID, store.RelAttachedTo)
	assertRelationship(t, rels, depID, envID, store.RelUses)
}

package aws

import (
	"fmt"
	"testing"

	"codeberg.org/icearp/disco/internal/store"
)

func TestResolveRefactorSpacesHierarchy(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	envARN := fmt.Sprintf("arn:aws:refactor-spaces:%s:%s:environment/env-1", testRegion, acct.ID)
	envID := upsertTestResource(t, st, "aws", acct.ID, TypeRefactorSpacesEnvironment, envARN, testRegion, "{}")
	appARN := envARN + "/application/app-1"
	appID := upsertTestResource(t, st, "aws", acct.ID, TypeRefactorSpacesApplication, appARN, testRegion, "{}")
	svcARN := appARN + "/service/svc-1"
	svcID := upsertTestResource(t, st, "aws", acct.ID, TypeRefactorSpacesService, svcARN, testRegion, "{}")
	routeARN := appARN + "/route/r-1"
	routeID := upsertTestResource(t, st, "aws", acct.ID, TypeRefactorSpacesRoute, routeARN, testRegion, "{}")
	if err := resolveRefactorSpacesHierarchy(acct, st); err != nil {
		t.Fatalf("resolveRefactorSpacesHierarchy: %v", err)
	}
	rels, _ := st.RelationshipsFrom(appID)
	assertRelationship(t, rels, appID, envID, store.RelAttachedTo)
	rels, _ = st.RelationshipsFrom(svcID)
	assertRelationship(t, rels, svcID, appID, store.RelAttachedTo)
	rels, _ = st.RelationshipsFrom(routeID)
	assertRelationship(t, rels, routeID, appID, store.RelAttachedTo)
}

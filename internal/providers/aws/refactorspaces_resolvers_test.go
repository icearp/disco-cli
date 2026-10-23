package aws

import (
	"fmt"
	"testing"

	"github.com/icearp/disco-cli/store"
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
	for _, e := range [][2]string{{appID, envID}, {svcID, appID}, {routeID, appID}} {
		rels, err := st.RelationshipsFrom(e[0])
		if err != nil {
			t.Fatalf("RelationshipsFrom(%s): %v", e[0], err)
		}
		assertRelationship(t, rels, e[0], e[1], store.RelAttachedTo)
	}
}

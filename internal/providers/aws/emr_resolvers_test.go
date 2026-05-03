package aws

import (
	"fmt"
	"testing"

	"codeberg.org/icearp/disco/internal/store"
)

func TestResolveEMRChildrenToCluster(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	clARN := emrARN(testRegion, acct.ID, "cluster", "j-ABC")
	clID := upsertTestResource(t, st, "aws", acct.ID, TypeEMRCluster, clARN, testRegion, "{}")
	stepARN := clARN + "/step/s-1"
	stepID := upsertTestResource(t, st, "aws", acct.ID, TypeEMRStep, stepARN, testRegion, "{}")
	ifARN := clARN + "/instance-fleet/if-1"
	ifID := upsertTestResource(t, st, "aws", acct.ID, TypeEMRInstanceFleet, ifARN, testRegion, "{}")
	igARN := clARN + "/instance-group/ig-1"
	igID := upsertTestResource(t, st, "aws", acct.ID, TypeEMRInstanceGroup, igARN, testRegion, "{}")

	if err := resolveEMRChildrenToCluster(acct, st); err != nil {
		t.Fatalf("resolveEMRChildrenToCluster: %v", err)
	}
	for _, child := range []string{stepID, ifID, igID} {
		rels, _ := st.RelationshipsFrom(child)
		assertRelationship(t, rels, child, clID, store.RelAttachedTo)
	}
}

func TestResolveEMRStudioSessionMappingToStudio(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	stARN := emrARN(testRegion, acct.ID, "studio", "es-1")
	stID := upsertTestResource(t, st, "aws", acct.ID, TypeEMRStudio, stARN, testRegion, "{}")
	smARN := fmt.Sprintf("%s/identity/u-1", stARN)
	smID := upsertTestResource(t, st, "aws", acct.ID, TypeEMRStudioSessionMapping, smARN, testRegion, "{}")
	if err := resolveEMRStudioSessionMappingToStudio(acct, st); err != nil {
		t.Fatalf("resolveEMRStudioSessionMappingToStudio: %v", err)
	}
	rels, _ := st.RelationshipsFrom(smID)
	assertRelationship(t, rels, smID, stID, store.RelAttachedTo)
}

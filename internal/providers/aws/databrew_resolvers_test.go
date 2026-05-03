package aws

import (
	"fmt"
	"testing"

	"codeberg.org/icearp/disco/internal/store"
)

func TestResolveDataBrewRefs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	dsARN := dbrARN(testRegion, acct.ID, "dataset", "ds1")
	dsID := upsertTestResource(t, st, "aws", acct.ID, TypeDataBrewDataset, dsARN, testRegion, "{}")
	rcpARN := dbrARN(testRegion, acct.ID, "recipe", "r1")
	rcpID := upsertTestResource(t, st, "aws", acct.ID, TypeDataBrewRecipe, rcpARN, testRegion, `{"ProjectName":"p1"}`)
	prjARN := dbrARN(testRegion, acct.ID, "project", "p1")
	prjID := upsertTestResource(t, st, "aws", acct.ID, TypeDataBrewProject, prjARN, testRegion,
		`{"DatasetName":"ds1","RecipeName":"r1"}`)
	roleARN := fmt.Sprintf("arn:aws:iam::%s:role/dbr", acct.ID)
	roleID := upsertTestResource(t, st, "aws", acct.ID, TypeIAMRole, roleARN, "", "{}")
	jobARN := dbrARN(testRegion, acct.ID, "job", "j1")
	jobID := upsertTestResource(t, st, "aws", acct.ID, TypeDataBrewJob, jobARN, testRegion,
		fmt.Sprintf(`{"DatasetName":"ds1","ProjectName":"p1","RoleArn":"%s"}`, roleARN))
	schARN := dbrARN(testRegion, acct.ID, "schedule", "s1")
	schID := upsertTestResource(t, st, "aws", acct.ID, TypeDataBrewSchedule, schARN, testRegion, `{"JobNames":["j1"]}`)

	if err := resolveDataBrewRefs(acct, st); err != nil {
		t.Fatalf("resolveDataBrewRefs: %v", err)
	}
	rels, _ := st.RelationshipsFrom(jobID)
	assertRelationship(t, rels, jobID, dsID, store.RelUses)
	assertRelationship(t, rels, jobID, prjID, store.RelUses)
	assertRelationship(t, rels, jobID, roleID, store.RelUses)
	rels, _ = st.RelationshipsFrom(prjID)
	assertRelationship(t, rels, prjID, dsID, store.RelUses)
	assertRelationship(t, rels, prjID, rcpID, store.RelUses)
	rels, _ = st.RelationshipsFrom(rcpID)
	assertRelationship(t, rels, rcpID, prjID, store.RelAttachedTo)
	rels, _ = st.RelationshipsFrom(schID)
	assertRelationship(t, rels, schID, jobID, store.RelUses)
}

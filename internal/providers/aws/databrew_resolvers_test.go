package aws

import (
	"fmt"
	"testing"

	databrewtypes "github.com/aws/aws-sdk-go-v2/service/databrew/types"
	"github.com/icearp/disco-cli/store"
)

// dbRels returns the relationships from id, failing the test on a read error.
func dbRels(t *testing.T, st *store.Store, id string) []store.Relationship {
	t.Helper()
	rels, err := st.RelationshipsFrom(id)
	if err != nil {
		t.Fatalf("RelationshipsFrom(%s): %v", id, err)
	}
	return rels
}

func TestResolveDataBrewRefs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	dsARN := dbrARN(testRegion, acct.ID, "dataset", "ds1")
	dsID := upsertTestResource(t, st, "aws", acct.ID, TypeDataBrewDataset, dsARN, testRegion, "{}")
	rcpARN := dbrARN(testRegion, acct.ID, "recipe", "r1")
	rcpID := upsertTestResource(t, st, "aws", acct.ID, TypeDataBrewRecipe, rcpARN, testRegion,
		mustJSON(databrewtypes.Recipe{ProjectName: ptrStr("p1")}))
	prjARN := dbrARN(testRegion, acct.ID, "project", "p1")
	prjID := upsertTestResource(t, st, "aws", acct.ID, TypeDataBrewProject, prjARN, testRegion,
		mustJSON(databrewtypes.Project{DatasetName: ptrStr("ds1"), RecipeName: ptrStr("r1")}))
	roleARN := fmt.Sprintf("arn:aws:iam::%s:role/dbr", acct.ID)
	roleID := upsertTestResource(t, st, "aws", acct.ID, TypeIAMRole, roleARN, "", "{}")
	jobARN := dbrARN(testRegion, acct.ID, "job", "j1")
	jobID := upsertTestResource(t, st, "aws", acct.ID, TypeDataBrewJob, jobARN, testRegion,
		mustJSON(databrewtypes.Job{DatasetName: ptrStr("ds1"), ProjectName: ptrStr("p1"), RoleArn: ptrStr(roleARN)}))
	schARN := dbrARN(testRegion, acct.ID, "schedule", "s1")
	schID := upsertTestResource(t, st, "aws", acct.ID, TypeDataBrewSchedule, schARN, testRegion,
		mustJSON(databrewtypes.Schedule{JobNames: []string{"j1"}}))

	if err := resolveDataBrewRefs(acct, st); err != nil {
		t.Fatalf("resolveDataBrewRefs: %v", err)
	}
	rels := dbRels(t, st, jobID)
	assertRelationship(t, rels, jobID, dsID, store.RelUses)
	assertRelationship(t, rels, jobID, prjID, store.RelUses)
	assertRelationship(t, rels, jobID, roleID, store.RelUses)
	rels = dbRels(t, st, prjID)
	assertRelationship(t, rels, prjID, dsID, store.RelUses)
	assertRelationship(t, rels, prjID, rcpID, store.RelUses)
	assertRelationship(t, dbRels(t, st, rcpID), rcpID, prjID, store.RelAttachedTo)
	assertRelationship(t, dbRels(t, st, schID), schID, jobID, store.RelUses)
}

func TestResolveDataBrewDatasetRefs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	dsARN := "arn:aws:databrew:us-east-1:" + testAccountID + ":dataset/d1"
	bARN := "arn:aws:s3:::brew-input"
	attrs := mustJSON(databrewtypes.Dataset{Input: &databrewtypes.Input{S3InputDefinition: &databrewtypes.S3Location{Bucket: ptrStr("brew-input")}}})
	dID := upsertTestResource(t, st, "aws", acct.ID, TypeDataBrewDataset, dsARN, testRegion, attrs)
	bID := upsertTestResource(t, st, "aws", acct.ID, TypeS3Bucket, bARN, testRegion, "{}")

	if err := resolveDataBrewDatasetRefs(acct, st); err != nil {
		t.Fatalf("resolveDataBrewDatasetRefs: %v", err)
	}
	assertRelationship(t, dbRels(t, st, dID), dID, bID, store.RelUses)
}

func TestResolveDataBrewRulesetTarget(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	rsARN := "arn:aws:databrew:us-east-1:" + testAccountID + ":ruleset/r1"
	dsARN := "arn:aws:databrew:us-east-1:" + testAccountID + ":dataset/d1"
	rID := upsertTestResource(t, st, "aws", acct.ID, TypeDataBrewRuleset, rsARN, testRegion,
		mustJSON(databrewtypes.RulesetItem{TargetArn: ptrStr(dsARN)}))
	dID := upsertTestResource(t, st, "aws", acct.ID, TypeDataBrewDataset, dsARN, testRegion, "{}")

	if err := resolveDataBrewRulesetTarget(acct, st); err != nil {
		t.Fatalf("resolveDataBrewRulesetTarget: %v", err)
	}
	assertRelationship(t, dbRels(t, st, rID), rID, dID, store.RelUses)
}

// dbResolverCases are the DataBrew resolvers by the source type they read.
var dbResolverCases = []struct {
	name    string
	resolve func(*account, *store.Store) error
	srcType string
}{
	{"job", resolveDataBrewRefs, TypeDataBrewJob},
	{"project", resolveDataBrewRefs, TypeDataBrewProject},
	{"recipe", resolveDataBrewRefs, TypeDataBrewRecipe},
	{"schedule", resolveDataBrewRefs, TypeDataBrewSchedule},
	{"dataset", resolveDataBrewDatasetRefs, TypeDataBrewDataset},
	{"ruleset", resolveDataBrewRulesetTarget, TypeDataBrewRuleset},
}

// seedDataBrewTargets stores one row of every type a DataBrew resolver
// targets, so an edge from an empty source row would have somewhere to land.
func seedDataBrewTargets(t *testing.T, st *store.Store) {
	t.Helper()
	for _, s := range []struct{ typ, arn string }{
		{TypeDataBrewDataset, dbrARN(testRegion, testAccountID, "dataset", "ds1")},
		{TypeDataBrewProject, dbrARN(testRegion, testAccountID, "project", "p1")},
		{TypeDataBrewRecipe, dbrARN(testRegion, testAccountID, "recipe", "r1")},
		{TypeDataBrewJob, dbrARN(testRegion, testAccountID, "job", "j1")},
		{TypeIAMRole, "arn:aws:iam::" + testAccountID + ":role/dbr"},
		{TypeS3Bucket, "arn:aws:s3:::brew-input"},
	} {
		upsertTestResource(t, st, "aws", testAccountID, s.typ, s.arn, testRegion, "{}")
	}
}

// TestResolveDataBrew_NoAttrsNoEdge pins that a source row with empty
// attributes emits no edge and no error.
func TestResolveDataBrew_NoAttrsNoEdge(t *testing.T) {
	for _, c := range dbResolverCases {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			acct := newTestAccount(testAccountID)
			seedDataBrewTargets(t, st)
			srcID := upsertTestResource(t, st, "aws", acct.ID, c.srcType, dbrARN(testRegion, acct.ID, "src", c.name), testRegion, "{}")
			if err := c.resolve(acct, st); err != nil {
				t.Fatalf("resolve: %v", err)
			}
			if rels := dbRels(t, st, srcID); len(rels) != 0 {
				t.Errorf("edges from an empty %s = %v, want none", c.srcType, rels)
			}
		})
	}
}

// TestResolveDataBrew_DecodeErrorReturned pins that a source row whose
// attributes do not decode makes the resolver return an error, while a
// decodable row of every other DataBrew source type still gets its edge.
func TestResolveDataBrew_DecodeErrorReturned(t *testing.T) {
	for _, c := range dbResolverCases {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			acct := newTestAccount(testAccountID)
			seedDataBrewTargets(t, st)
			upsertTestResource(t, st, "aws", acct.ID, c.srcType, dbrARN(testRegion, acct.ID, "bad", c.name), testRegion, "[]")
			var siblingID, siblingTarget string
			if c.srcType != TypeDataBrewSchedule && c.srcType != TypeDataBrewDataset && c.srcType != TypeDataBrewRuleset {
				siblingID = upsertTestResource(t, st, "aws", acct.ID, TypeDataBrewSchedule, dbrARN(testRegion, acct.ID, "schedule", "s1"), testRegion,
					mustJSON(databrewtypes.Schedule{JobNames: []string{"j1"}}))
				siblingTarget = store.ResourceID("aws", acct.ID, dbrARN(testRegion, acct.ID, "job", "j1"))
			}
			if err := c.resolve(acct, st); err == nil {
				t.Fatalf("resolve with an undecodable %s row: err = nil, want an error", c.srcType)
			}
			if siblingID != "" {
				assertRelationship(t, dbRels(t, st, siblingID), siblingID, siblingTarget, store.RelUses)
			}
		})
	}
}

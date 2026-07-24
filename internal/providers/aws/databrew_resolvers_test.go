package aws

import (
	"fmt"
	"testing"

	"github.com/icearp/disco-cli/store"
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

func TestResolveDataBrewDatasetRefs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	dsARN := "arn:aws:databrew:us-east-1:" + testAccountID + ":dataset/d1"
	bARN := "arn:aws:s3:::brew-input"
	attrs := `{"Input":{"S3InputDefinition":{"Bucket":"brew-input"}}}`
	dID := upsertTestResource(t, st, "aws", acct.ID, TypeDataBrewDataset, dsARN, testRegion, attrs)
	bID := upsertTestResource(t, st, "aws", acct.ID, TypeS3Bucket, bARN, testRegion, "{}")

	if err := resolveDataBrewDatasetRefs(acct, st); err != nil {
		t.Fatalf("resolveDataBrewDatasetRefs: %v", err)
	}
	rels, _ := st.RelationshipsFrom(dID)
	assertRelationship(t, rels, dID, bID, store.RelUses)
}

func TestResolveDataBrewRulesetTarget(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	rsARN := "arn:aws:databrew:us-east-1:" + testAccountID + ":ruleset/r1"
	dsARN := "arn:aws:databrew:us-east-1:" + testAccountID + ":dataset/d1"
	attrs := `{"TargetArn":"` + dsARN + `"}`
	rID := upsertTestResource(t, st, "aws", acct.ID, TypeDataBrewRuleset, rsARN, testRegion, attrs)
	dID := upsertTestResource(t, st, "aws", acct.ID, TypeDataBrewDataset, dsARN, testRegion, "{}")

	if err := resolveDataBrewRulesetTarget(acct, st); err != nil {
		t.Fatalf("resolveDataBrewRulesetTarget: %v", err)
	}
	rels, _ := st.RelationshipsFrom(rID)
	assertRelationship(t, rels, rID, dID, store.RelUses)
}

package aws

import (
	"testing"

	"github.com/icearp/disco-cli/store"
	ssmsaptypes "github.com/aws/aws-sdk-go-v2/service/ssmsap/types"
)

func TestResolveSSMSAPComponentApplication(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	appID := "myApp"
	appArn := "arn:aws:ssm-sap:us-east-1:" + acct.ID + ":application/" + appID
	compID := "comp-1"
	compArn := "arn:aws:ssm-sap:us-east-1:" + acct.ID + ":HANA/" + compID

	app := upsertTestResource(t, st, "aws", acct.ID, TypeSSMSAPApplication, appArn, testRegion,
		mustJSON(ssmsaptypes.ApplicationSummary{Id: &appID, Arn: &appArn}))
	comp := upsertTestResource(t, st, "aws", acct.ID, TypeSystemsManagerSAPComponent, compArn, testRegion,
		mustJSON(ssmsaptypes.ComponentSummary{Arn: &compArn, ComponentId: &compID, ApplicationId: &appID}))

	if err := resolveSSMSAPComponentApplication(acct, st); err != nil {
		t.Fatalf("resolveSSMSAPComponentApplication: %v", err)
	}
	rels, _ := st.RelationshipsFrom(comp)
	assertRelationship(t, rels, comp, app, store.RelAttachedTo)
}

func TestResolveSSMSAPDatabaseParent(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	appID := "myApp"
	appArn := "arn:aws:ssm-sap:us-east-1:" + acct.ID + ":application/" + appID
	compID := "comp-1"
	compArn := "arn:aws:ssm-sap:us-east-1:" + acct.ID + ":HANA/" + compID
	dbID := "db-1"
	dbArn := "arn:aws:ssm-sap:us-east-1:" + acct.ID + ":DB/" + dbID

	_ = upsertTestResource(t, st, "aws", acct.ID, TypeSSMSAPApplication, appArn, testRegion,
		mustJSON(ssmsaptypes.ApplicationSummary{Id: &appID, Arn: &appArn}))
	comp := upsertTestResource(t, st, "aws", acct.ID, TypeSystemsManagerSAPComponent, compArn, testRegion,
		mustJSON(ssmsaptypes.ComponentSummary{Arn: &compArn, ComponentId: &compID, ApplicationId: &appID}))
	db := upsertTestResource(t, st, "aws", acct.ID, TypeSystemsManagerSAPDatabase, dbArn, testRegion,
		mustJSON(ssmsaptypes.DatabaseSummary{Arn: &dbArn, DatabaseId: &dbID, ComponentId: &compID, ApplicationId: &appID}))

	if err := resolveSSMSAPDatabaseParent(acct, st); err != nil {
		t.Fatalf("resolveSSMSAPDatabaseParent: %v", err)
	}
	rels, _ := st.RelationshipsFrom(db)
	assertRelationship(t, rels, db, comp, store.RelAttachedTo)
}

func TestResolveSSMSAPResolvers_NoAttrs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	compArn := "arn:aws:ssm-sap:us-east-1:" + acct.ID + ":HANA/comp-1"
	comp := upsertTestResource(t, st, "aws", acct.ID, TypeSystemsManagerSAPComponent, compArn, testRegion, "{}")

	if err := resolveSSMSAPComponentApplication(acct, st); err != nil {
		t.Fatalf("resolveSSMSAPComponentApplication (no attrs): %v", err)
	}
	rels, _ := st.RelationshipsFrom(comp)
	if len(rels) != 0 {
		t.Errorf("expected no edges for component with no application, got %d", len(rels))
	}
}

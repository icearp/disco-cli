package aws

import (
	"testing"

	ssmsaptypes "github.com/aws/aws-sdk-go-v2/service/ssmsap/types"
	"github.com/icearp/disco-cli/store"
)

// sapResolverFixture stores app-a in testRegion and in us-west-2, and app-b
// in testRegion, each with a component HDB, so an ApplicationId or
// ComponentId alone names several rows.
type sapResolverFixture struct {
	app, appWest, appB, comp, compWest, compB string
}

func sapRegionARN(region, path string) string {
	return "arn:aws:ssm-sap:" + region + ":" + testAccountID + ":SAP_ABAP/" + path
}

func seedSAPResolverFixture(t *testing.T, st *store.Store) sapResolverFixture {
	t.Helper()
	app := func(region, id string) string {
		arn := sapRegionARN(region, id)
		return upsertTestResourceNamed(t, st, TypeSSMSAPApplication, arn, region,
			mustJSON(ssmsaptypes.ApplicationSummary{Id: &id, Arn: &arn}), id)
	}
	comp := func(region, appID, id string) string {
		arn := sapRegionARN(region, appID+"/COMPONENT/"+id)
		return upsertTestResourceNamed(t, st, TypeSystemsManagerSAPComponent, arn, region,
			mustJSON(ssmsaptypes.ComponentSummary{Arn: &arn, ComponentId: &id, ApplicationId: &appID}), id)
	}
	f := sapResolverFixture{
		app: app(testRegion, "app-a"), appWest: app("us-west-2", "app-a"),
		comp: comp(testRegion, "app-a", "HDB"), compWest: comp("us-west-2", "app-a", "HDB"),
	}
	f.appB = app(testRegion, "app-b")
	f.compB = comp(testRegion, "app-b", "HDB")
	return f
}

func seedSAPDatabase(t *testing.T, st *store.Store, appID, compID, dbID string) string {
	t.Helper()
	arn := sapRegionARN(testRegion, appID+"/DB/"+dbID)
	return upsertTestResourceNamed(t, st, TypeSystemsManagerSAPDatabase, arn, testRegion,
		mustJSON(ssmsaptypes.DatabaseSummary{Arn: &arn, DatabaseId: &dbID, ComponentId: &compID, ApplicationId: &appID}), dbID)
}

// assertOnlyEdge checks from's only outbound edge is attached-to want.
func assertOnlyEdge(t *testing.T, st *store.Store, from, want string) {
	t.Helper()
	rels, err := st.RelationshipsFrom(from)
	if err != nil {
		t.Fatalf("RelationshipsFrom(%s): %v", from, err)
	}
	if len(rels) != 1 || rels[0].ToID != want || rels[0].Kind != store.RelAttachedTo {
		t.Errorf("edges from %s = %+v, want one attached-to %s", from, rels, want)
	}
}

func assertNoEdges(t *testing.T, st *store.Store, from string) {
	t.Helper()
	rels, err := st.RelationshipsFrom(from)
	if err != nil {
		t.Fatalf("RelationshipsFrom(%s): %v", from, err)
	}
	if len(rels) != 0 {
		t.Errorf("edges from %s = %+v, want none", from, rels)
	}
}

// Each component links to the application of its own region and
// ApplicationId, not to a same-named application elsewhere.
func TestResolveSSMSAPComponentApplication(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	f := seedSAPResolverFixture(t, st)
	if err := resolveSSMSAPComponentApplication(acct, st); err != nil {
		t.Fatalf("resolveSSMSAPComponentApplication: %v", err)
	}
	assertOnlyEdge(t, st, f.comp, f.app)
	assertOnlyEdge(t, st, f.compWest, f.appWest)
	assertOnlyEdge(t, st, f.compB, f.appB)
}

// A database links to the component of its own region, application and
// ComponentId; with no such component it links to its application.
func TestResolveSSMSAPDatabaseParent(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	f := seedSAPResolverFixture(t, st)
	db := seedSAPDatabase(t, st, "app-a", "HDB", "SYSTEMDB")
	dbNoComp := seedSAPDatabase(t, st, "app-a", "GONE", "HDB2")
	dbOrphan := seedSAPDatabase(t, st, "app-gone", "HDB", "HDB3")
	dbB := seedSAPDatabase(t, st, "app-b", "HDB", "SYSTEMDB-B")
	if err := resolveSSMSAPDatabaseParent(acct, st); err != nil {
		t.Fatalf("resolveSSMSAPDatabaseParent: %v", err)
	}
	assertOnlyEdge(t, st, db, f.comp)
	assertOnlyEdge(t, st, dbB, f.compB)
	assertOnlyEdge(t, st, dbNoComp, f.app)
	assertNoEdges(t, st, dbOrphan)
}

func TestResolveSSMSAPResolvers_NoAttrs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	seedSAPResolverFixture(t, st)
	// An application with no Id is no parent for a child with no ApplicationId.
	upsertTestResource(t, st, "aws", acct.ID, TypeSSMSAPApplication, sapRegionARN(testRegion, "x"), testRegion, "{}")
	comp := upsertTestResource(t, st, "aws", acct.ID, TypeSystemsManagerSAPComponent, sapRegionARN(testRegion, "x/COMPONENT/c"), testRegion, "{}")
	db := upsertTestResource(t, st, "aws", acct.ID, TypeSystemsManagerSAPDatabase, sapRegionARN(testRegion, "x/DB/d"), testRegion, "{}")

	if err := resolveSSMSAPComponentApplication(acct, st); err != nil {
		t.Fatalf("resolveSSMSAPComponentApplication (no attrs): %v", err)
	}
	if err := resolveSSMSAPDatabaseParent(acct, st); err != nil {
		t.Fatalf("resolveSSMSAPDatabaseParent (no attrs): %v", err)
	}
	assertNoEdges(t, st, comp)
	assertNoEdges(t, st, db)
}

// A row whose attributes do not decode is returned as an error, and every
// other row still gets its edge. The bad row has no name, so it is read
// before the named fixture rows.
func TestResolveSSMSAPResolvers_DecodeErrorReturned(t *testing.T) {
	for _, tc := range []struct {
		name, badType string
		dbResolver    bool
	}{
		{"component resolver, bad component", TypeSystemsManagerSAPComponent, false},
		{"component resolver, bad application", TypeSSMSAPApplication, false},
		{"database resolver, bad database", TypeSystemsManagerSAPDatabase, true},
		{"database resolver, bad component", TypeSystemsManagerSAPComponent, true},
		{"database resolver, bad application", TypeSSMSAPApplication, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			acct := newTestAccount(testAccountID)
			f := seedSAPResolverFixture(t, st)
			db := seedSAPDatabase(t, st, "app-a", "HDB", "SYSTEMDB")
			dbNoComp := seedSAPDatabase(t, st, "app-a", "GONE", "HDB2")
			upsertTestResource(t, st, "aws", acct.ID, tc.badType, sapRegionARN(testRegion, "bad"), testRegion, `["not an object"]`)
			resolve := resolveSSMSAPComponentApplication
			if tc.dbResolver {
				resolve = resolveSSMSAPDatabaseParent
			}
			if err := resolve(acct, st); err == nil {
				t.Fatalf("resolve returned nil, want the decode error")
			}
			if tc.dbResolver {
				assertOnlyEdge(t, st, db, f.comp)
				assertOnlyEdge(t, st, dbNoComp, f.app)
				return
			}
			assertOnlyEdge(t, st, f.comp, f.app)
			assertOnlyEdge(t, st, f.compWest, f.appWest)
		})
	}
}

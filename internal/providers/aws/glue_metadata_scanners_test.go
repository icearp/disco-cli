package aws

import (
	"context"
	"encoding/json"
	"slices"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/glue"
	gluetypes "github.com/aws/aws-sdk-go-v2/service/glue/types"
	"github.com/aws/smithy-go"
	"github.com/icearp/disco-cli/store"
)

const (
	glueTestARNPrefix     = "arn:aws:glue:us-east-1:123456789012:"
	glueTestPolicyARN     = glueTestARNPrefix + "resource-policy"
	glueTestCatalogPolicy = `{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Principal":{"AWS":"arn:aws:iam::210987654321:root"},"Action":"glue:GetTable","Resource":"*"}]}`
)

var (
	glueDenied   = &smithy.GenericAPIError{Code: "AccessDeniedException", Message: "User: arn:aws:iam::123456789012:user/t is not authorized to perform: glue:List"}
	glueBadInput = &smithy.GenericAPIError{Code: "InvalidInputException", Message: "bad"}
	glueNoPolicy = &smithy.GenericAPIError{Code: "EntityNotFoundException", Message: "Policy not found"}

	gluePolicyCreated = time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
)

func glueStubClient(handlers map[string]func(in any) (any, error)) *glue.Client {
	return glue.NewFromConfig(opStubConfig(handlers, glueDenied))
}

func glueGlossaryARN(id string) string { return glueTestARNPrefix + "glossary/" + id }

func glueTermID(glossary, term string) string { return glueGlossaryARN(glossary) + "/term/" + term }

func gluePolicyHandler(any) (any, error) {
	return &glue.GetResourcePolicyOutput{PolicyInJson: ptrStr(glueTestCatalogPolicy), PolicyHash: ptrStr("h1"), CreateTime: &gluePolicyCreated}, nil
}

func TestScanGlueResourcePolicy_StoresCatalogPolicy(t *testing.T) {
	st := newTestStore(t)
	var asked *string
	client := glueStubClient(map[string]func(any) (any, error){"GetResourcePolicy": func(in any) (any, error) {
		asked = in.(*glue.GetResourcePolicyInput).ResourceArn
		return gluePolicyHandler(in)
	}})
	total, _, err := scanGlueResourcePolicy(context.Background(), client, newTestAccount(testAccountID), testRegion, st, testScanID)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if asked != nil {
		t.Errorf("ResourceArn = %q, want unset (the catalog policy)", *asked)
	}
	rows := rowsByNativeID(t, st, TypeGlueResourcePolicy)
	r, ok := rows[glueTestPolicyARN]
	if total != 1 || len(rows) != 1 || !ok {
		t.Fatalf("total = %d, rows = %v, want one row %s", total, rows, glueTestPolicyARN)
	}
	if r.Type != TypeGlueResourcePolicy || sv(r.Region) != testRegion || sv(r.Name) != "data-catalog-resource-policy" || sv(r.CreatedAt) != "2026-01-02T03:04:05Z" {
		t.Errorf("row = type %q region %q name %q created %q", r.Type, sv(r.Region), sv(r.Name), sv(r.CreatedAt))
	}
	if r.ManagedByProvider {
		t.Error("catalog resource policy stored as provider-managed; the account puts and deletes it")
	}
	var attrs glue.GetResourcePolicyOutput
	if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil || sv(attrs.PolicyInJson) != glueTestCatalogPolicy {
		t.Errorf("attrs = %s (%v), want PolicyInJson stored verbatim", r.AttributesJSON, err)
	}
}

func TestScanGlueResourcePolicy_Errors(t *testing.T) {
	cases := []struct {
		name     string
		err      error
		wantErr  bool
		wantWarn int
	}{
		{name: "no policy put", err: glueNoPolicy},
		{name: "empty policy body"},
		{name: "denied warns", err: glueDenied, wantWarn: 1},
		{name: "other error propagates", err: glueBadInput, wantErr: true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := countOpWarnings(st, "glue:GetResourcePolicy")
			client := glueStubClient(map[string]func(any) (any, error){"GetResourcePolicy": func(any) (any, error) {
				if c.err != nil {
					return nil, c.err
				}
				return &glue.GetResourcePolicyOutput{PolicyHash: ptrStr("h1")}, nil
			}})
			total, _, err := scanGlueResourcePolicy(context.Background(), client, newTestAccount(testAccountID), testRegion, st, testScanID)
			if c.wantErr != isAPIErrorCode(err, "InvalidInputException") || (!c.wantErr && err != nil) {
				t.Fatalf("err = %v, want InvalidInputException: %v", err, c.wantErr)
			}
			if total != 0 || len(rowsByNativeID(t, st, TypeGlueResourcePolicy)) != 0 {
				t.Errorf("total = %d, want 0 rows", total)
			}
			if *warns != c.wantWarn {
				t.Errorf("warnings = %d, want %d", *warns, c.wantWarn)
			}
		})
	}
}

func glueAssetTypesHandler(in any) (any, error) {
	if in.(*glue.ListAssetTypesInput).NextToken == nil {
		return &glue.ListAssetTypesOutput{NextToken: ptrStr("p2"), Items: []gluetypes.AssetTypeItem{{Id: ptrStr("at-1"), Name: ptrStr("Dashboard")}}}, nil
	}
	return &glue.ListAssetTypesOutput{Items: []gluetypes.AssetTypeItem{{Id: ptrStr("at-2"), Name: ptrStr("Report")}, {Name: ptrStr("no id")}}}, nil
}

func TestScanGlueAssetTypes_StoresEveryPage(t *testing.T) {
	st := newTestStore(t)
	client := glueStubClient(map[string]func(any) (any, error){"ListAssetTypes": glueAssetTypesHandler})
	total, _, err := scanGlueAssetTypes(context.Background(), client, newTestAccount(testAccountID), testRegion, st, testScanID)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	rows := rowsByNativeID(t, st, TypeGlueAssetType)
	if total != 2 || len(rows) != 2 {
		t.Fatalf("total = %d, rows = %d, want 2 and 2 (id-less item skipped)", total, len(rows))
	}
	r, ok := rows[glueTestARNPrefix+"assetType/at-2"]
	if !ok {
		t.Fatalf("no row for at-2; got %v", rows)
	}
	if r.Type != TypeGlueAssetType || sv(r.Name) != "Report" || sv(r.Region) != testRegion {
		t.Errorf("row = type %q name %q region %q", r.Type, sv(r.Name), sv(r.Region))
	}
	var attrs gluetypes.AssetTypeItem
	if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil || sv(attrs.Id) != "at-2" {
		t.Errorf("attrs = %s (%v), want Id at-2", r.AttributesJSON, err)
	}
}

func glueGlossariesHandler(in any) (any, error) {
	if in.(*glue.ListGlossariesInput).NextToken == nil {
		return &glue.ListGlossariesOutput{NextToken: ptrStr("p2"), Items: []gluetypes.GlossaryItem{{Id: ptrStr("g1"), Name: ptrStr("Finance")}}}, nil
	}
	return &glue.ListGlossariesOutput{Items: []gluetypes.GlossaryItem{{Id: ptrStr("g2"), Name: ptrStr("Sales"), Description: ptrStr("sales terms")}, {Name: ptrStr("no id")}}}, nil
}

func TestScanGlueGlossaries_StoresEveryPageAndReturnsParents(t *testing.T) {
	st := newTestStore(t)
	client := glueStubClient(map[string]func(any) (any, error){"ListGlossaries": glueGlossariesHandler})
	parents, total, _, err := scanGlueGlossaries(context.Background(), client, newTestAccount(testAccountID), testRegion, st, testScanID)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	want := []childParent{{id: "g1", arn: glueGlossaryARN("g1")}, {id: "g2", arn: glueGlossaryARN("g2")}}
	if !slices.Equal(parents, want) {
		t.Errorf("parents = %v, want %v", parents, want)
	}
	rows := rowsByNativeID(t, st, TypeGlueGlossary)
	if total != 2 || len(rows) != 2 {
		t.Fatalf("total = %d, rows = %d, want 2 and 2 (id-less item skipped)", total, len(rows))
	}
	r := rows[glueGlossaryARN("g2")]
	if r.Type != TypeGlueGlossary || sv(r.Name) != "Sales" || sv(r.Region) != testRegion {
		t.Errorf("row = type %q name %q region %q", r.Type, sv(r.Name), sv(r.Region))
	}
	var attrs gluetypes.GlossaryItem
	if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil || sv(attrs.Description) != "sales terms" {
		t.Errorf("attrs = %s (%v), want Description", r.AttributesJSON, err)
	}
}

// TestGlueMetadataListPhases_Errors drives the two parentless list phases
// through an empty result, a denial and an unmodelled error.
func TestGlueMetadataListPhases_Errors(t *testing.T) {
	cases := []struct {
		op    string
		typ   string
		empty any
		run   func(context.Context, glueAPI, *store.Store) (int, error)
	}{
		{
			op: "ListAssetTypes", typ: TypeGlueAssetType, empty: &glue.ListAssetTypesOutput{},
			run: func(ctx context.Context, c glueAPI, st *store.Store) (int, error) {
				total, _, err := scanGlueAssetTypes(ctx, c, newTestAccount(testAccountID), testRegion, st, testScanID)
				return total, err
			},
		},
		{
			op: "ListGlossaries", typ: TypeGlueGlossary, empty: &glue.ListGlossariesOutput{},
			run: func(ctx context.Context, c glueAPI, st *store.Store) (int, error) {
				_, total, _, err := scanGlueGlossaries(ctx, c, newTestAccount(testAccountID), testRegion, st, testScanID)
				return total, err
			},
		},
	}
	for _, c := range cases {
		t.Run(c.op+"/empty", func(t *testing.T) {
			st := newTestStore(t)
			client := glueStubClient(map[string]func(any) (any, error){c.op: func(any) (any, error) { return c.empty, nil }})
			total, err := c.run(context.Background(), client, st)
			if err != nil || total != 0 || len(rowsByNativeID(t, st, c.typ)) != 0 {
				t.Fatalf("total = %d, err = %v, want 0 rows and nil", total, err)
			}
		})
		t.Run(c.op+"/denied warns", func(t *testing.T) {
			st := newTestStore(t)
			warns := countOpWarnings(st, "glue:"+c.op)
			total, err := c.run(context.Background(), glueStubClient(nil), st)
			if err != nil || total != 0 {
				t.Fatalf("total = %d, err = %v, want 0 and nil", total, err)
			}
			if *warns != 1 {
				t.Errorf("warnings for glue:%s = %d, want 1", c.op, *warns)
			}
		})
		t.Run(c.op+"/other error propagates", func(t *testing.T) {
			st := newTestStore(t)
			client := glueStubClient(map[string]func(any) (any, error){c.op: func(any) (any, error) { return nil, glueBadInput }})
			if _, err := c.run(context.Background(), client, st); !isAPIErrorCode(err, "InvalidInputException") {
				t.Fatalf("err = %v, want InvalidInputException", err)
			}
		})
	}
}

// glueTermsHandler answers ListGlossaryTerms: g1 over two pages (plus an
// id-less term), g2 with one term; failFor glossaries get err.
func glueTermsHandler(calls *parentCalls, failFor string, err error) func(any) (any, error) {
	return func(in any) (any, error) {
		req := in.(*glue.ListGlossaryTermsInput)
		g := sv(req.GlossaryIdentifier)
		calls.add(g)
		if g == failFor {
			return nil, err
		}
		if g == "g1" && req.NextToken == nil {
			return &glue.ListGlossaryTermsOutput{NextToken: ptrStr("p2"), Items: []gluetypes.GlossaryTermItem{{Id: ptrStr("t1"), Name: ptrStr("Revenue")}}}, nil
		}
		return &glue.ListGlossaryTermsOutput{Items: []gluetypes.GlossaryTermItem{
			{Id: ptrStr(g + "-t2"), Name: ptrStr("Margin"), ShortDescription: ptrStr("gross margin")}, {Name: ptrStr("no id")},
		}}, nil
	}
}

func seedGlueGlossaries(t *testing.T, st *store.Store) []childParent {
	t.Helper()
	parents := []childParent{{id: "g1", arn: glueGlossaryARN("g1")}, {id: "g2", arn: glueGlossaryARN("g2")}}
	for _, p := range parents {
		upsertTestResource(t, st, "aws", testAccountID, TypeGlueGlossary, p.arn, testRegion, "{}")
	}
	return parents
}

func TestScanGlueGlossaryTerms_ListsEveryGlossaryUnderItsParent(t *testing.T) {
	st := newTestStore(t)
	parents := seedGlueGlossaries(t, st)
	calls := &parentCalls{}
	client := glueStubClient(map[string]func(any) (any, error){"ListGlossaryTerms": glueTermsHandler(calls, "", nil)})
	total, _, err := scanGlueGlossaryTerms(context.Background(), client, parents, newTestAccount(testAccountID), testRegion, st, testScanID)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if got := calls.distinct(); !slices.Equal(got, []string{"g1", "g2"}) {
		t.Errorf("glossaries queried = %v, want [g1 g2]", got)
	}
	rows := rowsByNativeID(t, st, TypeGlueGlossaryTerm)
	if total != 3 || len(rows) != 3 {
		t.Fatalf("total = %d, rows = %d, want 3 and 3 (both g1 pages, id-less term skipped)", total, len(rows))
	}
	r, ok := rows[glueTermID("g2", "g2-t2")]
	if !ok {
		t.Fatalf("no row for g2-t2; got %v", rows)
	}
	if r.Type != TypeGlueGlossaryTerm || sv(r.Name) != "Margin" || sv(r.Region) != testRegion {
		t.Errorf("row = type %q name %q region %q", r.Type, sv(r.Name), sv(r.Region))
	}
	var attrs gluetypes.GlossaryTermItem
	if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil || sv(attrs.ShortDescription) != "gross margin" {
		t.Errorf("attrs = %s (%v), want ShortDescription", r.AttributesJSON, err)
	}
	assertContainsNative(t, st, glueGlossaryARN("g1"), glueTermID("g1", "t1"))
	assertContainsNative(t, st, glueGlossaryARN("g2"), glueTermID("g2", "g2-t2"))
}

func TestScanGlueGlossaryTerms_DeniedGlossaryWarnsOnceAndKeepsSiblings(t *testing.T) {
	st := newTestStore(t)
	parents := seedGlueGlossaries(t, st)
	warns := countOpWarnings(st, "glue:ListGlossaryTerms")
	client := glueStubClient(map[string]func(any) (any, error){"ListGlossaryTerms": glueTermsHandler(&parentCalls{}, "g1", glueDenied)})
	total, _, err := scanGlueGlossaryTerms(context.Background(), client, parents, newTestAccount(testAccountID), testRegion, st, testScanID)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	rows := rowsByNativeID(t, st, TypeGlueGlossaryTerm)
	if _, ok := rows[glueTermID("g2", "g2-t2")]; total != 1 || len(rows) != 1 || !ok {
		t.Errorf("total = %d, rows = %v, want only g2's term", total, rows)
	}
	if *warns != 1 {
		t.Errorf("warnings = %d, want 1", *warns)
	}
}

func TestScanGlueGlossaryTerms_OtherErrorPropagates(t *testing.T) {
	st := newTestStore(t)
	parents := seedGlueGlossaries(t, st)
	client := glueStubClient(map[string]func(any) (any, error){"ListGlossaryTerms": glueTermsHandler(&parentCalls{}, "g2", glueBadInput)})
	if _, _, err := scanGlueGlossaryTerms(context.Background(), client, parents, newTestAccount(testAccountID), testRegion, st, testScanID); !isAPIErrorCode(err, "InvalidInputException") {
		t.Fatalf("err = %v, want InvalidInputException", err)
	}
}

// glueServiceHandlers answers one database with one user-defined function (the
// last existing phase) plus every new metadata op; other existing ops are
// denied and skip.
func glueServiceHandlers() map[string]func(any) (any, error) {
	return map[string]func(any) (any, error){
		"GetDatabases": func(any) (any, error) {
			return &glue.GetDatabasesOutput{DatabaseList: []gluetypes.Database{{Name: ptrStr("db1")}}}, nil
		},
		"GetUserDefinedFunctions": func(any) (any, error) {
			return &glue.GetUserDefinedFunctionsOutput{UserDefinedFunctions: []gluetypes.UserDefinedFunction{{FunctionName: ptrStr("fn1"), DatabaseName: ptrStr("db1")}}}, nil
		},
		"GetResourcePolicy": gluePolicyHandler,
		"ListAssetTypes":    glueAssetTypesHandler,
		"ListGlossaries":    glueGlossariesHandler,
		"ListGlossaryTerms": glueTermsHandler(&parentCalls{}, "", nil),
	}
}

func TestScanGlue_NewPhasesStoreRows(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	acct.cfg = opStubConfig(glueServiceHandlers(), glueDenied)
	if _, _, err := scanGlue(context.Background(), acct, testRegion, st, testScanID); err != nil {
		t.Fatalf("scanGlue: %v", err)
	}
	for typ, id := range map[string]string{
		TypeGlueResourcePolicy: glueTestPolicyARN,
		TypeGlueAssetType:      glueTestARNPrefix + "assetType/at-1",
		TypeGlueGlossary:       glueGlossaryARN("g1"),
		TypeGlueGlossaryTerm:   glueTermID("g1", "t1"),
	} {
		if _, ok := rowsByNativeID(t, st, typ)[id]; !ok {
			t.Errorf("%s: no row %s", typ, id)
		}
	}
	assertContainsNative(t, st, glueGlossaryARN("g2"), glueTermID("g2", "g2-t2"))
}

// A failing new phase must not cost the rows of the phases that ran before it,
// and its error must reach the dispatcher.
func TestScanGlue_NewPhaseErrorAfterExistingPhases(t *testing.T) {
	for _, failing := range []string{"GetResourcePolicy", "ListAssetTypes", "ListGlossaries", "ListGlossaryTerms"} {
		t.Run(failing, func(t *testing.T) {
			st := newTestStore(t)
			h := glueServiceHandlers()
			h[failing] = func(any) (any, error) { return nil, glueBadInput }
			acct := newTestAccount(testAccountID)
			acct.cfg = opStubConfig(h, glueDenied)
			if _, _, err := scanGlue(context.Background(), acct, testRegion, st, testScanID); !isAPIErrorCode(err, "InvalidInputException") {
				t.Fatalf("err = %v, want InvalidInputException", err)
			}
			if len(rowsByNativeID(t, st, TypeGlueUserDefinedFunction)) != 1 {
				t.Error("user-defined-function row from the last existing phase lost")
			}
		})
	}
}

// With no databases listed, the database-independent phases (a pre-existing
// one and the metadata phase) must still run.
func TestScanGlue_NoDatabasesStillRunsIndependentPhases(t *testing.T) {
	st := newTestStore(t)
	h := glueServiceHandlers()
	h["GetDatabases"] = func(any) (any, error) { return &glue.GetDatabasesOutput{}, nil }
	h["GetJobs"] = func(any) (any, error) {
		return &glue.GetJobsOutput{Jobs: []gluetypes.Job{{Name: ptrStr("job1")}}}, nil
	}
	acct := newTestAccount(testAccountID)
	acct.cfg = opStubConfig(h, glueDenied)
	if _, _, err := scanGlue(context.Background(), acct, testRegion, st, testScanID); err != nil {
		t.Fatalf("scanGlue: %v", err)
	}
	if n := len(rowsByNativeID(t, st, TypeGlueJob)); n != 1 {
		t.Errorf("job rows = %d, want 1", n)
	}
	for typ, id := range map[string]string{
		TypeGlueResourcePolicy: glueTestPolicyARN,
		TypeGlueGlossaryTerm:   glueTermID("g1", "t1"),
	} {
		if _, ok := rowsByNativeID(t, st, typ)[id]; !ok {
			t.Errorf("%s: no row %s", typ, id)
		}
	}
	if n := len(rowsByNativeID(t, st, TypeGlueUserDefinedFunction)); n != 0 {
		t.Errorf("user-defined-function rows = %d, want 0 with no database", n)
	}
}

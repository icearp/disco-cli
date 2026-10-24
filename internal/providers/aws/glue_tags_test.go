package aws

import (
	"context"
	"errors"
	"net"
	"slices"
	"strings"
	"sync"
	"testing"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/glue"
	gluetypes "github.com/aws/aws-sdk-go-v2/service/glue/types"
	"github.com/icearp/disco-cli/store"
)

// glueTagged is one type the tag read covers: its listing op, and the
// NativeID and tag-read ARN of its fixture resource name ("tagged" or
// "bare").
type glueTagged struct {
	op, typ    string
	nativeID   func(name string) string
	tagARN     func(name string) string
	tokenField string
}

func glueARN(kind string) func(string) string {
	return func(name string) string { return glueTestARNPrefix + kind + "/" + name }
}

func glueTaggedTypes() []glueTagged {
	named := func(op, typ, kind string) glueTagged {
		return glueTagged{op: op, typ: typ, nativeID: glueARN(kind), tagARN: glueARN(kind), tokenField: "NextToken"}
	}
	schema := func(name string) string { return glueTestARNPrefix + "schema/" + name + "/" + name }
	irp := func(name string) string { return glueTestARNPrefix + "integrationresourceproperty/s3/" + name }
	return []glueTagged{
		named("GetDatabases", TypeGlueDatabase, "database"),
		named("ListRegistries", TypeGlueRegistry, "registry"),
		{op: "ListSchemas", typ: TypeGlueSchema, nativeID: schema, tagARN: schema, tokenField: "NextToken"},
		named("GetJobs", TypeGlueJob, "job"),
		named("GetTriggers", TypeGlueTrigger, "trigger"),
		named("ListWorkflows", TypeGlueWorkflow, "workflow"),
		named("GetMLTransforms", TypeGlueMLTransform, "mlTransform"),
		named("GetDevEndpoints", TypeGlueDevEndpoint, "devEndpoint"),
		{op: "GetCatalogs", typ: TypeGlueCatalog, nativeID: func(n string) string { return glueTestARNPrefix + "catalog/id-" + n }, tagARN: glueARN("catalog"), tokenField: "NextToken"},
		named("ListCustomEntityTypes", TypeGlueCustomEntityType, "customEntityType"),
		named("ListDataQualityRulesets", TypeGlueDataQualityRuleset, "dataQualityRuleset"),
		{op: "ListIntegrationResourceProperties", typ: TypeGlueIntegrationResourceProperty, nativeID: irp, tagARN: irp, tokenField: "Marker"},
		named("ListUsageProfiles", TypeGlueUsageProfile, "usageProfile"),
		named("GetCrawlers", TypeGlueCrawler, "crawler"),
		named("GetConnections", TypeGlueConnection, "connection"),
		named("ListBlueprints", TypeGlueBlueprint, "blueprint"),
	}
}

// glueFixtureRows is how many rows glueHandlers lists besides the tagged
// fixtures: two integrations and the encryption-settings singleton.
const glueFixtureRows = 2 + 1

// glueCalls records the stubbed Glue calls (bacCalls) and the inputs the
// tests pin.
type glueCalls struct {
	*bacCalls
	pinned       sync.Mutex
	hidePassword []bool
	includeGraph []bool
	tablesOf     []string
	versionsOf   []string
}

func newGlueCalls() *glueCalls {
	c := &glueCalls{bacCalls: newBACCalls()}
	for _, f := range glueTaggedTypes() {
		c.tags[f.tagARN("tagged")] = map[string]string{"type": f.typ}
	}
	return c
}

// wantTagARNs is every ARN the tag read must be sent, sorted.
func glueWantTagARNs() []string {
	var arns []string
	for _, f := range glueTaggedTypes() {
		arns = append(arns, f.tagARN("tagged"), f.tagARN("bare"))
	}
	slices.Sort(arns)
	return arns
}

func (c *glueCalls) getTags(in any) (any, error) {
	arn := sv(in.(*glue.GetTagsInput).ResourceArn)
	c.mu.Lock()
	c.tagARNs = append(c.tagARNs, arn)
	err, tags := c.errs[arn], c.tags[arn]
	c.mu.Unlock()
	if err != nil {
		return nil, err
	}
	return &glue.GetTagsOutput{Tags: tags}, nil
}

// glueTwoPages serves "tagged" on the first page and "bare" on the second,
// paged by tokenField.
func (c *glueCalls) glueTwoPages(op, tokenField string, out func(name string, next *string) any) func(any) (any, error) {
	return c.answer(op, func(in any) any {
		if bacField[*string](in, tokenField) == nil {
			return out("tagged", sdkaws.String("p2"))
		}
		return out("bare", nil)
	})
}

// glueHandlers answers every Glue op the scan calls: two resources for each
// tagged type (glueTaggedTypes) and the integrations, one schema per
// registry, and empty answers or a modeled "none" for the other phases.
func glueHandlers(c *glueCalls) map[string]func(any) (any, error) {
	none := func(out any) func(any) (any, error) { return func(any) (any, error) { return out, nil } }
	return map[string]func(any) (any, error){
		"GetTags": c.getTags,
		"GetDatabases": c.glueTwoPages("GetDatabases", "NextToken", func(n string, next *string) any {
			return &glue.GetDatabasesOutput{NextToken: next, DatabaseList: []gluetypes.Database{{Name: sdkaws.String(n)}}}
		}),
		"GetTables": func(in any) (any, error) {
			c.pinned.Lock()
			c.tablesOf = append(c.tablesOf, sv(in.(*glue.GetTablesInput).DatabaseName))
			c.pinned.Unlock()
			return &glue.GetTablesOutput{}, nil
		},
		"ListRegistries": c.glueTwoPages("ListRegistries", "NextToken", func(n string, next *string) any {
			return &glue.ListRegistriesOutput{NextToken: next, Registries: []gluetypes.RegistryListItem{{RegistryArn: sdkaws.String(glueARN("registry")(n)), RegistryName: sdkaws.String(n)}}}
		}),
		"ListSchemas": c.answer("ListSchemas", func(in any) any {
			reg := sv(in.(*glue.ListSchemasInput).RegistryId.RegistryArn)
			n := strings.TrimPrefix(reg, glueTestARNPrefix+"registry/")
			return &glue.ListSchemasOutput{Schemas: []gluetypes.SchemaListItem{{SchemaArn: sdkaws.String(glueTestARNPrefix + "schema/" + n + "/" + n), SchemaName: sdkaws.String(n)}}}
		}),
		"ListSchemaVersions": func(in any) (any, error) {
			c.pinned.Lock()
			c.versionsOf = append(c.versionsOf, sv(in.(*glue.ListSchemaVersionsInput).SchemaId.SchemaArn))
			c.pinned.Unlock()
			return &glue.ListSchemaVersionsOutput{}, nil
		},
		"GetJobs": c.glueTwoPages("GetJobs", "NextToken", func(n string, next *string) any {
			return &glue.GetJobsOutput{NextToken: next, Jobs: []gluetypes.Job{{Name: sdkaws.String(n)}}}
		}),
		"GetTriggers": c.glueTwoPages("GetTriggers", "NextToken", func(n string, next *string) any {
			return &glue.GetTriggersOutput{NextToken: next, Triggers: []gluetypes.Trigger{{Name: sdkaws.String(n)}}}
		}),
		"ListWorkflows": c.glueTwoPages("ListWorkflows", "NextToken", func(n string, next *string) any {
			return &glue.ListWorkflowsOutput{NextToken: next, Workflows: []string{n}}
		}),
		"GetWorkflow": func(in any) (any, error) {
			req := in.(*glue.GetWorkflowInput)
			c.pinned.Lock()
			c.includeGraph = append(c.includeGraph, sdkaws.ToBool(req.IncludeGraph))
			c.pinned.Unlock()
			return &glue.GetWorkflowOutput{Workflow: &gluetypes.Workflow{Name: req.Name}}, nil
		},
		"GetMLTransforms": c.glueTwoPages("GetMLTransforms", "NextToken", func(n string, next *string) any {
			return &glue.GetMLTransformsOutput{NextToken: next, Transforms: []gluetypes.MLTransform{{TransformId: sdkaws.String(n)}}}
		}),
		"GetDevEndpoints": c.glueTwoPages("GetDevEndpoints", "NextToken", func(n string, next *string) any {
			return &glue.GetDevEndpointsOutput{NextToken: next, DevEndpoints: []gluetypes.DevEndpoint{{EndpointName: sdkaws.String(n)}}}
		}),
		"GetCatalogs": c.glueTwoPages("GetCatalogs", "NextToken", func(n string, next *string) any {
			return &glue.GetCatalogsOutput{NextToken: next, CatalogList: []gluetypes.Catalog{{CatalogId: sdkaws.String("id-" + n), Name: sdkaws.String(n), ResourceArn: sdkaws.String(glueARN("catalog")(n))}}}
		}),
		"ListCustomEntityTypes": c.glueTwoPages("ListCustomEntityTypes", "NextToken", func(n string, next *string) any {
			return &glue.ListCustomEntityTypesOutput{NextToken: next, CustomEntityTypes: []gluetypes.CustomEntityType{{Name: sdkaws.String(n)}}}
		}),
		"GetDataCatalogEncryptionSettings": none(&glue.GetDataCatalogEncryptionSettingsOutput{}),
		"ListDataQualityRulesets": c.glueTwoPages("ListDataQualityRulesets", "NextToken", func(n string, next *string) any {
			return &glue.ListDataQualityRulesetsOutput{NextToken: next, Rulesets: []gluetypes.DataQualityRulesetListDetails{{Name: sdkaws.String(n)}}}
		}),
		"GetGlueIdentityCenterConfiguration": func(any) (any, error) { return nil, glueNoPolicy },
		"DescribeIntegrations": c.glueTwoPages("DescribeIntegrations", "Marker", func(n string, next *string) any {
			i := gluetypes.Integration{IntegrationArn: sdkaws.String(glueTestARNPrefix + "integration:" + n), IntegrationName: sdkaws.String(n)}
			if n == "tagged" {
				i.Tags = []gluetypes.Tag{{Key: sdkaws.String("type"), Value: sdkaws.String(TypeGlueIntegration)}}
			}
			return &glue.DescribeIntegrationsOutput{Marker: next, Integrations: []gluetypes.Integration{i}}
		}),
		"ListIntegrationResourceProperties": c.glueTwoPages("ListIntegrationResourceProperties", "Marker", func(n string, next *string) any {
			return &glue.ListIntegrationResourcePropertiesOutput{Marker: next, IntegrationResourcePropertyList: []gluetypes.IntegrationResourceProperty{
				{ResourcePropertyArn: sdkaws.String(glueTestARNPrefix + "integrationresourceproperty/s3/" + n), ResourceArn: sdkaws.String("arn:aws:s3:::" + n)},
			}}
		}),
		"GetSecurityConfigurations": none(&glue.GetSecurityConfigurationsOutput{}),
		"ListUsageProfiles": c.glueTwoPages("ListUsageProfiles", "NextToken", func(n string, next *string) any {
			return &glue.ListUsageProfilesOutput{NextToken: next, Profiles: []gluetypes.UsageProfileDefinition{{Name: sdkaws.String(n)}}}
		}),
		"GetCrawlers": c.glueTwoPages("GetCrawlers", "NextToken", func(n string, next *string) any {
			return &glue.GetCrawlersOutput{NextToken: next, Crawlers: []gluetypes.Crawler{{Name: sdkaws.String(n)}}}
		}),
		"GetConnections": func(in any) (any, error) {
			c.pinned.Lock()
			c.hidePassword = append(c.hidePassword, in.(*glue.GetConnectionsInput).HidePassword)
			c.pinned.Unlock()
			return c.glueTwoPages("GetConnections", "NextToken", func(n string, next *string) any {
				return &glue.GetConnectionsOutput{NextToken: next, ConnectionList: []gluetypes.Connection{{Name: sdkaws.String(n)}}}
			})(in)
		},
		"GetClassifiers": none(&glue.GetClassifiersOutput{}),
		"ListBlueprints": c.glueTwoPages("ListBlueprints", "NextToken", func(n string, next *string) any {
			return &glue.ListBlueprintsOutput{NextToken: next, Blueprints: []string{n}}
		}),
		"GetUserDefinedFunctions": none(&glue.GetUserDefinedFunctionsOutput{}),
		"GetResourcePolicy":       func(any) (any, error) { return nil, glueNoPolicy },
		"ListAssetTypes":          none(&glue.ListAssetTypesOutput{}),
		"ListGlossaries":          none(&glue.ListGlossariesOutput{}),
	}
}

// glueScan runs the scan's real entry point against handlers; every op they
// do not answer is denied.
func glueScan(ctx context.Context, t *testing.T, st *store.Store, handlers map[string]func(any) (any, error)) (int, int, error) {
	t.Helper()
	acct := newTestAccount(testAccountID)
	acct.cfg = opStubConfig(handlers, glueDenied)
	return scanGlue(ctx, acct, testRegion, st, testScanID)
}

func glueRowOf(t *testing.T, st *store.Store, typ, nativeID string) (store.Resource, bool) {
	t.Helper()
	r, ok := rowsByNativeID(t, st, typ)[nativeID]
	return r, ok
}

// Every tagged type stores the tags GetTags returns for the resource's ARN,
// and nil for a resource without tags; the integrations store the tags
// their element carries, and no other row is sent to GetTags. Both pages of
// every listing are stored, each listing asks for its model's page cap, and
// the scan reports no error or warning.
func TestScanGlue_StoresTagsOfEveryTaggedType(t *testing.T) {
	st := newTestStore(t)
	warns := countWarnings(st)
	calls := newGlueCalls()

	total, inserted, err := glueScan(context.Background(), t, st, glueHandlers(calls))
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if want := 2*len(glueTaggedTypes()) + glueFixtureRows; total != want || inserted != want {
		t.Errorf("total, inserted = %d, %d; want %d, %d", total, inserted, want, want)
	}
	if *warns != 0 {
		t.Errorf("warnings = %d; want 0", *warns)
	}
	for _, f := range glueTaggedTypes() {
		tagged, ok := glueRowOf(t, st, f.typ, f.nativeID("tagged"))
		if !ok {
			t.Fatalf("%s %s missing", f.typ, f.nativeID("tagged"))
		}
		assertTagsMap(t, tagged.TagsJSON, map[string]string{"type": f.typ})
		bare, ok := glueRowOf(t, st, f.typ, f.nativeID("bare"))
		if !ok {
			t.Fatalf("%s %s missing", f.typ, f.nativeID("bare"))
		}
		if bare.TagsJSON != nil {
			t.Errorf("%s bare TagsJSON = %q; want nil", f.typ, *bare.TagsJSON)
		}
	}
	integ, ok := glueRowOf(t, st, TypeGlueIntegration, glueTestARNPrefix+"integration:tagged")
	if !ok {
		t.Fatal("tagged integration missing")
	}
	assertTagsMap(t, integ.TagsJSON, map[string]string{"type": TypeGlueIntegration})
	schema := glueTaggedTypeOf(t, TypeGlueSchema)
	slices.Sort(calls.versionsOf)
	if want := []string{schema.nativeID("bare"), schema.nativeID("tagged")}; !slices.Equal(calls.versionsOf, want) {
		t.Errorf("ListSchemaVersions sent %v; want every stored schema %v", calls.versionsOf, want)
	}
	if bare, _ := glueRowOf(t, st, TypeGlueIntegration, glueTestARNPrefix+"integration:bare"); bare.TagsJSON != nil {
		t.Errorf("bare integration TagsJSON = %q; want nil", *bare.TagsJSON)
	}
	slices.Sort(calls.tagARNs)
	if want := glueWantTagARNs(); !slices.Equal(calls.tagARNs, want) {
		t.Errorf("GetTags ARNs = %v\nwant %v", calls.tagARNs, want)
	}
	wantCaps := map[string]int32{
		"GetDatabases": 100, "ListRegistries": 100, "ListSchemas": 100, "GetJobs": 1000, "GetTriggers": 200,
		"ListWorkflows": 25, "GetMLTransforms": 1000, "GetDevEndpoints": 1000, "GetCatalogs": 1000,
		"ListCustomEntityTypes": 1000, "ListDataQualityRulesets": 1000, "ListUsageProfiles": 200,
		"GetCrawlers": 1000, "GetConnections": 1000, "ListBlueprints": 25,
	}
	for op, want := range wantCaps {
		got := calls.maxResults[op]
		if len(got) == 0 {
			t.Errorf("%s never sent MaxResults", op)
		}
		for _, n := range got {
			if n != want {
				t.Errorf("%s MaxResults = %d; want %d", op, n, want)
			}
		}
	}
	if !slices.Equal(calls.hidePassword, []bool{true, true}) {
		t.Errorf("GetConnections HidePassword = %v; want true on both pages", calls.hidePassword)
	}
	if !slices.Equal(calls.includeGraph, []bool{true, true}) {
		t.Errorf("GetWorkflow IncludeGraph = %v; want true for both workflows", calls.includeGraph)
	}
}

// A rescan stores the same rows without inserting any.
func TestScanGlue_RescanInsertsNone(t *testing.T) {
	st := newTestStore(t)
	calls := newGlueCalls()
	if _, _, err := glueScan(context.Background(), t, st, glueHandlers(calls)); err != nil {
		t.Fatalf("first scan: %v", err)
	}
	total, inserted, err := glueScan(context.Background(), t, st, glueHandlers(calls))
	if err != nil {
		t.Fatalf("rescan: %v", err)
	}
	if want := 2*len(glueTaggedTypes()) + glueFixtureRows; total != want || inserted != 0 {
		t.Errorf("rescan total, inserted = %d, %d; want %d, 0", total, inserted, want)
	}
}

// A failed tag read keeps the row without tags unless the resource is gone:
// a denial warns once under glue:GetTags, a throttle after retries is a
// transient error, any other code a hard one. A database GetTags no longer
// finds is dropped and its tables are never listed; a schema, read in the
// per-registry phase, and a catalog, read by its ResourceArn, likewise.
func TestScanGlue_TagReadFailures(t *testing.T) {
	for _, target := range []struct{ typ, nativeID, tagARN string }{
		{TypeGlueDatabase, glueARN("database")("tagged"), glueARN("database")("tagged")},
		{TypeGlueSchema, glueTestARNPrefix + "schema/tagged/tagged", glueTestARNPrefix + "schema/tagged/tagged"},
		{TypeGlueCatalog, glueTestARNPrefix + "catalog/id-tagged", glueARN("catalog")("tagged")},
	} {
		t.Run(target.typ, func(t *testing.T) { glueTagReadFailures(t, target.typ, target.nativeID, target.tagARN) })
	}
}

func glueTagReadFailures(t *testing.T, typ, nativeID, tagARN string) {
	for _, c := range []struct {
		name      string
		err       error
		kept      bool
		outcome   serviceOutcome
		wantWarns int
	}{
		{"denied", glueDenied, true, 0, 1},
		{"not found", apiErr("EntityNotFoundException", "gone"), false, 0, 0},
		{"other error", glueBadInput, true, outcomeError, 0},
		{"transient", apiErr("ThrottlingException", "Rate exceeded"), true, outcomeTransient, 0},
	} {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			warnings := bacWarnings(st, glueGetTagsOp)
			calls := newGlueCalls()
			calls.errs[tagARN] = c.err

			_, _, err := glueScan(context.Background(), t, st, glueHandlers(calls))
			if c.outcome == 0 && err != nil {
				t.Fatalf("err = %v; want nil", err)
			}
			if c.outcome != 0 {
				assertGlueOutcome(t, err, c.outcome)
			}
			if c.outcome == outcomeError {
				assertAPICode(t, err, "InvalidInputException")
			}
			r, ok := glueRowOf(t, st, typ, nativeID)
			if ok != c.kept {
				t.Fatalf("%s row stored = %v; want %v", typ, ok, c.kept)
			}
			if ok && r.TagsJSON != nil {
				t.Errorf("TagsJSON = %q; want nil", *r.TagsJSON)
			}
			if typ == TypeGlueDatabase {
				if listed := slices.Contains(calls.tablesOf, "tagged"); listed != c.kept {
					t.Errorf("tables of the database listed = %v; want %v", listed, c.kept)
				}
				if !slices.Contains(calls.tablesOf, "bare") {
					t.Error("tables of the database whose tag read succeeded not listed")
				}
			}
			if len(*warnings) != c.wantWarns {
				t.Fatalf("%s warnings = %v; want %d", glueGetTagsOp, *warnings, c.wantWarns)
			}
			for _, w := range *warnings {
				if w.Scope != testAccountID+"/"+testRegion {
					t.Errorf("warning Scope = %q; want %s/%s", w.Scope, testAccountID, testRegion)
				}
			}
		})
	}
}

// A throttled tag read beside a hard failure of the same op is demoted to
// one warning, so the hard error decides the outcome.
func TestScanGlue_TagThrottleDemotedBesideHardError(t *testing.T) {
	st := newTestStore(t)
	warns := countOpWarnings(st, glueGetTagsOp)
	calls := newGlueCalls()
	calls.errs[glueARN("job")("tagged")] = apiErr("ThrottlingException", "Rate exceeded")
	calls.errs[glueARN("job")("bare")] = glueBadInput

	_, _, err := glueScan(context.Background(), t, st, glueHandlers(calls))
	assertGlueOutcome(t, err, outcomeError)
	assertAPICode(t, err, "InvalidInputException")
	if *warns != 1 {
		t.Errorf("%s warnings = %d; want 1", glueGetTagsOp, *warns)
	}
}

// A failed write stops the scan before any later listing and sends no
// warning about the rows it lost: not the tag denial, and not a denied
// GetWorkflow or schema listing.
func TestScanGlue_FailedWriteStopsWithoutWarning(t *testing.T) {
	for _, tc := range []struct {
		typ, deniedARN, laterOp string
		deny                    func(h map[string]func(any) (any, error))
	}{
		{TypeGlueJob, glueARN("job")("tagged"), "GetTriggers", nil},
		// A denied workflow and one read back without a name each warn, but
		// only after a successful write.
		{TypeGlueWorkflow, glueARN("workflow")("tagged"), "GetMLTransforms", func(h map[string]func(any) (any, error)) {
			list := h["ListWorkflows"]
			h["ListWorkflows"] = func(in any) (any, error) {
				out, err := list(in)
				if err == nil && in.(*glue.ListWorkflowsInput).NextToken == nil {
					o := out.(*glue.ListWorkflowsOutput)
					o.Workflows = append(o.Workflows, "unnamed")
				}
				return out, err
			}
			next := h["GetWorkflow"]
			h["GetWorkflow"] = func(in any) (any, error) {
				switch sv(in.(*glue.GetWorkflowInput).Name) {
				case "bare":
					return nil, glueDenied
				case "unnamed":
					return &glue.GetWorkflowOutput{Workflow: &gluetypes.Workflow{}}, nil
				}
				return next(in)
			}
		}},
		{TypeGlueSchema, glueTestARNPrefix + "schema/tagged/tagged", "GetJobs", func(h map[string]func(any) (any, error)) {
			next := h["ListSchemas"]
			h["ListSchemas"] = func(in any) (any, error) {
				if strings.HasSuffix(sv(in.(*glue.ListSchemasInput).RegistryId.RegistryArn), "/bare") {
					return nil, glueDenied
				}
				return next(in)
			}
		}},
	} {
		t.Run(tc.typ, func(t *testing.T) {
			st := newTestStore(t)
			warns := countWarnings(st)
			refuseStoreWrites(t, st, tc.typ)
			calls := newGlueCalls()
			calls.errs[tc.deniedARN] = glueDenied
			handlers := glueHandlers(calls)
			if tc.deny != nil {
				tc.deny(handlers)
			}

			_, _, err := glueScan(context.Background(), t, st, handlers)
			if !errors.Is(err, store.ErrStoreWrite) {
				t.Fatalf("err = %v; want store.ErrStoreWrite", err)
			}
			if *warns != 0 {
				t.Errorf("warnings = %d; want 0", *warns)
			}
			if n := calls.count(tc.laterOp); n != 0 {
				t.Errorf("%s called %d times after the failed write", tc.laterOp, n)
			}
		})
	}
}

// A listing's failed page keeps the pages read before it, and neither a list
// failure, a pre-existing phase's failure nor a parent listing's failure
// stops the phases after it.
func TestScanGlue_ListFailureKeepsPagesAndRunsEveryPhase(t *testing.T) {
	st := newTestStore(t)
	calls := newGlueCalls()
	handlers := glueHandlers(calls)
	handlers["GetJobs"] = failOnCall(2, apiErr("InvalidInputException", "page 2"), handlers["GetJobs"])
	handlers["ListRegistries"] = func(any) (any, error) { return nil, apiErr("InternalServiceException", "registries") }
	handlers["GetTables"] = func(any) (any, error) { return nil, apiErr("GlueEncryptionException", "tables") }

	_, _, err := glueScan(context.Background(), t, st, handlers)
	for _, code := range []string{"InvalidInputException", "InternalServiceException", "GlueEncryptionException"} {
		if !joinCarriesAPIErrorCode(err, code) {
			t.Errorf("err = %v; want it to carry %s", err, code)
		}
	}
	r, ok := glueRowOf(t, st, TypeGlueJob, glueARN("job")("tagged"))
	if !ok {
		t.Fatal("job from the page before the failure missing")
	}
	assertTagsMap(t, r.TagsJSON, map[string]string{"type": TypeGlueJob})
	if n := calls.count("ListSchemas"); n != 0 {
		t.Errorf("ListSchemas called %d times with no registry stored", n)
	}
	for _, f := range glueTaggedTypes()[3:] {
		if _, ok := glueRowOf(t, st, f.typ, f.nativeID("bare")); !ok && f.typ != TypeGlueJob {
			t.Errorf("%s %s missing after the earlier failures", f.typ, f.nativeID("bare"))
		}
	}
}

// A workflow GetWorkflow fails has no element to store: one it no longer
// finds is dropped silently, a denied one with one warning, and any other
// error is returned, a throttle as transient; the other workflow is stored
// with its tags either way.
func TestScanGlue_WorkflowReadFailures(t *testing.T) {
	for _, c := range []struct {
		name      string
		err       error
		outcome   serviceOutcome
		wantWarns int
	}{
		{"not found", apiErr("EntityNotFoundException", "gone"), 0, 0},
		{"denied", glueDenied, 0, 1},
		{"other error", glueBadInput, outcomeError, 0},
		{"transient", apiErr("ThrottlingException", "Rate exceeded"), outcomeTransient, 0},
		// The listing reached the endpoint, so it is no region gap.
		{"NXDOMAIN", &net.DNSError{Err: "no such host", Name: "glue.us-east-1.amazonaws.com", IsNotFound: true}, outcomeError, 0},
	} {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			warnings := bacWarnings(st, "glue:GetWorkflow")
			calls := newGlueCalls()
			handlers := glueHandlers(calls)
			next := handlers["GetWorkflow"]
			handlers["GetWorkflow"] = func(in any) (any, error) {
				if sv(in.(*glue.GetWorkflowInput).Name) == "bare" {
					return nil, c.err
				}
				return next(in)
			}

			_, _, err := glueScan(context.Background(), t, st, handlers)
			if c.outcome == 0 && err != nil {
				t.Fatalf("err = %v; want nil", err)
			}
			if c.outcome != 0 {
				assertGlueOutcome(t, err, c.outcome)
			}
			rows := rowsByNativeID(t, st, TypeGlueWorkflow)
			if len(rows) != 1 {
				t.Fatalf("workflows stored = %d; want 1", len(rows))
			}
			r := rows[glueARN("workflow")("tagged")]
			assertTagsMap(t, r.TagsJSON, map[string]string{"type": TypeGlueWorkflow})
			if len(*warnings) != c.wantWarns {
				t.Fatalf("glue:GetWorkflow warnings = %v; want %d", *warnings, c.wantWarns)
			}
			for _, w := range *warnings {
				if w.Scope != testAccountID+"/"+testRegion {
					t.Errorf("warning Scope = %q; want %s/%s", w.Scope, testAccountID, testRegion)
				}
			}
		})
	}
}

// A throttled GetWorkflow beside a hard GetWorkflow error is demoted to one
// warning under glue:GetWorkflow and the hard error decides the outcome: one
// error joining both would classify as transient and hide the hard one.
func TestScanGlue_WorkflowThrottleDemotedBesideHardError(t *testing.T) {
	st := newTestStore(t)
	warns := countOpWarnings(st, "glue:GetWorkflow")
	handlers := glueHandlers(newGlueCalls())
	handlers["GetWorkflow"] = func(in any) (any, error) {
		if sv(in.(*glue.GetWorkflowInput).Name) == "bare" {
			return nil, apiErr("ThrottlingException", "Rate exceeded")
		}
		return nil, glueBadInput
	}

	_, _, err := glueScan(context.Background(), t, st, handlers)
	assertGlueOutcome(t, err, outcomeError)
	assertAPICode(t, err, "InvalidInputException")
	if *warns != 1 {
		t.Errorf("glue:GetWorkflow warnings = %d; want 1", *warns)
	}
}

// A catalog listed without a ResourceArn is stored without tags, never sent
// to GetTags, and fails the scan so the gap is seen.
func TestScanGlue_CatalogWithoutARN(t *testing.T) {
	st := newTestStore(t)
	calls := newGlueCalls()
	handlers := glueHandlers(calls)
	handlers["GetCatalogs"] = func(any) (any, error) {
		return &glue.GetCatalogsOutput{CatalogList: []gluetypes.Catalog{{CatalogId: sdkaws.String("id-noarn")}}}, nil
	}

	_, _, err := glueScan(context.Background(), t, st, handlers)
	if !errors.Is(err, errGlueCatalogNoARN) {
		t.Fatalf("err = %v; want errGlueCatalogNoARN", err)
	}
	r, ok := glueRowOf(t, st, TypeGlueCatalog, glueTestARNPrefix+"catalog/id-noarn")
	if !ok {
		t.Fatal("catalog without an ARN not stored")
	}
	if r.TagsJSON != nil {
		t.Errorf("TagsJSON = %q; want nil", *r.TagsJSON)
	}
	for _, arn := range calls.tagARNs {
		if strings.Contains(arn, ":catalog") {
			t.Errorf("GetTags sent catalog ARN %q", arn)
		}
	}
}

// A registry ListSchemas no longer finds is skipped without a warning; the
// other registry's schema is stored.
func TestScanGlue_SchemaRegistryGoneIsSkipped(t *testing.T) {
	st := newTestStore(t)
	warns := countOpWarnings(st, "glue:ListSchemas")
	calls := newGlueCalls()
	handlers := glueHandlers(calls)
	next := handlers["ListSchemas"]
	handlers["ListSchemas"] = func(in any) (any, error) {
		if strings.HasSuffix(sv(in.(*glue.ListSchemasInput).RegistryId.RegistryArn), "/bare") {
			return nil, apiErr("EntityNotFoundException", "registry gone")
		}
		return next(in)
	}

	if _, _, err := glueScan(context.Background(), t, st, handlers); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if *warns != 0 {
		t.Errorf("warnings = %d; want 0", *warns)
	}
	if rows := rowsByNativeID(t, st, TypeGlueSchema); len(rows) != 1 {
		t.Errorf("schemas stored = %d; want the 1 of the registry still found", len(rows))
	}
}

// Items that cannot be stored are warned about in one warning per op that
// names the first, or says it has none; their siblings are stored.
func TestScanGlue_UnstorableItemsWarn(t *testing.T) {
	for _, tc := range []struct {
		name, op, key, typ string
		answer             func(any) (any, error)
	}{
		{"job without a name", "GetJobs", "(an item with no id)", TypeGlueJob, func(any) (any, error) {
			return &glue.GetJobsOutput{Jobs: []gluetypes.Job{{Name: sdkaws.String("tagged")}, {}}}, nil
		}},
		{"trigger without a name", "GetTriggers", "trg-1", TypeGlueTrigger, func(any) (any, error) {
			return &glue.GetTriggersOutput{Triggers: []gluetypes.Trigger{{Name: sdkaws.String("tagged")}, {Id: sdkaws.String("trg-1")}}}, nil
		}},
		{"ML transform without an id", "GetMLTransforms", "nameonly", TypeGlueMLTransform, func(any) (any, error) {
			return &glue.GetMLTransformsOutput{Transforms: []gluetypes.MLTransform{{TransformId: sdkaws.String("tagged")}, {Name: sdkaws.String("nameonly")}}}, nil
		}},
		{"registry without an ARN", "ListRegistries", "reg", TypeGlueRegistry, func(any) (any, error) {
			return &glue.ListRegistriesOutput{Registries: []gluetypes.RegistryListItem{{RegistryArn: sdkaws.String(glueARN("registry")("tagged"))}, {RegistryName: sdkaws.String("reg")}}}, nil
		}},
		{"database without a name", "GetDatabases", "(a database with no catalog id)", TypeGlueDatabase, func(any) (any, error) {
			return &glue.GetDatabasesOutput{DatabaseList: []gluetypes.Database{{Name: sdkaws.String("tagged")}, {}}}, nil
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			warnings := bacWarnings(st, "glue:"+tc.op)
			calls := newGlueCalls()
			handlers := glueHandlers(calls)
			handlers[tc.op] = tc.answer

			if _, _, err := glueScan(context.Background(), t, st, handlers); err != nil {
				t.Fatalf("scan: %v", err)
			}
			if got := len(rowsByNativeID(t, st, tc.typ)); got != 1 {
				t.Errorf("%s stored = %d; want 1", tc.typ, got)
			}
			if len(*warnings) != 1 {
				t.Fatalf("warnings = %v; want 1", *warnings)
			}
			if w := (*warnings)[0]; !strings.HasSuffix(w.Message, "first "+tc.key) || w.Scope != testAccountID+"/"+testRegion {
				t.Errorf("warning = %q (Scope %q); want it to name %q first", w.Message, w.Scope, tc.key)
			}
		})
	}
}

// A repeated token ends the listing as an error, keeping its pages; the
// hand-paged catalog and integration listings included.
func TestScanGlue_RepeatedTokenKeepsPages(t *testing.T) {
	for _, tc := range []struct {
		op, typ, nativeID string
		out               any
	}{
		{"GetCatalogs", TypeGlueCatalog, glueTestARNPrefix + "catalog/id-tagged", &glue.GetCatalogsOutput{NextToken: sdkaws.String("same"), CatalogList: []gluetypes.Catalog{
			{CatalogId: sdkaws.String("id-tagged"), ResourceArn: sdkaws.String(glueARN("catalog")("tagged"))},
		}}},
		{"DescribeIntegrations", TypeGlueIntegration, glueTestARNPrefix + "integration:tagged", &glue.DescribeIntegrationsOutput{Marker: sdkaws.String("same"), Integrations: []gluetypes.Integration{
			{IntegrationArn: sdkaws.String(glueTestARNPrefix + "integration:tagged")},
		}}},
		{"ListWorkflows", TypeGlueWorkflow, glueARN("workflow")("tagged"), &glue.ListWorkflowsOutput{NextToken: sdkaws.String("same"), Workflows: []string{"tagged"}}},
	} {
		t.Run(tc.op, func(t *testing.T) {
			st := newTestStore(t)
			handlers := glueHandlers(newGlueCalls())
			handlers[tc.op] = func(any) (any, error) { return tc.out, nil }

			_, _, err := glueScan(context.Background(), t, st, handlers)
			if !errors.Is(err, errRepeatedNextToken) {
				t.Fatalf("err = %v; want errRepeatedNextToken", err)
			}
			if _, ok := glueRowOf(t, st, tc.typ, tc.nativeID); !ok {
				t.Errorf("%s from the pages read missing", tc.typ)
			}
		})
	}
}

// Once ctx ends no further phase starts, and the stop is reported once,
// naming the phases not run, although the phase in flight saw it in its
// listing and its tag reads; a hard error beside it is kept.
func TestScanGlue_ContextEndStopsOnce(t *testing.T) {
	st := newTestStore(t)
	calls := newGlueCalls()
	handlers := glueHandlers(calls)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// The registry listing ends on its second page, as an SDK call does
	// when ctx ends; its first page's registry is still tag-read.
	registries := handlers["ListRegistries"]
	handlers["ListRegistries"] = func(in any) (any, error) {
		if in.(*glue.ListRegistriesInput).NextToken != nil {
			cancel()
			return nil, context.Canceled
		}
		return registries(in)
	}

	_, _, err := glueScan(ctx, t, st, handlers)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v; want context.Canceled", err)
	}
	stops := 0
	for _, m := range bacTopMembers(err) {
		if errors.Is(m, context.Canceled) {
			stops++
		}
	}
	if stops != 1 {
		t.Errorf("members carrying the stop = %d; want 1 (err %v)", stops, err)
	}
	if n := calls.count("GetJobs"); n != 0 {
		t.Errorf("GetJobs called %d times after ctx ended", n)
	}
	// Registries are phase 5 of 28, so 23 never started.
	if !strings.Contains(err.Error(), "glue: 23 phases not run") {
		t.Errorf("err = %v; want it to say 23 phases not run", err)
	}
}

// An NXDOMAIN after rows were stored is a failure of this scan, not a region
// gap: the counts are kept. A tag read's NXDOMAIN is one too. With nothing
// stored it stays region-unavailable.
func TestScanGlue_NXDOMAIN(t *testing.T) {
	nx := &net.DNSError{Err: "no such host", Name: "glue.us-east-1.amazonaws.com", IsNotFound: true}
	t.Run("later phase after rows were stored", func(t *testing.T) {
		st := newTestStore(t)
		handlers := glueHandlers(newGlueCalls())
		handlers["GetCrawlers"] = func(any) (any, error) { return nil, nx }
		total, _, err := glueScan(context.Background(), t, st, handlers)
		assertGlueOutcome(t, err, outcomeError)
		if total == 0 {
			t.Error("total = 0; want the stored rows counted")
		}
	})
	t.Run("tag read", func(t *testing.T) {
		st := newTestStore(t)
		calls := newGlueCalls()
		calls.errs[glueARN("job")("tagged")] = nx
		_, _, err := glueScan(context.Background(), t, st, glueHandlers(calls))
		assertGlueOutcome(t, err, outcomeError)
	})
	t.Run("nothing stored", func(t *testing.T) {
		st := newTestStore(t)
		warns := countWarnings(st)
		acct := newTestAccount(testAccountID)
		acct.cfg = opStubConfig(nil, nx)
		_, _, err := scanGlue(context.Background(), acct, testRegion, st, testScanID)
		assertGlueOutcome(t, err, outcomeUnavailable)
		if *warns != 0 {
			t.Errorf("warnings = %d; want 0", *warns)
		}
	})
}

// assertGlueOutcome fails unless err is non-nil and classifies as want: a nil
// error classifies as outcomeError, so the classification alone passes
// vacuously for a lost error.
func assertGlueOutcome(t *testing.T, err error, want serviceOutcome) {
	t.Helper()
	if err == nil {
		t.Fatalf("err = nil; want an error classified %v", want)
	}
	if got := classifyServiceError(err); got != want {
		t.Errorf("classifyServiceError = %v; want %v (err %v)", got, want, err)
	}
}

func glueTaggedTypeOf(t *testing.T, typ string) glueTagged {
	t.Helper()
	for _, f := range glueTaggedTypes() {
		if f.typ == typ {
			return f
		}
	}
	t.Fatalf("%s is not a glue tagged type", typ)
	return glueTagged{}
}

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
	"github.com/aws/aws-sdk-go-v2/service/appconfig"
	actypes "github.com/aws/aws-sdk-go-v2/service/appconfig/types"
	"github.com/icearp/disco-cli/store"
)

const (
	acTestARNPrefix = "arn:aws:appconfig:" + testRegion + ":" + testAccountID + ":"
	// acTestPerApp is the rows under one application: two environments and
	// two profiles, each environment's two deployments and each profile's
	// two hosted versions.
	acTestPerApp = 2 + 2 + 2*2 + 2*2
	// acTestTotal is two applications with their rows, and two of each
	// account-wide listing.
	acTestTotal = 2*(1+acTestPerApp) + 4*2
	// acTestTagReads is every row but the hosted versions and the two
	// AWS-owned rows.
	acTestTagReads = acTestTotal - 2*2*2 - 2
)

var (
	acTestThrottle = apiErr("ThrottlingException", "Rate exceeded")
	acTestApps     = []string{"a1", "a2"}
)

func acTestAppARN(app string) string { return acTestARNPrefix + "application/" + app }

// acTestRow is a row of the fixture: its type and ARN, whether its tag read
// returns tags, whether it is read at all, and whether it is AWS-owned.
type acTestRow struct {
	typ, arn              string
	tagged, read, managed bool
}

// acTestRows is every row the fixture's scan stores. Each listing serves two
// pages of one element; the first page's element is tagged.
func acTestRows() []acTestRow {
	var rows []acTestRow
	add := func(typ, arn string, first bool) {
		rows = append(rows, acTestRow{typ: typ, arn: arn, tagged: first, read: true})
	}
	for i, app := range acTestApps {
		appARN := acTestAppARN(app)
		add(TypeAppConfigApplication, appARN, i == 0)
		for j, n := range []string{"1", "2"} {
			env := appARN + "/environment/e" + n
			add(TypeAppConfigEnvironment, env, j == 0)
			for k, d := range []string{"1", "2"} {
				add(TypeAppConfigDeployment, env+"/deployment/"+d, k == 0)
			}
			cp := appARN + "/configurationprofile/c" + n
			add(TypeAppConfigConfigurationProfile, cp, j == 0)
			for _, v := range []string{"1", "2"} {
				rows = append(rows, acTestRow{typ: TypeAppConfigHostedConfigurationVersion, arn: cp + "/hostedconfigurationversion/" + v})
			}
		}
	}
	add(TypeAppConfigDeploymentStrategy, acTestARNPrefix+"deploymentstrategy/ds1", true)
	rows = append(rows, acTestRow{typ: TypeAppConfigDeploymentStrategy, arn: acTestARNPrefix + "deploymentstrategy/AppConfig.AllAtOnce", managed: true})
	add(TypeAppConfigExtension, acTestARNPrefix+"extension/ext1/1", true)
	rows = append(rows, acTestRow{typ: TypeAppConfigExtension, arn: acTestARNPrefix + "extension/AWS.AppConfig.FeatureFlags/1", managed: true})
	add(TypeAppConfigExtensionAssociation, acTestARNPrefix+"extensionassociation/ea1", true)
	add(TypeAppConfigExtensionAssociation, acTestARNPrefix+"extensionassociation/ea2", false)
	add(TypeAppConfigExperimentDefinition, acTestAppARN("a1")+"/experimentdefinition/x1", true)
	add(TypeAppConfigExperimentDefinition, acTestAppARN("a2")+"/experimentdefinition/x2", false)
	return rows
}

// acTestTypeRows is the number of fixture rows of typ.
func acTestTypeRows(typ string) int {
	n := 0
	for _, r := range acTestRows() {
		if r.typ == typ {
			n++
		}
	}
	return n
}

// acTestTagReadARNs is the ARN of every row whose tags are read, sorted.
func acTestTagReadARNs() []string {
	var arns []string
	for _, r := range acTestRows() {
		if r.read {
			arns = append(arns, r.arn)
		}
	}
	slices.Sort(arns)
	return arns
}

// acCalls records the calls of a scan and answers the tag reads, by ARN,
// with tags or errs.
type acCalls struct {
	mu         sync.Mutex
	maxResults map[string][]int32
	parents    map[string][]string
	tagARNs    []string
	tags       map[string]map[string]string
	errs       map[string]error
}

// newACCalls tags every tagged fixture row with its type.
func newACCalls() *acCalls {
	c := &acCalls{maxResults: map[string][]int32{}, parents: map[string][]string{}, tags: map[string]map[string]string{}, errs: map[string]error{}}
	for _, r := range acTestRows() {
		if r.tagged {
			c.tags[r.arn] = map[string]string{"type": r.typ}
		}
	}
	return c
}

// record notes op's page size and, on a parent's first page, the parent:
// the application id, then the environment or profile id after a "/".
func (c *acCalls) record(op string, in any) {
	c.mu.Lock()
	defer c.mu.Unlock()
	var n int32
	if p := bacField[*int32](in, "MaxResults"); p != nil {
		n = *p
	}
	c.maxResults[op] = append(c.maxResults[op], n)
	app := sv(bacField[*string](in, "ApplicationId"))
	if app == "" || bacField[*string](in, "NextToken") != nil {
		return
	}
	if child := sv(bacField[*string](in, "EnvironmentId")) + sv(bacField[*string](in, "ConfigurationProfileId")); child != "" {
		app += "/" + child
	}
	c.parents[op] = append(c.parents[op], app)
}

func (c *acCalls) count(op string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.maxResults[op])
}

func (c *acCalls) listedParents(op string) []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return slices.Sorted(slices.Values(c.parents[op]))
}

func (c *acCalls) listTags(in any) (any, error) {
	arn := sv(in.(*appconfig.ListTagsForResourceInput).ResourceArn)
	c.mu.Lock()
	defer c.mu.Unlock()
	c.tagARNs = append(c.tagARNs, arn)
	if err := c.errs[arn]; err != nil {
		return nil, err
	}
	return &appconfig.ListTagsForResourceOutput{Tags: c.tags[arn]}, nil
}

func (c *acCalls) readARNs() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return slices.Sorted(slices.Values(c.tagARNs))
}

// twoPages answers op with page's output: first for the first page, which
// names a next one, and not first for the second, which ends the listing.
func (c *acCalls) twoPages(op string, page func(in any, first bool, next *string) any) func(any) (any, error) {
	return func(in any) (any, error) {
		c.record(op, in)
		first := bacField[*string](in, "NextToken") == nil
		var next *string
		if first {
			next = sdkaws.String("p2")
		}
		return page(in, first, next), nil
	}
}

// pick is a on the first page and b on the second.
func pick(first bool, a, b string) string {
	if first {
		return a
	}
	return b
}

// acHandlers answers every op the scan calls with the fixture of acTestRows.
func acHandlers(c *acCalls) map[string]func(in any) (any, error) {
	return map[string]func(in any) (any, error){
		"ListApplications": c.twoPages("ListApplications", func(_ any, first bool, next *string) any {
			id := pick(first, "a1", "a2")
			return &appconfig.ListApplicationsOutput{NextToken: next, Items: []actypes.Application{{Id: &id, Name: sdkaws.String("app-" + id)}}}
		}),
		"ListEnvironments": c.twoPages("ListEnvironments", func(_ any, first bool, next *string) any {
			return &appconfig.ListEnvironmentsOutput{NextToken: next, Items: []actypes.Environment{{Id: sdkaws.String(pick(first, "e1", "e2"))}}}
		}),
		"ListConfigurationProfiles": c.twoPages("ListConfigurationProfiles", func(_ any, first bool, next *string) any {
			return &appconfig.ListConfigurationProfilesOutput{NextToken: next, Items: []actypes.ConfigurationProfileSummary{{Id: sdkaws.String(pick(first, "c1", "c2"))}}}
		}),
		"ListDeployments": c.twoPages("ListDeployments", func(_ any, first bool, next *string) any {
			n := int32(2)
			if first {
				n = 1
			}
			return &appconfig.ListDeploymentsOutput{NextToken: next, Items: []actypes.DeploymentSummary{{DeploymentNumber: n, ConfigurationName: sdkaws.String("cfg")}}}
		}),
		"ListHostedConfigurationVersions": c.twoPages("ListHostedConfigurationVersions", func(_ any, first bool, next *string) any {
			n := int32(2)
			if first {
				n = 1
			}
			return &appconfig.ListHostedConfigurationVersionsOutput{NextToken: next, Items: []actypes.HostedConfigurationVersionSummary{{VersionNumber: n}}}
		}),
		"ListDeploymentStrategies": c.twoPages("ListDeploymentStrategies", func(_ any, first bool, next *string) any {
			// A customer may name a strategy with the predefined prefix; its
			// id decides whether AWS owns it.
			id := pick(first, "ds1", "AppConfig.AllAtOnce")
			return &appconfig.ListDeploymentStrategiesOutput{NextToken: next, Items: []actypes.DeploymentStrategy{{Id: &id, Name: sdkaws.String(pick(first, "AppConfig.Mine", id))}}}
		}),
		"ListExtensions": c.twoPages("ListExtensions", func(_ any, first bool, next *string) any {
			id := pick(first, "ext1", "AWS.AppConfig.FeatureFlags")
			return &appconfig.ListExtensionsOutput{NextToken: next, Items: []actypes.ExtensionSummary{{Id: &id, Arn: sdkaws.String(acTestARNPrefix + "extension/" + id + "/1")}}}
		}),
		"ListExtensionAssociations": c.twoPages("ListExtensionAssociations", func(_ any, first bool, next *string) any {
			return &appconfig.ListExtensionAssociationsOutput{NextToken: next, Items: []actypes.ExtensionAssociationSummary{{Id: sdkaws.String(pick(first, "ea1", "ea2"))}}}
		}),
		"ListExperimentDefinitions": c.twoPages("ListExperimentDefinitions", func(_ any, first bool, next *string) any {
			return &appconfig.ListExperimentDefinitionsOutput{NextToken: next, Items: []actypes.ExperimentDefinitionSummary{{
				ApplicationId: sdkaws.String(pick(first, "a1", "a2")), Id: sdkaws.String(pick(first, "x1", "x2")),
			}}}
		}),
		"ListTagsForResource": c.listTags,
	}
}

// acTestListOps is every list op the scan calls.
var acTestListOps = []string{
	"ListApplications", "ListEnvironments", "ListConfigurationProfiles", "ListDeployments", "ListHostedConfigurationVersions",
	"ListDeploymentStrategies", "ListExtensions", "ListExtensionAssociations", "ListExperimentDefinitions",
}

// acScan runs the scan's real entry point against handlers; every op they do
// not answer is denied.
func acScan(ctx context.Context, st *store.Store, handlers map[string]func(in any) (any, error)) (int, int, error) {
	acct := newTestAccount(testAccountID)
	acct.cfg = opStubConfig(handlers, acDenied)
	return scanAppConfig(ctx, acct, testRegion, st, testScanID)
}

func acWarningOps(w []store.ScanWarning) []string {
	var ops []string
	for _, x := range w {
		ops = append(ops, x.Service)
	}
	return ops
}

// Every listing stores both its pages (each child listing for every parent
// stored), each row with the tags its read returns, nil for a resource
// without tags. ListTagsForResource is sent exactly the ARNs of the rows
// other than hosted versions and the AWS-owned strategy and extension, which
// are stored untagged and managed; a customer strategy named with the
// predefined prefix is neither. Every listing is sent the model's page cap
// of 50. No error, no warning.
func TestScanAppConfig_StoresTagsOfEveryType(t *testing.T) {
	st := newTestStore(t)
	warns := rhWarnRecorder(st)
	calls := newACCalls()

	total, inserted, err := acScan(context.Background(), st, acHandlers(calls))
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != acTestTotal || inserted != acTestTotal {
		t.Errorf("total, inserted = %d, %d; want %d, %d", total, inserted, acTestTotal, acTestTotal)
	}
	if w := warns(); len(w) != 0 {
		t.Errorf("warnings = %v; want none", w)
	}
	byType := map[string]int{}
	for _, want := range acTestRows() {
		byType[want.typ]++
		r, ok := rowsByNativeID(t, st, want.typ)[want.arn]
		if !ok {
			t.Errorf("%s %s missing", want.typ, want.arn)
			continue
		}
		if r.ManagedByProvider != want.managed || sv(r.Region) != testRegion {
			t.Errorf("%s managed, region = %v, %q; want %v, %q", want.arn, r.ManagedByProvider, sv(r.Region), want.managed, testRegion)
		}
		if want.tagged {
			assertTagsMap(t, r.TagsJSON, map[string]string{"type": want.typ})
		} else if r.TagsJSON != nil {
			t.Errorf("%s TagsJSON = %q; want nil", want.arn, *r.TagsJSON)
		}
	}
	for typ, n := range byType {
		if got := len(rowsByNativeID(t, st, typ)); got != n {
			t.Errorf("%s rows = %d; want %d", typ, got, n)
		}
	}
	if got := calls.readARNs(); len(got) != acTestTagReads || !slices.Equal(got, acTestTagReadARNs()) {
		t.Errorf("ListTagsForResource ARNs = %v\nwant %v", got, acTestTagReadARNs())
	}
	for op, want := range map[string][]string{
		"ListEnvironments":                {"a1", "a2"},
		"ListConfigurationProfiles":       {"a1", "a2"},
		"ListDeployments":                 {"a1/e1", "a1/e2", "a2/e1", "a2/e2"},
		"ListHostedConfigurationVersions": {"a1/c1", "a1/c2", "a2/c1", "a2/c2"},
	} {
		if got := calls.listedParents(op); !slices.Equal(got, want) {
			t.Errorf("%s parents = %v; want %v", op, got, want)
		}
	}
	for _, op := range acTestListOps {
		if calls.count(op) == 0 {
			t.Errorf("%s not called", op)
		}
		for _, n := range calls.maxResults[op] {
			if n != 50 {
				t.Errorf("%s MaxResults = %d; want 50", op, n)
			}
		}
	}
}

// A rescan stores the same rows without inserting any.
func TestScanAppConfig_RescanInsertsNone(t *testing.T) {
	st := newTestStore(t)
	if _, _, err := acScan(context.Background(), st, acHandlers(newACCalls())); err != nil {
		t.Fatalf("first scan: %v", err)
	}
	total, inserted, err := acScan(context.Background(), st, acHandlers(newACCalls()))
	if err != nil {
		t.Fatalf("rescan: %v", err)
	}
	if total != acTestTotal || inserted != 0 {
		t.Errorf("rescan total, inserted = %d, %d; want %d, 0", total, inserted, acTestTotal)
	}
}

// A failed tag read. ResourceNotFoundException, which the op models, drops
// the row silently, and the children of a dropped parent are not listed. A
// denial keeps the row untagged and warns once under the tag op; a throttle
// keeps it and is transient; any other error keeps it and is an error.
func TestScanAppConfig_TagReadFailures(t *testing.T) {
	type tagCase struct {
		name     string
		arn, typ string
		err      error
		outcome  serviceOutcome // -1: no error
		code     string
		lost     int
		warnOp   string
	}
	env := acTestAppARN("a1") + "/environment/e1"
	for _, x := range []struct {
		arn, typ string
		children int
	}{
		{acTestAppARN("a2"), TypeAppConfigApplication, acTestPerApp},
		{env, TypeAppConfigEnvironment, 2},
		{env + "/deployment/2", TypeAppConfigDeployment, 0},
		{acTestARNPrefix + "deploymentstrategy/ds1", TypeAppConfigDeploymentStrategy, 0},
		{acTestARNPrefix + "extensionassociation/ea1", TypeAppConfigExtensionAssociation, 0},
		{acTestAppARN("a2") + "/experimentdefinition/x2", TypeAppConfigExperimentDefinition, 0},
	} {
		for _, c := range []tagCase{
			{"not found", x.arn, x.typ, apiErr("ResourceNotFoundException", "gone"), -1, "", 1 + x.children, ""},
			{"denied", x.arn, x.typ, acDenied, -1, "", 0, acTagsOp},
			{"transient", x.arn, x.typ, acTestThrottle, outcomeTransient, "ThrottlingException", 0, ""},
			{"other error", x.arn, x.typ, acBadRequest, outcomeError, "BadRequestException", 0, ""},
		} {
			t.Run(x.typ+"/"+c.name, func(t *testing.T) {
				st := newTestStore(t)
				warns := rhWarnRecorder(st)
				calls := newACCalls()
				calls.errs[c.arn] = c.err

				total, _, err := acScan(context.Background(), st, acHandlers(calls))
				if c.outcome < 0 {
					if err != nil {
						t.Fatalf("err = %v; want nil", err)
					}
				} else {
					assertGlueOutcome(t, err, c.outcome)
					assertAPICode(t, err, c.code)
				}
				if want := acTestTotal - c.lost; total != want {
					t.Errorf("total = %d; want %d", total, want)
				}
				r, ok := rowsByNativeID(t, st, c.typ)[c.arn]
				if ok != (c.lost == 0) {
					t.Fatalf("row stored = %v; want %v", ok, c.lost == 0)
				}
				if ok && r.TagsJSON != nil {
					t.Errorf("TagsJSON = %q; want nil", *r.TagsJSON)
				}
				w := warns()
				var want []string
				if c.warnOp != "" {
					want = []string{c.warnOp}
				}
				if got := acWarningOps(w); !slices.Equal(got, want) {
					t.Fatalf("warnings = %v; want %v", w, want)
				}
				for _, x := range w {
					if x.Scope != testAccountID+"/"+testRegion || !strings.Contains(x.Message, c.arn) {
						t.Errorf("warning Scope %q, message %q; want %s/%s naming %s", x.Scope, x.Message, testAccountID, testRegion, c.arn)
					}
				}
			})
		}
	}
}

// Denied tag reads in several phases send one warning for the whole scan;
// the rows are kept.
func TestScanAppConfig_TagDenialsWarnOncePerScan(t *testing.T) {
	st := newTestStore(t)
	warns := rhWarnRecorder(st)
	calls := newACCalls()
	for _, r := range acTestRows() {
		if r.typ == TypeAppConfigEnvironment || r.typ == TypeAppConfigDeployment || r.typ == TypeAppConfigExperimentDefinition {
			calls.errs[r.arn] = acDenied
		}
	}

	total, _, err := acScan(context.Background(), st, acHandlers(calls))
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != acTestTotal {
		t.Errorf("total = %d; want %d", total, acTestTotal)
	}
	if got := acWarningOps(warns()); !slices.Equal(got, []string{acTagsOp}) {
		t.Errorf("warnings = %v; want one under %s", got, acTagsOp)
	}
}

// A throttled tag read beside a hard failure of the same op is demoted to
// one warning, so the hard error decides the outcome.
func TestScanAppConfig_TagThrottleDemotedBesideHardError(t *testing.T) {
	st := newTestStore(t)
	warns := countOpWarnings(st, acTagsOp)
	calls := newACCalls()
	calls.errs[acTestAppARN("a1")+"/environment/e1"] = acTestThrottle
	calls.errs[acTestARNPrefix+"extensionassociation/ea2"] = acBadRequest

	_, _, err := acScan(context.Background(), st, acHandlers(calls))
	assertGlueOutcome(t, err, outcomeError)
	assertAPICode(t, err, "BadRequestException")
	if *warns != 1 {
		t.Errorf("%s warnings = %d; want 1", acTagsOp, *warns)
	}
}

// A failed write stops the scan: no warning about the rows it lost (a denied
// tag read among them), no later phase runs, and the earlier phases' rows
// stay stored. A failed environment write lists no deployments.
func TestScanAppConfig_FailedWriteStopsScan(t *testing.T) {
	t.Run("deployment strategies", func(t *testing.T) {
		st := newTestStore(t)
		warns := rhWarnRecorder(st)
		refuseStoreWrites(t, st, TypeAppConfigDeploymentStrategy)
		calls := newACCalls()
		calls.errs[acTestARNPrefix+"deploymentstrategy/ds1"] = acDenied

		total, _, err := acScan(context.Background(), st, acHandlers(calls))
		if !errors.Is(err, store.ErrStoreWrite) {
			t.Fatalf("err = %v; want store.ErrStoreWrite", err)
		}
		if w := warns(); len(w) != 0 {
			t.Errorf("warnings = %v; want none", w)
		}
		if n := calls.count("ListDeploymentStrategies"); n != 1 {
			t.Errorf("ListDeploymentStrategies called %d times; want 1, none after the failed write", n)
		}
		if n := calls.count("ListExtensions"); n != 0 {
			t.Errorf("ListExtensions called %d times; want 0 after the failed write", n)
		}
		if want := 2 * (1 + acTestPerApp); total != want {
			t.Errorf("total = %d; want %d (the applications and their children)", total, want)
		}
	})
	t.Run("environments", func(t *testing.T) {
		st := newTestStore(t)
		refuseStoreWrites(t, st, TypeAppConfigEnvironment)
		calls := newACCalls()

		_, _, err := acScan(context.Background(), st, acHandlers(calls))
		if !errors.Is(err, store.ErrStoreWrite) {
			t.Fatalf("err = %v; want store.ErrStoreWrite", err)
		}
		for _, op := range []string{"ListConfigurationProfiles", "ListDeployments", "ListHostedConfigurationVersions"} {
			if n := calls.count(op); n != 0 {
				t.Errorf("%s called %d times after the failed environment write", op, n)
			}
		}
	})
}

// A listing's failed page keeps the pages read before it, and its error does
// not stop the later phases. An application not stored has no children read.
func TestScanAppConfig_ListFailureKeepsPagesAndLaterPhases(t *testing.T) {
	for _, c := range []struct {
		op, typ, kept string
		lost          int
	}{
		{"ListApplications", TypeAppConfigApplication, acTestAppARN("a1"), 1 + acTestPerApp},
		{"ListExtensionAssociations", TypeAppConfigExtensionAssociation, acTestARNPrefix + "extensionassociation/ea1", 1},
	} {
		t.Run(c.op, func(t *testing.T) {
			st := newTestStore(t)
			calls := newACCalls()
			handlers := acHandlers(calls)
			handlers[c.op] = failOnCall(2, acBadRequest, handlers[c.op])

			total, _, err := acScan(context.Background(), st, handlers)
			assertGlueOutcome(t, err, outcomeError)
			assertAPICode(t, err, "BadRequestException")
			if want := acTestTotal - c.lost; total != want {
				t.Errorf("total = %d; want %d", total, want)
			}
			if _, ok := rowsByNativeID(t, st, c.typ)[c.kept]; !ok {
				t.Errorf("%s row from the page before the failure missing", c.typ)
			}
			if got := len(rowsByNativeID(t, st, TypeAppConfigExperimentDefinition)); got != 2 {
				t.Errorf("experiment definitions (the last phase) = %d; want 2", got)
			}
		})
	}
}

// A repeated NextToken ends the listing as an error, keeping its pages.
func TestScanAppConfig_RepeatedTokenKeepsPages(t *testing.T) {
	st := newTestStore(t)
	handlers := acHandlers(newACCalls())
	handlers["ListExtensionAssociations"] = func(in any) (any, error) {
		id := pick(bacField[*string](in, "NextToken") == nil, "ea1", "ea2")
		return &appconfig.ListExtensionAssociationsOutput{NextToken: sdkaws.String("same"), Items: []actypes.ExtensionAssociationSummary{{Id: &id}}}, nil
	}

	total, _, err := acScan(context.Background(), st, handlers)
	if !errors.Is(err, errRepeatedNextToken) {
		t.Fatalf("err = %v; want errRepeatedNextToken", err)
	}
	if total != acTestTotal {
		t.Errorf("total = %d; want %d", total, acTestTotal)
	}
}

// One parent's failed child listing costs the other parents none of their
// rows: a parent deleted since it was listed is skipped silently, a denial
// warns once under the op, and any other error is returned.
func TestScanAppConfig_PerParentListingFailures(t *testing.T) {
	a1 := acTestAppARN("a1")
	for _, p := range []struct {
		op, typ string
		fails   func(in any) bool
		// failed starts the NativeIDs of the failed parent's rows of typ.
		failed string
		lost   int
	}{
		{"ListEnvironments", TypeAppConfigEnvironment, func(in any) bool { return sv(bacField[*string](in, "ApplicationId")) == "a1" }, a1 + "/environment/", 2 + 2*2},
		{"ListConfigurationProfiles", TypeAppConfigConfigurationProfile, func(in any) bool { return sv(bacField[*string](in, "ApplicationId")) == "a1" }, a1 + "/configurationprofile/", 2 + 2*2},
		{"ListDeployments", TypeAppConfigDeployment, func(in any) bool {
			return sv(bacField[*string](in, "ApplicationId")) == "a1" && sv(bacField[*string](in, "EnvironmentId")) == "e1"
		}, a1 + "/environment/e1/", 2},
		{"ListHostedConfigurationVersions", TypeAppConfigHostedConfigurationVersion, func(in any) bool {
			return sv(bacField[*string](in, "ApplicationId")) == "a1" && sv(bacField[*string](in, "ConfigurationProfileId")) == "c1"
		}, a1 + "/configurationprofile/c1/", 2},
	} {
		for _, c := range []struct {
			name      string
			err       error
			code      string
			wantWarns int
		}{
			{"parent deleted", apiErr("ResourceNotFoundException", "gone"), "", 0},
			{"denied", acDenied, "", 1},
			{"other error", acBadRequest, "BadRequestException", 0},
		} {
			t.Run(p.op+"/"+c.name, func(t *testing.T) {
				st := newTestStore(t)
				warns := countOpWarnings(st, "appconfig:"+p.op)
				handlers := acHandlers(newACCalls())
				list := handlers[p.op]
				handlers[p.op] = func(in any) (any, error) {
					if p.fails(in) {
						return nil, c.err
					}
					return list(in)
				}

				total, _, err := acScan(context.Background(), st, handlers)
				if c.code == "" {
					if err != nil {
						t.Fatalf("err = %v; want nil", err)
					}
				} else {
					assertGlueOutcome(t, err, outcomeError)
					assertAPICode(t, err, c.code)
				}
				if want := acTestTotal - p.lost; total != want {
					t.Errorf("total = %d; want %d", total, want)
				}
				if *warns != c.wantWarns {
					t.Errorf("warnings = %d; want %d", *warns, c.wantWarns)
				}
				rows := rowsByNativeID(t, st, p.typ)
				for arn := range rows {
					if strings.HasPrefix(arn, p.failed) {
						t.Errorf("%s stored from the failed parent", arn)
					}
				}
				// Every parent lists two rows of typ.
				if want := acTestTypeRows(p.typ) - 2; len(rows) != want {
					t.Errorf("%s rows = %d; want %d, the other parents'", p.typ, len(rows), want)
				}
			})
		}
	}
}

// An element without the id its NativeID is built from is not stored, is not
// sent to the tag op, and is warned about once under its listing, keyed by
// its id or name, or by the fallback text when it has neither. An
// application not stored has no children read.
func TestScanAppConfig_ElementWithoutID(t *testing.T) {
	for _, c := range []struct {
		name, op, key string
		answer        any
		lost          int
	}{
		{
			"application", "ListApplications", "shop",
			&appconfig.ListApplicationsOutput{Items: []actypes.Application{{Name: sdkaws.String("shop")}}}, 2 * (1 + acTestPerApp),
		},
		{
			"environment with neither id nor name", "ListEnvironments", acNoIdentifier,
			&appconfig.ListEnvironmentsOutput{Items: []actypes.Environment{{}}}, 2 * (2 + 2*2),
		},
		{
			"extension", "ListExtensions", "ext9",
			&appconfig.ListExtensionsOutput{Items: []actypes.ExtensionSummary{{Id: sdkaws.String("ext9")}}}, 2,
		},
		{
			"extension association", "ListExtensionAssociations", acTestAppARN("a1"),
			&appconfig.ListExtensionAssociationsOutput{Items: []actypes.ExtensionAssociationSummary{{ResourceArn: sdkaws.String(acTestAppARN("a1"))}}}, 2,
		},
		{
			"experiment definition", "ListExperimentDefinitions", "x9",
			&appconfig.ListExperimentDefinitionsOutput{Items: []actypes.ExperimentDefinitionSummary{{Id: sdkaws.String("x9")}}}, 2,
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := rhWarnRecorder(st)
			calls := newACCalls()
			handlers := acHandlers(calls)
			handlers[c.op] = func(in any) (any, error) {
				calls.record(c.op, in)
				return c.answer, nil
			}

			total, _, err := acScan(context.Background(), st, handlers)
			if err != nil {
				t.Fatalf("scan: %v", err)
			}
			if want := acTestTotal - c.lost; total != want {
				t.Errorf("total = %d; want %d", total, want)
			}
			w := warns()
			if len(w) != 1 || w[0].Service != "appconfig:"+c.op || !strings.Contains(w[0].Message, c.key) {
				t.Fatalf("warnings = %v; want one under appconfig:%s naming %s", w, c.op, c.key)
			}
			if c.op == "ListApplications" {
				for _, op := range []string{"ListEnvironments", "ListConfigurationProfiles", "ListDeployments", "ListHostedConfigurationVersions"} {
					if n := calls.count(op); n != 0 {
						t.Errorf("%s called %d times; want none", op, n)
					}
				}
			}
			for _, arn := range calls.readARNs() {
				if strings.HasSuffix(arn, "/") || strings.HasSuffix(arn, ":") {
					t.Errorf("an ARN without an id was sent to ListTagsForResource: %s", arn)
				}
			}
		})
	}
}

// A GovCloud or China account's built ARNs, and an ARN a listing returns,
// reach the tag op unchanged.
func TestScanAppConfig_TagReadsKeepPartitionARNs(t *testing.T) {
	for _, pf := range partitionFixtures {
		t.Run(pf.partition, func(t *testing.T) {
			st := newTestStore(t)
			calls := newACCalls()
			app := partitionARN(pf.partition, "appconfig", pf.region, "application/a1")
			ext := partitionARN(pf.partition, "appconfig", pf.region, "extension/ext1/1")
			calls.tags[app] = map[string]string{"env": "prod"}
			handlers := acHandlers(calls)
			handlers["ListExtensions"] = func(in any) (any, error) {
				calls.record("ListExtensions", in)
				return &appconfig.ListExtensionsOutput{Items: []actypes.ExtensionSummary{{Id: sdkaws.String("ext1"), Arn: &ext}}}, nil
			}
			acct := partitionAccount(pf.partition)
			acct.cfg = opStubConfig(handlers, acDenied)

			if _, _, err := scanAppConfig(context.Background(), acct, pf.region, st, testScanID); err != nil {
				t.Fatalf("scan: %v", err)
			}
			read := calls.readARNs()
			for _, want := range []string{app, app + "/environment/e1/deployment/1", ext, partitionARN(pf.partition, "appconfig", pf.region, "deploymentstrategy/ds1")} {
				if !slices.Contains(read, want) {
					t.Errorf("ListTagsForResource ARNs lack %s: %v", want, read)
				}
			}
			assertTagsMap(t, rowsByNativeID(t, st, TypeAppConfigApplication)[app].TagsJSON, map[string]string{"env": "prod"})
		})
	}
}

// Once ctx ends, the stop is one member of the error, although the phase in
// flight saw it in its listing and its tag reads, and the later phases are
// not run.
func TestScanAppConfig_ContextEndReportedOnce(t *testing.T) {
	st := newTestStore(t)
	calls := newACCalls()
	handlers := acHandlers(calls)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	list := handlers["ListExtensions"]
	handlers["ListExtensions"] = func(in any) (any, error) {
		if bacField[*string](in, "NextToken") != nil {
			cancel()
			return nil, context.Canceled
		}
		return list(in)
	}

	_, _, err := acScan(ctx, st, handlers)
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
	if n := calls.count("ListExtensionAssociations"); n != 0 {
		t.Errorf("ListExtensionAssociations called %d times after ctx ended", n)
	}
}

// An NXDOMAIN after rows were stored is a failure of this scan, not a region
// gap: the counts are kept. With nothing stored it stays region-unavailable.
func TestScanAppConfig_NXDOMAIN(t *testing.T) {
	nx := &net.DNSError{Err: "no such host", Name: "appconfig.us-east-1.amazonaws.com", IsNotFound: true}
	t.Run("one listing after others stored rows", func(t *testing.T) {
		st := newTestStore(t)
		handlers := acHandlers(newACCalls())
		handlers["ListExtensions"] = func(any) (any, error) { return nil, nx }
		total, _, err := acScan(context.Background(), st, handlers)
		assertGlueOutcome(t, err, outcomeError)
		if total != acTestTotal-2 {
			t.Errorf("total = %d; want the %d other rows counted", total, acTestTotal-2)
		}
	})
	t.Run("tag read after the listing", func(t *testing.T) {
		st := newTestStore(t)
		calls := newACCalls()
		calls.errs[acTestARNPrefix+"extensionassociation/ea2"] = nx
		_, _, err := acScan(context.Background(), st, acHandlers(calls))
		assertGlueOutcome(t, err, outcomeError)
	})
	t.Run("nothing stored", func(t *testing.T) {
		st := newTestStore(t)
		warns := rhWarnRecorder(st)
		acct := newTestAccount(testAccountID)
		acct.cfg = opStubConfig(nil, nx)
		_, _, err := scanAppConfig(context.Background(), acct, testRegion, st, testScanID)
		assertGlueOutcome(t, err, outcomeUnavailable)
		if w := warns(); len(w) != 0 {
			t.Errorf("warnings = %v; want none", w)
		}
	})
}

// The experiment definition phase runs last: when ListExperimentDefinitions
// is called, every other listing has already been called.
func TestScanAppConfig_ExperimentDefinitionPhaseRunsLast(t *testing.T) {
	st := newTestStore(t)
	calls := newACCalls()
	handlers := acHandlers(calls)
	var notYet []string
	list := handlers["ListExperimentDefinitions"]
	handlers["ListExperimentDefinitions"] = func(in any) (any, error) {
		for _, op := range acTestListOps {
			if op != "ListExperimentDefinitions" && calls.count(op) == 0 {
				notYet = append(notYet, op)
			}
		}
		return list(in)
	}

	if _, _, err := acScan(context.Background(), st, handlers); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if len(notYet) != 0 {
		t.Errorf("listings not yet called when ListExperimentDefinitions ran: %v", notYet)
	}
}

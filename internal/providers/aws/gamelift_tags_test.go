package aws

import (
	"context"
	"errors"
	"fmt"
	"net"
	"slices"
	"strings"
	"sync"
	"testing"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/gamelift"
	gltypes "github.com/aws/aws-sdk-go-v2/service/gamelift/types"
	"github.com/aws/smithy-go"
	"github.com/icearp/disco-cli/store"
)

// The fixture: every account-wide listing serves its "-t" (tagged) item on
// its first page and its "-b" (bare) item on its second; the second page of
// builds also carries build-cn-t, the account's own in aws-cn, and that of
// locations an AWS location, which has no ARN. ListFleets names fleet-t, then
// fleet-b, and DescribeFleetAttributes describes the ids it is sent.
// ListTagsForResource answers with tags for every ARN whose id ends in "-t".
// The fleet children listings answer empty.

var (
	gltUnauthorized = &smithy.GenericAPIError{Code: "UnauthorizedException", Message: "the client failed authentication"}
	gltBadInput     = &smithy.GenericAPIError{Code: "InvalidRequestException", Message: "bad"}
)

// gltKind is one tagged type: its listing op and its ARN's resource kind.
type gltKind struct{ op, typ, kind string }

var gltKinds = []gltKind{
	{"ListAliases", TypeGameLiftAlias, "alias"},
	{"ListBuilds", TypeGameLiftBuild, "build"},
	{"ListContainerFleets", TypeGameLiftContainerFleet, "containerfleet"},
	{"ListContainerGroupDefinitions", TypeGameLiftContainerGroupDefinition, "containergroupdefinition"},
	{"ListFleets", TypeGameLiftFleet, "fleet"},
	{"ListGameServerGroups", TypeGameLiftGameServerGroup, "gameservergroup"},
	{"DescribeGameSessionQueues", TypeGameLiftGameSessionQueue, "gamesessionqueue"},
	{"ListLocations", TypeGameLiftLocation, "location"},
	{"DescribeMatchmakingConfigurations", TypeGameLiftMatchmakingConfiguration, "matchmakingconfiguration"},
	{"DescribeMatchmakingRuleSets", TypeGameLiftMatchmakingRuleSet, "matchmakingruleset"},
	{"ListScripts", TypeGameLiftScript, "script"},
}

// gltID is the id of kind's fixture item n ("t" or "b"). Custom location
// ids start "custom-", as the model's LocationArn pattern requires.
func gltID(kind, n string) string {
	if kind == "location" {
		return "custom-" + n
	}
	return kind + "-" + n
}

// gltARN is the ARN of kind's fixture item n. A container group
// definition's ARN ends in its version, as the model documents.
func gltARN(kind, n string) string {
	arn := arnFor("aws", "gamelift", testRegion, testAccountID, kind+"/"+gltID(kind, n))
	if kind == "containergroupdefinition" {
		arn += ":3"
	}
	return arn
}

func gltCNBuild() string {
	return arnFor("aws-cn", "gamelift", "cn-north-1", testAccountID, "build/build-cn-t")
}

func gltTagged(arn string) bool { return strings.HasSuffix(strings.TrimSuffix(arn, ":3"), "-t") }

type gltRow struct{ typ, arn string }

// gltRows is every row the fixture stores.
func gltRows() []gltRow {
	rows := []gltRow{{TypeGameLiftBuild, gltCNBuild()}}
	for _, k := range gltKinds {
		for _, n := range []string{"t", "b"} {
			rows = append(rows, gltRow{k.typ, gltARN(k.kind, n)})
		}
	}
	return rows
}

// gltCalls records the stubbed calls, and answers ListTagsForResource from
// tags and errs, keyed by ARN; calls run concurrently.
type gltCalls struct {
	mu      sync.Mutex
	ops     map[string]int
	limits  map[string][]string
	ins     map[string][]any
	tagARNs []string
	tags    map[string]map[string]string
	errs    map[string]error
}

func newGLTCalls() *gltCalls {
	c := &gltCalls{ops: map[string]int{}, limits: map[string][]string{}, ins: map[string][]any{}, tags: map[string]map[string]string{}, errs: map[string]error{}}
	for _, r := range gltRows() {
		if gltTagged(r.arn) {
			c.tags[r.arn] = map[string]string{"type": r.typ}
		}
	}
	return c
}

func (c *gltCalls) called(op string, in any) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.ops[op]++
	c.ins[op] = append(c.ins[op], in)
	limit := "none"
	if n := bacField[*int32](in, "Limit"); n != nil {
		limit = fmt.Sprint(*n)
	}
	c.limits[op] = append(c.limits[op], limit)
}

func (c *gltCalls) count(op string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.ops[op]
}

func (c *gltCalls) listTags(in any) (any, error) {
	arn := sv(in.(*gamelift.ListTagsForResourceInput).ResourceARN)
	c.mu.Lock()
	c.tagARNs = append(c.tagARNs, arn)
	err, tags := c.errs[arn], c.tags[arn]
	c.mu.Unlock()
	if err != nil {
		return nil, err
	}
	var out []gltypes.Tag
	for k, v := range tags {
		out = append(out, gltypes.Tag{Key: sdkaws.String(k), Value: sdkaws.String(v)})
	}
	return &gamelift.ListTagsForResourceOutput{Tags: out}, nil
}

// twoPages answers op with "t" on the first page and "b" on the second.
func (c *gltCalls) twoPages(op string) func(any) (any, error) {
	return func(in any) (any, error) {
		c.called(op, in)
		if bacField[*string](in, "NextToken") == nil {
			return gltPage(op, "t", sdkaws.String("p2")), nil
		}
		return gltPage(op, "b", nil), nil
	}
}

// gltPage is op's page holding the fixture item n.
func gltPage(op, n string, next *string) any {
	name := func(kind string) *string { return sdkaws.String("name " + gltID(kind, n)) }
	arn := func(kind string) *string { return sdkaws.String(gltARN(kind, n)) }
	id := func(kind string) *string { return sdkaws.String(gltID(kind, n)) }
	switch op {
	case "ListAliases":
		return &gamelift.ListAliasesOutput{NextToken: next, Aliases: []gltypes.Alias{{AliasArn: arn("alias"), AliasId: id("alias"), Name: name("alias")}}}
	case "ListBuilds":
		builds := []gltypes.Build{{BuildArn: arn("build"), BuildId: id("build"), Name: name("build")}}
		if n == "b" {
			builds = append(builds, gltypes.Build{BuildArn: sdkaws.String(gltCNBuild()), BuildId: sdkaws.String("build-cn-t")})
		}
		return &gamelift.ListBuildsOutput{NextToken: next, Builds: builds}
	case "ListContainerFleets":
		return &gamelift.ListContainerFleetsOutput{NextToken: next, ContainerFleets: []gltypes.ContainerFleet{{FleetArn: arn("containerfleet"), FleetId: id("containerfleet")}}}
	case "ListContainerGroupDefinitions":
		return &gamelift.ListContainerGroupDefinitionsOutput{NextToken: next, ContainerGroupDefinitions: []gltypes.ContainerGroupDefinition{
			{ContainerGroupDefinitionArn: arn("containergroupdefinition"), Name: name("containergroupdefinition")},
		}}
	case "ListFleets":
		return &gamelift.ListFleetsOutput{NextToken: next, FleetIds: []string{gltID("fleet", n)}}
	case "ListGameServerGroups":
		return &gamelift.ListGameServerGroupsOutput{NextToken: next, GameServerGroups: []gltypes.GameServerGroup{
			{GameServerGroupArn: arn("gameservergroup"), GameServerGroupName: name("gameservergroup")},
		}}
	case "DescribeGameSessionQueues":
		return &gamelift.DescribeGameSessionQueuesOutput{NextToken: next, GameSessionQueues: []gltypes.GameSessionQueue{
			{GameSessionQueueArn: arn("gamesessionqueue"), Name: name("gamesessionqueue")},
		}}
	case "ListLocations":
		locs := []gltypes.LocationModel{{LocationArn: arn("location"), LocationName: name("location")}}
		if n == "b" {
			locs = append(locs, gltypes.LocationModel{LocationName: sdkaws.String("us-east-1")})
		}
		return &gamelift.ListLocationsOutput{NextToken: next, Locations: locs}
	case "DescribeMatchmakingConfigurations":
		return &gamelift.DescribeMatchmakingConfigurationsOutput{NextToken: next, Configurations: []gltypes.MatchmakingConfiguration{
			{ConfigurationArn: arn("matchmakingconfiguration"), Name: name("matchmakingconfiguration")},
		}}
	case "DescribeMatchmakingRuleSets":
		return &gamelift.DescribeMatchmakingRuleSetsOutput{NextToken: next, RuleSets: []gltypes.MatchmakingRuleSet{
			{RuleSetArn: arn("matchmakingruleset"), RuleSetName: name("matchmakingruleset")},
		}}
	default:
		return &gamelift.ListScriptsOutput{NextToken: next, Scripts: []gltypes.Script{{ScriptArn: arn("script"), ScriptId: id("script"), Name: name("script")}}}
	}
}

// gltFleet is the attributes of fleet id.
func gltFleet(id string) gltypes.FleetAttributes {
	return gltypes.FleetAttributes{
		FleetId: sdkaws.String(id), FleetArn: sdkaws.String(arnFor("aws", "gamelift", testRegion, testAccountID, "fleet/"+id)),
		Name: sdkaws.String("name " + id), ComputeType: gltypes.ComputeTypeEc2, Status: gltypes.FleetStatusActive,
	}
}

// gltHandlers answers every op the scan calls with the fixture (gltRows).
func gltHandlers(c *gltCalls) map[string]func(any) (any, error) {
	h := map[string]func(any) (any, error){
		"ListTagsForResource": c.listTags,
		"DescribeFleetAttributes": func(in any) (any, error) {
			c.called("DescribeFleetAttributes", in)
			out := &gamelift.DescribeFleetAttributesOutput{}
			for _, id := range in.(*gamelift.DescribeFleetAttributesInput).FleetIds {
				out.FleetAttributes = append(out.FleetAttributes, gltFleet(id))
			}
			return out, nil
		},
		"ListCompute": func(in any) (any, error) {
			c.called("ListCompute", in)
			return &gamelift.ListComputeOutput{}, nil
		},
		"DescribeScalingPolicies": func(in any) (any, error) {
			c.called("DescribeScalingPolicies", in)
			return &gamelift.DescribeScalingPoliciesOutput{}, nil
		},
		"DescribeVpcPeeringConnections": func(in any) (any, error) {
			c.called("DescribeVpcPeeringConnections", in)
			return &gamelift.DescribeVpcPeeringConnectionsOutput{}, nil
		},
	}
	for _, k := range gltKinds {
		h[k.op] = c.twoPages(k.op)
	}
	return h
}

// gltScan runs the scan's real entry point against handlers; every op they
// do not answer is denied.
func gltScan(ctx context.Context, st *store.Store, handlers map[string]func(any) (any, error)) (int, int, error) {
	acct := newTestAccount(testAccountID)
	acct.cfg = opStubConfig(handlers, glDenied)
	return scanGameLift(ctx, acct, testRegion, st, testScanID)
}

func gltRowOf(t *testing.T, st *store.Store, typ, arn string) (store.Resource, bool) {
	t.Helper()
	r, ok := rowsByNativeID(t, st, typ)[arn]
	return r, ok
}

// Every type stores the tags ListTagsForResource returns for its ARN, and nil
// for a resource without tags; every stored row is sent to the tag op, the
// aws-cn build included. Both pages of every listing are stored, an AWS
// location (no ARN) is not, and the scan reports no error or warning. The
// listings whose model caps the page size send that cap, the others none.
func TestScanGameLift_StoresTagsOfEveryType(t *testing.T) {
	st := newTestStore(t)
	warns := countWarnings(st)
	calls := newGLTCalls()

	total, inserted, err := gltScan(context.Background(), st, gltHandlers(calls))
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	rows := gltRows()
	if total != len(rows) || inserted != len(rows) || len(rows) != 23 {
		t.Errorf("total, inserted = %d, %d; want %d (23 fixture rows)", total, inserted, len(rows))
	}
	if *warns != 0 {
		t.Errorf("warnings = %d; want 0", *warns)
	}
	var wantTagARNs []string
	for _, want := range rows {
		r, ok := gltRowOf(t, st, want.typ, want.arn)
		if !ok {
			t.Errorf("%s %s not stored", want.typ, want.arn)
			continue
		}
		wantTagARNs = append(wantTagARNs, want.arn)
		if gltTagged(want.arn) {
			assertTagsMap(t, r.TagsJSON, map[string]string{"type": want.typ})
		} else if r.TagsJSON != nil {
			t.Errorf("%s %s TagsJSON = %s; want nil", want.typ, want.arn, *r.TagsJSON)
		}
	}
	if n := len(rowsByNativeID(t, st, TypeGameLiftLocation)); n != 2 {
		t.Errorf("location rows = %d; want 2 (the AWS location not stored)", n)
	}
	got := slices.Clone(calls.tagARNs)
	slices.Sort(got)
	slices.Sort(wantTagARNs)
	if !slices.Equal(got, wantTagARNs) {
		t.Errorf("ListTagsForResource ARNs = %v; want %v", got, wantTagARNs)
	}
	for _, k := range gltKinds {
		want := "none"
		switch k.op {
		case "ListContainerGroupDefinitions":
			want = "100"
		case "ListLocations":
			want = "200"
		case "DescribeGameSessionQueues":
			want = "50"
		case "DescribeMatchmakingConfigurations", "DescribeMatchmakingRuleSets":
			want = "10"
		}
		if got := calls.limits[k.op]; len(got) != 2 || got[0] != want || got[1] != want {
			t.Errorf("%s Limit per page = %v; want %s on both pages", k.op, got, want)
		}
	}
	for _, n := range []struct{ typ, arn, name string }{
		{TypeGameLiftAlias, gltARN("alias", "t"), "name alias-t"},
		{TypeGameLiftBuild, gltARN("build", "t"), "name build-t"},
		{TypeGameLiftBuild, gltCNBuild(), "build-cn-t"},
		{TypeGameLiftContainerFleet, gltARN("containerfleet", "t"), "containerfleet-t"},
		{TypeGameLiftContainerGroupDefinition, gltARN("containergroupdefinition", "t"), "name containergroupdefinition-t"},
		{TypeGameLiftFleet, gltARN("fleet", "t"), "name fleet-t"},
		{TypeGameLiftGameServerGroup, gltARN("gameservergroup", "t"), "name gameservergroup-t"},
		{TypeGameLiftGameSessionQueue, gltARN("gamesessionqueue", "t"), "name gamesessionqueue-t"},
		{TypeGameLiftLocation, gltARN("location", "t"), "name custom-t"},
		{TypeGameLiftMatchmakingConfiguration, gltARN("matchmakingconfiguration", "t"), "name matchmakingconfiguration-t"},
		{TypeGameLiftMatchmakingRuleSet, gltARN("matchmakingruleset", "t"), "name matchmakingruleset-t"},
		{TypeGameLiftScript, gltARN("script", "t"), "name script-t"},
	} {
		if r, _ := gltRowOf(t, st, n.typ, n.arn); sv(r.Name) != n.name {
			t.Errorf("%s %s Name = %q; want %q", n.typ, n.arn, sv(r.Name), n.name)
		}
	}
}

// A rescan stores the same rows without inserting any.
func TestScanGameLift_RescanInsertsNone(t *testing.T) {
	st := newTestStore(t)
	calls := newGLTCalls()
	if _, _, err := gltScan(context.Background(), st, gltHandlers(calls)); err != nil {
		t.Fatalf("first scan: %v", err)
	}
	total, inserted, err := gltScan(context.Background(), st, gltHandlers(calls))
	if err != nil {
		t.Fatalf("rescan: %v", err)
	}
	if total != 23 || inserted != 0 {
		t.Errorf("rescan total, inserted = %d, %d; want 23, 0", total, inserted)
	}
}

// gltFleetChildCalls is the fleets the fleet children listings were sent.
func gltFleetChildCalls(c *gltCalls) []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	var fleets []string
	for _, in := range c.ins["DescribeScalingPolicies"] {
		fleets = append(fleets, sv(in.(*gamelift.DescribeScalingPoliciesInput).FleetId))
	}
	slices.Sort(fleets)
	return fleets
}

// A failed tag read keeps the row without tags unless the resource is gone: a
// denial warns once under gamelift:ListTagsForResource, a throttle after
// retries is a transient error, any other code a hard one. A fleet the tag
// read no longer finds is dropped and its children are not listed. A
// container group definition is kept on a not-found answer, as a hard error:
// its ARN carries a version, which the tag op is not verified to accept.
func TestScanGameLift_TagReadFailures(t *testing.T) {
	for _, target := range []gltRow{
		{TypeGameLiftBuild, gltARN("build", "t")},
		{TypeGameLiftFleet, gltARN("fleet", "t")},
		{TypeGameLiftContainerGroupDefinition, gltARN("containergroupdefinition", "t")},
	} {
		t.Run(target.typ, func(t *testing.T) {
			type tagCase struct {
				name      string
				err       error
				kept      bool
				outcome   serviceOutcome
				code      string
				wantWarns int
			}
			notFound := tagCase{"not found", glNotFound, false, 0, "", 0}
			if target.typ == TypeGameLiftContainerGroupDefinition {
				notFound = tagCase{"not found", glNotFound, true, outcomeError, "NotFoundException", 0}
			}
			for _, c := range []tagCase{
				{"denied", glDenied, true, 0, "", 1},
				{"unauthorized", gltUnauthorized, true, 0, "", 1},
				notFound,
				{"other error", gltBadInput, true, outcomeError, "InvalidRequestException", 0},
				{"transient", glThrottled, true, outcomeTransient, "", 0},
			} {
				t.Run(c.name, func(t *testing.T) {
					st := newTestStore(t)
					warnings := bacWarnings(st, glListTagsOp)
					calls := newGLTCalls()
					calls.errs[target.arn] = c.err

					_, _, err := gltScan(context.Background(), st, gltHandlers(calls))
					if c.outcome == 0 && err != nil {
						t.Fatalf("err = %v; want nil", err)
					}
					if c.outcome != 0 {
						assertGlueOutcome(t, err, c.outcome)
					}
					if c.outcome == outcomeError {
						assertAPICode(t, err, c.code)
					}
					r, ok := gltRowOf(t, st, target.typ, target.arn)
					if ok != c.kept {
						t.Fatalf("%s row stored = %v; want %v", target.typ, ok, c.kept)
					}
					if ok && r.TagsJSON != nil {
						t.Errorf("TagsJSON = %q; want nil", *r.TagsJSON)
					}
					if target.typ == TypeGameLiftFleet {
						want := []string{"fleet-b"}
						if c.kept {
							want = []string{"fleet-b", "fleet-t"}
						}
						if got := gltFleetChildCalls(calls); !slices.Equal(got, want) {
							t.Errorf("scaling policies listed for %v; want %v", got, want)
						}
					}
					if len(*warnings) != c.wantWarns {
						t.Fatalf("%s warnings = %v; want %d", glListTagsOp, *warnings, c.wantWarns)
					}
					for _, w := range *warnings {
						if w.Scope != testAccountID+"/"+testRegion {
							t.Errorf("warning Scope = %q; want %s/%s", w.Scope, testAccountID, testRegion)
						}
					}
				})
			}
		})
	}
}

// A throttled tag read beside a hard failure of the same op is demoted to
// one warning, so the hard error decides the outcome.
func TestScanGameLift_TagThrottleDemotedBesideHardError(t *testing.T) {
	st := newTestStore(t)
	warns := countOpWarnings(st, glListTagsOp)
	calls := newGLTCalls()
	calls.errs[gltARN("alias", "t")] = glThrottled
	calls.errs[gltARN("script", "b")] = gltBadInput

	_, _, err := gltScan(context.Background(), st, gltHandlers(calls))
	assertGlueOutcome(t, err, outcomeError)
	assertAPICode(t, err, "InvalidRequestException")
	if *warns != 1 {
		t.Errorf("%s warnings = %d; want 1", glListTagsOp, *warns)
	}
}

// A denied listing warns once under its own op and costs no other phase its
// rows, whether denied with AccessDeniedException or UnauthorizedException,
// the matchmaking ops included, whose models do not list the latter.
func TestScanGameLift_DeniedListingWarnsUnderItsOp(t *testing.T) {
	ops := []string{"DescribeFleetAttributes"}
	for _, k := range gltKinds {
		ops = append(ops, k.op)
	}
	for _, op := range ops {
		for _, denial := range []error{gltUnauthorized, glDenied} {
			t.Run(op+"/"+denial.(smithy.APIError).ErrorCode(), func(t *testing.T) {
				gltDeniedListing(t, op, denial)
			})
		}
	}
}

// gltDeniedListing scans with op denied by denial and checks the one
// warning under op and the other phases' rows.
func gltDeniedListing(t *testing.T, op string, denial error) {
	t.Helper()
	st := newTestStore(t)
	warns := countWarnings(st)
	opWarns := countOpWarnings(st, "gamelift:"+op)
	h := gltHandlers(newGLTCalls())
	h[op] = func(any) (any, error) { return nil, denial }

	total, _, err := gltScan(context.Background(), st, h)
	if err != nil {
		t.Fatalf("err = %v; want nil", err)
	}
	if *warns != 1 || *opWarns != 1 {
		t.Errorf("warnings = %d (gamelift:%s: %d); want 1 under the op", *warns, op, *opWarns)
	}
	if stored := countStoredRows(t, st); total == 0 || total != stored {
		t.Errorf("total = %d, stored rows = %d; want the other phases' rows counted", total, stored)
	}
}

// A failed write stops the scan before any later listing and sends no
// warning about the rows it lost.
func TestScanGameLift_FailedWriteStopsWithoutWarning(t *testing.T) {
	st := newTestStore(t)
	warns := countWarnings(st)
	refuseStoreWrites(t, st, TypeGameLiftFleet)
	calls := newGLTCalls()
	calls.errs[gltARN("fleet", "t")] = glDenied

	_, _, err := gltScan(context.Background(), st, gltHandlers(calls))
	if !errors.Is(err, store.ErrStoreWrite) {
		t.Fatalf("err = %v; want store.ErrStoreWrite", err)
	}
	if *warns != 0 {
		t.Errorf("warnings = %d; want 0", *warns)
	}
	for _, op := range []string{"ListGameServerGroups", "ListScripts", "DescribeScalingPolicies"} {
		if n := calls.count(op); n != 0 {
			t.Errorf("%s called %d times after the failed write", op, n)
		}
	}
}

// A listing's failed page keeps the pages read before it, and no listing's
// failure stops the phases after it. A failed ListFleets page keeps the
// fleets named before it, and only those are described and their children
// listed.
func TestScanGameLift_ListFailureKeepsPagesAndRunsEveryPhase(t *testing.T) {
	st := newTestStore(t)
	warns := countWarnings(st)
	calls := newGLTCalls()
	h := gltHandlers(calls)
	h["ListBuilds"] = failOnCall(2, apiErr("ConflictException", "page 2"), h["ListBuilds"])
	h["ListScripts"] = func(any) (any, error) { return nil, gltBadInput }
	h["ListFleets"] = failOnCall(2, apiErr("LimitExceededException", "page 2"), h["ListFleets"])

	_, _, err := gltScan(context.Background(), st, h)
	for _, code := range []string{"ConflictException", "InvalidRequestException", "LimitExceededException"} {
		if !joinCarriesAPIErrorCode(err, code) {
			t.Errorf("err = %v; want it to carry %s", err, code)
		}
	}
	if *warns != 0 {
		t.Errorf("warnings = %d; want 0", *warns)
	}
	for _, want := range []gltRow{
		{TypeGameLiftBuild, gltARN("build", "t")},
		{TypeGameLiftFleet, gltARN("fleet", "t")},
		{TypeGameLiftMatchmakingRuleSet, gltARN("matchmakingruleset", "b")},
	} {
		if _, ok := gltRowOf(t, st, want.typ, want.arn); !ok {
			t.Errorf("%s %s missing", want.typ, want.arn)
		}
	}
	for _, gone := range []gltRow{
		{TypeGameLiftBuild, gltARN("build", "b")},
		{TypeGameLiftFleet, gltARN("fleet", "b")},
	} {
		if _, ok := gltRowOf(t, st, gone.typ, gone.arn); ok {
			t.Errorf("%s %s stored; want it not read", gone.typ, gone.arn)
		}
	}
	if got := gltFleetChildCalls(calls); !slices.Equal(got, []string{"fleet-t"}) {
		t.Errorf("scaling policies listed for %v; want [fleet-t]", got)
	}
}

// DescribeFleetAttributes is sent the listed fleet ids 50 at a time. A failed
// call keeps the fleets described before it, and its error is kept under its
// op: throttled beside another phase's hard error, it is demoted to one
// warning under gamelift:DescribeFleetAttributes.
func TestScanGameLift_DescribesFleetsInBatches(t *testing.T) {
	var ids []string
	for i := range 51 {
		ids = append(ids, fmt.Sprintf("fleet-%02d", i))
	}
	for _, c := range []struct {
		name      string
		failCall2 bool
		fleets    int
	}{{"every batch read", false, 51}, {"second batch fails", true, 50}} {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			calls := newGLTCalls()
			h := gltHandlers(calls)
			h["ListFleets"] = func(any) (any, error) { return &gamelift.ListFleetsOutput{FleetIds: ids}, nil }
			describeWarns := countOpWarnings(st, "gamelift:DescribeFleetAttributes")
			if c.failCall2 {
				h["DescribeFleetAttributes"] = failOnCall(2, glThrottled, h["DescribeFleetAttributes"])
				h["ListScripts"] = func(any) (any, error) { return nil, gltBadInput }
			}

			_, _, err := gltScan(context.Background(), st, h)
			if c.failCall2 {
				assertGlueOutcome(t, err, outcomeError)
				assertAPICode(t, err, "InvalidRequestException")
				if *describeWarns != 1 {
					t.Errorf("gamelift:DescribeFleetAttributes warnings = %d; want 1 (the demoted throttle)", *describeWarns)
				}
			} else if err != nil {
				t.Fatalf("scan: %v", err)
			}
			var sizes []int
			for _, in := range calls.ins["DescribeFleetAttributes"] {
				sizes = append(sizes, len(in.(*gamelift.DescribeFleetAttributesInput).FleetIds))
			}
			if !c.failCall2 && !slices.Equal(sizes, []int{50, 1}) {
				t.Errorf("DescribeFleetAttributes batch sizes = %v; want [50 1]", sizes)
			}
			if n := len(rowsByNativeID(t, st, TypeGameLiftFleet)); n != c.fleets {
				t.Errorf("fleet rows = %d; want %d", n, c.fleets)
			}
		})
	}
}

// UnsupportedRegionException on a listing's first page means the feature is
// not offered in the region: the listing is empty, without an error or a
// warning. After a page was read it is an error, and that page is kept. Only
// the ops that model the code skip it.
func TestScanGameLift_RegionGap(t *testing.T) {
	unsupported := glNewError("UnsupportedRegionException")
	for _, op := range []string{"ListContainerFleets", "ListContainerGroupDefinitions", "DescribeMatchmakingConfigurations", "DescribeMatchmakingRuleSets"} {
		t.Run(op+"/first page", func(t *testing.T) {
			st := newTestStore(t)
			warns := countWarnings(st)
			h := gltHandlers(newGLTCalls())
			h[op] = func(any) (any, error) { return nil, unsupported }
			total, _, err := gltScan(context.Background(), st, h)
			if err != nil {
				t.Fatalf("err = %v; want nil", err)
			}
			if total != 21 || *warns != 0 {
				t.Errorf("total, warnings = %d, %d; want 21, 0", total, *warns)
			}
		})
	}
	t.Run("second page", func(t *testing.T) {
		st := newTestStore(t)
		h := gltHandlers(newGLTCalls())
		h["DescribeMatchmakingRuleSets"] = failOnCall(2, unsupported, h["DescribeMatchmakingRuleSets"])
		_, _, err := gltScan(context.Background(), st, h)
		assertAPICode(t, err, "UnsupportedRegionException")
		if _, ok := gltRowOf(t, st, TypeGameLiftMatchmakingRuleSet, gltARN("matchmakingruleset", "t")); !ok {
			t.Error("rule set from page 1 missing")
		}
	})
	t.Run("op that does not model it", func(t *testing.T) {
		st := newTestStore(t)
		h := gltHandlers(newGLTCalls())
		h["ListBuilds"] = func(any) (any, error) { return nil, unsupported }
		_, _, err := gltScan(context.Background(), st, h)
		assertAPICode(t, err, "UnsupportedRegionException")
	})
}

// Items listed without an ARN are not stored and are warned about once per
// listing, under a key that is never empty.
func TestScanGameLift_UnstorableItemsWarn(t *testing.T) {
	for _, c := range []struct{ op, warnOp, key string }{
		{"ListBuilds", "gamelift:ListBuilds", "(a build with no name)"},
		{"DescribeFleetAttributes", "gamelift:DescribeFleetAttributes", "(a fleet with no name)"},
		{"DescribeMatchmakingRuleSets", "gamelift:DescribeMatchmakingRuleSets", "(a matchmaking rule set with no name)"},
	} {
		t.Run(c.op, func(t *testing.T) {
			st := newTestStore(t)
			warnings := bacWarnings(st, c.warnOp)
			h := gltHandlers(newGLTCalls())
			h[c.op] = func(any) (any, error) {
				switch c.op {
				case "ListBuilds":
					return &gamelift.ListBuildsOutput{Builds: []gltypes.Build{{}, {}}}, nil
				case "DescribeFleetAttributes":
					return &gamelift.DescribeFleetAttributesOutput{FleetAttributes: []gltypes.FleetAttributes{{}}}, nil
				default:
					return &gamelift.DescribeMatchmakingRuleSetsOutput{RuleSets: []gltypes.MatchmakingRuleSet{{}}}, nil
				}
			}

			if _, _, err := gltScan(context.Background(), st, h); err != nil {
				t.Fatalf("err = %v; want nil", err)
			}
			if len(*warnings) != 1 || !strings.Contains((*warnings)[0].Message, c.key) {
				t.Errorf("warnings = %v; want one naming %q", *warnings, c.key)
			}
		})
	}
}

// A repeated token ends the listing as an error, keeping its pages.
func TestScanGameLift_RepeatedTokenKeepsPages(t *testing.T) {
	st := newTestStore(t)
	h := gltHandlers(newGLTCalls())
	h["ListScripts"] = func(any) (any, error) {
		return &gamelift.ListScriptsOutput{Scripts: []gltypes.Script{{ScriptArn: sdkaws.String(gltARN("script", "t"))}}, NextToken: sdkaws.String("same")}, nil
	}

	_, _, err := gltScan(context.Background(), st, h)
	if !errors.Is(err, errRepeatedNextToken) {
		t.Fatalf("err = %v; want errRepeatedNextToken", err)
	}
	if _, ok := gltRowOf(t, st, TypeGameLiftScript, gltARN("script", "t")); !ok {
		t.Error("script from the pages read missing")
	}
}

// Once ctx ends no further phase starts, and the stop is reported once,
// although the phase in flight saw it in its
// listing.
func TestScanGameLift_ContextEndStopsOnce(t *testing.T) {
	st := newTestStore(t)
	calls := newGLTCalls()
	h := gltHandlers(calls)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	builds := h["ListBuilds"]
	h["ListBuilds"] = func(in any) (any, error) {
		if in.(*gamelift.ListBuildsInput).NextToken != nil {
			cancel()
			return nil, context.Canceled
		}
		return builds(in)
	}

	_, _, err := gltScan(ctx, st, h)
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
	for _, op := range []string{"ListContainerFleets", "ListScripts", "DescribeVpcPeeringConnections"} {
		if n := calls.count(op); n != 0 {
			t.Errorf("%s called %d times after ctx ended", op, n)
		}
	}
}

// An unreachable endpoint on the alias listing stops the scan only when
// nothing was stored: not after a stored page, and not on the alias tag
// reads, since the listing reached the endpoint. An NXDOMAIN of a later
// listing that stored nothing, after the alias listing answered, is a
// failure of this scan, not a region gap.
func TestScanGameLift_Unreachable(t *testing.T) {
	nx := &net.DNSError{Err: "no such host", Name: "gamelift.us-east-1.amazonaws.com", IsNotFound: true}
	refused := &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("connection refused")}
	t.Run("alias tag read", func(t *testing.T) {
		st := newTestStore(t)
		calls := newGLTCalls()
		calls.errs[gltARN("alias", "t")] = refused
		total, _, err := gltScan(context.Background(), st, gltHandlers(calls))
		if err == nil {
			t.Fatal("err = nil; want the refused tag read")
		}
		if total != 23 || calls.count("ListScripts") == 0 {
			t.Errorf("total = %d, ListScripts calls = %d; want 23 and every phase run", total, calls.count("ListScripts"))
		}
	})
	t.Run("alias listing refused after a stored page", func(t *testing.T) {
		st := newTestStore(t)
		calls := newGLTCalls()
		h := gltHandlers(calls)
		h["ListAliases"] = failOnCall(2, refused, h["ListAliases"])
		total, _, err := gltScan(context.Background(), st, h)
		if !errors.Is(err, refused) {
			t.Errorf("err = %v; want the refused dial", err)
		}
		if total != 22 || calls.count("ListScripts") == 0 {
			t.Errorf("total = %d, ListScripts calls = %d; want 22 and every phase run", total, calls.count("ListScripts"))
		}
	})
	t.Run("alias listing refused", func(t *testing.T) {
		st := newTestStore(t)
		calls := newGLTCalls()
		h := gltHandlers(calls)
		h["ListAliases"] = func(any) (any, error) { return nil, refused }
		total, _, err := gltScan(context.Background(), st, h)
		if !errors.Is(err, refused) {
			t.Errorf("err = %v; want the refused dial", err)
		}
		if total != 0 || calls.count("ListBuilds") != 0 {
			t.Errorf("total = %d, ListBuilds calls = %d; want 0, 0", total, calls.count("ListBuilds"))
		}
	})
	t.Run("later listing NXDOMAIN with nothing stored", func(t *testing.T) {
		st := newTestStore(t)
		h := map[string]func(any) (any, error){
			"ListAliases": func(any) (any, error) { return &gamelift.ListAliasesOutput{}, nil },
			"ListBuilds":  func(any) (any, error) { return nil, nx },
		}
		total, _, err := gltScan(context.Background(), st, h)
		if total != 0 {
			t.Errorf("total = %d; want 0", total)
		}
		assertGlueOutcome(t, err, outcomeError)
	})
}

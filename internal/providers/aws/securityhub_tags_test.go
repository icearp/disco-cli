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
	"github.com/aws/aws-sdk-go-v2/service/securityhub"
	securityhubtypes "github.com/aws/aws-sdk-go-v2/service/securityhub/types"
	"github.com/icearp/disco-cli/store"
)

const (
	// shTagTestListed is the rows of the two-page tagged listings: two per
	// listing, one per page.
	shTagTestListed = 2 * 5
	// shTagTestTotal adds the hub, the V2 hub and the organization
	// configuration (stored untagged).
	shTagTestTotal = shTagTestListed + 3
)

var shThrottle = apiErr("ThrottlingException", "Rate exceeded")

func shTestARN(partition, region, resource string) string {
	return "arn:" + partition + ":securityhub:" + region + ":" + testAccountID + ":" + resource
}

var shTestHubV2ARN = shTestARN("aws", testRegion, "hubv2/hv2-1")

// shTagKind is one tagged listing: the List op, the type it stores, the ARN
// resource type of its rows, the element field holding the ARN, and page,
// which builds the op's output holding one element named name under arn,
// with next.
type shTagKind struct {
	op, typ, arnType, arnField string
	page                       func(arn, name, next *string) any
}

func (k shTagKind) name(suffix string) string { return k.arnType + "-" + suffix }

func (k shTagKind) arn(suffix string) string {
	return shTestARN("aws", testRegion, k.arnType+"/"+k.name(suffix))
}

// storedName is the Name k's row stores: an aggregator, which has no name,
// is named by its ARN.
func (k shTagKind) storedName(suffix string) string {
	if k.typ == TypeSecurityHubAggregatorV2 {
		return k.arn(suffix)
	}
	return k.name(suffix)
}

func shTagKinds() []shTagKind {
	return []shTagKind{
		{"ListAggregatorsV2", TypeSecurityHubAggregatorV2, "aggregatorv2", "AggregatorV2Arn", func(arn, _, next *string) any {
			return &securityhub.ListAggregatorsV2Output{NextToken: next, AggregatorsV2: []securityhubtypes.AggregatorV2{{AggregatorV2Arn: arn}}}
		}},
		{"ListAutomationRules", TypeSecurityHubAutomationRule, "automation-rule", "RuleArn", func(arn, name, next *string) any {
			return &securityhub.ListAutomationRulesOutput{NextToken: next, AutomationRulesMetadata: []securityhubtypes.AutomationRulesMetadata{{RuleArn: arn, RuleName: name}}}
		}},
		{"ListAutomationRulesV2", TypeSecurityHubAutomationRuleV2, "automation-rulev2", "RuleArn", func(arn, name, next *string) any {
			return &securityhub.ListAutomationRulesV2Output{NextToken: next, Rules: []securityhubtypes.AutomationRulesMetadataV2{{RuleArn: arn, RuleName: name}}}
		}},
		{"ListConfigurationPolicies", TypeSecurityHubConfigurationPolicy, "configuration-policy", "Arn", func(arn, name, next *string) any {
			return &securityhub.ListConfigurationPoliciesOutput{NextToken: next, ConfigurationPolicySummaries: []securityhubtypes.ConfigurationPolicySummary{{Arn: arn, Name: name}}}
		}},
		{"ListConnectorsV2", TypeSecurityHubConnectorV2, "connectorv2", "ConnectorArn", func(arn, name, next *string) any {
			return &securityhub.ListConnectorsV2Output{NextToken: next, Connectors: []securityhubtypes.ConnectorSummary{{ConnectorArn: arn, Name: name}}}
		}},
	}
}

func shTagKindOf(t *testing.T, op string) shTagKind {
	t.Helper()
	for _, k := range shTagKinds() {
		if k.op == op {
			return k
		}
	}
	t.Fatalf("%s is no tagged Security Hub listing", op)
	return shTagKind{}
}

// shTagCalls records the calls of a scan and answers ListTagsForResource
// with tags, or errs, by ARN.
type shTagCalls struct {
	mu      sync.Mutex
	ops     map[string]int
	inputs  map[string][]any
	tagARNs []string
	tags    map[string]map[string]string
	errs    map[string]error
}

// newSHTagCalls tags the hub, the V2 hub and the "tagged" row of every
// listing with its type.
func newSHTagCalls() *shTagCalls {
	c := &shTagCalls{ops: map[string]int{}, inputs: map[string][]any{}, tags: map[string]map[string]string{}, errs: map[string]error{}}
	c.tags[shHubARN(testRegion)] = map[string]string{"type": TypeSecurityHubHub}
	c.tags[shTestHubV2ARN] = map[string]string{"type": TypeSecurityHubHubV2}
	for _, k := range shTagKinds() {
		c.tags[k.arn("tagged")] = map[string]string{"type": k.typ}
	}
	return c
}

func (c *shTagCalls) recorded(op string, answer func(in any) (any, error)) func(in any) (any, error) {
	return func(in any) (any, error) {
		c.mu.Lock()
		c.ops[op]++
		c.inputs[op] = append(c.inputs[op], in)
		c.mu.Unlock()
		return answer(in)
	}
}

func (c *shTagCalls) count(op string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.ops[op]
}

func (c *shTagCalls) sentARNs() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return slices.Sorted(slices.Values(c.tagARNs))
}

func (c *shTagCalls) listTags(in any) (any, error) {
	arn := sv(in.(*securityhub.ListTagsForResourceInput).ResourceArn)
	c.mu.Lock()
	c.tagARNs = append(c.tagARNs, arn)
	err, tags := c.errs[arn], c.tags[arn]
	c.mu.Unlock()
	if err != nil {
		return nil, err
	}
	return &securityhub.ListTagsForResourceOutput{Tags: tags}, nil
}

// twoSHPages answers k's op with "tagged" on the first page and "bare" on the
// second.
func twoSHPages(k shTagKind) func(any) (any, error) {
	return func(in any) (any, error) {
		suffix, next := "tagged", sdkaws.String("t2")
		if bacField[*string](in, "NextToken") != nil {
			suffix, next = "bare", nil
		}
		arn, name := k.arn(suffix), k.name(suffix)
		return k.page(&arn, &name, next), nil
	}
}

// shTagHandlers answers every op the scan calls: the hub, the V2 hub, an
// organization configuration, each tagged listing in two pages, and every
// other listing empty.
func shTagHandlers(c *shTagCalls) map[string]func(in any) (any, error) {
	answers := map[string]func(in any) (any, error){
		"ListTagsForResource": c.listTags,
		"DescribeHub": func(any) (any, error) {
			return &securityhub.DescribeHubOutput{HubArn: sdkaws.String(shHubARN(testRegion)), SubscribedAt: sdkaws.String("2026-01-02T03:04:05Z")}, nil
		},
		"DescribeSecurityHubV2": func(any) (any, error) {
			return &securityhub.DescribeSecurityHubV2Output{HubV2Arn: sdkaws.String(shTestHubV2ARN), SubscribedAt: sdkaws.String("2026-01-02T03:04:05Z")}, nil
		},
		"DescribeOrganizationConfiguration": func(any) (any, error) {
			return &securityhub.DescribeOrganizationConfigurationOutput{AutoEnable: sdkaws.Bool(true)}, nil
		},
		"GetInsights":                         func(any) (any, error) { return &securityhub.GetInsightsOutput{}, nil },
		"GetEnabledStandards":                 func(any) (any, error) { return &securityhub.GetEnabledStandardsOutput{}, nil },
		"ListEnabledProductsForImport":        func(any) (any, error) { return &securityhub.ListEnabledProductsForImportOutput{}, nil },
		"ListFindingAggregators":              func(any) (any, error) { return &securityhub.ListFindingAggregatorsOutput{}, nil },
		"ListConfigurationPolicyAssociations": func(any) (any, error) { return &securityhub.ListConfigurationPolicyAssociationsOutput{}, nil },
		"ListSecurityControlDefinitions":      func(any) (any, error) { return &securityhub.ListSecurityControlDefinitionsOutput{}, nil },
		"DescribeStandards":                   func(any) (any, error) { return &securityhub.DescribeStandardsOutput{}, nil },
		"DescribeActionTargets":               func(any) (any, error) { return &securityhub.DescribeActionTargetsOutput{}, nil },
		"ListMembers":                         func(any) (any, error) { return &securityhub.ListMembersOutput{}, nil },
	}
	for _, k := range shTagKinds() {
		answers[k.op] = twoSHPages(k)
	}
	h := make(map[string]func(in any) (any, error), len(answers))
	for op, answer := range answers {
		h[op] = c.recorded(op, answer)
	}
	return h
}

// shTagScanIn runs the scan's real entry point for acct in region against
// handlers; every op they do not answer is denied.
func shTagScanIn(ctx context.Context, st *store.Store, acct *account, region string, handlers map[string]func(in any) (any, error)) (int, int, error) {
	acct.cfg = opStubConfig(handlers, shDenied)
	return scanSecurityHub(ctx, acct, region, st, testScanID)
}

func shTagScan(ctx context.Context, st *store.Store, handlers map[string]func(in any) (any, error)) (int, int, error) {
	return shTagScanIn(ctx, st, newTestAccount(testAccountID), testRegion, handlers)
}

// shTagRow is one row whose tags the fixture's scan reads.
type shTagRow struct{ typ, arn string }

// shTaggedRows is the hub, the V2 hub and the "tagged" row of every listing.
func shTaggedRows() []shTagRow {
	rows := []shTagRow{{TypeSecurityHubHub, shHubARN(testRegion)}, {TypeSecurityHubHubV2, shTestHubV2ARN}}
	for _, k := range shTagKinds() {
		rows = append(rows, shTagRow{k.typ, k.arn("tagged")})
	}
	return rows
}

// shTagReadARNs is the ARN of every row whose tags the fixture's scan reads.
func shTagReadARNs() []string {
	arns := []string{shHubARN(testRegion), shTestHubV2ARN}
	for _, k := range shTagKinds() {
		arns = append(arns, k.arn("tagged"), k.arn("bare"))
	}
	slices.Sort(arns)
	return arns
}

// assertSHRow checks a stored row's identity columns: Name, Region, no
// Status or CreatedAt, not managed, and the element as attributes (field
// holding want).
func assertSHRow(t *testing.T, r store.Resource, name *string, field, want string) {
	t.Helper()
	if sv(r.Name) != sv(name) || (r.Name == nil) != (name == nil) || sv(r.Region) != testRegion || r.Status != nil || r.CreatedAt != nil || r.ManagedByProvider {
		t.Errorf("%s Name, Region, Status, CreatedAt, Managed = %v, %q, %v, %v, %v; want %v, %q, no Status, no CreatedAt, false",
			r.NativeID, r.Name, sv(r.Region), r.Status, r.CreatedAt, r.ManagedByProvider, name, testRegion)
	}
	if !jsonHasString(t, r.AttributesJSON, field, want) {
		t.Errorf("%s attrs = %s; want %s %q", r.NativeID, r.AttributesJSON, field, want)
	}
}

// Every tagged listing stores both its pages, each row under its ARN, with
// its name (an aggregator's ARN) and the listed element as attributes, and
// the tags ListTagsForResource returns for that ARN, nil for a resource
// without tags; the hub and the V2 hub likewise. Exactly those ARNs are sent
// to the tag op; the organization configuration is stored untagged and never
// sent. Every listing call asks for 100 results. No error, no warning.
func TestScanSecurityHub_StoresTagsOfEveryTaggedType(t *testing.T) {
	st := newTestStore(t)
	warns := rhWarnRecorder(st)
	calls := newSHTagCalls()

	total, inserted, err := shTagScan(context.Background(), st, shTagHandlers(calls))
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != shTagTestTotal || inserted != shTagTestTotal {
		t.Errorf("total, inserted = %d, %d; want %d, %d", total, inserted, shTagTestTotal, shTagTestTotal)
	}
	if w := warns(); len(w) != 0 {
		t.Errorf("warnings = %v; want none", w)
	}
	for _, k := range shTagKinds() {
		rows := rowsByNativeID(t, st, k.typ)
		if len(rows) != 2 {
			t.Errorf("%s rows = %v; want 2", k.typ, nativeIDs(rows))
		}
		for _, suffix := range []string{"tagged", "bare"} {
			arn := k.arn(suffix)
			r, ok := rows[arn]
			if !ok {
				t.Errorf("%s %s missing", k.typ, arn)
				continue
			}
			assertSHRow(t, r, sdkaws.String(k.storedName(suffix)), k.arnField, arn)
			if suffix == "tagged" {
				assertTagsMap(t, r.TagsJSON, map[string]string{"type": k.typ})
			} else if r.TagsJSON != nil {
				t.Errorf("%s TagsJSON = %q; want nil", arn, *r.TagsJSON)
			}
		}
		if n := calls.count(k.op); n != 2 {
			t.Errorf("%s calls = %d; want 2", k.op, n)
		}
		for _, in := range calls.inputs[k.op] {
			if n := bacField[*int32](in, "MaxResults"); n == nil || *n != 100 {
				t.Errorf("%s MaxResults = %v; want 100", k.op, n)
			}
		}
	}
	hub, ok := rowsByNativeID(t, st, TypeSecurityHubHub)[shHubARN(testRegion)]
	if !ok {
		t.Fatal("hub row missing")
	}
	assertSHRow(t, hub, nil, "HubArn", shHubARN(testRegion))
	assertTagsMap(t, hub.TagsJSON, map[string]string{"type": TypeSecurityHubHub})
	hubV2, ok := rowsByNativeID(t, st, TypeSecurityHubHubV2)[shTestHubV2ARN]
	if !ok {
		t.Fatal("V2 hub row missing")
	}
	assertSHRow(t, hubV2, sdkaws.String("hub-v2"), "HubV2Arn", shTestHubV2ARN)
	assertTagsMap(t, hubV2.TagsJSON, map[string]string{"type": TypeSecurityHubHubV2})
	orgARN := shTestARN("aws", testRegion, "organization-configuration")
	org, ok := rowsByNativeID(t, st, TypeSecurityHubOrganizationConfiguration)[orgARN]
	if !ok || org.TagsJSON != nil {
		t.Errorf("organization configuration stored %v, TagsJSON %v; want stored untagged", ok, org.TagsJSON)
	}
	if got := calls.sentARNs(); !slices.Equal(got, shTagReadARNs()) {
		t.Errorf("ListTagsForResource ARNs = %v\nwant %v", got, shTagReadARNs())
	}
}

// The hub and the V2 hub without tags store nil TagsJSON.
func TestScanSecurityHub_UntaggedHubs(t *testing.T) {
	st := newTestStore(t)
	calls := newSHTagCalls()
	delete(calls.tags, shHubARN(testRegion))
	delete(calls.tags, shTestHubV2ARN)

	if _, _, err := shTagScan(context.Background(), st, shTagHandlers(calls)); err != nil {
		t.Fatalf("scan: %v", err)
	}
	for typ, arn := range map[string]string{TypeSecurityHubHub: shHubARN(testRegion), TypeSecurityHubHubV2: shTestHubV2ARN} {
		if r, ok := rowsByNativeID(t, st, typ)[arn]; !ok || r.TagsJSON != nil {
			t.Errorf("%s stored %v, TagsJSON %v; want stored untagged", arn, ok, r.TagsJSON)
		}
	}
}

// An element without a name is named by its fallback field: an automation rule
// by its ARN, a V2 rule by its RuleId, a policy by its Id, a connector by its
// ConnectorId.
func TestScanSecurityHub_NameFallbacks(t *testing.T) {
	st := newTestStore(t)
	handlers := shTagHandlers(newSHTagCalls())
	rule := shTestARN("aws", testRegion, "automation-rule/r-1")
	ruleV2 := shTestARN("aws", testRegion, "automation-rulev2/r2-1")
	policy := shTestARN("aws", testRegion, "configuration-policy/cp-1")
	connector := shTestARN("aws", testRegion, "connectorv2/c-1")
	handlers["ListAutomationRules"] = func(any) (any, error) {
		return &securityhub.ListAutomationRulesOutput{AutomationRulesMetadata: []securityhubtypes.AutomationRulesMetadata{{RuleArn: &rule}}}, nil
	}
	handlers["ListAutomationRulesV2"] = func(any) (any, error) {
		return &securityhub.ListAutomationRulesV2Output{Rules: []securityhubtypes.AutomationRulesMetadataV2{{RuleArn: &ruleV2, RuleId: sdkaws.String("r2-id")}}}, nil
	}
	handlers["ListConfigurationPolicies"] = func(any) (any, error) {
		return &securityhub.ListConfigurationPoliciesOutput{ConfigurationPolicySummaries: []securityhubtypes.ConfigurationPolicySummary{{Arn: &policy, Id: sdkaws.String("cp-id")}}}, nil
	}
	handlers["ListConnectorsV2"] = func(any) (any, error) {
		return &securityhub.ListConnectorsV2Output{Connectors: []securityhubtypes.ConnectorSummary{{ConnectorArn: &connector, ConnectorId: sdkaws.String("c-id")}}}, nil
	}

	if _, _, err := shTagScan(context.Background(), st, handlers); err != nil {
		t.Fatalf("scan: %v", err)
	}
	for _, c := range []struct{ typ, arn, name string }{
		{TypeSecurityHubAutomationRule, rule, rule},
		{TypeSecurityHubAutomationRuleV2, ruleV2, "r2-id"},
		{TypeSecurityHubConfigurationPolicy, policy, "cp-id"},
		{TypeSecurityHubConnectorV2, connector, "c-id"},
	} {
		if r, ok := rowsByNativeID(t, st, c.typ)[c.arn]; !ok || sv(r.Name) != c.name {
			t.Errorf("%s stored %v, Name %q; want %q", c.arn, ok, sv(r.Name), c.name)
		}
	}
}

// A rescan stores the same rows without inserting any.
func TestScanSecurityHub_TaggedRescanInsertsNone(t *testing.T) {
	st := newTestStore(t)
	if _, _, err := shTagScan(context.Background(), st, shTagHandlers(newSHTagCalls())); err != nil {
		t.Fatalf("first scan: %v", err)
	}
	total, inserted, err := shTagScan(context.Background(), st, shTagHandlers(newSHTagCalls()))
	if err != nil {
		t.Fatalf("rescan: %v", err)
	}
	if total != shTagTestTotal || inserted != 0 {
		t.Errorf("rescan total, inserted = %d, %d; want %d, 0", total, inserted, shTagTestTotal)
	}
}

// A failed tag read: ResourceNotFoundException, the op's one modeled
// not-found code, drops the row silently; a denial keeps the row untagged
// and warns once under the tag op; a throttle keeps it and is transient; any
// other error keeps it and is an error.
func TestScanSecurityHub_TagReadFailures(t *testing.T) {
	for _, row := range shTaggedRows() {
		for _, c := range []struct {
			name      string
			err       error
			outcome   serviceOutcome // -1: no error
			code      string
			wantKept  bool
			wantWarns int
		}{
			{"not found", shNotFound, -1, "", false, 0},
			{"denied", shDenied, -1, "", true, 1},
			{"transient", shThrottle, outcomeTransient, "ThrottlingException", true, 0},
			{"other error", shBadInput, outcomeError, "InvalidInputException", true, 0},
		} {
			t.Run(row.typ+"/"+c.name, func(t *testing.T) {
				st := newTestStore(t)
				warns := rhWarnRecorder(st)
				calls := newSHTagCalls()
				calls.errs[row.arn] = c.err

				total, _, err := shTagScan(context.Background(), st, shTagHandlers(calls))
				if c.outcome < 0 {
					if err != nil {
						t.Fatalf("err = %v; want nil", err)
					}
				} else {
					assertGlueOutcome(t, err, c.outcome)
					assertAPICode(t, err, c.code)
				}
				wantTotal := shTagTestTotal
				if !c.wantKept {
					wantTotal--
				}
				if total != wantTotal {
					t.Errorf("total = %d; want %d", total, wantTotal)
				}
				r, ok := rowsByNativeID(t, st, row.typ)[row.arn]
				if ok != c.wantKept {
					t.Fatalf("row stored = %v; want %v", ok, c.wantKept)
				}
				if ok && r.TagsJSON != nil {
					t.Errorf("TagsJSON = %q; want nil", *r.TagsJSON)
				}
				w := warns()
				if len(w) != c.wantWarns {
					t.Fatalf("warnings = %v; want %d", w, c.wantWarns)
				}
				for _, x := range w {
					if x.Service != shTagsOp || x.Scope != testAccountID+"/"+testRegion || !strings.Contains(x.Message, row.arn) {
						t.Errorf("warning %+v; want one under %s in %s/%s naming %s", x, shTagsOp, testAccountID, testRegion, row.arn)
					}
				}
			})
		}
	}
}

// Denied tag reads in two listings send one warning for the whole scan,
// under the tag op, naming the first denied row; the rows are kept untagged.
func TestScanSecurityHub_TagDenialsWarnOncePerScan(t *testing.T) {
	st := newTestStore(t)
	warnings := bacWarnings(st, shTagsOp)
	calls := newSHTagCalls()
	ops := []string{"ListAutomationRules", "ListConnectorsV2"}
	for _, op := range ops {
		k := shTagKindOf(t, op)
		calls.errs[k.arn("tagged")] = shDenied
		calls.errs[k.arn("bare")] = shDenied
	}

	total, _, err := shTagScan(context.Background(), st, shTagHandlers(calls))
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != shTagTestTotal {
		t.Errorf("total = %d; want %d", total, shTagTestTotal)
	}
	first := shTagKindOf(t, ops[0]).arn("tagged")
	if len(*warnings) != 1 || (*warnings)[0].Scope != testAccountID+"/"+testRegion || !strings.Contains((*warnings)[0].Message, first) {
		t.Fatalf("%s warnings = %v; want 1 in %s/%s naming %s", shTagsOp, *warnings, testAccountID, testRegion, first)
	}
	for _, op := range ops {
		k := shTagKindOf(t, op)
		for _, arn := range []string{k.arn("tagged"), k.arn("bare")} {
			if r, ok := rowsByNativeID(t, st, k.typ)[arn]; !ok || r.TagsJSON != nil {
				t.Errorf("%s stored %v; want stored untagged", arn, ok)
			}
		}
	}
}

// A throttled tag read beside a hard failure of the same op is demoted to one
// warning, so the hard error decides the outcome.
func TestScanSecurityHub_TagThrottleDemotedBesideHardError(t *testing.T) {
	st := newTestStore(t)
	warns := countOpWarnings(st, shTagsOp)
	calls := newSHTagCalls()
	calls.errs[shHubARN(testRegion)] = shThrottle
	calls.errs[shTagKindOf(t, "ListConnectorsV2").arn("bare")] = shBadInput

	_, _, err := shTagScan(context.Background(), st, shTagHandlers(calls))
	assertGlueOutcome(t, err, outcomeError)
	assertAPICode(t, err, "InvalidInputException")
	if *warns != 1 {
		t.Errorf("%s warnings = %d; want 1", shTagsOp, *warns)
	}
}

// A failed write of a listing's first page stops the scan: its second page
// is not read, no later phase runs and no warning is sent about rows the
// write lost; the phases before it keep their rows.
func TestScanSecurityHub_FailedTaggedWriteStopsScan(t *testing.T) {
	kinds := shTagKinds()
	for i, k := range kinds {
		t.Run(k.op, func(t *testing.T) {
			st := newTestStore(t)
			warns := rhWarnRecorder(st)
			refuseStoreWrites(t, st, k.typ)
			calls := newSHTagCalls()
			calls.errs[k.arn("tagged")] = shDenied

			total, _, err := shTagScan(context.Background(), st, shTagHandlers(calls))
			if !errors.Is(err, store.ErrStoreWrite) {
				t.Fatalf("err = %v; want store.ErrStoreWrite", err)
			}
			if w := warns(); len(w) != 0 {
				t.Errorf("warnings = %v; want none", w)
			}
			if n := calls.count(k.op); n != 1 {
				t.Errorf("%s called %d times; want 1, none after the failed write", k.op, n)
			}
			later := []string{"ListFindingAggregators", "ListConfigurationPolicyAssociations", "ListSecurityControlDefinitions", "DescribeStandards", "DescribeActionTargets", "ListMembers"}
			for _, after := range kinds[i+1:] {
				later = append(later, after.op)
			}
			for _, op := range later {
				if n := calls.count(op); n != 0 {
					t.Errorf("%s called %d times after the failed write; want 0", op, n)
				}
			}
			// The hub, the V2 hub, the organization configuration and both
			// rows of every earlier listing were stored before it.
			if want := 3 + 2*i; total != want {
				t.Errorf("total = %d; want %d", total, want)
			}
		})
	}
}

// A listing's failed page keeps the page read before it, and its error does
// not stop the later listings.
func TestScanSecurityHub_TaggedListFailureKeepsPagesAndLaterPhases(t *testing.T) {
	st := newTestStore(t)
	handlers := shTagHandlers(newSHTagCalls())
	k := shTagKindOf(t, "ListAutomationRules")
	handlers[k.op] = failOnCall(2, shBadInput, handlers[k.op])

	total, _, err := shTagScan(context.Background(), st, handlers)
	assertGlueOutcome(t, err, outcomeError)
	assertAPICode(t, err, "InvalidInputException")
	if total != shTagTestTotal-1 {
		t.Errorf("total = %d; want %d", total, shTagTestTotal-1)
	}
	if _, ok := rowsByNativeID(t, st, k.typ)[k.arn("tagged")]; !ok {
		t.Error("rule from the page before the failure missing")
	}
	if got := len(rowsByNativeID(t, st, TypeSecurityHubConnectorV2)); got != 2 {
		t.Errorf("connectors (a later phase) = %d; want 2", got)
	}
}

// A repeated token ends a hand-rolled listing as an error, keeping its pages.
func TestScanSecurityHub_RepeatedTokenKeepsPages(t *testing.T) {
	for _, op := range []string{"ListAutomationRules", "ListAutomationRulesV2", "ListConnectorsV2"} {
		t.Run(op, func(t *testing.T) {
			st := newTestStore(t)
			handlers := shTagHandlers(newSHTagCalls())
			k := shTagKindOf(t, op)
			handlers[k.op] = func(in any) (any, error) {
				suffix := "tagged"
				if bacField[*string](in, "NextToken") != nil {
					suffix = "bare"
				}
				arn, name := k.arn(suffix), k.name(suffix)
				return k.page(&arn, &name, sdkaws.String("same")), nil
			}
			total, _, err := shTagScan(context.Background(), st, handlers)
			if !errors.Is(err, errRepeatedNextToken) {
				t.Fatalf("err = %v; want errRepeatedNextToken", err)
			}
			if total != shTagTestTotal {
				t.Errorf("total = %d; want %d", total, shTagTestTotal)
			}
		})
	}
}

// An element without an ARN is not stored, is not sent to the tag op, and is
// warned about once under its listing, named by its name, its id, or the
// fallback text when it has none.
func TestScanSecurityHub_ElementWithoutARNWarns(t *testing.T) {
	for _, c := range []struct {
		name, op, key string
		answer        func(in any) (any, error)
		lost          int
	}{
		{"aggregator", "ListAggregatorsV2", noIdentifier, func(any) (any, error) {
			return &securityhub.ListAggregatorsV2Output{AggregatorsV2: []securityhubtypes.AggregatorV2{{}}}, nil
		}, 2},
		{"automation rule", "ListAutomationRules", "r-x", func(any) (any, error) {
			return &securityhub.ListAutomationRulesOutput{AutomationRulesMetadata: []securityhubtypes.AutomationRulesMetadata{{RuleName: sdkaws.String("r-x")}}}, nil
		}, 2},
		{"V2 automation rule by id", "ListAutomationRulesV2", "r2-id", func(any) (any, error) {
			return &securityhub.ListAutomationRulesV2Output{Rules: []securityhubtypes.AutomationRulesMetadataV2{{RuleId: sdkaws.String("r2-id")}}}, nil
		}, 2},
		{"configuration policy by id", "ListConfigurationPolicies", "cp-id", func(any) (any, error) {
			return &securityhub.ListConfigurationPoliciesOutput{ConfigurationPolicySummaries: []securityhubtypes.ConfigurationPolicySummary{{Id: sdkaws.String("cp-id")}}}, nil
		}, 2},
		{"connector", "ListConnectorsV2", "c-x", func(any) (any, error) {
			return &securityhub.ListConnectorsV2Output{Connectors: []securityhubtypes.ConnectorSummary{{Name: sdkaws.String("c-x")}}}, nil
		}, 2},
		{"V2 hub", "DescribeSecurityHubV2", "hub-v2", func(any) (any, error) {
			return &securityhub.DescribeSecurityHubV2Output{SubscribedAt: sdkaws.String("2026-01-02T03:04:05Z")}, nil
		}, 1},
	} {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := rhWarnRecorder(st)
			calls := newSHTagCalls()
			handlers := shTagHandlers(calls)
			handlers[c.op] = c.answer

			total, _, err := shTagScan(context.Background(), st, handlers)
			if err != nil {
				t.Fatalf("scan: %v", err)
			}
			if want := shTagTestTotal - c.lost; total != want {
				t.Errorf("total = %d; want %d", total, want)
			}
			w := warns()
			if len(w) != 1 {
				t.Fatalf("warnings = %v; want 1", w)
			}
			if w[0].Service != "securityhub:"+c.op || !strings.Contains(w[0].Message, c.key) || w[0].Scope != testAccountID+"/"+testRegion {
				t.Errorf("warning %+v; want one under securityhub:%s naming %q in %s/%s", w[0], c.op, c.key, testAccountID, testRegion)
			}
			if slices.Contains(calls.sentARNs(), "") {
				t.Error("an empty ARN was sent to ListTagsForResource")
			}
		})
	}
}

// A listing refused as not enabled on its first page stores nothing,
// silently, and every other listing is stored; the V2 hub not found likewise.
// The same answer on a later page is an error that keeps the page before it.
func TestScanSecurityHub_TaggedListingNotEnabled(t *testing.T) {
	for _, k := range shTagKinds() {
		t.Run(k.op+"/first page", func(t *testing.T) {
			st := newTestStore(t)
			warns := rhWarnRecorder(st)
			handlers := shTagHandlers(newSHTagCalls())
			handlers[k.op] = func(any) (any, error) { return nil, shNotEnabled }
			total, _, err := shTagScan(context.Background(), st, handlers)
			if err != nil {
				t.Fatalf("err = %v; want nil", err)
			}
			if want := shTagTestTotal - 2; total != want {
				t.Errorf("total = %d; want %d", total, want)
			}
			if w := warns(); len(w) != 0 {
				t.Errorf("warnings = %v; want none", w)
			}
		})
	}
	// ListAggregatorsV2 and ListConnectorsV2 model ResourceNotFoundException,
	// not InvalidAccessException.
	for _, op := range []string{"ListAggregatorsV2", "ListConnectorsV2"} {
		t.Run(op+"/first page not found", func(t *testing.T) {
			st := newTestStore(t)
			warns := rhWarnRecorder(st)
			handlers := shTagHandlers(newSHTagCalls())
			handlers[op] = func(any) (any, error) { return nil, shNotFound }
			total, _, err := shTagScan(context.Background(), st, handlers)
			if err != nil {
				t.Fatalf("err = %v; want nil", err)
			}
			if want := shTagTestTotal - 2; total != want {
				t.Errorf("total = %d; want %d", total, want)
			}
			if w := warns(); len(w) != 0 {
				t.Errorf("warnings = %v; want none", w)
			}
		})
	}
	t.Run("V2 hub not found", func(t *testing.T) {
		st := newTestStore(t)
		warns := rhWarnRecorder(st)
		handlers := shTagHandlers(newSHTagCalls())
		handlers["DescribeSecurityHubV2"] = func(any) (any, error) { return nil, shNotFound }
		total, _, err := shTagScan(context.Background(), st, handlers)
		if err != nil {
			t.Fatalf("err = %v; want nil", err)
		}
		if want := shTagTestTotal - 1; total != want {
			t.Errorf("total = %d; want %d", total, want)
		}
		if w := warns(); len(w) != 0 {
			t.Errorf("warnings = %v; want none", w)
		}
	})
	t.Run("later page", func(t *testing.T) {
		st := newTestStore(t)
		handlers := shTagHandlers(newSHTagCalls())
		k := shTagKindOf(t, "ListConfigurationPolicies")
		handlers[k.op] = failOnCall(2, shNotEnabled, handlers[k.op])
		total, _, err := shTagScan(context.Background(), st, handlers)
		assertGlueOutcome(t, err, outcomeError)
		assertAPICode(t, err, "InvalidAccessException")
		if total != shTagTestTotal-1 {
			t.Errorf("total = %d; want %d", total, shTagTestTotal-1)
		}
		if _, ok := rowsByNativeID(t, st, k.typ)[k.arn("tagged")]; !ok {
			t.Error("policy from the first page missing")
		}
	})
}

// A denied listing warns once under its op; a denial on a later page keeps
// the page read before it. Every other listing still stores its rows. The V2
// hub denied warns the same way.
func TestScanSecurityHub_DeniedTaggedListingWarnsUnderItsOp(t *testing.T) {
	for _, k := range shTagKinds() {
		for _, call := range []int{1, 2} {
			t.Run(fmt.Sprintf("%s/page%d", k.op, call), func(t *testing.T) {
				st := newTestStore(t)
				warns := rhWarnRecorder(st)
				handlers := shTagHandlers(newSHTagCalls())
				handlers[k.op] = failOnCall(call, shDenied, handlers[k.op])

				total, _, err := shTagScan(context.Background(), st, handlers)
				if err != nil {
					t.Fatalf("err = %v; want nil", err)
				}
				if want := shTagTestTotal - 2 + call - 1; total != want {
					t.Errorf("total = %d; want %d", total, want)
				}
				if w := warns(); len(w) != 1 || w[0].Service != "securityhub:"+k.op {
					t.Errorf("warnings = %v; want one under securityhub:%s", w, k.op)
				}
				if got := len(rowsByNativeID(t, st, k.typ)); got != call-1 {
					t.Errorf("%s rows = %d; want %d", k.typ, got, call-1)
				}
				for _, other := range shTagKinds() {
					if other.op == k.op {
						continue
					}
					if got := len(rowsByNativeID(t, st, other.typ)); got != 2 {
						t.Errorf("%s rows = %d; want 2", other.typ, got)
					}
				}
			})
		}
	}
	t.Run("V2 hub", func(t *testing.T) {
		st := newTestStore(t)
		warns := rhWarnRecorder(st)
		handlers := shTagHandlers(newSHTagCalls())
		handlers["DescribeSecurityHubV2"] = func(any) (any, error) { return nil, shDenied }
		total, _, err := shTagScan(context.Background(), st, handlers)
		if err != nil {
			t.Fatalf("err = %v; want nil", err)
		}
		if total != shTagTestTotal-1 {
			t.Errorf("total = %d; want %d", total, shTagTestTotal-1)
		}
		if w := warns(); len(w) != 1 || w[0].Service != "securityhub:DescribeSecurityHubV2" {
			t.Errorf("warnings = %v; want one under securityhub:DescribeSecurityHubV2", w)
		}
	})
}

// A DescribeSecurityHubV2 failure other than not found or a denial stores no
// V2 hub and is the scan's error (a throttle transient), with no warning;
// every listing after it still stores its rows.
func TestScanSecurityHub_HubV2HardError(t *testing.T) {
	for _, c := range []struct {
		name    string
		err     error
		outcome serviceOutcome
		code    string
	}{
		{"validation", apiErr("ValidationException", "bad"), outcomeError, "ValidationException"},
		{"throttle", shThrottle, outcomeTransient, "ThrottlingException"},
	} {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := rhWarnRecorder(st)
			handlers := shTagHandlers(newSHTagCalls())
			handlers["DescribeSecurityHubV2"] = func(any) (any, error) { return nil, c.err }

			total, _, err := shTagScan(context.Background(), st, handlers)
			assertGlueOutcome(t, err, c.outcome)
			assertAPICode(t, err, c.code)
			if total != shTagTestTotal-1 {
				t.Errorf("total = %d; want %d", total, shTagTestTotal-1)
			}
			if w := warns(); len(w) != 0 {
				t.Errorf("warnings = %v; want none", w)
			}
			if got := len(rowsByNativeID(t, st, TypeSecurityHubHubV2)); got != 0 {
				t.Errorf("V2 hub rows = %d; want 0", got)
			}
			if got := len(rowsByNativeID(t, st, TypeSecurityHubConnectorV2)); got != 2 {
				t.Errorf("connectors (a later phase) = %d; want 2", got)
			}
		})
	}
}

// The hub is stored and tag-read under the HubArn DescribeHub returns, even
// when that differs from the ARN the scan would build for the region.
func TestScanSecurityHub_HubUsesReturnedHubArn(t *testing.T) {
	st := newTestStore(t)
	calls := newSHTagCalls()
	handlers := shTagHandlers(calls)
	returned := shTestARN("aws", "eu-west-1", "hub/default")
	calls.tags[returned] = map[string]string{"hub": "returned"}
	handlers["DescribeHub"] = func(any) (any, error) { return &securityhub.DescribeHubOutput{HubArn: &returned}, nil }

	if _, _, err := shTagScan(context.Background(), st, handlers); err != nil {
		t.Fatalf("scan: %v", err)
	}
	hubs := rowsByNativeID(t, st, TypeSecurityHubHub)
	hub, ok := hubs[returned]
	if len(hubs) != 1 || !ok {
		t.Fatalf("hub rows = %v; want only %s", nativeIDs(hubs), returned)
	}
	assertTagsMap(t, hub.TagsJSON, map[string]string{"hub": "returned"})
	want := slices.DeleteFunc(shTagReadARNs(), func(a string) bool { return a == shHubARN(testRegion) })
	want = append(want, returned)
	slices.Sort(want)
	if got := calls.sentARNs(); !slices.Equal(got, want) {
		t.Errorf("ListTagsForResource ARNs = %v\nwant %v", got, want)
	}
}

// A denied DescribeHub warns once and runs no other phase; the hub not
// enabled is the service disabled, silently, and runs no other phase.
func TestScanSecurityHub_HubGate(t *testing.T) {
	for _, c := range []struct {
		name      string
		err       error
		disabled  bool
		wantWarns int
	}{
		{"denied", shDenied, false, 1},
		{"not enabled", shNotEnabled, true, 0},
	} {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := rhWarnRecorder(st)
			calls := newSHTagCalls()
			handlers := shTagHandlers(calls)
			handlers["DescribeHub"] = func(any) (any, error) { return nil, c.err }

			total, _, err := shTagScan(context.Background(), st, handlers)
			if c.disabled {
				assertGlueOutcome(t, err, outcomeDisabled)
			} else if err != nil {
				t.Fatalf("err = %v; want nil", err)
			}
			if total != 0 {
				t.Errorf("total = %d; want 0", total)
			}
			w := warns()
			if len(w) != c.wantWarns {
				t.Fatalf("warnings = %v; want %d", w, c.wantWarns)
			}
			if len(w) == 1 && w[0].Service != shDescribeHubOp {
				t.Errorf("warning %+v; want one under %s", w[0], shDescribeHubOp)
			}
			for op := range handlers {
				if n := calls.count(op); n != 0 {
					t.Errorf("%s called %d times; want 0", op, n)
				}
			}
		})
	}
}

// Rows of the aws-us-gov and aws-cn partitions reach the tag op under their
// listed ARNs unchanged, and a hub DescribeHub returns without HubArn is
// stored and read under the hub ARN of the account's partition.
func TestScanSecurityHub_TagReadsKeepPartitionARNs(t *testing.T) {
	for _, pf := range partitionFixtures {
		t.Run(pf.partition, func(t *testing.T) {
			st := newTestStore(t)
			calls := newSHTagCalls()
			handlers := shTagHandlers(calls)
			hubARN := shTestARN(pf.partition, pf.region, "hub/default")
			connector := shTestARN(pf.partition, pf.region, "connectorv2/c-p")
			calls.tags[hubARN] = map[string]string{"hub": "p"}
			calls.tags[connector] = map[string]string{"k": "v"}
			handlers["DescribeHub"] = func(any) (any, error) { return &securityhub.DescribeHubOutput{}, nil }
			handlers["ListConnectorsV2"] = func(any) (any, error) {
				return &securityhub.ListConnectorsV2Output{Connectors: []securityhubtypes.ConnectorSummary{{ConnectorArn: &connector, Name: sdkaws.String("c-p")}}}, nil
			}

			if _, _, err := shTagScanIn(context.Background(), st, partitionAccount(pf.partition), pf.region, handlers); err != nil {
				t.Fatalf("scan: %v", err)
			}
			for _, want := range []string{hubARN, connector} {
				if !slices.Contains(calls.sentARNs(), want) {
					t.Errorf("ListTagsForResource ARNs %v lack %s", calls.sentARNs(), want)
				}
			}
			assertTagsMap(t, rowsByNativeID(t, st, TypeSecurityHubHub)[hubARN].TagsJSON, map[string]string{"hub": "p"})
			assertTagsMap(t, rowsByNativeID(t, st, TypeSecurityHubConnectorV2)[connector].TagsJSON, map[string]string{"k": "v"})
		})
	}
}

// Once ctx ends no further phase starts, and the stop is one member of the
// error, although the phase in flight saw it in its listing.
func TestScanSecurityHub_ContextEndReportedOnce(t *testing.T) {
	st := newTestStore(t)
	calls := newSHTagCalls()
	handlers := shTagHandlers(calls)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	k := shTagKindOf(t, "ListConfigurationPolicies")
	list := handlers[k.op]
	handlers[k.op] = func(in any) (any, error) {
		if bacField[*string](in, "NextToken") != nil {
			cancel()
			return nil, context.Canceled
		}
		return list(in)
	}

	_, _, err := shTagScan(ctx, st, handlers)
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
	if n := calls.count("ListConnectorsV2"); n != 0 {
		t.Errorf("ListConnectorsV2 called %d times after the stop; want 0", n)
	}
}

// An NXDOMAIN after rows were stored is a failure of this scan, not a region
// gap: the counts are kept. With nothing stored it stays region-unavailable.
func TestScanSecurityHub_NXDOMAIN(t *testing.T) {
	nx := &net.DNSError{Err: "no such host", Name: "securityhub.us-east-1.amazonaws.com", IsNotFound: true}
	t.Run("a listing after others stored rows", func(t *testing.T) {
		st := newTestStore(t)
		handlers := shTagHandlers(newSHTagCalls())
		handlers["ListConnectorsV2"] = func(any) (any, error) { return nil, nx }
		total, _, err := shTagScan(context.Background(), st, handlers)
		assertGlueOutcome(t, err, outcomeError)
		if total != shTagTestTotal-2 {
			t.Errorf("total = %d; want the %d other rows counted", total, shTagTestTotal-2)
		}
	})
	t.Run("tag read after the listing", func(t *testing.T) {
		st := newTestStore(t)
		calls := newSHTagCalls()
		calls.errs[shTagKindOf(t, "ListAggregatorsV2").arn("bare")] = nx
		_, _, err := shTagScan(context.Background(), st, shTagHandlers(calls))
		assertGlueOutcome(t, err, outcomeError)
	})
	t.Run("nothing stored", func(t *testing.T) {
		st := newTestStore(t)
		warns := rhWarnRecorder(st)
		acct := newTestAccount(testAccountID)
		acct.cfg = opStubConfig(nil, nx)
		_, _, err := scanSecurityHub(context.Background(), acct, testRegion, st, testScanID)
		assertGlueOutcome(t, err, outcomeUnavailable)
		if w := warns(); len(w) != 0 {
			t.Errorf("warnings = %v; want none", w)
		}
	})
}

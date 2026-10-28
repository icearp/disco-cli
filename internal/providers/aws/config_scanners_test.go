package aws

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"slices"
	"strings"
	"sync"
	"testing"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/configservice"
	cfgtypes "github.com/aws/aws-sdk-go-v2/service/configservice/types"
	"github.com/icearp/disco-cli/store"
)

var (
	cfgThrottle   = apiErr("ThrottlingException", "slow down")
	cfgValidation = apiErr("ValidationException", "bad arn")
	cfgOrgDenied  = apiErr("OrganizationAccessDeniedException", "not the management account")
)

const (
	cfgTagsOp = "config:ListTagsForResource"
	// cfgTaggedCount is the rows of the tagged types the full fixture stores:
	// two per listing.
	cfgTaggedCount = 18
	// cfgTotalCount adds the untagged rows: one delivery channel, one
	// retention configuration and one remediation configuration per rule.
	cfgTotalCount = cfgTaggedCount + 4
)

func cfgARN(resource string) string {
	return "arn:aws:config:" + testRegion + ":" + testAccountID + ":" + resource
}

// cfgKind is one tagged listing of the fixture: its op, the type it stores,
// the ARN resource prefix, the Name a row of suffix ("tagged" or "bare") is
// stored under, and page, which builds the op's output from one element's ARN
// and suffix and the next token.
type cfgKind struct {
	op, typ, prefix string
	name            func(suffix string) string
	page            func(arn, suffix string, next *string) any
}

func cfgSuffixName(label string) func(string) string {
	return func(suffix string) string { return label + "-" + suffix }
}

func cfgKinds() []cfgKind {
	return []cfgKind{
		{"DescribeConfigRules", TypeConfigRule, "config-rule/", cfgSuffixName("rule"), func(arn, suffix string, next *string) any {
			return &configservice.DescribeConfigRulesOutput{ConfigRules: []cfgtypes.ConfigRule{{
				ConfigRuleArn: &arn, ConfigRuleName: sdkaws.String("rule-" + suffix), ConfigRuleState: cfgtypes.ConfigRuleStateActive,
			}}, NextToken: next}
		}},
		{"DescribeAggregationAuthorizations", TypeConfigAggregationAuthorization, "aggregation-authorization/", func(suffix string) string { return suffix + "/eu-west-1" }, func(arn, suffix string, next *string) any {
			return &configservice.DescribeAggregationAuthorizationsOutput{AggregationAuthorizations: []cfgtypes.AggregationAuthorization{{
				AggregationAuthorizationArn: &arn, AuthorizedAccountId: sdkaws.String(suffix), AuthorizedAwsRegion: sdkaws.String("eu-west-1"),
			}}, NextToken: next}
		}},
		{"DescribeConfigurationAggregators", TypeConfigConfigurationAggregator, "config-aggregator/", cfgSuffixName("agg"), func(arn, suffix string, next *string) any {
			return &configservice.DescribeConfigurationAggregatorsOutput{ConfigurationAggregators: []cfgtypes.ConfigurationAggregator{{
				ConfigurationAggregatorArn: &arn, ConfigurationAggregatorName: sdkaws.String("agg-" + suffix),
			}}, NextToken: next}
		}},
		{"DescribeConformancePacks", TypeConfigConformancePack, "conformance-pack/", cfgSuffixName("pack"), func(arn, suffix string, next *string) any {
			return &configservice.DescribeConformancePacksOutput{ConformancePackDetails: []cfgtypes.ConformancePackDetail{{
				ConformancePackArn: &arn, ConformancePackName: sdkaws.String("pack-" + suffix),
			}}, NextToken: next}
		}},
		{"DescribeOrganizationConfigRules", TypeConfigOrganizationConfigRule, "organization-config-rule/", cfgSuffixName("org-rule"), func(arn, suffix string, next *string) any {
			return &configservice.DescribeOrganizationConfigRulesOutput{OrganizationConfigRules: []cfgtypes.OrganizationConfigRule{{
				OrganizationConfigRuleArn: &arn, OrganizationConfigRuleName: sdkaws.String("org-rule-" + suffix),
			}}, NextToken: next}
		}},
		{"DescribeOrganizationConformancePacks", TypeConfigOrganizationConformancePack, "organization-conformance-pack/", cfgSuffixName("org-pack"), func(arn, suffix string, next *string) any {
			return &configservice.DescribeOrganizationConformancePacksOutput{OrganizationConformancePacks: []cfgtypes.OrganizationConformancePack{{
				OrganizationConformancePackArn: &arn, OrganizationConformancePackName: sdkaws.String("org-pack-" + suffix),
			}}, NextToken: next}
		}},
		{"ListStoredQueries", TypeConfigStoredQuery, "stored-query/", cfgSuffixName("query"), func(arn, suffix string, next *string) any {
			return &configservice.ListStoredQueriesOutput{StoredQueryMetadata: []cfgtypes.StoredQueryMetadata{{
				QueryArn: &arn, QueryId: sdkaws.String("q-" + suffix), QueryName: sdkaws.String("query-" + suffix),
			}}, NextToken: next}
		}},
		{"ListConnectors", TypeConfigConnector, "connector/azure/tenant-1/", cfgSuffixName("conn"), func(arn, suffix string, next *string) any {
			return &configservice.ListConnectorsOutput{ConnectorSummaries: []cfgtypes.ConnectorSummary{{
				Arn: &arn, Name: sdkaws.String("conn-" + suffix), Provider: cfgtypes.ProviderAzure, CreatedTime: &configConnectorCreated,
			}}, NextToken: next}
		}},
	}
}

func cfgKindOf(t *testing.T, op string) cfgKind {
	t.Helper()
	for _, k := range cfgKinds() {
		if k.op == op {
			return k
		}
	}
	t.Fatalf("no fixture kind for %s", op)
	return cfgKind{}
}

func cfgKindARN(k cfgKind, suffix string) string { return cfgARN(k.prefix + k.name(suffix)) }

func cfgRecorderARN(suffix string) string {
	return cfgARN("configuration-recorder/recorder-" + suffix + "/id-" + suffix)
}

// cfgCalls records the stubbed calls: how often each op ran, the page size
// each was sent (Limit or MaxResults, 0 for none), the rule names sent to
// DescribeRemediationConfigurations, and the ARNs sent to
// ListTagsForResource, which it answers from tags or errs, both keyed by ARN.
// Tag and Get reads run concurrently.
type cfgCalls struct {
	mu        sync.Mutex
	ops       map[string]int
	pageSizes map[string][]int32
	ruleNames []string
	tagARNs   []string
	tags      map[string]map[string]string
	errs      map[string]error
	getErrs   map[string]error
}

// newCfgCalls tags every "tagged" resource with its type.
func newCfgCalls() *cfgCalls {
	c := &cfgCalls{ops: map[string]int{}, pageSizes: map[string][]int32{}, tags: map[string]map[string]string{}, errs: map[string]error{}, getErrs: map[string]error{}}
	for _, k := range cfgKinds() {
		c.tags[cfgKindARN(k, "tagged")] = map[string]string{"type": k.typ}
	}
	c.tags[cfgRecorderARN("tagged")] = map[string]string{"type": TypeConfigRecorder}
	return c
}

func cfgPageSize(in any) int32 {
	if n := bacField[int32](in, "Limit"); n != 0 {
		return n
	}
	if n := bacField[int32](in, "MaxResults"); n != 0 {
		return n
	}
	return sdkaws.ToInt32(bacField[*int32](in, "MaxResults"))
}

func (c *cfgCalls) record(op string, in any) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.ops[op]++
	c.pageSizes[op] = append(c.pageSizes[op], cfgPageSize(in))
}

func (c *cfgCalls) count(op string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.ops[op]
}

func (c *cfgCalls) listTags(in any) (any, error) {
	arn := sv(in.(*configservice.ListTagsForResourceInput).ResourceArn)
	c.record("ListTagsForResource", in)
	c.mu.Lock()
	c.tagARNs = append(c.tagARNs, arn)
	err, tags := c.errs[arn], c.tags[arn]
	c.mu.Unlock()
	if err != nil {
		return nil, err
	}
	var list []cfgtypes.Tag
	for k, v := range tags {
		list = append(list, cfgtypes.Tag{Key: sdkaws.String(k), Value: sdkaws.String(v)})
	}
	return &configservice.ListTagsForResourceOutput{Tags: list}, nil
}

// twoPages answers k's op with "tagged" on the first page and "bare" on the
// second.
func (c *cfgCalls) twoPages(k cfgKind) func(any) (any, error) {
	return func(in any) (any, error) {
		c.record(k.op, in)
		suffix, next := "tagged", sdkaws.String("t2")
		if bacField[*string](in, "NextToken") != nil {
			suffix, next = "bare", nil
		}
		return k.page(cfgKindARN(k, suffix), suffix, next), nil
	}
}

func (c *cfgCalls) getConnector(in any) (any, error) {
	arn := sv(in.(*configservice.GetConnectorInput).Arn)
	c.record("GetConnector", in)
	c.mu.Lock()
	err := c.getErrs[arn]
	c.mu.Unlock()
	if err != nil {
		return nil, err
	}
	return configConnectorBody(arn)
}

func cfgHandlers(c *cfgCalls) map[string]func(any) (any, error) {
	h := map[string]func(any) (any, error){
		"ListTagsForResource": c.listTags,
		"GetConnector":        c.getConnector,
		"DescribeConfigurationRecorders": func(in any) (any, error) {
			c.record("DescribeConfigurationRecorders", in)
			var recs []cfgtypes.ConfigurationRecorder
			for _, suffix := range []string{"tagged", "bare"} {
				recs = append(recs, cfgtypes.ConfigurationRecorder{Arn: sdkaws.String(cfgRecorderARN(suffix)), Name: sdkaws.String("recorder-" + suffix), RoleARN: sdkaws.String("arn:aws:iam::123456789012:role/config")})
			}
			return &configservice.DescribeConfigurationRecordersOutput{ConfigurationRecorders: recs}, nil
		},
		"DescribeDeliveryChannels": func(in any) (any, error) {
			c.record("DescribeDeliveryChannels", in)
			return &configservice.DescribeDeliveryChannelsOutput{DeliveryChannels: []cfgtypes.DeliveryChannel{{Name: sdkaws.String("default"), S3BucketName: sdkaws.String("bucket")}}}, nil
		},
		"DescribeRemediationConfigurations": func(in any) (any, error) {
			names := in.(*configservice.DescribeRemediationConfigurationsInput).ConfigRuleNames
			c.record("DescribeRemediationConfigurations", in)
			c.mu.Lock()
			c.ruleNames = append(c.ruleNames, names...)
			c.mu.Unlock()
			out := &configservice.DescribeRemediationConfigurationsOutput{}
			for _, n := range names {
				out.RemediationConfigurations = append(out.RemediationConfigurations, cfgtypes.RemediationConfiguration{
					ConfigRuleName: sdkaws.String(n), Arn: sdkaws.String(cfgARN("remediation-configuration/" + n + "/id")),
				})
			}
			return out, nil
		},
		"DescribeRetentionConfigurations": func(in any) (any, error) {
			c.record("DescribeRetentionConfigurations", in)
			return &configservice.DescribeRetentionConfigurationsOutput{RetentionConfigurations: []cfgtypes.RetentionConfiguration{{Name: sdkaws.String("default"), RetentionPeriodInDays: sdkaws.Int32(400)}}}, nil
		},
		"DescribeRemediationExceptions": func(in any) (any, error) {
			c.record("DescribeRemediationExceptions", in)
			return &configservice.DescribeRemediationExceptionsOutput{}, nil
		},
	}
	for _, k := range cfgKinds() {
		h[k.op] = c.twoPages(k)
	}
	return h
}

// cfgScan runs the scan's real entry point against handlers; every op they
// do not answer is denied.
func cfgScan(ctx context.Context, st *store.Store, handlers map[string]func(any) (any, error)) (int, int, error) {
	acct := newTestAccount(testAccountID)
	acct.cfg = opStubConfig(handlers, configDenied)
	return scanConfig(ctx, acct, testRegion, st, testScanID)
}

func cfgRowOf(t *testing.T, st *store.Store, typ, arn string) (store.Resource, bool) {
	t.Helper()
	r, ok := rowsByNativeID(t, st, typ)[arn]
	return r, ok
}

// cfgWant is one tagged row the full fixture stores: its type, ARN, Name and
// tags (nil for none).
type cfgWant struct {
	typ, arn, name string
	tags           map[string]string
}

func cfgWants() []cfgWant {
	var want []cfgWant
	for _, suffix := range []string{"tagged", "bare"} {
		w := cfgWant{TypeConfigRecorder, cfgRecorderARN(suffix), "recorder-" + suffix, nil}
		if suffix == "tagged" {
			w.tags = map[string]string{"type": TypeConfigRecorder}
		}
		want = append(want, w)
		for _, k := range cfgKinds() {
			w := cfgWant{k.typ, cfgKindARN(k, suffix), k.name(suffix), nil}
			if suffix == "tagged" {
				w.tags = map[string]string{"type": k.typ}
			}
			want = append(want, w)
		}
	}
	return want
}

// Every tagged type stores both pages of its listing, named as before, with
// the tags ListTagsForResource returns for its ARN, and nil for a resource
// without tags. Exactly the ARN of every tagged row is sent to the tag op,
// each with Limit 50; every listing is sent its model's page cap; the
// untagged types are stored too. No error or warning.
func TestScanConfig_StoresTagsOfEveryType(t *testing.T) {
	st := newTestStore(t)
	warns := countWarnings(st)
	calls := newCfgCalls()

	total, inserted, err := cfgScan(context.Background(), st, cfgHandlers(calls))
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != cfgTotalCount || inserted != cfgTotalCount {
		t.Errorf("total, inserted = %d, %d; want %d, %d", total, inserted, cfgTotalCount, cfgTotalCount)
	}
	if *warns != 0 {
		t.Errorf("warnings = %d; want 0", *warns)
	}
	var wantARNs []string
	for _, w := range cfgWants() {
		wantARNs = append(wantARNs, w.arn)
		r, ok := cfgRowOf(t, st, w.typ, w.arn)
		if !ok {
			t.Errorf("%s %s missing", w.typ, w.arn)
			continue
		}
		if got := sv(r.Name); got != w.name {
			t.Errorf("%s %s Name = %q; want %q", w.typ, w.arn, got, w.name)
		}
		if sv(r.Region) != testRegion {
			t.Errorf("%s %s Region = %q; want %s", w.typ, w.arn, sv(r.Region), testRegion)
		}
		if w.tags != nil {
			assertTagsMap(t, r.TagsJSON, w.tags)
		} else if r.TagsJSON != nil {
			t.Errorf("%s %s TagsJSON = %q; want nil", w.typ, w.arn, *r.TagsJSON)
		}
	}
	slices.Sort(wantARNs)
	slices.Sort(calls.tagARNs)
	if !slices.Equal(calls.tagARNs, wantARNs) {
		t.Errorf("ListTagsForResource ARNs = %v\nwant %v", calls.tagARNs, wantARNs)
	}
	for op, want := range map[string]int32{
		"ListTagsForResource":                  50,
		"DescribeConfigRules":                  0,
		"DescribeAggregationAuthorizations":    100,
		"DescribeConfigurationAggregators":     100,
		"DescribeConformancePacks":             20,
		"DescribeOrganizationConfigRules":      100,
		"DescribeOrganizationConformancePacks": 100,
		"ListStoredQueries":                    100,
		"ListConnectors":                       100,
	} {
		if len(calls.pageSizes[op]) == 0 {
			t.Errorf("%s not called", op)
		}
		for _, n := range calls.pageSizes[op] {
			if n != want {
				t.Errorf("%s page size = %d; want %d", op, n, want)
			}
		}
	}
	rule, _ := cfgRowOf(t, st, TypeConfigRule, cfgKindARN(cfgKindOf(t, "DescribeConfigRules"), "tagged"))
	if sv(rule.Status) != "ACTIVE" {
		t.Errorf("rule Status = %q; want ACTIVE", sv(rule.Status))
	}
	conn, _ := cfgRowOf(t, st, TypeConfigConnector, cfgKindARN(cfgKindOf(t, "ListConnectors"), "tagged"))
	var body cfgtypes.Connector
	if err := json.Unmarshal([]byte(conn.AttributesJSON), &body); err != nil || body.ConnectorConfiguration == nil {
		t.Errorf("connector attrs = %s (%v); want the GetConnector body", conn.AttributesJSON, err)
	}
	if sv(conn.CreatedAt) != "2026-01-02T03:04:05Z" {
		t.Errorf("connector CreatedAt = %q; want 2026-01-02T03:04:05Z", sv(conn.CreatedAt))
	}
	for typ, n := range map[string]int{TypeConfigDeliveryChannel: 1, TypeConfigRetentionConfiguration: 1, TypeConfigRemediationConfiguration: 2} {
		if got := len(rowsByNativeID(t, st, typ)); got != n {
			t.Errorf("%s rows = %d; want %d", typ, got, n)
		}
	}
}

// The remediation phases take the rules the rule phase stored: the rules are
// listed once (two pages), and both rule names reach
// DescribeRemediationConfigurations.
func TestScanConfig_RemediationPhasesUseStoredRules(t *testing.T) {
	st := newTestStore(t)
	calls := newCfgCalls()
	if _, _, err := cfgScan(context.Background(), st, cfgHandlers(calls)); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if n := calls.count("DescribeConfigRules"); n != 2 {
		t.Errorf("DescribeConfigRules calls = %d; want 2 (one listing of two pages)", n)
	}
	slices.Sort(calls.ruleNames)
	if want := []string{"rule-bare", "rule-tagged"}; !slices.Equal(calls.ruleNames, want) {
		t.Errorf("remediation rule names = %v; want %v", calls.ruleNames, want)
	}
	if n := calls.count("DescribeRemediationExceptions"); n != 2 {
		t.Errorf("DescribeRemediationExceptions calls = %d; want 2", n)
	}
}

// A rescan stores the same rows without inserting any.
func TestScanConfig_RescanInsertsNone(t *testing.T) {
	st := newTestStore(t)
	calls := newCfgCalls()
	if _, _, err := cfgScan(context.Background(), st, cfgHandlers(calls)); err != nil {
		t.Fatalf("first scan: %v", err)
	}
	total, inserted, err := cfgScan(context.Background(), st, cfgHandlers(calls))
	if err != nil {
		t.Fatalf("rescan: %v", err)
	}
	if total != cfgTotalCount || inserted != 0 {
		t.Errorf("rescan total, inserted = %d, %d; want %d, 0", total, inserted, cfgTotalCount)
	}
}

// A failed tag read: ResourceNotFoundException, the op's modeled not-found,
// drops the row silently; a denial keeps the row untagged and warns once
// under config:ListTagsForResource; a throttle after retries keeps it and is
// a transient error; ValidationException keeps it and is a hard error.
func TestScanConfig_TagReadFailures(t *testing.T) {
	for _, target := range []struct{ typ, arn string }{
		{TypeConfigStoredQuery, cfgKindARN(cfgKindOf(t, "ListStoredQueries"), "tagged")},
		{TypeConfigRecorder, cfgRecorderARN("tagged")},
		{TypeConfigConnector, cfgKindARN(cfgKindOf(t, "ListConnectors"), "bare")},
	} {
		t.Run(target.typ, func(t *testing.T) {
			for _, c := range []struct {
				name      string
				err       error
				outcome   serviceOutcome
				code      string
				wantTotal int
				wantKept  bool
				wantWarns int
			}{
				{"not found", configNotFound, -1, "", cfgTotalCount - 1, false, 0},
				{"denied", configDenied, -1, "", cfgTotalCount, true, 1},
				{"transient", cfgThrottle, outcomeTransient, "ThrottlingException", cfgTotalCount, true, 0},
				{"other error", cfgValidation, outcomeError, "ValidationException", cfgTotalCount, true, 0},
			} {
				t.Run(c.name, func(t *testing.T) {
					st := newTestStore(t)
					all := countWarnings(st)
					warnings := bacWarnings(st, cfgTagsOp)
					calls := newCfgCalls()
					calls.errs[target.arn] = c.err

					total, _, err := cfgScan(context.Background(), st, cfgHandlers(calls))
					if c.outcome < 0 {
						if err != nil {
							t.Fatalf("err = %v; want nil", err)
						}
					} else {
						assertGlueOutcome(t, err, c.outcome)
						assertAPICode(t, err, c.code)
					}
					if total != c.wantTotal {
						t.Errorf("total = %d; want %d", total, c.wantTotal)
					}
					r, ok := cfgRowOf(t, st, target.typ, target.arn)
					if ok != c.wantKept {
						t.Fatalf("row stored = %v; want %v", ok, c.wantKept)
					}
					if ok && r.TagsJSON != nil {
						t.Errorf("TagsJSON = %q; want nil", *r.TagsJSON)
					}
					if len(*warnings) != c.wantWarns || *all != c.wantWarns {
						t.Fatalf("%s warnings = %v (all ops %d); want %d", cfgTagsOp, *warnings, *all, c.wantWarns)
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

// A rule the tag read finds deleted is dropped and is not a parent of the
// remediation phases.
func TestScanConfig_RuleGoneIsNoRemediationParent(t *testing.T) {
	st := newTestStore(t)
	calls := newCfgCalls()
	calls.errs[cfgKindARN(cfgKindOf(t, "DescribeConfigRules"), "bare")] = configNotFound
	if _, _, err := cfgScan(context.Background(), st, cfgHandlers(calls)); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if want := []string{"rule-tagged"}; !slices.Equal(calls.ruleNames, want) {
		t.Errorf("remediation rule names = %v; want %v", calls.ruleNames, want)
	}
	if n := calls.count("DescribeRemediationExceptions"); n != 1 {
		t.Errorf("DescribeRemediationExceptions calls = %d; want 1", n)
	}
}

// Denied tag reads across several phases send one warning per phase, never
// one per row.
func TestScanConfig_TagDenialsWarnOncePerPhase(t *testing.T) {
	st := newTestStore(t)
	warnings := bacWarnings(st, cfgTagsOp)
	calls := newCfgCalls()
	k := cfgKindOf(t, "DescribeConformancePacks")
	calls.errs[cfgKindARN(k, "tagged")] = configDenied
	calls.errs[cfgKindARN(k, "bare")] = configDenied

	if _, _, err := cfgScan(context.Background(), st, cfgHandlers(calls)); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if len(*warnings) != 1 {
		t.Errorf("%s warnings = %d; want 1", cfgTagsOp, len(*warnings))
	}
	if got := len(rowsByNativeID(t, st, k.typ)); got != 2 {
		t.Errorf("conformance packs = %d; want 2", got)
	}
}

// Tags are read over every page ListTagsForResource returns; a repeated
// token ends the read as an error, keeping the row without tags.
func TestScanConfig_TagPages(t *testing.T) {
	arn := cfgKindARN(cfgKindOf(t, "ListStoredQueries"), "tagged")
	pages := func(second *string) func(any) (any, error) {
		return func(in any) (any, error) {
			req := in.(*configservice.ListTagsForResourceInput)
			if sv(req.ResourceArn) != arn {
				return &configservice.ListTagsForResourceOutput{}, nil
			}
			if req.NextToken == nil {
				return &configservice.ListTagsForResourceOutput{Tags: []cfgtypes.Tag{{Key: sdkaws.String("a"), Value: sdkaws.String("1")}}, NextToken: sdkaws.String("p2")}, nil
			}
			return &configservice.ListTagsForResourceOutput{Tags: []cfgtypes.Tag{{Key: sdkaws.String("b"), Value: sdkaws.String("2")}}, NextToken: second}, nil
		}
	}
	t.Run("every page", func(t *testing.T) {
		st := newTestStore(t)
		h := cfgHandlers(newCfgCalls())
		h["ListTagsForResource"] = pages(nil)
		if _, _, err := cfgScan(context.Background(), st, h); err != nil {
			t.Fatalf("scan: %v", err)
		}
		r, _ := cfgRowOf(t, st, TypeConfigStoredQuery, arn)
		assertTagsMap(t, r.TagsJSON, map[string]string{"a": "1", "b": "2"})
	})
	t.Run("repeated token", func(t *testing.T) {
		st := newTestStore(t)
		h := cfgHandlers(newCfgCalls())
		h["ListTagsForResource"] = pages(sdkaws.String("p2"))
		_, _, err := cfgScan(context.Background(), st, h)
		if !errors.Is(err, errRepeatedNextToken) {
			t.Fatalf("err = %v; want errRepeatedNextToken", err)
		}
		r, ok := cfgRowOf(t, st, TypeConfigStoredQuery, arn)
		if !ok || r.TagsJSON != nil {
			t.Errorf("row stored = %v, TagsJSON = %v; want stored without tags", ok, r.TagsJSON)
		}
	})
}

// A recorder listed without an Arn is stored under the ARN built from its
// name, its tags are not read, and one warning under the tag op says so; a
// recorder with an Arn but no name is stored and tagged; one with neither is
// warned about under its listing, not stored.
func TestScanConfig_RecorderWithoutARN(t *testing.T) {
	st := newTestStore(t)
	all := countWarnings(st)
	tagWarns := bacWarnings(st, cfgTagsOp)
	listWarns := bacWarnings(st, "config:DescribeConfigurationRecorders")
	calls := newCfgCalls()
	h := cfgHandlers(calls)
	h["DescribeConfigurationRecorders"] = func(any) (any, error) {
		return &configservice.DescribeConfigurationRecordersOutput{ConfigurationRecorders: []cfgtypes.ConfigurationRecorder{
			{Name: sdkaws.String("default")}, {Arn: sdkaws.String(cfgRecorderARN("x"))}, {RoleARN: sdkaws.String("arn:aws:iam::123456789012:role/config")},
		}}, nil
	}

	total, _, err := cfgScan(context.Background(), st, h)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if want := cfgTotalCount; total != want {
		t.Errorf("total = %d; want %d", total, want)
	}
	synth := cfgARN("config-recorder/default")
	r, ok := cfgRowOf(t, st, TypeConfigRecorder, synth)
	if !ok || r.TagsJSON != nil || sv(r.Name) != "default" {
		t.Fatalf("recorder %s stored = %v, Name %q; want stored as default without tags", synth, ok, sv(r.Name))
	}
	if slices.Contains(calls.tagARNs, synth) {
		t.Errorf("tag read sent the built ARN %s", synth)
	}
	cfgAssertWarning(t, *tagWarns, cfgTagsOp, synth)
	if _, ok := cfgRowOf(t, st, TypeConfigRecorder, cfgRecorderARN("x")); !ok || !slices.Contains(calls.tagARNs, cfgRecorderARN("x")) {
		t.Errorf("nameless recorder stored = %v, tags read = %v; want both", ok, slices.Contains(calls.tagARNs, cfgRecorderARN("x")))
	}
	cfgAssertWarning(t, *listWarns, "config:DescribeConfigurationRecorders", "(a recorder with no identifier)")
	if *all != 2 {
		t.Errorf("warnings = %d; want 2", *all)
	}
}

// A delivery channel without a name is warned about under its listing,
// named by its bucket, and not stored.
func TestScanConfig_DeliveryChannelWithoutNameWarns(t *testing.T) {
	st := newTestStore(t)
	all := countWarnings(st)
	warns := bacWarnings(st, "config:DescribeDeliveryChannels")
	handlers := cfgHandlers(newCfgCalls())
	handlers["DescribeDeliveryChannels"] = func(any) (any, error) {
		return &configservice.DescribeDeliveryChannelsOutput{DeliveryChannels: []cfgtypes.DeliveryChannel{{S3BucketName: sdkaws.String("nameless-bucket")}}}, nil
	}

	if _, _, err := cfgScan(context.Background(), st, handlers); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if got := len(rowsByNativeID(t, st, TypeConfigDeliveryChannel)); got != 0 {
		t.Errorf("delivery channels = %d; want 0", got)
	}
	if len(*warns) != 1 || *all != 1 || !strings.Contains((*warns)[0].Message, "nameless-bucket") {
		t.Errorf("warnings = %v (all ops %d); want 1 naming nameless-bucket", *warns, *all)
	}
}

// A denied listing warns once under its own op and stores nothing of its
// type; every other listing still runs. The organization listings treat
// OrganizationAccessDeniedException as a denial too.
func TestScanConfig_DeniedListingWarnsUnderItsOp(t *testing.T) {
	type deniedOp struct {
		op, typ string
		err     error
		lost    int
	}
	ops := []deniedOp{
		{"DescribeConfigurationRecorders", TypeConfigRecorder, configDenied, 2},
		{"DescribeDeliveryChannels", TypeConfigDeliveryChannel, configDenied, 1},
	}
	for _, k := range cfgKinds() {
		lost := 2
		if k.op == "DescribeConfigRules" {
			lost = 4 // and the two remediation configurations
		}
		ops = append(ops, deniedOp{k.op, k.typ, configDenied, lost})
	}
	for _, c := range ops {
		t.Run(c.op+"/"+c.err.(interface{ ErrorCode() string }).ErrorCode(), func(t *testing.T) {
			st := newTestStore(t)
			all := countWarnings(st)
			warnings := bacWarnings(st, "config:"+c.op)
			handlers := cfgHandlers(newCfgCalls())
			handlers[c.op] = func(any) (any, error) { return nil, c.err }

			total, _, err := cfgScan(context.Background(), st, handlers)
			if err != nil {
				t.Fatalf("err = %v; want nil", err)
			}
			if total != cfgTotalCount-c.lost {
				t.Errorf("total = %d; want %d", total, cfgTotalCount-c.lost)
			}
			if len(*warnings) != 1 || *all != 1 {
				t.Errorf("config:%s warnings = %v (all ops %d); want 1", c.op, *warnings, *all)
			}
			if got := len(rowsByNativeID(t, st, c.typ)); got != 0 {
				t.Errorf("%s rows = %d; want 0", c.typ, got)
			}
		})
	}
}

// cfgAssertWarning asserts that warns is exactly one warning under op, in
// the test account and region, naming key: an ARN, a fallback key, or the
// error code of a denial warning, which names no row.
func cfgAssertWarning(t *testing.T, warns []store.ScanWarning, op, key string) {
	t.Helper()
	if len(warns) != 1 {
		t.Fatalf("%s warnings = %v; want 1", op, warns)
	}
	w := warns[0]
	if w.Service != op || w.Scope != testAccountID+"/"+testRegion {
		t.Errorf("warning op, scope = %q, %q; want %q, %s/%s", w.Service, w.Scope, op, testAccountID, testRegion)
	}
	if !strings.Contains(w.Message, key) {
		t.Errorf("warning %q does not name %s", w.Message, key)
	}
}

// OrganizationAccessDeniedException on an organization listing's first page,
// the expected answer to a member or standalone account, ends it silently: no
// row, no warning, no error, and every other phase runs.
func TestScanConfig_OrgAccessDeniedFirstPageIsSilent(t *testing.T) {
	for _, op := range []string{"DescribeOrganizationConfigRules", "DescribeOrganizationConformancePacks"} {
		t.Run(op, func(t *testing.T) {
			st := newTestStore(t)
			all := countWarnings(st)
			calls := newCfgCalls()
			handlers := cfgHandlers(calls)
			k := cfgKindOf(t, op)
			handlers[op] = func(any) (any, error) { return nil, cfgOrgDenied }

			total, _, err := cfgScan(context.Background(), st, handlers)
			if err != nil {
				t.Fatalf("err = %v; want nil", err)
			}
			if total != cfgTotalCount-2 || len(rowsByNativeID(t, st, k.typ)) != 0 {
				t.Errorf("total = %d, %s rows = %d; want %d and 0", total, k.typ, len(rowsByNativeID(t, st, k.typ)), cfgTotalCount-2)
			}
			if *all != 0 {
				t.Errorf("warnings = %d; want 0", *all)
			}
			if calls.count("ListConnectors") == 0 {
				t.Error("last phase not run")
			}
		})
	}
}

// After an organization listing's first page: an IAM denial keeps that page
// and warns once under the op; OrganizationAccessDeniedException keeps it
// and is returned as an error, since the listing had been served.
func TestScanConfig_OrgDenialAfterFirstPageKeepsIt(t *testing.T) {
	k := cfgKindOf(t, "DescribeOrganizationConfigRules")
	first := cfgKindARN(k, "tagged")
	t.Run("IAM denial", func(t *testing.T) {
		st := newTestStore(t)
		warnings := bacWarnings(st, "config:"+k.op)
		handlers := cfgHandlers(newCfgCalls())
		handlers[k.op] = failOnCall(2, configDenied, handlers[k.op])

		if _, _, err := cfgScan(context.Background(), st, handlers); err != nil {
			t.Fatalf("err = %v; want nil", err)
		}
		if _, ok := cfgRowOf(t, st, k.typ, first); !ok {
			t.Error("organization rule from the first page missing")
		}
		cfgAssertWarning(t, *warnings, "config:"+k.op, "AccessDeniedException")
	})
	t.Run("OrganizationAccessDeniedException", func(t *testing.T) {
		st := newTestStore(t)
		all := countWarnings(st)
		handlers := cfgHandlers(newCfgCalls())
		handlers[k.op] = failOnCall(2, cfgOrgDenied, handlers[k.op])

		_, _, err := cfgScan(context.Background(), st, handlers)
		assertGlueOutcome(t, err, outcomeError)
		assertAPICode(t, err, "OrganizationAccessDeniedException")
		if _, ok := cfgRowOf(t, st, k.typ, first); !ok {
			t.Error("organization rule from the first page missing")
		}
		if *all != 0 {
			t.Errorf("warnings = %d; want 0", *all)
		}
	})
}

// A listing's failed page keeps the pages read before it, and its error does
// not stop the listings after it.
func TestScanConfig_ListFailureKeepsPagesAndRunsEveryPhase(t *testing.T) {
	for _, op := range []string{"DescribeConfigRules", "DescribeAggregationAuthorizations", "ListConnectors"} {
		t.Run(op, func(t *testing.T) {
			st := newTestStore(t)
			calls := newCfgCalls()
			handlers := cfgHandlers(calls)
			k := cfgKindOf(t, op)
			handlers[k.op] = failOnCall(2, cfgValidation, handlers[k.op])

			_, _, err := cfgScan(context.Background(), st, handlers)
			assertGlueOutcome(t, err, outcomeError)
			assertAPICode(t, err, "ValidationException")
			if _, ok := cfgRowOf(t, st, k.typ, cfgKindARN(k, "tagged")); !ok {
				t.Errorf("%s row from the page before the failure missing", k.typ)
			}
			if n := calls.count("ListConnectors"); n == 0 {
				t.Error("last phase not run")
			}
			if op == "DescribeConfigRules" && !slices.Equal(calls.ruleNames, []string{"rule-tagged"}) {
				t.Errorf("remediation rule names = %v; want [rule-tagged]", calls.ruleNames)
			}
			if got := len(rowsByNativeID(t, st, TypeConfigRetentionConfiguration)); got != 1 {
				t.Errorf("retention configurations = %d; want 1", got)
			}
		})
	}
}

// A repeated token ends the listing as an error, keeping its pages.
func TestScanConfig_RepeatedTokenKeepsPages(t *testing.T) {
	st := newTestStore(t)
	handlers := cfgHandlers(newCfgCalls())
	k := cfgKindOf(t, "DescribeConfigurationAggregators")
	handlers[k.op] = func(in any) (any, error) {
		suffix := "tagged"
		if bacField[*string](in, "NextToken") != nil {
			suffix = "bare"
		}
		return k.page(cfgKindARN(k, suffix), suffix, sdkaws.String("same")), nil
	}

	total, _, err := cfgScan(context.Background(), st, handlers)
	if !errors.Is(err, errRepeatedNextToken) {
		t.Fatalf("err = %v; want errRepeatedNextToken", err)
	}
	if total != cfgTotalCount {
		t.Errorf("total = %d; want %d", total, cfgTotalCount)
	}
}

// An element without an ARN is warned about under its listing, named by its
// name, or by the fallback text when it has none, and is not stored.
func TestScanConfig_ElementWithoutARNWarns(t *testing.T) {
	for _, c := range []struct {
		name, key string
		page      any
	}{
		{"named", "query-x", &configservice.ListStoredQueriesOutput{StoredQueryMetadata: []cfgtypes.StoredQueryMetadata{{QueryName: sdkaws.String("query-x")}}}},
		{"no identifier", "(an element with no identifier)", &configservice.ListStoredQueriesOutput{StoredQueryMetadata: []cfgtypes.StoredQueryMetadata{{}}}},
	} {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			all := countWarnings(st)
			warns := bacWarnings(st, "config:ListStoredQueries")
			handlers := cfgHandlers(newCfgCalls())
			handlers["ListStoredQueries"] = func(any) (any, error) { return c.page, nil }

			total, _, err := cfgScan(context.Background(), st, handlers)
			if err != nil {
				t.Fatalf("scan: %v", err)
			}
			if total != cfgTotalCount-2 {
				t.Errorf("total = %d; want %d", total, cfgTotalCount-2)
			}
			if len(*warns) != 1 || *all != 1 {
				t.Fatalf("warnings = %v (all ops %d); want 1", *warns, *all)
			}
			if !strings.Contains((*warns)[0].Message, c.key) {
				t.Errorf("warning %q does not name %q", (*warns)[0].Message, c.key)
			}
		})
	}
}

// A failed GetConnector: not found drops the connector; a denial keeps its
// list element with its tags and warns once under config:GetConnector; a
// throttle keeps it and is transient; any other error keeps it and is
// returned.
func TestScanConfig_GetConnectorFailures(t *testing.T) {
	k := cfgKindOf(t, "ListConnectors")
	arn := cfgKindARN(k, "tagged")
	for _, c := range []struct {
		name      string
		err       error
		outcome   serviceOutcome
		wantKept  bool
		wantWarns int
	}{
		{"not found", configNotFound, -1, false, 0},
		{"denied", configDenied, -1, true, 1},
		{"transient", cfgThrottle, outcomeTransient, true, 0},
		{"other error", cfgValidation, outcomeError, true, 0},
	} {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			all := countWarnings(st)
			warns := bacWarnings(st, "config:GetConnector")
			calls := newCfgCalls()
			calls.getErrs[arn] = c.err

			_, _, err := cfgScan(context.Background(), st, cfgHandlers(calls))
			if c.outcome < 0 {
				if err != nil {
					t.Fatalf("err = %v; want nil", err)
				}
			} else {
				assertGlueOutcome(t, err, c.outcome)
			}
			r, ok := cfgRowOf(t, st, k.typ, arn)
			if ok != c.wantKept {
				t.Fatalf("connector stored = %v; want %v", ok, c.wantKept)
			}
			if ok {
				var summary cfgtypes.ConnectorSummary
				if err := json.Unmarshal([]byte(r.AttributesJSON), &summary); err != nil || summary.Provider != cfgtypes.ProviderAzure {
					t.Errorf("attrs = %s (%v); want the ListConnectors element", r.AttributesJSON, err)
				}
				assertTagsMap(t, r.TagsJSON, map[string]string{"type": k.typ})
				if sv(r.Name) != "conn-tagged" || sv(r.CreatedAt) != "2026-01-02T03:04:05Z" {
					t.Errorf("Name, CreatedAt = %q, %q; want conn-tagged, 2026-01-02T03:04:05Z", sv(r.Name), sv(r.CreatedAt))
				}
			}
			if len(*warns) != c.wantWarns || *all != c.wantWarns {
				t.Errorf("config:GetConnector warnings = %v (all ops %d); want %d", *warns, *all, c.wantWarns)
			}
		})
	}
}

// A throttled tag read beside a hard failure of the same op is demoted to
// one warning, so the hard error decides the outcome.
func TestScanConfig_TagThrottleDemotedBesideHardError(t *testing.T) {
	st := newTestStore(t)
	warns := countOpWarnings(st, cfgTagsOp)
	calls := newCfgCalls()
	calls.errs[cfgKindARN(cfgKindOf(t, "DescribeConfigRules"), "tagged")] = cfgThrottle
	calls.errs[cfgKindARN(cfgKindOf(t, "ListStoredQueries"), "bare")] = cfgValidation

	_, _, err := cfgScan(context.Background(), st, cfgHandlers(calls))
	assertGlueOutcome(t, err, outcomeError)
	assertAPICode(t, err, "ValidationException")
	if *warns != 1 {
		t.Errorf("%s warnings = %d; want 1", cfgTagsOp, *warns)
	}
}

// A failed write stops the scan before any later listing and sends no
// warning about the rows it lost.
func TestScanConfig_FailedWriteStopsWithoutWarning(t *testing.T) {
	st := newTestStore(t)
	warns := countWarnings(st)
	refuseStoreWrites(t, st, TypeConfigRecorder)
	calls := newCfgCalls()
	calls.errs[cfgRecorderARN("tagged")] = configDenied

	_, _, err := cfgScan(context.Background(), st, cfgHandlers(calls))
	if !errors.Is(err, store.ErrStoreWrite) {
		t.Fatalf("err = %v; want store.ErrStoreWrite", err)
	}
	if *warns != 0 {
		t.Errorf("warnings = %d; want 0", *warns)
	}
	if n := calls.count("DescribeDeliveryChannels"); n != 0 {
		t.Errorf("DescribeDeliveryChannels called %d times after the failed write", n)
	}
}

// Once ctx ends no further phase starts, and the stop is one member of the
// error, although the phase in flight saw it too.
func TestScanConfig_ContextEndStopsOnce(t *testing.T) {
	st := newTestStore(t)
	calls := newCfgCalls()
	handlers := cfgHandlers(calls)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	k := cfgKindOf(t, "DescribeConformancePacks")
	list := handlers[k.op]
	handlers[k.op] = func(in any) (any, error) {
		if bacField[*string](in, "NextToken") != nil {
			cancel()
			return nil, context.Canceled
		}
		return list(in)
	}

	_, _, err := cfgScan(ctx, st, handlers)
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
	for _, op := range []string{"DescribeOrganizationConfigRules", "ListConnectors"} {
		if n := calls.count(op); n != 0 {
			t.Errorf("%s called %d times after ctx ended", op, n)
		}
	}
}

// An NXDOMAIN after rows were stored is a failure of this scan, not a region
// gap: the counts are kept. With nothing stored it stays region-unavailable.
func TestScanConfig_NXDOMAIN(t *testing.T) {
	nx := &net.DNSError{Err: "no such host", Name: "config.us-east-1.amazonaws.com", IsNotFound: true}
	t.Run("later phase after rows were stored", func(t *testing.T) {
		st := newTestStore(t)
		handlers := cfgHandlers(newCfgCalls())
		handlers["ListStoredQueries"] = func(any) (any, error) { return nil, nx }
		total, _, err := cfgScan(context.Background(), st, handlers)
		assertGlueOutcome(t, err, outcomeError)
		if total != cfgTotalCount-2 {
			t.Errorf("total = %d; want the %d other rows counted", total, cfgTotalCount-2)
		}
	})
	t.Run("tag read after the listing", func(t *testing.T) {
		st := newTestStore(t)
		calls := newCfgCalls()
		calls.errs[cfgRecorderARN("bare")] = nx
		_, _, err := cfgScan(context.Background(), st, cfgHandlers(calls))
		assertGlueOutcome(t, err, outcomeError)
	})
	t.Run("nothing stored", func(t *testing.T) {
		st := newTestStore(t)
		warns := countWarnings(st)
		acct := newTestAccount(testAccountID)
		acct.cfg = opStubConfig(nil, nx)
		_, _, err := scanConfig(context.Background(), acct, testRegion, st, testScanID)
		assertGlueOutcome(t, err, outcomeUnavailable)
		if *warns != 0 {
			t.Errorf("warnings = %d; want 0", *warns)
		}
	})
}

// cfgServiceLinked is handlers with the "tagged" config rule and conformance
// pack listed as service-linked (CreatedBy set).
func cfgServiceLinked(c *cfgCalls) map[string]func(any) (any, error) {
	h := cfgHandlers(c)
	rules, packs := h["DescribeConfigRules"], h["DescribeConformancePacks"]
	h["DescribeConfigRules"] = func(in any) (any, error) {
		out, err := rules(in)
		if r := out.(*configservice.DescribeConfigRulesOutput); r.NextToken != nil {
			r.ConfigRules[0].CreatedBy = sdkaws.String("securityhub.amazonaws.com")
		}
		return out, err
	}
	h["DescribeConformancePacks"] = func(in any) (any, error) {
		out, err := packs(in)
		if p := out.(*configservice.DescribeConformancePacksOutput); p.NextToken != nil {
			p.ConformancePackDetails[0].CreatedBy = sdkaws.String("fms.amazonaws.com")
		}
		return out, err
	}
	return h
}

// A service-linked rule or conformance pack (CreatedBy set) whose tag read is
// refused with ValidationException is kept without tags and warned about once
// under the tag op, with no error; the same code on a rule or pack the
// account created is a hard error.
func TestScanConfig_ServiceLinkedTagRefusal(t *testing.T) {
	for _, op := range []string{"DescribeConfigRules", "DescribeConformancePacks"} {
		k := cfgKindOf(t, op)
		t.Run(op+"/service-linked", func(t *testing.T) {
			st := newTestStore(t)
			all := countWarnings(st)
			warns := bacWarnings(st, cfgTagsOp)
			calls := newCfgCalls()
			linked := cfgKindARN(k, "tagged")
			calls.errs[linked] = cfgValidation

			total, _, err := cfgScan(context.Background(), st, cfgServiceLinked(calls))
			if err != nil {
				t.Fatalf("err = %v; want nil", err)
			}
			if total != cfgTotalCount {
				t.Errorf("total = %d; want %d", total, cfgTotalCount)
			}
			r, ok := cfgRowOf(t, st, k.typ, linked)
			if !ok || r.TagsJSON != nil {
				t.Errorf("row stored = %v, TagsJSON = %v; want stored without tags", ok, r.TagsJSON)
			}
			cfgAssertWarning(t, *warns, cfgTagsOp, linked)
			if *all != 1 {
				t.Errorf("warnings = %d; want 1", *all)
			}
		})
		t.Run(op+"/own", func(t *testing.T) {
			st := newTestStore(t)
			warns := countWarnings(st)
			calls := newCfgCalls()
			own := cfgKindARN(k, "bare")
			calls.errs[own] = cfgValidation

			_, _, err := cfgScan(context.Background(), st, cfgServiceLinked(calls))
			assertGlueOutcome(t, err, outcomeError)
			assertAPICode(t, err, "ValidationException")
			if _, ok := cfgRowOf(t, st, k.typ, own); !ok {
				t.Error("row not stored")
			}
			if *warns != 0 {
				t.Errorf("warnings = %d; want 0", *warns)
			}
		})
	}
}

// A failed write of the rules or packs sends no warning about a
// service-linked row whose tag read was refused.
func TestScanConfig_ServiceLinkedRefusalNoWarningAfterFailedWrite(t *testing.T) {
	for _, op := range []string{"DescribeConfigRules", "DescribeConformancePacks"} {
		t.Run(op, func(t *testing.T) {
			k := cfgKindOf(t, op)
			st := newTestStore(t)
			warns := countWarnings(st)
			refuseStoreWrites(t, st, k.typ)
			calls := newCfgCalls()
			calls.errs[cfgKindARN(k, "tagged")] = cfgValidation

			_, _, err := cfgScan(context.Background(), st, cfgServiceLinked(calls))
			if !errors.Is(err, store.ErrStoreWrite) {
				t.Fatalf("err = %v; want store.ErrStoreWrite", err)
			}
			if *warns != 0 {
				t.Errorf("warnings = %d; want 0", *warns)
			}
		})
	}
}

// A GovCloud account's rows reach the tag op under the ARNs their listings
// return, and a recorder listed without an Arn is stored under an ARN built in
// the account's partition, its tags not read.
func TestScanConfig_TagReadInAccountPartition(t *testing.T) {
	acct := partitionAccount("aws-us-gov")
	region := acct.Regions[0]
	st := newTestStore(t)
	calls := newCfgCalls()
	prefix := "arn:aws-us-gov:config:" + region + ":" + testAccountID + ":"
	recorder, rule := prefix+"configuration-recorder/gov/id-1", prefix+"config-rule/config-rule-gov"
	calls.tags[recorder] = map[string]string{"k": "recorder"}
	calls.tags[rule] = map[string]string{"k": "rule"}
	acct.cfg = opStubConfig(map[string]func(any) (any, error){
		"ListTagsForResource": calls.listTags,
		"DescribeConfigurationRecorders": func(any) (any, error) {
			return &configservice.DescribeConfigurationRecordersOutput{ConfigurationRecorders: []cfgtypes.ConfigurationRecorder{
				{Arn: &recorder, Name: sdkaws.String("gov")}, {Name: sdkaws.String("default")},
			}}, nil
		},
		"DescribeConfigRules": func(any) (any, error) {
			return &configservice.DescribeConfigRulesOutput{ConfigRules: []cfgtypes.ConfigRule{{ConfigRuleArn: &rule, ConfigRuleName: sdkaws.String("gov")}}}, nil
		},
	}, configDenied)

	if _, _, err := scanConfig(context.Background(), acct, region, st, testScanID); err != nil {
		t.Fatalf("scan: %v", err)
	}
	slices.Sort(calls.tagARNs)
	if want := []string{rule, recorder}; !slices.Equal(calls.tagARNs, want) {
		t.Errorf("ListTagsForResource ARNs = %v; want %v", calls.tagARNs, want)
	}
	for _, w := range []struct{ typ, arn, tag string }{{TypeConfigRecorder, recorder, "recorder"}, {TypeConfigRule, rule, "rule"}} {
		r, ok := cfgRowOf(t, st, w.typ, w.arn)
		if !ok {
			t.Fatalf("%s %s missing", w.typ, w.arn)
		}
		assertTagsMap(t, r.TagsJSON, map[string]string{"k": w.tag})
	}
	if _, ok := cfgRowOf(t, st, TypeConfigRecorder, prefix+"config-recorder/default"); !ok {
		t.Errorf("recorder without an Arn not stored under %sconfig-recorder/default", prefix)
	}
}

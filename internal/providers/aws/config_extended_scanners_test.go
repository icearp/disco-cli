package aws

import (
	"context"
	"encoding/json"
	"slices"
	"strconv"
	"testing"
	"time"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/configservice"
	cfgtypes "github.com/aws/aws-sdk-go-v2/service/configservice/types"
	"github.com/aws/smithy-go"
	"github.com/icearp/disco-cli/store"
)

const configTestRulePrefix = "arn:aws:config:us-east-1:123456789012:config-rule/"

var (
	configDenied   = &smithy.GenericAPIError{Code: "AccessDeniedException", Message: "User: arn:aws:iam::123456789012:user/t is not authorized to perform: config:Describe"}
	configNotFound = &smithy.GenericAPIError{Code: "ResourceNotFoundException", Message: "gone"}
	configBadParam = &smithy.GenericAPIError{Code: "InvalidParameterValueException", Message: "bad"}
)

// configStubConfig answers each Config call from handlers keyed by operation
// name; an op without a handler is denied (see opStubConfig).
func configStubConfig(handlers map[string]func(in any) (any, error)) sdkaws.Config {
	return opStubConfig(handlers, configDenied)
}

func configStubClient(handlers map[string]func(in any) (any, error)) *configservice.Client {
	return configservice.NewFromConfig(configStubConfig(handlers))
}

func configRetentionHandler(in any) (any, error) {
	if in.(*configservice.DescribeRetentionConfigurationsInput).NextToken == nil {
		return &configservice.DescribeRetentionConfigurationsOutput{NextToken: ptrStr("p2"), RetentionConfigurations: []cfgtypes.RetentionConfiguration{
			{Name: ptrStr("default"), RetentionPeriodInDays: sdkaws.Int32(400)},
		}}, nil
	}
	return &configservice.DescribeRetentionConfigurationsOutput{RetentionConfigurations: []cfgtypes.RetentionConfiguration{
		{Name: ptrStr("second"), RetentionPeriodInDays: sdkaws.Int32(30)}, {RetentionPeriodInDays: sdkaws.Int32(1)},
	}}, nil
}

func TestScanConfigRetentionConfigurations_StoresEveryPage(t *testing.T) {
	st := newTestStore(t)
	client := configStubClient(map[string]func(any) (any, error){"DescribeRetentionConfigurations": configRetentionHandler})
	total, _, err := scanConfigRetentionConfigurations(context.Background(), client, newTestAccount(testAccountID), testRegion, st, testScanID)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	rows := rowsByNativeID(t, st, TypeConfigRetentionConfiguration)
	if total != 2 || len(rows) != 2 {
		t.Fatalf("total = %d, rows = %d, want 2 and 2", total, len(rows))
	}
	r, ok := rows["arn:aws:config:us-east-1:123456789012:retention-configuration/default"]
	if !ok {
		t.Fatalf("no row for default; got %v", rows)
	}
	if sv(r.Name) != "default" || sv(r.Region) != testRegion || r.Type != TypeConfigRetentionConfiguration {
		t.Errorf("row = name %q region %q type %q", sv(r.Name), sv(r.Region), r.Type)
	}
	if r.ManagedByProvider {
		t.Error("retention configuration stored as provider-managed; it is user-created")
	}
	var attrs cfgtypes.RetentionConfiguration
	if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil || sdkaws.ToInt32(attrs.RetentionPeriodInDays) != 400 {
		t.Errorf("attrs = %s (%v), want RetentionPeriodInDays 400", r.AttributesJSON, err)
	}
}

// singleListCase drives a one-op list phase through an empty result, a denial
// and an unmodelled error.
type singleListCase struct {
	name string
	op   string
	typ  string
	run  func(context.Context, configserviceAPI, *account, *store.Store) (int, int, error)
}

func configSingleListCases() []singleListCase {
	return []singleListCase{
		{
			name: "retention configurations", op: "DescribeRetentionConfigurations", typ: TypeConfigRetentionConfiguration,
			run: func(ctx context.Context, c configserviceAPI, a *account, st *store.Store) (int, int, error) {
				return scanConfigRetentionConfigurations(ctx, c, a, testRegion, st, testScanID)
			},
		},
	}
}

func TestConfigListPhases_Errors(t *testing.T) {
	empty := map[string]any{
		"DescribeRetentionConfigurations": &configservice.DescribeRetentionConfigurationsOutput{},
	}
	for _, c := range configSingleListCases() {
		t.Run(c.name+"/empty", func(t *testing.T) {
			st := newTestStore(t)
			client := configStubClient(map[string]func(any) (any, error){c.op: func(any) (any, error) { return empty[c.op], nil }})
			total, _, err := c.run(context.Background(), client, newTestAccount(testAccountID), st)
			if err != nil || total != 0 || len(rowsByNativeID(t, st, c.typ)) != 0 {
				t.Fatalf("total = %d, err = %v, want 0 rows and nil", total, err)
			}
		})
		t.Run(c.name+"/denied warns", func(t *testing.T) {
			st := newTestStore(t)
			warns := countOpWarnings(st, "config:"+c.op)
			total, _, err := c.run(context.Background(), configStubClient(nil), newTestAccount(testAccountID), st)
			if err != nil || total != 0 {
				t.Fatalf("total = %d, err = %v, want 0 and nil", total, err)
			}
			if *warns != 1 {
				t.Errorf("warnings for config:%s = %d, want 1", c.op, *warns)
			}
		})
		t.Run(c.name+"/other error propagates", func(t *testing.T) {
			st := newTestStore(t)
			client := configStubClient(map[string]func(any) (any, error){c.op: func(any) (any, error) { return nil, configBadParam }})
			if _, _, err := c.run(context.Background(), client, newTestAccount(testAccountID), st); !isAPIErrorCode(err, "InvalidParameterValueException") {
				t.Fatalf("err = %v, want InvalidParameterValueException", err)
			}
		})
	}
}

var configConnectorCreated = time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)

func configConnectorARN(id string) string {
	return "arn:aws:config:us-east-1:123456789012:connector/azure/tenant-1/" + id
}

// configConnectorHandlers lists connectors a, b, gone (plus one without an ARN)
// over two pages; get answers GetConnector.
func configConnectorHandlers(get func(arn string) (any, error)) map[string]func(any) (any, error) {
	return map[string]func(any) (any, error){
		"ListConnectors": func(in any) (any, error) {
			if in.(*configservice.ListConnectorsInput).NextToken == nil {
				return &configservice.ListConnectorsOutput{NextToken: ptrStr("p2"), ConnectorSummaries: []cfgtypes.ConnectorSummary{{Arn: ptrStr(configConnectorARN("a"))}}}, nil
			}
			return &configservice.ListConnectorsOutput{ConnectorSummaries: []cfgtypes.ConnectorSummary{
				{Arn: ptrStr(configConnectorARN("b"))}, {Arn: ptrStr(configConnectorARN("gone"))}, {Name: ptrStr("no arn")},
			}}, nil
		},
		"GetConnector": func(in any) (any, error) { return get(sv(in.(*configservice.GetConnectorInput).Arn)) },
	}
}

func configConnectorBody(arn string) (any, error) {
	return &configservice.GetConnectorOutput{Connector: &cfgtypes.Connector{
		Arn: ptrStr(arn), Name: ptrStr("conn-" + arn[len(arn)-1:]), CreatedTime: &configConnectorCreated,
		ConnectorConfiguration: &cfgtypes.ConnectorConfiguration{Azure: &cfgtypes.AzureConnectorConfiguration{ClientIdentifier: ptrStr("client-1"), TenantIdentifier: ptrStr("tenant-1")}},
	}}, nil
}

// 30 rules must reach DescribeRemediationConfigurations in batches of at
// most 25 names, every rule once, and each returned configuration must be
// stored.
func TestScanConfigRemediationConfigs_BatchesEveryListedRule(t *testing.T) {
	st := newTestStore(t)
	var (
		names []string
		rules []childParent
	)
	for i := range 30 {
		n := "rule-" + strconv.Itoa(i)
		names = append(names, n)
		rules = append(rules, childParent{id: n, arn: configTestRulePrefix + n})
	}
	var batches [][]string
	client := configStubClient(map[string]func(any) (any, error){
		"DescribeRemediationConfigurations": func(in any) (any, error) {
			batch := in.(*configservice.DescribeRemediationConfigurationsInput).ConfigRuleNames
			batches = append(batches, batch)
			out := &configservice.DescribeRemediationConfigurationsOutput{}
			for _, n := range batch {
				out.RemediationConfigurations = append(out.RemediationConfigurations, cfgtypes.RemediationConfiguration{
					ConfigRuleName: ptrStr(n), Arn: ptrStr("arn:aws:config:us-east-1:123456789012:remediation-configuration/" + n + "/id"),
				})
			}
			return out, nil
		},
	})
	acct := newTestAccount(testAccountID)
	total, _, err := scanConfigRemediationConfigs(context.Background(), client, rules, acct, testRegion, st, testScanID)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	var sent []string
	for _, b := range batches {
		if len(b) == 0 || len(b) > 25 {
			t.Errorf("batch of %d names, want 1..25", len(b))
		}
		sent = append(sent, b...)
	}
	if len(batches) != 2 || !slices.Equal(sent, names) {
		t.Errorf("batches = %d sending %v; want 2 sending %v", len(batches), sent, names)
	}
	rows := rowsByNativeID(t, st, TypeConfigRemediationConfiguration)
	if total != 30 || len(rows) != 30 {
		t.Fatalf("total = %d, rows = %d, want 30 and 30", total, len(rows))
	}
	r, ok := rows["arn:aws:config:us-east-1:123456789012:remediation-configuration/rule-29/id"]
	if !ok || sv(r.Name) != "rule-29" || sv(r.Region) != testRegion {
		t.Errorf("rule-29 row = %+v (found %v), want name rule-29 in %s", r, ok, testRegion)
	}
}

// configRules returns rule parents and seeds their rows, as the rule phase
// stores them before the exception phase records pairs under them.
func configRules(t *testing.T, st *store.Store, names ...string) []childParent {
	t.Helper()
	out := make([]childParent, len(names))
	for i, n := range names {
		out[i] = childParent{id: n, arn: configTestRulePrefix + n}
		upsertTestResource(t, st, "aws", testAccountID, TypeConfigRule, out[i].arn, testRegion, "{}")
	}
	return out
}

func configExceptionID(rule, resourceID string) string {
	return configTestRulePrefix + rule + "/remediation-exception/AWS::EC2::SecurityGroup/" + resourceID
}

// configExceptionHandler answers two pages per rule, plus one element with no
// resource id; calls records the rules queried.
func configExceptionHandler(calls *parentCalls, fail map[string]error) func(any) (any, error) {
	return func(in any) (any, error) {
		p := in.(*configservice.DescribeRemediationExceptionsInput)
		rule := sv(p.ConfigRuleName)
		calls.add(rule)
		if err := fail[rule]; err != nil {
			return nil, err
		}
		sg := ptrStr("AWS::EC2::SecurityGroup")
		if p.NextToken == nil {
			return &configservice.DescribeRemediationExceptionsOutput{NextToken: ptrStr("p2"), RemediationExceptions: []cfgtypes.RemediationException{
				{ConfigRuleName: ptrStr(rule), ResourceType: sg, ResourceId: ptrStr("sg-" + rule + "-1"), Message: ptrStr("hold")},
			}}, nil
		}
		return &configservice.DescribeRemediationExceptionsOutput{RemediationExceptions: []cfgtypes.RemediationException{
			{ConfigRuleName: ptrStr(rule), ResourceType: sg, ResourceId: ptrStr("sg-" + rule + "-2")}, {ConfigRuleName: ptrStr(rule), ResourceType: sg},
		}}, nil
	}
}

func TestScanConfigRemediationExceptions_PagesEveryRule(t *testing.T) {
	st := newTestStore(t)
	rules := configRules(t, st, "r1", "r2")
	calls := &parentCalls{}
	client := configStubClient(map[string]func(any) (any, error){"DescribeRemediationExceptions": configExceptionHandler(calls, nil)})
	total, _, err := scanConfigRemediationExceptions(context.Background(), client, rules, newTestAccount(testAccountID), testRegion, st, testScanID)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if got := calls.distinct(); !slices.Equal(got, []string{"r1", "r2"}) {
		t.Errorf("rules queried = %v, want [r1 r2]", got)
	}
	rows := rowsByNativeID(t, st, TypeConfigRemediationException)
	if total != 4 || len(rows) != 4 {
		t.Fatalf("total = %d, rows = %d, want 4 and 4", total, len(rows))
	}
	for _, rule := range []string{"r1", "r2"} {
		for _, n := range []string{"1", "2"} {
			id := configExceptionID(rule, "sg-"+rule+"-"+n)
			r, ok := rows[id]
			if !ok {
				t.Errorf("no row %s", id)
				continue
			}
			if sv(r.Name) != "sg-"+rule+"-"+n || sv(r.Region) != testRegion || r.Type != TypeConfigRemediationException {
				t.Errorf("%s: name %q region %q type %q", id, sv(r.Name), sv(r.Region), r.Type)
			}
			assertContainsNative(t, st, configTestRulePrefix+rule, id)
		}
	}
	var attrs cfgtypes.RemediationException
	if err := json.Unmarshal([]byte(rows[configExceptionID("r1", "sg-r1-1")].AttributesJSON), &attrs); err != nil || sv(attrs.Message) != "hold" {
		t.Errorf("attrs Message = %q (%v), want hold", sv(attrs.Message), err)
	}
}

func TestScanConfigRemediationExceptions_Errors(t *testing.T) {
	t.Run("no rules", func(t *testing.T) {
		st := newTestStore(t)
		total, _, err := scanConfigRemediationExceptions(context.Background(), configStubClient(nil), nil, newTestAccount(testAccountID), testRegion, st, testScanID)
		if err != nil || total != 0 {
			t.Fatalf("total = %d, err = %v, want 0 and nil", total, err)
		}
	})
	t.Run("denied warns once and keeps siblings", func(t *testing.T) {
		st := newTestStore(t)
		rules := configRules(t, st, "r1", "r2", "r3")
		warns := countOpWarnings(st, "config:DescribeRemediationExceptions")
		calls := &parentCalls{}
		client := configStubClient(map[string]func(any) (any, error){
			"DescribeRemediationExceptions": configExceptionHandler(calls, map[string]error{"r1": configDenied, "r2": configDenied}),
		})
		total, _, err := scanConfigRemediationExceptions(context.Background(), client, rules, newTestAccount(testAccountID), testRegion, st, testScanID)
		if err != nil || total != 2 {
			t.Fatalf("total = %d, err = %v, want 2 (r3's) and nil", total, err)
		}
		if *warns != 1 {
			t.Errorf("warnings = %d, want 1 for two denied rules", *warns)
		}
	})
	t.Run("not-found is not skipped", func(t *testing.T) {
		st := newTestStore(t)
		rules := configRules(t, st, "r1")
		client := configStubClient(map[string]func(any) (any, error){
			"DescribeRemediationExceptions": configExceptionHandler(&parentCalls{}, map[string]error{"r1": configNotFound}),
		})
		if _, _, err := scanConfigRemediationExceptions(context.Background(), client, rules, newTestAccount(testAccountID), testRegion, st, testScanID); !isAPIErrorCode(err, "ResourceNotFoundException") {
			t.Fatalf("err = %v, want ResourceNotFoundException", err)
		}
	})
}

// configServiceHandlers answers the whole Config scan: rules r1 and r2 (stored
// by the rule phase), one stored query (the last pre-existing phase) and the new
// phases' rows. Ops without a handler (aggregators, packs, ...) are denied.
func configServiceHandlers() map[string]func(any) (any, error) {
	h := configConnectorHandlers(func(arn string) (any, error) {
		if arn == configConnectorARN("gone") {
			return nil, configNotFound
		}
		return configConnectorBody(arn)
	})
	h["DescribeConfigurationRecorders"] = func(any) (any, error) {
		return &configservice.DescribeConfigurationRecordersOutput{}, nil
	}
	h["DescribeDeliveryChannels"] = func(any) (any, error) {
		return &configservice.DescribeDeliveryChannelsOutput{}, nil
	}
	h["DescribeConfigRules"] = func(any) (any, error) {
		return &configservice.DescribeConfigRulesOutput{ConfigRules: []cfgtypes.ConfigRule{
			{ConfigRuleName: ptrStr("r1"), ConfigRuleArn: ptrStr(configTestRulePrefix + "r1")},
			{ConfigRuleName: ptrStr("r2"), ConfigRuleArn: ptrStr(configTestRulePrefix + "r2")},
		}}, nil
	}
	h["DescribeRemediationConfigurations"] = func(any) (any, error) {
		return &configservice.DescribeRemediationConfigurationsOutput{}, nil
	}
	h["ListStoredQueries"] = func(any) (any, error) {
		return &configservice.ListStoredQueriesOutput{StoredQueryMetadata: []cfgtypes.StoredQueryMetadata{
			{QueryArn: ptrStr(configStoredQueryARN), QueryId: ptrStr("q-1"), QueryName: ptrStr("q")},
		}}, nil
	}
	h["DescribeRetentionConfigurations"] = configRetentionHandler
	h["DescribeRemediationExceptions"] = configExceptionHandler(&parentCalls{}, nil)
	return h
}

const configStoredQueryARN = "arn:aws:config:us-east-1:123456789012:stored-query/q/q-1"

func TestScanConfig_NewPhasesStoreRows(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	acct.cfg = configStubConfig(configServiceHandlers())
	if _, _, err := scanConfig(context.Background(), acct, testRegion, st, testScanID); err != nil {
		t.Fatalf("scanConfig: %v", err)
	}
	for typ, id := range map[string]string{
		TypeConfigRetentionConfiguration: "arn:aws:config:us-east-1:123456789012:retention-configuration/default",
		TypeConfigConnector:              configConnectorARN("b"),
		TypeConfigRemediationException:   configExceptionID("r2", "sg-r2-2"),
	} {
		if _, ok := rowsByNativeID(t, st, typ)[id]; !ok {
			t.Errorf("%s: no row %s", typ, id)
		}
	}
	assertContainsNative(t, st, configTestRulePrefix+"r1", configExceptionID("r1", "sg-r1-1"))
}

// A failing new phase must not cost the rows of the phases that ran before it.
func TestScanConfig_NewPhaseErrorAfterExistingPhases(t *testing.T) {
	for _, failing := range []string{"DescribeRetentionConfigurations", "ListConnectors", "GetConnector", "DescribeRemediationExceptions"} {
		t.Run(failing, func(t *testing.T) {
			st := newTestStore(t)
			h := configServiceHandlers()
			h[failing] = func(any) (any, error) { return nil, configBadParam }
			acct := newTestAccount(testAccountID)
			acct.cfg = configStubConfig(h)
			if _, _, err := scanConfig(context.Background(), acct, testRegion, st, testScanID); !isAPIErrorCode(err, "InvalidParameterValueException") {
				t.Fatalf("err = %v, want InvalidParameterValueException", err)
			}
			if _, ok := rowsByNativeID(t, st, TypeConfigStoredQuery)[configStoredQueryARN]; !ok {
				t.Error("stored-query row from the last existing phase lost")
			}
		})
	}
}

// Connectors run last on an unverified assumption: as the newest feature they
// may fail with an unmodelled error in some regions, and last is where an
// unknown gap costs the fewest rows. This pins that order.
func TestScanConfig_ConnectorErrorKeepsOtherNewPhases(t *testing.T) {
	st := newTestStore(t)
	h := configServiceHandlers()
	h["ListConnectors"] = func(any) (any, error) { return nil, configBadParam }
	acct := newTestAccount(testAccountID)
	acct.cfg = configStubConfig(h)
	if _, _, err := scanConfig(context.Background(), acct, testRegion, st, testScanID); !isAPIErrorCode(err, "InvalidParameterValueException") {
		t.Fatalf("err = %v, want InvalidParameterValueException", err)
	}
	for typ, id := range map[string]string{
		TypeConfigRetentionConfiguration: "arn:aws:config:us-east-1:123456789012:retention-configuration/default",
		TypeConfigRemediationException:   configExceptionID("r1", "sg-r1-1"),
	} {
		if _, ok := rowsByNativeID(t, st, typ)[id]; !ok {
			t.Errorf("%s: row %s lost", typ, id)
		}
	}
}

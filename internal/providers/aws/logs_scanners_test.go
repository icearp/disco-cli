package aws

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"net"
	"slices"
	"sync"
	"testing"
	"time"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	cwlogs "github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs"
	logsTypes "github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs/types"
	"github.com/aws/aws-sdk-go-v2/service/observabilityadmin"
	obstypes "github.com/aws/aws-sdk-go-v2/service/observabilityadmin/types"
	"github.com/aws/smithy-go"
	"github.com/icearp/disco-cli/store"
)

var (
	logsDenied  = &smithy.GenericAPIError{Code: "AccessDeniedException", Message: "User: arn:aws:iam::123456789012:user/t is not authorized to perform: logs:List"}
	logsGone    = &smithy.GenericAPIError{Code: "ResourceNotFoundException", Message: "gone"}
	logsBadArg  = &smithy.GenericAPIError{Code: "InvalidParameterException", Message: "bad"}
	logsNoOp    = &smithy.GenericAPIError{Code: "UnknownOperationException", Message: "unknown op"}
	logsCreated = time.Date(2026, 9, 1, 8, 0, 0, 0, time.UTC)
)

func logsTestClient(handlers map[string]func(in any) (any, error)) *cwlogs.Client {
	return cwlogs.NewFromConfig(opStubConfig(handlers, logsDenied))
}

func obsTestClient(handlers map[string]func(in any) (any, error)) *observabilityadmin.Client {
	return observabilityadmin.NewFromConfig(opStubConfig(handlers, logsDenied))
}

func lgTestARN(name string) string { return logGroupNativeIDFromName(testAccountID, testRegion, name) }

func s3tIntegrationARN(id string) string {
	return "arn:aws:observabilityadmin:" + testRegion + ":" + testAccountID + ":s3tableintegration/" + id
}

// seedLogGroups stores one log group row per name, as phase 1 would, and
// returns them as phase 2 receives them.
func seedLogGroups(t *testing.T, st *store.Store, names ...string) []store.Resource {
	t.Helper()
	classes := map[string]logsTypes.LogGroupClass{}
	for _, n := range names {
		classes[n] = ""
	}
	return seedLogGroupClasses(t, st, classes)
}

// seedLogGroupClasses is seedLogGroups with each group's LogGroupClass ("" for
// none, as in rows stored before log classes).
func seedLogGroupClasses(t *testing.T, st *store.Store, classes map[string]logsTypes.LogGroupClass) []store.Resource {
	t.Helper()
	acct := newTestAccount(testAccountID)
	region := testRegion
	batch := make([]*store.Resource, 0, len(classes))
	for n, class := range classes {
		batch = append(batch, &store.Resource{
			Provider: "aws", AccountID: testAccountID, AccountName: &acct.Name,
			Type: TypeLogsLogGroup, NativeID: lgTestARN(n), Name: ptrStr(n), Region: &region,
			AttributesJSON: mustJSON(logsTypes.LogGroup{LogGroupName: ptrStr(n), LogGroupClass: class}), DiscoveredBy: testScanID,
		})
	}
	if _, err := st.UpsertResources(batch); err != nil {
		t.Fatalf("seed log groups: %v", err)
	}
	groups, err := loadLogGroupsForRegion(acct, testRegion, st)
	if err != nil {
		t.Fatalf("load log groups: %v", err)
	}
	return groups
}

// --- index policies ---

func logGroupIndexPolicy(groupARN string) logsTypes.IndexPolicy {
	return logsTypes.IndexPolicy{
		LogGroupIdentifier: ptrStr(groupARN), PolicyDocument: ptrStr(`{"Fields":["requestId"]}`),
		Source: logsTypes.IndexSourceLogGroup,
	}
}

func accountIndexPolicy() logsTypes.IndexPolicy {
	return logsTypes.IndexPolicy{
		PolicyName: ptrStr("acct-wide"), PolicyDocument: ptrStr(`{"Fields":["traceId"]}`),
		Source: logsTypes.IndexSourceAccount,
	}
}

func TestScanLogsIndexPolicies_StoresOnlyGroupLevelPolicies(t *testing.T) {
	st := newTestStore(t)
	groups := seedLogGroups(t, st, "a", "b", "c")
	calls := &parentCalls{}
	client := logsTestClient(map[string]func(any) (any, error){
		"DescribeIndexPolicies": func(in any) (any, error) {
			req := in.(*cwlogs.DescribeIndexPoliciesInput)
			arn := req.LogGroupIdentifiers[0]
			calls.add(arn)
			switch {
			// a's own policy arrives on the second page, after an account-sourced one.
			case arn == lgTestARN("a") && req.NextToken == nil:
				return &cwlogs.DescribeIndexPoliciesOutput{IndexPolicies: []logsTypes.IndexPolicy{accountIndexPolicy()}, NextToken: ptrStr("p2")}, nil
			case arn == lgTestARN("a"):
				return &cwlogs.DescribeIndexPoliciesOutput{IndexPolicies: []logsTypes.IndexPolicy{logGroupIndexPolicy(arn)}}, nil
			case arn == lgTestARN("b"):
				return &cwlogs.DescribeIndexPoliciesOutput{IndexPolicies: []logsTypes.IndexPolicy{accountIndexPolicy()}}, nil
			default:
				return nil, logsGone
			}
		},
	})
	total, _, err := scanLogsIndexPolicies(context.Background(), client, newTestAccount(testAccountID), testRegion, groups, st, testScanID)
	if err != nil || total != 1 {
		t.Fatalf("total, err = %d, %v; want 1, nil", total, err)
	}
	if got, want := calls.distinct(), []string{lgTestARN("a"), lgTestARN("b"), lgTestARN("c")}; !slices.Equal(got, want) {
		t.Errorf("log groups queried %v, want %v", got, want)
	}
	rows := rowsByNativeID(t, st, TypeLogsIndexPolicy)
	if len(rows) != 1 {
		t.Fatalf("index policy rows = %v, want one", slices.Collect(maps.Keys(rows)))
	}
	id := lgTestARN("a") + ":index-policy"
	got, ok := rows[id]
	if !ok {
		t.Fatalf("index policy %s missing; rows %v", id, slices.Collect(maps.Keys(rows)))
	}
	if got.Type != TypeLogsIndexPolicy || sv(got.Name) != "a" || sv(got.Region) != testRegion {
		t.Errorf("row = type %q name %q region %q", got.Type, sv(got.Name), sv(got.Region))
	}
	var attrs struct{ PolicyDocument, Source string }
	if err := json.Unmarshal([]byte(got.AttributesJSON), &attrs); err != nil || attrs.PolicyDocument != `{"Fields":["requestId"]}` || attrs.Source != "LOG_GROUP" {
		t.Errorf("attrs = %+v, %v; want a's policy document and Source LOG_GROUP", attrs, err)
	}
	assertContainsNative(t, st, lgTestARN("a"), id)
}

func TestScanLogsIndexPolicies_NoPoliciesStoresNothing(t *testing.T) {
	st := newTestStore(t)
	groups := seedLogGroups(t, st, "a")
	client := logsTestClient(map[string]func(any) (any, error){
		"DescribeIndexPolicies": func(any) (any, error) { return &cwlogs.DescribeIndexPoliciesOutput{}, nil },
	})
	total, _, err := scanLogsIndexPolicies(context.Background(), client, newTestAccount(testAccountID), testRegion, groups, st, testScanID)
	if err != nil || total != 0 || len(rowsByNativeID(t, st, TypeLogsIndexPolicy)) != 0 {
		t.Errorf("total, err = %d, %v; want 0 rows, nil", total, err)
	}
}

func TestScanLogsIndexPolicies_DeniedWarnsOnceAndKeepsSiblings(t *testing.T) {
	st := newTestStore(t)
	groups := seedLogGroups(t, st, "a", "b", "c")
	warnings := countOpWarnings(st, "logs:DescribeIndexPolicies")
	client := logsTestClient(map[string]func(any) (any, error){
		"DescribeIndexPolicies": func(in any) (any, error) {
			arn := in.(*cwlogs.DescribeIndexPoliciesInput).LogGroupIdentifiers[0]
			if arn == lgTestARN("a") {
				return &cwlogs.DescribeIndexPoliciesOutput{IndexPolicies: []logsTypes.IndexPolicy{logGroupIndexPolicy(arn)}}, nil
			}
			return nil, logsDenied
		},
	})
	total, _, err := scanLogsIndexPolicies(context.Background(), client, newTestAccount(testAccountID), testRegion, groups, st, testScanID)
	if err != nil || total != 1 {
		t.Fatalf("total, err = %d, %v; want 1, nil", total, err)
	}
	if *warnings != 1 {
		t.Errorf("warnings = %d, want 1 for two denied log groups", *warnings)
	}
}

func TestScanLogsIndexPolicies_OtherErrorPropagates(t *testing.T) {
	st := newTestStore(t)
	groups := seedLogGroups(t, st, "a")
	client := logsTestClient(map[string]func(any) (any, error){
		"DescribeIndexPolicies": func(any) (any, error) { return nil, logsBadArg },
	})
	_, _, err := scanLogsIndexPolicies(context.Background(), client, newTestAccount(testAccountID), testRegion, groups, st, testScanID)
	if !isAPIErrorCode(err, "InvalidParameterException") {
		t.Errorf("err = %v; want the InvalidParameterException", err)
	}
}

// A repeated token fails the group's listing with errRepeatedNextToken, and
// the policy already listed is stored.
func TestScanLogsIndexPolicies_RepeatedTokenFails(t *testing.T) {
	st := newTestStore(t)
	groups := seedLogGroups(t, st, "a")
	calls := &parentCalls{}
	client := logsTestClient(map[string]func(any) (any, error){
		"DescribeIndexPolicies": func(in any) (any, error) {
			calls.add(sv(in.(*cwlogs.DescribeIndexPoliciesInput).NextToken))
			return &cwlogs.DescribeIndexPoliciesOutput{
				IndexPolicies: []logsTypes.IndexPolicy{{Source: logsTypes.IndexSourceLogGroup, PolicyDocument: ptrStr("{}")}},
				NextToken:     ptrStr("same"),
			}, nil
		},
	})
	_, _, err := scanLogsIndexPolicies(context.Background(), client, newTestAccount(testAccountID), testRegion, groups, st, testScanID)
	if !errors.Is(err, errRepeatedNextToken) {
		t.Fatalf("err = %v, want errRepeatedNextToken", err)
	}
	if got := len(calls.ids); got != 2 {
		t.Errorf("calls = %d, want 2 (first page, then the repeated token)", got)
	}
	if got := len(rowsByNativeID(t, st, TypeLogsIndexPolicy)); got != 1 {
		t.Errorf("stored = %d; want the listed policy stored", got)
	}
}

func TestScanLogsIndexPolicies_QueriesOnlyStandardGroups(t *testing.T) {
	st := newTestStore(t)
	groups := seedLogGroupClasses(t, st, map[string]logsTypes.LogGroupClass{
		"std": logsTypes.LogGroupClassStandard, "legacy": "",
		"ia": logsTypes.LogGroupClassInfrequentAccess, "delivery": logsTypes.LogGroupClassDelivery,
	})
	calls := &parentCalls{}
	client := logsTestClient(map[string]func(any) (any, error){
		"DescribeIndexPolicies": func(in any) (any, error) {
			calls.add(in.(*cwlogs.DescribeIndexPoliciesInput).LogGroupIdentifiers[0])
			return &cwlogs.DescribeIndexPoliciesOutput{}, nil
		},
	})
	if _, _, err := scanLogsIndexPolicies(context.Background(), client, newTestAccount(testAccountID), testRegion, groups, st, testScanID); err != nil {
		t.Fatalf("err = %v", err)
	}
	if got, want := calls.distinct(), []string{lgTestARN("legacy"), lgTestARN("std")}; !slices.Equal(got, want) {
		t.Errorf("log groups queried %v, want %v", got, want)
	}
}

func TestScanLogsIndexPolicies_ThrottledGroupWarnsAndKeepsOthers(t *testing.T) {
	st := newTestStore(t)
	groups := seedLogGroups(t, st, "a", "b", "c", "d")
	warnings := countOpWarnings(st, "logs:DescribeIndexPolicies")
	client := logsTestClient(map[string]func(any) (any, error){
		"DescribeIndexPolicies": func(in any) (any, error) {
			arn := in.(*cwlogs.DescribeIndexPoliciesInput).LogGroupIdentifiers[0]
			if arn == lgTestARN("c") || arn == lgTestARN("d") {
				return nil, &smithy.GenericAPIError{Code: "ThrottlingException", Message: "rate exceeded"}
			}
			return &cwlogs.DescribeIndexPoliciesOutput{IndexPolicies: []logsTypes.IndexPolicy{logGroupIndexPolicy(arn)}}, nil
		},
	})
	total, _, err := scanLogsIndexPolicies(context.Background(), client, newTestAccount(testAccountID), testRegion, groups, st, testScanID)
	if err != nil || total != 2 || len(rowsByNativeID(t, st, TypeLogsIndexPolicy)) != 2 {
		t.Errorf("total, err = %d, %v; want 2 stored, nil", total, err)
	}
	if *warnings != 1 {
		t.Errorf("warnings = %d, want 1 for the two throttled groups", *warnings)
	}
}

func TestScanLogsIndexPolicies_UndecodableGroupFails(t *testing.T) {
	st := newTestStore(t)
	calls := &parentCalls{}
	client := logsTestClient(map[string]func(any) (any, error){
		"DescribeIndexPolicies": func(in any) (any, error) {
			calls.add(in.(*cwlogs.DescribeIndexPoliciesInput).LogGroupIdentifiers[0])
			return &cwlogs.DescribeIndexPoliciesOutput{}, nil
		},
	})
	groups := []store.Resource{{NativeID: lgTestARN("bad"), AttributesJSON: "{"}}
	_, _, err := scanLogsIndexPolicies(context.Background(), client, newTestAccount(testAccountID), testRegion, groups, st, testScanID)
	var syntaxErr *json.SyntaxError
	if !errors.As(err, &syntaxErr) || len(calls.ids) != 0 {
		t.Errorf("err, calls = %v, %v; want a JSON syntax error and no call", err, calls.ids)
	}
}

// --- syslog configurations ---

func syslogConfig(groupARN, vpce string) logsTypes.SyslogConfiguration {
	return logsTypes.SyslogConfiguration{
		LogGroupArn: ptrStr(groupARN + ":*"), VpcEndpointId: ptrStr(vpce),
		SourceType: logsTypes.SyslogSourceTypeVpce, CreatedAt: sdkaws.Int64(logsCreated.UnixMilli()),
	}
}

func TestScanLogsSyslogConfigurations_StoresEveryPage(t *testing.T) {
	st := newTestStore(t)
	groups := seedLogGroups(t, st, "a")
	var (
		mu    sync.Mutex
		sizes []int32
	)
	client := logsTestClient(map[string]func(any) (any, error){
		"ListSyslogConfigurations": func(in any) (any, error) {
			req := in.(*cwlogs.ListSyslogConfigurationsInput)
			mu.Lock()
			sizes = append(sizes, req.MaxResults)
			mu.Unlock()
			if req.NextToken == nil {
				return &cwlogs.ListSyslogConfigurationsOutput{
					SyslogConfigurations: []logsTypes.SyslogConfiguration{syslogConfig(lgTestARN("a"), "vpce-1")},
					NextToken:            ptrStr("p2"),
				}, nil
			}
			noVPCE := syslogConfig(lgTestARN("a"), "vpce-x")
			noVPCE.VpcEndpointId = nil
			return &cwlogs.ListSyslogConfigurationsOutput{SyslogConfigurations: []logsTypes.SyslogConfiguration{
				syslogConfig(lgTestARN("unlisted"), "vpce-2"), noVPCE,
			}}, nil
		},
	})
	var warnings []store.ScanWarning
	st.OnWarn = func(w store.ScanWarning) { warnings = append(warnings, w) }
	total, _, err := scanLogsSyslogConfigurations(context.Background(), client, newTestAccount(testAccountID), testRegion, groups, st, testScanID)
	if err != nil || total != 2 {
		t.Fatalf("total, err = %d, %v; want 2, nil", total, err)
	}
	if len(warnings) != 0 {
		t.Errorf("warnings = %v; want none (no pair is recorded for an unlisted log group)", warnings)
	}
	if !slices.Equal(sizes, []int32{50, 50}) {
		t.Errorf("MaxResults per page = %v, want [50 50]", sizes)
	}
	rows := rowsByNativeID(t, st, TypeLogsSyslogConfig)
	first := lgTestARN("a") + ":syslog-configuration:vpce-1"
	second := lgTestARN("unlisted") + ":syslog-configuration:vpce-2"
	if len(rows) != 2 {
		t.Fatalf("rows = %v, want %s and %s", slices.Collect(maps.Keys(rows)), first, second)
	}
	got, ok := rows[first]
	if !ok {
		t.Fatalf("syslog configuration %s missing; rows %v", first, slices.Collect(maps.Keys(rows)))
	}
	if got.Type != TypeLogsSyslogConfig || sv(got.Name) != "a" || sv(got.Region) != testRegion || sv(got.CreatedAt) != "2026-09-01T08:00:00Z" {
		t.Errorf("row = type %q name %q region %q created %q", got.Type, sv(got.Name), sv(got.Region), sv(got.CreatedAt))
	}
	var attrs struct {
		SourceType    string
		VpcEndpointID string `json:"VpcEndpointId"`
	}
	if err := json.Unmarshal([]byte(got.AttributesJSON), &attrs); err != nil || attrs.SourceType != "VPCE" || attrs.VpcEndpointID != "vpce-1" {
		t.Errorf("attrs = %+v, %v; want SourceType VPCE, VpcEndpointId vpce-1", attrs, err)
	}
	if _, ok := rows[second]; !ok {
		t.Errorf("second-page configuration %s missing", second)
	}
	assertContainsNative(t, st, lgTestARN("a"), first)
	rels, err := st.RelationshipsTo(store.ResourceID("aws", testAccountID, second), store.RelContains)
	if err != nil || len(rels) != 0 {
		t.Errorf("contains edges into %s = %v, %v; want none (its log group was not listed)", second, rels, err)
	}
}

func TestScanLogsSyslogConfigurations_Errors(t *testing.T) {
	onePage := func(in any) (any, error) {
		if in.(*cwlogs.ListSyslogConfigurationsInput).NextToken == nil {
			return &cwlogs.ListSyslogConfigurationsOutput{
				SyslogConfigurations: []logsTypes.SyslogConfiguration{syslogConfig(lgTestARN("a"), "vpce-1")},
				NextToken:            ptrStr("p2"),
			}, nil
		}
		return nil, logsNoOp
	}
	for _, tc := range []struct {
		name      string
		list      func(any) (any, error)
		wantTotal int
		wantErr   string
		wantIs    error
		wantWarn  int
	}{
		{name: "empty", list: func(any) (any, error) { return &cwlogs.ListSyslogConfigurationsOutput{}, nil }},
		{name: "denied", list: func(any) (any, error) { return nil, logsDenied }, wantWarn: 1},
		{name: "op not deployed", list: func(any) (any, error) { return nil, logsNoOp }},
		{name: "op gone after a page", list: onePage, wantTotal: 1, wantErr: "UnknownOperationException"},
		{name: "other error", list: func(any) (any, error) { return nil, logsBadArg }, wantErr: "InvalidParameterException"},
		{name: "repeated token fails", list: func(in any) (any, error) {
			if in.(*cwlogs.ListSyslogConfigurationsInput).NextToken != nil {
				return &cwlogs.ListSyslogConfigurationsOutput{NextToken: ptrStr("same")}, nil
			}
			return &cwlogs.ListSyslogConfigurationsOutput{
				SyslogConfigurations: []logsTypes.SyslogConfiguration{syslogConfig(lgTestARN("a"), "vpce-1")},
				NextToken:            ptrStr("same"),
			}, nil
		}, wantTotal: 1, wantIs: errRepeatedNextToken},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			warnings := countOpWarnings(st, "logs:ListSyslogConfigurations")
			client := logsTestClient(map[string]func(any) (any, error){"ListSyslogConfigurations": tc.list})
			total, _, err := scanLogsSyslogConfigurations(context.Background(), client, newTestAccount(testAccountID), testRegion, nil, st, testScanID)
			switch {
			case tc.wantIs != nil:
				if !errors.Is(err, tc.wantIs) {
					t.Errorf("err = %v; want %v", err, tc.wantIs)
				}
			case tc.wantErr == "" && err != nil || tc.wantErr != "" && !isAPIErrorCode(err, tc.wantErr):
				t.Errorf("err = %v; want code %q", err, tc.wantErr)
			}
			if total != tc.wantTotal || len(rowsByNativeID(t, st, TypeLogsSyslogConfig)) != tc.wantTotal {
				t.Errorf("total = %d; want %d stored", total, tc.wantTotal)
			}
			if *warnings != tc.wantWarn {
				t.Errorf("warnings = %d, want %d", *warnings, tc.wantWarn)
			}
		})
	}
}

// --- S3 Table integration sources ---

func s3tSource(id, name string) logsTypes.S3TableIntegrationSource {
	return logsTypes.S3TableIntegrationSource{
		Identifier: ptrStr(id), DataSource: &logsTypes.DataSource{Name: ptrStr(name), Type: ptrStr("CWL")},
		Status: logsTypes.S3TableIntegrationSourceStatusActive, CreatedTimeStamp: sdkaws.Int64(logsCreated.UnixMilli()),
	}
}

// s3tHandlers lists integrations int-1 and int-2 over two pages (plus one with
// no ARN); int-1 has a source on each of two pages, int-2 is gone. Ops in
// override answer instead.
func s3tHandlers(calls *parentCalls, override map[string]func(any) (any, error)) map[string]func(any) (any, error) {
	handlers := map[string]func(any) (any, error){
		"ListS3TableIntegrations": func(in any) (any, error) {
			if in.(*observabilityadmin.ListS3TableIntegrationsInput).NextToken == nil {
				return &observabilityadmin.ListS3TableIntegrationsOutput{
					IntegrationSummaries: []obstypes.IntegrationSummary{{Arn: ptrStr(s3tIntegrationARN("int-1"))}},
					NextToken:            ptrStr("p2"),
				}, nil
			}
			return &observabilityadmin.ListS3TableIntegrationsOutput{IntegrationSummaries: []obstypes.IntegrationSummary{
				{Arn: ptrStr(s3tIntegrationARN("int-2")), Status: obstypes.IntegrationStatusDeleting}, {},
			}}, nil
		},
		"ListSourcesForS3TableIntegration": func(in any) (any, error) {
			req := in.(*cwlogs.ListSourcesForS3TableIntegrationInput)
			calls.add(*req.IntegrationArn)
			switch {
			case *req.IntegrationArn != s3tIntegrationARN("int-1"):
				return nil, logsGone
			case req.NextToken == nil:
				return &cwlogs.ListSourcesForS3TableIntegrationOutput{Sources: []logsTypes.S3TableIntegrationSource{s3tSource("src-1", "vpc-flow")}, NextToken: ptrStr("p2")}, nil
			default:
				return &cwlogs.ListSourcesForS3TableIntegrationOutput{Sources: []logsTypes.S3TableIntegrationSource{s3tSource("src-2", "cloudtrail"), s3tSource("", "no-id")}}, nil
			}
		},
	}
	maps.Copy(handlers, override)
	return handlers
}

func runS3TSources(t *testing.T, st *store.Store, acct *account, handlers map[string]func(any) (any, error)) (int, error) {
	t.Helper()
	total, _, err := scanLogsS3TableSources(context.Background(), logsTestClient(handlers), obsTestClient(handlers), acct, testRegion, st, testScanID)
	return total, err
}

func TestScanLogsS3TableSources_StoresEveryIntegrationsSources(t *testing.T) {
	st := newTestStore(t)
	calls := &parentCalls{}
	var (
		mu     sync.Mutex
		limits []int32
	)
	handlers := s3tHandlers(calls, nil)
	list := handlers["ListSourcesForS3TableIntegration"]
	handlers["ListSourcesForS3TableIntegration"] = func(in any) (any, error) {
		mu.Lock()
		limits = append(limits, sdkaws.ToInt32(in.(*cwlogs.ListSourcesForS3TableIntegrationInput).MaxResults))
		mu.Unlock()
		return list(in)
	}
	total, err := runS3TSources(t, st, newTestAccount(testAccountID), handlers)
	if err != nil || total != 2 {
		t.Fatalf("total, err = %d, %v; want 2, nil", total, err)
	}
	if got, want := calls.distinct(), []string{s3tIntegrationARN("int-1"), s3tIntegrationARN("int-2")}; !slices.Equal(got, want) {
		t.Errorf("integrations queried %v, want %v", got, want)
	}
	if slices.ContainsFunc(limits, func(l int32) bool { return l != 100 }) {
		t.Errorf("MaxResults per call = %v, want 100 each", limits)
	}
	rows := rowsByNativeID(t, st, TypeLogsSource)
	first := logsSourceNativeID(s3tIntegrationARN("int-1"), "src-1")
	second := logsSourceNativeID(s3tIntegrationARN("int-1"), "src-2")
	if len(rows) != 2 {
		t.Fatalf("rows = %v, want %s and %s", slices.Collect(maps.Keys(rows)), first, second)
	}
	got, ok := rows[first]
	if !ok {
		t.Fatalf("source %s missing; rows %v", first, slices.Collect(maps.Keys(rows)))
	}
	if first != s3tIntegrationARN("int-1")+"/source/src-1" {
		t.Errorf("NativeID = %q, want {integrationARN}/source/{identifier}", first)
	}
	if got.Type != TypeLogsSource || sv(got.Name) != "vpc-flow" || sv(got.Region) != testRegion || sv(got.Status) != "ACTIVE" || sv(got.CreatedAt) != "2026-09-01T08:00:00Z" {
		t.Errorf("row = type %q name %q region %q status %q created %q", got.Type, sv(got.Name), sv(got.Region), sv(got.Status), sv(got.CreatedAt))
	}
	var attrs struct{ Identifier string }
	if err := json.Unmarshal([]byte(got.AttributesJSON), &attrs); err != nil || attrs.Identifier != "src-1" {
		t.Errorf("attrs = %+v, %v; want Identifier src-1", attrs, err)
	}
	if _, ok := rows[second]; !ok {
		t.Errorf("second-page source %s missing", second)
	}
}

func TestScanLogsS3TableSources_NoIntegrationsListsNoSources(t *testing.T) {
	st := newTestStore(t)
	calls := &parentCalls{}
	total, err := runS3TSources(t, st, newTestAccount(testAccountID), s3tHandlers(calls, map[string]func(any) (any, error){
		"ListS3TableIntegrations": func(any) (any, error) { return &observabilityadmin.ListS3TableIntegrationsOutput{}, nil },
	}))
	if err != nil || total != 0 || len(calls.ids) != 0 {
		t.Errorf("total, err, source calls = %d, %v, %v; want 0, nil, none", total, err, calls.ids)
	}
}

func TestScanLogsS3TableSources_Errors(t *testing.T) {
	const (
		obsOp = "observabilityadmin:ListS3TableIntegrations"
		srcOp = "logs:ListSourcesForS3TableIntegration"
	)
	fail := func(err error) func(any) (any, error) { return func(any) (any, error) { return nil, err } }
	for _, tc := range []struct {
		name       string
		override   map[string]func(any) (any, error)
		wantTotal  int
		wantErr    string
		wantWarn   map[string]int
		wantNotice int
	}{
		{name: "integrations denied", override: map[string]func(any) (any, error){"ListS3TableIntegrations": fail(logsDenied)}, wantNotice: 1},
		{name: "integrations endpoint absent", override: map[string]func(any) (any, error){"ListS3TableIntegrations": fail(&net.DNSError{Err: "no such host", Name: "observabilityadmin.xx.amazonaws.com", IsNotFound: true})}},
		{name: "integrations unreachable", override: map[string]func(any) (any, error){"ListS3TableIntegrations": fail(&net.OpError{Op: "dial", Err: errors.New("connection refused")})}, wantWarn: map[string]int{obsOp: 1}},
		{name: "integrations other error", override: map[string]func(any) (any, error){"ListS3TableIntegrations": fail(logsBadArg)}, wantErr: "InvalidParameterException"},
		{name: "sources denied", override: map[string]func(any) (any, error){"ListSourcesForS3TableIntegration": fail(logsDenied)}, wantWarn: map[string]int{srcOp: 1}},
		{name: "sources other error", override: map[string]func(any) (any, error){"ListSourcesForS3TableIntegration": fail(logsBadArg)}, wantErr: "InvalidParameterException"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			var (
				mu    sync.Mutex
				warns = map[string]int{}
			)
			st.OnWarn = func(w store.ScanWarning) {
				mu.Lock()
				warns[w.Service]++
				mu.Unlock()
			}
			notices := 0
			st.OnNotice = func(n store.ScanNotice) {
				if n.Service == obsOp {
					notices++
				}
			}
			total, err := runS3TSources(t, st, newTestAccount(testAccountID), s3tHandlers(&parentCalls{}, tc.override))
			if tc.wantErr == "" && err != nil || tc.wantErr != "" && !isAPIErrorCode(err, tc.wantErr) {
				t.Errorf("err = %v; want code %q", err, tc.wantErr)
			}
			if total != tc.wantTotal || len(rowsByNativeID(t, st, TypeLogsSource)) != tc.wantTotal {
				t.Errorf("total = %d; want %d stored", total, tc.wantTotal)
			}
			if tc.wantWarn == nil {
				tc.wantWarn = map[string]int{}
			}
			if !maps.Equal(warns, tc.wantWarn) {
				t.Errorf("warnings = %v, want %v", warns, tc.wantWarn)
			}
			if notices != tc.wantNotice {
				t.Errorf("notices = %d, want %d", notices, tc.wantNotice)
			}
		})
	}
}

func TestScanLogsS3TableSources_ThrottledIntegrationsWarnOnceAndKeepOthers(t *testing.T) {
	st := newTestStore(t)
	warnings := countOpWarnings(st, "logs:ListSourcesForS3TableIntegration")
	handlers := s3tHandlers(&parentCalls{}, nil)
	list := handlers["ListSourcesForS3TableIntegration"]
	handlers["ListS3TableIntegrations"] = func(any) (any, error) {
		return &observabilityadmin.ListS3TableIntegrationsOutput{IntegrationSummaries: []obstypes.IntegrationSummary{
			{Arn: ptrStr(s3tIntegrationARN("int-1"))}, {Arn: ptrStr(s3tIntegrationARN("int-2"))}, {Arn: ptrStr(s3tIntegrationARN("int-3"))},
		}}, nil
	}
	handlers["ListSourcesForS3TableIntegration"] = func(in any) (any, error) {
		if *in.(*cwlogs.ListSourcesForS3TableIntegrationInput).IntegrationArn != s3tIntegrationARN("int-1") {
			return nil, &smithy.GenericAPIError{Code: "ThrottlingException", Message: "rate exceeded"}
		}
		return list(in)
	}
	total, err := runS3TSources(t, st, newTestAccount(testAccountID), handlers)
	if err != nil || total != 2 || len(rowsByNativeID(t, st, TypeLogsSource)) != 2 {
		t.Errorf("total, err = %d, %v; want int-1's 2 sources stored, nil", total, err)
	}
	if *warnings != 1 {
		t.Errorf("warnings = %d, want 1 for the two throttled integrations", *warnings)
	}
}

func TestScanLogsS3TableSources_SkipsRegionWithoutObservabilityAdmin(t *testing.T) {
	const region = "xx-nowhere-1"
	for _, tc := range []struct {
		name         string
		scopeOff     bool
		wantObsCalls int
	}{
		{name: "region scoping on", wantObsCalls: 0},
		{name: "region scoping off", scopeOff: true, wantObsCalls: 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			acct := newTestAccount(testAccountID)
			acct.availByCode = map[string]map[string]bool{"observabilityadmin": {testRegion: true}}
			acct.regionScopeDisabled = tc.scopeOff
			obsCalls := &parentCalls{}
			handlers := s3tHandlers(&parentCalls{}, nil)
			list := handlers["ListS3TableIntegrations"]
			handlers["ListS3TableIntegrations"] = func(in any) (any, error) {
				obsCalls.add("call")
				return list(in)
			}
			_, _, err := scanLogsS3TableSources(context.Background(), logsTestClient(handlers), obsTestClient(handlers), acct, region, st, testScanID)
			if err != nil || len(obsCalls.ids) != tc.wantObsCalls {
				t.Errorf("err, ListS3TableIntegrations calls = %v, %d; want nil, %d", err, len(obsCalls.ids), tc.wantObsCalls)
			}
		})
	}
}

// --- top-level wiring ---

// logsTopLevelHandlers answers the region's one log group "a" (with one log
// stream) and every new phase-2/3 op; every other op is denied. Ops in override
// answer instead.
func logsTopLevelHandlers(override map[string]func(any) (any, error)) map[string]func(any) (any, error) {
	handlers := s3tHandlers(&parentCalls{}, nil)
	maps.Copy(handlers, map[string]func(any) (any, error){
		"DescribeLogGroups": func(any) (any, error) {
			return &cwlogs.DescribeLogGroupsOutput{LogGroups: []logsTypes.LogGroup{{LogGroupName: ptrStr("a"), Arn: ptrStr(lgTestARN("a") + ":*")}}}, nil
		},
		"DescribeLogStreams": func(any) (any, error) {
			return &cwlogs.DescribeLogStreamsOutput{LogStreams: []logsTypes.LogStream{{LogStreamName: ptrStr("s1")}}}, nil
		},
		"DescribeIndexPolicies": func(in any) (any, error) {
			arn := in.(*cwlogs.DescribeIndexPoliciesInput).LogGroupIdentifiers[0]
			return &cwlogs.DescribeIndexPoliciesOutput{IndexPolicies: []logsTypes.IndexPolicy{logGroupIndexPolicy(arn)}}, nil
		},
		"ListSyslogConfigurations": func(any) (any, error) {
			return &cwlogs.ListSyslogConfigurationsOutput{SyslogConfigurations: []logsTypes.SyslogConfiguration{syslogConfig(lgTestARN("a"), "vpce-1")}}, nil
		},
	})
	maps.Copy(handlers, override)
	return handlers
}

func TestScanLogs_StoresNewChildTypes(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	acct.cfg = opStubConfig(logsTopLevelHandlers(nil), logsDenied)
	if _, _, err := scanLogs(context.Background(), acct, testRegion, st, testScanID); err != nil {
		t.Fatalf("scanLogs: %v", err)
	}
	for typ, id := range map[string]string{
		TypeLogsIndexPolicy:  lgTestARN("a") + ":index-policy",
		TypeLogsSyslogConfig: lgTestARN("a") + ":syslog-configuration:vpce-1",
		TypeLogsSource:       logsSourceNativeID(s3tIntegrationARN("int-1"), "src-1"),
	} {
		if _, ok := rowsByNativeID(t, st, typ)[id]; !ok {
			t.Errorf("%s row %s missing", typ, id)
		}
	}
	assertContainsNative(t, st, lgTestARN("a"), lgTestARN("a")+":index-policy")
	assertContainsNative(t, st, lgTestARN("a"), lgTestARN("a")+":syslog-configuration:vpce-1")
}

// TestScanLogs_NewPhaseErrorsDoNotStopOtherScans fails each new op with its own
// code: the existing phases still store their rows, every new phase still runs,
// and each failure is returned.
func TestScanLogs_NewPhaseErrorsDoNotStopOtherScans(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	fail := func(code string) func(any) (any, error) {
		return func(any) (any, error) { return nil, &smithy.GenericAPIError{Code: code, Message: "boom"} }
	}
	acct.cfg = opStubConfig(logsTopLevelHandlers(map[string]func(any) (any, error){
		"DescribeIndexPolicies":            fail("IndexBoom"),
		"ListSyslogConfigurations":         fail("SyslogBoom"),
		"ListSourcesForS3TableIntegration": fail("SourceBoom"),
	}), logsDenied)
	_, _, err := scanLogs(context.Background(), acct, testRegion, st, testScanID)
	for _, code := range []string{"IndexBoom", "SyslogBoom", "SourceBoom"} {
		if !joinCarriesAPIErrorCode(err, code) {
			t.Errorf("err = %v; want it to carry %s", err, code)
		}
	}
	if _, ok := rowsByNativeID(t, st, TypeLogsLogStream)["a/stream/s1"]; !ok {
		t.Error("log stream missing: a new phase's failure stopped phase 2")
	}
}

// joinCarriesAPIErrorCode reports whether any member of err's join tree carries
// code; isAPIErrorCode alone stops at the first API error it finds.
func joinCarriesAPIErrorCode(err error, code string) bool {
	if j, ok := err.(interface{ Unwrap() []error }); ok {
		return slices.ContainsFunc(j.Unwrap(), func(e error) bool { return joinCarriesAPIErrorCode(e, code) })
	}
	return isAPIErrorCode(err, code)
}

// TestScanLogs_TransientPhaseErrorDoesNotMaskHardError throttles one phase and
// fails another: the throttle is a warning, and the returned error classifies
// as the hard error it carries.
func TestScanLogs_TransientPhaseErrorDoesNotMaskHardError(t *testing.T) {
	throttled := func(any) (any, error) {
		return nil, &smithy.GenericAPIError{Code: "ThrottlingException", Message: "rate exceeded"}
	}
	hard := func(any) (any, error) { return nil, logsBadArg }
	for _, tc := range []struct {
		name, throttledOp, hardOp string
	}{
		{name: "phase 2 throttled, phase 3 fails", throttledOp: "DescribeLogStreams", hardOp: "ListSyslogConfigurations"},
		{name: "phase 3 throttled, phase 2 fails", throttledOp: "ListSyslogConfigurations", hardOp: "DescribeLogStreams"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			acct := newTestAccount(testAccountID)
			warnings := countOpWarnings(st, "logs:"+tc.throttledOp)
			acct.cfg = opStubConfig(logsTopLevelHandlers(map[string]func(any) (any, error){
				tc.throttledOp: throttled,
				tc.hardOp:      hard,
			}), logsDenied)
			_, _, err := scanLogs(context.Background(), acct, testRegion, st, testScanID)
			if got := classifyServiceError(err); got != outcomeError || !joinCarriesAPIErrorCode(err, "InvalidParameterException") {
				t.Errorf("classifyServiceError(%v) = %d; want outcomeError (%d) carrying InvalidParameterException", err, got, outcomeError)
			}
			if *warnings != 1 {
				t.Errorf("%s warnings = %d, want 1", tc.throttledOp, *warnings)
			}
		})
	}
}

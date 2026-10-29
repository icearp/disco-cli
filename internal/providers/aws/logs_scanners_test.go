package aws

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net"
	"slices"
	"strings"
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
	groups, err := loadLogGroupsForRegion(acct, testRegion, st, testScanID, false)
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
		name          string
		override      map[string]func(any) (any, error)
		wantTotal     int
		wantErr       string
		wantTransient bool
		wantWarn      map[string]int
		wantNotice    int
	}{
		{name: "integrations denied", override: map[string]func(any) (any, error){"ListS3TableIntegrations": fail(logsDenied)}, wantNotice: 1},
		{name: "integrations endpoint absent", override: map[string]func(any) (any, error){"ListS3TableIntegrations": fail(&net.DNSError{Err: "no such host", Name: "observabilityadmin.xx.amazonaws.com", IsNotFound: true})}},
		{name: "integrations unreachable", override: map[string]func(any) (any, error){"ListS3TableIntegrations": fail(&net.OpError{Op: "dial", Err: errors.New("connection refused")})}, wantTransient: true},
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
			switch {
			case tc.wantTransient:
				if got := classifyServiceError(err); got != outcomeTransient {
					t.Errorf("classifyServiceError(%v) = %v, want outcomeTransient", err, got)
				}
			case tc.wantErr == "" && err != nil || tc.wantErr != "" && !isAPIErrorCode(err, tc.wantErr):
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

// A throttle on one phase, with no other phase failing, is returned as
// transient, so the scan is partial rather than complete; it is not demoted.
func TestScanLogs_LoneTransientPhaseMarksPartial(t *testing.T) {
	throttled := func(any) (any, error) {
		return nil, &smithy.GenericAPIError{Code: "ThrottlingException", Message: "rate exceeded"}
	}
	for _, op := range []string{"DescribeLogStreams", "ListSyslogConfigurations"} {
		t.Run(op, func(t *testing.T) {
			st := newTestStore(t)
			acct := newTestAccount(testAccountID)
			warnings := countOpWarnings(st, "logs:"+op)
			acct.cfg = opStubConfig(logsTopLevelHandlers(map[string]func(any) (any, error){op: throttled}), logsDenied)
			total, _, err := scanLogs(context.Background(), acct, testRegion, st, testScanID)
			if got := classifyServiceError(err); got != outcomeTransient {
				t.Errorf("classifyServiceError(%v) = %d; want outcomeTransient (%d)", err, got, outcomeTransient)
			}
			if *warnings != 0 {
				t.Errorf("%s warnings = %d, want 0", op, *warnings)
			}
			if total == 0 {
				t.Error("total = 0; want the other phases' rows counted")
			}
		})
	}
}

// An integrations endpoint that stops resolving after a page succeeded is a
// plain error, not observabilityadmin's region gap, so the listing is reported
// incomplete instead of read as finished.
func TestScanLogsS3TableSources_IntegrationsNXDOMAINAfterPageIsAnError(t *testing.T) {
	st := newTestStore(t)
	handlers := s3tHandlers(&parentCalls{}, nil)
	first := handlers["ListS3TableIntegrations"]
	handlers["ListS3TableIntegrations"] = func(in any) (any, error) {
		if in.(*observabilityadmin.ListS3TableIntegrationsInput).NextToken == nil {
			return first(in)
		}
		return nil, &net.DNSError{Err: "no such host", Name: "observabilityadmin.us-east-1.amazonaws.com", IsNotFound: true}
	}
	_, err := runS3TSources(t, st, newTestAccount(testAccountID), handlers)
	if got := classifyServiceError(err); err == nil || got != outcomeError {
		t.Errorf("classifyServiceError(%v) = %v, want outcomeError", err, got)
	}
}

// --- tags ---

const (
	// logsTestListed is the rows of the tagged listings: two per listing,
	// one per page.
	logsTestListed = 2 * 8
	// logsTestTotal adds one log stream per log group.
	logsTestTotal   = logsTestListed + 2
	logsTestOldScan = "an-earlier-scan"
)

var logsThrottle = apiErr("ThrottlingException", "Rate exceeded")

// logsKind is one listing whose rows carry tags: the SDK op, the type it
// stores, the ARN resource type of its rows, the input field that sets its
// page size and the size every call must send, whether the scan reads the
// row's tags with ListTagsForResource (else the element carries them), and
// page, which builds the op's output holding one element named name under
// arn, carrying tags, with next.
type logsKind struct {
	op, typ, arnType, sizeField string
	size                        int32
	tagRead                     bool
	page                        func(arn, name *string, tags map[string]string, next *string) any
}

func (k logsKind) name(suffix string) string { return k.arnType + "-" + suffix }

// arnIn is the row's ARN in the Service Reference's format.
func (k logsKind) arnIn(partition, region, suffix string) string {
	return "arn:" + partition + ":logs:" + region + ":" + testAccountID + ":" + k.arnType + ":" + k.name(suffix)
}

func (k logsKind) arn(suffix string) string { return k.arnIn("aws", testRegion, suffix) }

// logsWildcard is arn with the ":*" DescribeLogGroups appends to a log
// group's Arn.
func logsWildcard(arn *string) *string {
	if arn == nil {
		return nil
	}
	return sdkaws.String(*arn + ":*")
}

func logsKinds() []logsKind {
	return []logsKind{
		{"DescribeLogGroups", TypeLogsLogGroup, "log-group", "Limit", 50, true, func(arn, name *string, _ map[string]string, next *string) any {
			return &cwlogs.DescribeLogGroupsOutput{NextToken: next, LogGroups: []logsTypes.LogGroup{{Arn: logsWildcard(arn), LogGroupArn: arn, LogGroupName: name}}}
		}},
		{"DescribeDeliveries", TypeLogsDelivery, "delivery", "Limit", 50, false, func(arn, name *string, tags map[string]string, next *string) any {
			return &cwlogs.DescribeDeliveriesOutput{NextToken: next, Deliveries: []logsTypes.Delivery{{Arn: arn, DeliverySourceName: name, Tags: tags}}}
		}},
		{"DescribeDeliveryDestinations", TypeLogsDeliveryDest, "delivery-destination", "Limit", 50, false, func(arn, name *string, tags map[string]string, next *string) any {
			return &cwlogs.DescribeDeliveryDestinationsOutput{NextToken: next, DeliveryDestinations: []logsTypes.DeliveryDestination{{Arn: arn, Name: name, Tags: tags}}}
		}},
		{"DescribeDeliverySources", TypeLogsDeliverySource, "delivery-source", "Limit", 50, false, func(arn, name *string, tags map[string]string, next *string) any {
			return &cwlogs.DescribeDeliverySourcesOutput{NextToken: next, DeliverySources: []logsTypes.DeliverySource{{Arn: arn, Name: name, Tags: tags}}}
		}},
		{"DescribeDestinations", TypeLogsDestination, "destination", "Limit", 50, true, func(arn, name *string, _ map[string]string, next *string) any {
			return &cwlogs.DescribeDestinationsOutput{NextToken: next, Destinations: []logsTypes.Destination{{Arn: arn, DestinationName: name}}}
		}},
		{"ListLogAnomalyDetectors", TypeLogsLogAnomalyDetector, "anomaly-detector", "Limit", 50, true, func(arn, name *string, _ map[string]string, next *string) any {
			return &cwlogs.ListLogAnomalyDetectorsOutput{NextToken: next, AnomalyDetectors: []logsTypes.AnomalyDetector{{AnomalyDetectorArn: arn, DetectorName: name}}}
		}},
		{"ListScheduledQueries", TypeLogsScheduledQuery, "scheduled-query", "MaxResults", 1000, true, func(arn, name *string, _ map[string]string, next *string) any {
			return &cwlogs.ListScheduledQueriesOutput{NextToken: next, ScheduledQueries: []logsTypes.ScheduledQuerySummary{{ScheduledQueryArn: arn, Name: name}}}
		}},
		{"DescribeLookupTables", TypeLogsLookupTable, "lookup-table", "MaxResults", 100, true, func(arn, name *string, _ map[string]string, next *string) any {
			return &cwlogs.DescribeLookupTablesOutput{NextToken: next, LookupTables: []logsTypes.LookupTable{{LookupTableArn: arn, LookupTableName: name}}}
		}},
	}
}

func logsKindOf(t *testing.T, op string) logsKind {
	t.Helper()
	for _, k := range logsKinds() {
		if k.op == op {
			return k
		}
	}
	t.Fatalf("%s is no tagged logs listing", op)
	return logsKind{}
}

// logsPageSize is the page size in sent in field, a pointer or not; 0 for
// none.
func logsPageSize(in any, field string) int32 {
	if p := bacField[*int32](in, field); p != nil {
		return *p
	}
	return bacField[int32](in, field)
}

// logsCalls records the calls of a scan in partition and region, and
// answers ListTagsForResource with tags, or errs, by ARN.
type logsCalls struct {
	partition, region string
	mu                sync.Mutex
	ops               map[string]int
	inputs            map[string][]any
	tagARNs           []string
	streamGroups      []string
	tags              map[string]map[string]string
	errs              map[string]error
}

// newLogsCalls tags the "tagged" row of every kind with its type, in the
// listing's element or for ListTagsForResource.
func newLogsCalls(partition, region string) *logsCalls {
	c := &logsCalls{partition: partition, region: region, ops: map[string]int{}, inputs: map[string][]any{}, tags: map[string]map[string]string{}, errs: map[string]error{}}
	for _, k := range logsKinds() {
		if k.tagRead {
			c.tags[c.arn(k, "tagged")] = map[string]string{"type": k.typ}
		}
	}
	return c
}

func (c *logsCalls) arn(k logsKind, suffix string) string {
	return k.arnIn(c.partition, c.region, suffix)
}

func (c *logsCalls) record(op string, in any) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.ops[op]++
	c.inputs[op] = append(c.inputs[op], in)
}

func (c *logsCalls) count(op string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.ops[op]
}

func (c *logsCalls) listTags(in any) (any, error) {
	arn := sv(in.(*cwlogs.ListTagsForResourceInput).ResourceArn)
	c.record("ListTagsForResource", in)
	c.mu.Lock()
	c.tagARNs = append(c.tagARNs, arn)
	err, tags := c.errs[arn], c.tags[arn]
	c.mu.Unlock()
	if err != nil {
		return nil, err
	}
	return &cwlogs.ListTagsForResourceOutput{Tags: tags}, nil
}

// twoPages answers k's op with "tagged" on the first page and "bare" on the
// second; a kind whose element carries its tags gives "tagged" the tag type=<its type>.
func (c *logsCalls) twoPages(k logsKind) func(any) (any, error) {
	return func(in any) (any, error) {
		c.record(k.op, in)
		suffix, next := "tagged", sdkaws.String("t2")
		if bacField[*string](in, "NextToken") != nil {
			suffix, next = "bare", nil
		}
		var tags map[string]string
		if suffix == "tagged" && !k.tagRead {
			tags = map[string]string{"type": k.typ}
		}
		arn, name := c.arn(k, suffix), k.name(suffix)
		return k.page(&arn, &name, tags, next), nil
	}
}

// logsTagHandlers answers every op the scan calls: each tagged kind in two
// pages, one log stream per log group, and no rows for every other listing
// (GetTransformer is denied, which the scan skips silently).
func logsTagHandlers(c *logsCalls) map[string]func(in any) (any, error) {
	empty := func(op string, out any) func(any) (any, error) {
		return func(in any) (any, error) {
			c.record(op, in)
			return out, nil
		}
	}
	h := map[string]func(in any) (any, error){
		"ListTagsForResource":         c.listTags,
		"DescribeAccountPolicies":     empty("DescribeAccountPolicies", &cwlogs.DescribeAccountPoliciesOutput{}),
		"ListIntegrations":            empty("ListIntegrations", &cwlogs.ListIntegrationsOutput{}),
		"DescribeMetricFilters":       empty("DescribeMetricFilters", &cwlogs.DescribeMetricFiltersOutput{}),
		"DescribeQueryDefinitions":    empty("DescribeQueryDefinitions", &cwlogs.DescribeQueryDefinitionsOutput{}),
		"DescribeResourcePolicies":    empty("DescribeResourcePolicies", &cwlogs.DescribeResourcePoliciesOutput{}),
		"DescribeSubscriptionFilters": empty("DescribeSubscriptionFilters", &cwlogs.DescribeSubscriptionFiltersOutput{}),
		"DescribeIndexPolicies":       empty("DescribeIndexPolicies", &cwlogs.DescribeIndexPoliciesOutput{}),
		"ListSyslogConfigurations":    empty("ListSyslogConfigurations", &cwlogs.ListSyslogConfigurationsOutput{}),
		"ListS3TableIntegrations":     empty("ListS3TableIntegrations", &observabilityadmin.ListS3TableIntegrationsOutput{}),
		"DescribeLogStreams": func(in any) (any, error) {
			c.record("DescribeLogStreams", in)
			group := sv(in.(*cwlogs.DescribeLogStreamsInput).LogGroupName)
			c.mu.Lock()
			c.streamGroups = append(c.streamGroups, group)
			c.mu.Unlock()
			return &cwlogs.DescribeLogStreamsOutput{LogStreams: []logsTypes.LogStream{{LogStreamName: sdkaws.String("s1")}}}, nil
		},
	}
	for _, k := range logsKinds() {
		h[k.op] = c.twoPages(k)
	}
	return h
}

// logsScan runs the scan's real entry point against handlers; every op they
// do not answer is denied.
func logsScan(ctx context.Context, st *store.Store, handlers map[string]func(in any) (any, error)) (int, int, error) {
	acct := newTestAccount(testAccountID)
	acct.cfg = opStubConfig(handlers, logsDenied)
	return scanLogs(ctx, acct, testRegion, st, testScanID)
}

// logsTagReadARNs is the ARN of every row whose tags the fixture's scan
// reads, sorted.
func logsTagReadARNs(c *logsCalls) []string {
	var arns []string
	for _, k := range logsKinds() {
		if k.tagRead {
			arns = append(arns, c.arn(k, "tagged"), c.arn(k, "bare"))
		}
	}
	slices.Sort(arns)
	return arns
}

// Every tagged listing stores both its pages, each row under its ARN (a log
// group's without the ":*" its Arn carries), named by its name (a delivery by
// its source's), in the region, with the listed element as attributes and no
// status or creation time. Its tags come from ListTagsForResource, sent
// exactly the ARNs of the log groups, destinations, anomaly detectors,
// scheduled queries and lookup tables, or from the element for the three
// delivery types; nil for a resource without tags. Log streams are stored
// untagged and never sent to the tag op. Every listing sends its page cap.
func TestScanLogs_StoresTagsOfEveryType(t *testing.T) {
	st := newTestStore(t)
	warns := rhWarnRecorder(st)
	calls := newLogsCalls("aws", testRegion)

	total, inserted, err := logsScan(context.Background(), st, logsTagHandlers(calls))
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != logsTestTotal || inserted != logsTestTotal {
		t.Errorf("total, inserted = %d, %d; want %d, %d", total, inserted, logsTestTotal, logsTestTotal)
	}
	if w := warns(); len(w) != 0 {
		t.Errorf("warnings = %v; want none", w)
	}
	for _, k := range logsKinds() {
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
			if sv(r.Name) != k.name(suffix) || sv(r.Region) != testRegion || r.Status != nil || r.CreatedAt != nil || r.ManagedByProvider {
				t.Errorf("%s Name, Region, Status, CreatedAt, Managed = %q, %q, %v, %v, %v; want %q, %q, no status, no creation time, false",
					arn, sv(r.Name), sv(r.Region), r.Status, r.CreatedAt, r.ManagedByProvider, k.name(suffix), testRegion)
			}
			if k.typ == TypeLogsLogGroup && !strings.Contains(r.AttributesJSON, `"Arn":"`+arn+`:*"`) {
				t.Errorf("%s attrs = %s; want the listed element, its Arn ending in :*", arn, r.AttributesJSON)
			}
			if !strings.Contains(r.AttributesJSON, `"`+arn+`"`) || !strings.Contains(r.AttributesJSON, `"`+k.name(suffix)+`"`) {
				t.Errorf("%s attrs = %s; want the listed element", arn, r.AttributesJSON)
			}
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
			if got := logsPageSize(in, k.sizeField); got != k.size {
				t.Errorf("%s %s = %d; want %d", k.op, k.sizeField, got, k.size)
			}
		}
	}
	streams := rowsByNativeID(t, st, TypeLogsLogStream)
	if len(streams) != 2 {
		t.Errorf("log streams = %v; want one per log group", nativeIDs(streams))
	}
	for id, r := range streams {
		if r.TagsJSON != nil {
			t.Errorf("log stream %s TagsJSON = %q; want nil", id, *r.TagsJSON)
		}
	}
	if got := slices.Sorted(slices.Values(calls.tagARNs)); !slices.Equal(got, logsTagReadARNs(calls)) {
		t.Errorf("ListTagsForResource ARNs = %v\nwant %v", got, logsTagReadARNs(calls))
	}
}

// A rescan stores the same rows without inserting any.
func TestScanLogs_RescanInsertsNone(t *testing.T) {
	st := newTestStore(t)
	if _, _, err := logsScan(context.Background(), st, logsTagHandlers(newLogsCalls("aws", testRegion))); err != nil {
		t.Fatalf("first scan: %v", err)
	}
	total, inserted, err := logsScan(context.Background(), st, logsTagHandlers(newLogsCalls("aws", testRegion)))
	if err != nil {
		t.Fatalf("rescan: %v", err)
	}
	if total != logsTestTotal || inserted != 0 {
		t.Errorf("rescan total, inserted = %d, %d; want %d, 0", total, inserted, logsTestTotal)
	}
}

// A failed tag read: ResourceNotFoundException, the op's only modeled
// not-found code, drops the row silently (a log group's stream is then not
// listed); a denial keeps the row untagged and warns once under
// logs:ListTagsForResource; a throttle keeps it and is transient; any other
// error keeps it and is an error.
func TestScanLogs_TagReadFailures(t *testing.T) {
	type tagCase struct {
		name      string
		k         logsKind
		err       error
		outcome   serviceOutcome // -1: no error
		code      string
		wantTotal int
		wantKept  bool
		wantWarns int
	}
	var cases []tagCase
	for _, k := range logsKinds() {
		if !k.tagRead {
			continue
		}
		gone := logsTestTotal - 1
		if k.typ == TypeLogsLogGroup {
			gone--
		}
		cases = append(cases,
			tagCase{k.typ + "/not found", k, logsGone, -1, "", gone, false, 0},
			tagCase{k.typ + "/denied", k, logsDenied, -1, "", logsTestTotal, true, 1},
			tagCase{k.typ + "/transient", k, logsThrottle, outcomeTransient, "ThrottlingException", logsTestTotal, true, 0},
			tagCase{k.typ + "/other error", k, logsBadArg, outcomeError, "InvalidParameterException", logsTestTotal, true, 0},
		)
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := rhWarnRecorder(st)
			calls := newLogsCalls("aws", testRegion)
			arn := c.k.arn("tagged")
			calls.errs[arn] = c.err

			total, _, err := logsScan(context.Background(), st, logsTagHandlers(calls))
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
			r, ok := rowsByNativeID(t, st, c.k.typ)[arn]
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
				if x.Service != logsTagsOp || x.Scope != testAccountID+"/"+testRegion || !strings.Contains(x.Message, arn) {
					t.Errorf("warning %+v; want one under %s in %s/%s naming %s", x, logsTagsOp, testAccountID, testRegion, arn)
				}
			}
		})
	}
}

// The per-group listings (phase 2) read only the log groups this scan
// stored: not one an earlier scan stored and this one did not list, nor one
// whose tag read found it deleted.
func TestScanLogs_PerGroupPhaseReadsGroupsStoredThisScan(t *testing.T) {
	st := newTestStore(t)
	seedOldLogGroup(t, st)
	calls := newLogsCalls("aws", testRegion)
	k := logsKindOf(t, "DescribeLogGroups")
	calls.errs[k.arn("tagged")] = logsGone

	if _, _, err := logsScan(context.Background(), st, logsTagHandlers(calls)); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if got, want := calls.streamGroups, []string{k.name("bare")}; !slices.Equal(got, want) {
		t.Errorf("DescribeLogStreams log groups = %v; want %v", got, want)
	}
}

// seedOldLogGroup stores log group "old" as an earlier scan found it.
func seedOldLogGroup(t *testing.T, st *store.Store) {
	t.Helper()
	insertTestScan(t, st, logsTestOldScan, "completed")
	acct := newTestAccount(testAccountID)
	region := testRegion
	if _, err := st.UpsertResources([]*store.Resource{{
		Provider: "aws", AccountID: testAccountID, AccountName: &acct.Name,
		Type: TypeLogsLogGroup, NativeID: lgTestARN("old"), Name: ptrStr("old"), Region: &region,
		AttributesJSON: mustJSON(logsTypes.LogGroup{LogGroupName: ptrStr("old")}), DiscoveredBy: logsTestOldScan,
	}}); err != nil {
		t.Fatalf("seed: %v", err)
	}
}

// A denied DescribeLogGroups confirms none of the groups after the denied
// page, so the per-group phase reads every group stored for the region: one
// an earlier scan stored still has its stream listed and stored, beside the
// group of a page read before the denial.
func TestScanLogs_DeniedLogGroupsReadsStoredGroups(t *testing.T) {
	k := logsKindOf(t, "DescribeLogGroups")
	for call, want := range map[int][]string{1: {"old"}, 2: {k.name("tagged"), "old"}} {
		t.Run(fmt.Sprintf("page%d", call), func(t *testing.T) {
			st := newTestStore(t)
			seedOldLogGroup(t, st)
			calls := newLogsCalls("aws", testRegion)
			handlers := logsTagHandlers(calls)
			handlers[k.op] = failOnCall(call, logsDenied, handlers[k.op])

			if _, _, err := logsScan(context.Background(), st, handlers); err != nil {
				t.Fatalf("scan: %v", err)
			}
			got := slices.Sorted(slices.Values(calls.streamGroups))
			if !slices.Equal(got, want) {
				t.Errorf("DescribeLogStreams log groups = %v; want %v", got, want)
			}
			if _, ok := rowsByNativeID(t, st, TypeLogsLogStream)["old/stream/s1"]; !ok {
				t.Error("stream of the earlier scan's log group not stored")
			}
		})
	}
}

// Only a denial lifts the per-group phase to every stored group: after any
// other DescribeLogGroups error the phase still reads only the groups this
// scan stored, so an earlier scan's group gets no stream listing.
func TestScanLogs_FailedLogGroupsReadsGroupsStoredThisScan(t *testing.T) {
	k := logsKindOf(t, "DescribeLogGroups")
	for call, want := range map[int][]string{1: nil, 2: {k.name("tagged")}} {
		t.Run(fmt.Sprintf("page%d", call), func(t *testing.T) {
			st := newTestStore(t)
			seedOldLogGroup(t, st)
			calls := newLogsCalls("aws", testRegion)
			handlers := logsTagHandlers(calls)
			handlers[k.op] = failOnCall(call, logsBadArg, handlers[k.op])

			_, _, err := logsScan(context.Background(), st, handlers)
			assertGlueOutcome(t, err, outcomeError)
			assertAPICode(t, err, "InvalidParameterException")
			if got := calls.streamGroups; !slices.Equal(got, want) {
				t.Errorf("DescribeLogStreams log groups = %v; want %v", got, want)
			}
			if _, ok := rowsByNativeID(t, st, TypeLogsLogStream)["old/stream/s1"]; ok {
				t.Error("stream of the earlier scan's log group stored; want none")
			}
		})
	}
}

// A failed read of the log groups the per-group phase needs stops the scan
// as a store failure, not as a transient one a rescan fixes: no per-group
// listing and no later phase runs.
func TestScanLogs_LogGroupLoadFailureStopsScan(t *testing.T) {
	st := newTestStore(t)
	calls := newLogsCalls("aws", testRegion)
	handlers := logsTagHandlers(calls)
	// The last phase-1 listing drops the table the load reads.
	handlers["DescribeLookupTables"] = func(any) (any, error) {
		if _, err := st.DB().Exec(`ALTER TABLE resources RENAME TO resources_gone`); err != nil {
			t.Errorf("rename resources: %v", err)
		}
		return &cwlogs.DescribeLookupTablesOutput{}, nil
	}

	_, _, err := logsScan(context.Background(), st, handlers)
	if !errors.Is(err, errStoreRead) {
		t.Fatalf("err = %v; want errStoreRead", err)
	}
	assertGlueOutcome(t, err, outcomeError)
	for _, op := range []string{"DescribeLogStreams", "ListSyslogConfigurations", "ListS3TableIntegrations"} {
		if n := calls.count(op); n != 0 {
			t.Errorf("%s called %d times after the failed read; want 0", op, n)
		}
	}
}

// A delivery without an ARN is warned about under its Id, not under its
// source's name, which many deliveries share.
func TestScanLogs_DeliveryWithoutARNWarnsByID(t *testing.T) {
	st := newTestStore(t)
	warns := rhWarnRecorder(st)
	handlers := logsTagHandlers(newLogsCalls("aws", testRegion))
	handlers["DescribeDeliveries"] = func(any) (any, error) {
		return &cwlogs.DescribeDeliveriesOutput{Deliveries: []logsTypes.Delivery{{Id: sdkaws.String("d-1"), DeliverySourceName: sdkaws.String("src")}}}, nil
	}

	if _, _, err := logsScan(context.Background(), st, handlers); err != nil {
		t.Fatalf("scan: %v", err)
	}
	w := warns()
	if len(w) != 1 || w[0].Service != "logs:DescribeDeliveries" || !strings.HasSuffix(w[0].Message, " d-1") {
		t.Errorf("warnings = %+v; want one under logs:DescribeDeliveries naming d-1", w)
	}
}

// Denied tag reads in two listings send one warning for the whole scan,
// under the tag op, naming the first denied row; the rows are kept untagged.
func TestScanLogs_TagDenialsWarnOncePerScan(t *testing.T) {
	st := newTestStore(t)
	warnings := bacWarnings(st, logsTagsOp)
	calls := newLogsCalls("aws", testRegion)
	denied := []string{"DescribeDestinations", "DescribeLookupTables"}
	for _, op := range denied {
		k := logsKindOf(t, op)
		calls.errs[k.arn("tagged")] = logsDenied
		calls.errs[k.arn("bare")] = logsDenied
	}

	total, _, err := logsScan(context.Background(), st, logsTagHandlers(calls))
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != logsTestTotal {
		t.Errorf("total = %d; want %d", total, logsTestTotal)
	}
	first := logsKindOf(t, "DescribeDestinations").arn("tagged")
	if len(*warnings) != 1 || (*warnings)[0].Scope != testAccountID+"/"+testRegion || !strings.Contains((*warnings)[0].Message, first) {
		t.Fatalf("%s warnings = %v; want 1 in %s/%s naming %s", logsTagsOp, *warnings, testAccountID, testRegion, first)
	}
	for _, op := range denied {
		k := logsKindOf(t, op)
		for _, arn := range []string{k.arn("tagged"), k.arn("bare")} {
			if r, ok := rowsByNativeID(t, st, k.typ)[arn]; !ok || r.TagsJSON != nil {
				t.Errorf("%s stored %v; want stored untagged", arn, ok)
			}
		}
	}
}

// A throttled tag read beside a hard failure of the same op is demoted to one
// warning, so the hard error decides the outcome.
func TestScanLogs_TagThrottleDemotedBesideHardError(t *testing.T) {
	st := newTestStore(t)
	warns := countOpWarnings(st, logsTagsOp)
	calls := newLogsCalls("aws", testRegion)
	calls.errs[logsKindOf(t, "DescribeDestinations").arn("tagged")] = logsThrottle
	calls.errs[logsKindOf(t, "DescribeLookupTables").arn("bare")] = logsBadArg

	_, _, err := logsScan(context.Background(), st, logsTagHandlers(calls))
	assertGlueOutcome(t, err, outcomeError)
	assertAPICode(t, err, "InvalidParameterException")
	if *warns != 1 {
		t.Errorf("%s warnings = %d; want 1", logsTagsOp, *warns)
	}
}

// A failed write stops the scan: no later phase runs and no warning is sent
// about rows the write lost; the phases before it keep their rows. That
// holds for a failed write in the per-group phase too.
func TestScanLogs_FailedWriteStopsScan(t *testing.T) {
	t.Run("destinations", func(t *testing.T) {
		st := newTestStore(t)
		warns := rhWarnRecorder(st)
		refuseStoreWrites(t, st, TypeLogsDestination)
		calls := newLogsCalls("aws", testRegion)
		k := logsKindOf(t, "DescribeDestinations")
		calls.errs[k.arn("tagged")] = logsDenied

		total, _, err := logsScan(context.Background(), st, logsTagHandlers(calls))
		if !errors.Is(err, store.ErrStoreWrite) {
			t.Fatalf("err = %v; want store.ErrStoreWrite", err)
		}
		if w := warns(); len(w) != 0 {
			t.Errorf("warnings = %v; want none", w)
		}
		if n := calls.count(k.op); n != 1 {
			t.Errorf("%s called %d times; want 1, none after the failed write", k.op, n)
		}
		for _, op := range []string{"ListIntegrations", "ListLogAnomalyDetectors", "DescribeLookupTables", "DescribeLogStreams", "ListSyslogConfigurations"} {
			if n := calls.count(op); n != 0 {
				t.Errorf("%s called %d times after the failed write; want 0", op, n)
			}
		}
		// The log groups and the three delivery types (2 each) ran before it.
		if total != 4*2 {
			t.Errorf("total = %d; want %d", total, 4*2)
		}
	})
	t.Run("log streams", func(t *testing.T) {
		st := newTestStore(t)
		refuseStoreWrites(t, st, TypeLogsLogStream)
		calls := newLogsCalls("aws", testRegion)

		_, _, err := logsScan(context.Background(), st, logsTagHandlers(calls))
		if !errors.Is(err, store.ErrStoreWrite) {
			t.Fatalf("err = %v; want store.ErrStoreWrite", err)
		}
		for _, op := range []string{"ListSyslogConfigurations", "ListS3TableIntegrations"} {
			if n := calls.count(op); n != 0 {
				t.Errorf("%s called %d times after the failed write; want 0", op, n)
			}
		}
	})
}

// A listing's failed page keeps the page read before it, and its error does
// not stop the later listings: the log group of the first page still has its
// stream listed, and the last phase-1 listing stores its rows.
func TestScanLogs_ListFailureKeepsPagesAndLaterPhases(t *testing.T) {
	for _, op := range []string{"DescribeLogGroups", "DescribeDeliveries", "ListScheduledQueries"} {
		t.Run(op, func(t *testing.T) {
			st := newTestStore(t)
			calls := newLogsCalls("aws", testRegion)
			handlers := logsTagHandlers(calls)
			k := logsKindOf(t, op)
			handlers[k.op] = failOnCall(2, logsBadArg, handlers[k.op])

			total, _, err := logsScan(context.Background(), st, handlers)
			assertGlueOutcome(t, err, outcomeError)
			assertAPICode(t, err, "InvalidParameterException")
			lost := 1
			if k.typ == TypeLogsLogGroup {
				lost++ // the second group's stream
			}
			if total != logsTestTotal-lost {
				t.Errorf("total = %d; want %d", total, logsTestTotal-lost)
			}
			if _, ok := rowsByNativeID(t, st, k.typ)[k.arn("tagged")]; !ok {
				t.Error("row from the page before the failure missing")
			}
			if got := len(rowsByNativeID(t, st, TypeLogsLookupTable)); got != 2 {
				t.Errorf("lookup tables (the last phase-1 listing) = %d; want 2", got)
			}
			if _, ok := rowsByNativeID(t, st, TypeLogsLogStream)[logsKindOf(t, "DescribeLogGroups").name("tagged")+"/stream/s1"]; !ok {
				t.Error("first log group's stream missing: the per-group phase did not run")
			}
		})
	}
}

// A denied listing warns once under its op; a denial on a later page keeps
// the page read before it. Every later listing still runs.
func TestScanLogs_DeniedListingWarnsUnderItsOp(t *testing.T) {
	for _, op := range []string{"DescribeDestinations", "DescribeDeliverySources"} {
		for _, call := range []int{1, 2} {
			t.Run(fmt.Sprintf("%s/page%d", op, call), func(t *testing.T) {
				st := newTestStore(t)
				warns := rhWarnRecorder(st)
				handlers := logsTagHandlers(newLogsCalls("aws", testRegion))
				k := logsKindOf(t, op)
				handlers[k.op] = failOnCall(call, logsDenied, handlers[k.op])

				total, _, err := logsScan(context.Background(), st, handlers)
				if err != nil {
					t.Fatalf("err = %v; want nil", err)
				}
				if want := logsTestTotal - 2 + call - 1; total != want {
					t.Errorf("total = %d; want %d", total, want)
				}
				if w := warns(); len(w) != 1 || w[0].Service != "logs:"+k.op {
					t.Errorf("warnings = %v; want one under logs:%s", w, k.op)
				}
				if got := len(rowsByNativeID(t, st, TypeLogsLookupTable)); got != 2 {
					t.Errorf("lookup tables (a later listing) = %d; want 2", got)
				}
			})
		}
	}
}

// A repeated token ends a listing as an error, keeping its pages.
func TestScanLogs_RepeatedTokenKeepsPages(t *testing.T) {
	for _, k := range logsKinds() {
		t.Run(k.op, func(t *testing.T) {
			st := newTestStore(t)
			calls := newLogsCalls("aws", testRegion)
			handlers := logsTagHandlers(calls)
			handlers[k.op] = func(in any) (any, error) {
				suffix := "tagged"
				if bacField[*string](in, "NextToken") != nil {
					suffix = "bare"
				}
				arn, name := k.arn(suffix), k.name(suffix)
				return k.page(&arn, &name, nil, sdkaws.String("same")), nil
			}
			total, _, err := logsScan(context.Background(), st, handlers)
			if !errors.Is(err, errRepeatedNextToken) {
				t.Fatalf("err = %v; want errRepeatedNextToken", err)
			}
			if total != logsTestTotal {
				t.Errorf("total = %d; want %d", total, logsTestTotal)
			}
			if got := len(rowsByNativeID(t, st, k.typ)); got != 2 {
				t.Errorf("%s rows = %d; want both pages' 2", k.typ, got)
			}
		})
	}
}

// An element without an ARN is not stored, is not sent to the tag op, and is
// warned about once under its listing, named by its name or by the fallback
// text when it has none.
func TestScanLogs_ElementWithoutARNWarns(t *testing.T) {
	for _, k := range logsKinds() {
		for _, name := range []string{"x-1", ""} {
			t.Run(k.op+"/"+cmp.Or(name, "unnamed"), func(t *testing.T) {
				st := newTestStore(t)
				warns := rhWarnRecorder(st)
				calls := newLogsCalls("aws", testRegion)
				handlers := logsTagHandlers(calls)
				handlers[k.op] = func(any) (any, error) { return k.page(nil, &name, nil, nil), nil }

				total, _, err := logsScan(context.Background(), st, handlers)
				if err != nil {
					t.Fatalf("scan: %v", err)
				}
				lost := 2
				if k.typ == TypeLogsLogGroup {
					lost += 2 // their streams
				}
				if want := logsTestTotal - lost; total != want {
					t.Errorf("total = %d; want %d", total, want)
				}
				w := warns()
				if len(w) != 1 {
					t.Fatalf("warnings = %v; want 1", w)
				}
				key := cmp.Or(name, logsNoIdentifier)
				if w[0].Service != "logs:"+k.op || !strings.Contains(w[0].Message, key) || w[0].Scope != testAccountID+"/"+testRegion {
					t.Errorf("warning %+v; want one under logs:%s naming %q in %s/%s", w[0], k.op, key, testAccountID, testRegion)
				}
				if slices.Contains(calls.tagARNs, "") {
					t.Error("an empty ARN was sent to ListTagsForResource")
				}
			})
		}
	}
}

// Rows of the aws-us-gov and aws-cn partitions reach the tag op under their
// listed ARNs unchanged.
func TestScanLogs_TagReadsKeepPartitionARNs(t *testing.T) {
	for _, partition := range []string{"aws-us-gov", "aws-cn"} {
		t.Run(partition, func(t *testing.T) {
			st := newTestStore(t)
			acct := partitionAccount(partition)
			region := acct.Regions[0]
			calls := newLogsCalls(partition, region)
			acct.cfg = opStubConfig(logsTagHandlers(calls), logsDenied)
			if _, _, err := scanLogs(context.Background(), acct, region, st, testScanID); err != nil {
				t.Fatalf("scan: %v", err)
			}
			if got := slices.Sorted(slices.Values(calls.tagARNs)); !slices.Equal(got, logsTagReadARNs(calls)) {
				t.Errorf("ListTagsForResource ARNs = %v\nwant %v", got, logsTagReadARNs(calls))
			}
			k := logsKindOf(t, "DescribeLogGroups")
			assertTagsMap(t, rowsByNativeID(t, st, k.typ)[calls.arn(k, "tagged")].TagsJSON, map[string]string{"type": k.typ})
		})
	}
}

// Once ctx ends no further phase starts, and the stop is one member of the
// error, although the phase in flight saw it in its listing.
func TestScanLogs_ContextEndReportedOnce(t *testing.T) {
	st := newTestStore(t)
	calls := newLogsCalls("aws", testRegion)
	handlers := logsTagHandlers(calls)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	k := logsKindOf(t, "DescribeDestinations")
	list := handlers[k.op]
	handlers[k.op] = func(in any) (any, error) {
		if bacField[*string](in, "NextToken") != nil {
			cancel()
			return nil, context.Canceled
		}
		return list(in)
	}

	_, _, err := logsScan(ctx, st, handlers)
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
	for _, op := range []string{"ListIntegrations", "DescribeLogStreams"} {
		if n := calls.count(op); n != 0 {
			t.Errorf("%s called %d times after the stop; want 0", op, n)
		}
	}
}

// An NXDOMAIN after rows were stored is a failure of this scan, not a region
// gap: the counts are kept. With nothing stored it stays region-unavailable.
func TestScanLogs_NXDOMAIN(t *testing.T) {
	nx := &net.DNSError{Err: "no such host", Name: "logs.us-east-1.amazonaws.com", IsNotFound: true}
	t.Run("a listing after others stored rows", func(t *testing.T) {
		st := newTestStore(t)
		handlers := logsTagHandlers(newLogsCalls("aws", testRegion))
		handlers["DescribeLookupTables"] = func(any) (any, error) { return nil, nx }
		total, _, err := logsScan(context.Background(), st, handlers)
		assertGlueOutcome(t, err, outcomeError)
		if total != logsTestTotal-2 {
			t.Errorf("total = %d; want the %d other rows counted", total, logsTestTotal-2)
		}
	})
	t.Run("tag read after the listing", func(t *testing.T) {
		st := newTestStore(t)
		calls := newLogsCalls("aws", testRegion)
		calls.errs[logsKindOf(t, "DescribeLogGroups").arn("bare")] = nx
		_, _, err := logsScan(context.Background(), st, logsTagHandlers(calls))
		assertGlueOutcome(t, err, outcomeError)
	})
	t.Run("nothing stored", func(t *testing.T) {
		st := newTestStore(t)
		warns := rhWarnRecorder(st)
		acct := newTestAccount(testAccountID)
		acct.cfg = opStubConfig(nil, nx)
		_, _, err := scanLogs(context.Background(), acct, testRegion, st, testScanID)
		assertGlueOutcome(t, err, outcomeUnavailable)
		if w := warns(); len(w) != 0 {
			t.Errorf("warnings = %v; want none", w)
		}
	})
}

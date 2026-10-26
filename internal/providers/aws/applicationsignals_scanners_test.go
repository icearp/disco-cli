package aws

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/applicationsignals"
	applicationsignalstypes "github.com/aws/aws-sdk-go-v2/service/applicationsignals/types"
	"github.com/icearp/disco-cli/store"
)

// stubApplicationSignals is a deterministic in-memory replacement for
// applicationSignalsAPI used by the scanner tests.
type stubApplicationSignals struct {
	sloPages      []*applicationsignals.ListServiceLevelObjectivesOutput
	sloIdx        int
	groupingPages []*applicationsignals.ListGroupingAttributeDefinitionsOutput
	groupingIdx   int
	servicePages  []*applicationsignals.ListServicesOutput
	serviceIdx    int
	sloErr        error
	groupingErr   error
	serviceErr    error
}

func (s *stubApplicationSignals) ListServices(_ context.Context, _ *applicationsignals.ListServicesInput, _ ...func(*applicationsignals.Options)) (*applicationsignals.ListServicesOutput, error) {
	if s.serviceErr != nil {
		return nil, s.serviceErr
	}
	if s.serviceIdx >= len(s.servicePages) {
		return &applicationsignals.ListServicesOutput{}, nil
	}
	out := s.servicePages[s.serviceIdx]
	s.serviceIdx++
	return out, nil
}

func (s *stubApplicationSignals) ListServiceLevelObjectives(_ context.Context, _ *applicationsignals.ListServiceLevelObjectivesInput, _ ...func(*applicationsignals.Options)) (*applicationsignals.ListServiceLevelObjectivesOutput, error) {
	if s.sloErr != nil {
		return nil, s.sloErr
	}
	if s.sloIdx >= len(s.sloPages) {
		return &applicationsignals.ListServiceLevelObjectivesOutput{}, nil
	}
	out := s.sloPages[s.sloIdx]
	s.sloIdx++
	return out, nil
}

func (s *stubApplicationSignals) ListGroupingAttributeDefinitions(_ context.Context, _ *applicationsignals.ListGroupingAttributeDefinitionsInput, _ ...func(*applicationsignals.Options)) (*applicationsignals.ListGroupingAttributeDefinitionsOutput, error) {
	if s.groupingErr != nil {
		return nil, s.groupingErr
	}
	if s.groupingIdx >= len(s.groupingPages) {
		return &applicationsignals.ListGroupingAttributeDefinitionsOutput{}, nil
	}
	out := s.groupingPages[s.groupingIdx]
	s.groupingIdx++
	return out, nil
}

func (s *stubApplicationSignals) ListInstrumentationConfigurations(_ context.Context, _ *applicationsignals.ListInstrumentationConfigurationsInput, _ ...func(*applicationsignals.Options)) (*applicationsignals.ListInstrumentationConfigurationsOutput, error) {
	return &applicationsignals.ListInstrumentationConfigurationsOutput{}, nil
}

func TestApplicationSignalsGroupingConfigurationNativeID(t *testing.T) {
	got := applicationSignalsGroupingConfigurationNativeID("aws", "us-east-1", "111111111111")
	want := "arn:aws:application-signals:us-east-1:111111111111:grouping-configuration"
	if got != want {
		t.Errorf("native id = %q, want %q", got, want)
	}
}

func TestScanApplicationSignalsGroupingConfiguration_Populated(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	region := testRegion

	gname := "BusinessUnit"
	updated := time.Date(2026, 4, 1, 12, 0, 0, 0, time.UTC)
	stub := &stubApplicationSignals{
		groupingPages: []*applicationsignals.ListGroupingAttributeDefinitionsOutput{{
			GroupingAttributeDefinitions: []applicationsignalstypes.GroupingAttributeDefinition{{
				GroupingName:       &gname,
				GroupingSourceKeys: []string{"team"},
			}},
			UpdatedAt: &updated,
		}},
	}
	total, inserted, err := scanApplicationSignalsGroupingConfiguration(context.Background(), stub, acct, region, st, testScanID)
	if err != nil {
		t.Fatalf("scanApplicationSignalsGroupingConfiguration: %v", err)
	}
	if total != 1 || inserted != 1 {
		t.Errorf("total=%d inserted=%d, want 1/1", total, inserted)
	}
	want := applicationSignalsGroupingConfigurationNativeID("aws", region, acct.ID)
	rows, err := st.ListResources(store.ResourceFilter{Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeApplicationSignalsGroupingConfiguration}, Limit: 100})
	if err != nil {
		t.Fatalf("ListResources: %v", err)
	}
	if len(rows) != 1 || rows[0].NativeID != want {
		t.Errorf("rows=%+v, want one row with NativeID %s", rows, want)
	}
}

func TestApplicationSignalsServiceNativeID_StableAcrossMapOrder(t *testing.T) {
	a := applicationSignalsServiceNativeID("aws", "us-east-1", "111111111111", map[string]string{"Type": "Service", "Name": "checkout", "Environment": "prod"})
	b := applicationSignalsServiceNativeID("aws", "us-east-1", "111111111111", map[string]string{"Name": "checkout", "Environment": "prod", "Type": "Service"})
	if a != b {
		t.Errorf("native id not stable across map order:\n a=%q\n b=%q", a, b)
	}
	want := "arn:aws:application-signals:us-east-1:111111111111:service/Environment=prod;Name=checkout;Type=Service"
	if a != want {
		t.Errorf("native id = %q, want %q", a, want)
	}
}

func TestScanApplicationSignalsServices_Populated(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	region := testRegion

	stub := &stubApplicationSignals{
		servicePages: []*applicationsignals.ListServicesOutput{{
			ServiceSummaries: []applicationsignalstypes.ServiceSummary{{
				KeyAttributes: map[string]string{"Type": "Service", "Name": "checkout", "Environment": "prod"},
			}},
		}},
	}
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	total, inserted, _, err := scanApplicationSignalsServices(context.Background(), stub, acct, region, st, testScanID, now)
	if err != nil {
		t.Fatalf("scanApplicationSignalsServices: %v", err)
	}
	if total != 1 || inserted != 1 {
		t.Errorf("total=%d inserted=%d, want 1/1", total, inserted)
	}
	rows, err := st.ListResources(store.ResourceFilter{Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeCloudWatchService}, Limit: 100})
	if err != nil {
		t.Fatalf("ListResources: %v", err)
	}
	want := applicationSignalsServiceNativeID("aws", region, acct.ID, map[string]string{"Type": "Service", "Name": "checkout", "Environment": "prod"})
	if len(rows) != 1 || rows[0].NativeID != want {
		t.Errorf("rows=%+v, want one row with NativeID %s", rows, want)
	}
}

func TestScanApplicationSignalsServices_Empty(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	stub := &stubApplicationSignals{}
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	total, inserted, _, err := scanApplicationSignalsServices(context.Background(), stub, acct, testRegion, st, testScanID, now)
	if err != nil {
		t.Fatalf("scanApplicationSignalsServices: %v", err)
	}
	if total != 0 || inserted != 0 {
		t.Errorf("total=%d inserted=%d, want 0/0 (no services)", total, inserted)
	}
}

func TestScanApplicationSignalsGroupingConfiguration_Empty(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	stub := &stubApplicationSignals{}
	total, inserted, err := scanApplicationSignalsGroupingConfiguration(context.Background(), stub, acct, testRegion, st, testScanID)
	if err != nil {
		t.Fatalf("scanApplicationSignalsGroupingConfiguration: %v", err)
	}
	if total != 0 || inserted != 0 {
		t.Errorf("total=%d inserted=%d, want 0/0 (skip empty)", total, inserted)
	}
	rows, err := st.ListResources(store.ResourceFilter{Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeApplicationSignalsGroupingConfiguration}, Limit: 100})
	if err != nil {
		t.Fatalf("ListResources: %v", err)
	}
	if len(rows) != 0 {
		t.Errorf("expected no rows; got %+v", rows)
	}
}

var appSignalsDenied = apiErr("AccessDeniedException", "User: arn:aws:iam::123:user/x is not authorized to perform: application-signals:List")

var appSignalsCreated = time.Date(2026, 9, 1, 8, 0, 0, 0, time.UTC)

func appSignalsServiceKeyAttrs(name string) map[string]string {
	return map[string]string{"Type": "Service", "Name": name, "Environment": "eks:prod/default"}
}

func appSignalsTestService(name string) applicationSignalsService {
	return applicationSignalsService{
		name: name, environment: "eks:prod/default",
		nativeID: applicationSignalsServiceNativeID("aws", testRegion, testAccountID, appSignalsServiceKeyAttrs(name)),
	}
}

func appSignalsConfigARN(service, loc string) string {
	return "arn:aws:application-signals:us-east-1:123456789012:instrumentationConfig/" + service + "/eks:prod/default/SNAPSHOT/" + loc
}

func appSignalsConfig(service, loc string) applicationsignalstypes.InstrumentationConfigurationWithoutServiceEnv {
	return applicationsignalstypes.InstrumentationConfigurationWithoutServiceEnv{
		ARN:                 sdkaws.String(appSignalsConfigARN(service, loc)),
		CreatedAt:           &appSignalsCreated,
		Description:         sdkaws.String("desc " + loc),
		InstrumentationType: applicationsignalstypes.InstrumentationTypeProbe,
		LocationHash:        sdkaws.String(loc),
		SignalType:          applicationsignalstypes.DynamicInstrumentationSignalTypeSnapshot,
		Location: &applicationsignalstypes.LocationMemberCodeLocation{Value: applicationsignalstypes.CodeLocation{
			FilePath: sdkaws.String("src/" + loc + ".py"),
		}},
		CaptureConfiguration: &applicationsignalstypes.CaptureConfigurationMemberCodeCapture{Value: applicationsignalstypes.CodeCaptureConfiguration{
			CaptureLimits: &applicationsignalstypes.CaptureLimitsConfig{MaxHits: sdkaws.Int32(7)},
		}},
	}
}

// appSignalsConfigHandler answers ListInstrumentationConfigurations with two
// PROBE configurations per service over two pages, recording each call as
// service/environment/type. A call without the model's page cap, or for
// BREAKPOINT, fails hard.
func appSignalsConfigHandler(calls *parentCalls) func(in any) (any, error) {
	return func(in any) (any, error) {
		req := in.(*applicationsignals.ListInstrumentationConfigurationsInput)
		svc := sdkaws.ToString(req.Service)
		calls.add(svc + "/" + sdkaws.ToString(req.Environment) + "/" + string(req.InstrumentationType))
		switch {
		case sdkaws.ToInt32(req.MaxResults) != 100:
			return nil, apiErr("UnexpectedMaxResults", "want 100")
		case req.InstrumentationType != applicationsignalstypes.InstrumentationTypeProbe:
			return nil, apiErr("UnexpectedInstrumentationType", "want PROBE")
		case req.NextToken == nil:
			return &applicationsignals.ListInstrumentationConfigurationsOutput{
				LatestConfigurations: []applicationsignalstypes.InstrumentationConfigurationWithoutServiceEnv{appSignalsConfig(svc, svc+"-probe1")},
				NextToken:            sdkaws.String("p2"),
			}, nil
		default:
			return &applicationsignals.ListInstrumentationConfigurationsOutput{
				LatestConfigurations: []applicationsignalstypes.InstrumentationConfigurationWithoutServiceEnv{appSignalsConfig(svc, svc+"-probe2")},
			}, nil
		}
	}
}

func appSignalsClient(handlers map[string]func(in any) (any, error)) *applicationsignals.Client {
	return applicationsignals.NewFromConfig(opStubConfig(handlers, appSignalsDenied))
}

// seedAppSignalsServices stores the service rows the configurations are
// linked under, as the services phase would.
func seedAppSignalsServices(t *testing.T, st *store.Store, services []applicationSignalsService) {
	t.Helper()
	for _, s := range services {
		upsertTestResource(t, st, "aws", testAccountID, TypeCloudWatchService, s.nativeID, testRegion, "{}")
	}
}

func TestScanApplicationSignalsInstrumentationConfigurations_ProbesOfEveryPage(t *testing.T) {
	st := newTestStore(t)
	services := []applicationSignalsService{appSignalsTestService("checkout"), appSignalsTestService("cart")}
	seedAppSignalsServices(t, st, services)
	var calls parentCalls
	client := appSignalsClient(map[string]func(any) (any, error){"ListInstrumentationConfigurations": appSignalsConfigHandler(&calls)})

	total, _, err := scanApplicationSignalsInstrumentationConfigurations(context.Background(), client, newTestAccount(testAccountID), testRegion, st, testScanID, services)
	if err != nil {
		t.Fatalf("err = %v; want nil", err)
	}
	wantCalls := []string{"cart/eks:prod/default/PROBE", "checkout/eks:prod/default/PROBE"}
	if got := calls.distinct(); !reflect.DeepEqual(got, wantCalls) {
		t.Errorf("calls = %v; want %v", got, wantCalls)
	}
	rows := rowsByNativeID(t, st, TypeApplicationSignalsInstrumentationConfiguration)
	if total != 4 || len(rows) != 4 {
		t.Fatalf("total, rows = %d, %d; want 4, 4", total, len(rows))
	}
	for _, s := range services {
		for _, loc := range []string{s.name + "-probe1", s.name + "-probe2"} {
			arn := appSignalsConfigARN(s.name, loc)
			r, ok := rows[arn]
			if !ok {
				t.Errorf("configuration %s not stored", arn)
				continue
			}
			if r.Type != TypeApplicationSignalsInstrumentationConfiguration || sv(r.Region) != testRegion ||
				sv(r.Name) != "desc "+loc || sv(r.CreatedAt) != "2026-09-01T08:00:00Z" {
				t.Errorf("row %s = type %q region %q name %q created %q", arn, r.Type, sv(r.Region), sv(r.Name), sv(r.CreatedAt))
			}
			if !strings.Contains(r.AttributesJSON, `"Location":{"CodeLocation":{"FilePath":"src/`+loc+`.py"`) ||
				!strings.Contains(r.AttributesJSON, `"CaptureConfiguration":{"CodeCapture":{"CaptureLimits":{`) {
				t.Errorf("attrs of %s = %s; want the union members labelled", arn, r.AttributesJSON)
			}
			assertContainsNative(t, st, s.nativeID, arn)
		}
	}
	assertContainsEdgesDeclared(t, st)
}

func TestScanApplicationSignalsInstrumentationConfigurations_Empty(t *testing.T) {
	st := newTestStore(t)
	services := []applicationSignalsService{appSignalsTestService("checkout")}
	seedAppSignalsServices(t, st, services)
	var calls parentCalls
	client := appSignalsClient(map[string]func(any) (any, error){
		"ListInstrumentationConfigurations": func(in any) (any, error) {
			calls.add(string(in.(*applicationsignals.ListInstrumentationConfigurationsInput).InstrumentationType))
			return &applicationsignals.ListInstrumentationConfigurationsOutput{}, nil
		},
	})
	acct := newTestAccount(testAccountID)

	total, inserted, err := scanApplicationSignalsInstrumentationConfigurations(context.Background(), client, acct, testRegion, st, testScanID, services)
	if err != nil || total != 0 || inserted != 0 {
		t.Errorf("total, inserted, err = %d, %d, %v; want 0, 0, nil", total, inserted, err)
	}
	if got := calls.distinct(); !reflect.DeepEqual(got, []string{"PROBE"}) {
		t.Errorf("types queried = %v; want [PROBE]", got)
	}
	total, _, err = scanApplicationSignalsInstrumentationConfigurations(context.Background(), client, acct, testRegion, st, testScanID, nil)
	if err != nil || total != 0 || len(calls.ids) != 1 {
		t.Errorf("no services: total, err, calls = %d, %v, %v; want 0, nil, no new calls", total, err, calls.ids)
	}
	if rows := rowsByNativeID(t, st, TypeApplicationSignalsInstrumentationConfiguration); len(rows) != 0 {
		t.Errorf("rows = %v; want none", rows)
	}
}

func TestScanApplicationSignalsInstrumentationConfigurations_EmptyARNSkipped(t *testing.T) {
	st := newTestStore(t)
	services := []applicationSignalsService{appSignalsTestService("checkout")}
	seedAppSignalsServices(t, st, services)
	noARN := appSignalsConfig("checkout", "no-arn")
	noARN.ARN = nil
	client := appSignalsClient(map[string]func(any) (any, error){
		"ListInstrumentationConfigurations": func(any) (any, error) {
			return &applicationsignals.ListInstrumentationConfigurationsOutput{
				LatestConfigurations: []applicationsignalstypes.InstrumentationConfigurationWithoutServiceEnv{noARN, appSignalsConfig("checkout", "kept")},
			}, nil
		},
	})

	_, _, err := scanApplicationSignalsInstrumentationConfigurations(context.Background(), client, newTestAccount(testAccountID), testRegion, st, testScanID, services)
	if err != nil {
		t.Fatalf("err = %v; want nil", err)
	}
	rows := rowsByNativeID(t, st, TypeApplicationSignalsInstrumentationConfiguration)
	if _, ok := rows[appSignalsConfigARN("checkout", "kept")]; !ok || len(rows) != 1 {
		t.Errorf("rows = %v; want only the configuration with an ARN", rows)
	}
}

// Two service rows sharing a name and environment (other KeyAttributes differ)
// are queried once, and each configuration is stored once under the first.
func TestScanApplicationSignalsInstrumentationConfigurations_SharedNameEnvironmentQueriedOnce(t *testing.T) {
	st := newTestStore(t)
	first := appSignalsTestService("checkout")
	attrs := appSignalsServiceKeyAttrs("checkout")
	attrs["AwsAccountId"] = "210987654321"
	second := first
	second.nativeID = applicationSignalsServiceNativeID("aws", testRegion, testAccountID, attrs)
	services := []applicationSignalsService{first, second}
	seedAppSignalsServices(t, st, services)
	var calls parentCalls
	client := appSignalsClient(map[string]func(any) (any, error){"ListInstrumentationConfigurations": appSignalsConfigHandler(&calls)})

	total, _, err := scanApplicationSignalsInstrumentationConfigurations(context.Background(), client, newTestAccount(testAccountID), testRegion, st, testScanID, services)
	if err != nil {
		t.Fatalf("err = %v; want nil", err)
	}
	if total != 2 || len(calls.ids) != 2 {
		t.Errorf("total, calls = %d, %v; want 2 configurations from one two-page listing", total, calls.ids)
	}
	arn := appSignalsConfigARN("checkout", "checkout-probe1")
	assertContainsNative(t, st, first.nativeID, arn)
	rels, err := st.RelationshipsFrom(store.ResourceID("aws", testAccountID, second.nativeID), store.RelContains)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 0 {
		t.Errorf("second service contains %v; want nothing", rels)
	}
}

// Per service error policy: cart fails with code, checkout succeeds.
func TestScanApplicationSignalsInstrumentationConfigurations_ErrorPolicy(t *testing.T) {
	cases := []struct {
		name      string
		code      string
		bothFail  bool
		wantWarns int
		wantHard  bool
	}{
		{name: "not found skipped silently", code: "ResourceNotFoundException"},
		{name: "op not deployed skipped silently", code: "UnknownOperationException"},
		{name: "validation warns once", code: "ValidationException", wantWarns: 1},
		{name: "transient warns once", code: "ThrottlingException", wantWarns: 1},
		{name: "denial warns once", code: "AccessDeniedException", bothFail: true, wantWarns: 1},
		{name: "other error fails the phase", code: "ConflictException", wantHard: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := countOpWarnings(st, appSignalsListInstrumentationConfigurationsOp)
			services := []applicationSignalsService{appSignalsTestService("checkout"), appSignalsTestService("cart")}
			seedAppSignalsServices(t, st, services)
			var calls parentCalls
			ok := appSignalsConfigHandler(&calls)
			client := appSignalsClient(map[string]func(any) (any, error){
				"ListInstrumentationConfigurations": func(in any) (any, error) {
					if tc.bothFail || sdkaws.ToString(in.(*applicationsignals.ListInstrumentationConfigurationsInput).Service) == "cart" {
						return nil, apiErr(tc.code, "refused")
					}
					return ok(in)
				},
			})

			_, _, err := scanApplicationSignalsInstrumentationConfigurations(context.Background(), client, newTestAccount(testAccountID), testRegion, st, testScanID, services)
			if tc.wantHard {
				if !isAPIErrorCode(err, tc.code) || classifyServiceError(err) != outcomeError {
					t.Errorf("err = %v; want a hard %s", err, tc.code)
				}
				return
			}
			if err != nil {
				t.Fatalf("err = %v; want nil", err)
			}
			if *warns != tc.wantWarns {
				t.Errorf("warnings = %d; want %d", *warns, tc.wantWarns)
			}
			wantRows := 2
			if tc.bothFail {
				wantRows = 0
			}
			rows := rowsByNativeID(t, st, TypeApplicationSignalsInstrumentationConfiguration)
			if len(rows) != wantRows {
				t.Errorf("rows = %d; want %d (checkout's configurations kept)", len(rows), wantRows)
			}
			for arn := range rows {
				if strings.Contains(arn, "/cart/") {
					t.Errorf("cart configuration %s stored despite its error", arn)
				}
			}
		})
	}
}

func TestScanApplicationSignalsServices_ReturnsStoredInstrumentedServices(t *testing.T) {
	st := newTestStore(t)
	client := appSignalsClient(map[string]func(any) (any, error){
		"ListServices": func(in any) (any, error) {
			if in.(*applicationsignals.ListServicesInput).NextToken != nil {
				return nil, apiErr("ConflictException", "page 2 fails")
			}
			return &applicationsignals.ListServicesOutput{
				ServiceSummaries: []applicationsignalstypes.ServiceSummary{
					{KeyAttributes: appSignalsServiceKeyAttrs("checkout")},
					{KeyAttributes: map[string]string{"Type": "RemoteService", "Name": "payments", "Environment": "eks:prod/default"}},
					{KeyAttributes: map[string]string{"Type": "AWS::Service", "Name": "AWS::DynamoDB", "Environment": "eks:prod/default"}},
					{KeyAttributes: map[string]string{"Type": "Service", "Name": "no-env"}},
				},
				NextToken: sdkaws.String("p2"),
			}, nil
		},
	})

	total, _, services, err := scanApplicationSignalsServices(context.Background(), client, newTestAccount(testAccountID), testRegion, st, testScanID, time.Now())
	if !isAPIErrorCode(err, "ConflictException") {
		t.Errorf("err = %v; want the page 2 ConflictException", err)
	}
	if total != 4 {
		t.Errorf("total = %d; want the 4 services of page 1", total)
	}
	want := []applicationSignalsService{appSignalsTestService("checkout")}
	if !reflect.DeepEqual(services, want) {
		t.Errorf("services = %+v; want %+v", services, want)
	}
}

// appSignalsTopLevelHandlers answers every op of the service scan: one SLO,
// no grouping configuration, the checkout and cart services, and their
// configurations.
func appSignalsTopLevelHandlers(calls *parentCalls) map[string]func(any) (any, error) {
	return map[string]func(any) (any, error){
		"ListServiceLevelObjectives": func(any) (any, error) {
			return &applicationsignals.ListServiceLevelObjectivesOutput{
				SloSummaries: []applicationsignalstypes.ServiceLevelObjectiveSummary{{
					Arn: sdkaws.String("arn:aws:application-signals:us-east-1:123456789012:slo/s1"), Name: sdkaws.String("s1"),
				}},
			}, nil
		},
		"ListGroupingAttributeDefinitions": func(any) (any, error) {
			return &applicationsignals.ListGroupingAttributeDefinitionsOutput{}, nil
		},
		"ListServices": func(any) (any, error) {
			return &applicationsignals.ListServicesOutput{
				ServiceSummaries: []applicationsignalstypes.ServiceSummary{
					{KeyAttributes: appSignalsServiceKeyAttrs("checkout")},
					{KeyAttributes: appSignalsServiceKeyAttrs("cart")},
				},
			}, nil
		},
		"ListInstrumentationConfigurations": appSignalsConfigHandler(calls),
	}
}

func TestScanApplicationSignals_ListsConfigurationsOfStoredServices(t *testing.T) {
	st := newTestStore(t)
	var calls parentCalls
	acct := &account{ID: testAccountID, Name: "Test Account", cfg: opStubConfig(appSignalsTopLevelHandlers(&calls), appSignalsDenied)}

	total, _, err := scanApplicationSignals(context.Background(), acct, testRegion, st, testScanID)
	if err != nil {
		t.Fatalf("err = %v; want nil", err)
	}
	if total != 7 {
		t.Errorf("total = %d; want 7 (1 SLO + 2 services + 4 configurations)", total)
	}
	for _, name := range []string{"checkout", "cart"} {
		assertContainsNative(t, st, appSignalsTestService(name).nativeID, appSignalsConfigARN(name, name+"-probe2"))
	}
}

// A hard configuration failure fails the service but keeps the rows of every
// earlier phase.
func TestScanApplicationSignals_ConfigurationFailureKeepsEarlierPhases(t *testing.T) {
	st := newTestStore(t)
	var calls parentCalls
	h := appSignalsTopLevelHandlers(&calls)
	h["ListInstrumentationConfigurations"] = func(any) (any, error) { return nil, apiErr("ConflictException", "bad") }
	acct := &account{ID: testAccountID, Name: "Test Account", cfg: opStubConfig(h, appSignalsDenied)}

	total, _, err := scanApplicationSignals(context.Background(), acct, testRegion, st, testScanID)
	if !isAPIErrorCode(err, "ConflictException") || classifyServiceError(err) != outcomeError {
		t.Errorf("err = %v; want a hard ConflictException", err)
	}
	if total != 3 || len(rowsByNativeID(t, st, TypeApplicationSignalsSLO)) != 1 || len(rowsByNativeID(t, st, TypeCloudWatchService)) != 2 {
		t.Errorf("total = %d; want the SLO and 2 services stored and counted", total)
	}
}

// A throttled services page is demoted to one warning, so a hard
// configuration error still classifies the service as failed, and the
// configurations of the services on the earlier page are queried.
func TestScanApplicationSignals_ServicesThrottleDemotedBesideHardConfigError(t *testing.T) {
	st := newTestStore(t)
	warns := countOpWarnings(st, appSignalsListServicesOp)
	var calls parentCalls
	h := appSignalsTopLevelHandlers(&calls)
	h["ListServices"] = func(in any) (any, error) {
		if in.(*applicationsignals.ListServicesInput).NextToken != nil {
			return nil, apiErr("ThrottlingException", "Rate exceeded")
		}
		return &applicationsignals.ListServicesOutput{
			ServiceSummaries: []applicationsignalstypes.ServiceSummary{{KeyAttributes: appSignalsServiceKeyAttrs("checkout")}},
			NextToken:        sdkaws.String("p2"),
		}, nil
	}
	h["ListInstrumentationConfigurations"] = func(in any) (any, error) {
		calls.add(sdkaws.ToString(in.(*applicationsignals.ListInstrumentationConfigurationsInput).Service))
		return nil, apiErr("ConflictException", "bad")
	}
	acct := &account{ID: testAccountID, Name: "Test Account", cfg: opStubConfig(h, appSignalsDenied)}

	_, _, err := scanApplicationSignals(context.Background(), acct, testRegion, st, testScanID)
	if classifyServiceError(err) != outcomeError || *warns != 1 {
		t.Errorf("outcome, warnings = %v, %d; want outcomeError, 1 (err %v)", classifyServiceError(err), *warns, err)
	}
	if got := calls.distinct(); !reflect.DeepEqual(got, []string{"checkout"}) {
		t.Errorf("services queried = %v; want [checkout]", got)
	}
}

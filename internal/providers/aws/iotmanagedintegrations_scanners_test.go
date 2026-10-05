package aws

import (
	"context"
	"encoding/json"
	"slices"
	"strconv"
	"sync"
	"testing"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/iotmanagedintegrations"
	imitypes "github.com/aws/aws-sdk-go-v2/service/iotmanagedintegrations/types"
	"github.com/icearp/disco-cli/store"
)

// stubIMI serves each account-wide List op from per-op pages (token = page
// index, see smPage); err is returned instead of page errPage of those ops.
// Thing links are served per AccountAssociationId; assocErrs[id] replaces
// page assocErrPage[id] of that association.
type stubIMI struct {
	iotManagedIntegrationsAPI
	t             *testing.T
	connectors    [][]imitypes.ConnectorItem
	connectorDsts [][]imitypes.ConnectorDestinationSummary
	destinations  [][]imitypes.DestinationSummary
	eventLogs     [][]imitypes.EventLogConfigurationSummary
	notifications [][]imitypes.NotificationConfigurationSummary
	otaConfigs    [][]imitypes.OtaTaskConfigurationSummary
	err           error
	errPage       int

	assocLinks   map[string][][]imitypes.ManagedThingAssociation
	assocErrs    map[string]error
	assocErrPage map[string]int
	mu           sync.Mutex
	queried      []string
}

func imiStubPage[T any](s *stubIMI, pages [][]T, token *string) ([]T, *string, error) {
	s.t.Helper()
	page := 0
	if token != nil {
		var err error
		if page, err = strconv.Atoi(*token); err != nil {
			s.t.Fatalf("stub page token %q: %v", *token, err)
		}
	}
	if s.err != nil && page == s.errPage {
		return nil, nil, s.err
	}
	return smPage(pages, token)
}

func (s *stubIMI) ListCloudConnectors(_ context.Context, in *iotmanagedintegrations.ListCloudConnectorsInput, _ ...func(*iotmanagedintegrations.Options)) (*iotmanagedintegrations.ListCloudConnectorsOutput, error) {
	items, next, err := imiStubPage(s, s.connectors, in.NextToken)
	if err != nil {
		return nil, err
	}
	return &iotmanagedintegrations.ListCloudConnectorsOutput{Items: items, NextToken: next}, nil
}

func (s *stubIMI) ListConnectorDestinations(_ context.Context, in *iotmanagedintegrations.ListConnectorDestinationsInput, _ ...func(*iotmanagedintegrations.Options)) (*iotmanagedintegrations.ListConnectorDestinationsOutput, error) {
	items, next, err := imiStubPage(s, s.connectorDsts, in.NextToken)
	if err != nil {
		return nil, err
	}
	return &iotmanagedintegrations.ListConnectorDestinationsOutput{ConnectorDestinationList: items, NextToken: next}, nil
}

func (s *stubIMI) ListDestinations(_ context.Context, in *iotmanagedintegrations.ListDestinationsInput, _ ...func(*iotmanagedintegrations.Options)) (*iotmanagedintegrations.ListDestinationsOutput, error) {
	items, next, err := imiStubPage(s, s.destinations, in.NextToken)
	if err != nil {
		return nil, err
	}
	return &iotmanagedintegrations.ListDestinationsOutput{DestinationList: items, NextToken: next}, nil
}

func (s *stubIMI) ListEventLogConfigurations(_ context.Context, in *iotmanagedintegrations.ListEventLogConfigurationsInput, _ ...func(*iotmanagedintegrations.Options)) (*iotmanagedintegrations.ListEventLogConfigurationsOutput, error) {
	items, next, err := imiStubPage(s, s.eventLogs, in.NextToken)
	if err != nil {
		return nil, err
	}
	return &iotmanagedintegrations.ListEventLogConfigurationsOutput{EventLogConfigurationList: items, NextToken: next}, nil
}

func (s *stubIMI) ListNotificationConfigurations(_ context.Context, in *iotmanagedintegrations.ListNotificationConfigurationsInput, _ ...func(*iotmanagedintegrations.Options)) (*iotmanagedintegrations.ListNotificationConfigurationsOutput, error) {
	items, next, err := imiStubPage(s, s.notifications, in.NextToken)
	if err != nil {
		return nil, err
	}
	return &iotmanagedintegrations.ListNotificationConfigurationsOutput{NotificationConfigurationList: items, NextToken: next}, nil
}

func (s *stubIMI) ListOtaTaskConfigurations(_ context.Context, in *iotmanagedintegrations.ListOtaTaskConfigurationsInput, _ ...func(*iotmanagedintegrations.Options)) (*iotmanagedintegrations.ListOtaTaskConfigurationsOutput, error) {
	items, next, err := imiStubPage(s, s.otaConfigs, in.NextToken)
	if err != nil {
		return nil, err
	}
	return &iotmanagedintegrations.ListOtaTaskConfigurationsOutput{Items: items, NextToken: next}, nil
}

func (s *stubIMI) ListManagedThingAccountAssociations(_ context.Context, in *iotmanagedintegrations.ListManagedThingAccountAssociationsInput, _ ...func(*iotmanagedintegrations.Options)) (*iotmanagedintegrations.ListManagedThingAccountAssociationsOutput, error) {
	assoc := sv(in.AccountAssociationId)
	s.mu.Lock()
	s.queried = append(s.queried, assoc)
	s.mu.Unlock()
	page := 0
	if in.NextToken != nil {
		var err error
		if page, err = strconv.Atoi(*in.NextToken); err != nil {
			s.t.Errorf("stub page token %q: %v", *in.NextToken, err)
			return nil, err
		}
	}
	if err := s.assocErrs[assoc]; err != nil && page == s.assocErrPage[assoc] {
		return nil, err
	}
	items, next, err := smPage(s.assocLinks[assoc], in.NextToken)
	if err != nil {
		s.t.Errorf("stub page: %v", err)
		return nil, err
	}
	return &iotmanagedintegrations.ListManagedThingAccountAssociationsOutput{Items: items, NextToken: next}, nil
}

type imiScanFn func(context.Context, iotManagedIntegrationsAPI, *account, string, *store.Store, string) (int, int, error)

func imiNID(kind, id string) string { return imiARN(testRegion, testAccountID, kind, id) }

// imiCase describes one account-wide sub-scanner: fill installs two pages
// (page 1 carries a valid item plus one lacking its identity field, page 2 one
// valid item); want are the NativeIDs those pages must store; wantName/attrKey
// pin the first stored row.
type imiCase struct {
	name     string
	scan     imiScanFn
	rtype    string
	fill     func(*stubIMI)
	want     []string
	wantName string
	attrKey  string
}

func imiCases() []imiCase {
	s := sdkaws.String
	return []imiCase{
		{
			name: "cloud connectors", scan: scanIMICloudConnectors, rtype: TypeIoTManagedIntegrationsCloudConnector,
			fill: func(st *stubIMI) {
				st.connectors = [][]imitypes.ConnectorItem{
					{{Id: s("cc-1"), Name: s("conn one"), EndpointConfig: &imitypes.EndpointConfig{Lambda: &imitypes.LambdaConfig{Arn: s("arn:aws:lambda:us-east-1:123456789012:function:f")}}}, {Name: s("no id")}},
					{{Id: s("cc-2"), Name: s("conn two")}},
				}
			},
			want:     []string{imiNID("cloud-connector", "cc-1"), imiNID("cloud-connector", "cc-2")},
			wantName: "conn one", attrKey: "EndpointConfig",
		},
		{
			name: "connector destinations", scan: scanIMIConnectorDestinations, rtype: TypeIoTManagedIntegrationsConnectorDestination,
			fill: func(st *stubIMI) {
				st.connectorDsts = [][]imitypes.ConnectorDestinationSummary{
					{{Id: s("cd-1"), CloudConnectorId: s("cc-1"), Name: s("dest one")}, {CloudConnectorId: s("cc-1"), Name: s("no id")}},
					{{Id: s("cd-2"), Name: s("dest two")}},
				}
			},
			want:     []string{imiNID("connector-destination", "cd-1"), imiNID("connector-destination", "cd-2")},
			wantName: "dest one", attrKey: "CloudConnectorId",
		},
		{
			name: "destinations", scan: scanIMIDestinations, rtype: TypeIoTManagedIntegrationsDestination,
			fill: func(st *stubIMI) {
				st.destinations = [][]imitypes.DestinationSummary{
					{{Name: s("d1"), DeliveryDestinationArn: s("arn:aws:kinesis:us-east-1:123456789012:stream/s")}, {Description: s("no name")}},
					{{Name: s("d2")}},
				}
			},
			want:     []string{imiNID("destination", "d1"), imiNID("destination", "d2")},
			wantName: "d1", attrKey: "DeliveryDestinationArn",
		},
		{
			name: "event log configurations", scan: scanIMIEventLogConfigurations, rtype: TypeIoTManagedIntegrationsEventLogConfiguration,
			fill: func(st *stubIMI) {
				st.eventLogs = [][]imitypes.EventLogConfigurationSummary{
					{{Id: s("el-1"), ResourceType: s("ManagedThing"), EventLogLevel: imitypes.LogLevelError}, {ResourceType: s("no id")}},
					{{Id: s("el-2")}},
				}
			},
			want:    []string{imiNID("event-log-configuration", "el-1"), imiNID("event-log-configuration", "el-2")},
			attrKey: "EventLogLevel",
		},
		{
			name: "notification configurations", scan: scanIMINotificationConfigurations, rtype: TypeIoTManagedIntegrationsNotificationConfiguration,
			fill: func(st *stubIMI) {
				st.notifications = [][]imitypes.NotificationConfigurationSummary{
					{{EventType: imitypes.EventTypeDeviceEvent, DestinationName: s("d1")}, {DestinationName: s("no event type")}},
					{{EventType: imitypes.EventTypeDeviceState}},
				}
			},
			want: []string{
				imiNID("notification-configuration", string(imitypes.EventTypeDeviceEvent)),
				imiNID("notification-configuration", string(imitypes.EventTypeDeviceState)),
			},
			wantName: string(imitypes.EventTypeDeviceEvent), attrKey: "DestinationName",
		},
		{
			name: "ota task configurations", scan: scanIMIOtaTaskConfigurations, rtype: TypeIoTManagedIntegrationsOtaTaskConfiguration,
			fill: func(st *stubIMI) {
				st.otaConfigs = [][]imitypes.OtaTaskConfigurationSummary{
					{{TaskConfigurationId: s("otc-1"), Name: s("cfg one")}, {Name: s("no id")}},
					{{TaskConfigurationId: s("otc-2")}},
				}
			},
			want:     []string{imiNID("ota-task-configuration", "otc-1"), imiNID("ota-task-configuration", "otc-2")},
			wantName: "cfg one", attrKey: "TaskConfigurationId",
		},
	}
}

func TestScanIMIList_TwoPagesStored(t *testing.T) {
	for _, tc := range imiCases() {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			stub := &stubIMI{t: t}
			tc.fill(stub)
			total, _, err := tc.scan(context.Background(), stub, newTestAccount(testAccountID), testRegion, st, testScanID)
			if err != nil {
				t.Fatalf("scan: %v", err)
			}
			if total != len(tc.want) {
				t.Errorf("total = %d; want %d", total, len(tc.want))
			}
			assertSMIDs(t, st, tc.rtype, tc.want...)
			r := smStoredRows(t, st, tc.rtype)[tc.want[0]]
			if got := sv(r.Name); got != tc.wantName {
				t.Errorf("name = %q; want %q", got, tc.wantName)
			}
			var attrs map[string]any
			if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
				t.Fatalf("attrs: %v", err)
			}
			if _, ok := attrs[tc.attrKey]; !ok {
				t.Errorf("attrs missing %s: %s", tc.attrKey, r.AttributesJSON)
			}
		})
	}
}

func TestScanIMIList_Empty(t *testing.T) {
	for _, tc := range imiCases() {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			total, _, err := tc.scan(context.Background(), &stubIMI{t: t}, newTestAccount(testAccountID), testRegion, st, testScanID)
			if err != nil || total != 0 {
				t.Fatalf("scan = (%d, %v); want (0, nil)", total, err)
			}
			assertSMIDs(t, st, tc.rtype)
		})
	}
}

func TestScanIMIList_AccessDeniedWarnsOnce(t *testing.T) {
	for _, tc := range imiCases() {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			warnings := countSMWarnings(st)
			stub := &stubIMI{t: t, err: apiErr("AccessDeniedException", "User: x is not authorized to perform: y")}
			tc.fill(stub)
			if _, _, err := tc.scan(context.Background(), stub, newTestAccount(testAccountID), testRegion, st, testScanID); err != nil {
				t.Fatalf("scan: %v", err)
			}
			if *warnings != 1 {
				t.Errorf("warnings = %d; want 1", *warnings)
			}
			assertSMIDs(t, st, tc.rtype)
		})
	}
}

func TestScanIMIList_OtherErrorPropagates(t *testing.T) {
	for _, tc := range imiCases() {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			stub := &stubIMI{t: t, err: apiErr("InternalServerException", "boom")}
			_, _, err := tc.scan(context.Background(), stub, newTestAccount(testAccountID), testRegion, st, testScanID)
			if !isAPIErrorCode(err, "InternalServerException") {
				t.Fatalf("err = %v; want wrapped InternalServerException", err)
			}
		})
	}
}

// A denial on a later page keeps the rows from pages already read.
func TestScanIMIList_DeniedOnSecondPageKeepsFirst(t *testing.T) {
	st := newTestStore(t)
	warnings := countSMWarnings(st)
	stub := &stubIMI{
		t:            t,
		destinations: [][]imitypes.DestinationSummary{{{Name: sdkaws.String("d1")}}, {{Name: sdkaws.String("d2")}}},
		err:          apiErr("AccessDeniedException", "denied"),
		errPage:      1,
	}
	total, _, err := scanIMIDestinations(context.Background(), stub, newTestAccount(testAccountID), testRegion, st, testScanID)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != 1 {
		t.Errorf("total = %d; want 1", total)
	}
	assertSMIDs(t, st, TypeIoTManagedIntegrationsDestination, imiNID("destination", "d1"))
	if *warnings != 1 {
		t.Errorf("warnings = %d; want 1", *warnings)
	}
}

// A destination without a CloudConnectorId is stored but contained by nothing,
// and no hierarchy pair is attempted for it (a pair to an absent parent warns).
func TestScanIMIConnectorDestinations_NoConnectorNoEdge(t *testing.T) {
	st := newTestStore(t)
	warnings := countSMWarnings(st)
	connector := imiNID("cloud-connector", "cc-1")
	upsertTestResource(t, st, "aws", testAccountID, TypeIoTManagedIntegrationsCloudConnector, connector, testRegion, "{}")
	stub := &stubIMI{t: t, connectorDsts: [][]imitypes.ConnectorDestinationSummary{{
		{Id: sdkaws.String("cd-1"), CloudConnectorId: sdkaws.String("cc-1")},
		{Id: sdkaws.String("cd-2")},
	}}}
	if _, _, err := scanIMIConnectorDestinations(context.Background(), stub, newTestAccount(testAccountID), testRegion, st, testScanID); err != nil {
		t.Fatalf("scan: %v", err)
	}
	linked, orphan := imiNID("connector-destination", "cd-1"), imiNID("connector-destination", "cd-2")
	assertSMIDs(t, st, TypeIoTManagedIntegrationsConnectorDestination, linked, orphan)
	connectorID := store.ResourceID("aws", testAccountID, connector)
	assertSMContains(t, st, connectorID, linked)
	rels, err := st.RelationshipsFrom(connectorID, store.RelContains)
	if err != nil {
		t.Fatalf("relationships: %v", err)
	}
	if len(rels) != 1 {
		t.Errorf("connector contains %d rows; want 1 (%s only)", len(rels), linked)
	}
	for _, id := range []string{linked, orphan} {
		in, err := st.RelationshipsTo(store.ResourceID("aws", testAccountID, id), store.RelContains)
		if err != nil {
			t.Fatalf("relationships to %s: %v", id, err)
		}
		want := 0
		if id == linked {
			want = 1
		}
		if len(in) != want {
			t.Errorf("%s has %d contains parents; want %d", id, len(in), want)
		}
	}
	if *warnings != 0 {
		t.Errorf("warnings = %d; want 0", *warnings)
	}
}

func imiThingARNs(ids ...string) map[string]string {
	out := make(map[string]string, len(ids))
	for _, id := range ids {
		out[id] = imiNID("managed-thing", id)
	}
	return out
}

func seedIMIThings(t *testing.T, st *store.Store, thingARNs map[string]string) {
	t.Helper()
	for _, arn := range thingARNs {
		upsertTestResource(t, st, "aws", testAccountID, TypeIoTManagedIntegrationsManagedThing, arn, testRegion, "{}")
	}
}

func imiLinkNID(thing, assoc string) string {
	return imiNID("managed-thing", thing) + "/account-association/" + assoc
}

// Every account association is queried (both pages), each link is keyed and
// contained under the thing its ManagedThingId names, and links whose thing is
// missing or was not stored are skipped without a warning.
func TestScanIMIManagedThingAccountAssociations_FanOut(t *testing.T) {
	st := newTestStore(t)
	warnings := countSMWarnings(st)
	s := sdkaws.String
	things := imiThingARNs("mt-1", "mt-2")
	seedIMIThings(t, st, things)
	stub := &stubIMI{t: t, assocLinks: map[string][][]imitypes.ManagedThingAssociation{
		"aa-1": {
			{{ManagedThingId: s("mt-1"), AccountAssociationId: s("aa-1"), ManagedThingAssociationStatus: imitypes.ManagedThingAssociationStatusAssociated}, {AccountAssociationId: s("aa-1")}},
			{{ManagedThingId: s("mt-2"), AccountAssociationId: s("aa-1")}},
		},
		"aa-2": {{{ManagedThingId: s("mt-1"), AccountAssociationId: s("aa-2")}, {ManagedThingId: s("mt-unscanned"), AccountAssociationId: s("aa-2")}, {ManagedThingId: s("mt-2"), AccountAssociationId: s("aa-other")}}},
	}}
	total, _, err := scanIMIManagedThingAccountAssociations(context.Background(), stub, newTestAccount(testAccountID), testRegion, st, testScanID, []string{"aa-1", "aa-2"}, things)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	a1, b1, a2 := imiLinkNID("mt-1", "aa-1"), imiLinkNID("mt-2", "aa-1"), imiLinkNID("mt-1", "aa-2")
	if total != 3 {
		t.Errorf("total = %d; want 3", total)
	}
	assertSMIDs(t, st, TypeIoTManagedIntegrationsManagedThingAccountAssociation, a1, b1, a2)
	slices.Sort(stub.queried)
	if want := []string{"aa-1", "aa-1", "aa-2"}; !slices.Equal(stub.queried, want) {
		t.Errorf("queried = %v; want %v (two pages of aa-1, one of aa-2)", stub.queried, want)
	}
	rows := smStoredRows(t, st, TypeIoTManagedIntegrationsManagedThingAccountAssociation)
	if got := sv(rows[a1].Status); got != string(imitypes.ManagedThingAssociationStatusAssociated) {
		t.Errorf("%s status = %q; want ASSOCIATED", a1, got)
	}
	if rows[b1].Status != nil {
		t.Errorf("%s status = %q; want nil", b1, *rows[b1].Status)
	}
	assertSMContains(t, st, store.ResourceID("aws", testAccountID, things["mt-1"]), a1)
	assertSMContains(t, st, store.ResourceID("aws", testAccountID, things["mt-2"]), b1)
	assertSMContains(t, st, store.ResourceID("aws", testAccountID, things["mt-1"]), a2)
	if *warnings != 0 {
		t.Errorf("warnings = %d; want 0", *warnings)
	}
}

// With no stored things (e.g. ListManagedThings denied) no link can be keyed:
// no rows, no error, and no calls.
func TestScanIMIManagedThingAccountAssociations_NoThingsNoRows(t *testing.T) {
	st := newTestStore(t)
	stub := &stubIMI{t: t}
	total, _, err := scanIMIManagedThingAccountAssociations(context.Background(), stub, newTestAccount(testAccountID), testRegion, st, testScanID, []string{"aa-1"}, map[string]string{})
	if err != nil || total != 0 {
		t.Fatalf("scan = (%d, %v); want (0, nil)", total, err)
	}
	if len(stub.queried) != 0 {
		t.Errorf("queried = %v; want none", stub.queried)
	}
}

func TestScanIMIManagedThingAccountAssociations_NoAssociationsNoCalls(t *testing.T) {
	st := newTestStore(t)
	stub := &stubIMI{t: t}
	total, _, err := scanIMIManagedThingAccountAssociations(context.Background(), stub, newTestAccount(testAccountID), testRegion, st, testScanID, nil, imiThingARNs("mt-1"))
	if err != nil || total != 0 {
		t.Fatalf("scan = (%d, %v); want (0, nil)", total, err)
	}
	if len(stub.queried) != 0 {
		t.Errorf("queried = %v; want none", stub.queried)
	}
}

// Denied associations warn once in total; the remaining ones are still stored.
func TestScanIMIManagedThingAccountAssociations_DeniedWarnsOnceKeepsSiblings(t *testing.T) {
	st := newTestStore(t)
	warnings := countSMWarnings(st)
	things := imiThingARNs("mt-1")
	seedIMIThings(t, st, things)
	denied := apiErr("AccessDeniedException", "User: x is not authorized to perform: y")
	stub := &stubIMI{
		t:          t,
		assocLinks: map[string][][]imitypes.ManagedThingAssociation{"ok": {{{ManagedThingId: sdkaws.String("mt-1")}}}},
		assocErrs:  map[string]error{"denied-1": denied, "denied-2": denied},
	}
	if _, _, err := scanIMIManagedThingAccountAssociations(context.Background(), stub, newTestAccount(testAccountID), testRegion, st, testScanID, []string{"denied-1", "ok", "denied-2"}, things); err != nil {
		t.Fatalf("scan: %v", err)
	}
	assertSMIDs(t, st, TypeIoTManagedIntegrationsManagedThingAccountAssociation, imiLinkNID("mt-1", "ok"))
	if *warnings != 1 {
		t.Errorf("warnings = %d; want 1", *warnings)
	}
}

// A denial on an association's second page keeps its first page and siblings.
func TestScanIMIManagedThingAccountAssociations_DeniedOnSecondPageKeepsFirst(t *testing.T) {
	st := newTestStore(t)
	warnings := countSMWarnings(st)
	things := imiThingARNs("mt-1", "mt-2")
	seedIMIThings(t, st, things)
	s := sdkaws.String
	stub := &stubIMI{
		t: t,
		assocLinks: map[string][][]imitypes.ManagedThingAssociation{
			"aa-1": {{{ManagedThingId: s("mt-1")}}, {{ManagedThingId: s("mt-2")}}},
			"aa-2": {{{ManagedThingId: s("mt-2")}}},
		},
		assocErrs:    map[string]error{"aa-1": apiErr("AccessDeniedException", "denied")},
		assocErrPage: map[string]int{"aa-1": 1},
	}
	if _, _, err := scanIMIManagedThingAccountAssociations(context.Background(), stub, newTestAccount(testAccountID), testRegion, st, testScanID, []string{"aa-1", "aa-2"}, things); err != nil {
		t.Fatalf("scan: %v", err)
	}
	assertSMIDs(t, st, TypeIoTManagedIntegrationsManagedThingAccountAssociation, imiLinkNID("mt-1", "aa-1"), imiLinkNID("mt-2", "aa-2"))
	if *warnings != 1 {
		t.Errorf("warnings = %d; want 1", *warnings)
	}
}

func TestScanIMIManagedThingAccountAssociations_OtherErrorPropagates(t *testing.T) {
	st := newTestStore(t)
	stub := &stubIMI{t: t, assocErrs: map[string]error{"aa-1": apiErr("ValidationException", "bad")}}
	_, _, err := scanIMIManagedThingAccountAssociations(context.Background(), stub, newTestAccount(testAccountID), testRegion, st, testScanID, []string{"aa-1"}, imiThingARNs("mt-1"))
	if !isAPIErrorCode(err, "ValidationException") {
		t.Fatalf("err = %v; want wrapped ValidationException", err)
	}
}

// TestScanIoTManagedIntegrations_ChildrenContainedByParents runs the whole
// service against the real SDK client: children are contained by parents
// stored earlier in the same scan.
func TestScanIoTManagedIntegrations_ChildrenContainedByParents(t *testing.T) {
	st := newTestStore(t)
	s := sdkaws.String
	thingARN := imiNID("managed-thing", "mt-1")
	connector := imiNID("cloud-connector", "cc-1")
	one := func(out any) []stubCall { return []stubCall{{Output: out}} }
	stub := stubResponses(t, map[string][]stubCall{
		"ListAccountAssociations": one(&iotmanagedintegrations.ListAccountAssociationsOutput{Items: []imitypes.AccountAssociationItem{{
			Arn: s("arn:aws:iotmanagedintegrations:us-east-1:123456789012:account-association/aa-1"), AccountAssociationId: s("aa-1"),
		}}}),
		"ListCredentialLockers":    one(&iotmanagedintegrations.ListCredentialLockersOutput{}),
		"ListManagedThings":        one(&iotmanagedintegrations.ListManagedThingsOutput{Items: []imitypes.ManagedThingSummary{{Arn: s(thingARN), Id: s("mt-1")}}}),
		"ListOtaTasks":             one(&iotmanagedintegrations.ListOtaTasksOutput{}),
		"ListProvisioningProfiles": one(&iotmanagedintegrations.ListProvisioningProfilesOutput{}),
		"ListCloudConnectors":      one(&iotmanagedintegrations.ListCloudConnectorsOutput{Items: []imitypes.ConnectorItem{{Id: s("cc-1"), Name: s("c")}}}),
		"ListConnectorDestinations": one(&iotmanagedintegrations.ListConnectorDestinationsOutput{
			ConnectorDestinationList: []imitypes.ConnectorDestinationSummary{{Id: s("cd-1"), CloudConnectorId: s("cc-1")}},
		}),
		"ListDestinations":               one(&iotmanagedintegrations.ListDestinationsOutput{DestinationList: []imitypes.DestinationSummary{{Name: s("d1")}}}),
		"ListEventLogConfigurations":     one(&iotmanagedintegrations.ListEventLogConfigurationsOutput{EventLogConfigurationList: []imitypes.EventLogConfigurationSummary{{Id: s("el-1")}}}),
		"ListNotificationConfigurations": one(&iotmanagedintegrations.ListNotificationConfigurationsOutput{NotificationConfigurationList: []imitypes.NotificationConfigurationSummary{{EventType: imitypes.EventTypeDeviceEvent}}}),
		"ListOtaTaskConfigurations":      one(&iotmanagedintegrations.ListOtaTaskConfigurationsOutput{Items: []imitypes.OtaTaskConfigurationSummary{{TaskConfigurationId: s("otc-1")}}}),
		"ListManagedThingAccountAssociations": one(&iotmanagedintegrations.ListManagedThingAccountAssociationsOutput{
			Items: []imitypes.ManagedThingAssociation{{ManagedThingId: s("mt-1"), AccountAssociationId: s("aa-1")}},
		}),
	})
	acct := newTestAccount(testAccountID)
	acct.cfg = cloud9CfgWithStub(stub, testRegion)
	if _, _, err := scanIoTManagedIntegrations(context.Background(), acct, testRegion, st, testScanID); err != nil {
		t.Fatalf("scan: %v", err)
	}
	assertSMIDs(t, st, TypeIoTManagedIntegrationsDestination, imiNID("destination", "d1"))
	assertSMIDs(t, st, TypeIoTManagedIntegrationsEventLogConfiguration, imiNID("event-log-configuration", "el-1"))
	assertSMIDs(t, st, TypeIoTManagedIntegrationsNotificationConfiguration, imiNID("notification-configuration", string(imitypes.EventTypeDeviceEvent)))
	assertSMIDs(t, st, TypeIoTManagedIntegrationsOtaTaskConfiguration, imiNID("ota-task-configuration", "otc-1"))
	assertSMContains(t, st, store.ResourceID("aws", testAccountID, connector), imiNID("connector-destination", "cd-1"))
	assertSMContains(t, st, store.ResourceID("aws", testAccountID, thingARN), thingARN+"/account-association/aa-1")
}

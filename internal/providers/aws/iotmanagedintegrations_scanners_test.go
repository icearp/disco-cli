package aws

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/iotmanagedintegrations"
	imitypes "github.com/aws/aws-sdk-go-v2/service/iotmanagedintegrations/types"
	"github.com/icearp/disco-cli/store"
)

// stubIMI serves each account-wide List op from per-op pages (token = page
// index, see smPage); err is returned instead of page errPage of those ops.
// Thing links are served per AccountAssociationId; assocErrs[id] replaces
// page assocErrPage[id] of that association; linkCaps records the MaxResults
// each link listing sent.
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
	linkCaps     []int32
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
	if in.MaxResults != nil {
		s.linkCaps = append(s.linkCaps, *in.MaxResults)
	}
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

func imiNID(kind, id string) string { return imiARN("aws", testRegion, testAccountID, kind, id) }

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

// imiParents is one parent per id, its ARN of kind.
func imiParents(kind string, ids ...string) []childParent {
	out := make([]childParent, len(ids))
	for i, id := range ids {
		out[i] = childParent{id: id, arn: imiNID(kind, id)}
	}
	return out
}

func seedIMIThings(t *testing.T, st *store.Store, things []childParent) {
	t.Helper()
	for _, th := range things {
		upsertTestResource(t, st, "aws", testAccountID, TypeIoTManagedIntegrationsManagedThing, th.arn, testRegion, "{}")
	}
}

func imiLinkNID(thing, assoc string) string {
	return imiNID("managed-thing", thing) + "/account-association/" + assoc
}

// Every account association is queried (both pages), each asking for the
// model's page cap of 1000, and each link is keyed and contained under the
// thing its ManagedThingId names. Links whose thing is missing or was not
// stored, and a link the filter let through for another association, are not
// stored and are warned about once per reason under the op.
func TestScanIMIManagedThingAccountAssociations_FanOut(t *testing.T) {
	st := newTestStore(t)
	warnings := bacWarnings(st, imiThingLinksOp)
	s := sdkaws.String
	things := imiParents("managed-thing", "mt-1", "mt-2")
	seedIMIThings(t, st, things)
	a1Item := imitypes.ManagedThingAssociation{ManagedThingId: s("mt-1"), AccountAssociationId: s("aa-1"), ManagedThingAssociationStatus: imitypes.ManagedThingAssociationStatusAssociated}
	stub := &stubIMI{t: t, assocLinks: map[string][][]imitypes.ManagedThingAssociation{
		"aa-1": {
			{a1Item, {AccountAssociationId: s("aa-1")}},
			{{ManagedThingId: s("mt-2"), AccountAssociationId: s("aa-1")}},
		},
		"aa-2": {{{ManagedThingId: s("mt-1"), AccountAssociationId: s("aa-2")}, {ManagedThingId: s("mt-unscanned"), AccountAssociationId: s("aa-2")}, {ManagedThingId: s("mt-2"), AccountAssociationId: s("aa-other")}}},
	}}
	total, _, err := scanIMIManagedThingAccountAssociations(context.Background(), stub, newTestAccount(testAccountID), testRegion, st, testScanID, imiParents("account-association", "aa-1", "aa-2"), things)
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
	if want := []int32{1000, 1000, 1000}; !slices.Equal(stub.linkCaps, want) {
		t.Errorf("MaxResults = %v; want %v", stub.linkCaps, want)
	}
	rows := smStoredRows(t, st, TypeIoTManagedIntegrationsManagedThingAccountAssociation)
	if got := sv(rows[a1].Status); got != string(imitypes.ManagedThingAssociationStatusAssociated) {
		t.Errorf("%s status = %q; want ASSOCIATED", a1, got)
	}
	if rows[b1].Status != nil {
		t.Errorf("%s status = %q; want nil", b1, *rows[b1].Status)
	}
	if got := rows[a1]; got.AttributesJSON != mustJSON(a1Item) || sv(got.Region) != testRegion || sv(got.AccountName) != newTestAccount(testAccountID).Name {
		t.Errorf("%s attributes, region, account name = %s, %v, %v; want %s, %s, %s", a1, got.AttributesJSON, got.Region, got.AccountName, mustJSON(a1Item), testRegion, newTestAccount(testAccountID).Name)
	}
	assertSMContains(t, st, store.ResourceID("aws", testAccountID, things[0].arn), a1)
	assertSMContains(t, st, store.ResourceID("aws", testAccountID, things[1].arn), b1)
	assertSMContains(t, st, store.ResourceID("aws", testAccountID, things[0].arn), a2)
	if len(*warnings) != 2 {
		t.Errorf("%s warnings = %v; want 2, one per reason", imiThingLinksOp, *warnings)
	}
}

// With no stored things (e.g. ListManagedThings denied) no link can be keyed:
// no rows, no error, and no calls.
func TestScanIMIManagedThingAccountAssociations_NoThingsNoRows(t *testing.T) {
	st := newTestStore(t)
	stub := &stubIMI{t: t}
	total, _, err := scanIMIManagedThingAccountAssociations(context.Background(), stub, newTestAccount(testAccountID), testRegion, st, testScanID, imiParents("account-association", "aa-1"), nil)
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
	total, _, err := scanIMIManagedThingAccountAssociations(context.Background(), stub, newTestAccount(testAccountID), testRegion, st, testScanID, nil, imiParents("managed-thing", "mt-1"))
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
	things := imiParents("managed-thing", "mt-1")
	seedIMIThings(t, st, things)
	denied := apiErr("AccessDeniedException", "User: x is not authorized to perform: y")
	stub := &stubIMI{
		t:          t,
		assocLinks: map[string][][]imitypes.ManagedThingAssociation{"ok": {{{ManagedThingId: sdkaws.String("mt-1")}}}},
		assocErrs:  map[string]error{"denied-1": denied, "denied-2": denied},
	}
	if _, _, err := scanIMIManagedThingAccountAssociations(context.Background(), stub, newTestAccount(testAccountID), testRegion, st, testScanID, imiParents("account-association", "denied-1", "ok", "denied-2"), things); err != nil {
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
	things := imiParents("managed-thing", "mt-1", "mt-2")
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
	if _, _, err := scanIMIManagedThingAccountAssociations(context.Background(), stub, newTestAccount(testAccountID), testRegion, st, testScanID, imiParents("account-association", "aa-1", "aa-2"), things); err != nil {
		t.Fatalf("scan: %v", err)
	}
	assertSMIDs(t, st, TypeIoTManagedIntegrationsManagedThingAccountAssociation, imiLinkNID("mt-1", "aa-1"), imiLinkNID("mt-2", "aa-2"))
	if *warnings != 1 {
		t.Errorf("warnings = %d; want 1", *warnings)
	}
}

// One association's failure is returned after its siblings' links, and the
// links of its pages read before the failure, are stored and contained.
func TestScanIMIManagedThingAccountAssociations_OtherErrorKeepsSiblings(t *testing.T) {
	st := newTestStore(t)
	things := imiParents("managed-thing", "mt-1", "mt-2")
	seedIMIThings(t, st, things)
	s := sdkaws.String
	stub := &stubIMI{
		t: t,
		assocLinks: map[string][][]imitypes.ManagedThingAssociation{
			"aa-1": {{{ManagedThingId: s("mt-1")}}, {{ManagedThingId: s("mt-2")}}},
			"aa-2": {{{ManagedThingId: s("mt-2")}}},
		},
		assocErrs:    map[string]error{"aa-1": apiErr("ValidationException", "bad")},
		assocErrPage: map[string]int{"aa-1": 1},
	}
	total, _, err := scanIMIManagedThingAccountAssociations(context.Background(), stub, newTestAccount(testAccountID), testRegion, st, testScanID, imiParents("account-association", "aa-1", "aa-2"), things)
	if !isAPIErrorCode(err, "ValidationException") {
		t.Fatalf("err = %v; want wrapped ValidationException", err)
	}
	if total != 2 {
		t.Errorf("total = %d; want 2", total)
	}
	assertSMIDs(t, st, TypeIoTManagedIntegrationsManagedThingAccountAssociation, imiLinkNID("mt-1", "aa-1"), imiLinkNID("mt-2", "aa-2"))
	assertSMContains(t, st, store.ResourceID("aws", testAccountID, things[1].arn), imiLinkNID("mt-2", "aa-2"))
}

// A failed closure write is returned as a store write failure, with the
// counts of the links already stored, and sends no warning, not even the one
// for a denied sibling association.
func TestScanIMIManagedThingAccountAssociations_FailedClosureWriteReturned(t *testing.T) {
	st := newTestStore(t)
	warns := countWarnings(st)
	things := imiParents("managed-thing", "mt-1")
	seedIMIThings(t, st, things)
	if _, err := st.DB().Exec(`CREATE TRIGGER refuse_closure BEFORE INSERT ON hierarchy_closure
		BEGIN SELECT RAISE(ABORT, 'test: closure write refused'); END`); err != nil {
		t.Fatalf("create trigger: %v", err)
	}
	stub := &stubIMI{
		t:          t,
		assocLinks: map[string][][]imitypes.ManagedThingAssociation{"aa-1": {{{ManagedThingId: sdkaws.String("mt-1")}}}},
		assocErrs:  map[string]error{"aa-2": crDenied},
	}
	total, _, err := scanIMIManagedThingAccountAssociations(context.Background(), stub, newTestAccount(testAccountID), testRegion, st, testScanID, imiParents("account-association", "aa-1", "aa-2"), things)
	if !errors.Is(err, store.ErrStoreWrite) {
		t.Fatalf("err = %v; want store.ErrStoreWrite", err)
	}
	if total != 1 {
		t.Errorf("total = %d; want 1", total)
	}
	if *warns != 0 {
		t.Errorf("warnings = %d; want 0", *warns)
	}
}

// A repeated token ends an association's listing as an error, keeping its
// first page.
func TestScanIMIManagedThingAccountAssociations_RepeatedTokenKeepsPage(t *testing.T) {
	st := newTestStore(t)
	h := imiEmptyHandlers(newIMICalls())
	h["ListManagedThings"] = func(any) (any, error) {
		return &iotmanagedintegrations.ListManagedThingsOutput{Items: []imitypes.ManagedThingSummary{imiThing("t")}}, nil
	}
	h["ListAccountAssociations"] = func(any) (any, error) {
		return &iotmanagedintegrations.ListAccountAssociationsOutput{Items: []imitypes.AccountAssociationItem{imiAssoc("t")}}, nil
	}
	h["ListManagedThingAccountAssociations"] = func(any) (any, error) {
		return &iotmanagedintegrations.ListManagedThingAccountAssociationsOutput{
			Items:     []imitypes.ManagedThingAssociation{{ManagedThingId: sdkaws.String("mtt"), AccountAssociationId: sdkaws.String("aat")}},
			NextToken: sdkaws.String("same"),
		}, nil
	}
	_, _, err := imiScan(context.Background(), t, st, h)
	if !errors.Is(err, errRepeatedNextToken) {
		t.Fatalf("err = %v; want errRepeatedNextToken", err)
	}
	assertSMIDs(t, st, TypeIoTManagedIntegrationsManagedThingAccountAssociation, imiLinkNID("mtt", "aat"))
}

// The fixture of the whole-service tests: every tagged listing serves its "t"
// item on the first page and its "b" item on the second; each account
// association links to the managed thing of the same suffix. Items whose id
// ends in "t" carry tags.

var imiCreated = time.Date(2026, 9, 3, 8, 0, 0, 0, time.UTC)

func imiAssoc(n string) imitypes.AccountAssociationItem {
	return imitypes.AccountAssociationItem{
		Arn: sdkaws.String(imiNID("account-association", "aa"+n)), AccountAssociationId: sdkaws.String("aa" + n),
		Name: sdkaws.String("association " + n), AssociationState: imitypes.AssociationStateAssociationSucceeded,
	}
}

func imiLocker(n string) imitypes.CredentialLockerSummary {
	return imitypes.CredentialLockerSummary{
		Arn: sdkaws.String(imiNID("credential-locker", "cl"+n)), Id: sdkaws.String("cl" + n),
		Name: sdkaws.String("locker " + n), CreatedAt: &imiCreated,
	}
}

func imiThing(n string) imitypes.ManagedThingSummary {
	return imitypes.ManagedThingSummary{
		Arn: sdkaws.String(imiNID("managed-thing", "mt"+n)), Id: sdkaws.String("mt" + n),
		Name: sdkaws.String("thing " + n), ProvisioningStatus: imitypes.ProvisioningStatusActivated, CreatedAt: &imiCreated,
	}
}

func imiOtaTask(n string) imitypes.OtaTaskSummary {
	return imitypes.OtaTaskSummary{
		TaskArn: sdkaws.String(imiNID("ota-task", "ot"+n)), TaskId: sdkaws.String("ot" + n),
		Status: imitypes.OtaStatusInProgress, CreatedAt: &imiCreated,
	}
}

func imiProfile(n string) imitypes.ProvisioningProfileSummary {
	return imitypes.ProvisioningProfileSummary{
		Arn: sdkaws.String(imiNID("provisioning-profile", "pp"+n)), Id: sdkaws.String("pp" + n),
		Name: sdkaws.String("profile " + n), Status: imitypes.ProvisioningProfileStatusCreated,
	}
}

// imiWant is a row the fixture stores: its Name, Status and CreatedAt ("" for
// none) and the element its attributes are.
type imiWant struct {
	typ, arn, name, status, created string
	element                         any
}

// imiRows is every tagged row the fixture stores.
func imiRows() []imiWant {
	created := sv(tp(&imiCreated))
	var rows []imiWant
	for _, n := range []string{"t", "b"} {
		a, l, th, o, pp := imiAssoc(n), imiLocker(n), imiThing(n), imiOtaTask(n), imiProfile(n)
		rows = append(rows,
			imiWant{TypeIoTManagedIntegrationsAccountAssociation, sv(a.Arn), sv(a.Name), "ASSOCIATION_SUCCEEDED", "", a},
			imiWant{TypeIoTManagedIntegrationsCredentialLocker, sv(l.Arn), sv(l.Name), "", created, l},
			imiWant{TypeIoTManagedIntegrationsManagedThing, sv(th.Arn), sv(th.Name), "ACTIVATED", created, th},
			imiWant{TypeIoTManagedIntegrationsOtaTask, sv(o.TaskArn), "", "IN_PROGRESS", created, o},
			imiWant{TypeIoTManagedIntegrationsProvisioningProfile, sv(pp.Arn), sv(pp.Name), "CREATED", "", pp},
		)
	}
	return rows
}

// imiCalls records the stubbed calls and answers ListTagsForResource from
// tags and errs, keyed by ARN.
type imiCalls struct{ *crtCalls }

func newIMICalls() *imiCalls {
	c := &imiCalls{&crtCalls{newBACCalls()}}
	for _, r := range imiRows() {
		if strings.HasSuffix(r.arn, "t") {
			c.tags[r.arn] = map[string]string{"type": r.typ}
		}
	}
	return c
}

func (c *imiCalls) listTags(in any) (any, error) {
	arn := sv(in.(*iotmanagedintegrations.ListTagsForResourceInput).ResourceArn)
	c.mu.Lock()
	c.tagARNs = append(c.tagARNs, arn)
	err, tags := c.errs[arn], c.tags[arn]
	c.mu.Unlock()
	if err != nil {
		return nil, err
	}
	return &iotmanagedintegrations.ListTagsForResourceOutput{Tags: tags}, nil
}

// imiTaggedListOps is every tagged listing, in call order.
var imiTaggedListOps = []string{"ListAccountAssociations", "ListCredentialLockers", "ListManagedThings", "ListOtaTasks", "ListProvisioningProfiles"}

// imiHandlers answers every op the scan calls with the fixture (imiRows);
// the untagged listings are empty.
func imiHandlers(c *imiCalls) map[string]func(any) (any, error) {
	h := imiEmptyHandlers(c)
	h["ListAccountAssociations"] = c.twoPages("ListAccountAssociations", func(n string, next *string) any {
		return &iotmanagedintegrations.ListAccountAssociationsOutput{Items: []imitypes.AccountAssociationItem{imiAssoc(n)}, NextToken: next}
	})
	h["ListCredentialLockers"] = c.twoPages("ListCredentialLockers", func(n string, next *string) any {
		return &iotmanagedintegrations.ListCredentialLockersOutput{Items: []imitypes.CredentialLockerSummary{imiLocker(n)}, NextToken: next}
	})
	h["ListManagedThings"] = c.twoPages("ListManagedThings", func(n string, next *string) any {
		return &iotmanagedintegrations.ListManagedThingsOutput{Items: []imitypes.ManagedThingSummary{imiThing(n)}, NextToken: next}
	})
	h["ListOtaTasks"] = c.twoPages("ListOtaTasks", func(n string, next *string) any {
		return &iotmanagedintegrations.ListOtaTasksOutput{Tasks: []imitypes.OtaTaskSummary{imiOtaTask(n)}, NextToken: next}
	})
	h["ListProvisioningProfiles"] = c.twoPages("ListProvisioningProfiles", func(n string, next *string) any {
		return &iotmanagedintegrations.ListProvisioningProfilesOutput{Items: []imitypes.ProvisioningProfileSummary{imiProfile(n)}, NextToken: next}
	})
	h["ListManagedThingAccountAssociations"] = c.answer("ListManagedThingAccountAssociations", func(in any) any {
		assoc := sv(in.(*iotmanagedintegrations.ListManagedThingAccountAssociationsInput).AccountAssociationId)
		thing := "mt" + strings.TrimPrefix(assoc, "aa")
		return &iotmanagedintegrations.ListManagedThingAccountAssociationsOutput{Items: []imitypes.ManagedThingAssociation{
			{ManagedThingId: &thing, AccountAssociationId: &assoc},
		}}
	})
	return h
}

// imiEmptyHandlers answers every listing the scan makes with an empty page
// and ListTagsForResource from c.
func imiEmptyHandlers(c *imiCalls) map[string]func(any) (any, error) {
	empty := map[string]any{
		"ListAccountAssociations":             &iotmanagedintegrations.ListAccountAssociationsOutput{},
		"ListCredentialLockers":               &iotmanagedintegrations.ListCredentialLockersOutput{},
		"ListManagedThings":                   &iotmanagedintegrations.ListManagedThingsOutput{},
		"ListOtaTasks":                        &iotmanagedintegrations.ListOtaTasksOutput{},
		"ListProvisioningProfiles":            &iotmanagedintegrations.ListProvisioningProfilesOutput{},
		"ListCloudConnectors":                 &iotmanagedintegrations.ListCloudConnectorsOutput{},
		"ListConnectorDestinations":           &iotmanagedintegrations.ListConnectorDestinationsOutput{},
		"ListDestinations":                    &iotmanagedintegrations.ListDestinationsOutput{},
		"ListEventLogConfigurations":          &iotmanagedintegrations.ListEventLogConfigurationsOutput{},
		"ListNotificationConfigurations":      &iotmanagedintegrations.ListNotificationConfigurationsOutput{},
		"ListOtaTaskConfigurations":           &iotmanagedintegrations.ListOtaTaskConfigurationsOutput{},
		"ListManagedThingAccountAssociations": &iotmanagedintegrations.ListManagedThingAccountAssociationsOutput{},
	}
	h := map[string]func(any) (any, error){"ListTagsForResource": c.listTags}
	for op, out := range empty {
		h[op] = c.answer(op, func(any) any { return out })
	}
	return h
}

// imiScan runs the scan's real entry point against handlers; every op they
// do not answer is denied.
func imiScan(ctx context.Context, t *testing.T, st *store.Store, handlers map[string]func(any) (any, error)) (int, int, error) {
	t.Helper()
	acct := newTestAccount(testAccountID)
	acct.cfg = opStubConfig(handlers, crDenied)
	return scanIoTManagedIntegrations(ctx, acct, testRegion, st, testScanID)
}

// Every tagged type stores the tags ListTagsForResource returns for its ARN,
// nil for a resource without tags, and the ARN of every such row, and of no
// other, is sent to the tag op. Both pages of every tagged listing are read,
// each asking for the model's page cap of 1000, as does the thing-link
// listing; each link is contained by its thing, and the scan reports no
// error or warning. Each row keeps the base scan's Name and verbatim
// element; Status and CreatedAt are the element's.
func TestScanIoTManagedIntegrations_StoresTagsOfEveryType(t *testing.T) {
	st := newTestStore(t)
	warns := countWarnings(st)
	calls := newIMICalls()

	total, inserted, err := imiScan(context.Background(), t, st, imiHandlers(calls))
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	rows := imiRows()
	if total != 12 || inserted != 12 || len(rows) != 10 {
		t.Errorf("total, inserted = %d, %d; want 12 (fixture tagged rows %d, plus 2 links)", total, inserted, len(rows))
	}
	if *warns != 0 {
		t.Errorf("warnings = %d; want 0", *warns)
	}
	var wantTagARNs []string
	for _, want := range rows {
		r, ok := rowsByNativeID(t, st, want.typ)[want.arn]
		if !ok {
			t.Errorf("%s %s not stored", want.typ, want.arn)
			continue
		}
		wantTagARNs = append(wantTagARNs, want.arn)
		if strings.HasSuffix(want.arn, "t") {
			assertTagsMap(t, r.TagsJSON, map[string]string{"type": want.typ})
		} else if r.TagsJSON != nil {
			t.Errorf("%s %s TagsJSON = %s; want nil", want.typ, want.arn, *r.TagsJSON)
		}
		if sv(r.Name) != want.name || (r.Name == nil) != (want.name == "") {
			t.Errorf("%s %s Name = %v; want %q", want.typ, want.arn, r.Name, want.name)
		}
		if sv(r.Status) != want.status || (r.Status == nil) != (want.status == "") {
			t.Errorf("%s %s Status = %v; want %q", want.typ, want.arn, r.Status, want.status)
		}
		if sv(r.CreatedAt) != want.created || (r.CreatedAt == nil) != (want.created == "") {
			t.Errorf("%s %s CreatedAt = %v; want %q", want.typ, want.arn, r.CreatedAt, want.created)
		}
		if sv(r.AccountName) != newTestAccount(testAccountID).Name {
			t.Errorf("%s %s AccountName = %v; want %q", want.typ, want.arn, r.AccountName, newTestAccount(testAccountID).Name)
		}
		if sv(r.Region) != testRegion || r.AttributesJSON != mustJSON(want.element) {
			t.Errorf("%s %s Region, attributes = %v, %s; want %s, %s", want.typ, want.arn, r.Region, r.AttributesJSON, testRegion, mustJSON(want.element))
		}
	}
	got := slices.Clone(calls.tagARNs)
	slices.Sort(got)
	slices.Sort(wantTagARNs)
	if !slices.Equal(got, wantTagARNs) {
		t.Errorf("ListTagsForResource ARNs = %v; want %v", got, wantTagARNs)
	}
	for _, op := range append(slices.Clone(imiTaggedListOps), "ListManagedThingAccountAssociations") {
		if caps := calls.maxResults[op]; !slices.Equal(caps, []int32{1000, 1000}) {
			t.Errorf("%s MaxResults = %v; want [1000 1000]", op, caps)
		}
	}
	links := rowsByNativeID(t, st, TypeIoTManagedIntegrationsManagedThingAccountAssociation)
	for _, n := range []string{"t", "b"} {
		link := imiLinkNID("mt"+n, "aa"+n)
		if r, ok := links[link]; !ok || r.TagsJSON != nil {
			t.Errorf("link %s stored %v, TagsJSON %v; want it stored without tags", link, ok, r.TagsJSON)
		}
		assertSMContains(t, st, store.ResourceID("aws", testAccountID, imiNID("managed-thing", "mt"+n)), link)
	}
}

// An aws-us-gov account scanned in us-gov-west-1 sends the gov ARNs its
// listings return to the tag op unchanged and stores their tags.
func TestScanIoTManagedIntegrations_GovCloudARNsReachTagOp(t *testing.T) {
	const govRegion = "us-gov-west-1"
	assoc := arnFor("aws-us-gov", "iotmanagedintegrations", govRegion, testAccountID, "account-association/aagov")
	task := arnFor("aws-us-gov", "iotmanagedintegrations", govRegion, testAccountID, "ota-task/otgov")
	st := newTestStore(t)
	calls := newIMICalls()
	calls.tags[assoc] = map[string]string{"env": "gov"}
	calls.tags[task] = map[string]string{"env": "gov-task"}
	h := imiEmptyHandlers(calls)
	h["ListAccountAssociations"] = calls.answer("ListAccountAssociations", func(any) any {
		return &iotmanagedintegrations.ListAccountAssociationsOutput{Items: []imitypes.AccountAssociationItem{{Arn: &assoc, AccountAssociationId: sdkaws.String("aagov")}}}
	})
	h["ListOtaTasks"] = calls.answer("ListOtaTasks", func(any) any {
		return &iotmanagedintegrations.ListOtaTasksOutput{Tasks: []imitypes.OtaTaskSummary{{TaskArn: &task, TaskId: sdkaws.String("otgov")}}}
	})
	acct := partitionAccount("aws-us-gov")
	acct.cfg = opStubConfig(h, crDenied)

	total, _, err := scanIoTManagedIntegrations(context.Background(), acct, govRegion, st, testScanID)
	if err != nil || total != 2 {
		t.Fatalf("scan = %d, %v; want 2, nil", total, err)
	}
	got := slices.Clone(calls.tagARNs)
	slices.Sort(got)
	if want := []string{assoc, task}; !slices.Equal(got, want) {
		t.Errorf("ListTagsForResource ARNs = %v; want %v", got, want)
	}
	for _, c := range []struct {
		typ, arn, env string
	}{{TypeIoTManagedIntegrationsAccountAssociation, assoc, "gov"}, {TypeIoTManagedIntegrationsOtaTask, task, "gov-task"}} {
		r, ok := rowsByNativeID(t, st, c.typ)[c.arn]
		if !ok {
			t.Fatalf("%s %s not stored", c.typ, c.arn)
		}
		assertTagsMap(t, r.TagsJSON, map[string]string{"env": c.env})
		if sv(r.Region) != govRegion {
			t.Errorf("%s Region = %v; want %s", c.arn, r.Region, govRegion)
		}
	}
}

// Every listing returning nothing stores nothing, reads no tags or thing
// links, and reports nothing.
func TestScanIoTManagedIntegrations_EmptyListings(t *testing.T) {
	st := newTestStore(t)
	warns := countWarnings(st)
	calls := newIMICalls()

	total, inserted, err := imiScan(context.Background(), t, st, imiEmptyHandlers(calls))
	if err != nil || total != 0 || inserted != 0 {
		t.Fatalf("scan = %d, %d, %v; want 0, 0, nil", total, inserted, err)
	}
	if *warns != 0 || len(calls.tagARNs) != 0 || calls.count("ListManagedThingAccountAssociations") != 0 {
		t.Errorf("warnings, tag reads, link listings = %d, %d, %d; want 0, 0, 0", *warns, len(calls.tagARNs), calls.count("ListManagedThingAccountAssociations"))
	}
}

// A rescan stores the same rows without inserting any.
func TestScanIoTManagedIntegrations_RescanInsertsNone(t *testing.T) {
	st := newTestStore(t)
	calls := newIMICalls()
	if _, _, err := imiScan(context.Background(), t, st, imiHandlers(calls)); err != nil {
		t.Fatalf("first scan: %v", err)
	}
	total, inserted, err := imiScan(context.Background(), t, st, imiHandlers(calls))
	if err != nil {
		t.Fatalf("rescan: %v", err)
	}
	if total != 12 || inserted != 0 {
		t.Errorf("rescan total, inserted = %d, %d; want 12, 0", total, inserted)
	}
}

// A failed tag read keeps the row without tags unless the resource is gone:
// a denial, AccessDeniedException or the op's modeled UnauthorizedException,
// warns once under iotmanagedintegrations:ListTagsForResource; a throttle
// after retries is a transient error, any other code a hard one; a not-found
// drops the row.
func TestScanIoTManagedIntegrations_TagReadFailures(t *testing.T) {
	target := imiNID("credential-locker", "clt")
	for _, c := range []struct {
		name      string
		err       error
		kept      bool
		outcome   serviceOutcome
		wantWarns int
	}{
		{"access denied", crDenied, true, 0, 1},
		{"unauthorized", apiErr("UnauthorizedException", "not authorized"), true, 0, 1},
		{"not found", crNotFound, false, 0, 0},
		{"other error", apiErr("InvalidRequestException", "bad"), true, outcomeError, 0},
		{"transient", crThrottled, true, outcomeTransient, 0},
	} {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			warnings := bacWarnings(st, imiTagsOp)
			calls := newIMICalls()
			calls.errs[target] = c.err

			total, _, err := imiScan(context.Background(), t, st, imiHandlers(calls))
			if c.outcome == 0 && err != nil {
				t.Fatalf("err = %v; want nil", err)
			}
			if c.outcome != 0 {
				assertGlueOutcome(t, err, c.outcome)
			}
			if c.outcome == outcomeError {
				assertAPICode(t, err, "InvalidRequestException")
			}
			r, ok := rowsByNativeID(t, st, TypeIoTManagedIntegrationsCredentialLocker)[target]
			if ok != c.kept {
				t.Fatalf("row stored = %v; want %v", ok, c.kept)
			}
			if ok && r.TagsJSON != nil {
				t.Errorf("TagsJSON = %q; want nil", *r.TagsJSON)
			}
			want := 12
			if !c.kept {
				want = 11
			}
			if total != want {
				t.Errorf("total = %d; want %d", total, want)
			}
			if len(*warnings) != c.wantWarns {
				t.Fatalf("%s warnings = %v; want %d", imiTagsOp, *warnings, c.wantWarns)
			}
			for _, w := range *warnings {
				if w.Scope != testAccountID+"/"+testRegion {
					t.Errorf("warning Scope = %q; want %s/%s", w.Scope, testAccountID, testRegion)
				}
			}
		})
	}
}

// A parent whose tag read finds it gone is not stored and not a parent: its
// thing links are not listed, or not stored, and nothing is warned about.
func TestScanIoTManagedIntegrations_VanishedParentIsNotAParent(t *testing.T) {
	t.Run("account association", func(t *testing.T) {
		st := newTestStore(t)
		warns := countWarnings(st)
		calls := newIMICalls()
		calls.errs[imiNID("account-association", "aat")] = crNotFound
		if _, _, err := imiScan(context.Background(), t, st, imiHandlers(calls)); err != nil {
			t.Fatalf("err = %v; want nil", err)
		}
		if n := calls.count("ListManagedThingAccountAssociations"); n != 1 {
			t.Errorf("link listings = %d; want 1, for aab only", n)
		}
		assertSMIDs(t, st, TypeIoTManagedIntegrationsManagedThingAccountAssociation, imiLinkNID("mtb", "aab"))
		if *warns != 0 {
			t.Errorf("warnings = %d; want 0", *warns)
		}
	})
	t.Run("managed thing", func(t *testing.T) {
		st := newTestStore(t)
		linkWarns := bacWarnings(st, imiThingLinksOp)
		calls := newIMICalls()
		calls.errs[imiNID("managed-thing", "mtt")] = crNotFound
		if _, _, err := imiScan(context.Background(), t, st, imiHandlers(calls)); err != nil {
			t.Fatalf("err = %v; want nil", err)
		}
		assertSMIDs(t, st, TypeIoTManagedIntegrationsManagedThingAccountAssociation, imiLinkNID("mtb", "aab"))
		if len(*linkWarns) != 1 {
			t.Errorf("%s warnings = %v; want 1, the link of the vanished thing", imiThingLinksOp, *linkWarns)
		}
	})
}

// Denied tag reads in several listings are one warning for the scan.
func TestScanIoTManagedIntegrations_TagDenialsWarnOnce(t *testing.T) {
	st := newTestStore(t)
	warnings := bacWarnings(st, imiTagsOp)
	calls := newIMICalls()
	calls.errs[imiNID("credential-locker", "clt")] = crDenied
	calls.errs[imiNID("provisioning-profile", "ppb")] = apiErr("UnauthorizedException", "not authorized")

	if _, _, err := imiScan(context.Background(), t, st, imiHandlers(calls)); err != nil {
		t.Fatalf("err = %v; want nil", err)
	}
	if len(*warnings) != 1 || (*warnings)[0].Scope != testAccountID+"/"+testRegion {
		t.Errorf("%s warnings = %v; want one, scoped %s/%s", imiTagsOp, *warnings, testAccountID, testRegion)
	}
	for _, r := range []struct{ typ, arn string }{
		{TypeIoTManagedIntegrationsCredentialLocker, imiNID("credential-locker", "clt")},
		{TypeIoTManagedIntegrationsProvisioningProfile, imiNID("provisioning-profile", "ppb")},
	} {
		if got, ok := rowsByNativeID(t, st, r.typ)[r.arn]; !ok || got.TagsJSON != nil {
			t.Errorf("%s stored %v, TagsJSON %v; want it kept without tags", r.arn, ok, got.TagsJSON)
		}
	}
}

// A throttled tag read beside a hard failure of the same op is demoted to
// one warning, so the hard error decides the outcome.
func TestScanIoTManagedIntegrations_TagThrottleDemotedBesideHardError(t *testing.T) {
	st := newTestStore(t)
	warns := countOpWarnings(st, imiTagsOp)
	calls := newIMICalls()
	calls.errs[imiNID("credential-locker", "clt")] = crThrottled
	calls.errs[imiNID("ota-task", "otb")] = apiErr("InvalidRequestException", "bad")

	_, _, err := imiScan(context.Background(), t, st, imiHandlers(calls))
	assertGlueOutcome(t, err, outcomeError)
	assertAPICode(t, err, "InvalidRequestException")
	if *warns != 1 {
		t.Errorf("%s warnings = %d; want 1", imiTagsOp, *warns)
	}
}

// A denied listing warns once under its own op and costs no other phase its
// rows; a denied parent listing leaves its children unread.
func TestScanIoTManagedIntegrations_DeniedListingWarnsUnderItsOp(t *testing.T) {
	// 12 rows, less the denied listing's 2, and for a parent listing the 2
	// links too.
	lost := map[string]int{"ListAccountAssociations": 4, "ListManagedThings": 4}
	for _, op := range append(slices.Clone(imiTaggedListOps), "ListManagedThingAccountAssociations") {
		t.Run(op, func(t *testing.T) {
			st := newTestStore(t)
			warns := countWarnings(st)
			opWarns := countOpWarnings(st, "iotmanagedintegrations:"+op)
			h := imiHandlers(newIMICalls())
			h[op] = func(any) (any, error) { return nil, crDenied }

			total, _, err := imiScan(context.Background(), t, st, h)
			if err != nil {
				t.Fatalf("err = %v; want nil", err)
			}
			if *warns != 1 || *opWarns != 1 {
				t.Errorf("warnings = %d (%s: %d); want 1 under the op", *warns, op, *opWarns)
			}
			want := 12 - 2
			if n, ok := lost[op]; ok {
				want = 12 - n
			}
			if stored := countStoredRows(t, st); total != want || stored != want {
				t.Errorf("total = %d, stored rows = %d; want %d", total, stored, want)
			}
		})
	}
}

// UnauthorizedException, which ListManagedThings and ListProvisioningProfiles
// model as a denial beside AccessDeniedException, warns once under the
// listing's op and costs no other phase its rows; the denied things leave
// their links unread.
func TestScanIoTManagedIntegrations_UnauthorizedListingWarnsUnderItsOp(t *testing.T) {
	unauthorized := apiErr(imiUnauthorized, "You are not authorized to perform this operation.")
	// 12 rows, less the denied listing's 2, and for the things the 2 links.
	for op, want := range map[string]int{"ListManagedThings": 12 - 4, "ListProvisioningProfiles": 12 - 2} {
		t.Run(op, func(t *testing.T) {
			st := newTestStore(t)
			warns := countWarnings(st)
			opWarns := countOpWarnings(st, "iotmanagedintegrations:"+op)
			h := imiHandlers(newIMICalls())
			h[op] = func(any) (any, error) { return nil, unauthorized }

			total, _, err := imiScan(context.Background(), t, st, h)
			if err != nil {
				t.Fatalf("err = %v; want nil", err)
			}
			if *warns != 1 || *opWarns != 1 {
				t.Errorf("warnings = %d (%s: %d); want 1 under the op", *warns, op, *opWarns)
			}
			if stored := countStoredRows(t, st); total != want || stored != want {
				t.Errorf("total = %d, stored rows = %d; want %d", total, stored, want)
			}
		})
	}
}

// A failed write of the links stops the scan with no warning and records no
// closure for them.
func TestScanIoTManagedIntegrations_FailedLinkWriteSendsNoWarning(t *testing.T) {
	st := newTestStore(t)
	warns := countWarnings(st)
	refuseStoreWrites(t, st, TypeIoTManagedIntegrationsManagedThingAccountAssociation)

	_, _, err := imiScan(context.Background(), t, st, imiHandlers(newIMICalls()))
	if !errors.Is(err, store.ErrStoreWrite) {
		t.Fatalf("err = %v; want store.ErrStoreWrite", err)
	}
	if *warns != 0 {
		t.Errorf("warnings = %d; want 0", *warns)
	}
	for _, n := range []string{"t", "b"} {
		thing := store.ResourceID("aws", testAccountID, imiNID("managed-thing", "mt"+n))
		rels, rerr := st.RelationshipsFrom(thing, store.RelContains)
		if rerr != nil {
			t.Fatalf("relationships from %s: %v", thing, rerr)
		}
		if len(rels) != 0 {
			t.Errorf("contains rows under %s = %v; want none", thing, rels)
		}
	}
}

// A failed write stops the scan before any later listing and sends no
// warning about the rows it lost.
func TestScanIoTManagedIntegrations_FailedWriteStopsWithoutWarning(t *testing.T) {
	st := newTestStore(t)
	warns := countWarnings(st)
	refuseStoreWrites(t, st, TypeIoTManagedIntegrationsCredentialLocker)
	calls := newIMICalls()
	calls.errs[imiNID("credential-locker", "clt")] = crDenied

	_, _, err := imiScan(context.Background(), t, st, imiHandlers(calls))
	if !errors.Is(err, store.ErrStoreWrite) {
		t.Fatalf("err = %v; want store.ErrStoreWrite", err)
	}
	if *warns != 0 {
		t.Errorf("warnings = %d; want 0", *warns)
	}
	for _, op := range []string{"ListManagedThings", "ListCloudConnectors", "ListManagedThingAccountAssociations"} {
		if n := calls.count(op); n != 0 {
			t.Errorf("%s called %d times after the failed write", op, n)
		}
	}
}

// A listing's failed page keeps the pages read before it, and no listing's
// failure stops the phases after it; a throttled listing beside it is
// demoted to a warning.
func TestScanIoTManagedIntegrations_ListFailureKeepsPagesAndRunsEveryPhase(t *testing.T) {
	st := newTestStore(t)
	warns := countWarnings(st)
	calls := newIMICalls()
	h := imiHandlers(calls)
	h["ListCredentialLockers"] = failOnCall(2, crtBadInput, h["ListCredentialLockers"])
	h["ListAccountAssociations"] = func(any) (any, error) { return nil, crThrottled }

	total, _, err := imiScan(context.Background(), t, st, h)
	assertGlueOutcome(t, err, outcomeError)
	assertAPICode(t, err, "ValidationException")
	if *warns != 1 {
		t.Errorf("warnings = %d; want 1, the demoted throttle", *warns)
	}
	// 12 rows less 2 account associations, their 2 links and the second
	// credential locker.
	if total != 7 {
		t.Errorf("total = %d; want 7", total)
	}
	assertSMIDs(t, st, TypeIoTManagedIntegrationsCredentialLocker, imiNID("credential-locker", "clt"))
	assertSMIDs(t, st, TypeIoTManagedIntegrationsProvisioningProfile, imiNID("provisioning-profile", "ppt"), imiNID("provisioning-profile", "ppb"))
}

// Items listed without an ARN are not stored or read and are warned about
// once per listing, under a key that is never empty; parents stored without
// the id their links key on are warned about, as their links are not read.
func TestScanIoTManagedIntegrations_UnstorableItemsWarn(t *testing.T) {
	s := sdkaws.String
	for _, c := range []struct {
		name, op, key string
		out           any
	}{
		{"association without ARN", "ListAccountAssociations", "first (an account association with no name)", &iotmanagedintegrations.ListAccountAssociationsOutput{Items: []imitypes.AccountAssociationItem{{}, {}}}},
		{"locker without ARN", "ListCredentialLockers", "first locker x", &iotmanagedintegrations.ListCredentialLockersOutput{Items: []imitypes.CredentialLockerSummary{{Name: s("locker x")}}}},
		{"thing without ARN", "ListManagedThings", "first mtx", &iotmanagedintegrations.ListManagedThingsOutput{Items: []imitypes.ManagedThingSummary{{Id: s("mtx")}}}},
		{"task without ARN", "ListOtaTasks", "first (an OTA task with no id)", &iotmanagedintegrations.ListOtaTasksOutput{Tasks: []imitypes.OtaTaskSummary{{}}}},
		{"profile without ARN", "ListProvisioningProfiles", "first ppx", &iotmanagedintegrations.ListProvisioningProfilesOutput{Items: []imitypes.ProvisioningProfileSummary{{Id: s("ppx")}}}},
		{"association without id", "ListAccountAssociations", "first " + imiNID("account-association", "aax"), &iotmanagedintegrations.ListAccountAssociationsOutput{Items: []imitypes.AccountAssociationItem{{Arn: s(imiNID("account-association", "aax"))}}}},
		{"thing without id", "ListManagedThings", "first " + imiNID("managed-thing", "mtx"), &iotmanagedintegrations.ListManagedThingsOutput{Items: []imitypes.ManagedThingSummary{{Arn: s(imiNID("managed-thing", "mtx"))}}}},
	} {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			warnings := bacWarnings(st, "iotmanagedintegrations:"+c.op)
			h := imiHandlers(newIMICalls())
			h[c.op] = func(any) (any, error) { return c.out, nil }

			if _, _, err := imiScan(context.Background(), t, st, h); err != nil {
				t.Fatalf("err = %v; want nil", err)
			}
			if len(*warnings) != 1 || !strings.Contains((*warnings)[0].Message, c.key) {
				t.Errorf("warnings = %v; want one naming %q", *warnings, c.key)
			}
		})
	}
}

// A repeated token ends a tagged listing as an error, keeping its pages.
func TestScanIoTManagedIntegrations_RepeatedTokenKeepsPages(t *testing.T) {
	st := newTestStore(t)
	h := imiHandlers(newIMICalls())
	h["ListOtaTasks"] = func(any) (any, error) {
		return &iotmanagedintegrations.ListOtaTasksOutput{Tasks: []imitypes.OtaTaskSummary{imiOtaTask("t")}, NextToken: sdkaws.String("same")}, nil
	}

	_, _, err := imiScan(context.Background(), t, st, h)
	if !errors.Is(err, errRepeatedNextToken) {
		t.Fatalf("err = %v; want errRepeatedNextToken", err)
	}
	assertSMIDs(t, st, TypeIoTManagedIntegrationsOtaTask, imiNID("ota-task", "ott"))
}

// An account without the custom endpoint OTA tasks need has none: that
// answer to the first page is an empty listing with no warning; to a later
// page it is an error, the first page kept.
func TestScanIoTManagedIntegrations_OtaEndpointGap(t *testing.T) {
	gap := apiErr("UnknownError", "Please register the custom endpoint and onboard the managed thing")
	t.Run("first page", func(t *testing.T) {
		st := newTestStore(t)
		warns := countWarnings(st)
		h := imiHandlers(newIMICalls())
		h["ListOtaTasks"] = func(any) (any, error) { return nil, gap }
		total, _, err := imiScan(context.Background(), t, st, h)
		if err != nil || total != 10 || *warns != 0 {
			t.Errorf("total, err, warnings = %d, %v, %d; want 10, nil, 0", total, err, *warns)
		}
	})
	t.Run("later page", func(t *testing.T) {
		st := newTestStore(t)
		h := imiHandlers(newIMICalls())
		h["ListOtaTasks"] = failOnCall(2, gap, h["ListOtaTasks"])
		_, _, err := imiScan(context.Background(), t, st, h)
		if !isAPIErrorCode(err, "UnknownError") {
			t.Fatalf("err = %v; want the UnknownError", err)
		}
		assertSMIDs(t, st, TypeIoTManagedIntegrationsOtaTask, imiNID("ota-task", "ott"))
	})
}

// Once ctx ends no further phase starts, and the stop is reported once,
// naming the phases not run.
func TestScanIoTManagedIntegrations_ContextEndStopsOnce(t *testing.T) {
	st := newTestStore(t)
	calls := newIMICalls()
	h := imiHandlers(calls)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	things := h["ListManagedThings"]
	h["ListManagedThings"] = func(in any) (any, error) {
		if in.(*iotmanagedintegrations.ListManagedThingsInput).NextToken != nil {
			cancel()
			return nil, context.Canceled
		}
		return things(in)
	}

	_, _, err := imiScan(ctx, t, st, h)
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
	if n := calls.count("ListOtaTasks"); n != 0 {
		t.Errorf("ListOtaTasks called %d times after ctx ended", n)
	}
	// Managed things are listed in phase 3 of 12, so 9 never started.
	if !strings.Contains(err.Error(), "iotmanagedintegrations: 9 phases not run") {
		t.Errorf("err = %v; want it to say 9 phases not run", err)
	}
}

// A region without the service (NXDOMAIN on the first listing, nothing
// stored) is unavailable, and no other listing is made. An NXDOMAIN of a tag
// read after rows were listed is a failure of this scan: the counts are kept.
func TestScanIoTManagedIntegrations_Unreachable(t *testing.T) {
	nx := &net.DNSError{Err: "no such host", Name: "api.iotmanagedintegrations.us-east-1.api.aws", IsNotFound: true}
	t.Run("endpoint absent", func(t *testing.T) {
		st := newTestStore(t)
		calls := newIMICalls()
		h := imiHandlers(calls)
		h["ListAccountAssociations"] = func(any) (any, error) { return nil, nx }
		total, _, err := imiScan(context.Background(), t, st, h)
		assertGlueOutcome(t, err, outcomeUnavailable)
		if total != 0 || calls.count("ListCredentialLockers") != 0 || calls.count("ListOtaTaskConfigurations") != 0 {
			t.Errorf("total = %d, ListCredentialLockers, ListOtaTaskConfigurations calls = %d, %d; want 0, 0, 0",
				total, calls.count("ListCredentialLockers"), calls.count("ListOtaTaskConfigurations"))
		}
	})
	t.Run("tag read after rows were listed", func(t *testing.T) {
		st := newTestStore(t)
		calls := newIMICalls()
		calls.errs[imiNID("credential-locker", "clt")] = nx
		total, _, err := imiScan(context.Background(), t, st, imiHandlers(calls))
		assertGlueOutcome(t, err, outcomeError)
		if total != 12 {
			t.Errorf("total = %d; want 12", total)
		}
	})
}

// TestScanIoTManagedIntegrations_ChildrenContainedByParents runs the whole
// service against the real SDK client: children are contained by parents
// stored earlier in the same scan.
func TestScanIoTManagedIntegrations_ChildrenContainedByParents(t *testing.T) {
	st := newTestStore(t)
	s := sdkaws.String
	thingARN := imiNID("managed-thing", "mt1")
	connector := imiNID("cloud-connector", "cc-1")
	one := func(out any) []stubCall { return []stubCall{{Output: out}} }
	stub := stubResponses(t, map[string][]stubCall{
		"ListAccountAssociations": one(&iotmanagedintegrations.ListAccountAssociationsOutput{Items: []imitypes.AccountAssociationItem{{
			Arn: s(imiNID("account-association", "aa1")), AccountAssociationId: s("aa1"),
		}}}),
		"ListCredentialLockers":    one(&iotmanagedintegrations.ListCredentialLockersOutput{}),
		"ListManagedThings":        one(&iotmanagedintegrations.ListManagedThingsOutput{Items: []imitypes.ManagedThingSummary{{Arn: s(thingARN), Id: s("mt1")}}}),
		"ListOtaTasks":             one(&iotmanagedintegrations.ListOtaTasksOutput{}),
		"ListProvisioningProfiles": one(&iotmanagedintegrations.ListProvisioningProfilesOutput{}),
		"ListTagsForResource":      {{Output: &iotmanagedintegrations.ListTagsForResourceOutput{}}, {Output: &iotmanagedintegrations.ListTagsForResourceOutput{}}},
		"ListCloudConnectors":      one(&iotmanagedintegrations.ListCloudConnectorsOutput{Items: []imitypes.ConnectorItem{{Id: s("cc-1"), Name: s("c")}}}),
		"ListConnectorDestinations": one(&iotmanagedintegrations.ListConnectorDestinationsOutput{
			ConnectorDestinationList: []imitypes.ConnectorDestinationSummary{{Id: s("cd-1"), CloudConnectorId: s("cc-1")}},
		}),
		"ListDestinations":               one(&iotmanagedintegrations.ListDestinationsOutput{DestinationList: []imitypes.DestinationSummary{{Name: s("d1")}}}),
		"ListEventLogConfigurations":     one(&iotmanagedintegrations.ListEventLogConfigurationsOutput{EventLogConfigurationList: []imitypes.EventLogConfigurationSummary{{Id: s("el-1")}}}),
		"ListNotificationConfigurations": one(&iotmanagedintegrations.ListNotificationConfigurationsOutput{NotificationConfigurationList: []imitypes.NotificationConfigurationSummary{{EventType: imitypes.EventTypeDeviceEvent}}}),
		"ListOtaTaskConfigurations":      one(&iotmanagedintegrations.ListOtaTaskConfigurationsOutput{Items: []imitypes.OtaTaskConfigurationSummary{{TaskConfigurationId: s("otc-1")}}}),
		"ListManagedThingAccountAssociations": one(&iotmanagedintegrations.ListManagedThingAccountAssociationsOutput{
			Items: []imitypes.ManagedThingAssociation{{ManagedThingId: s("mt1"), AccountAssociationId: s("aa1")}},
		}),
	})
	acct := newTestAccount(testAccountID)
	acct.cfg = cfgWithStub(stub, testRegion)
	if _, _, err := scanIoTManagedIntegrations(context.Background(), acct, testRegion, st, testScanID); err != nil {
		t.Fatalf("scan: %v", err)
	}
	assertSMIDs(t, st, TypeIoTManagedIntegrationsDestination, imiNID("destination", "d1"))
	assertSMIDs(t, st, TypeIoTManagedIntegrationsEventLogConfiguration, imiNID("event-log-configuration", "el-1"))
	assertSMIDs(t, st, TypeIoTManagedIntegrationsNotificationConfiguration, imiNID("notification-configuration", string(imitypes.EventTypeDeviceEvent)))
	assertSMIDs(t, st, TypeIoTManagedIntegrationsOtaTaskConfiguration, imiNID("ota-task-configuration", "otc-1"))
	assertSMContains(t, st, store.ResourceID("aws", testAccountID, connector), imiNID("connector-destination", "cd-1"))
	assertSMContains(t, st, store.ResourceID("aws", testAccountID, thingARN), thingARN+"/account-association/aa1")
}

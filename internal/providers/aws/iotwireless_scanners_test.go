package aws

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"sync/atomic"
	"testing"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/iotwireless"
	iwtypes "github.com/aws/aws-sdk-go-v2/service/iotwireless/types"
	"github.com/icearp/disco-cli/store"
)

const (
	iwTestARNPrefix   = "arn:aws:iotwireless:us-east-1:123456789012:"
	iwTestDevice      = iwTestARNPrefix + "WirelessDevice/dev-1"
	iwTestGateway     = iwTestARNPrefix + "WirelessGateway/gw-1"
	iwTestPartner     = iwTestARNPrefix + "SidewalkAccount/amzn-1"
	iwTestTaskDef     = iwTestARNPrefix + "WirelessGatewayTaskDefinition/td-1"
	iwTestCfgDevice   = iwTestDevice + "/event-configuration"
	iwTestCfgDevEui   = iwTestARNPrefix + "DevEui/a1b2c3d4e5f60708/event-configuration"
	iwTestCfgUnlisted = iwTestARNPrefix + "WirelessDevice/dev-unlisted/event-configuration"
	iwTestCfgGateway  = iwTestGateway + "/event-configuration"
	iwTestCfgGwEui    = iwTestARNPrefix + "GatewayEui/b1b2c3d4e5f60708/event-configuration"
	iwTestCfgPartner  = iwTestPartner + "/event-configuration"
)

var (
	iwDenied   = apiErr("AccessDeniedException", "User: arn:aws:iam::123456789012:role/r is not authorized to perform: iotwireless:ListEventConfigurations")
	iwGated    = apiErr("AccessDeniedException", "Account 123456789012 is not authorized to call this API in this region us-west-2")
	iwBadInput = apiErr("ValidationException", "bad")
	iwBusy     = apiErr("InternalServerException", "try later")
	iwConflict = apiErr("ConflictException", "unexpected")

	// iwTestCfgRows are the event configurations iwHandlers lists, by resource type.
	iwTestCfgRows = map[iwtypes.EventNotificationResourceType][]string{
		iwtypes.EventNotificationResourceTypeWirelessDevice:  {iwTestCfgDevice, iwTestCfgDevEui, iwTestCfgUnlisted},
		iwtypes.EventNotificationResourceTypeWirelessGateway: {iwTestCfgGateway, iwTestCfgGwEui},
		iwtypes.EventNotificationResourceTypeSidewalkAccount: {iwTestCfgPartner},
	}
)

// iwCfgRowsExcept returns the event configuration NativeIDs of every resource
// type but skip.
func iwCfgRowsExcept(skip ...iwtypes.EventNotificationResourceType) []string {
	var out []string
	for rt, ids := range iwTestCfgRows {
		if !slices.Contains(skip, rt) {
			out = append(out, ids...)
		}
	}
	return out
}

// iwHandlers answers every IoT Wireless op the scan calls. The older phases
// store one partner account, wireless device (with a DevEui), wireless gateway
// (with a GatewayEui) and gateway task definition. ListEventConfigurations
// answers WirelessDevice in two pages (with an identifier-less item to skip
// and a device no older phase listed), WirelessGateway by id and by EUI and
// SidewalkAccount by partner account id; every call records its resource type
// in calls and fails unless MaxResults is the model's cap.
func iwHandlers(calls *parentCalls) map[string]func(in any) (any, error) {
	badLimit := apiErr("ValidationException", "maxResults must be at most 250")
	item := func(id string, idType iwtypes.IdentifierType) iwtypes.EventConfigurationItem {
		return iwtypes.EventConfigurationItem{
			Identifier: sdkaws.String(id), IdentifierType: idType,
			Events: &iwtypes.EventNotificationItemConfigurations{
				Join: &iwtypes.JoinEventConfiguration{WirelessDeviceIdEventTopic: iwtypes.EventNotificationTopicStatusEnabled},
			},
		}
	}
	return map[string]func(in any) (any, error){
		"ListDestinations":   func(any) (any, error) { return &iotwireless.ListDestinationsOutput{}, nil },
		"ListDeviceProfiles": func(any) (any, error) { return &iotwireless.ListDeviceProfilesOutput{}, nil },
		"ListFuotaTasks":     func(any) (any, error) { return &iotwireless.ListFuotaTasksOutput{}, nil },
		"ListMulticastGroups": func(any) (any, error) {
			return &iotwireless.ListMulticastGroupsOutput{}, nil
		},
		"ListNetworkAnalyzerConfigurations": func(any) (any, error) {
			return &iotwireless.ListNetworkAnalyzerConfigurationsOutput{}, nil
		},
		"ListPartnerAccounts": func(any) (any, error) {
			return &iotwireless.ListPartnerAccountsOutput{Sidewalk: []iwtypes.SidewalkAccountInfoWithFingerprint{
				{AmazonId: sdkaws.String("amzn-1"), Arn: sdkaws.String(iwTestPartner)},
			}}, nil
		},
		"ListServiceProfiles": func(any) (any, error) { return &iotwireless.ListServiceProfilesOutput{}, nil },
		"ListWirelessDevices": func(any) (any, error) {
			return &iotwireless.ListWirelessDevicesOutput{WirelessDeviceList: []iwtypes.WirelessDeviceStatistics{
				{Arn: sdkaws.String(iwTestDevice), Id: sdkaws.String("dev-1"), LoRaWAN: &iwtypes.LoRaWANListDevice{DevEui: sdkaws.String("a1b2c3d4e5f60708")}},
			}}, nil
		},
		"ListWirelessDeviceImportTasks": func(any) (any, error) {
			return &iotwireless.ListWirelessDeviceImportTasksOutput{}, nil
		},
		"ListWirelessGateways": func(any) (any, error) {
			return &iotwireless.ListWirelessGatewaysOutput{WirelessGatewayList: []iwtypes.WirelessGatewayStatistics{
				{Arn: sdkaws.String(iwTestGateway), Id: sdkaws.String("gw-1"), LoRaWAN: &iwtypes.LoRaWANGateway{GatewayEui: sdkaws.String("b1b2c3d4e5f60708")}},
			}}, nil
		},
		"ListWirelessGatewayTaskDefinitions": func(any) (any, error) {
			return &iotwireless.ListWirelessGatewayTaskDefinitionsOutput{TaskDefinitions: []iwtypes.UpdateWirelessGatewayTaskEntry{
				{Arn: sdkaws.String(iwTestTaskDef), Id: sdkaws.String("td-1")},
			}}, nil
		},
		"ListEventConfigurations": func(in any) (any, error) {
			p := in.(*iotwireless.ListEventConfigurationsInput)
			calls.add(string(p.ResourceType))
			if p.MaxResults != iwEventConfigPageLimit {
				return nil, badLimit
			}
			var out iotwireless.ListEventConfigurationsOutput
			switch p.ResourceType {
			case iwtypes.EventNotificationResourceTypeWirelessDevice:
				out.EventConfigurationsList, out.NextToken = evsPage(p.NextToken,
					[]iwtypes.EventConfigurationItem{item("dev-1", iwtypes.IdentifierTypeWirelessDeviceId), {IdentifierType: iwtypes.IdentifierTypeWirelessDeviceId}},
					[]iwtypes.EventConfigurationItem{item("a1b2c3d4e5f60708", iwtypes.IdentifierTypeDevEui), item("dev-unlisted", iwtypes.IdentifierTypeWirelessDeviceId)})
			case iwtypes.EventNotificationResourceTypeWirelessGateway:
				out.EventConfigurationsList = []iwtypes.EventConfigurationItem{
					item("gw-1", iwtypes.IdentifierTypeWirelessGatewayId), item("b1b2c3d4e5f60708", iwtypes.IdentifierTypeGatewayEui),
				}
			case iwtypes.EventNotificationResourceTypeSidewalkAccount:
				partner := item("amzn-1", iwtypes.IdentifierTypePartnerAccountId)
				partner.PartnerType = iwtypes.EventNotificationPartnerTypeSidewalk
				out.EventConfigurationsList = []iwtypes.EventConfigurationItem{partner}
			default:
				return nil, iwConflict
			}
			return &out, nil
		},
	}
}

func scanIoTWirelessTopLevel(t *testing.T, st *store.Store, handlers map[string]func(in any) (any, error)) (int, error) {
	t.Helper()
	acct := newTestAccount(testAccountID)
	acct.cfg = opStubConfig(handlers, iwDenied)
	total, _, err := scanIoTWireless(context.Background(), acct, testRegion, st, testScanID)
	return total, err
}

// failIWEventConfigs makes ListEventConfigurations fail with err for the
// resource types in failing and answer the others as before.
func failIWEventConfigs(handlers map[string]func(in any) (any, error), err error, failing ...iwtypes.EventNotificationResourceType) {
	base := handlers["ListEventConfigurations"]
	handlers["ListEventConfigurations"] = func(in any) (any, error) {
		if slices.Contains(failing, in.(*iotwireless.ListEventConfigurationsInput).ResourceType) {
			return nil, err
		}
		return base(in)
	}
}

// assertIWCfgRows fails unless the stored event configurations are exactly want.
func assertIWCfgRows(t *testing.T, st *store.Store, want []string) {
	t.Helper()
	var got []string
	for id := range rowsByNativeID(t, st, TypeIoTWirelessEventConfiguration) {
		got = append(got, id)
	}
	slices.Sort(got)
	want = slices.Sorted(slices.Values(want))
	if !slices.Equal(got, want) {
		t.Errorf("event configuration rows = %v; want %v", got, want)
	}
}

func TestScanIoTWireless_StoresEventConfigurations(t *testing.T) {
	st := newTestStore(t)
	var warnings atomic.Int32
	st.OnWarn = func(store.ScanWarning) { warnings.Add(1) }
	calls := &parentCalls{}
	total, err := scanIoTWirelessTopLevel(t, st, iwHandlers(calls))
	if err != nil {
		t.Fatalf("scanIoTWireless: %v", err)
	}
	if n := warnings.Load(); n != 0 {
		t.Errorf("warnings = %d; want 0", n)
	}
	// 4 older-phase rows + 6 event configurations.
	if total != 10 {
		t.Errorf("total = %d; want 10", total)
	}
	assertIWCfgRows(t, st, iwCfgRowsExcept())
	if got, want := calls.distinct(), []string{"SidewalkAccount", "WirelessDevice", "WirelessGateway"}; !slices.Equal(got, want) {
		t.Errorf("ListEventConfigurations resource types = %v; want %v", got, want)
	}

	row := rowsByNativeID(t, st, TypeIoTWirelessEventConfiguration)[iwTestCfgPartner]
	var attrs iwtypes.EventConfigurationItem
	if err := json.Unmarshal([]byte(row.AttributesJSON), &attrs); err != nil {
		t.Fatalf("decode event configuration attrs: %v", err)
	}
	if row.Type != TypeIoTWirelessEventConfiguration || sv(row.Region) != testRegion || sv(row.Name) != "amzn-1" ||
		attrs.PartnerType != iwtypes.EventNotificationPartnerTypeSidewalk || attrs.Events == nil || attrs.Events.Join == nil ||
		attrs.Events.Join.WirelessDeviceIdEventTopic != iwtypes.EventNotificationTopicStatusEnabled {
		t.Errorf("partner event configuration row = type %q region %q name %q attrs %s; want %q %q %q with PartnerType Sidewalk and the Join topic enabled",
			row.Type, sv(row.Region), sv(row.Name), row.AttributesJSON, TypeIoTWirelessEventConfiguration, testRegion, "amzn-1")
	}

	assertContainsNative(t, st, iwTestDevice, iwTestCfgDevice)
	assertContainsNative(t, st, iwTestDevice, iwTestCfgDevEui)
	assertContainsNative(t, st, iwTestGateway, iwTestCfgGateway)
	assertContainsNative(t, st, iwTestGateway, iwTestCfgGwEui)
	assertContainsNative(t, st, iwTestPartner, iwTestCfgPartner)
	assertNoIWContainer(t, st, iwTestCfgUnlisted)
	assertContainsEdgesDeclared(t, st)
}

// assertNoIWContainer fails if any row contains the child.
func assertNoIWContainer(t *testing.T, st *store.Store, childNativeID string) {
	t.Helper()
	rels, err := st.ListRelationships()
	if err != nil {
		t.Fatalf("ListRelationships: %v", err)
	}
	childID := store.ResourceID("aws", testAccountID, childNativeID)
	for _, r := range rels {
		if r.Kind == store.RelContains && r.ToID == childID {
			t.Errorf("%s is contained by %s; want no parent", childNativeID, r.FromID)
		}
	}
}

func TestScanIoTWireless_NoEventConfigurations(t *testing.T) {
	st := newTestStore(t)
	handlers := iwHandlers(&parentCalls{})
	handlers["ListEventConfigurations"] = func(any) (any, error) { return &iotwireless.ListEventConfigurationsOutput{}, nil }
	total, err := scanIoTWirelessTopLevel(t, st, handlers)
	if err != nil {
		t.Fatalf("scanIoTWireless: %v", err)
	}
	if total != 4 {
		t.Errorf("total = %d; want 4 (the older-phase rows)", total)
	}
	assertIWCfgRows(t, st, nil)
}

// TestScanIoTWireless_EventConfigurationErrors fails ListEventConfigurations
// for the case's resource types and checks the warnings, the error and that
// the other resource types' rows are stored.
func TestScanIoTWireless_EventConfigurationErrors(t *testing.T) {
	gateway := iwtypes.EventNotificationResourceTypeWirelessGateway
	device := iwtypes.EventNotificationResourceTypeWirelessDevice
	cases := []struct {
		name      string
		failing   []iwtypes.EventNotificationResourceType
		err       error
		wantCode  string
		wantWarns int
	}{
		{name: "denied warns", failing: []iwtypes.EventNotificationResourceType{gateway}, err: iwDenied, wantWarns: 1},
		{name: "denied on two types warns once", failing: []iwtypes.EventNotificationResourceType{gateway, device}, err: iwDenied, wantWarns: 1},
		{name: "sidewalk region gap is silent", failing: []iwtypes.EventNotificationResourceType{gateway}, err: iwGated},
		{name: "validation exception is returned", failing: []iwtypes.EventNotificationResourceType{gateway}, err: iwBadInput, wantCode: "ValidationException"},
		{name: "transients warn once", failing: []iwtypes.EventNotificationResourceType{gateway, device}, err: iwBusy, wantWarns: 1},
		{name: "other error is returned", failing: []iwtypes.EventNotificationResourceType{gateway}, err: iwConflict, wantCode: "ConflictException"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := countOpWarnings(st, iwListEventConfigurationsOp)
			handlers := iwHandlers(&parentCalls{})
			failIWEventConfigs(handlers, tc.err, tc.failing...)
			_, err := scanIoTWirelessTopLevel(t, st, handlers)
			if tc.wantCode != "" {
				if !isAPIErrorCode(err, tc.wantCode) || isTransientNetworkError(err) {
					t.Fatalf("err = %v; want %s", err, tc.wantCode)
				}
			} else if err != nil {
				t.Fatalf("err = %v; want nil", err)
			}
			if *warns != tc.wantWarns {
				t.Errorf("%s warnings = %d; want %d", iwListEventConfigurationsOp, *warns, tc.wantWarns)
			}
			assertIWCfgRows(t, st, iwCfgRowsExcept(tc.failing...))
		})
	}
}

// TestScanIoTWireless_EventConfigurationPageErrorKeepsRows fails the second
// WirelessDevice page and checks the first page's row is kept.
func TestScanIoTWireless_EventConfigurationPageErrorKeepsRows(t *testing.T) {
	st := newTestStore(t)
	warns := countOpWarnings(st, iwListEventConfigurationsOp)
	handlers := iwHandlers(&parentCalls{})
	base := handlers["ListEventConfigurations"]
	handlers["ListEventConfigurations"] = func(in any) (any, error) {
		if in.(*iotwireless.ListEventConfigurationsInput).NextToken != nil {
			return nil, iwBusy
		}
		return base(in)
	}
	if _, err := scanIoTWirelessTopLevel(t, st, handlers); err != nil {
		t.Fatalf("err = %v; want nil", err)
	}
	if *warns != 1 {
		t.Errorf("warnings = %d; want 1", *warns)
	}
	assertIWCfgRows(t, st, append(iwCfgRowsExcept(iwtypes.EventNotificationResourceTypeWirelessDevice), iwTestCfgDevice))
}

func TestScanIoTWireless_EventConfigurationRepeatedTokenFails(t *testing.T) {
	st := newTestStore(t)
	handlers := iwHandlers(&parentCalls{})
	var calls atomic.Int32
	handlers["ListEventConfigurations"] = func(any) (any, error) {
		if calls.Add(1) > 10 {
			return nil, iwConflict
		}
		return &iotwireless.ListEventConfigurationsOutput{NextToken: sdkaws.String("same")}, nil
	}
	if _, err := scanIoTWirelessTopLevel(t, st, handlers); !errors.Is(err, errRepeatedNextToken) {
		t.Fatalf("err = %v; want errRepeatedNextToken", err)
	}
	// Each resource type: the first call, then one call with the repeated token.
	if n := calls.Load(); n != 6 {
		t.Errorf("ListEventConfigurations calls = %d; want 6", n)
	}
}

func TestScanIoTWireless_OlderPhaseErrorStillRunsEventConfigurations(t *testing.T) {
	st := newTestStore(t)
	handlers := iwHandlers(&parentCalls{})
	handlers["ListDestinations"] = func(any) (any, error) { return nil, iwConflict }
	_, err := scanIoTWirelessTopLevel(t, st, handlers)
	if !isAPIErrorCode(err, "ConflictException") {
		t.Fatalf("err = %v; want the ListDestinations ConflictException", err)
	}
	// No older phase stored a parent, so the rows are stored unlinked.
	assertIWCfgRows(t, st, iwCfgRowsExcept())
	assertNoIWContainer(t, st, iwTestCfgDevice)
}

func TestScanIoTWireless_EventConfigurationErrorAfterEveryOlderPhase(t *testing.T) {
	st := newTestStore(t)
	handlers := iwHandlers(&parentCalls{})
	handlers["ListEventConfigurations"] = func(any) (any, error) { return nil, iwConflict }
	_, err := scanIoTWirelessTopLevel(t, st, handlers)
	if !isAPIErrorCode(err, "ConflictException") {
		t.Fatalf("err = %v; want ConflictException", err)
	}
	if _, ok := rowsByNativeID(t, st, TypeIoTWirelessTaskDefinition)[iwTestTaskDef]; !ok {
		t.Errorf("task definition %s not stored; the last older phase must run before event configurations", iwTestTaskDef)
	}
}

func TestScanIoTWireless_OlderTransientDoesNotMaskEventConfigurationError(t *testing.T) {
	st := newTestStore(t)
	warns := countOpWarnings(st, "iotwireless")
	handlers := iwHandlers(&parentCalls{})
	handlers["ListFuotaTasks"] = func(any) (any, error) { return nil, iwBusy }
	failIWEventConfigs(handlers, iwConflict, iwtypes.EventNotificationResourceTypeWirelessGateway)
	_, err := scanIoTWirelessTopLevel(t, st, handlers)
	if !isAPIErrorCode(err, "ConflictException") || isTransientNetworkError(err) {
		t.Fatalf("err = %v; want the event configuration ConflictException, not transient", err)
	}
	if *warns != 1 {
		t.Errorf("iotwireless warnings = %d; want 1 (the demoted ListFuotaTasks error)", *warns)
	}
}

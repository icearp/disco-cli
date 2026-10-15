package aws

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/iotfleetwise"
	iotfwtypes "github.com/aws/aws-sdk-go-v2/service/iotfleetwise/types"
	smithy "github.com/aws/smithy-go"
	"github.com/icearp/disco-cli/store"
)

type stubIoTFW struct {
	listCampaignsErr error
}

func (s *stubIoTFW) ListCampaigns(_ context.Context, _ *iotfleetwise.ListCampaignsInput, _ ...func(*iotfleetwise.Options)) (*iotfleetwise.ListCampaignsOutput, error) {
	if s.listCampaignsErr != nil {
		return nil, s.listCampaignsErr
	}
	return &iotfleetwise.ListCampaignsOutput{}, nil
}

func (s *stubIoTFW) ListDecoderManifests(_ context.Context, _ *iotfleetwise.ListDecoderManifestsInput, _ ...func(*iotfleetwise.Options)) (*iotfleetwise.ListDecoderManifestsOutput, error) {
	return &iotfleetwise.ListDecoderManifestsOutput{}, nil
}

func (s *stubIoTFW) ListDecoderManifestNetworkInterfaces(_ context.Context, _ *iotfleetwise.ListDecoderManifestNetworkInterfacesInput, _ ...func(*iotfleetwise.Options)) (*iotfleetwise.ListDecoderManifestNetworkInterfacesOutput, error) {
	return &iotfleetwise.ListDecoderManifestNetworkInterfacesOutput{}, nil
}

func (s *stubIoTFW) ListDecoderManifestSignals(_ context.Context, _ *iotfleetwise.ListDecoderManifestSignalsInput, _ ...func(*iotfleetwise.Options)) (*iotfleetwise.ListDecoderManifestSignalsOutput, error) {
	return &iotfleetwise.ListDecoderManifestSignalsOutput{}, nil
}

func (s *stubIoTFW) ListFleets(_ context.Context, _ *iotfleetwise.ListFleetsInput, _ ...func(*iotfleetwise.Options)) (*iotfleetwise.ListFleetsOutput, error) {
	return &iotfleetwise.ListFleetsOutput{}, nil
}

func (s *stubIoTFW) ListModelManifests(_ context.Context, _ *iotfleetwise.ListModelManifestsInput, _ ...func(*iotfleetwise.Options)) (*iotfleetwise.ListModelManifestsOutput, error) {
	return &iotfleetwise.ListModelManifestsOutput{}, nil
}

func (s *stubIoTFW) ListSignalCatalogs(_ context.Context, _ *iotfleetwise.ListSignalCatalogsInput, _ ...func(*iotfleetwise.Options)) (*iotfleetwise.ListSignalCatalogsOutput, error) {
	return &iotfleetwise.ListSignalCatalogsOutput{}, nil
}

func (s *stubIoTFW) ListStateTemplates(_ context.Context, _ *iotfleetwise.ListStateTemplatesInput, _ ...func(*iotfleetwise.Options)) (*iotfleetwise.ListStateTemplatesOutput, error) {
	return &iotfleetwise.ListStateTemplatesOutput{}, nil
}

func (s *stubIoTFW) ListVehicles(_ context.Context, _ *iotfleetwise.ListVehiclesInput, _ ...func(*iotfleetwise.Options)) (*iotfleetwise.ListVehiclesOutput, error) {
	return &iotfleetwise.ListVehiclesOutput{}, nil
}

// TestGateIoTFleetWiseClosedToAccount verifies an empty-message
// AccessDeniedException (closed-to-new-customers) yields a not-entitled
// sentinel — the account can't self-enable IoT FleetWise — so the dispatcher
// renders `(account: not entitled)` instead of N per-phase warnings.
func TestGateIoTFleetWiseClosedToAccount(t *testing.T) {
	stub := &stubIoTFW{
		listCampaignsErr: &smithy.GenericAPIError{Code: "AccessDeniedException", Message: ""},
	}
	err := gateIoTFleetWise(context.Background(), stub)
	if err == nil {
		t.Fatal("expected not-entitled sentinel; got nil")
	}
	if !errors.Is(err, errServiceNotEntitled) {
		t.Fatalf("expected errServiceNotEntitled; got %v", err)
	}
}

// TestGateIoTFleetWiseRealIAMDenial verifies a per-op IAM denial with an
// action-identifying message does NOT trip the gate — skipIfAccessDenied
// still handles it as a warning.
func TestGateIoTFleetWiseRealIAMDenial(t *testing.T) {
	stub := &stubIoTFW{
		listCampaignsErr: &smithy.GenericAPIError{
			Code:    "AccessDeniedException",
			Message: "User: arn:aws:iam::123:user/x is not authorized to perform: iotfleetwise:ListCampaigns",
		},
	}
	err := gateIoTFleetWise(context.Background(), stub)
	if err != nil {
		t.Fatalf("expected nil from gate on real IAM denial; got %v", err)
	}
}

// TestGateIoTFleetWiseSuccess verifies a successful probe call returns nil
// (not disabled) so the phase loop runs.
func TestGateIoTFleetWiseSuccess(t *testing.T) {
	stub := &stubIoTFW{}
	err := gateIoTFleetWise(context.Background(), stub)
	if err != nil {
		t.Fatalf("expected nil from gate on success; got %v", err)
	}
}

const (
	iotFWTestManifestA      = "arn:aws:iotfleetwise:us-east-1:123456789012:decoder-manifest/dm-a"
	iotFWTestManifestB      = "arn:aws:iotfleetwise:us-east-1:123456789012:decoder-manifest/dm-b"
	iotFWTestManifestGone   = "arn:aws:iotfleetwise:us-east-1:123456789012:decoder-manifest/dm-gone"
	iotFWTestManifestNoName = "arn:aws:iotfleetwise:us-east-1:123456789012:decoder-manifest/dm-noname"
	iotFWTestVehicle        = "arn:aws:iotfleetwise:us-east-1:123456789012:vehicle/car-1"

	iotFWTestSignalA1 = iotFWTestManifestA + "/signal/Vehicle.Speed"
	iotFWTestSignalA2 = iotFWTestManifestA + "/signal/Vehicle.Powertrain.Rpm"
	iotFWTestSignalB1 = iotFWTestManifestB + "/signal/Vehicle.Speed"

	iotFWTestIfaceA1 = iotFWTestManifestA + "/network-interface/can0"
	iotFWTestIfaceA2 = iotFWTestManifestA + "/network-interface/obd-1"
)

var (
	iotFWDenied = apiErr("AccessDeniedException", "User: arn:aws:iam::123456789012:role/r is not authorized to perform: iotfleetwise:List")
	// iotFWClosed is the empty-message denial of a service closed to the account.
	iotFWClosed    = apiErr("AccessDeniedException", "")
	iotFWGone      = apiErr("ResourceNotFoundException", "gone")
	iotFWBusy      = apiErr("InternalServerException", "try later")
	iotFWBadInput  = apiErr("ValidationException", "bad")
	iotFWConflict  = apiErr("ConflictException", "unexpected")
	iotFWChildOps  = []string{"ListDecoderManifestSignals", "ListDecoderManifestNetworkInterfaces"}
	iotFWChildRows = map[string][]string{
		TypeIoTFWDecoderManifestSignal:           {iotFWTestSignalA1, iotFWTestSignalA2, iotFWTestSignalB1},
		TypeIoTFWDecoderManifestNetworkInterface: {iotFWTestIfaceA1, iotFWTestIfaceA2},
	}
)

// iotFWHandlers answers every IoT FleetWise op the scan calls. The decoder
// manifests are dm-a, dm-b, dm-gone (deleted after listing) and one with no
// name, whose children are never listed; the last older phase stores one
// vehicle. dm-a has two pages of signal decoders and of network interfaces
// (each with a keyless element to skip), dm-b one signal decoder and no
// network interfaces. Every child call is recorded in calls under its op,
// keyed by manifest name, and fails unless MaxResults is the model's cap.
func iotFWHandlers(calls map[string]*parentCalls) map[string]func(in any) (any, error) {
	for _, op := range iotFWChildOps {
		calls[op] = &parentCalls{}
	}
	badLimit := apiErr("ValidationException", "maxResults must be at most 100")
	return map[string]func(in any) (any, error){
		"ListCampaigns": func(any) (any, error) { return &iotfleetwise.ListCampaignsOutput{}, nil },
		"ListDecoderManifests": func(any) (any, error) {
			return &iotfleetwise.ListDecoderManifestsOutput{Summaries: []iotfwtypes.DecoderManifestSummary{
				{Arn: sdkaws.String(iotFWTestManifestA), Name: sdkaws.String("dm-a"), Status: iotfwtypes.ManifestStatusActive},
				{Arn: sdkaws.String(iotFWTestManifestB), Name: sdkaws.String("dm-b"), Status: iotfwtypes.ManifestStatusDraft},
				{Arn: sdkaws.String(iotFWTestManifestGone), Name: sdkaws.String("dm-gone"), Status: iotfwtypes.ManifestStatusActive},
				{Arn: sdkaws.String(iotFWTestManifestNoName)},
			}}, nil
		},
		"ListFleets":         func(any) (any, error) { return &iotfleetwise.ListFleetsOutput{}, nil },
		"ListModelManifests": func(any) (any, error) { return &iotfleetwise.ListModelManifestsOutput{}, nil },
		"ListSignalCatalogs": func(any) (any, error) { return &iotfleetwise.ListSignalCatalogsOutput{}, nil },
		"ListStateTemplates": func(any) (any, error) { return &iotfleetwise.ListStateTemplatesOutput{}, nil },
		"ListVehicles": func(any) (any, error) {
			return &iotfleetwise.ListVehiclesOutput{VehicleSummaries: []iotfwtypes.VehicleSummary{
				{Arn: sdkaws.String(iotFWTestVehicle), VehicleName: sdkaws.String("car-1")},
			}}, nil
		},
		"ListDecoderManifestSignals": func(in any) (any, error) {
			p := in.(*iotfleetwise.ListDecoderManifestSignalsInput)
			calls["ListDecoderManifestSignals"].add(sdkaws.ToString(p.Name))
			if sdkaws.ToInt32(p.MaxResults) != iotFWPageLimit {
				return nil, badLimit
			}
			signal := func(name, iface string) iotfwtypes.SignalDecoder {
				return iotfwtypes.SignalDecoder{FullyQualifiedName: sdkaws.String(name), InterfaceId: sdkaws.String(iface), Type: iotfwtypes.SignalDecoderTypeCanSignal}
			}
			var out iotfleetwise.ListDecoderManifestSignalsOutput
			switch sdkaws.ToString(p.Name) {
			case "dm-a":
				out.SignalDecoders, out.NextToken = evsPage(p.NextToken,
					[]iotfwtypes.SignalDecoder{signal("Vehicle.Speed", "can0"), {InterfaceId: sdkaws.String("can0")}},
					[]iotfwtypes.SignalDecoder{signal("Vehicle.Powertrain.Rpm", "obd-1")})
			case "dm-b":
				out.SignalDecoders = []iotfwtypes.SignalDecoder{signal("Vehicle.Speed", "can0")}
			case "dm-gone":
				return nil, iotFWGone
			default:
				return nil, iotFWConflict
			}
			return &out, nil
		},
		"ListDecoderManifestNetworkInterfaces": func(in any) (any, error) {
			p := in.(*iotfleetwise.ListDecoderManifestNetworkInterfacesInput)
			calls["ListDecoderManifestNetworkInterfaces"].add(sdkaws.ToString(p.Name))
			if sdkaws.ToInt32(p.MaxResults) != iotFWPageLimit {
				return nil, badLimit
			}
			var out iotfleetwise.ListDecoderManifestNetworkInterfacesOutput
			switch sdkaws.ToString(p.Name) {
			case "dm-a":
				out.NetworkInterfaces, out.NextToken = evsPage(p.NextToken,
					[]iotfwtypes.NetworkInterface{
						{InterfaceId: sdkaws.String("can0"), Type: iotfwtypes.NetworkInterfaceTypeCanInterface, CanInterface: &iotfwtypes.CanInterface{Name: sdkaws.String("can0")}},
						{Type: iotfwtypes.NetworkInterfaceTypeCanInterface},
					},
					[]iotfwtypes.NetworkInterface{{InterfaceId: sdkaws.String("obd-1"), Type: iotfwtypes.NetworkInterfaceTypeObdInterface}})
			case "dm-b":
			case "dm-gone":
				return nil, iotFWGone
			default:
				return nil, iotFWConflict
			}
			return &out, nil
		},
	}
}

func scanIoTFWTopLevel(t *testing.T, st *store.Store, handlers map[string]func(in any) (any, error)) (int, error) {
	t.Helper()
	acct := newTestAccount(testAccountID)
	acct.cfg = opStubConfig(handlers, iotFWDenied)
	total, _, err := scanIoTFleetWise(context.Background(), acct, testRegion, st, testScanID)
	return total, err
}

// assertIoTFWRows fails unless the stored rows of typ are exactly want.
func assertIoTFWRows(t *testing.T, st *store.Store, typ string, want []string) {
	t.Helper()
	rows := rowsByNativeID(t, st, typ)
	got := make([]string, 0, len(rows))
	for id := range rows {
		got = append(got, id)
	}
	slices.Sort(got)
	want = slices.Sorted(slices.Values(want))
	if !slices.Equal(got, want) {
		t.Errorf("%s rows = %v; want %v", typ, got, want)
	}
}

func TestScanIoTFleetWise_StoresDecoderManifestChildren(t *testing.T) {
	st := newTestStore(t)
	var warnings atomic.Int32
	st.OnWarn = func(store.ScanWarning) { warnings.Add(1) }
	calls := map[string]*parentCalls{}
	total, err := scanIoTFWTopLevel(t, st, iotFWHandlers(calls))
	if err != nil {
		t.Fatalf("scanIoTFleetWise: %v", err)
	}
	if n := warnings.Load(); n != 0 {
		t.Errorf("warnings = %d; want 0", n)
	}
	// 4 manifests + 1 vehicle + 3 signal decoders + 2 network interfaces.
	if total != 10 {
		t.Errorf("total = %d; want 10", total)
	}
	for typ, want := range iotFWChildRows {
		assertIoTFWRows(t, st, typ, want)
	}
	for _, op := range iotFWChildOps {
		if got, want := calls[op].distinct(), []string{"dm-a", "dm-b", "dm-gone"}; !slices.Equal(got, want) {
			t.Errorf("%s manifests = %v; want %v", op, got, want)
		}
	}

	signal := rowsByNativeID(t, st, TypeIoTFWDecoderManifestSignal)[iotFWTestSignalA1]
	var sigAttrs iotfwtypes.SignalDecoder
	if err := json.Unmarshal([]byte(signal.AttributesJSON), &sigAttrs); err != nil {
		t.Fatalf("decode signal attrs: %v", err)
	}
	if signal.Type != TypeIoTFWDecoderManifestSignal || sv(signal.Region) != testRegion || sv(signal.Name) != "Vehicle.Speed" ||
		sv(sigAttrs.InterfaceId) != "can0" || sigAttrs.Type != iotfwtypes.SignalDecoderTypeCanSignal {
		t.Errorf("signal row = type %q region %q name %q interface %q attrs type %q; want %q %q %q %q %q",
			signal.Type, sv(signal.Region), sv(signal.Name), sv(sigAttrs.InterfaceId), sigAttrs.Type,
			TypeIoTFWDecoderManifestSignal, testRegion, "Vehicle.Speed", "can0", iotfwtypes.SignalDecoderTypeCanSignal)
	}

	iface := rowsByNativeID(t, st, TypeIoTFWDecoderManifestNetworkInterface)[iotFWTestIfaceA1]
	var ifaceAttrs iotfwtypes.NetworkInterface
	if err := json.Unmarshal([]byte(iface.AttributesJSON), &ifaceAttrs); err != nil {
		t.Fatalf("decode network interface attrs: %v", err)
	}
	if iface.Type != TypeIoTFWDecoderManifestNetworkInterface || sv(iface.Region) != testRegion || sv(iface.Name) != "can0" ||
		ifaceAttrs.CanInterface == nil || sv(ifaceAttrs.CanInterface.Name) != "can0" {
		t.Errorf("network interface row = type %q region %q name %q attrs %s; want %q %q %q with CanInterface.Name can0",
			iface.Type, sv(iface.Region), sv(iface.Name), iface.AttributesJSON,
			TypeIoTFWDecoderManifestNetworkInterface, testRegion, "can0")
	}

	assertContainsNative(t, st, iotFWTestManifestA, iotFWTestSignalA1)
	assertContainsNative(t, st, iotFWTestManifestA, iotFWTestSignalA2)
	assertContainsNative(t, st, iotFWTestManifestB, iotFWTestSignalB1)
	assertContainsNative(t, st, iotFWTestManifestA, iotFWTestIfaceA1)
	assertContainsNative(t, st, iotFWTestManifestA, iotFWTestIfaceA2)
	assertContainsEdgesDeclared(t, st)
}

func TestScanIoTFleetWise_ManifestsWithoutChildren(t *testing.T) {
	st := newTestStore(t)
	handlers := iotFWHandlers(map[string]*parentCalls{})
	handlers["ListDecoderManifestSignals"] = func(any) (any, error) { return &iotfleetwise.ListDecoderManifestSignalsOutput{}, nil }
	handlers["ListDecoderManifestNetworkInterfaces"] = func(any) (any, error) {
		return &iotfleetwise.ListDecoderManifestNetworkInterfacesOutput{}, nil
	}
	total, err := scanIoTFWTopLevel(t, st, handlers)
	if err != nil {
		t.Fatalf("scanIoTFleetWise: %v", err)
	}
	if total != 5 {
		t.Errorf("total = %d; want 5 (4 manifests + 1 vehicle)", total)
	}
	for typ := range iotFWChildRows {
		assertIoTFWRows(t, st, typ, nil)
	}
}

// TestScanIoTFleetWise_ManifestChildErrors fails one child op for the case's
// manifests (dm-b, or dm-b and dm-gone) and checks what the phase keeps: dm-a's rows of that op, every row
// of the other op and of the older phases.
func TestScanIoTFleetWise_ManifestChildErrors(t *testing.T) {
	opLabels := map[string]string{
		"ListDecoderManifestSignals":           iotFWListDecoderManifestSignalsOp,
		"ListDecoderManifestNetworkInterfaces": iotFWListDecoderManifestNetworkInterfacesOp,
	}
	opTypes := map[string]string{
		"ListDecoderManifestSignals":           TypeIoTFWDecoderManifestSignal,
		"ListDecoderManifestNetworkInterfaces": TypeIoTFWDecoderManifestNetworkInterface,
	}
	cases := []struct {
		name      string
		failing   []string
		err       error
		wantErr   bool
		wantWarns int
	}{
		{name: "denied warns once", failing: []string{"dm-b", "dm-gone"}, err: iotFWDenied, wantWarns: 1},
		{name: "closed to the account is silent", failing: []string{"dm-b", "dm-gone"}, err: iotFWClosed},
		{name: "transient warns once", failing: []string{"dm-b", "dm-gone"}, err: iotFWBusy, wantWarns: 1},
		{name: "validation exception on one manifest warns", failing: []string{"dm-b"}, err: iotFWBadInput, wantWarns: 1},
		{name: "validation exception on two manifests warns once", failing: []string{"dm-b", "dm-gone"}, err: iotFWBadInput, wantWarns: 1},
		{name: "other error fails the phase", failing: []string{"dm-b", "dm-gone"}, err: iotFWConflict, wantErr: true},
	}
	for _, op := range iotFWChildOps {
		for _, tc := range cases {
			t.Run(op+"/"+tc.name, func(t *testing.T) {
				st := newTestStore(t)
				warns := countOpWarnings(st, opLabels[op])
				handlers := iotFWHandlers(map[string]*parentCalls{})
				base := handlers[op]
				handlers[op] = func(in any) (any, error) {
					var name string
					switch p := in.(type) {
					case *iotfleetwise.ListDecoderManifestSignalsInput:
						name = sdkaws.ToString(p.Name)
					case *iotfleetwise.ListDecoderManifestNetworkInterfacesInput:
						name = sdkaws.ToString(p.Name)
					}
					if slices.Contains(tc.failing, name) {
						return nil, tc.err
					}
					return base(in)
				}
				_, err := scanIoTFWTopLevel(t, st, handlers)
				if tc.wantErr {
					if !isAPIErrorCode(err, "ConflictException") {
						t.Fatalf("err = %v; want ConflictException", err)
					}
				} else if err != nil {
					t.Fatalf("err = %v; want nil", err)
				}
				if *warns != tc.wantWarns {
					t.Errorf("%s warnings = %d; want %d", opLabels[op], *warns, tc.wantWarns)
				}
				for typ, want := range iotFWChildRows {
					if typ != opTypes[op] {
						assertIoTFWRows(t, st, typ, want)
					}
				}
				if tc.wantErr {
					return
				}
				var fromA []string
				for _, id := range iotFWChildRows[opTypes[op]] {
					if strings.HasPrefix(id, iotFWTestManifestA+"/") {
						fromA = append(fromA, id)
					}
				}
				assertIoTFWRows(t, st, opTypes[op], fromA)
				if _, ok := rowsByNativeID(t, st, TypeIoTFWVehicle)[iotFWTestVehicle]; !ok {
					t.Errorf("vehicle %s not stored", iotFWTestVehicle)
				}
			})
		}
	}
}

func TestScanIoTFleetWise_OlderPhaseErrorStillRunsManifestChildren(t *testing.T) {
	st := newTestStore(t)
	handlers := iotFWHandlers(map[string]*parentCalls{})
	handlers["ListVehicles"] = func(any) (any, error) { return nil, iotFWConflict }
	_, err := scanIoTFWTopLevel(t, st, handlers)
	if !isAPIErrorCode(err, "ConflictException") {
		t.Fatalf("err = %v; want the ListVehicles ConflictException", err)
	}
	for typ, want := range iotFWChildRows {
		assertIoTFWRows(t, st, typ, want)
	}
}

func TestScanIoTFleetWise_ChildErrorsAfterEveryOlderPhase(t *testing.T) {
	st := newTestStore(t)
	handlers := iotFWHandlers(map[string]*parentCalls{})
	for _, op := range iotFWChildOps {
		handlers[op] = func(any) (any, error) { return nil, iotFWConflict }
	}
	_, err := scanIoTFWTopLevel(t, st, handlers)
	if !isAPIErrorCode(err, "ConflictException") {
		t.Fatalf("err = %v; want ConflictException", err)
	}
	if _, ok := rowsByNativeID(t, st, TypeIoTFWVehicle)[iotFWTestVehicle]; !ok {
		t.Errorf("vehicle %s not stored; the last older phase must run before the child phases", iotFWTestVehicle)
	}
}

func TestScanIoTFleetWise_OlderTransientDoesNotMaskChildError(t *testing.T) {
	st := newTestStore(t)
	warns := countOpWarnings(st, "iotfleetwise")
	handlers := iotFWHandlers(map[string]*parentCalls{})
	handlers["ListFleets"] = func(any) (any, error) { return nil, iotFWBusy }
	handlers["ListDecoderManifestSignals"] = func(any) (any, error) { return nil, iotFWConflict }
	_, err := scanIoTFWTopLevel(t, st, handlers)
	if !isAPIErrorCode(err, "ConflictException") || isTransientNetworkError(err) {
		t.Fatalf("err = %v; want the signal ConflictException, not transient", err)
	}
	if *warns != 1 {
		t.Errorf("iotfleetwise warnings = %d; want 1 (the demoted ListFleets error)", *warns)
	}
	assertIoTFWRows(t, st, TypeIoTFWDecoderManifestNetworkInterface, iotFWChildRows[TypeIoTFWDecoderManifestNetworkInterface])
}

// iotFWFeatureGated is ListStateTemplates' answer to an account without the
// state-template feature.
var iotFWFeatureGated = apiErr("AccessDeniedException", "Account is not authorized to use this feature")

// iotFWEmptyHandlers is iotFWHandlers with no resources anywhere.
func iotFWEmptyHandlers() map[string]func(in any) (any, error) {
	handlers := iotFWHandlers(map[string]*parentCalls{})
	handlers["ListDecoderManifests"] = func(any) (any, error) { return &iotfleetwise.ListDecoderManifestsOutput{}, nil }
	handlers["ListVehicles"] = func(any) (any, error) { return &iotfleetwise.ListVehiclesOutput{}, nil }
	return handlers
}

func TestScanIoTFleetWise_StateTemplateGateAfterRowsWarns(t *testing.T) {
	st := newTestStore(t)
	warns := countOpWarnings(st, iotFWListStateTemplatesOp)
	handlers := iotFWHandlers(map[string]*parentCalls{})
	handlers["ListStateTemplates"] = func(any) (any, error) { return nil, iotFWFeatureGated }
	total, err := scanIoTFWTopLevel(t, st, handlers)
	if err != nil {
		t.Fatalf("err = %v; want nil (errServiceDisabled would discard the stored rows)", err)
	}
	if *warns != 1 {
		t.Errorf("%s warnings = %d; want 1", iotFWListStateTemplatesOp, *warns)
	}
	if total != 10 {
		t.Errorf("total = %d; want 10", total)
	}
	if _, ok := rowsByNativeID(t, st, TypeIoTFWVehicle)[iotFWTestVehicle]; !ok {
		t.Errorf("vehicle %s not stored; the phases after state templates must still run", iotFWTestVehicle)
	}
	for typ, want := range iotFWChildRows {
		assertIoTFWRows(t, st, typ, want)
	}
}

func TestScanIoTFleetWise_StateTemplateGateWithNothingStoredIsDisabled(t *testing.T) {
	st := newTestStore(t)
	handlers := iotFWEmptyHandlers()
	handlers["ListStateTemplates"] = func(any) (any, error) { return nil, iotFWFeatureGated }
	if _, err := scanIoTFWTopLevel(t, st, handlers); !errors.Is(err, errServiceDisabled) {
		t.Fatalf("err = %v; want errServiceDisabled", err)
	}
}

func TestScanIoTFleetWise_StateTemplateGateDoesNotHideHardError(t *testing.T) {
	st := newTestStore(t)
	warns := countOpWarnings(st, iotFWListStateTemplatesOp)
	handlers := iotFWEmptyHandlers()
	handlers["ListStateTemplates"] = func(any) (any, error) { return nil, iotFWFeatureGated }
	handlers["ListVehicles"] = func(any) (any, error) { return nil, iotFWConflict }
	_, err := scanIoTFWTopLevel(t, st, handlers)
	if !isAPIErrorCode(err, "ConflictException") || errors.Is(err, errServiceDisabled) {
		t.Fatalf("err = %v; want the ListVehicles ConflictException without errServiceDisabled", err)
	}
	if *warns != 1 {
		t.Errorf("%s warnings = %d; want 1", iotFWListStateTemplatesOp, *warns)
	}
}

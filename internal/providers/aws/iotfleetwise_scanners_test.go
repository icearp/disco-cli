package aws

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"net"
	"slices"
	"strings"
	"sync"
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

func (s *stubIoTFW) ListTagsForResource(_ context.Context, _ *iotfleetwise.ListTagsForResourceInput, _ ...func(*iotfleetwise.Options)) (*iotfleetwise.ListTagsForResourceOutput, error) {
	return &iotfleetwise.ListTagsForResourceOutput{}, nil
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
// keyed by manifest name, and fails unless MaxResults is the model's cap. No
// resource has tags.
func iotFWHandlers(calls map[string]*parentCalls) map[string]func(in any) (any, error) {
	for _, op := range iotFWChildOps {
		calls[op] = &parentCalls{}
	}
	badLimit := apiErr("ValidationException", "maxResults must be at most 100")
	return map[string]func(in any) (any, error){
		"ListTagsForResource": func(any) (any, error) { return &iotfleetwise.ListTagsForResourceOutput{}, nil },
		"ListCampaigns":       func(any) (any, error) { return &iotfleetwise.ListCampaignsOutput{}, nil },
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
	nameless := countOpWarnings(st, iotFWListDecoderManifestsOp)
	calls := map[string]*parentCalls{}
	total, err := scanIoTFWTopLevel(t, st, iotFWHandlers(calls))
	if err != nil {
		t.Fatalf("scanIoTFleetWise: %v", err)
	}
	// The one warning: the manifest without a name, whose children are not
	// listed.
	if n := warnings.Load(); n != 1 || *nameless != 1 {
		t.Errorf("warnings = %d (%s %d); want 1, the nameless manifest", n, iotFWListDecoderManifestsOp, *nameless)
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

// A manifest refused as not ready on its second page keeps, and counts, the
// rows its first page stored.
func TestScanIoTFWDecoderManifestSignals_NotReadyKeepsEarlierPages(t *testing.T) {
	st := newTestStore(t)
	upsertTestResource(t, st, "aws", testAccountID, TypeIoTFWDecoderManifest, iotFWTestManifestA, testRegion, "{}")
	warns := countOpWarnings(st, iotFWListDecoderManifestSignalsOp)
	client := iotfleetwise.NewFromConfig(opStubConfig(map[string]func(in any) (any, error){
		"ListDecoderManifestSignals": func(in any) (any, error) {
			if in.(*iotfleetwise.ListDecoderManifestSignalsInput).NextToken != nil {
				return nil, iotFWBadInput
			}
			return &iotfleetwise.ListDecoderManifestSignalsOutput{
				SignalDecoders: []iotfwtypes.SignalDecoder{{FullyQualifiedName: sdkaws.String("Vehicle.Speed")}},
				NextToken:      sdkaws.String("p2"),
			}, nil
		},
	}, iotFWDenied))
	total, _, err := scanIoTFWDecoderManifestSignals(context.Background(), client, newTestAccount(testAccountID), testRegion, st, testScanID,
		[]childParent{{id: "dm-a", arn: iotFWTestManifestA}})
	if err != nil || total != 1 || *warns != 1 {
		t.Errorf("err, total, warnings = %v, %d, %d; want nil, 1, 1", err, total, *warns)
	}
	assertIoTFWRows(t, st, TypeIoTFWDecoderManifestSignal, []string{iotFWDecoderManifestChildNativeID(iotFWTestManifestA, iotFWSignalKind, "Vehicle.Speed")})
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
	warns := countOpWarnings(st, iotFWListFleetsOp)
	handlers := iotFWHandlers(map[string]*parentCalls{})
	handlers["ListFleets"] = func(any) (any, error) { return nil, iotFWBusy }
	handlers["ListDecoderManifestSignals"] = func(any) (any, error) { return nil, iotFWConflict }
	_, err := scanIoTFWTopLevel(t, st, handlers)
	if !isAPIErrorCode(err, "ConflictException") || isTransientNetworkError(err) {
		t.Fatalf("err = %v; want the signal ConflictException, not transient", err)
	}
	if *warns != 1 {
		t.Errorf("%s warnings = %d; want 1 (the demoted error)", iotFWListFleetsOp, *warns)
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

// iotFWKind is an account-wide listing the tag fixture answers with two
// pages: resource "tagged" on the first, "bare" on the second.
type iotFWKind struct {
	op, typ, resource string
	page              func(arn, name string, next *string) any
}

func iotFWKinds() []iotFWKind {
	return []iotFWKind{
		{"ListCampaigns", TypeIoTFWCampaign, "campaign", func(arn, name string, next *string) any {
			return &iotfleetwise.ListCampaignsOutput{CampaignSummaries: []iotfwtypes.CampaignSummary{{Arn: &arn, Name: &name}}, NextToken: next}
		}},
		{"ListFleets", TypeIoTFWFleet, "fleet", func(arn, name string, next *string) any {
			return &iotfleetwise.ListFleetsOutput{FleetSummaries: []iotfwtypes.FleetSummary{{Arn: &arn, Id: &name}}, NextToken: next}
		}},
		{"ListModelManifests", TypeIoTFWModelManifest, "model-manifest", func(arn, name string, next *string) any {
			return &iotfleetwise.ListModelManifestsOutput{Summaries: []iotfwtypes.ModelManifestSummary{{Arn: &arn, Name: &name}}, NextToken: next}
		}},
		{"ListSignalCatalogs", TypeIoTFWSignalCatalog, "signal-catalog", func(arn, name string, next *string) any {
			return &iotfleetwise.ListSignalCatalogsOutput{Summaries: []iotfwtypes.SignalCatalogSummary{{Arn: &arn, Name: &name}}, NextToken: next}
		}},
		{"ListStateTemplates", TypeIoTFWStateTemplate, "state-template", func(arn, name string, next *string) any {
			return &iotfleetwise.ListStateTemplatesOutput{Summaries: []iotfwtypes.StateTemplateSummary{{Arn: &arn, Name: &name}}, NextToken: next}
		}},
		{"ListVehicles", TypeIoTFWVehicle, "vehicle", func(arn, name string, next *string) any {
			return &iotfleetwise.ListVehiclesOutput{VehicleSummaries: []iotfwtypes.VehicleSummary{{Arn: &arn, VehicleName: &name}}, NextToken: next}
		}},
	}
}

func iotFWKindOf(t *testing.T, op string) iotFWKind {
	t.Helper()
	for _, k := range iotFWKinds() {
		if k.op == op {
			return k
		}
	}
	t.Fatalf("no fixture kind lists with %s", op)
	return iotFWKind{}
}

func iotFWName(k iotFWKind, suffix string) string { return k.resource + "-" + suffix }

func iotFWARN(k iotFWKind, suffix string) string {
	return "arn:aws:iotfleetwise:" + testRegion + ":" + testAccountID + ":" + k.resource + "/" + iotFWName(k, suffix)
}

// iotFWTagFixtureTotal is every row of the tag fixture: two per kind, the
// four decoder manifests and their five children.
const iotFWTagFixtureTotal = 2*6 + 4 + 5

// iotFWCalls records the stubbed calls: the MaxResults each list op was sent
// (0 for none) and the ARNs sent to ListTagsForResource, which it answers
// from tags or errs, both keyed by ARN.
type iotFWCalls struct {
	mu         sync.Mutex
	maxResults map[string][]int32
	tagARNs    []string
	tags       map[string]map[string]string
	errs       map[string]error
}

// newIoTFWCalls tags each kind's "tagged" resource and decoder manifest dm-a
// with their type.
func newIoTFWCalls() *iotFWCalls {
	c := &iotFWCalls{maxResults: map[string][]int32{}, tags: map[string]map[string]string{}, errs: map[string]error{}}
	for _, k := range iotFWKinds() {
		c.tags[iotFWARN(k, "tagged")] = map[string]string{"type": k.typ}
	}
	c.tags[iotFWTestManifestA] = map[string]string{"type": TypeIoTFWDecoderManifest}
	return c
}

func (c *iotFWCalls) record(op string, in any) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.maxResults[op] = append(c.maxResults[op], sdkaws.ToInt32(bacField[*int32](in, "MaxResults")))
}

func (c *iotFWCalls) listTags(in any) (any, error) {
	arn := sv(in.(*iotfleetwise.ListTagsForResourceInput).ResourceARN)
	c.mu.Lock()
	c.tagARNs = append(c.tagARNs, arn)
	err, tags := c.errs[arn], c.tags[arn]
	c.mu.Unlock()
	if err != nil {
		return nil, err
	}
	var list []iotfwtypes.Tag
	for k, v := range tags {
		list = append(list, iotfwtypes.Tag{Key: sdkaws.String(k), Value: sdkaws.String(v)})
	}
	return &iotfleetwise.ListTagsForResourceOutput{Tags: list}, nil
}

func (c *iotFWCalls) twoPages(k iotFWKind) func(any) (any, error) {
	return func(in any) (any, error) {
		c.record(k.op, in)
		suffix, next := "tagged", sdkaws.String("t2")
		if bacField[*string](in, "NextToken") != nil {
			suffix, next = "bare", nil
		}
		return k.page(iotFWARN(k, suffix), iotFWName(k, suffix), next), nil
	}
}

// handlers is iotFWHandlers with every kind listed in two pages and the tag
// op answered from c.
func (c *iotFWCalls) handlers() map[string]func(any) (any, error) {
	h := iotFWHandlers(map[string]*parentCalls{})
	h["ListTagsForResource"] = c.listTags
	manifests := h["ListDecoderManifests"]
	h["ListDecoderManifests"] = func(in any) (any, error) {
		c.record("ListDecoderManifests", in)
		return manifests(in)
	}
	for _, k := range iotFWKinds() {
		h[k.op] = c.twoPages(k)
	}
	return h
}

func iotFWScan(ctx context.Context, st *store.Store, handlers map[string]func(any) (any, error)) (int, int, error) {
	acct := newTestAccount(testAccountID)
	acct.cfg = opStubConfig(handlers, iotFWDenied)
	return scanIoTFleetWise(ctx, acct, testRegion, st, testScanID)
}

// Every taggable type stores both pages of its listing, named as listed,
// with the tags ListTagsForResource returns for its ARN and nil for a
// resource without tags. Exactly the ARN of every taggable row is sent to the
// tag op (the decoder manifest children have none); every listing is sent
// MaxResults 100 (the gate probe 1).
func TestScanIoTFleetWise_StoresTagsOfEveryType(t *testing.T) {
	st := newTestStore(t)
	calls := newIoTFWCalls()

	total, inserted, err := iotFWScan(context.Background(), st, calls.handlers())
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != iotFWTagFixtureTotal || inserted != iotFWTagFixtureTotal {
		t.Errorf("total, inserted = %d, %d; want %d", total, inserted, iotFWTagFixtureTotal)
	}
	var wantARNs []string
	for _, k := range iotFWKinds() {
		for _, suffix := range []string{"tagged", "bare"} {
			arn := iotFWARN(k, suffix)
			wantARNs = append(wantARNs, arn)
			r, ok := rowsByNativeID(t, st, k.typ)[arn]
			if !ok {
				t.Errorf("%s %s missing", k.typ, arn)
				continue
			}
			if sv(r.Name) != iotFWName(k, suffix) || sv(r.Region) != testRegion {
				t.Errorf("%s Name, Region = %q, %q; want %q, %q", arn, sv(r.Name), sv(r.Region), iotFWName(k, suffix), testRegion)
			}
			if suffix == "tagged" {
				assertTagsMap(t, r.TagsJSON, map[string]string{"type": k.typ})
			} else if r.TagsJSON != nil {
				t.Errorf("%s TagsJSON = %s; want nil", arn, *r.TagsJSON)
			}
		}
	}
	manifests := rowsByNativeID(t, st, TypeIoTFWDecoderManifest)
	assertTagsMap(t, manifests[iotFWTestManifestA].TagsJSON, map[string]string{"type": TypeIoTFWDecoderManifest})
	if r := manifests[iotFWTestManifestB]; r.TagsJSON != nil {
		t.Errorf("%s TagsJSON = %s; want nil", iotFWTestManifestB, *r.TagsJSON)
	}
	wantARNs = append(wantARNs, iotFWTestManifestA, iotFWTestManifestB, iotFWTestManifestGone, iotFWTestManifestNoName)
	slices.Sort(wantARNs)
	slices.Sort(calls.tagARNs)
	if !slices.Equal(calls.tagARNs, wantARNs) {
		t.Errorf("ListTagsForResource ARNs = %v\nwant %v", calls.tagARNs, wantARNs)
	}
	wantMax := map[string][]int32{"ListDecoderManifests": {100}}
	for _, k := range iotFWKinds() {
		wantMax[k.op] = []int32{100, 100}
	}
	wantMax["ListCampaigns"] = []int32{1, 100, 100}
	for op, want := range wantMax {
		if got := calls.maxResults[op]; !slices.Equal(got, want) {
			t.Errorf("%s MaxResults = %v; want %v", op, got, want)
		}
	}
}

// A rescan stores the same rows without inserting any.
func TestScanIoTFleetWise_RescanInsertsNone(t *testing.T) {
	st := newTestStore(t)
	if _, _, err := iotFWScan(context.Background(), st, newIoTFWCalls().handlers()); err != nil {
		t.Fatalf("first scan: %v", err)
	}
	total, inserted, err := iotFWScan(context.Background(), st, newIoTFWCalls().handlers())
	if err != nil {
		t.Fatalf("rescan: %v", err)
	}
	if total != iotFWTagFixtureTotal || inserted != 0 {
		t.Errorf("rescan total, inserted = %d, %d; want %d, 0", total, inserted, iotFWTagFixtureTotal)
	}
}

var (
	iotFWThrottle = apiErr("ThrottlingException", "Rate exceeded")
	iotFWNX       = &net.DNSError{Err: "no such host", Name: "iotfleetwise.us-east-1.amazonaws.com", IsNotFound: true}
)

// A failed tag read: ResourceNotFoundException, the op's modeled not-found,
// drops the row silently (a manifest's children with it); a denial keeps the
// row untagged and warns once under the tag op; a throttle after retries
// keeps it and is a transient error; ValidationException keeps it and is a
// hard error. Each holds for a row written page by page (a vehicle) and a
// parent listing's row (a decoder manifest).
func TestScanIoTFleetWise_TagReadFailures(t *testing.T) {
	for _, target := range []struct {
		typ, arn string
		goneLost int
	}{
		{TypeIoTFWVehicle, iotFWARN(iotFWKindOf(t, "ListVehicles"), "tagged"), 1},
		{TypeIoTFWDecoderManifest, iotFWTestManifestB, 2},
	} {
		t.Run(target.typ, func(t *testing.T) {
			for _, c := range []struct {
				name      string
				err       error
				outcome   serviceOutcome
				code      string
				wantKept  bool
				wantWarns int
			}{
				{"not found", iotFWGone, 0, "", false, 0},
				{"denied", iotFWDenied, 0, "", true, 1},
				{"transient", iotFWThrottle, outcomeTransient, "", true, 0},
				{"other error", iotFWBadInput, outcomeError, "ValidationException", true, 0},
			} {
				t.Run(c.name, func(t *testing.T) {
					st := newTestStore(t)
					warnings := bacWarnings(st, iotFWListTagsOp)
					calls := newIoTFWCalls()
					calls.errs[target.arn] = c.err

					total, _, err := iotFWScan(context.Background(), st, calls.handlers())
					if c.outcome == 0 && err != nil {
						t.Fatalf("err = %v; want nil", err)
					}
					if c.outcome != 0 {
						assertGlueOutcome(t, err, c.outcome)
					}
					if c.code != "" {
						assertAPICode(t, err, c.code)
					}
					wantTotal := iotFWTagFixtureTotal
					if !c.wantKept {
						wantTotal -= target.goneLost
					}
					if total != wantTotal {
						t.Errorf("total = %d; want %d", total, wantTotal)
					}
					r, ok := rowsByNativeID(t, st, target.typ)[target.arn]
					if ok != c.wantKept {
						t.Fatalf("row stored = %v; want %v", ok, c.wantKept)
					}
					if ok && r.TagsJSON != nil {
						t.Errorf("TagsJSON = %s; want nil", *r.TagsJSON)
					}
					if len(*warnings) != c.wantWarns {
						t.Fatalf("%s warnings = %v; want %d", iotFWListTagsOp, *warnings, c.wantWarns)
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

// A decoder manifest the tag read finds deleted is no parent: its children
// are not listed.
func TestScanIoTFleetWise_ManifestGoneIsNoParent(t *testing.T) {
	st := newTestStore(t)
	calls := newIoTFWCalls()
	calls.errs[iotFWTestManifestB] = iotFWGone
	children := map[string]*parentCalls{}
	handlers := calls.handlers()
	base := iotFWHandlers(children)
	for _, op := range iotFWChildOps {
		handlers[op] = base[op]
	}
	if _, _, err := iotFWScan(context.Background(), st, handlers); err != nil {
		t.Fatalf("scan: %v", err)
	}
	for _, op := range iotFWChildOps {
		if got, want := children[op].distinct(), []string{"dm-a", "dm-gone"}; !slices.Equal(got, want) {
			t.Errorf("%s manifests = %v; want %v", op, got, want)
		}
	}
}

// A throttled tag read beside a hard failure of the same op is demoted to
// one warning, so the hard error decides the outcome.
func TestScanIoTFleetWise_TagThrottleDemotedBesideHardError(t *testing.T) {
	st := newTestStore(t)
	warns := countOpWarnings(st, iotFWListTagsOp)
	calls := newIoTFWCalls()
	calls.errs[iotFWARN(iotFWKindOf(t, "ListFleets"), "tagged")] = iotFWThrottle
	calls.errs[iotFWARN(iotFWKindOf(t, "ListVehicles"), "bare")] = iotFWBadInput

	_, _, err := iotFWScan(context.Background(), st, calls.handlers())
	assertGlueOutcome(t, err, outcomeError)
	assertAPICode(t, err, "ValidationException")
	if *warns != 1 {
		t.Errorf("%s warnings = %d; want 1", iotFWListTagsOp, *warns)
	}
}

// A failed write stops the scan: no later page or phase is read, and no
// warning is sent about the rows it lost or about state templates.
func TestScanIoTFleetWise_FailedWriteStopsWithoutWarning(t *testing.T) {
	st := newTestStore(t)
	tagWarns := countOpWarnings(st, iotFWListTagsOp)
	gateWarns := countOpWarnings(st, iotFWListStateTemplatesOp)
	refuseStoreWrites(t, st, TypeIoTFWVehicle)
	calls := newIoTFWCalls()
	calls.errs[iotFWARN(iotFWKindOf(t, "ListVehicles"), "tagged")] = iotFWDenied
	children := map[string]*parentCalls{}
	handlers := calls.handlers()
	base := iotFWHandlers(children)
	for _, op := range iotFWChildOps {
		handlers[op] = base[op]
	}
	handlers["ListStateTemplates"] = func(any) (any, error) { return nil, iotFWFeatureGated }

	_, _, err := iotFWScan(context.Background(), st, handlers)
	if !errors.Is(err, store.ErrStoreWrite) {
		t.Fatalf("err = %v; want store.ErrStoreWrite", err)
	}
	if *tagWarns != 0 || *gateWarns != 0 {
		t.Errorf("warnings %s, %s = %d, %d; want 0, 0", iotFWListTagsOp, iotFWListStateTemplatesOp, *tagWarns, *gateWarns)
	}
	if n := len(calls.maxResults["ListVehicles"]); n != 1 {
		t.Errorf("ListVehicles calls = %d; want 1 (no page after the failed write)", n)
	}
	for _, op := range iotFWChildOps {
		if got := children[op].distinct(); len(got) != 0 {
			t.Errorf("%s called for %v after the failed write", op, got)
		}
	}
}

// Vehicles are written a page at a time: the first page's vehicle is stored
// with its tags before the second page is read.
func TestScanIoTFleetWise_VehiclesWrittenPerPage(t *testing.T) {
	st := newTestStore(t)
	calls := newIoTFWCalls()
	handlers := calls.handlers()
	k := iotFWKindOf(t, "ListVehicles")
	list := handlers[k.op]
	var storedFirst bool
	handlers[k.op] = func(in any) (any, error) {
		if bacField[*string](in, "NextToken") != nil {
			rows, err := st.ListResources(store.ResourceFilter{Providers: []string{"aws"}, Types: []string{k.typ}})
			for _, r := range rows {
				storedFirst = storedFirst || (err == nil && r.NativeID == iotFWARN(k, "tagged") && r.TagsJSON != nil)
			}
		}
		return list(in)
	}
	if _, _, err := iotFWScan(context.Background(), st, handlers); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if !storedFirst {
		t.Error("first page's vehicle not stored with tags before the second page was read")
	}
}

// A listing's failed page keeps the pages read before it, and its error
// stops no later phase.
func TestScanIoTFleetWise_ListFailureKeepsPagesAndRunsEveryPhase(t *testing.T) {
	st := newTestStore(t)
	handlers := newIoTFWCalls().handlers()
	k := iotFWKindOf(t, "ListCampaigns")
	// Call 1 is the gate's probe, call 2 the first page.
	handlers[k.op] = failOnCall(3, iotFWConflict, handlers[k.op])

	total, _, err := iotFWScan(context.Background(), st, handlers)
	assertGlueOutcome(t, err, outcomeError)
	assertAPICode(t, err, "ConflictException")
	if total != iotFWTagFixtureTotal-1 {
		t.Errorf("total = %d; want %d", total, iotFWTagFixtureTotal-1)
	}
	if _, ok := rowsByNativeID(t, st, k.typ)[iotFWARN(k, "tagged")]; !ok {
		t.Error("campaign from the page before the failure missing")
	}
	if got := len(rowsByNativeID(t, st, TypeIoTFWVehicle)); got != 2 {
		t.Errorf("vehicles = %d; want 2", got)
	}
	for typ, want := range iotFWChildRows {
		assertIoTFWRows(t, st, typ, want)
	}
}

// A repeated token ends the listing as an error, keeping its pages.
func TestScanIoTFleetWise_RepeatedTokenKeepsPages(t *testing.T) {
	st := newTestStore(t)
	handlers := newIoTFWCalls().handlers()
	k := iotFWKindOf(t, "ListVehicles")
	handlers[k.op] = func(in any) (any, error) {
		suffix := "tagged"
		if bacField[*string](in, "NextToken") != nil {
			suffix = "bare"
		}
		return k.page(iotFWARN(k, suffix), iotFWName(k, suffix), sdkaws.String("same")), nil
	}

	total, _, err := iotFWScan(context.Background(), st, handlers)
	if !errors.Is(err, errRepeatedNextToken) {
		t.Fatalf("err = %v; want errRepeatedNextToken", err)
	}
	if total != iotFWTagFixtureTotal {
		t.Errorf("total = %d; want %d", total, iotFWTagFixtureTotal)
	}
}

// An element without an ARN is not stored and is warned about once under its
// listing; its siblings are stored.
func TestScanIoTFleetWise_ElementWithoutARNWarns(t *testing.T) {
	st := newTestStore(t)
	warns := bacWarnings(st, iotFWListFleetsOp)
	handlers := newIoTFWCalls().handlers()
	k := iotFWKindOf(t, "ListFleets")
	kept := iotFWARN(k, "kept")
	handlers[k.op] = func(any) (any, error) {
		return &iotfleetwise.ListFleetsOutput{FleetSummaries: []iotfwtypes.FleetSummary{{Id: sdkaws.String("no-arn")}, {Arn: &kept, Id: sdkaws.String("kept")}}}, nil
	}

	if _, _, err := iotFWScan(context.Background(), st, handlers); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if rows := rowsByNativeID(t, st, k.typ); len(rows) != 1 || rows[kept].NativeID != kept {
		t.Errorf("fleets = %v; want %s only", slices.Sorted(maps.Keys(rows)), kept)
	}
	if len(*warns) != 1 {
		t.Fatalf("%s warnings = %v; want 1", iotFWListFleetsOp, *warns)
	}
	if !strings.Contains((*warns)[0].Message, "no-arn") || (*warns)[0].Scope != testAccountID+"/"+testRegion {
		t.Errorf("warning = %+v; want it to name key no-arn with Scope %s/%s", (*warns)[0], testAccountID, testRegion)
	}
}

// A listing refused as closed to new customers on its first page is read
// as empty, silently. Refused on a later page, it keeps the pages before and
// warns once under its op, since rows after that page were not read. The
// other listings run either way.
func TestScanIoTFleetWise_ClosedListing(t *testing.T) {
	for _, c := range []struct {
		name      string
		call      int
		wantRows  int
		wantWarns int
	}{
		{"first page is silent", 1, 0, 0},
		{"later page warns", 2, 1, 1},
	} {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := bacWarnings(st, iotFWListFleetsOp)
			handlers := newIoTFWCalls().handlers()
			k := iotFWKindOf(t, "ListFleets")
			handlers[k.op] = failOnCall(c.call, iotFWClosed, handlers[k.op])

			total, _, err := iotFWScan(context.Background(), st, handlers)
			if err != nil {
				t.Fatalf("err = %v; want nil", err)
			}
			if want := iotFWTagFixtureTotal - 2 + c.wantRows; total != want {
				t.Errorf("total = %d; want %d", total, want)
			}
			rows := rowsByNativeID(t, st, k.typ)
			if len(rows) != c.wantRows {
				t.Errorf("fleets = %v; want %d", slices.Sorted(maps.Keys(rows)), c.wantRows)
			}
			if _, ok := rows[iotFWARN(k, "tagged")]; ok != (c.wantRows == 1) {
				t.Errorf("first page's fleet stored = %v; want %v", ok, c.wantRows == 1)
			}
			if len(*warns) != c.wantWarns {
				t.Fatalf("%s warnings = %v; want %d", iotFWListFleetsOp, *warns, c.wantWarns)
			}
			for _, w := range *warns {
				if w.Scope != testAccountID+"/"+testRegion {
					t.Errorf("warning Scope = %q; want %s/%s", w.Scope, testAccountID, testRegion)
				}
			}
		})
	}
}

// A denied listing warns once under its own op; every other listing runs.
func TestScanIoTFleetWise_DeniedListingWarnsUnderItsOp(t *testing.T) {
	st := newTestStore(t)
	warns := countOpWarnings(st, iotFWListVehiclesOp)
	handlers := newIoTFWCalls().handlers()
	delete(handlers, "ListVehicles")

	total, _, err := iotFWScan(context.Background(), st, handlers)
	if err != nil {
		t.Fatalf("err = %v; want nil", err)
	}
	if total != iotFWTagFixtureTotal-2 || *warns != 1 {
		t.Errorf("total, %s warnings = %d, %d; want %d, 1", iotFWListVehiclesOp, total, *warns, iotFWTagFixtureTotal-2)
	}
}

// The state-template feature gate on a later page keeps the first page and
// warns once, since rows were stored.
func TestScanIoTFleetWise_StateTemplateGateOnLaterPageKeepsRows(t *testing.T) {
	st := newTestStore(t)
	warns := countOpWarnings(st, iotFWListStateTemplatesOp)
	handlers := newIoTFWCalls().handlers()
	k := iotFWKindOf(t, "ListStateTemplates")
	handlers[k.op] = failOnCall(2, iotFWFeatureGated, handlers[k.op])

	if _, _, err := iotFWScan(context.Background(), st, handlers); err != nil {
		t.Fatalf("err = %v; want nil", err)
	}
	if _, ok := rowsByNativeID(t, st, k.typ)[iotFWARN(k, "tagged")]; !ok {
		t.Error("state template from the first page missing")
	}
	if *warns != 1 {
		t.Errorf("%s warnings = %d; want 1", iotFWListStateTemplatesOp, *warns)
	}
}

// Once ctx ends no further phase starts, and the stop is one member of the
// error, although the phase in flight saw it too.
func TestScanIoTFleetWise_ContextEndStopsOnce(t *testing.T) {
	st := newTestStore(t)
	calls := newIoTFWCalls()
	handlers := calls.handlers()
	children := map[string]*parentCalls{}
	base := iotFWHandlers(children)
	for _, op := range iotFWChildOps {
		handlers[op] = base[op]
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	k := iotFWKindOf(t, "ListModelManifests")
	list := handlers[k.op]
	handlers[k.op] = func(in any) (any, error) {
		if bacField[*string](in, "NextToken") != nil {
			cancel()
			return nil, context.Canceled
		}
		return list(in)
	}

	_, _, err := iotFWScan(ctx, st, handlers)
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
	if n := len(calls.maxResults["ListVehicles"]); n != 0 {
		t.Errorf("ListVehicles called %d times after ctx ended", n)
	}
	for _, op := range iotFWChildOps {
		if got := children[op].distinct(); len(got) != 0 {
			t.Errorf("%s called for %v after ctx ended", op, got)
		}
	}
}

// An NXDOMAIN after rows were stored is a failure of this scan, not a region
// gap: the counts are kept. With nothing stored it stays region-unavailable.
func TestScanIoTFleetWise_NXDOMAIN(t *testing.T) {
	t.Run("later phase after rows were stored", func(t *testing.T) {
		st := newTestStore(t)
		handlers := newIoTFWCalls().handlers()
		handlers["ListVehicles"] = func(any) (any, error) { return nil, iotFWNX }
		total, _, err := iotFWScan(context.Background(), st, handlers)
		assertGlueOutcome(t, err, outcomeError)
		if total != iotFWTagFixtureTotal-2 {
			t.Errorf("total = %d; want the %d other rows counted", total, iotFWTagFixtureTotal-2)
		}
	})
	t.Run("tag read after the listing", func(t *testing.T) {
		st := newTestStore(t)
		calls := newIoTFWCalls()
		calls.errs[iotFWARN(iotFWKindOf(t, "ListCampaigns"), "tagged")] = iotFWNX
		_, _, err := iotFWScan(context.Background(), st, calls.handlers())
		assertGlueOutcome(t, err, outcomeError)
	})
	t.Run("nothing stored", func(t *testing.T) {
		st := newTestStore(t)
		warns := countWarnings(st)
		acct := newTestAccount(testAccountID)
		acct.cfg = opStubConfig(nil, iotFWNX)
		_, _, err := scanIoTFleetWise(context.Background(), acct, testRegion, st, testScanID)
		assertGlueOutcome(t, err, outcomeUnavailable)
		if *warns != 0 {
			t.Errorf("warnings = %d; want 0", *warns)
		}
	})
}

// A repeated NextToken in a manifest's child listing ends it with
// errRepeatedNextToken, keeping both pages it read and every other
// manifest's rows.
func TestScanIoTFleetWise_ChildRepeatedTokenKeepsPages(t *testing.T) {
	for _, op := range iotFWChildOps {
		t.Run(op, func(t *testing.T) {
			st := newTestStore(t)
			handlers := iotFWHandlers(map[string]*parentCalls{})
			base := handlers[op]
			handlers[op] = func(in any) (any, error) {
				out, err := base(in)
				if sv(bacField[*string](in, "Name")) != "dm-a" || err != nil {
					return out, err
				}
				switch o := out.(type) {
				case *iotfleetwise.ListDecoderManifestSignalsOutput:
					o.NextToken = sdkaws.String("same")
				case *iotfleetwise.ListDecoderManifestNetworkInterfacesOutput:
					o.NextToken = sdkaws.String("same")
				}
				return out, nil
			}
			_, err := scanIoTFWTopLevel(t, st, handlers)
			if !errors.Is(err, errRepeatedNextToken) {
				t.Fatalf("err = %v; want errRepeatedNextToken", err)
			}
			for typ, want := range iotFWChildRows {
				assertIoTFWRows(t, st, typ, want)
			}
		})
	}
}

// A failed write of a manifest's children is returned as a store write
// failure, never dropped.
func TestScanIoTFleetWise_ChildWriteFailureReturned(t *testing.T) {
	st := newTestStore(t)
	refuseStoreWrites(t, st, TypeIoTFWDecoderManifestSignal)
	_, err := scanIoTFWTopLevel(t, st, iotFWHandlers(map[string]*parentCalls{}))
	if !errors.Is(err, store.ErrStoreWrite) {
		t.Fatalf("err = %v; want store.ErrStoreWrite", err)
	}
}

// When ctx ends during a vehicle page's tag reads, the stop is one member of
// the error, the second vehicle page is not read and no later phase runs.
func TestScanIoTFleetWise_ContextEndsDuringTagReads(t *testing.T) {
	st := newTestStore(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	calls := newIoTFWCalls()
	handlers := calls.handlers()
	children := map[string]*parentCalls{}
	base := iotFWHandlers(children)
	for _, op := range iotFWChildOps {
		handlers[op] = base[op]
	}
	k := iotFWKindOf(t, "ListVehicles")
	listTags := handlers["ListTagsForResource"]
	handlers["ListTagsForResource"] = func(in any) (any, error) {
		if sv(in.(*iotfleetwise.ListTagsForResourceInput).ResourceARN) == iotFWARN(k, "tagged") {
			cancel()
			return nil, context.Canceled
		}
		return listTags(in)
	}

	_, _, err := iotFWScan(ctx, st, handlers)
	stops := 0
	for _, m := range bacTopMembers(err) {
		if errors.Is(m, context.Canceled) {
			stops++
		}
	}
	if stops != 1 {
		t.Errorf("members carrying the stop = %d; want 1 (err %v)", stops, err)
	}
	if n := len(calls.maxResults["ListVehicles"]); n != 1 {
		t.Errorf("ListVehicles calls = %d; want 1", n)
	}
	rows := rowsByNativeID(t, st, k.typ)
	if _, ok := rows[iotFWARN(k, "bare")]; ok {
		t.Error("second page's vehicle stored after ctx ended")
	}
	if r, ok := rows[iotFWARN(k, "tagged")]; !ok || r.TagsJSON != nil {
		t.Errorf("first page's vehicle stored = %v; want stored untagged", ok)
	}
	for _, op := range iotFWChildOps {
		if got := children[op].distinct(); len(got) != 0 {
			t.Errorf("%s called for %v after ctx ended", op, got)
		}
	}
}

// Rows of an account in another partition are sent to the tag op under the
// ARNs their listings return.
func TestScanIoTFleetWise_TagReadInAccountPartition(t *testing.T) {
	acct := partitionAccount("aws-us-gov")
	region := acct.Regions[0]
	st := newTestStore(t)
	calls := newIoTFWCalls()
	vehicle := "arn:aws-us-gov:iotfleetwise:" + region + ":" + acct.ID + ":vehicle/gov-car"
	manifest := "arn:aws-us-gov:iotfleetwise:" + region + ":" + acct.ID + ":decoder-manifest/gov-dm"
	calls.tags[vehicle] = map[string]string{"k": "vehicle"}
	calls.tags[manifest] = map[string]string{"k": "manifest"}
	empty := func(any) (any, error) { return &iotfleetwise.ListCampaignsOutput{}, nil }
	acct.cfg = opStubConfig(map[string]func(any) (any, error){
		"ListTagsForResource": calls.listTags,
		"ListCampaigns":       empty,
		"ListFleets":          func(any) (any, error) { return &iotfleetwise.ListFleetsOutput{}, nil },
		"ListModelManifests":  func(any) (any, error) { return &iotfleetwise.ListModelManifestsOutput{}, nil },
		"ListSignalCatalogs":  func(any) (any, error) { return &iotfleetwise.ListSignalCatalogsOutput{}, nil },
		"ListStateTemplates":  func(any) (any, error) { return &iotfleetwise.ListStateTemplatesOutput{}, nil },
		"ListDecoderManifests": func(any) (any, error) {
			return &iotfleetwise.ListDecoderManifestsOutput{Summaries: []iotfwtypes.DecoderManifestSummary{{Arn: &manifest}}}, nil
		},
		"ListVehicles": func(any) (any, error) {
			return &iotfleetwise.ListVehiclesOutput{VehicleSummaries: []iotfwtypes.VehicleSummary{{Arn: &vehicle, VehicleName: sdkaws.String("gov-car")}}}, nil
		},
	}, iotFWDenied)

	if _, _, err := scanIoTFleetWise(context.Background(), acct, region, st, testScanID); err != nil {
		t.Fatalf("scan: %v", err)
	}
	slices.Sort(calls.tagARNs)
	if want := []string{manifest, vehicle}; !slices.Equal(calls.tagARNs, want) {
		t.Errorf("ListTagsForResource ARNs = %v; want %v", calls.tagARNs, want)
	}
	for _, w := range []struct{ typ, arn, tag string }{{TypeIoTFWVehicle, vehicle, "vehicle"}, {TypeIoTFWDecoderManifest, manifest, "manifest"}} {
		r, ok := rowsByNativeID(t, st, w.typ)[w.arn]
		if !ok {
			t.Fatalf("%s %s missing", w.typ, w.arn)
		}
		assertTagsMap(t, r.TagsJSON, map[string]string{"k": w.tag})
	}
}

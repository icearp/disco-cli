package aws

import (
	"context"
	"encoding/json"
	"slices"
	"testing"
	"time"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/evs"
	evstypes "github.com/aws/aws-sdk-go-v2/service/evs/types"
	"github.com/icearp/disco-cli/store"
)

const (
	evsTestEnvA   = "arn:aws:evs:us-east-1:123456789012:environment/env-aaaaaaaaaa"
	evsTestEnvB   = "arn:aws:evs:us-east-1:123456789012:environment/env-bbbbbbbbbb"
	evsTestEnvDel = "arn:aws:evs:us-east-1:123456789012:environment/env-dddddddddd"

	evsTestHostA1 = evsTestEnvA + "/host/esx-a1"
	evsTestHostA2 = evsTestEnvA + "/host/esx-a2"
	evsTestHostB1 = evsTestEnvB + "/host/esx-b1"

	evsTestVlanA1     = evsTestEnvA + "/vlan/30"
	evsTestVlanA2     = evsTestEnvA + "/vlan/10"
	evsTestVlanNoName = evsTestEnvA + "/vlan/31"
	evsTestVlanHcx2   = evsTestEnvA + "/vlan/33"

	evsTestConnVcA  = evsTestEnvA + "/connector/cnctr-vcaaaaaaaa"
	evsTestConnSddc = evsTestEnvA + "/connector/cnctr-sddcaaaaaa"
	evsTestConnVcB  = evsTestEnvB + "/connector/cnctr-vcbbbbbbbb"

	evsTestEntA1 = evsTestConnVcA + "/vm-entitlement/WINDOWS_SERVER/vm-101"
	evsTestEntA2 = evsTestConnVcA + "/vm-entitlement/WINDOWS_SERVER/vm-102"
	evsTestEntB1 = evsTestConnVcB + "/vm-entitlement/WINDOWS_SERVER/vm-201"
)

var (
	evsDenied   = apiErr("AccessDeniedException", "User: arn:aws:iam::123456789012:role/r is not authorized to perform: evs:List")
	evsBadInput = apiErr("ValidationException", "bad")
	evsGone     = apiErr("ResourceNotFoundException", "gone")
	// evsUnexpected is an error code the model does not list for the child ops.
	evsUnexpected = apiErr("ConflictException", "unexpected")
	evsCreated    = time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
)

// evsPage answers a paginated call: page one (no token) carries first and the
// token "page2", page two carries second. A nil second means a single page.
func evsPage[T any](token *string, first, second []T) ([]T, *string) {
	if token == nil && second != nil {
		return first, sdkaws.String("page2")
	}
	if token == nil {
		return first, nil
	}
	return second, nil
}

// evsHandlers answers every EVS op the scan calls: environments A and B plus a
// deleted one whose children are never listed. A has two pages of hosts (with
// a nameless and a deleted host to skip), two pages of VLANs and a vCenter and
// an SDDC Manager connector; B has one host, no VLANs and a vCenter connector.
// The vCenter connectors' entitlements are A: two pages, B: one page. Every
// child call is recorded in calls under its op, keyed by parent id, and fails
// unless MaxResults is the model's cap.
func evsHandlers(calls map[string]*parentCalls) map[string]func(in any) (any, error) {
	for _, op := range []string{"ListEnvironmentHosts", "ListEnvironmentVlans", "ListEnvironmentConnectors", "ListVmEntitlements"} {
		calls[op] = &parentCalls{}
	}
	badLimit := apiErr("ValidationException", "maxResults must be at most 100")
	return map[string]func(in any) (any, error){
		"ListEnvironments": func(any) (any, error) {
			return &evs.ListEnvironmentsOutput{EnvironmentSummaries: []evstypes.EnvironmentSummary{
				{EnvironmentArn: sdkaws.String(evsTestEnvA), EnvironmentId: sdkaws.String("env-aaaaaaaaaa"), EnvironmentState: evstypes.EnvironmentStateCreated},
				{EnvironmentArn: sdkaws.String(evsTestEnvB), EnvironmentId: sdkaws.String("env-bbbbbbbbbb"), EnvironmentState: evstypes.EnvironmentStateCreated},
				{EnvironmentArn: sdkaws.String(evsTestEnvDel), EnvironmentId: sdkaws.String("env-dddddddddd"), EnvironmentState: evstypes.EnvironmentStateDeleted},
			}}, nil
		},
		"ListEnvironmentHosts": func(in any) (any, error) {
			p := in.(*evs.ListEnvironmentHostsInput)
			calls["ListEnvironmentHosts"].add(sdkaws.ToString(p.EnvironmentId))
			if sdkaws.ToInt32(p.MaxResults) != evsPageLimit {
				return nil, badLimit
			}
			host := func(name string, state evstypes.HostState) evstypes.Host {
				return evstypes.Host{
					HostName: sdkaws.String(name), HostState: state, CreatedAt: &evsCreated,
					Ec2InstanceId: sdkaws.String("i-0" + name), InstanceType: evstypes.InstanceTypeI4iMetal,
				}
			}
			var out evs.ListEnvironmentHostsOutput
			switch sdkaws.ToString(p.EnvironmentId) {
			case "env-aaaaaaaaaa":
				out.EnvironmentHosts, out.NextToken = evsPage(p.NextToken,
					[]evstypes.Host{host("esx-a1", evstypes.HostStateCreated), {HostState: evstypes.HostStateCreated}, host("esx-gone", evstypes.HostStateDeleted)},
					[]evstypes.Host{host("esx-a2", evstypes.HostStateUpdating)})
			case "env-bbbbbbbbbb":
				out.EnvironmentHosts = []evstypes.Host{host("esx-b1", evstypes.HostStateCreated)}
			default:
				return nil, evsUnexpected
			}
			return &out, nil
		},
		"ListEnvironmentVlans": func(in any) (any, error) {
			p := in.(*evs.ListEnvironmentVlansInput)
			calls["ListEnvironmentVlans"].add(sdkaws.ToString(p.EnvironmentId))
			if sdkaws.ToInt32(p.MaxResults) != evsPageLimit {
				return nil, badLimit
			}
			vlan := func(name string, id int32, state evstypes.VlanState) evstypes.Vlan {
				return evstypes.Vlan{FunctionName: sdkaws.String(name), VlanId: sdkaws.Int32(id), VlanState: state, CreatedAt: &evsCreated, SubnetId: sdkaws.String("subnet-0" + name)}
			}
			var out evs.ListEnvironmentVlansOutput
			switch sdkaws.ToString(p.EnvironmentId) {
			case "env-aaaaaaaaaa":
				out.EnvironmentVlans, out.NextToken = evsPage(p.NextToken,
					[]evstypes.Vlan{vlan("hcx", 30, evstypes.VlanStateCreated), {VlanId: sdkaws.Int32(31), FunctionName: sdkaws.String("")}, vlan("nsxUplink", 32, evstypes.VlanStateDeleted)},
					[]evstypes.Vlan{vlan("vmkManagement", 10, evstypes.VlanStateCreated), {FunctionName: sdkaws.String("no id")}, vlan("hcx", 33, evstypes.VlanStateCreated)})
			case "env-bbbbbbbbbb":
			default:
				return nil, evsUnexpected
			}
			return &out, nil
		},
		"ListEnvironmentConnectors": func(in any) (any, error) {
			p := in.(*evs.ListEnvironmentConnectorsInput)
			calls["ListEnvironmentConnectors"].add(sdkaws.ToString(p.EnvironmentId))
			if sdkaws.ToInt32(p.MaxResults) != evsPageLimit {
				return nil, badLimit
			}
			conn := func(id string, typ evstypes.ConnectorType, state evstypes.ConnectorState) evstypes.Connector {
				return evstypes.Connector{
					ConnectorId: sdkaws.String(id), Type: typ, State: state, CreatedAt: &evsCreated,
					ApplianceFqdn: sdkaws.String(id + ".vcf.example"), EnvironmentId: p.EnvironmentId,
					SecretArn: sdkaws.String("arn:aws:secretsmanager:us-east-1:123456789012:secret:" + id + "-AbCdEf"),
				}
			}
			var out evs.ListEnvironmentConnectorsOutput
			switch sdkaws.ToString(p.EnvironmentId) {
			case "env-aaaaaaaaaa":
				out.Connectors, out.NextToken = evsPage(p.NextToken,
					[]evstypes.Connector{conn("cnctr-vcaaaaaaaa", evstypes.ConnectorTypeVcenter, evstypes.ConnectorStateActive), {Type: evstypes.ConnectorTypeVcenter}},
					[]evstypes.Connector{conn("cnctr-sddcaaaaaa", evstypes.ConnectorTypeSddcManager, evstypes.ConnectorStateActive), conn("cnctr-gonegonego", evstypes.ConnectorTypeVcenter, evstypes.ConnectorStateDeleted)})
			case "env-bbbbbbbbbb":
				out.Connectors = []evstypes.Connector{conn("cnctr-vcbbbbbbbb", evstypes.ConnectorTypeVcenter, evstypes.ConnectorStateActive)}
			default:
				return nil, evsUnexpected
			}
			return &out, nil
		},
		"ListVmEntitlements": func(in any) (any, error) {
			p := in.(*evs.ListVmEntitlementsInput)
			calls["ListVmEntitlements"].add(sdkaws.ToString(p.EnvironmentId) + "/" + sdkaws.ToString(p.ConnectorId) + "/" + string(p.EntitlementType))
			if sdkaws.ToInt32(p.MaxResults) != evsPageLimit {
				return nil, badLimit
			}
			ent := func(vmID, name string, status evstypes.EntitlementStatus) evstypes.VmEntitlement {
				return evstypes.VmEntitlement{
					VmId: sdkaws.String(vmID), VmName: sdkaws.String(name), Status: status, Type: p.EntitlementType,
					ConnectorId: p.ConnectorId, EnvironmentId: p.EnvironmentId,
				}
			}
			var out evs.ListVmEntitlementsOutput
			switch sdkaws.ToString(p.ConnectorId) {
			case "cnctr-vcaaaaaaaa":
				out.Entitlements, out.NextToken = evsPage(p.NextToken,
					[]evstypes.VmEntitlement{ent("vm-101", "web-1", evstypes.EntitlementStatusCreated), {VmName: sdkaws.String("no id")}, ent("vm-109", "old", evstypes.EntitlementStatusDeleted)},
					[]evstypes.VmEntitlement{ent("vm-102", "web-2", evstypes.EntitlementStatusAtRisk)})
			case "cnctr-vcbbbbbbbb":
				out.Entitlements = []evstypes.VmEntitlement{ent("vm-201", "db-1", evstypes.EntitlementStatusCreated)}
			default:
				return nil, evsUnexpected
			}
			return &out, nil
		},
	}
}

func scanEVSTopLevel(t *testing.T, st *store.Store, handlers map[string]func(in any) (any, error)) (int, error) {
	t.Helper()
	acct := newTestAccount(testAccountID)
	acct.cfg = opStubConfig(handlers, evsDenied)
	total, _, err := scanEVS(context.Background(), acct, testRegion, st, testScanID)
	return total, err
}

var evsAllChildRows = map[string][]string{
	TypeEVSEnvironmentHost:      {evsTestHostA1, evsTestHostA2, evsTestHostB1},
	TypeEVSEnvironmentVlan:      {evsTestVlanA2, evsTestVlanA1, evsTestVlanNoName, evsTestVlanHcx2},
	TypeEVSEnvironmentConnector: {evsTestConnSddc, evsTestConnVcA, evsTestConnVcB},
	TypeEVSVMEntitlement:        {evsTestEntA1, evsTestEntA2, evsTestEntB1},
}

func TestScanEVS_StoresChildren(t *testing.T) {
	st := newTestStore(t)
	calls := map[string]*parentCalls{}
	total, err := scanEVSTopLevel(t, st, evsHandlers(calls))
	if err != nil {
		t.Fatalf("scanEVS: %v", err)
	}
	// 3 environments + 3 hosts + 4 VLANs + 3 connectors + 3 entitlements
	if total != 16 {
		t.Errorf("total = %d, want 16", total)
	}
	for typ, want := range evsAllChildRows {
		if got := nativeIDs(rowsByNativeID(t, st, typ)); !slices.Equal(got, want) {
			t.Errorf("%s rows = %v, want %v", typ, got, want)
		}
	}
	envIDs := []string{"env-aaaaaaaaaa", "env-bbbbbbbbbb"}
	for _, op := range []string{"ListEnvironmentHosts", "ListEnvironmentVlans", "ListEnvironmentConnectors"} {
		if got := calls[op].distinct(); !slices.Equal(got, envIDs) {
			t.Errorf("%s parents = %v, want %v (not the deleted environment)", op, got, envIDs)
		}
	}
	wantEnt := []string{"env-aaaaaaaaaa/cnctr-vcaaaaaaaa/WINDOWS_SERVER", "env-bbbbbbbbbb/cnctr-vcbbbbbbbb/WINDOWS_SERVER"}
	if got := calls["ListVmEntitlements"].distinct(); !slices.Equal(got, wantEnt) {
		t.Errorf("ListVmEntitlements calls = %v, want %v (vCenter connectors only)", got, wantEnt)
	}

	if r := rowsByNativeID(t, st, TypeEVSEnvironmentVlan)[evsTestVlanNoName]; r.Name != nil {
		t.Errorf("VLAN without a function name: Name = %q, want unset", sv(r.Name))
	}
	created := evsCreated.Format(time.RFC3339)
	for _, tc := range []struct {
		typ, nativeID, name, status, createdAt, attrKey, attrVal string
	}{
		{TypeEVSEnvironmentHost, evsTestHostA2, "esx-a2", "UPDATING", created, "Ec2InstanceId", "i-0esx-a2"},
		{TypeEVSEnvironmentVlan, evsTestVlanA2, "vmkManagement", "CREATED", created, "SubnetId", "subnet-0vmkManagement"},
		{TypeEVSEnvironmentConnector, evsTestConnSddc, "cnctr-sddcaaaaaa.vcf.example", "ACTIVE", created, "Type", "SDDC_MANAGER"},
		{TypeEVSVMEntitlement, evsTestEntA2, "web-2", "AT_RISK", "", "VmId", "vm-102"},
	} {
		r := rowsByNativeID(t, st, tc.typ)[tc.nativeID]
		if r.Type != tc.typ || sv(r.Region) != testRegion || sv(r.Name) != tc.name || sv(r.Status) != tc.status || sv(r.CreatedAt) != tc.createdAt {
			t.Errorf("%s row = type %q region %q name %q status %q created %q", tc.nativeID, r.Type, sv(r.Region), sv(r.Name), sv(r.Status), sv(r.CreatedAt))
		}
		var attrs map[string]any
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			t.Fatalf("attrs %s: %v", tc.nativeID, err)
		}
		if attrs[tc.attrKey] != tc.attrVal {
			t.Errorf("%s attrs[%s] = %v, want %q (SDK element verbatim)", tc.nativeID, tc.attrKey, attrs[tc.attrKey], tc.attrVal)
		}
	}

	for parent, children := range map[string][]string{
		evsTestEnvA:    {evsTestHostA1, evsTestHostA2, evsTestVlanA1, evsTestVlanA2, evsTestVlanNoName, evsTestVlanHcx2, evsTestConnVcA, evsTestConnSddc},
		evsTestEnvB:    {evsTestHostB1, evsTestConnVcB},
		evsTestConnVcA: {evsTestEntA1, evsTestEntA2},
		evsTestConnVcB: {evsTestEntB1},
	} {
		for _, child := range children {
			assertContainsNative(t, st, parent, child)
		}
	}
}

func TestScanEVS_NoChildren(t *testing.T) {
	st := newTestStore(t)
	calls := map[string]*parentCalls{}
	h := evsHandlers(calls)
	h["ListEnvironmentHosts"] = func(any) (any, error) { return &evs.ListEnvironmentHostsOutput{}, nil }
	h["ListEnvironmentVlans"] = func(any) (any, error) { return &evs.ListEnvironmentVlansOutput{}, nil }
	h["ListEnvironmentConnectors"] = func(any) (any, error) { return &evs.ListEnvironmentConnectorsOutput{}, nil }
	total, err := scanEVSTopLevel(t, st, h)
	if err != nil {
		t.Fatalf("scanEVS: %v", err)
	}
	if total != 3 {
		t.Errorf("total = %d, want the 3 environments only", total)
	}
	for typ := range evsAllChildRows {
		if n := len(rowsByNativeID(t, st, typ)); n != 0 {
			t.Errorf("%s rows = %d, want 0", typ, n)
		}
	}
	if n := len(calls["ListVmEntitlements"].ids); n != 0 {
		t.Errorf("ListVmEntitlements called %d times with no connectors", n)
	}
}

// A vCenter connector with no entitlements stores none and is not an error.
func TestScanEVS_NoEntitlements(t *testing.T) {
	st := newTestStore(t)
	calls := map[string]*parentCalls{}
	h := evsHandlers(calls)
	h["ListVmEntitlements"] = func(any) (any, error) { return &evs.ListVmEntitlementsOutput{}, nil }
	if _, err := scanEVSTopLevel(t, st, h); err != nil {
		t.Fatalf("scanEVS: %v", err)
	}
	if n := len(rowsByNativeID(t, st, TypeEVSVMEntitlement)); n != 0 {
		t.Errorf("entitlement rows = %d, want 0", n)
	}
}

// A parent deleted since listing, denied, failing transiently, or rejected
// with ValidationException (possibly not ready) drops only its own children:
// deleted is silent, and each of the others warns once however many parents
// hit it. An error code the model does not list fails that
// phase (none of its rows stored) and is returned; every other phase still
// stores its rows, except that a failed connector phase hands the entitlement
// phase no connectors.
func TestScanEVS_FailedChildParent(t *testing.T) {
	throttled := apiErr("ThrottlingException", "slow down")
	without := func(typ string, drop ...string) map[string][]string {
		out := map[string][]string{}
		for k, v := range evsAllChildRows {
			out[k] = v
		}
		out[typ] = slices.DeleteFunc(slices.Clone(out[typ]), func(id string) bool { return slices.Contains(drop, id) })
		return out
	}
	for _, tc := range []struct {
		name      string
		op        string           // the op whose calls fail
		opLabel   string           // its warning label
		fail      map[string]error // parent id (env or connector id) -> error
		wantCode  string           // "" means the scan returns nil
		wantRows  map[string][]string
		wantWarns int
	}{
		{
			"hosts gone", "ListEnvironmentHosts", evsListEnvironmentHostsOp,
			map[string]error{"env-aaaaaaaaaa": evsGone},
			"",
			without(TypeEVSEnvironmentHost, evsTestHostA1, evsTestHostA2), 0,
		},
		{
			"hosts denied", "ListEnvironmentHosts", evsListEnvironmentHostsOp,
			map[string]error{"env-aaaaaaaaaa": evsDenied, "env-bbbbbbbbbb": evsDenied},
			"",
			without(TypeEVSEnvironmentHost, evsAllChildRows[TypeEVSEnvironmentHost]...), 1,
		},
		{
			"hosts throttled", "ListEnvironmentHosts", evsListEnvironmentHostsOp,
			map[string]error{"env-bbbbbbbbbb": throttled},
			"",
			without(TypeEVSEnvironmentHost, evsTestHostB1), 1,
		},
		{
			"hosts other", "ListEnvironmentHosts", evsListEnvironmentHostsOp,
			map[string]error{"env-bbbbbbbbbb": evsUnexpected},
			"ConflictException",
			without(TypeEVSEnvironmentHost, evsAllChildRows[TypeEVSEnvironmentHost]...), 0,
		},
		{
			"hosts not ready", "ListEnvironmentHosts", evsListEnvironmentHostsOp,
			map[string]error{"env-bbbbbbbbbb": evsBadInput},
			"",
			without(TypeEVSEnvironmentHost, evsTestHostB1), 1,
		},
		{
			"hosts both not ready", "ListEnvironmentHosts", evsListEnvironmentHostsOp,
			map[string]error{"env-aaaaaaaaaa": evsBadInput, "env-bbbbbbbbbb": evsBadInput},
			"",
			without(TypeEVSEnvironmentHost, evsAllChildRows[TypeEVSEnvironmentHost]...), 1,
		},
		{
			"vlans gone", "ListEnvironmentVlans", evsListEnvironmentVlansOp,
			map[string]error{"env-aaaaaaaaaa": evsGone},
			"",
			without(TypeEVSEnvironmentVlan, evsAllChildRows[TypeEVSEnvironmentVlan]...), 0,
		},
		{
			"vlans denied", "ListEnvironmentVlans", evsListEnvironmentVlansOp,
			map[string]error{"env-aaaaaaaaaa": evsDenied, "env-bbbbbbbbbb": evsDenied},
			"",
			without(TypeEVSEnvironmentVlan, evsAllChildRows[TypeEVSEnvironmentVlan]...), 1,
		},
		{
			"vlans throttled", "ListEnvironmentVlans", evsListEnvironmentVlansOp,
			map[string]error{"env-aaaaaaaaaa": throttled, "env-bbbbbbbbbb": throttled},
			"",
			without(TypeEVSEnvironmentVlan, evsAllChildRows[TypeEVSEnvironmentVlan]...), 1,
		},
		{
			"vlans other", "ListEnvironmentVlans", evsListEnvironmentVlansOp,
			map[string]error{"env-bbbbbbbbbb": evsUnexpected},
			"ConflictException",
			without(TypeEVSEnvironmentVlan, evsAllChildRows[TypeEVSEnvironmentVlan]...), 0,
		},
		{
			"vlans not ready", "ListEnvironmentVlans", evsListEnvironmentVlansOp,
			map[string]error{"env-aaaaaaaaaa": evsBadInput},
			"",
			without(TypeEVSEnvironmentVlan, evsAllChildRows[TypeEVSEnvironmentVlan]...), 1,
		},
		{
			"connectors gone", "ListEnvironmentConnectors", evsListEnvironmentConnectorsOp,
			map[string]error{"env-bbbbbbbbbb": evsGone},
			"",
			func() map[string][]string {
				m := without(TypeEVSEnvironmentConnector, evsTestConnVcB)
				m[TypeEVSVMEntitlement] = []string{evsTestEntA1, evsTestEntA2}
				return m
			}(), 0,
		},
		{
			"connectors denied", "ListEnvironmentConnectors", evsListEnvironmentConnectorsOp,
			map[string]error{"env-aaaaaaaaaa": evsDenied, "env-bbbbbbbbbb": evsDenied},
			"",
			func() map[string][]string {
				m := without(TypeEVSEnvironmentConnector, evsAllChildRows[TypeEVSEnvironmentConnector]...)
				m[TypeEVSVMEntitlement] = nil
				return m
			}(), 1,
		},
		{
			"connectors throttled", "ListEnvironmentConnectors", evsListEnvironmentConnectorsOp,
			map[string]error{"env-aaaaaaaaaa": throttled},
			"",
			func() map[string][]string {
				m := without(TypeEVSEnvironmentConnector, evsTestConnVcA, evsTestConnSddc)
				m[TypeEVSVMEntitlement] = []string{evsTestEntB1}
				return m
			}(), 1,
		},
		{
			"connectors other", "ListEnvironmentConnectors", evsListEnvironmentConnectorsOp,
			map[string]error{"env-bbbbbbbbbb": evsUnexpected},
			"ConflictException",
			func() map[string][]string {
				m := without(TypeEVSEnvironmentConnector, evsAllChildRows[TypeEVSEnvironmentConnector]...)
				m[TypeEVSVMEntitlement] = nil
				return m
			}(), 0,
		},
		{
			"connectors not ready", "ListEnvironmentConnectors", evsListEnvironmentConnectorsOp,
			map[string]error{"env-aaaaaaaaaa": evsBadInput},
			"",
			func() map[string][]string {
				m := without(TypeEVSEnvironmentConnector, evsTestConnVcA, evsTestConnSddc)
				m[TypeEVSVMEntitlement] = []string{evsTestEntB1}
				return m
			}(), 1,
		},
		{
			"entitlements gone", "ListVmEntitlements", evsListVMEntitlementsOp,
			map[string]error{"cnctr-vcbbbbbbbb": evsGone},
			"",
			without(TypeEVSVMEntitlement, evsTestEntB1), 0,
		},
		{
			"entitlements denied", "ListVmEntitlements", evsListVMEntitlementsOp,
			map[string]error{"cnctr-vcaaaaaaaa": evsDenied, "cnctr-vcbbbbbbbb": evsDenied},
			"",
			without(TypeEVSVMEntitlement, evsAllChildRows[TypeEVSVMEntitlement]...), 1,
		},
		{
			"entitlements throttled", "ListVmEntitlements", evsListVMEntitlementsOp,
			map[string]error{"cnctr-vcaaaaaaaa": throttled},
			"",
			without(TypeEVSVMEntitlement, evsTestEntA1, evsTestEntA2), 1,
		},
		{
			"entitlements not ready", "ListVmEntitlements", evsListVMEntitlementsOp,
			map[string]error{"cnctr-vcbbbbbbbb": evsBadInput},
			"",
			without(TypeEVSVMEntitlement, evsTestEntB1), 1,
		},
		{
			"entitlements both not ready", "ListVmEntitlements", evsListVMEntitlementsOp,
			map[string]error{"cnctr-vcaaaaaaaa": evsBadInput, "cnctr-vcbbbbbbbb": evsBadInput},
			"",
			without(TypeEVSVMEntitlement, evsAllChildRows[TypeEVSVMEntitlement]...), 1,
		},
		{
			"entitlements other", "ListVmEntitlements", evsListVMEntitlementsOp,
			map[string]error{"cnctr-vcbbbbbbbb": evsUnexpected},
			"ConflictException",
			without(TypeEVSVMEntitlement, evsAllChildRows[TypeEVSVMEntitlement]...), 0,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := countOpWarnings(st, tc.opLabel)
			calls := map[string]*parentCalls{}
			h := evsHandlers(calls)
			next := h[tc.op]
			h[tc.op] = func(in any) (any, error) {
				var parent string
				switch p := in.(type) {
				case *evs.ListEnvironmentHostsInput:
					parent = sdkaws.ToString(p.EnvironmentId)
				case *evs.ListEnvironmentVlansInput:
					parent = sdkaws.ToString(p.EnvironmentId)
				case *evs.ListEnvironmentConnectorsInput:
					parent = sdkaws.ToString(p.EnvironmentId)
				case *evs.ListVmEntitlementsInput:
					parent = sdkaws.ToString(p.ConnectorId)
				}
				if err, ok := tc.fail[parent]; ok {
					return nil, err
				}
				return next(in)
			}
			_, err := scanEVSTopLevel(t, st, h)
			if tc.wantCode == "" {
				if err != nil {
					t.Fatalf("err = %v, want nil", err)
				}
			} else {
				assertAPICode(t, err, tc.wantCode)
			}
			for typ, want := range tc.wantRows {
				if got := nativeIDs(rowsByNativeID(t, st, typ)); !slices.Equal(got, want) {
					t.Errorf("%s rows = %v, want %v", typ, got, want)
				}
			}
			if *warns != tc.wantWarns {
				t.Errorf("warnings = %d, want %d", *warns, tc.wantWarns)
			}
			if n := len(rowsByNativeID(t, st, TypeEVSEnvironment)); n != 3 {
				t.Errorf("environment rows = %d, want 3", n)
			}
		})
	}
}

// A failed environment listing stores nothing and lists no children; a
// denial warns and returns nil, anything else is returned.
func TestScanEVS_EnvironmentListErrors(t *testing.T) {
	for _, tc := range []struct {
		name      string
		err       error
		wantCode  string
		wantWarns int
	}{
		{"denied", evsDenied, "", 1},
		{"other", evsBadInput, "ValidationException", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := countOpWarnings(st, "evs:ListEnvironments")
			calls := map[string]*parentCalls{}
			h := evsHandlers(calls)
			h["ListEnvironments"] = func(any) (any, error) { return nil, tc.err }
			total, err := scanEVSTopLevel(t, st, h)
			if tc.wantCode == "" {
				if err != nil {
					t.Fatalf("err = %v, want nil", err)
				}
			} else {
				assertAPICode(t, err, tc.wantCode)
			}
			if total != 0 || *warns != tc.wantWarns {
				t.Errorf("total = %d warnings = %d, want 0 and %d", total, *warns, tc.wantWarns)
			}
			if n := len(calls["ListEnvironmentHosts"].ids); n != 0 {
				t.Errorf("ListEnvironmentHosts called %d times after a failed environment listing", n)
			}
		})
	}
}

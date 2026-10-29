package aws

import (
	"context"
	"encoding/json"
	"errors"
	"net"
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

// evsTestEnvATags are environment A's tags; B and the deleted environment have
// none.
var evsTestEnvATags = map[string]string{"team": "vmware", "stage": "prod"}

// evsEnv is a listed environment named after its id.
func evsEnv(arn, id string, state evstypes.EnvironmentState) evstypes.EnvironmentSummary {
	return evstypes.EnvironmentSummary{
		EnvironmentArn: sdkaws.String(arn), EnvironmentId: sdkaws.String(id), EnvironmentName: sdkaws.String("name-" + id),
		EnvironmentState: state, CreatedAt: &evsCreated, VcfVersion: evstypes.VcfVersionVcf521,
	}
}

// evsHandlers answers every EVS op the scan calls: environments A and a
// deleted one on page one, B on page two; the deleted one's children are
// never listed. A has two pages of hosts (with
// a nameless and a deleted host to skip), two pages of VLANs and a vCenter and
// an SDDC Manager connector; B has one host, no VLANs and a vCenter connector.
// The vCenter connectors' entitlements are A: two pages, B: one page. Every
// child call is recorded in calls under its op, keyed by parent id, and every
// tag read under ListTagsForResource, keyed by ARN. Every listing fails unless
// MaxResults is the model's cap.
func evsHandlers(calls map[string]*parentCalls) map[string]func(in any) (any, error) {
	for _, op := range []string{"ListEnvironmentHosts", "ListEnvironmentVlans", "ListEnvironmentConnectors", "ListVmEntitlements", "ListTagsForResource"} {
		calls[op] = &parentCalls{}
	}
	badLimit := apiErr("ValidationException", "maxResults must be at most 100")
	return map[string]func(in any) (any, error){
		"ListEnvironments": func(in any) (any, error) {
			p := in.(*evs.ListEnvironmentsInput)
			if sdkaws.ToInt32(p.MaxResults) != 100 {
				return nil, badLimit
			}
			var out evs.ListEnvironmentsOutput
			out.EnvironmentSummaries, out.NextToken = evsPage(p.NextToken,
				[]evstypes.EnvironmentSummary{
					evsEnv(evsTestEnvA, "env-aaaaaaaaaa", evstypes.EnvironmentStateCreated),
					evsEnv(evsTestEnvDel, "env-dddddddddd", evstypes.EnvironmentStateDeleted),
				},
				[]evstypes.EnvironmentSummary{evsEnv(evsTestEnvB, "env-bbbbbbbbbb", evstypes.EnvironmentStateCreating)})
			return &out, nil
		},
		"ListTagsForResource": func(in any) (any, error) {
			arn := sdkaws.ToString(in.(*evs.ListTagsForResourceInput).ResourceArn)
			calls["ListTagsForResource"].add(arn)
			switch arn {
			case evsTestEnvA:
				return &evs.ListTagsForResourceOutput{Tags: evsTestEnvATags}, nil
			case evsTestEnvDel:
				// What EVS may answer for a deleted environment.
				return nil, evsGone
			}
			return &evs.ListTagsForResourceOutput{}, nil
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
	total, _, err := evsScan(context.Background(), st, handlers)
	return total, err
}

// evsScan runs the service scan in the test account and region; an op
// without a handler is denied.
func evsScan(ctx context.Context, st *store.Store, handlers map[string]func(in any) (any, error)) (int, int, error) {
	acct := newTestAccount(testAccountID)
	acct.cfg = opStubConfig(handlers, evsDenied)
	return scanEVS(ctx, acct, testRegion, st, testScanID)
}

// evsFailTags makes the tag read of each ARN in fail return its error.
func evsFailTags(h map[string]func(in any) (any, error), fail map[string]error) {
	next := h["ListTagsForResource"]
	h["ListTagsForResource"] = func(in any) (any, error) {
		if err, ok := fail[sdkaws.ToString(in.(*evs.ListTagsForResourceInput).ResourceArn)]; ok {
			return nil, err
		}
		return next(in)
	}
}

// evsTestTotal is every row evsHandlers lists: 3 environments + 3 hosts + 4
// VLANs + 3 connectors + 3 entitlements.
const evsTestTotal = 16

// evsTestEnvAChildren is the number of rows listed under environment A: 2
// hosts, 4 VLANs, 2 connectors and the 2 entitlements of its vCenter one.
const evsTestEnvAChildren = 10

var evsAllChildRows = map[string][]string{
	TypeEVSEnvironmentHost:      {evsTestHostA1, evsTestHostA2, evsTestHostB1},
	TypeEVSEnvironmentVlan:      {evsTestVlanA2, evsTestVlanA1, evsTestVlanNoName, evsTestVlanHcx2},
	TypeEVSEnvironmentConnector: {evsTestConnSddc, evsTestConnVcA, evsTestConnVcB},
	TypeEVSVMEntitlement:        {evsTestEntA1, evsTestEntA2, evsTestEntB1},
}

func TestScanEVS_StoresChildren(t *testing.T) {
	st := newTestStore(t)
	listWarns := bacWarnings(st, "evs:ListEnvironments")
	tagWarns := bacWarnings(st, "evs:ListTagsForResource")
	calls := map[string]*parentCalls{}
	total, err := scanEVSTopLevel(t, st, evsHandlers(calls))
	if err != nil {
		t.Fatalf("scanEVS: %v", err)
	}
	if len(*listWarns) != 0 || len(*tagWarns) != 0 {
		t.Errorf("warnings: evs:ListEnvironments %v, evs:ListTagsForResource %v; want none", *listWarns, *tagWarns)
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

	envs := rowsByNativeID(t, st, TypeEVSEnvironment)
	if got, want := nativeIDs(envs), []string{evsTestEnvA, evsTestEnvB, evsTestEnvDel}; !slices.Equal(got, want) {
		t.Fatalf("environment rows = %v, want %v", got, want)
	}
	assertTagsMap(t, envs[evsTestEnvA].TagsJSON, evsTestEnvATags)
	for _, arn := range []string{evsTestEnvB, evsTestEnvDel} {
		if tg := envs[arn].TagsJSON; tg != nil {
			t.Errorf("%s TagsJSON = %q, want nil (no tags)", arn, *tg)
		}
	}
	// The deleted environment is not read: its tags were deleted with it.
	if got, want := calls["ListTagsForResource"].distinct(), []string{evsTestEnvA, evsTestEnvB}; !slices.Equal(got, want) || len(calls["ListTagsForResource"].ids) != 2 {
		t.Errorf("ListTagsForResource ARNs = %v, want %v once each", calls["ListTagsForResource"].ids, want)
	}
	b := envs[evsTestEnvB]
	if sv(b.Name) != "name-env-bbbbbbbbbb" || sv(b.Status) != "CREATING" || sv(b.Region) != testRegion || sv(b.CreatedAt) != evsCreated.Format(time.RFC3339) {
		t.Errorf("environment B row = name %q status %q region %q created %q", sv(b.Name), sv(b.Status), sv(b.Region), sv(b.CreatedAt))
	}
	var envAttrs map[string]any
	if err := json.Unmarshal([]byte(b.AttributesJSON), &envAttrs); err != nil {
		t.Fatalf("environment B attrs: %v", err)
	}
	if envAttrs["EnvironmentId"] != "env-bbbbbbbbbb" || envAttrs["VcfVersion"] != "VCF-5.2.1" {
		t.Errorf("environment B attrs = %v, want the listed element verbatim", envAttrs)
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

// An empty environment listing stores nothing, reads no tags and lists no
// children.
func TestScanEVS_NoEnvironments(t *testing.T) {
	st := newTestStore(t)
	calls := map[string]*parentCalls{}
	h := evsHandlers(calls)
	h["ListEnvironments"] = func(any) (any, error) { return &evs.ListEnvironmentsOutput{}, nil }
	total, inserted, err := evsScan(context.Background(), st, h)
	if err != nil || total != 0 || inserted != 0 {
		t.Fatalf("scan = %d, %d, %v; want 0, 0, nil", total, inserted, err)
	}
	for _, op := range []string{"ListTagsForResource", "ListEnvironmentHosts", "ListEnvironmentVlans", "ListEnvironmentConnectors"} {
		if n := len(calls[op].ids); n != 0 {
			t.Errorf("%s called %d times with no environments", op, n)
		}
	}
}

func TestScanEVS_RescanInsertsNone(t *testing.T) {
	st := newTestStore(t)
	total, inserted, err := evsScan(context.Background(), st, evsHandlers(map[string]*parentCalls{}))
	if err != nil || total != evsTestTotal || inserted != evsTestTotal {
		t.Fatalf("first scan = %d, %d, %v; want %d, %d, nil", total, inserted, err, evsTestTotal, evsTestTotal)
	}
	total, inserted, err = evsScan(context.Background(), st, evsHandlers(map[string]*parentCalls{}))
	if err != nil || total != evsTestTotal || inserted != 0 {
		t.Errorf("rescan = %d, %d, %v; want %d, 0, nil", total, inserted, err, evsTestTotal)
	}
}

// A failed environment tag read: ResourceNotFoundException (deleted since
// listing) drops the row and its children silently; a denial keeps the row
// untagged and warns once under the tag op however many rows it hit; a
// throttle keeps it and makes the scan partial; any other error keeps it and
// is returned. The other environments are read and their children listed.
func TestScanEVS_EnvironmentTagReadFailures(t *testing.T) {
	throttled := apiErr("ThrottlingException", "slow down")
	both := []string{"env-aaaaaaaaaa", "env-bbbbbbbbbb"}
	for _, tc := range []struct {
		name        string
		fail        map[string]error
		outcome     serviceOutcome // -1: no error
		code        string
		wantTotal   int
		wantEnvs    []string
		wantParents []string
		wantWarns   int
	}{
		{"not found", map[string]error{evsTestEnvA: evsGone}, -1, "", evsTestTotal - 1 - evsTestEnvAChildren, []string{evsTestEnvB, evsTestEnvDel}, []string{"env-bbbbbbbbbb"}, 0},
		{"denied", map[string]error{evsTestEnvA: evsDenied}, -1, "", evsTestTotal, []string{evsTestEnvA, evsTestEnvB, evsTestEnvDel}, both, 1},
		{"denied twice", map[string]error{evsTestEnvA: evsDenied, evsTestEnvB: evsDenied}, -1, "", evsTestTotal, []string{evsTestEnvA, evsTestEnvB, evsTestEnvDel}, both, 1},
		{"throttled", map[string]error{evsTestEnvA: throttled}, outcomeTransient, "ThrottlingException", evsTestTotal, []string{evsTestEnvA, evsTestEnvB, evsTestEnvDel}, both, 0},
		{"other", map[string]error{evsTestEnvA: evsUnexpected}, outcomeError, "ConflictException", evsTestTotal, []string{evsTestEnvA, evsTestEnvB, evsTestEnvDel}, both, 0},
		// The throttle is demoted to one warning so the hard error decides the outcome.
		{"throttled beside other", map[string]error{evsTestEnvA: throttled, evsTestEnvB: evsUnexpected}, outcomeError, "ConflictException", evsTestTotal, []string{evsTestEnvA, evsTestEnvB, evsTestEnvDel}, both, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := bacWarnings(st, "evs:ListTagsForResource")
			calls := map[string]*parentCalls{}
			h := evsHandlers(calls)
			evsFailTags(h, tc.fail)

			total, _, err := evsScan(context.Background(), st, h)
			if tc.outcome < 0 {
				if err != nil {
					t.Fatalf("err = %v, want nil", err)
				}
			} else {
				assertGlueOutcome(t, err, tc.outcome)
				assertAPICode(t, err, tc.code)
			}
			if total != tc.wantTotal {
				t.Errorf("total = %d, want %d", total, tc.wantTotal)
			}
			envs := rowsByNativeID(t, st, TypeEVSEnvironment)
			if got := nativeIDs(envs); !slices.Equal(got, tc.wantEnvs) {
				t.Errorf("environment rows = %v, want %v", got, tc.wantEnvs)
			}
			for arn := range tc.fail {
				if r, ok := envs[arn]; ok && r.TagsJSON != nil {
					t.Errorf("%s TagsJSON = %q, want nil", arn, *r.TagsJSON)
				}
			}
			if got := calls["ListEnvironmentHosts"].distinct(); !slices.Equal(got, tc.wantParents) {
				t.Errorf("ListEnvironmentHosts parents = %v, want %v", got, tc.wantParents)
			}
			if len(*warns) != tc.wantWarns {
				t.Fatalf("evs:ListTagsForResource warnings = %v, want %d", *warns, tc.wantWarns)
			}
			for _, w := range *warns {
				if w.Scope != testAccountID+"/"+testRegion {
					t.Errorf("warning scope = %q, want %s/%s", w.Scope, testAccountID, testRegion)
				}
			}
		})
	}
}

// A throttled tag read beside a child phase's hard error is demoted to one
// warning, so the hard error decides the outcome.
func TestScanEVS_TagThrottleDemotedBesideHardError(t *testing.T) {
	st := newTestStore(t)
	warns := countOpWarnings(st, "evs:ListTagsForResource")
	h := evsHandlers(map[string]*parentCalls{})
	evsFailTags(h, map[string]error{evsTestEnvA: apiErr("ThrottlingException", "slow down")})
	hosts := h["ListEnvironmentHosts"]
	h["ListEnvironmentHosts"] = func(in any) (any, error) {
		if sdkaws.ToString(in.(*evs.ListEnvironmentHostsInput).EnvironmentId) == "env-bbbbbbbbbb" {
			return nil, evsUnexpected
		}
		return hosts(in)
	}

	_, _, err := evsScan(context.Background(), st, h)
	assertGlueOutcome(t, err, outcomeError)
	assertAPICode(t, err, "ConflictException")
	if *warns != 1 {
		t.Errorf("evs:ListTagsForResource warnings = %d, want 1", *warns)
	}
}

// A failed environment write stops the scan: no later page or phase runs, and
// no warning is sent about rows the write lost.
func TestScanEVS_FailedEnvironmentWriteStopsScan(t *testing.T) {
	st := newTestStore(t)
	warns := rhWarnRecorder(st)
	refuseStoreWrites(t, st, TypeEVSEnvironment)
	calls := map[string]*parentCalls{}
	h := evsHandlers(calls)
	evsFailTags(h, map[string]error{evsTestEnvA: evsDenied})
	var pages int
	list := h["ListEnvironments"]
	h["ListEnvironments"] = func(in any) (any, error) {
		pages++
		return list(in)
	}

	total, _, err := evsScan(context.Background(), st, h)
	if !errors.Is(err, store.ErrStoreWrite) {
		t.Fatalf("err = %v, want store.ErrStoreWrite", err)
	}
	if total != 0 || pages != 1 {
		t.Errorf("total = %d, ListEnvironments pages = %d; want 0 and 1", total, pages)
	}
	if w := warns(); len(w) != 0 {
		t.Errorf("warnings = %v, want none", w)
	}
	for _, op := range []string{"ListEnvironmentHosts", "ListEnvironmentVlans", "ListEnvironmentConnectors"} {
		if n := len(calls[op].ids); n != 0 {
			t.Errorf("%s called %d times after the failed write", op, n)
		}
	}
}

// A failed later page of the environment listing keeps the page before it,
// whose environments' children are still listed, and is returned.
func TestScanEVS_EnvironmentListFailureKeepsPage(t *testing.T) {
	st := newTestStore(t)
	calls := map[string]*parentCalls{}
	h := evsHandlers(calls)
	h["ListEnvironments"] = failOnCall(2, evsBadInput, h["ListEnvironments"])

	total, _, err := evsScan(context.Background(), st, h)
	assertGlueOutcome(t, err, outcomeError)
	assertAPICode(t, err, "ValidationException")
	if want := 2 + evsTestEnvAChildren; total != want {
		t.Errorf("total = %d, want %d", total, want)
	}
	if got, want := nativeIDs(rowsByNativeID(t, st, TypeEVSEnvironment)), []string{evsTestEnvA, evsTestEnvDel}; !slices.Equal(got, want) {
		t.Errorf("environment rows = %v, want %v", got, want)
	}
	if got, want := calls["ListEnvironmentHosts"].distinct(), []string{"env-aaaaaaaaaa"}; !slices.Equal(got, want) {
		t.Errorf("ListEnvironmentHosts parents = %v, want %v", got, want)
	}
}

// A repeated NextToken ends the environment listing as an error, keeping
// every page read and listing their children.
func TestScanEVS_RepeatedTokenKeepsPages(t *testing.T) {
	st := newTestStore(t)
	h := evsHandlers(map[string]*parentCalls{})
	list := h["ListEnvironments"]
	h["ListEnvironments"] = func(in any) (any, error) {
		out, err := list(in)
		if err != nil {
			return nil, err
		}
		out.(*evs.ListEnvironmentsOutput).NextToken = sdkaws.String("page2")
		return out, nil
	}

	total, _, err := evsScan(context.Background(), st, h)
	if !errors.Is(err, errRepeatedNextToken) {
		t.Fatalf("err = %v, want errRepeatedNextToken", err)
	}
	if total != evsTestTotal {
		t.Errorf("total = %d, want %d", total, evsTestTotal)
	}
}

// An environment without an ARN is not stored and not sent to the tag op; one
// with an ARN but no id is stored with its tags but has no children listed.
// Each is warned about once under the listing.
func TestScanEVS_EnvironmentWithoutIdentifiers(t *testing.T) {
	st := newTestStore(t)
	warns := bacWarnings(st, "evs:ListEnvironments")
	calls := map[string]*parentCalls{}
	h := evsHandlers(calls)
	noID := "arn:aws:evs:us-east-1:123456789012:environment/env-noidnoidno"
	noIDTags := map[string]string{"owner": "ops"}
	tags := h["ListTagsForResource"]
	h["ListTagsForResource"] = func(in any) (any, error) {
		out, err := tags(in)
		if sdkaws.ToString(in.(*evs.ListTagsForResourceInput).ResourceArn) == noID {
			return &evs.ListTagsForResourceOutput{Tags: noIDTags}, err
		}
		return out, err
	}
	h["ListEnvironments"] = func(any) (any, error) {
		return &evs.ListEnvironmentsOutput{EnvironmentSummaries: []evstypes.EnvironmentSummary{
			evsEnv(evsTestEnvA, "env-aaaaaaaaaa", evstypes.EnvironmentStateCreated),
			{EnvironmentName: sdkaws.String("no-arn"), EnvironmentId: sdkaws.String("env-noarnnoarn")},
			{EnvironmentArn: sdkaws.String(noID), EnvironmentName: sdkaws.String("no-id")},
		}}, nil
	}

	total, _, err := evsScan(context.Background(), st, h)
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if want := 2 + evsTestEnvAChildren; total != want {
		t.Errorf("total = %d, want %d", total, want)
	}
	if got, want := calls["ListTagsForResource"].distinct(), []string{evsTestEnvA, noID}; !slices.Equal(got, want) {
		t.Errorf("ListTagsForResource ARNs = %v, want %v", got, want)
	}
	if got, want := calls["ListEnvironmentHosts"].distinct(), []string{"env-aaaaaaaaaa"}; !slices.Equal(got, want) {
		t.Errorf("ListEnvironmentHosts parents = %v, want %v", got, want)
	}
	if len(*warns) != 2 {
		t.Errorf("evs:ListEnvironments warnings = %v, want 2 (no ARN, no id)", *warns)
	}
	r := rowsByNativeID(t, st, TypeEVSEnvironment)[noID]
	// An empty EnvironmentState stores no Status, not "".
	if r.Status != nil {
		t.Errorf("no-id environment Status = %q, want unset", *r.Status)
	}
	assertTagsMap(t, r.TagsJSON, noIDTags)
}

// A DELETED environment without an id is stored and not warned about: being
// deleted, not the missing id, is why its children are not read.
func TestScanEVS_DeletedEnvironmentWithoutIDNotWarned(t *testing.T) {
	st := newTestStore(t)
	warns := bacWarnings(st, "evs:ListEnvironments")
	h := evsHandlers(map[string]*parentCalls{})
	h["ListEnvironments"] = func(any) (any, error) {
		return &evs.ListEnvironmentsOutput{EnvironmentSummaries: []evstypes.EnvironmentSummary{
			{EnvironmentArn: sdkaws.String(evsTestEnvDel), EnvironmentState: evstypes.EnvironmentStateDeleted},
		}}, nil
	}

	total, _, err := evsScan(context.Background(), st, h)
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if total != 1 {
		t.Errorf("total = %d, want 1", total)
	}
	if len(*warns) != 0 {
		t.Errorf("evs:ListEnvironments warnings = %v, want none", *warns)
	}
}

// A GovCloud environment's listed ARN reaches the tag op unchanged.
func TestScanEVS_GovCloudARNReachesTagOp(t *testing.T) {
	st := newTestStore(t)
	calls := map[string]*parentCalls{}
	h := evsHandlers(calls)
	gov := "arn:aws-us-gov:evs:us-gov-west-1:" + testAccountID + ":environment/env-aaaaaaaaaa"
	h["ListEnvironments"] = func(any) (any, error) {
		return &evs.ListEnvironmentsOutput{EnvironmentSummaries: []evstypes.EnvironmentSummary{evsEnv(gov, "env-aaaaaaaaaa", evstypes.EnvironmentStateCreated)}}, nil
	}
	h["ListTagsForResource"] = func(in any) (any, error) {
		calls["ListTagsForResource"].add(sdkaws.ToString(in.(*evs.ListTagsForResourceInput).ResourceArn))
		return &evs.ListTagsForResourceOutput{Tags: evsTestEnvATags}, nil
	}
	acct := newTestAccount(testAccountID)
	acct.Partition = "aws-us-gov"
	acct.cfg = opStubConfig(h, evsDenied)

	if _, _, err := scanEVS(context.Background(), acct, "us-gov-west-1", st, testScanID); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if got := calls["ListTagsForResource"].ids; !slices.Equal(got, []string{gov}) {
		t.Errorf("ListTagsForResource ARNs = %v, want [%s]", got, gov)
	}
	assertTagsMap(t, rowsByNativeID(t, st, TypeEVSEnvironment)[gov].TagsJSON, evsTestEnvATags)
	if _, ok := rowsByNativeID(t, st, TypeEVSEnvironmentHost)[gov+"/host/esx-a1"]; !ok {
		t.Error("host of the GovCloud environment missing")
	}
}

// Once ctx ends during the tag reads, the page is written, no child phase
// runs, and the stop is one member of the error.
func TestScanEVS_ContextEndReportedOnce(t *testing.T) {
	st := newTestStore(t)
	calls := map[string]*parentCalls{}
	h := evsHandlers(calls)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	evsFailTags(h, map[string]error{evsTestEnvA: context.Canceled})
	tags := h["ListTagsForResource"]
	h["ListTagsForResource"] = func(in any) (any, error) {
		if sdkaws.ToString(in.(*evs.ListTagsForResourceInput).ResourceArn) == evsTestEnvA {
			cancel()
		}
		return tags(in)
	}

	_, _, err := evsScan(ctx, st, h)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	stops := 0
	for _, m := range bacTopMembers(err) {
		if errors.Is(m, context.Canceled) {
			stops++
		}
	}
	if stops != 1 {
		t.Errorf("members carrying the stop = %d, want 1 (err %v)", stops, err)
	}
	if _, ok := rowsByNativeID(t, st, TypeEVSEnvironment)[evsTestEnvA]; !ok {
		t.Error("environment A, whose read the stop cut short, was not kept")
	}
	if n := len(calls["ListEnvironmentHosts"].ids); n != 0 {
		t.Errorf("ListEnvironmentHosts called %d times after the stop", n)
	}
}

// An NXDOMAIN after environments were stored is a failure of this scan, not a
// region gap, and the counts are kept; with nothing stored the region is
// unavailable.
func TestScanEVS_NXDOMAIN(t *testing.T) {
	nx := &net.DNSError{Err: "no such host", Name: "evs.us-east-1.amazonaws.com", IsNotFound: true}
	t.Run("tag read", func(t *testing.T) {
		st := newTestStore(t)
		h := evsHandlers(map[string]*parentCalls{})
		evsFailTags(h, map[string]error{evsTestEnvA: nx})
		total, _, err := evsScan(context.Background(), st, h)
		assertGlueOutcome(t, err, outcomeError)
		if total != evsTestTotal {
			t.Errorf("total = %d, want %d", total, evsTestTotal)
		}
	})
	t.Run("later page", func(t *testing.T) {
		st := newTestStore(t)
		h := evsHandlers(map[string]*parentCalls{})
		h["ListEnvironments"] = failOnCall(2, nx, h["ListEnvironments"])
		total, _, err := evsScan(context.Background(), st, h)
		assertGlueOutcome(t, err, outcomeError)
		if want := 2 + evsTestEnvAChildren; total != want {
			t.Errorf("total = %d, want %d", total, want)
		}
	})
	t.Run("nothing stored", func(t *testing.T) {
		st := newTestStore(t)
		warns := rhWarnRecorder(st)
		acct := newTestAccount(testAccountID)
		acct.cfg = opStubConfig(nil, nx)
		_, _, err := scanEVS(context.Background(), acct, testRegion, st, testScanID)
		assertGlueOutcome(t, err, outcomeUnavailable)
		if w := warns(); len(w) != 0 {
			t.Errorf("warnings = %v, want none", w)
		}
	})
}

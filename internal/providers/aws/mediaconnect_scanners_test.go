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

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/mediaconnect"
	mctypes "github.com/aws/aws-sdk-go-v2/service/mediaconnect/types"
	"github.com/icearp/disco-cli/store"
)

// fakeMC serves ListGatewayInstances pages keyed by NextToken ("" is the first
// page); pageErrs[token] fails that page.
type fakeMC struct {
	mediaConnectAPI
	instances map[string]*mediaconnect.ListGatewayInstancesOutput
	pageErrs  map[string]error
}

func (f fakeMC) ListGatewayInstances(_ context.Context, in *mediaconnect.ListGatewayInstancesInput, _ ...func(*mediaconnect.Options)) (*mediaconnect.ListGatewayInstancesOutput, error) {
	if err := f.pageErrs[sv(in.NextToken)]; err != nil {
		return nil, err
	}
	if out := f.instances[sv(in.NextToken)]; out != nil {
		return out, nil
	}
	return &mediaconnect.ListGatewayInstancesOutput{}, nil
}

func mcGatewayARN(name string) string {
	return "arn:aws:mediaconnect:" + testRegion + ":" + testAccountID + ":gateway:1-abc-" + name
}

func mcInstance(gateway, id string) mctypes.ListedGatewayInstance {
	return mctypes.ListedGatewayInstance{
		GatewayArn:         sdkaws.String(mcGatewayARN(gateway)),
		GatewayInstanceArn: sdkaws.String(mcGatewayARN(gateway) + "/instance/" + id),
		InstanceId:         sdkaws.String(id),
		InstanceState:      mctypes.InstanceStateActive,
	}
}

func TestScanMCGatewayInstances_PaginatesAndContainsUnderListedGateway(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	var warns []store.ScanWarning
	st.OnWarn = func(w store.ScanWarning) { warns = append(warns, w) }
	upsertTestResource(t, st, "aws", testAccountID, TypeMediaConnectGateway, mcGatewayARN("gw1"), testRegion, "{}")
	noState := mcInstance("gw1", "mi-3")
	noState.InstanceState = ""
	fake := fakeMC{instances: map[string]*mediaconnect.ListGatewayInstancesOutput{
		"":   {Instances: []mctypes.ListedGatewayInstance{mcInstance("gw1", "mi-1"), {InstanceId: sdkaws.String("no-arn")}}, NextToken: sdkaws.String("p2")},
		"p2": {Instances: []mctypes.ListedGatewayInstance{mcInstance("gw1", "mi-2"), noState, mcInstance("unlisted", "mi-4")}},
	}}

	total, _, err := scanMCGatewayInstances(context.Background(), fake, newTestAccount(testAccountID), testRegion, st, testScanID, []string{mcGatewayARN("gw1")})
	if err != nil {
		t.Fatalf("scanMCGatewayInstances: %v", err)
	}
	if total != 4 {
		t.Errorf("total = %d; want 4 (the ARN-less instance is skipped)", total)
	}
	rows := qsRows(t, st, TypeMediaConnectGatewayInstance)
	if len(rows) != 4 {
		t.Fatalf("stored %d gateway-instance rows; want 4", len(rows))
	}
	mi1 := mcGatewayARN("gw1") + "/instance/mi-1"
	r := rows[mi1]
	if r.Type != TypeMediaConnectGatewayInstance || sv(r.Region) != testRegion || sv(r.Name) != "mi-1" || sv(r.Status) != "ACTIVE" {
		t.Errorf("row %s: type=%q region=%q name=%q status=%q", mi1, r.Type, sv(r.Region), sv(r.Name), sv(r.Status))
	}
	var attrs map[string]any
	if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil || attrs["GatewayArn"] != mcGatewayARN("gw1") {
		t.Errorf("row %s: attributes %s lack GatewayArn (err %v)", mi1, r.AttributesJSON, err)
	}
	if s := rows[mcGatewayARN("gw1")+"/instance/mi-3"].Status; s != nil {
		t.Errorf("state-less instance stored Status %q; want unset", *s)
	}
	assertQSContains(t, st, mcGatewayARN("gw1"), mi1, mcGatewayARN("gw1")+"/instance/mi-2", mcGatewayARN("gw1")+"/instance/mi-3")
	orphan := store.ResourceID("aws", testAccountID, mcGatewayARN("unlisted")+"/instance/mi-4")
	if rels, err := st.RelationshipsTo(orphan, store.RelContains); err != nil || len(rels) != 0 {
		t.Errorf("instance of an unlisted gateway has contains parents %v (err %v); want none", rels, err)
	}
	if len(warns) != 0 {
		t.Errorf("warnings %v; want none (an unlisted gateway is not a missing hierarchy parent)", warns)
	}
}

// TestScanMCGatewayInstances_ErrorShapes: a deny on page 2 keeps page 1's rows
// and warns once; any other error propagates.
func TestScanMCGatewayInstances_ErrorShapes(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name      string
		err       error
		wantErr   bool
		wantRows  int
		wantWarns int
	}{
		{"no error stores page 1", nil, false, 1, 0},
		{"access denied warns and keeps page 1", apiErr("AccessDeniedException", "User: x is not authorized to perform: mediaconnect:ListGatewayInstances"), false, 1, 1},
		{"other error propagates", apiErr("InternalServerErrorException", "boom"), true, 0, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			var warned []string
			st.OnWarn = func(w store.ScanWarning) { warned = append(warned, w.Service) }
			fake := fakeMC{
				instances: map[string]*mediaconnect.ListGatewayInstancesOutput{"": {Instances: []mctypes.ListedGatewayInstance{mcInstance("gw1", "mi-1")}, NextToken: sdkaws.String("p2")}},
				pageErrs:  map[string]error{"p2": tc.err},
			}
			_, _, err := scanMCGatewayInstances(context.Background(), fake, newTestAccount(testAccountID), testRegion, st, testScanID, nil)
			if tc.wantErr {
				if !isAPIErrorCode(err, "InternalServerErrorException") {
					t.Fatalf("err = %v; want the InternalServerErrorException propagated", err)
				}
			} else if err != nil {
				t.Fatalf("err = %v; want nil", err)
			}
			if got := len(qsRows(t, st, TypeMediaConnectGatewayInstance)); got != tc.wantRows {
				t.Errorf("stored %d rows; want %d", got, tc.wantRows)
			}
			if len(warned) != tc.wantWarns {
				t.Errorf("recorded %d warnings; want %d", len(warned), tc.wantWarns)
			}
			for _, svc := range warned {
				if svc != "mediaconnect:ListGatewayInstances" {
					t.Errorf("warning names %q; want mediaconnect:ListGatewayInstances", svc)
				}
			}
		})
	}
}

func TestScanMCGatewayInstances_Empty(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	total, _, err := scanMCGatewayInstances(context.Background(), fakeMC{}, newTestAccount(testAccountID), testRegion, st, testScanID, nil)
	if err != nil || total != 0 {
		t.Errorf("total=%d err=%v; want 0, nil", total, err)
	}
}

// mcServiceStub serves every op scanMediaConnect calls: one gateway, one
// reservation, one instance of that gateway, and override replacing an op's
// queue.
func mcServiceStub(t *testing.T, override map[string][]stubCall) sdkaws.Config {
	t.Helper()
	one := func(out any) []stubCall { return []stubCall{{Output: out}} }
	calls := map[string][]stubCall{
		"ListBridges": one(&mediaconnect.ListBridgesOutput{}),
		"ListFlows":   one(&mediaconnect.ListFlowsOutput{}),
		"ListGateways": one(&mediaconnect.ListGatewaysOutput{Gateways: []mctypes.ListedGateway{
			{GatewayArn: sdkaws.String(mcGatewayARN("gw")), Name: sdkaws.String("gw")},
		}}),
		"ListRouterInputs":            one(&mediaconnect.ListRouterInputsOutput{}),
		"ListRouterNetworkInterfaces": one(&mediaconnect.ListRouterNetworkInterfacesOutput{}),
		"ListRouterOutputs":           one(&mediaconnect.ListRouterOutputsOutput{}),
		"ListReservations": one(&mediaconnect.ListReservationsOutput{Reservations: []mctypes.Reservation{
			{ReservationArn: sdkaws.String("arn:aws:mediaconnect:" + testRegion + ":" + testAccountID + ":reservation:r1")},
		}}),
		"ListGatewayInstances": one(&mediaconnect.ListGatewayInstancesOutput{Instances: []mctypes.ListedGatewayInstance{mcInstance("gw", "mi-1")}}),
	}
	for op, c := range override {
		calls[op] = c
	}
	return cfgWithStub(stubResponses(t, calls), testRegion)
}

// TestScanMediaConnect_GatewayInstancesAfterExistingPhases runs the whole
// service against the real SDK client: the instance is stored and contained by
// the gateway listed earlier in the same scan.
func TestScanMediaConnect_GatewayInstancesAfterExistingPhases(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	acct.cfg = mcServiceStub(t, nil)
	total, _, err := scanMediaConnect(context.Background(), acct, testRegion, st, testScanID)
	if err != nil {
		t.Fatalf("scanMediaConnect: %v", err)
	}
	if total != 3 {
		t.Errorf("total = %d; want 3 (gateway, reservation, gateway instance)", total)
	}
	assertQSContains(t, st, mcGatewayARN("gw"), mcGatewayARN("gw")+"/instance/mi-1")
}

// TestScanMediaConnect_GatewayInstanceErrorPropagates pins that the new phase's
// hard error reaches the service result without costing the phases before it.
func TestScanMediaConnect_GatewayInstanceErrorPropagates(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	acct.cfg = mcServiceStub(t, map[string][]stubCall{
		"ListGatewayInstances": {{Err: apiErr("InternalServerErrorException", "boom")}},
	})
	_, _, err := scanMediaConnect(context.Background(), acct, testRegion, st, testScanID)
	if !isAPIErrorCode(err, "InternalServerErrorException") {
		t.Fatalf("err = %v; want the ListGatewayInstances InternalServerErrorException", err)
	}
	if got := len(qsRows(t, st, TypeMediaConnectReservation)); got != 1 {
		t.Errorf("stored %d reservations; want 1 (listed before the failing phase)", got)
	}
}

var (
	mcTestDenied    = apiErr("ForbiddenException", "User: arn:aws:iam::123:user/x is not authorized to perform: mediaconnect:List")
	mcTestThrottled = apiErr("TooManyRequestsException", "Rate exceeded")
	mcTestGone      = apiErr("NotFoundException", "not found")
	mcTestBad       = apiErr("BadRequestException", "bad")
)

// mcTestTotal is every row of mcFixture: 2 flows, 1 entitlement, 2 outputs,
// 2 sources, 2 VPC interfaces, and 2 router inputs, network interfaces and
// outputs.
const mcTestTotal = 15

// mcFixture answers every op of a mediaconnect scan and records its calls.
// ListFlows returns flow-tagged on its first page and flow-bare on its
// second; each router listing returns its "-tagged" element, then its
// "-bare" one. flow-tagged holds entitlement e-1, output o-1, source s-1 (in
// Sources, and again as Source with another Description) and VPC interface
// vpc-1; flow-bare holds output o-2, source s-2 (as Source only) and VPC
// interface vpc-2. flow-tagged, every "-1" child and every "-tagged" router
// element are tagged with their type. flow-bare, output o-2 and router network
// interface rni-bare have no Name. Gateways, reservations and gateway
// instances list empty, and so do bridges unless a test sets bridges.
// Handlers run on fan-out goroutines, so mu guards the state they share.
type mcFixture struct {
	partition, region string

	mu            sync.Mutex
	calls         map[string]int
	maxResults    map[string][]int32
	tagARNs       []string // sent to ListTagsForResource
	globalTagARNs []string // sent to ListTagsForGlobalResource
	described     parentCalls
	tags          map[string]map[string]string
	errs          map[string]error // tag read errors by ARN, either op
	describeErrs  map[string]error // DescribeFlow errors by flow ARN
	bridgeErrs    map[string]error // DescribeBridge errors by bridge ARN
	// listErrs fails a listing: key op for its first page, op+"/p2" for its
	// second.
	listErrs map[string]error
	bridges  []mctypes.ListedBridge
	flows    [][]mctypes.ListedFlow
	bodies   map[string]*mctypes.Flow // DescribeFlow's Flow by flow ARN
	inputs   [][]mctypes.ListedRouterInput
	nis      [][]mctypes.ListedRouterNetworkInterface
	outputs  [][]mctypes.ListedRouterOutput
}

func newMCFixture() *mcFixture { return newMCFixtureIn("aws", testRegion) }

func newMCFixtureIn(partition, region string) *mcFixture {
	f := &mcFixture{
		partition: partition, region: region,
		calls: map[string]int{}, maxResults: map[string][]int32{},
		errs: map[string]error{}, describeErrs: map[string]error{}, bridgeErrs: map[string]error{}, listErrs: map[string]error{},
	}
	f.flows = [][]mctypes.ListedFlow{{f.flow("flow-tagged")}, {f.unnamedFlow("flow-bare")}}
	primary, bareSource := f.source("s-1"), f.source("s-2")
	primary.Description = sdkaws.String("as Source")
	f.bodies = map[string]*mctypes.Flow{
		f.flowARN("flow-tagged"): {
			FlowArn: sdkaws.String(f.flowARN("flow-tagged")), Name: sdkaws.String("name-flow-tagged"),
			Entitlements:  []mctypes.Entitlement{f.entitlement("e-1")},
			Outputs:       []mctypes.Output{f.output("o-1")},
			Sources:       []mctypes.Source{f.source("s-1")},
			Source:        &primary,
			VpcInterfaces: []mctypes.VpcInterface{mcVpcInterface("vpc-1")},
		},
		f.flowARN("flow-bare"): {
			FlowArn: sdkaws.String(f.flowARN("flow-bare")), Name: sdkaws.String("name-flow-bare"),
			Outputs:       []mctypes.Output{f.unnamedOutput("o-2")},
			Source:        &bareSource,
			VpcInterfaces: []mctypes.VpcInterface{mcVpcInterface("vpc-2")},
		},
	}
	f.inputs = [][]mctypes.ListedRouterInput{{f.routerInput("ri-tagged")}, {f.routerInput("ri-bare")}}
	f.nis = [][]mctypes.ListedRouterNetworkInterface{{f.routerNI("rni-tagged")}, {f.unnamedRouterNI("rni-bare")}}
	f.outputs = [][]mctypes.ListedRouterOutput{{f.routerOutput("ro-tagged")}, {f.routerOutput("ro-bare")}}
	f.tags = map[string]map[string]string{
		f.flowARN("flow-tagged"):                            {"type": TypeMediaConnectFlow},
		f.entARN("e-1"):                                     {"type": TypeMediaConnectFlowEntitlement},
		f.outARN("o-1"):                                     {"type": TypeMediaConnectFlowOutput},
		f.srcARN("s-1"):                                     {"type": TypeMediaConnectFlowSource},
		f.vpcTagARN("flow-tagged", "vpc-1"):                 {"type": TypeMediaConnectFlowVpcInterface},
		f.routerARN("routerInput", "ri-tagged"):             {"type": TypeMediaConnectRouterInput},
		f.routerARN("routerNetworkInterface", "rni-tagged"): {"type": TypeMediaConnectRouterNetworkInterface},
		f.routerARN("routerOutput", "ro-tagged"):            {"type": TypeMediaConnectRouterOutput},
	}
	return f
}

func (f *mcFixture) arn(resource string) string {
	return fmt.Sprintf("arn:%s:mediaconnect:%s:%s:%s", f.partition, f.region, testAccountID, resource)
}

func (f *mcFixture) bridgeARN(name string) string { return f.arn("bridge:1-" + name + ":" + name) }
func (f *mcFixture) flowARN(name string) string   { return f.arn("flow:1-" + name + ":" + name) }
func (f *mcFixture) entARN(name string) string    { return f.arn("entitlement:1-flow-tagged:" + name) }
func (f *mcFixture) outARN(name string) string    { return f.arn("output:1-" + name + ":" + name) }
func (f *mcFixture) srcARN(name string) string    { return f.arn("source:1-" + name + ":" + name) }

func (f *mcFixture) routerARN(kind, id string) string { return f.arn(kind + ":" + id) }

// vpcNativeID is a VPC interface row's NativeID; vpcTagARN is the Service
// Reference ARN its tags are read with.
func (f *mcFixture) vpcNativeID(flow, name string) string {
	return f.flowARN(flow) + "/vpc-interface/" + name
}

func (f *mcFixture) vpcTagARN(flow, name string) string {
	return f.flowARN(flow) + "/vpcInterface/" + name
}

func (f *mcFixture) flow(name string) mctypes.ListedFlow {
	return mctypes.ListedFlow{
		FlowArn: sdkaws.String(f.flowARN(name)), Name: sdkaws.String("name-" + name),
		AvailabilityZone: sdkaws.String(f.region + "a"), Description: sdkaws.String("d"),
		SourceType: mctypes.SourceTypeOwned, Status: mctypes.StatusActive,
	}
}

func (f *mcFixture) unnamedFlow(name string) mctypes.ListedFlow {
	fl := f.flow(name)
	fl.Name = nil
	return fl
}

func (f *mcFixture) entitlement(name string) mctypes.Entitlement {
	return mctypes.Entitlement{EntitlementArn: sdkaws.String(f.entARN(name)), Name: sdkaws.String(name), Subscribers: []string{"111122223333"}}
}

func (f *mcFixture) output(name string) mctypes.Output {
	return mctypes.Output{OutputArn: sdkaws.String(f.outARN(name)), Name: sdkaws.String(name), Port: sdkaws.Int32(5000)}
}

func (f *mcFixture) unnamedOutput(name string) mctypes.Output {
	o := f.output(name)
	o.Name = nil
	return o
}

func (f *mcFixture) source(name string) mctypes.Source {
	return mctypes.Source{SourceArn: sdkaws.String(f.srcARN(name)), Name: sdkaws.String(name), IngestPort: sdkaws.Int32(6000)}
}

func mcVpcInterface(name string) mctypes.VpcInterface {
	return mctypes.VpcInterface{
		Name: sdkaws.String(name), SubnetId: sdkaws.String("subnet-1"), SecurityGroupIds: []string{"sg-1"},
		NetworkInterfaceIds: []string{"eni-1"}, NetworkInterfaceType: mctypes.NetworkInterfaceTypeEna,
	}
}

func (f *mcFixture) routerInput(id string) mctypes.ListedRouterInput {
	return mctypes.ListedRouterInput{
		Arn: sdkaws.String(f.routerARN("routerInput", id)), Id: sdkaws.String(id), Name: sdkaws.String("name-" + id),
		RegionName: sdkaws.String(f.region), State: mctypes.RouterInputStateActive,
	}
}

func (f *mcFixture) routerNI(id string) mctypes.ListedRouterNetworkInterface {
	return mctypes.ListedRouterNetworkInterface{
		Arn: sdkaws.String(f.routerARN("routerNetworkInterface", id)), Id: sdkaws.String(id), Name: sdkaws.String("name-" + id),
		RegionName: sdkaws.String(f.region), State: mctypes.RouterNetworkInterfaceStateActive,
	}
}

func (f *mcFixture) unnamedRouterNI(id string) mctypes.ListedRouterNetworkInterface {
	r := f.routerNI(id)
	r.Name = nil
	return r
}

func (f *mcFixture) routerOutput(id string) mctypes.ListedRouterOutput {
	return mctypes.ListedRouterOutput{
		Arn: sdkaws.String(f.routerARN("routerOutput", id)), Id: sdkaws.String(id), Name: sdkaws.String("name-" + id),
		RegionName: sdkaws.String(f.region), State: mctypes.RouterOutputStateActive,
	}
}

// allTagARNs is every ARN ListTagsForResource is sent, sorted: the flows and
// their children, each VPC interface by its Service Reference ARN.
func (f *mcFixture) allTagARNs() []string {
	return slices.Sorted(slices.Values([]string{
		f.flowARN("flow-tagged"), f.flowARN("flow-bare"), f.entARN("e-1"), f.outARN("o-1"), f.outARN("o-2"),
		f.srcARN("s-1"), f.srcARN("s-2"), f.vpcTagARN("flow-tagged", "vpc-1"), f.vpcTagARN("flow-bare", "vpc-2"),
	}))
}

// allGlobalTagARNs is every ARN ListTagsForGlobalResource is sent, sorted.
func (f *mcFixture) allGlobalTagARNs() []string {
	return slices.Sorted(slices.Values([]string{
		f.routerARN("routerInput", "ri-tagged"), f.routerARN("routerInput", "ri-bare"),
		f.routerARN("routerNetworkInterface", "rni-tagged"), f.routerARN("routerNetworkInterface", "rni-bare"),
		f.routerARN("routerOutput", "ro-tagged"), f.routerARN("routerOutput", "ro-bare"),
	}))
}

func (f *mcFixture) record(op string, in any) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls[op]++
	if n := bacField[*int32](in, "MaxResults"); n != nil {
		f.maxResults[op] = append(f.maxResults[op], *n)
	}
}

func (f *mcFixture) count(op string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls[op]
}

func (f *mcFixture) sentTagARNs() (tags, global []string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Sorted(slices.Values(f.tagARNs)), slices.Sorted(slices.Values(f.globalTagARNs))
}

// mcFixturePage answers one page of pages: the second when token is "p2",
// else the first with NextToken "p2" when a second exists, or the error
// listErrs holds for op (op+"/p2" for the second page).
func mcFixturePage[T any](f *mcFixture, op string, in any, token *string, pages [][]T) ([]T, *string, error) {
	f.record(op, in)
	page, errKey := 0, op
	if sdkaws.ToString(token) == "p2" {
		page, errKey = 1, op+"/p2"
	}
	f.mu.Lock()
	err := f.listErrs[errKey]
	f.mu.Unlock()
	if err != nil {
		return nil, nil, err
	}
	var items []T
	if page < len(pages) {
		items = pages[page]
	}
	var next *string
	if page == 0 && len(pages) > 1 {
		next = sdkaws.String("p2")
	}
	return items, next, nil
}

func (f *mcFixture) readTags(arn string, sent *[]string) (map[string]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	*sent = append(*sent, arn)
	if err := f.errs[arn]; err != nil {
		return nil, err
	}
	return f.tags[arn], nil
}

func (f *mcFixture) handlers() map[string]func(in any) (any, error) {
	return map[string]func(in any) (any, error){
		"ListBridges": func(in any) (any, error) {
			items, _, err := mcFixturePage(f, "ListBridges", in, nil, [][]mctypes.ListedBridge{f.bridges})
			return &mediaconnect.ListBridgesOutput{Bridges: items}, err
		},
		"DescribeBridge": func(in any) (any, error) {
			f.record("DescribeBridge", in)
			f.mu.Lock()
			defer f.mu.Unlock()
			if err := f.bridgeErrs[sdkaws.ToString(in.(*mediaconnect.DescribeBridgeInput).BridgeArn)]; err != nil {
				return nil, err
			}
			return &mediaconnect.DescribeBridgeOutput{}, nil
		},
		"ListGateways": func(in any) (any, error) {
			_, _, err := mcFixturePage[mctypes.ListedGateway](f, "ListGateways", in, nil, nil)
			return &mediaconnect.ListGatewaysOutput{}, err
		},
		"ListReservations": func(in any) (any, error) {
			_, _, err := mcFixturePage[mctypes.Reservation](f, "ListReservations", in, nil, nil)
			return &mediaconnect.ListReservationsOutput{}, err
		},
		"ListGatewayInstances": func(in any) (any, error) {
			_, _, err := mcFixturePage[mctypes.ListedGatewayInstance](f, "ListGatewayInstances", in, nil, nil)
			return &mediaconnect.ListGatewayInstancesOutput{}, err
		},
		"ListFlows": func(in any) (any, error) {
			items, next, err := mcFixturePage(f, "ListFlows", in, in.(*mediaconnect.ListFlowsInput).NextToken, f.flows)
			if err != nil {
				return nil, err
			}
			return &mediaconnect.ListFlowsOutput{Flows: items, NextToken: next}, nil
		},
		"DescribeFlow": func(in any) (any, error) {
			f.record("DescribeFlow", in)
			arn := sdkaws.ToString(in.(*mediaconnect.DescribeFlowInput).FlowArn)
			f.described.add(arn)
			f.mu.Lock()
			defer f.mu.Unlock()
			if err := f.describeErrs[arn]; err != nil {
				return nil, err
			}
			return &mediaconnect.DescribeFlowOutput{Flow: f.bodies[arn]}, nil
		},
		"ListRouterInputs": func(in any) (any, error) {
			items, next, err := mcFixturePage(f, "ListRouterInputs", in, in.(*mediaconnect.ListRouterInputsInput).NextToken, f.inputs)
			if err != nil {
				return nil, err
			}
			return &mediaconnect.ListRouterInputsOutput{RouterInputs: items, NextToken: next}, nil
		},
		"ListRouterNetworkInterfaces": func(in any) (any, error) {
			items, next, err := mcFixturePage(f, "ListRouterNetworkInterfaces", in, in.(*mediaconnect.ListRouterNetworkInterfacesInput).NextToken, f.nis)
			if err != nil {
				return nil, err
			}
			return &mediaconnect.ListRouterNetworkInterfacesOutput{RouterNetworkInterfaces: items, NextToken: next}, nil
		},
		"ListRouterOutputs": func(in any) (any, error) {
			items, next, err := mcFixturePage(f, "ListRouterOutputs", in, in.(*mediaconnect.ListRouterOutputsInput).NextToken, f.outputs)
			if err != nil {
				return nil, err
			}
			return &mediaconnect.ListRouterOutputsOutput{RouterOutputs: items, NextToken: next}, nil
		},
		"ListTagsForResource": func(in any) (any, error) {
			tags, err := f.readTags(sdkaws.ToString(in.(*mediaconnect.ListTagsForResourceInput).ResourceArn), &f.tagARNs)
			if err != nil {
				return nil, err
			}
			return &mediaconnect.ListTagsForResourceOutput{Tags: tags}, nil
		},
		"ListTagsForGlobalResource": func(in any) (any, error) {
			tags, err := f.readTags(sdkaws.ToString(in.(*mediaconnect.ListTagsForGlobalResourceInput).ResourceArn), &f.globalTagARNs)
			if err != nil {
				return nil, err
			}
			return &mediaconnect.ListTagsForGlobalResourceOutput{Tags: tags}, nil
		},
	}
}

// mcScan runs the scan's real entry point against handlers in the fixture's
// partition and region; every op they do not answer is denied.
func mcScan(ctx context.Context, st *store.Store, f *mcFixture, handlers map[string]func(in any) (any, error)) (int, int, error) {
	acct := newTestAccount(testAccountID)
	acct.Partition = f.partition
	acct.cfg = opStubConfig(handlers, mcTestDenied)
	return scanMediaConnect(ctx, acct, f.region, st, testScanID)
}

func mcWarnOps(w []store.ScanWarning) []string {
	var ops []string
	for _, x := range w {
		ops = append(ops, x.Service)
	}
	return ops
}

// mcRowCase is one stored row a full fixture scan must produce.
type mcRowCase struct {
	typ, nativeID, name string
	attrs               any
	tags                map[string]string
}

func (f *mcFixture) rowCases() []mcRowCase {
	return []mcRowCase{
		{TypeMediaConnectFlow, f.flowARN("flow-tagged"), "name-flow-tagged", f.flow("flow-tagged"), map[string]string{"type": TypeMediaConnectFlow}},
		// flow-bare, o-2 and rni-bare have no Name: the flow and the output are
		// named by their ARN, the network interface by its Id.
		{TypeMediaConnectFlow, f.flowARN("flow-bare"), f.flowARN("flow-bare"), f.unnamedFlow("flow-bare"), nil},
		{TypeMediaConnectFlowEntitlement, f.entARN("e-1"), "e-1", f.entitlement("e-1"), map[string]string{"type": TypeMediaConnectFlowEntitlement}},
		{TypeMediaConnectFlowOutput, f.outARN("o-1"), "o-1", f.output("o-1"), map[string]string{"type": TypeMediaConnectFlowOutput}},
		{TypeMediaConnectFlowOutput, f.outARN("o-2"), f.outARN("o-2"), f.unnamedOutput("o-2"), nil},
		// s-1 is stored from Sources, not from the Source copy of it.
		{TypeMediaConnectFlowSource, f.srcARN("s-1"), "s-1", f.source("s-1"), map[string]string{"type": TypeMediaConnectFlowSource}},
		{TypeMediaConnectFlowSource, f.srcARN("s-2"), "s-2", f.source("s-2"), nil},
		{TypeMediaConnectFlowVpcInterface, f.vpcNativeID("flow-tagged", "vpc-1"), "vpc-1", mcVpcInterface("vpc-1"), map[string]string{"type": TypeMediaConnectFlowVpcInterface}},
		{TypeMediaConnectFlowVpcInterface, f.vpcNativeID("flow-bare", "vpc-2"), "vpc-2", mcVpcInterface("vpc-2"), nil},
		{TypeMediaConnectRouterInput, f.routerARN("routerInput", "ri-tagged"), "ri-tagged", f.routerInput("ri-tagged"), map[string]string{"type": TypeMediaConnectRouterInput}},
		{TypeMediaConnectRouterInput, f.routerARN("routerInput", "ri-bare"), "ri-bare", f.routerInput("ri-bare"), nil},
		{TypeMediaConnectRouterNetworkInterface, f.routerARN("routerNetworkInterface", "rni-tagged"), "name-rni-tagged", f.routerNI("rni-tagged"), map[string]string{"type": TypeMediaConnectRouterNetworkInterface}},
		{TypeMediaConnectRouterNetworkInterface, f.routerARN("routerNetworkInterface", "rni-bare"), "rni-bare", f.unnamedRouterNI("rni-bare"), nil},
		{TypeMediaConnectRouterOutput, f.routerARN("routerOutput", "ro-tagged"), "ro-tagged", f.routerOutput("ro-tagged"), map[string]string{"type": TypeMediaConnectRouterOutput}},
		{TypeMediaConnectRouterOutput, f.routerARN("routerOutput", "ro-bare"), "ro-bare", f.routerOutput("ro-bare"), nil},
	}
}

// Flows, their entitlements, outputs, sources and VPC interfaces, and the
// router inputs, network interfaces and outputs are stored with their tags
// (nil for none), their listed or described element as attributes, their
// name and no Status or CreatedAt. ListTagsForResource is sent exactly the
// flows' and children's ARNs, a VPC interface's built from its flow's;
// ListTagsForGlobalResource exactly the router ARNs. A flow's Source missing
// from its Sources is stored; one in Sources is stored once. Every flow is
// described and every listing sends its model's page cap. No error, no
// warning.
func TestScanMediaConnect_StoresTags(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	warns := rhWarnRecorder(st)
	f := newMCFixture()

	total, inserted, err := mcScan(context.Background(), st, f, f.handlers())
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != mcTestTotal || inserted != mcTestTotal {
		t.Errorf("total, inserted = %d, %d; want %d, %d", total, inserted, mcTestTotal, mcTestTotal)
	}
	tagARNs, globalARNs := f.sentTagARNs()
	if !slices.Equal(tagARNs, f.allTagARNs()) {
		t.Errorf("ListTagsForResource ARNs = %v\nwant %v", tagARNs, f.allTagARNs())
	}
	if !slices.Equal(globalARNs, f.allGlobalTagARNs()) {
		t.Errorf("ListTagsForGlobalResource ARNs = %v\nwant %v", globalARNs, f.allGlobalTagARNs())
	}
	if got, want := f.described.distinct(), []string{f.flowARN("flow-bare"), f.flowARN("flow-tagged")}; !slices.Equal(got, want) {
		t.Errorf("DescribeFlow flows = %v; want %v", got, want)
	}
	for op, want := range map[string][]int32{
		"ListFlows": {1000, 1000}, "ListRouterInputs": {100, 100},
		"ListRouterNetworkInterfaces": {100, 100}, "ListRouterOutputs": {100, 100},
	} {
		if got := f.maxResults[op]; !slices.Equal(got, want) {
			t.Errorf("%s MaxResults = %v; want %v", op, got, want)
		}
	}
	perType := map[string]int{}
	for _, c := range f.rowCases() {
		perType[c.typ]++
		r, ok := rowsByNativeID(t, st, c.typ)[c.nativeID]
		if !ok {
			t.Errorf("%s %s not stored", c.typ, c.nativeID)
			continue
		}
		if sv(r.Name) != c.name || sv(r.Region) != f.region || r.Status != nil || r.CreatedAt != nil || r.ManagedByProvider {
			t.Errorf("%s: Name %q Region %q Status %v CreatedAt %v ManagedByProvider %v; want %q %s, no Status or CreatedAt, false",
				c.nativeID, sv(r.Name), sv(r.Region), r.Status, r.CreatedAt, r.ManagedByProvider, c.name, f.region)
		}
		if want := mustJSON(c.attrs); r.AttributesJSON != want {
			t.Errorf("%s attrs = %s\nwant %s", c.nativeID, r.AttributesJSON, want)
		}
		assertTagsMapOrNil(t, r.TagsJSON, c.tags)
	}
	for typ, want := range perType {
		if n := len(rowsByNativeID(t, st, typ)); n != want {
			t.Errorf("%s rows = %d; want %d", typ, n, want)
		}
	}
	if w := warns(); len(w) != 0 {
		t.Errorf("warnings = %v; want none", w)
	}
}

// A rescan reports every stored row in its total, not only the newly inserted
// ones. The dispatcher reports total for the service, and a first scan into an
// empty store has total == inserted, so only a rescan catches the two swapped.
func TestScanMediaConnect_RescanKeepsTotal(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	f := newMCFixture()
	if _, _, err := mcScan(context.Background(), st, f, f.handlers()); err != nil {
		t.Fatalf("first scan: %v", err)
	}
	total, _, err := mcScan(context.Background(), st, f, f.handlers())
	if err != nil || total != mcTestTotal {
		t.Errorf("rescan total = %d, err = %v; want %d, nil", total, err, mcTestTotal)
	}
}

// Empty listings: each listing called once, no flow described, no tag read,
// nothing stored, no error, no warning.
func TestScanMediaConnect_Empty(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	warns := rhWarnRecorder(st)
	f := newMCFixture()
	f.flows, f.inputs, f.nis, f.outputs = nil, nil, nil, nil

	total, _, err := mcScan(context.Background(), st, f, f.handlers())
	if err != nil || total != 0 {
		t.Fatalf("total, err = %d, %v; want 0, nil", total, err)
	}
	for _, op := range []string{"ListBridges", "ListFlows", "ListGateways", "ListRouterInputs", "ListRouterNetworkInterfaces", "ListRouterOutputs", "ListReservations", "ListGatewayInstances"} {
		if n := f.count(op); n != 1 {
			t.Errorf("%s called %d times; want 1", op, n)
		}
	}
	if n := f.count("DescribeFlow"); n != 0 {
		t.Errorf("DescribeFlow called %d times; want 0", n)
	}
	if tags, global := f.sentTagARNs(); len(tags)+len(global) != 0 {
		t.Errorf("tag reads = %v %v; want none", tags, global)
	}
	if w := warns(); len(w) != 0 {
		t.Errorf("warnings = %v; want none", w)
	}
}

// A failed tag read. NotFoundException is modeled by both tag ops, so a
// resource deleted since it was listed is dropped silently, and a flow so
// dropped is not described. A VPC interface, read by a built ARN, is kept
// untagged on NotFoundException and the read returned as an error. A denial
// keeps the row untagged and warns once under its tag op with the scan's
// scope; a throttle keeps it and is transient; any other error keeps it and
// is an error.
func TestScanMediaConnect_TagReadFailures(t *testing.T) {
	t.Parallel()
	f := newMCFixture()
	for _, c := range []struct {
		name      string
		typ, arn  string // the row's type and NativeID
		tagARN    string // the ARN failing; arn when empty
		err       error
		outcome   serviceOutcome // -1: no error
		code      string
		sentinel  error
		wantTotal int
		wantKept  bool
		warn      string
	}{
		{name: "flow/not found", typ: TypeMediaConnectFlow, arn: f.flowARN("flow-tagged"), err: mcTestGone, outcome: -1, wantTotal: mcTestTotal - 5},
		{name: "entitlement/not found", typ: TypeMediaConnectFlowEntitlement, arn: f.entARN("e-1"), err: mcTestGone, outcome: -1, wantTotal: mcTestTotal - 1},
		{name: "router input/not found", typ: TypeMediaConnectRouterInput, arn: f.routerARN("routerInput", "ri-tagged"), err: mcTestGone, outcome: -1, wantTotal: mcTestTotal - 1},
		{name: "VPC interface/not found kept", typ: TypeMediaConnectFlowVpcInterface, arn: f.vpcNativeID("flow-tagged", "vpc-1"), tagARN: f.vpcTagARN("flow-tagged", "vpc-1"), err: mcTestGone, outcome: outcomeError, sentinel: errMCVpcInterfaceNotFound, wantTotal: mcTestTotal, wantKept: true},
		{name: "flow/denied", typ: TypeMediaConnectFlow, arn: f.flowARN("flow-tagged"), err: mcTestDenied, outcome: -1, wantTotal: mcTestTotal, wantKept: true, warn: mcTagsOp},
		{name: "router output/denied", typ: TypeMediaConnectRouterOutput, arn: f.routerARN("routerOutput", "ro-tagged"), err: mcTestDenied, outcome: -1, wantTotal: mcTestTotal, wantKept: true, warn: mcGlobalTagsOp},
		{name: "source/transient", typ: TypeMediaConnectFlowSource, arn: f.srcARN("s-1"), err: mcTestThrottled, outcome: outcomeTransient, code: "TooManyRequestsException", wantTotal: mcTestTotal, wantKept: true},
		{name: "router network interface/other error", typ: TypeMediaConnectRouterNetworkInterface, arn: f.routerARN("routerNetworkInterface", "rni-tagged"), err: mcTestBad, outcome: outcomeError, code: "BadRequestException", wantTotal: mcTestTotal, wantKept: true},
	} {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := rhWarnRecorder(st)
			f := newMCFixture()
			f.errs[cmp.Or(c.tagARN, c.arn)] = c.err

			total, _, err := mcScan(context.Background(), st, f, f.handlers())
			switch {
			case c.outcome < 0:
				if err != nil {
					t.Fatalf("err = %v; want nil", err)
				}
			case c.sentinel != nil:
				assertGlueOutcome(t, err, c.outcome)
				if !errors.Is(err, c.sentinel) || isAPIErrorCode(err, "NotFoundException") {
					t.Errorf("err = %v; want %v without the NotFoundException code", err, c.sentinel)
				}
			default:
				assertGlueOutcome(t, err, c.outcome)
				assertAPICode(t, err, c.code)
			}
			if total != c.wantTotal {
				t.Errorf("total = %d; want %d", total, c.wantTotal)
			}
			r, ok := rowsByNativeID(t, st, c.typ)[c.arn]
			if ok != c.wantKept {
				t.Fatalf("row stored = %v; want %v", ok, c.wantKept)
			}
			if ok && r.TagsJSON != nil {
				t.Errorf("TagsJSON = %q; want nil", *r.TagsJSON)
			}
			wantDescribed := []string{f.flowARN("flow-bare"), f.flowARN("flow-tagged")}
			if c.typ == TypeMediaConnectFlow && !c.wantKept {
				wantDescribed = wantDescribed[:1]
			}
			if got := f.described.distinct(); !slices.Equal(got, wantDescribed) {
				t.Errorf("DescribeFlow flows = %v; want %v", got, wantDescribed)
			}
			w := warns()
			var want []string
			if c.warn != "" {
				want = []string{c.warn}
			}
			if got := mcWarnOps(w); !slices.Equal(got, want) {
				t.Fatalf("warnings = %v; want %v", w, want)
			}
			for _, x := range w {
				if x.Scope != testAccountID+"/"+testRegion {
					t.Errorf("warning Scope %q; want %s/%s", x.Scope, testAccountID, testRegion)
				}
			}
		})
	}
}

// Denied tag reads across phases send one warning per tag op for the whole
// scan; the rows are kept.
func TestScanMediaConnect_TagDenialsWarnOncePerOp(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	warns := rhWarnRecorder(st)
	f := newMCFixture()
	for _, arn := range []string{
		f.flowARN("flow-bare"), f.outARN("o-1"), f.vpcTagARN("flow-bare", "vpc-2"),
		f.routerARN("routerInput", "ri-tagged"), f.routerARN("routerOutput", "ro-bare"),
	} {
		f.errs[arn] = mcTestDenied
	}

	total, _, err := mcScan(context.Background(), st, f, f.handlers())
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != mcTestTotal {
		t.Errorf("total = %d; want %d", total, mcTestTotal)
	}
	if got := mcWarnOps(warns()); !slices.Equal(got, []string{mcTagsOp, mcGlobalTagsOp}) {
		t.Errorf("warnings = %v; want one under %s and one under %s", got, mcTagsOp, mcGlobalTagsOp)
	}
}

// A throttled tag read in one phase beside a hard failure of the same op in
// another is demoted to one warning, so the hard error decides the outcome.
func TestScanMediaConnect_TagThrottleDemotedBesideHardError(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	warns := countOpWarnings(st, mcTagsOp)
	f := newMCFixture()
	f.errs[f.flowARN("flow-bare")] = mcTestThrottled
	f.errs[f.outARN("o-1")] = mcTestBad

	_, _, err := mcScan(context.Background(), st, f, f.handlers())
	assertGlueOutcome(t, err, outcomeError)
	assertAPICode(t, err, "BadRequestException")
	if *warns != 1 {
		t.Errorf("%s warnings = %d; want 1", mcTagsOp, *warns)
	}
}

// A failed DescribeFlow costs the other flow none of its children, and the
// flow itself stays stored. A flow no longer found is skipped silently; a
// denial warns once under DescribeFlow; a throttle is transient; an answer
// without a flow and any other error are errors.
func TestScanMediaConnect_DescribeFlowFailures(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name     string
		err      error // nil: answered without a flow
		outcome  serviceOutcome
		code     string
		sentinel error
		warn     bool
	}{
		{name: "not found", err: mcTestGone, outcome: -1},
		{name: "denied", err: mcTestDenied, outcome: -1, warn: true},
		{name: "transient", err: mcTestThrottled, outcome: outcomeTransient, code: "TooManyRequestsException"},
		{name: "other error", err: mcTestBad, outcome: outcomeError, code: "BadRequestException"},
		{name: "no body", outcome: outcomeError, sentinel: errMCNoFlowBody},
	} {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := rhWarnRecorder(st)
			f := newMCFixture()
			failing := f.flowARN("flow-tagged")
			if c.err != nil {
				f.describeErrs[failing] = c.err
			} else {
				delete(f.bodies, failing)
			}

			total, _, err := mcScan(context.Background(), st, f, f.handlers())
			switch {
			case c.outcome < 0:
				if err != nil {
					t.Fatalf("err = %v; want nil", err)
				}
			case c.sentinel != nil:
				assertGlueOutcome(t, err, c.outcome)
				if !errors.Is(err, c.sentinel) {
					t.Errorf("err = %v; want %v", err, c.sentinel)
				}
			default:
				assertGlueOutcome(t, err, c.outcome)
				assertAPICode(t, err, c.code)
			}
			if want := mcTestTotal - 4; total != want {
				t.Errorf("total = %d; want %d (flow-tagged's 4 children lost)", total, want)
			}
			if _, ok := rowsByNativeID(t, st, TypeMediaConnectFlow)[failing]; !ok {
				t.Error("the flow whose DescribeFlow failed is not stored")
			}
			for typ, id := range map[string]string{
				TypeMediaConnectFlowOutput:       f.outARN("o-2"),
				TypeMediaConnectFlowSource:       f.srcARN("s-2"),
				TypeMediaConnectFlowVpcInterface: f.vpcNativeID("flow-bare", "vpc-2"),
			} {
				if _, ok := rowsByNativeID(t, st, typ)[id]; !ok {
					t.Errorf("sibling flow's child %s not stored", id)
				}
			}
			var want []string
			if c.warn {
				want = []string{mcDescribeFlowOp}
			}
			if got := mcWarnOps(warns()); !slices.Equal(got, want) {
				t.Errorf("warnings = %v; want %v", got, want)
			}
		})
	}
}

// A listing's failed later page keeps the page read before it, and no
// listing's or DescribeBridge's error stops the later phases; a denied later
// page is warned about under its op instead. A throttled DescribeBridge
// beside a hard error is demoted to one warning under DescribeBridge.
func TestScanMediaConnect_ListFailureKeepsPagesAndLaterPhases(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name        string
		listErrs    map[string]error
		bridgeErr   error // non-nil: ListBridges lists bridge b1 and DescribeBridge fails with it
		wantTotal   int
		wantFlows   int // flows described
		warnOp      string
		wantErrCode string
	}{
		{"flows page 2", map[string]error{"ListFlows/p2": mcTestBad}, nil, mcTestTotal - 4, 1, "", "BadRequestException"},
		{"flows page 1", map[string]error{"ListFlows": mcTestBad}, nil, 6, 0, "", "BadRequestException"},
		{"router inputs page 2 denied", map[string]error{"ListRouterInputs/p2": mcTestDenied}, nil, mcTestTotal - 1, 2, mcListRouterInputsOp, ""},
		{"bridges", map[string]error{"ListBridges": mcTestBad}, nil, mcTestTotal, 2, "", "BadRequestException"},
		{"gateways", map[string]error{"ListGateways": mcTestBad}, nil, mcTestTotal, 2, "", "BadRequestException"},
		{"describe bridge", nil, mcTestBad, mcTestTotal + 1, 2, "", "BadRequestException"},
		{"describe bridge throttled beside a hard error", map[string]error{"ListGateways": mcTestBad}, mcTestThrottled, mcTestTotal + 1, 2, mcDescribeBridgeOp, "BadRequestException"},
	} {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := rhWarnRecorder(st)
			f := newMCFixture()
			maps.Copy(f.listErrs, c.listErrs)
			if c.bridgeErr != nil {
				f.bridges = []mctypes.ListedBridge{{BridgeArn: sdkaws.String(f.bridgeARN("b1")), Name: sdkaws.String("b1")}}
				f.bridgeErrs[f.bridgeARN("b1")] = c.bridgeErr
			}

			total, _, err := mcScan(context.Background(), st, f, f.handlers())
			if c.wantErrCode != "" {
				assertGlueOutcome(t, err, outcomeError)
				assertAPICode(t, err, c.wantErrCode)
			} else if err != nil {
				t.Fatalf("err = %v; want nil", err)
			}
			if total != c.wantTotal {
				t.Errorf("total = %d; want %d", total, c.wantTotal)
			}
			if n := len(f.described.distinct()); n != c.wantFlows {
				t.Errorf("flows described = %d; want %d", n, c.wantFlows)
			}
			for _, op := range []string{"ListReservations", "ListGatewayInstances"} {
				if n := f.count(op); n != 1 {
					t.Errorf("%s called %d times; want 1 (the later phases still run)", op, n)
				}
			}
			var want []string
			if c.warnOp != "" {
				want = []string{c.warnOp}
			}
			if got := mcWarnOps(warns()); !slices.Equal(got, want) {
				t.Errorf("warnings = %v; want %v", got, want)
			}
		})
	}
}

// A repeated NextToken ends the listing as an error, keeping its pages (the
// one that repeated it included), and the later phases still run.
func TestScanMediaConnect_RepeatedTokenKeepsPages(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	f := newMCFixture()
	handlers := f.handlers()
	handlers["ListRouterOutputs"] = func(in any) (any, error) {
		f.record("ListRouterOutputs", in)
		if in.(*mediaconnect.ListRouterOutputsInput).NextToken == nil {
			return &mediaconnect.ListRouterOutputsOutput{RouterOutputs: f.outputs[0], NextToken: sdkaws.String("p2")}, nil
		}
		return &mediaconnect.ListRouterOutputsOutput{RouterOutputs: f.outputs[1], NextToken: sdkaws.String("p2")}, nil
	}

	total, _, err := mcScan(context.Background(), st, f, handlers)
	if !errors.Is(err, errRepeatedNextToken) {
		t.Fatalf("err = %v; want errRepeatedNextToken", err)
	}
	if n := f.count("ListRouterOutputs"); n != 2 {
		t.Errorf("ListRouterOutputs called %d times; want 2", n)
	}
	if total != mcTestTotal {
		t.Errorf("total = %d; want %d", total, mcTestTotal)
	}
	if n := f.count("ListGatewayInstances"); n != 1 {
		t.Errorf("ListGatewayInstances called %d times; want 1", n)
	}
}

// An element without an ARN (a VPC interface: without a name) is not stored,
// not sent to a tag op, and warned about once under its listing or
// DescribeFlow, keyed by its name, or for a nameless VPC interface by its
// flow's ARN.
func TestScanMediaConnect_ElementsWithoutIdentifier(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	warns := rhWarnRecorder(st)
	f := newMCFixture()
	noARNFlow := f.flow("flow-noarn")
	noARNFlow.FlowArn = nil
	f.flows[1] = append(f.flows[1], noARNFlow)
	noARNInput := f.routerInput("ri-noarn")
	noARNInput.Arn = nil
	f.inputs[1] = append(f.inputs[1], noARNInput)
	noARNEnt := f.entitlement("e-noarn")
	noARNEnt.EntitlementArn = nil
	tagged := f.bodies[f.flowARN("flow-tagged")]
	tagged.Entitlements = append(tagged.Entitlements, noARNEnt)
	bare := f.bodies[f.flowARN("flow-bare")]
	bare.VpcInterfaces = append(bare.VpcInterfaces, mctypes.VpcInterface{SubnetId: sdkaws.String("subnet-2")})

	total, _, err := mcScan(context.Background(), st, f, f.handlers())
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != mcTestTotal {
		t.Errorf("total = %d; want %d", total, mcTestTotal)
	}
	tagARNs, globalARNs := f.sentTagARNs()
	if !slices.Equal(tagARNs, f.allTagARNs()) || !slices.Equal(globalARNs, f.allGlobalTagARNs()) {
		t.Errorf("tag reads = %v %v; want %v %v", tagARNs, globalARNs, f.allTagARNs(), f.allGlobalTagARNs())
	}
	w := warns()
	if got := mcWarnOps(w); !slices.Equal(got, []string{mcListFlowsOp, mcDescribeFlowOp, mcDescribeFlowOp, mcListRouterInputsOp}) {
		t.Fatalf("warnings = %v; want one under ListFlows, two under DescribeFlow, one under ListRouterInputs", w)
	}
	for _, c := range []struct{ op, first string }{
		{mcListFlowsOp, "name-flow-noarn"},
		{mcDescribeFlowOp, "e-noarn"},
		{mcDescribeFlowOp, "a VPC interface of " + f.flowARN("flow-bare")},
		{mcListRouterInputsOp, "name-ri-noarn"},
	} {
		if !slices.ContainsFunc(w, func(x store.ScanWarning) bool {
			return x.Service == c.op && strings.HasSuffix(x.Message, "first "+c.first)
		}) {
			t.Errorf("no %s warning keyed %q in %v", c.op, c.first, w)
		}
	}
}

// A GovCloud or China ARN reaches the tag ops unchanged and is the row's
// NativeID; a VPC interface's tag ARN is built on its flow's ARN, so it is
// in the flow's partition.
func TestScanMediaConnect_TagReadsKeepPartitionARNs(t *testing.T) {
	t.Parallel()
	for _, c := range []struct{ partition, region string }{
		{"aws-us-gov", "us-gov-west-1"},
		{"aws-cn", "cn-north-1"},
	} {
		t.Run(c.partition, func(t *testing.T) {
			st := newTestStore(t)
			f := newMCFixtureIn(c.partition, c.region)
			if _, _, err := mcScan(context.Background(), st, f, f.handlers()); err != nil {
				t.Fatalf("scan: %v", err)
			}
			tagARNs, globalARNs := f.sentTagARNs()
			if !slices.Equal(tagARNs, f.allTagARNs()) {
				t.Errorf("ListTagsForResource ARNs = %v\nwant %v", tagARNs, f.allTagARNs())
			}
			if !slices.Equal(globalARNs, f.allGlobalTagARNs()) {
				t.Errorf("ListTagsForGlobalResource ARNs = %v\nwant %v", globalARNs, f.allGlobalTagARNs())
			}
			vpcTag := "arn:" + c.partition + ":mediaconnect:" + c.region + ":" + testAccountID + ":flow:1-flow-tagged:flow-tagged/vpcInterface/vpc-1"
			if !slices.Contains(tagARNs, vpcTag) {
				t.Errorf("ListTagsForResource ARNs = %v; want %s among them", tagARNs, vpcTag)
			}
			for _, rc := range f.rowCases() {
				r, ok := rowsByNativeID(t, st, rc.typ)[rc.nativeID]
				if !ok {
					t.Errorf("%s not stored", rc.nativeID)
					continue
				}
				assertTagsMapOrNil(t, r.TagsJSON, rc.tags)
			}
		})
	}
}

// A failed write stops the scan, in the flows' phase, in DescribeFlow's or in
// a router phase: no warning about the rows it lost (a denied tag read among
// them) and no later phase runs.
func TestScanMediaConnect_FailedWriteStopsScan(t *testing.T) {
	t.Parallel()
	f := newMCFixture()
	for _, c := range []struct {
		name, refused string
		denied        string // a tag read of the refused batch that is denied
		withBridge    bool
		wantTotal     int
		notCalled     []string
	}{
		{"bridges", TypeMediaConnectBridge, "", true, 0, []string{"ListFlows", "DescribeBridge", "ListGatewayInstances"}},
		{"flows", TypeMediaConnectFlow, f.flowARN("flow-tagged"), false, 0, []string{"DescribeFlow", "ListRouterInputs", "ListGatewayInstances"}},
		{"DescribeFlow", TypeMediaConnectFlowOutput, f.outARN("o-1"), false, 2, []string{"ListGateways", "ListRouterInputs", "ListGatewayInstances"}},
		{"router inputs", TypeMediaConnectRouterInput, f.routerARN("routerInput", "ri-tagged"), false, 9, []string{"ListRouterNetworkInterfaces", "ListRouterOutputs", "ListGatewayInstances"}},
		{"router network interfaces", TypeMediaConnectRouterNetworkInterface, f.routerARN("routerNetworkInterface", "rni-tagged"), false, 11, []string{"ListRouterOutputs", "ListReservations", "ListGatewayInstances"}},
		{"router outputs", TypeMediaConnectRouterOutput, f.routerARN("routerOutput", "ro-tagged"), false, 13, []string{"ListReservations", "ListGatewayInstances"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := rhWarnRecorder(st)
			refuseStoreWrites(t, st, c.refused)
			f := newMCFixture()
			if c.denied != "" {
				f.errs[c.denied] = mcTestDenied
			}
			if c.withBridge {
				f.bridges = []mctypes.ListedBridge{{BridgeArn: sdkaws.String(f.bridgeARN("b1")), Name: sdkaws.String("b1")}}
			}

			total, _, err := mcScan(context.Background(), st, f, f.handlers())
			if !errors.Is(err, store.ErrStoreWrite) {
				t.Fatalf("err = %v; want store.ErrStoreWrite", err)
			}
			if w := warns(); len(w) != 0 {
				t.Errorf("warnings = %v; want none", w)
			}
			for _, op := range c.notCalled {
				if n := f.count(op); n != 0 {
					t.Errorf("%s called %d times; want 0 after the failed write", op, n)
				}
			}
			if total != c.wantTotal {
				t.Errorf("total = %d; want %d", total, c.wantTotal)
			}
		})
	}
}

// Once ctx ends, the stop is one member of the error, although the phase in
// flight saw it in its listing, and the later phases are not run.
func TestScanMediaConnect_ContextEndReportedOnce(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	f := newMCFixture()
	handlers := f.handlers()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	list := handlers["ListRouterInputs"]
	handlers["ListRouterInputs"] = func(in any) (any, error) {
		if in.(*mediaconnect.ListRouterInputsInput).NextToken != nil {
			cancel()
			return nil, context.Canceled
		}
		return list(in)
	}

	_, _, err := mcScan(ctx, st, f, handlers)
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
	for _, op := range []string{"ListRouterNetworkInterfaces", "ListRouterOutputs", "ListGatewayInstances"} {
		if n := f.count(op); n != 0 {
			t.Errorf("%s called %d times after ctx ended", op, n)
		}
	}
	if _, ok := rowsByNativeID(t, st, TypeMediaConnectRouterInput)[f.routerARN("routerInput", "ri-tagged")]; !ok {
		t.Error("router input of the page read before the stop not kept")
	}
}

// With nothing stored, an NXDOMAIN on the first listing means the region
// does not serve the service: no later phase runs and nothing is warned.
// Once rows were stored, an NXDOMAIN (a tag read's included) is a failure of
// this scan.
func TestScanMediaConnect_NXDOMAIN(t *testing.T) {
	t.Parallel()
	nx := &net.DNSError{Err: "no such host", Name: "mediaconnect.us-east-1.amazonaws.com", IsNotFound: true}
	t.Run("nothing stored", func(t *testing.T) {
		st := newTestStore(t)
		warns := rhWarnRecorder(st)
		f := newMCFixture()
		f.listErrs["ListBridges"] = nx
		_, _, err := mcScan(context.Background(), st, f, f.handlers())
		assertGlueOutcome(t, err, outcomeUnavailable)
		if w := warns(); len(w) != 0 {
			t.Errorf("warnings = %v; want none", w)
		}
		if n := f.count("ListFlows"); n != 0 {
			t.Errorf("ListFlows called %d times; want 0 once the region proved unavailable", n)
		}
	})
	t.Run("a tag read", func(t *testing.T) {
		st := newTestStore(t)
		f := newMCFixture()
		f.errs[f.routerARN("routerOutput", "ro-bare")] = nx
		total, _, err := mcScan(context.Background(), st, f, f.handlers())
		assertGlueOutcome(t, err, outcomeError)
		if total != mcTestTotal {
			t.Errorf("total = %d; want %d", total, mcTestTotal)
		}
	})
}

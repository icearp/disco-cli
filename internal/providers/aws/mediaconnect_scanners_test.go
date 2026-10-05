package aws

import (
	"context"
	"encoding/json"
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
	return cloud9CfgWithStub(stubResponses(t, calls), testRegion)
}

// TestScanMediaConnect_GatewayInstancesAfterExistingPhases runs the whole
// service against the real SDK client: the instance is stored and contained by
// the gateway listed earlier in the same scan.
func TestScanMediaConnect_GatewayInstancesAfterExistingPhases(t *testing.T) {
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

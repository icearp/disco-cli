package aws

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"maps"
	"net"
	"slices"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/gamelift"
	gltypes "github.com/aws/aws-sdk-go-v2/service/gamelift/types"
	"github.com/aws/smithy-go"
	"github.com/icearp/disco-cli/store"
)

var (
	glDenied    = &smithy.GenericAPIError{Code: "AccessDeniedException", Message: "User: arn:aws:iam::123456789012:user/t is not authorized to perform: gamelift:List"}
	glNotFound  = &smithy.GenericAPIError{Code: "NotFoundException", Message: "fleet gone"}
	glThrottled = &smithy.GenericAPIError{Code: "ThrottlingException", Message: "rate exceeded"}
	glCreated   = time.Date(2026, 9, 1, 8, 0, 0, 0, time.UTC)
)

const glFleetPrefix = "arn:aws:gamelift:us-east-1:123456789012:fleet/"

func glClient(handlers map[string]func(in any) (any, error)) *gamelift.Client {
	return gamelift.NewFromConfig(opStubConfig(handlers, glDenied))
}

func glFleet(id string, ct gltypes.ComputeType, status gltypes.FleetStatus) gltypes.FleetAttributes {
	return gltypes.FleetAttributes{FleetId: ptrStr(id), FleetArn: ptrStr(glFleetPrefix + id), ComputeType: ct, Status: status}
}

// glSeedFleets stores the fleet rows the children are linked under.
func glSeedFleets(t *testing.T, st *store.Store, fleets []gltypes.FleetAttributes) {
	t.Helper()
	for _, f := range fleets {
		upsertTestResource(t, st, "aws", testAccountID, TypeGameLiftFleet, sv(f.FleetArn), testRegion, "{}")
	}
}

func glNewError(code string) *smithy.GenericAPIError {
	return &smithy.GenericAPIError{Code: code, Message: code}
}

func TestScanGLComputes_StoresAnywhereFleetComputes(t *testing.T) {
	st := newTestStore(t)
	fleets := []gltypes.FleetAttributes{
		glFleet("fleet-a1", gltypes.ComputeTypeAnywhere, gltypes.FleetStatusActive),
		glFleet("fleet-a2", gltypes.ComputeTypeAnywhere, gltypes.FleetStatusDeleting),
		glFleet("fleet-e", gltypes.ComputeTypeEc2, gltypes.FleetStatusActive),
		glFleet("fleet-t", gltypes.ComputeTypeAnywhere, gltypes.FleetStatusTerminated),
	}
	glSeedFleets(t, st, fleets)
	calls := &parentCalls{}
	handlers := map[string]func(any) (any, error){
		"ListCompute": func(in any) (any, error) {
			req := in.(*gamelift.ListComputeInput)
			fleet := sv(req.FleetId)
			calls.add(fleet)
			arn := func(name string) *string {
				return ptrStr("arn:aws:gamelift:us-east-1:123456789012:compute/" + fleet + "/" + name)
			}
			switch {
			case fleet == "fleet-a1" && req.NextToken == nil:
				return &gamelift.ListComputeOutput{NextToken: ptrStr("p2"), ComputeList: []gltypes.Compute{{
					ComputeArn: arn("c1"), ComputeName: ptrStr("c1"), ComputeStatus: gltypes.ComputeStatusActive,
					CreationTime: &glCreated, IpAddress: ptrStr("10.0.0.1"),
				}}}, nil
			case fleet == "fleet-a1":
				return &gamelift.ListComputeOutput{ComputeList: []gltypes.Compute{
					{ComputeArn: arn("c2"), ComputeName: ptrStr("c2")},
					{ComputeName: ptrStr("no-arn")},
				}}, nil
			}
			return &gamelift.ListComputeOutput{ComputeList: []gltypes.Compute{{ComputeArn: arn("c3"), ComputeName: ptrStr("c3")}}}, nil
		},
	}
	total, _, err := scanGLComputes(context.Background(), glClient(handlers), newTestAccount(testAccountID), testRegion, st, testScanID, fleets)
	if err != nil {
		t.Fatalf("scanGLComputes: %v", err)
	}
	if got, want := calls.distinct(), []string{"fleet-a1", "fleet-a2"}; !slices.Equal(got, want) {
		t.Errorf("queried fleets %v, want %v", got, want)
	}
	if total != 3 {
		t.Errorf("total = %d, want 3", total)
	}
	rows := rowsByNativeID(t, st, TypeGameLiftCompute)
	c1ARN := "arn:aws:gamelift:us-east-1:123456789012:compute/fleet-a1/c1"
	want := []string{c1ARN, "arn:aws:gamelift:us-east-1:123456789012:compute/fleet-a1/c2", "arn:aws:gamelift:us-east-1:123456789012:compute/fleet-a2/c3"}
	if got := slices.Sorted(maps.Keys(rows)); !slices.Equal(got, want) {
		t.Fatalf("compute rows %v, want %v", got, want)
	}
	c1 := rows[c1ARN]
	if sv(c1.Name) != "c1" || sv(c1.Status) != "ACTIVE" || sv(c1.CreatedAt) != "2026-09-01T08:00:00Z" || sv(c1.Region) != testRegion {
		t.Errorf("c1 name/status/created/region = %q/%q/%q/%q", sv(c1.Name), sv(c1.Status), sv(c1.CreatedAt), sv(c1.Region))
	}
	var attrs gltypes.Compute
	if err := json.Unmarshal([]byte(c1.AttributesJSON), &attrs); err != nil || sv(attrs.IpAddress) != "10.0.0.1" {
		t.Errorf("c1 attrs IpAddress = %q (err %v), want 10.0.0.1", sv(attrs.IpAddress), err)
	}
	assertContainsNative(t, st, glFleetPrefix+"fleet-a1", c1ARN)
	assertContainsNative(t, st, glFleetPrefix+"fleet-a2", want[2])
	assertContainsEdgesDeclared(t, st)
}

func TestScanGLScalingPolicies_StoresHomeRegionPolicies(t *testing.T) {
	st := newTestStore(t)
	fleets := []gltypes.FleetAttributes{
		glFleet("fleet-e", gltypes.ComputeTypeEc2, gltypes.FleetStatusActive),
		glFleet("fleet-old", "", gltypes.FleetStatusActive),
		glFleet("fleet-gone", gltypes.ComputeTypeEc2, gltypes.FleetStatusActive),
		glFleet("fleet-a", gltypes.ComputeTypeAnywhere, gltypes.FleetStatusActive),
		glFleet("fleet-t", gltypes.ComputeTypeEc2, gltypes.FleetStatusTerminated),
	}
	glSeedFleets(t, st, fleets)
	calls := &parentCalls{}
	handlers := map[string]func(any) (any, error){
		"DescribeScalingPolicies": func(in any) (any, error) {
			req := in.(*gamelift.DescribeScalingPoliciesInput)
			fleet := sv(req.FleetId)
			calls.add(fleet)
			switch {
			case fleet == "fleet-gone":
				return nil, glNotFound
			case fleet == "fleet-e" && req.NextToken == nil:
				return &gamelift.DescribeScalingPoliciesOutput{NextToken: ptrStr("p2"), ScalingPolicies: []gltypes.ScalingPolicy{
					{Name: ptrStr("up"), Location: ptrStr("us-east-1"), Status: gltypes.ScalingStatusTypeActive},
					{Name: ptrStr("up"), Location: ptrStr("us-west-2"), Status: gltypes.ScalingStatusTypeActive},
				}}, nil
			case fleet == "fleet-e":
				return &gamelift.DescribeScalingPoliciesOutput{ScalingPolicies: []gltypes.ScalingPolicy{
					{Name: ptrStr("down")},
					{Name: ptrStr("old"), Status: gltypes.ScalingStatusTypeDeleted},
					{Status: gltypes.ScalingStatusTypeActive},
				}}, nil
			}
			return &gamelift.DescribeScalingPoliciesOutput{ScalingPolicies: []gltypes.ScalingPolicy{{Name: ptrStr("keep")}}}, nil
		},
	}
	total, _, err := scanGLScalingPolicies(context.Background(), glClient(handlers), newTestAccount(testAccountID), testRegion, st, testScanID, fleets)
	if err != nil {
		t.Fatalf("scanGLScalingPolicies: %v", err)
	}
	if got, want := calls.distinct(), []string{"fleet-e", "fleet-gone", "fleet-old"}; !slices.Equal(got, want) {
		t.Errorf("queried fleets %v, want %v", got, want)
	}
	if total != 3 {
		t.Errorf("total = %d, want 3", total)
	}
	up := glFleetPrefix + "fleet-e/scaling-policy/up"
	want := []string{glFleetPrefix + "fleet-e/scaling-policy/down", up, glFleetPrefix + "fleet-old/scaling-policy/keep"}
	rows := rowsByNativeID(t, st, TypeGameLiftScalingPolicy)
	if got := slices.Sorted(maps.Keys(rows)); !slices.Equal(got, want) {
		t.Fatalf("policy rows %v, want %v", got, want)
	}
	r := rows[up]
	if sv(r.Name) != "up" || sv(r.Status) != "ACTIVE" || sv(r.Region) != testRegion {
		t.Errorf("up name/status/region = %q/%q/%q", sv(r.Name), sv(r.Status), sv(r.Region))
	}
	var attrs gltypes.ScalingPolicy
	if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil || sv(attrs.Location) != "us-east-1" {
		t.Errorf("up attrs Location = %q (err %v), want us-east-1", sv(attrs.Location), err)
	}
	assertContainsNative(t, st, glFleetPrefix+"fleet-e", up)
	assertContainsNative(t, st, glFleetPrefix+"fleet-old", want[2])
	assertContainsEdgesDeclared(t, st)
}

func TestScanGLVpcPeeringConnections_StoresAndLinksListedFleets(t *testing.T) {
	st := newTestStore(t)
	fleets := []gltypes.FleetAttributes{glFleet("fleet-e", gltypes.ComputeTypeEc2, gltypes.FleetStatusActive)}
	glSeedFleets(t, st, fleets)
	handlers := map[string]func(any) (any, error){
		"DescribeVpcPeeringConnections": func(any) (any, error) {
			return &gamelift.DescribeVpcPeeringConnectionsOutput{VpcPeeringConnections: []gltypes.VpcPeeringConnection{
				{
					FleetArn: ptrStr(glFleetPrefix + "fleet-e"), VpcPeeringConnectionId: ptrStr("pcx-1"), PeerVpcId: ptrStr("vpc-1"),
					Status: &gltypes.VpcPeeringConnectionStatus{Code: ptrStr("active")},
				},
				{FleetArn: ptrStr(glFleetPrefix + "fleet-unlisted"), VpcPeeringConnectionId: ptrStr("pcx-2")},
				{FleetArn: ptrStr(glFleetPrefix + "fleet-e")},
				{FleetId: ptrStr("fleet-e"), VpcPeeringConnectionId: ptrStr("pcx-4")},
				{VpcPeeringConnectionId: ptrStr("pcx-3")},
			}}, nil
		},
	}
	// Linking pcx-2 under its unlisted fleet would be gated out with a warning.
	var warnings []store.ScanWarning
	st.OnWarn = func(w store.ScanWarning) { warnings = append(warnings, w) }
	total, _, err := scanGLVpcPeeringConnections(context.Background(), glClient(handlers), newTestAccount(testAccountID), testRegion, st, testScanID, fleets)
	if err != nil {
		t.Fatalf("scanGLVpcPeeringConnections: %v", err)
	}
	if len(warnings) != 0 {
		t.Errorf("warnings = %v, want none", warnings)
	}
	if total != 3 {
		t.Errorf("total = %d, want 3", total)
	}
	pcx1 := glFleetPrefix + "fleet-e/vpc-peering-connection/pcx-1"
	pcx2 := glFleetPrefix + "fleet-unlisted/vpc-peering-connection/pcx-2"
	pcx4 := glFleetPrefix + "fleet-e/vpc-peering-connection/pcx-4"
	rows := rowsByNativeID(t, st, TypeGameLiftVpcPeeringConnection)
	if got, want := slices.Sorted(maps.Keys(rows)), []string{pcx1, pcx4, pcx2}; !slices.Equal(got, want) {
		t.Fatalf("connection rows %v, want %v", got, want)
	}
	r := rows[pcx1]
	if sv(r.Name) != "pcx-1" || sv(r.Status) != "active" || sv(r.Region) != testRegion {
		t.Errorf("pcx-1 name/status/region = %q/%q/%q", sv(r.Name), sv(r.Status), sv(r.Region))
	}
	if rows[pcx2].Status != nil {
		t.Errorf("pcx-2 status = %q, want unset", sv(rows[pcx2].Status))
	}
	var attrs gltypes.VpcPeeringConnection
	if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil || sv(attrs.PeerVpcId) != "vpc-1" {
		t.Errorf("pcx-1 attrs PeerVpcId = %q (err %v), want vpc-1", sv(attrs.PeerVpcId), err)
	}
	assertContainsNative(t, st, glFleetPrefix+"fleet-e", pcx1)
	assertContainsNative(t, st, glFleetPrefix+"fleet-e", pcx4)
	parents, err := st.RelationshipsTo(store.ResourceID("aws", testAccountID, pcx2), store.RelContains)
	if err != nil || len(parents) != 0 {
		t.Errorf("pcx-2 contains parents = %v (err %v), want none: its fleet was not listed", parents, err)
	}
	assertContainsEdgesDeclared(t, st)
}

// glPhaseCase drives one new phase with a single fleet of the kind it queries.
type glPhaseCase struct {
	op  string
	typ string
	run func(context.Context, gameLiftAPI, *store.Store) (int, int, error)
}

var glNewPhases = []glPhaseCase{
	{"ListCompute", TypeGameLiftCompute, func(ctx context.Context, c gameLiftAPI, st *store.Store) (int, int, error) {
		fleets := []gltypes.FleetAttributes{
			glFleet("fleet-a1", gltypes.ComputeTypeAnywhere, gltypes.FleetStatusActive),
			glFleet("fleet-a2", gltypes.ComputeTypeAnywhere, gltypes.FleetStatusActive),
		}
		return scanGLComputes(ctx, c, newTestAccount(testAccountID), testRegion, st, testScanID, fleets)
	}},
	{"DescribeScalingPolicies", TypeGameLiftScalingPolicy, func(ctx context.Context, c gameLiftAPI, st *store.Store) (int, int, error) {
		fleets := []gltypes.FleetAttributes{
			glFleet("fleet-e1", gltypes.ComputeTypeEc2, gltypes.FleetStatusActive),
			glFleet("fleet-e2", gltypes.ComputeTypeEc2, gltypes.FleetStatusActive),
		}
		return scanGLScalingPolicies(ctx, c, newTestAccount(testAccountID), testRegion, st, testScanID, fleets)
	}},
	{"DescribeVpcPeeringConnections", TypeGameLiftVpcPeeringConnection, func(ctx context.Context, c gameLiftAPI, st *store.Store) (int, int, error) {
		return scanGLVpcPeeringConnections(ctx, c, newTestAccount(testAccountID), testRegion, st, testScanID, nil)
	}},
}

// glEmptyOutput is an empty response for a new phase's op.
func glEmptyOutput(op string) any {
	switch op {
	case "ListCompute":
		return &gamelift.ListComputeOutput{}
	case "DescribeScalingPolicies":
		return &gamelift.DescribeScalingPoliciesOutput{}
	default:
		return &gamelift.DescribeVpcPeeringConnectionsOutput{}
	}
}

func TestGameLiftFleetChildPhases_Errors(t *testing.T) {
	badInput := glNewError("InvalidRequestException")
	for _, p := range glNewPhases {
		t.Run(p.op+"/empty", func(t *testing.T) {
			st := newTestStore(t)
			handlers := map[string]func(any) (any, error){p.op: func(any) (any, error) { return glEmptyOutput(p.op), nil }}
			total, _, err := p.run(context.Background(), glClient(handlers), st)
			if err != nil || total != 0 {
				t.Errorf("total, err = %d, %v; want 0, nil", total, err)
			}
		})
		t.Run(p.op+"/access denied warns once", func(t *testing.T) {
			st := newTestStore(t)
			warned := countOpWarnings(st, "gamelift:"+p.op)
			total, _, err := p.run(context.Background(), glClient(nil), st)
			if err != nil || total != 0 {
				t.Errorf("total, err = %d, %v; want 0, nil", total, err)
			}
			if *warned != 1 {
				t.Errorf("warnings = %d, want 1", *warned)
			}
		})
		t.Run(p.op+"/other error propagates", func(t *testing.T) {
			st := newTestStore(t)
			handlers := map[string]func(any) (any, error){p.op: func(any) (any, error) { return nil, badInput }}
			_, _, err := p.run(context.Background(), glClient(handlers), st)
			if !errors.Is(err, badInput) {
				t.Errorf("err = %v, want the InvalidRequestException", err)
			}
		})
	}
}

// glTopLevelHandlers serves one build, an Anywhere and an EC2 fleet, and one
// child of each new phase, with the ops in override answering instead. Every
// other op is denied.
func glTopLevelHandlers(override map[string]func(any) (any, error)) map[string]func(any) (any, error) {
	handlers := map[string]func(any) (any, error){
		"ListBuilds": func(any) (any, error) {
			return &gamelift.ListBuildsOutput{Builds: []gltypes.Build{{BuildArn: ptrStr("arn:aws:gamelift:us-east-1:123456789012:build/b-1"), BuildId: ptrStr("b-1")}}}, nil
		},
		"ListFleets": func(any) (any, error) {
			return &gamelift.ListFleetsOutput{FleetIds: []string{"fleet-a", "fleet-e"}}, nil
		},
		"DescribeFleetAttributes": func(any) (any, error) {
			return &gamelift.DescribeFleetAttributesOutput{FleetAttributes: []gltypes.FleetAttributes{
				glFleet("fleet-a", gltypes.ComputeTypeAnywhere, gltypes.FleetStatusActive),
				glFleet("fleet-e", gltypes.ComputeTypeEc2, gltypes.FleetStatusActive),
			}}, nil
		},
		"ListCompute": func(any) (any, error) {
			return &gamelift.ListComputeOutput{ComputeList: []gltypes.Compute{{ComputeArn: ptrStr("arn:aws:gamelift:us-east-1:123456789012:compute/c1")}}}, nil
		},
		"DescribeScalingPolicies": func(any) (any, error) {
			return &gamelift.DescribeScalingPoliciesOutput{ScalingPolicies: []gltypes.ScalingPolicy{{Name: ptrStr("up")}}}, nil
		},
		"DescribeVpcPeeringConnections": func(any) (any, error) {
			return &gamelift.DescribeVpcPeeringConnectionsOutput{VpcPeeringConnections: []gltypes.VpcPeeringConnection{
				{FleetArn: ptrStr(glFleetPrefix + "fleet-e"), VpcPeeringConnectionId: ptrStr("pcx-1")},
			}}, nil
		},
	}
	maps.Copy(handlers, override)
	return handlers
}

// The fleet children phases run over the fleets the fleet phase listed, and
// an earlier phase's error neither stops them nor is lost.
func TestScanGameLiftIn_RunsFleetChildPhasesOverListedFleets(t *testing.T) {
	st := newTestStore(t)
	aliasErr := glNewError("InvalidRequestException")
	handlers := glTopLevelHandlers(map[string]func(any) (any, error){
		"ListAliases": func(any) (any, error) { return nil, aliasErr },
	})
	total, _, err := scanGameLiftIn(context.Background(), glClient(handlers), newTestAccount(testAccountID), testRegion, st, testScanID)
	if !errors.Is(err, aliasErr) {
		t.Errorf("err = %v, want the ListAliases error", err)
	}
	// 1 build + 2 fleets + 1 compute + 1 scaling policy + 1 peering connection.
	if total != 6 {
		t.Errorf("total = %d, want 6", total)
	}
	assertContainsNative(t, st, glFleetPrefix+"fleet-a", "arn:aws:gamelift:us-east-1:123456789012:compute/c1")
	assertContainsNative(t, st, glFleetPrefix+"fleet-e", glFleetPrefix+"fleet-e/scaling-policy/up")
	assertContainsNative(t, st, glFleetPrefix+"fleet-e", glFleetPrefix+"fleet-e/vpc-peering-connection/pcx-1")
	assertContainsEdgesDeclared(t, st)
}

// Every new phase runs after the existing phases: their rows are stored and
// counted when every new phase fails, and each new phase's error is returned.
func TestScanGameLiftIn_FleetChildPhaseErrorsKeepEarlierRows(t *testing.T) {
	st := newTestStore(t)
	override := map[string]func(any) (any, error){}
	var phaseErrs []error
	for _, p := range glNewPhases {
		perr := glNewError("InvalidRequestException")
		phaseErrs = append(phaseErrs, perr)
		override[p.op] = func(any) (any, error) { return nil, perr }
	}
	total, _, err := scanGameLiftIn(context.Background(), glClient(glTopLevelHandlers(override)), newTestAccount(testAccountID), testRegion, st, testScanID)
	for i, perr := range phaseErrs {
		if !errors.Is(err, perr) {
			t.Errorf("err = %v, want it to carry the %s error", err, glNewPhases[i].op)
		}
	}
	if total != 3 {
		t.Errorf("total = %d, want 3 (1 build + 2 fleets)", total)
	}
	if _, ok := rowsByNativeID(t, st, TypeGameLiftBuild)["arn:aws:gamelift:us-east-1:123456789012:build/b-1"]; !ok {
		t.Error("build row missing")
	}
	if n := len(rowsByNativeID(t, st, TypeGameLiftFleet)); n != 2 {
		t.Errorf("fleet rows = %d, want 2", n)
	}
}

// A later phase's transient error, a throttle or a dropped connection, is
// warned about on its own, so it cannot make the dispatcher classify the whole
// service transient or stop the phases after it.
func TestScanGameLiftIn_TransientPhaseDoesNotMaskOthers(t *testing.T) {
	for name, transient := range map[string]error{"throttle": glThrottled, "dropped connection": io.ErrUnexpectedEOF} {
		t.Run(name, func(t *testing.T) {
			st := newTestStore(t)
			warned := countOpWarnings(st, "gamelift:ListCompute")
			vpcErr := glNewError("InvalidRequestException")
			handlers := glTopLevelHandlers(map[string]func(any) (any, error){
				"ListCompute":                   func(any) (any, error) { return nil, transient },
				"DescribeVpcPeeringConnections": func(any) (any, error) { return nil, vpcErr },
			})
			total, _, err := scanGameLiftIn(context.Background(), glClient(handlers), newTestAccount(testAccountID), testRegion, st, testScanID)
			if got := classifyServiceError(err); got != outcomeError {
				t.Errorf("classifyServiceError(%v) = %v, want outcomeError", err, got)
			}
			if !errors.Is(err, vpcErr) {
				t.Errorf("err = %v, want the DescribeVpcPeeringConnections error", err)
			}
			if *warned != 1 {
				t.Errorf("ListCompute transient warnings = %d, want 1", *warned)
			}
			// 1 build + 2 fleets + 1 scaling policy.
			if total != 4 {
				t.Errorf("total = %d, want 4", total)
			}
		})
	}
}

// In a region with no GameLift endpoint the first phase's NXDOMAIN is returned
// alone, so the dispatcher reports the region unavailable instead of every
// phase warning a transient error.
func TestScanGameLiftIn_UnreachableEndpointStopsTheScan(t *testing.T) {
	st := newTestStore(t)
	var warnings []store.ScanWarning
	st.OnWarn = func(w store.ScanWarning) { warnings = append(warnings, w) }
	calls := &parentCalls{}
	handlers := map[string]func(any) (any, error){}
	for _, op := range []string{
		"ListAliases", "ListBuilds", "ListContainerFleets", "ListContainerGroupDefinitions", "ListFleets",
		"DescribeFleetAttributes", "ListGameServerGroups", "DescribeGameSessionQueues", "ListLocations",
		"DescribeMatchmakingConfigurations", "DescribeMatchmakingRuleSets", "ListScripts", "ListCompute",
		"DescribeScalingPolicies", "DescribeVpcPeeringConnections",
	} {
		handlers[op] = func(any) (any, error) {
			calls.add(op)
			return nil, &net.DNSError{Err: "no such host", Name: "gamelift.us-east-1.amazonaws.com", IsNotFound: true}
		}
	}
	total, _, err := scanGameLiftIn(context.Background(), glClient(handlers), newTestAccount(testAccountID), testRegion, st, testScanID)
	if got := classifyServiceError(err); got != outcomeUnavailable {
		t.Errorf("classifyServiceError(%v) = %v, want outcomeUnavailable", err, got)
	}
	if got, want := calls.distinct(), []string{"ListAliases"}; !slices.Equal(got, want) {
		t.Errorf("ops called %v, want %v", got, want)
	}
	if total != 0 || len(warnings) != 0 {
		t.Errorf("total, warnings = %d, %v; want 0, none", total, warnings)
	}
}

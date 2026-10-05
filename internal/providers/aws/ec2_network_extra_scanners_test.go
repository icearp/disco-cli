package aws

import (
	"context"
	"encoding/json"
	"slices"
	"sync"
	"testing"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"

	"github.com/icearp/disco-cli/store"
)

const (
	netExtraRegion       = "us-east-1"
	netExtraOtherAccount = "210987654321"
)

// netExtraStub serves each op's pages keyed by the request's NextToken ("" is
// the first page); a fixture links pages by setting NextToken on the output.
// A non-nil err for an op is returned instead of any page. The embedded ec2API
// nil-panics on any op the test did not expect.
type netExtraStub struct {
	ec2API

	sgrPages   map[string]*ec2.DescribeSecurityGroupRulesOutput
	sgrErr     error
	connPages  map[string]*ec2.DescribeVpcEndpointConnectionsOutput
	connErr    error
	assocPages map[string]*ec2.DescribeVpcEndpointAssociationsOutput
	assocErr   error

	subnetPages map[string]*ec2.DescribeSubnetsOutput
	subnetErr   error
	// resvPages and resvErr are keyed by subnet id, then (pages) by NextToken.
	resvPages map[string]map[string]*ec2.GetSubnetCidrReservationsOutput
	resvErr   map[string]error

	mu            sync.Mutex
	resvQueried   []string
	assocRequests int
}

func (s *netExtraStub) DescribeSecurityGroupRules(_ context.Context, in *ec2.DescribeSecurityGroupRulesInput, _ ...func(*ec2.Options)) (*ec2.DescribeSecurityGroupRulesOutput, error) {
	if s.sgrErr != nil {
		return nil, s.sgrErr
	}
	return s.sgrPages[sdkaws.ToString(in.NextToken)], nil
}

func (s *netExtraStub) DescribeVpcEndpointConnections(_ context.Context, in *ec2.DescribeVpcEndpointConnectionsInput, _ ...func(*ec2.Options)) (*ec2.DescribeVpcEndpointConnectionsOutput, error) {
	if s.connErr != nil {
		return nil, s.connErr
	}
	return s.connPages[sdkaws.ToString(in.NextToken)], nil
}

func (s *netExtraStub) DescribeVpcEndpointAssociations(_ context.Context, in *ec2.DescribeVpcEndpointAssociationsInput, _ ...func(*ec2.Options)) (*ec2.DescribeVpcEndpointAssociationsOutput, error) {
	s.assocRequests++
	if s.assocErr != nil {
		return nil, s.assocErr
	}
	return s.assocPages[sdkaws.ToString(in.NextToken)], nil
}

func (s *netExtraStub) DescribeSubnets(_ context.Context, in *ec2.DescribeSubnetsInput, _ ...func(*ec2.Options)) (*ec2.DescribeSubnetsOutput, error) {
	if s.subnetErr != nil {
		return nil, s.subnetErr
	}
	return s.subnetPages[sdkaws.ToString(in.NextToken)], nil
}

func (s *netExtraStub) GetSubnetCidrReservations(_ context.Context, in *ec2.GetSubnetCidrReservationsInput, _ ...func(*ec2.Options)) (*ec2.GetSubnetCidrReservationsOutput, error) {
	id := sdkaws.ToString(in.SubnetId)
	s.mu.Lock()
	if in.NextToken == nil {
		s.resvQueried = append(s.resvQueried, id)
	}
	s.mu.Unlock()
	if err := s.resvErr[id]; err != nil {
		return nil, err
	}
	if page := s.resvPages[id][sdkaws.ToString(in.NextToken)]; page != nil {
		return page, nil
	}
	return &ec2.GetSubnetCidrReservationsOutput{}, nil
}

func nameTag(name string) []ec2types.Tag {
	return []ec2types.Tag{{Key: sdkaws.String("Name"), Value: sdkaws.String(name)}}
}

func ownedSubnet(id, owner string) ec2types.Subnet {
	return ec2types.Subnet{SubnetId: sdkaws.String(id), OwnerId: sdkaws.String(owner)}
}

// listNetExtraRows returns the current rows of one type keyed by NativeID.
func listNetExtraRows(t *testing.T, st *store.Store, typ string) map[string]store.Resource {
	t.Helper()
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"},
		AccountID: testAccountID,
		Types:     []string{typ},
		Limit:     1000,
	})
	if err != nil {
		t.Fatalf("ListResources(%s): %v", typ, err)
	}
	out := make(map[string]store.Resource, len(rows))
	for _, r := range rows {
		out[r.NativeID] = r
	}
	return out
}

// netExtraWant describes one expected stored row. idKey/idVal name a field of
// the SDK element that AttributesJSON must carry verbatim; status "" means the
// type sets no status.
type netExtraWant struct {
	nativeID, typ, name, status, idKey, idVal string
}

func assertNetExtraRow(t *testing.T, rows map[string]store.Resource, w netExtraWant) {
	t.Helper()
	r, ok := rows[w.nativeID]
	if !ok {
		t.Errorf("missing row %s; have %d rows", w.nativeID, len(rows))
		return
	}
	if r.Type != w.typ {
		t.Errorf("%s: Type = %q; want %q", w.nativeID, r.Type, w.typ)
	}
	if got := sdkaws.ToString(r.Region); got != netExtraRegion {
		t.Errorf("%s: Region = %q; want %q", w.nativeID, got, netExtraRegion)
	}
	if got := sdkaws.ToString(r.Name); got != w.name {
		t.Errorf("%s: Name = %q; want %q (from the Name tag)", w.nativeID, got, w.name)
	}
	if got := sdkaws.ToString(r.Status); got != w.status {
		t.Errorf("%s: Status = %q; want %q", w.nativeID, got, w.status)
	}
	var attrs map[string]any
	if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
		t.Fatalf("%s: AttributesJSON does not decode: %v", w.nativeID, err)
	}
	if got := attrs[w.idKey]; got != w.idVal {
		t.Errorf("%s: attributes[%q] = %v; want %q (the SDK element verbatim)", w.nativeID, w.idKey, got, w.idVal)
	}
}

type netExtraScanFn func(context.Context, ec2API, *account, string, *store.Store, string) (int, int, error)

// runNetExtra runs one scanner against the stub and returns the stored rows of
// typ and the warnings the scan recorded.
func runNetExtra(t *testing.T, fn netExtraScanFn, stub *netExtraStub, typ string) (total int, rows map[string]store.Resource, warns []store.ScanWarning) {
	t.Helper()
	st := newTestStore(t)
	var mu sync.Mutex
	st.OnWarn = func(w store.ScanWarning) {
		mu.Lock()
		warns = append(warns, w)
		mu.Unlock()
	}
	total, _, err := fn(context.Background(), stub, newTestAccount(testAccountID), netExtraRegion, st, testScanID)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	return total, listNetExtraRows(t, st, typ), warns
}

// scanNetExtraErr runs one scanner expecting it to fail and returns the error.
func scanNetExtraErr(t *testing.T, fn netExtraScanFn, stub *netExtraStub) error {
	t.Helper()
	_, _, err := fn(context.Background(), stub, newTestAccount(testAccountID), netExtraRegion, newTestStore(t), testScanID)
	return err
}

func TestScanSecurityGroupRules(t *testing.T) {
	arn1 := "arn:aws:ec2:us-east-1:123456789012:security-group-rule/sgr-1"
	arn2 := "arn:aws:ec2:us-east-1:123456789012:security-group-rule/sgr-2"
	t.Run("TwoPages", func(t *testing.T) {
		stub := &netExtraStub{sgrPages: map[string]*ec2.DescribeSecurityGroupRulesOutput{
			"": {
				SecurityGroupRules: []ec2types.SecurityGroupRule{{SecurityGroupRuleId: sdkaws.String("sgr-1"), SecurityGroupRuleArn: &arn1, Tags: nameTag("rule-1")}},
				NextToken:          sdkaws.String("p2"),
			},
			"p2": {SecurityGroupRules: []ec2types.SecurityGroupRule{
				{SecurityGroupRuleId: sdkaws.String("sgr-2"), SecurityGroupRuleArn: &arn2, Tags: nameTag("rule-2")},
				{SecurityGroupRuleId: sdkaws.String("sgr-no-arn")},
			}},
		}}
		total, rows, _ := runNetExtra(t, scanSecurityGroupRules, stub, TypeEC2SecurityGroupRule)
		if total != 2 || len(rows) != 2 {
			t.Fatalf("total = %d, rows = %d; want 2, 2 (the ARN-less rule is skipped)", total, len(rows))
		}
		assertNetExtraRow(t, rows, netExtraWant{arn1, TypeEC2SecurityGroupRule, "rule-1", "", "SecurityGroupRuleId", "sgr-1"})
		assertNetExtraRow(t, rows, netExtraWant{arn2, TypeEC2SecurityGroupRule, "rule-2", "", "SecurityGroupRuleId", "sgr-2"})
	})
	t.Run("Empty", func(t *testing.T) {
		stub := &netExtraStub{sgrPages: map[string]*ec2.DescribeSecurityGroupRulesOutput{"": {}}}
		total, rows, warns := runNetExtra(t, scanSecurityGroupRules, stub, TypeEC2SecurityGroupRule)
		if total != 0 || len(rows) != 0 || len(warns) != 0 {
			t.Errorf("total = %d, rows = %d, warns = %d; want all 0", total, len(rows), len(warns))
		}
	})
	t.Run("AccessDenied", func(t *testing.T) {
		stub := &netExtraStub{sgrErr: apiErr("UnauthorizedOperation", "denied")}
		_, rows, warns := runNetExtra(t, scanSecurityGroupRules, stub, TypeEC2SecurityGroupRule)
		if len(rows) != 0 || len(warns) != 1 {
			t.Errorf("rows = %d, warns = %d; want 0, 1", len(rows), len(warns))
		}
	})
}

func TestScanSubnetCidrReservations(t *testing.T) {
	subnets := map[string]*ec2.DescribeSubnetsOutput{
		"": {
			Subnets: []ec2types.Subnet{
				ownedSubnet("subnet-a", testAccountID),
				// RAM-shared in from another account: never queried.
				ownedSubnet("subnet-shared", netExtraOtherAccount),
			},
			NextToken: sdkaws.String("p2"),
		},
		"p2": {Subnets: []ec2types.Subnet{ownedSubnet("subnet-gone", testAccountID), ownedSubnet("subnet-b", testAccountID)}},
	}
	resv := func(id string) ec2types.SubnetCidrReservation {
		return ec2types.SubnetCidrReservation{SubnetCidrReservationId: sdkaws.String(id), Tags: nameTag("name-" + id)}
	}
	t.Run("FanOutAcrossOwnedSubnetsAndPages", func(t *testing.T) {
		stub := &netExtraStub{
			subnetPages: subnets,
			resvPages: map[string]map[string]*ec2.GetSubnetCidrReservationsOutput{
				"subnet-a": {
					"": {
						SubnetIpv4CidrReservations: []ec2types.SubnetCidrReservation{resv("scr-v4")},
						NextToken:                  sdkaws.String("p2"),
					},
					"p2": {SubnetIpv6CidrReservations: []ec2types.SubnetCidrReservation{resv("scr-v6")}},
				},
				"subnet-b":      {"": {SubnetIpv4CidrReservations: []ec2types.SubnetCidrReservation{resv("scr-b")}}},
				"subnet-shared": {"": {SubnetIpv4CidrReservations: []ec2types.SubnetCidrReservation{resv("scr-shared")}}},
			},
			// Deleted between DescribeSubnets and the per-subnet call.
			resvErr: map[string]error{"subnet-gone": apiErr("InvalidSubnetID.NotFound", "gone")},
		}
		total, rows, warns := runNetExtra(t, scanSubnetCidrReservations, stub, TypeEC2SubnetCidrReservation)
		if total != 3 || len(rows) != 3 {
			t.Fatalf("total = %d, rows = %d; want 3, 3", total, len(rows))
		}
		for _, id := range []string{"scr-v4", "scr-v6", "scr-b"} {
			assertNetExtraRow(t, rows, netExtraWant{
				ec2ARN(netExtraRegion, testAccountID, "subnet-cidr-reservation", id),
				TypeEC2SubnetCidrReservation, "name-" + id, "", "SubnetCidrReservationId", id,
			})
		}
		slices.Sort(stub.resvQueried)
		if want := []string{"subnet-a", "subnet-b", "subnet-gone"}; !slices.Equal(stub.resvQueried, want) {
			t.Errorf("subnets queried = %v; want %v (owned subnets only)", stub.resvQueried, want)
		}
		if len(warns) != 0 {
			t.Errorf("warns = %v; want none (a vanished subnet is not a warning)", warns)
		}
	})
	t.Run("NoSubnets", func(t *testing.T) {
		stub := &netExtraStub{subnetPages: map[string]*ec2.DescribeSubnetsOutput{"": {}}}
		total, rows, warns := runNetExtra(t, scanSubnetCidrReservations, stub, TypeEC2SubnetCidrReservation)
		if total != 0 || len(rows) != 0 || len(warns) != 0 || len(stub.resvQueried) != 0 {
			t.Errorf("total = %d, rows = %d, warns = %d, queried = %v; want all empty",
				total, len(rows), len(warns), stub.resvQueried)
		}
	})
	t.Run("ListSubnetsAccessDenied", func(t *testing.T) {
		stub := &netExtraStub{subnetErr: apiErr("UnauthorizedOperation", "denied")}
		_, rows, warns := runNetExtra(t, scanSubnetCidrReservations, stub, TypeEC2SubnetCidrReservation)
		if len(rows) != 0 || len(warns) != 0 || len(stub.resvQueried) != 0 {
			t.Errorf("rows = %d, warns = %v, queried = %v; want all empty "+
				"(the networking phase owns the DescribeSubnets warning)", len(rows), warns, stub.resvQueried)
		}
	})
	t.Run("ListSubnetsOtherError", func(t *testing.T) {
		stub := &netExtraStub{subnetErr: apiErr("InvalidParameterValue", "bad")}
		if err := scanNetExtraErr(t, scanSubnetCidrReservations, stub); !isAPIErrorCode(err, "InvalidParameterValue") {
			t.Errorf("err = %v; want the InvalidParameterValue API error", err)
		}
	})
	t.Run("AccessDeniedWarnsOnce", func(t *testing.T) {
		denied := apiErr("UnauthorizedOperation", "denied")
		stub := &netExtraStub{
			subnetPages: subnets,
			resvErr:     map[string]error{"subnet-a": denied, "subnet-b": denied, "subnet-gone": denied},
		}
		_, rows, warns := runNetExtra(t, scanSubnetCidrReservations, stub, TypeEC2SubnetCidrReservation)
		if len(rows) != 0 || len(warns) != 1 {
			t.Errorf("rows = %d, warns = %d; want 0, 1", len(rows), len(warns))
		}
	})
	t.Run("RegionFeatureGapSilent", func(t *testing.T) {
		gap := apiErr("UnsupportedOperation", "not in this region")
		stub := &netExtraStub{
			subnetPages: subnets,
			resvErr:     map[string]error{"subnet-a": gap, "subnet-b": gap, "subnet-gone": gap},
		}
		_, rows, warns := runNetExtra(t, scanSubnetCidrReservations, stub, TypeEC2SubnetCidrReservation)
		if len(rows) != 0 || len(warns) != 0 {
			t.Errorf("rows = %d, warns = %d; want 0, 0", len(rows), len(warns))
		}
	})
	t.Run("OtherErrorPropagates", func(t *testing.T) {
		stub := &netExtraStub{
			subnetPages: subnets,
			resvErr:     map[string]error{"subnet-b": apiErr("InvalidParameterValue", "bad")},
		}
		if err := scanNetExtraErr(t, scanSubnetCidrReservations, stub); !isAPIErrorCode(err, "InvalidParameterValue") {
			t.Errorf("err = %v; want the InvalidParameterValue API error", err)
		}
	})
}

func TestScanVPCEndpointConnections(t *testing.T) {
	conn := func(id string) ec2types.VpcEndpointConnection {
		return ec2types.VpcEndpointConnection{
			VpcEndpointConnectionId: sdkaws.String(id),
			ServiceId:               sdkaws.String("vpce-svc-1"),
			VpcEndpointId:           sdkaws.String("vpce-" + id),
			VpcEndpointOwner:        sdkaws.String(netExtraOtherAccount),
			VpcEndpointState:        ec2types.StateAvailable,
			Tags:                    nameTag("name-" + id),
		}
	}
	t.Run("TwoPages", func(t *testing.T) {
		stub := &netExtraStub{connPages: map[string]*ec2.DescribeVpcEndpointConnectionsOutput{
			"":   {VpcEndpointConnections: []ec2types.VpcEndpointConnection{conn("con-1")}, NextToken: sdkaws.String("p2")},
			"p2": {VpcEndpointConnections: []ec2types.VpcEndpointConnection{conn("con-2"), {VpcEndpointId: sdkaws.String("vpce-no-conn-id")}}},
		}}
		total, rows, _ := runNetExtra(t, scanVPCEndpointConnections, stub, TypeEC2VPCEndpointConnection)
		if total != 2 || len(rows) != 2 {
			t.Fatalf("total = %d, rows = %d; want 2, 2 (the id-less connection is skipped)", total, len(rows))
		}
		for _, id := range []string{"con-1", "con-2"} {
			assertNetExtraRow(t, rows, netExtraWant{
				ec2ARN(netExtraRegion, testAccountID, "vpc-endpoint-connection", id),
				TypeEC2VPCEndpointConnection, "name-" + id, string(ec2types.StateAvailable), "VpcEndpointConnectionId", id,
			})
		}
	})
	t.Run("Empty", func(t *testing.T) {
		stub := &netExtraStub{connPages: map[string]*ec2.DescribeVpcEndpointConnectionsOutput{"": {}}}
		total, rows, warns := runNetExtra(t, scanVPCEndpointConnections, stub, TypeEC2VPCEndpointConnection)
		if total != 0 || len(rows) != 0 || len(warns) != 0 {
			t.Errorf("total = %d, rows = %d, warns = %d; want all 0", total, len(rows), len(warns))
		}
	})
	t.Run("AccessDenied", func(t *testing.T) {
		stub := &netExtraStub{connErr: apiErr("UnauthorizedOperation", "denied")}
		_, rows, warns := runNetExtra(t, scanVPCEndpointConnections, stub, TypeEC2VPCEndpointConnection)
		if len(rows) != 0 || len(warns) != 1 {
			t.Errorf("rows = %d, warns = %d; want 0, 1", len(rows), len(warns))
		}
	})
}

func TestScanVPCEndpointAssociations(t *testing.T) {
	assoc := func(id string) ec2types.VpcEndpointAssociation {
		return ec2types.VpcEndpointAssociation{
			Id:                              sdkaws.String(id),
			VpcEndpointId:                   sdkaws.String("vpce-1"),
			AssociatedResourceAccessibility: sdkaws.String("Accessible"),
			Tags:                            nameTag("name-" + id),
		}
	}
	t.Run("TwoPages", func(t *testing.T) {
		stub := &netExtraStub{assocPages: map[string]*ec2.DescribeVpcEndpointAssociationsOutput{
			"":   {VpcEndpointAssociations: []ec2types.VpcEndpointAssociation{assoc("vpce-rsc-asc-1")}, NextToken: sdkaws.String("p2")},
			"p2": {VpcEndpointAssociations: []ec2types.VpcEndpointAssociation{assoc("vpce-rsc-asc-2"), {VpcEndpointId: sdkaws.String("vpce-1")}}},
		}}
		total, rows, _ := runNetExtra(t, scanVPCEndpointAssociations, stub, TypeEC2VPCEndpointAssociation)
		if total != 2 || len(rows) != 2 {
			t.Fatalf("total = %d, rows = %d; want 2, 2 (the id-less association is skipped)", total, len(rows))
		}
		for _, id := range []string{"vpce-rsc-asc-1", "vpce-rsc-asc-2"} {
			assertNetExtraRow(t, rows, netExtraWant{
				ec2ARN(netExtraRegion, testAccountID, "vpc-endpoint-association", id),
				TypeEC2VPCEndpointAssociation, "name-" + id, "Accessible", "Id", id,
			})
		}
		if stub.assocRequests != 2 {
			t.Errorf("requests = %d; want 2 (stop after the page without NextToken)", stub.assocRequests)
		}
	})
	t.Run("Empty", func(t *testing.T) {
		stub := &netExtraStub{assocPages: map[string]*ec2.DescribeVpcEndpointAssociationsOutput{"": {}}}
		total, rows, warns := runNetExtra(t, scanVPCEndpointAssociations, stub, TypeEC2VPCEndpointAssociation)
		if total != 0 || len(rows) != 0 || len(warns) != 0 {
			t.Errorf("total = %d, rows = %d, warns = %d; want all 0", total, len(rows), len(warns))
		}
	})
	t.Run("AccessDenied", func(t *testing.T) {
		stub := &netExtraStub{assocErr: apiErr("UnauthorizedOperation", "denied")}
		_, rows, warns := runNetExtra(t, scanVPCEndpointAssociations, stub, TypeEC2VPCEndpointAssociation)
		if len(rows) != 0 || len(warns) != 1 {
			t.Errorf("rows = %d, warns = %d; want 0, 1", len(rows), len(warns))
		}
	})
	t.Run("RegionFeatureGapSilent", func(t *testing.T) {
		stub := &netExtraStub{assocErr: apiErr("InvalidAction", "not in this region")}
		_, rows, warns := runNetExtra(t, scanVPCEndpointAssociations, stub, TypeEC2VPCEndpointAssociation)
		if len(rows) != 0 || len(warns) != 0 {
			t.Errorf("rows = %d, warns = %d; want 0, 0", len(rows), len(warns))
		}
	})
	t.Run("OtherErrorPropagates", func(t *testing.T) {
		stub := &netExtraStub{assocErr: apiErr("InvalidParameterValue", "bad")}
		if err := scanNetExtraErr(t, scanVPCEndpointAssociations, stub); !isAPIErrorCode(err, "InvalidParameterValue") {
			t.Errorf("err = %v; want the InvalidParameterValue API error", err)
		}
	})
}

// TestScanEC2NetworkExtra_AllPhasesStore pins that the category function runs
// every sub-scanner: dropping one from its list stops that type being stored.
func TestScanEC2NetworkExtra_AllPhasesStore(t *testing.T) {
	ruleARN := "arn:aws:ec2:us-east-1:123456789012:security-group-rule/sgr-1"
	stub := &netExtraStub{
		sgrPages: map[string]*ec2.DescribeSecurityGroupRulesOutput{"": {SecurityGroupRules: []ec2types.SecurityGroupRule{
			{SecurityGroupRuleId: sdkaws.String("sgr-1"), SecurityGroupRuleArn: &ruleARN},
		}}},
		subnetPages: map[string]*ec2.DescribeSubnetsOutput{"": {Subnets: []ec2types.Subnet{ownedSubnet("subnet-a", testAccountID)}}},
		resvPages: map[string]map[string]*ec2.GetSubnetCidrReservationsOutput{"subnet-a": {"": {
			SubnetIpv4CidrReservations: []ec2types.SubnetCidrReservation{{SubnetCidrReservationId: sdkaws.String("scr-1")}},
		}}},
		connPages: map[string]*ec2.DescribeVpcEndpointConnectionsOutput{"": {VpcEndpointConnections: []ec2types.VpcEndpointConnection{
			{VpcEndpointConnectionId: sdkaws.String("con-1")},
		}}},
		assocPages: map[string]*ec2.DescribeVpcEndpointAssociationsOutput{"": {VpcEndpointAssociations: []ec2types.VpcEndpointAssociation{
			{Id: sdkaws.String("vpce-rsc-asc-1")},
		}}},
	}
	st := newTestStore(t)
	total, _, err := scanEC2NetworkExtra(context.Background(), stub, newTestAccount(testAccountID), netExtraRegion, st, testScanID)
	if err != nil {
		t.Fatalf("scanEC2NetworkExtra: %v", err)
	}
	if total != 4 {
		t.Errorf("total = %d; want 4", total)
	}
	for _, typ := range []string{TypeEC2SecurityGroupRule, TypeEC2SubnetCidrReservation, TypeEC2VPCEndpointConnection, TypeEC2VPCEndpointAssociation} {
		if n := len(listNetExtraRows(t, st, typ)); n != 1 {
			t.Errorf("%s: %d rows; want 1", typ, n)
		}
	}
}

package aws

import (
	"encoding/json"
	"errors"
	"testing"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	vltypes "github.com/aws/aws-sdk-go-v2/service/vpclattice/types"
	"github.com/icearp/disco-cli/store"
)

func vlAssocAttrs(vpce, vpc string) string {
	return mustJSON(vltypes.ServiceNetworkEndpointAssociation{
		Id: sdkaws.String("snve-1"), ServiceNetworkArn: sdkaws.String(vlTestNetA),
		VpcEndpointId: sdkaws.String(vpce), VpcId: sdkaws.String(vpc),
	})
}

func vlTargetGroupAttrs(tgARN string, typ vltypes.TargetGroupType) string {
	return mustJSON(vltypes.TargetGroupSummary{Arn: sdkaws.String(tgARN), Type: typ})
}

func TestResolveVPCLatticeSNVpcEndpointAssocs_EndpointAndVPC(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	vpceID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2VPCEndpoint, ec2ARN(testRegion, acct.ID, "vpc-endpoint", "vpce-1"), testRegion, "{}")
	vpcID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2VPC, ec2ARN(testRegion, acct.ID, "vpc", "vpc-1"), testRegion, "{}")
	a := upsertTestResource(t, st, "aws", acct.ID, TypeVpcLatticeServiceNetworkVpcEndpointAssociation, vlTestAssocs[0], testRegion, vlAssocAttrs("vpce-1", "vpc-1"))
	// An endpoint and VPC this scan did not store get no edge.
	unscanned := upsertTestResource(t, st, "aws", acct.ID, TypeVpcLatticeServiceNetworkVpcEndpointAssociation, vlTestAssocs[1], testRegion, vlAssocAttrs("vpce-9", "vpc-9"))

	if err := resolveVPCLatticeSNVpcEndpointAssocs(acct, st); err != nil {
		t.Fatalf("resolveVPCLatticeSNVpcEndpointAssocs: %v", err)
	}
	rels, _ := st.RelationshipsFrom(a)
	assertRelationship(t, rels, a, vpceID, store.RelAttachedTo)
	assertRelationship(t, rels, a, vpcID, store.RelAttachedTo)
	if len(rels) != 2 {
		t.Errorf("association edges = %v, want 2", rels)
	}
	if rels, _ := st.RelationshipsFrom(unscanned); len(rels) != 0 {
		t.Errorf("edges to unscanned targets = %v, want none", rels)
	}
}

func TestResolveVPCLatticeSNVpcEndpointAssocs_NoAttrs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	upsertTestResource(t, st, "aws", acct.ID, TypeEC2VPCEndpoint, ec2ARN(testRegion, acct.ID, "vpc-endpoint", "vpce-1"), testRegion, "{}")
	a := upsertTestResource(t, st, "aws", acct.ID, TypeVpcLatticeServiceNetworkVpcEndpointAssociation, vlTestAssocs[0], testRegion, "{}")
	if err := resolveVPCLatticeSNVpcEndpointAssocs(acct, st); err != nil {
		t.Fatalf("resolveVPCLatticeSNVpcEndpointAssocs: %v", err)
	}
	if rels, _ := st.RelationshipsFrom(a); len(rels) != 0 {
		t.Errorf("edges = %v, want none", rels)
	}
}

func TestResolveVPCLatticeSNVpcEndpointAssocs_DecodeErrorKeepsOtherEdges(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	vpceID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2VPCEndpoint, ec2ARN(testRegion, acct.ID, "vpc-endpoint", "vpce-1"), testRegion, "{}")
	upsertTestResource(t, st, "aws", acct.ID, TypeVpcLatticeServiceNetworkVpcEndpointAssociation, vlTestAssocs[0], testRegion, `{"VpcEndpointId":7}`)
	good := upsertTestResource(t, st, "aws", acct.ID, TypeVpcLatticeServiceNetworkVpcEndpointAssociation, vlTestAssocs[1], testRegion, vlAssocAttrs("vpce-1", ""))

	var typeErr *json.UnmarshalTypeError
	if err := resolveVPCLatticeSNVpcEndpointAssocs(acct, st); !errors.As(err, &typeErr) {
		t.Errorf("err = %v, want a JSON type error", err)
	}
	rels, _ := st.RelationshipsFrom(good)
	assertRelationship(t, rels, good, vpceID, store.RelAttachedTo)
}

const (
	vlTestALB   = "arn:aws:elasticloadbalancing:us-east-1:123456789012:loadbalancer/app/web/50dc6c495c0c9188"
	vlTestTGALB = "arn:aws:vpc-lattice:us-east-1:123456789012:targetgroup/tg-alb"
	vlTestTGIP  = "arn:aws:vpc-lattice:us-east-1:123456789012:targetgroup/tg-ip"
)

// vlTargetFixture stores the target groups tg-a (INSTANCE), tg-b (LAMBDA),
// tg-alb (ALB) and tg-ip (IP), and one resource of each type a target can
// name: instance i-1, the function and the load balancer.
func vlTargetFixture(t *testing.T, st *store.Store, acct *account) (instID, fnID, albID string) {
	t.Helper()
	for arn, typ := range map[string]vltypes.TargetGroupType{
		vlTestTGA: vltypes.TargetGroupTypeInstance, vlTestTGB: vltypes.TargetGroupTypeLambda,
		vlTestTGALB: vltypes.TargetGroupTypeAlb, vlTestTGIP: vltypes.TargetGroupTypeIp,
	} {
		upsertTestResource(t, st, "aws", acct.ID, TypeVpcLatticeTargetGroup, arn, testRegion, vlTargetGroupAttrs(arn, typ))
	}
	instID = upsertTestResource(t, st, "aws", acct.ID, TypeEC2Instance, ec2ARN(testRegion, acct.ID, "instance", "i-1"), testRegion, "{}")
	fnID = upsertTestResource(t, st, "aws", acct.ID, TypeLambdaFunction, vlTestLambda, testRegion, "{}")
	albID = upsertTestResource(t, st, "aws", acct.ID, TypeELBv2LoadBalancer, vlTestALB, testRegion, "{}")
	return instID, fnID, albID
}

func vlTarget(t *testing.T, st *store.Store, tgARN, id string, port *int32) string {
	t.Helper()
	return upsertTestResource(t, st, "aws", testAccountID, TypeVpcLatticeTarget, vlTargetNativeID(tgARN, id, port), testRegion,
		mustJSON(vltypes.TargetSummary{Id: sdkaws.String(id), Port: port, Status: vltypes.TargetStatusHealthy}))
}

func TestResolveVPCLatticeTargets_ByTargetGroupType(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	instID, fnID, albID := vlTargetFixture(t, st, acct)
	cases := []struct {
		name, tg, id string
		port         *int32
		want         string // "" means no edge
		from         string
	}{
		{name: "instance", tg: vlTestTGA, id: "i-1", port: sdkaws.Int32(80), want: instID},
		{name: "qualified lambda", tg: vlTestTGB, id: vlTestLambda + ":live", want: fnID},
		{name: "alb", tg: vlTestTGALB, id: vlTestALB, port: sdkaws.Int32(443), want: albID},
		{name: "ip", tg: vlTestTGIP, id: "10.0.0.5", port: sdkaws.Int32(80)},
		{name: "unscanned instance", tg: vlTestTGA, id: "i-9", port: sdkaws.Int32(80)},
		{name: "target group not stored", tg: "arn:aws:vpc-lattice:us-east-1:123456789012:targetgroup/tg-gone", id: "i-1", port: sdkaws.Int32(80)},
	}
	for i := range cases {
		cases[i].from = vlTarget(t, st, cases[i].tg, cases[i].id, cases[i].port)
	}
	if err := resolveVPCLatticeTargets(acct, st); err != nil {
		t.Fatalf("resolveVPCLatticeTargets: %v", err)
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rels, _ := st.RelationshipsFrom(tc.from)
			if tc.want == "" {
				if len(rels) != 0 {
					t.Errorf("edges = %v, want none", rels)
				}
				return
			}
			assertRelationship(t, rels, tc.from, tc.want, store.RelRoutesTo)
			if len(rels) != 1 {
				t.Errorf("edges = %v, want 1", rels)
			}
		})
	}
}

func TestResolveVPCLatticeTargets_NoAttrs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	vlTargetFixture(t, st, acct)
	r := upsertTestResource(t, st, "aws", acct.ID, TypeVpcLatticeTarget, vlTestTargets[0], testRegion, "{}")
	if err := resolveVPCLatticeTargets(acct, st); err != nil {
		t.Fatalf("resolveVPCLatticeTargets: %v", err)
	}
	if rels, _ := st.RelationshipsFrom(r); len(rels) != 0 {
		t.Errorf("edges = %v, want none", rels)
	}
}

func TestResolveVPCLatticeTargets_DecodeErrorKeepsOtherEdges(t *testing.T) {
	for _, tc := range []struct {
		name string
		seed func(t *testing.T, st *store.Store)
	}{
		{"target attrs", func(t *testing.T, st *store.Store) {
			upsertTestResource(t, st, "aws", testAccountID, TypeVpcLatticeTarget, vlTestTGA+"/target/81/i-2", testRegion, `{"Id":7}`)
		}},
		{"target group attrs", func(t *testing.T, st *store.Store) {
			upsertTestResource(t, st, "aws", testAccountID, TypeVpcLatticeTargetGroup, "arn:aws:vpc-lattice:us-east-1:123456789012:targetgroup/tg-bad", testRegion, `{"Type":7}`)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			acct := newTestAccount(testAccountID)
			instID, _, _ := vlTargetFixture(t, st, acct)
			tc.seed(t, st)
			good := vlTarget(t, st, vlTestTGA, "i-1", sdkaws.Int32(80))
			var typeErr *json.UnmarshalTypeError
			if err := resolveVPCLatticeTargets(acct, st); !errors.As(err, &typeErr) {
				t.Errorf("err = %v, want a JSON type error", err)
			}
			rels, _ := st.RelationshipsFrom(good)
			assertRelationship(t, rels, good, instID, store.RelRoutesTo)
		})
	}
}

func TestResolveVPCLatticeTargets_MalformedNativeIDReturnsError(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	instID, _, _ := vlTargetFixture(t, st, acct)
	upsertTestResource(t, st, "aws", acct.ID, TypeVpcLatticeTarget, vlTestTGA+"-no-segment", testRegion, mustJSON(vltypes.TargetSummary{Id: sdkaws.String("i-1")}))
	good := vlTarget(t, st, vlTestTGA, "i-1", sdkaws.Int32(80))
	if err := resolveVPCLatticeTargets(acct, st); err == nil {
		t.Error("err = nil, want an error for the target whose NativeID has no /target/ segment")
	}
	rels, _ := st.RelationshipsFrom(good)
	assertRelationship(t, rels, good, instID, store.RelRoutesTo)
}

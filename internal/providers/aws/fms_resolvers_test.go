package aws

import (
	"reflect"
	"slices"
	"testing"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	fmstypes "github.com/aws/aws-sdk-go-v2/service/fms/types"
	"github.com/icearp/disco-cli/store"
)

const fmsOtherAccount = "210987654321"

func fmsPolicyAttrsJSON(setIDs ...string) string {
	return mustJSON(fmstypes.Policy{PolicyId: sdkaws.String("p"), ResourceSetIds: setIDs})
}

func fmsSetAttrs(members ...fmstypes.Resource) string {
	return mustJSON(fmsResourceSetAttrs{ResourceSet: fmstypes.ResourceSetSummary{Id: sdkaws.String("rs")}, Resources: &members})
}

// edgeSources returns the sorted from-ids of the kind edges into toID.
func edgeSources(t *testing.T, st *store.Store, toID, kind string) []string {
	t.Helper()
	rels, err := st.RelationshipsTo(toID, kind)
	if err != nil {
		t.Fatalf("RelationshipsTo: %v", err)
	}
	var out []string
	for _, r := range rels {
		out = append(out, r.FromID)
	}
	slices.Sort(out)
	return out
}

// A policy's resource-set ARNs are rebuilt with the policy row's region: the
// eu-west-1 policy reaches its own region's rs1, never us-east-1's rs1 or rs2.
func TestResolveFMSPolicyResourceSets(t *testing.T) {
	const region = "eu-west-1"
	st := newTestStore(t)
	rs1 := upsertTestResource(t, st, "aws", testAccountID, TypeFMSResourceSet, fmsResourceSetARN(region, testAccountID, "rs1"), region, fmsSetAttrs())
	otherRS1 := upsertTestResource(t, st, "aws", testAccountID, TypeFMSResourceSet, fmsResourceSetARN("us-east-1", testAccountID, "rs1"), "us-east-1", fmsSetAttrs())
	otherRS2 := upsertTestResource(t, st, "aws", testAccountID, TypeFMSResourceSet, fmsResourceSetARN("us-east-1", testAccountID, "rs2"), "us-east-1", fmsSetAttrs())
	p1 := upsertTestResource(t, st, "aws", testAccountID, TypeFMSPolicy, fmsPolicyARNIn(region, "p1"), region, fmsPolicyAttrsJSON("rs1", "rs2", "rs-unscanned"))
	noAttrs := upsertTestResource(t, st, "aws", testAccountID, TypeFMSPolicy, fmsPolicyARNIn(region, "p2"), region, "{}")
	broken := upsertTestResource(t, st, "aws", testAccountID, TypeFMSPolicy, fmsPolicyARNIn(region, "p3"), region, `{"ResourceSetIds":"rs1"}`)

	err := resolveFMSPolicyResourceSets(newTestAccount(testAccountID), st)
	if err == nil {
		t.Error("err = nil; want p3's decode error")
	}
	if got := edgeSources(t, st, rs1, store.RelUses); !reflect.DeepEqual(got, []string{p1}) {
		t.Errorf("uses edges into %s rs1 from %v; want [%s] only (p2 has no ids, p3 does not decode)", region, got, p1)
	}
	for _, other := range []string{otherRS1, otherRS2} {
		if got := edgeSources(t, st, other, store.RelUses); len(got) != 0 {
			t.Errorf("uses edges into us-east-1 set %s from %v; want none (the policy is in %s)", other, got, region)
		}
	}
	rels, err := st.RelationshipsFrom(p1, store.RelUses)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 1 {
		t.Errorf("p1 uses edges = %d; want 1 (rs2 and rs-unscanned are not stored in %s)", len(rels), region)
	}
	for _, p := range []string{noAttrs, broken} {
		if rels, _ := st.RelationshipsFrom(p); len(rels) != 0 {
			t.Errorf("%s edges = %v; want none", p, rels)
		}
	}
}

func TestResolveFMSPolicyResourceSets_NoPolicies(t *testing.T) {
	st := newTestStore(t)
	upsertTestResource(t, st, "aws", testAccountID, TypeFMSResourceSet, fmsResourceSetARN(testRegion, testAccountID, "rs1"), testRegion, fmsSetAttrs())
	if err := resolveFMSPolicyResourceSets(newTestAccount(testAccountID), st); err != nil {
		t.Fatalf("err = %v", err)
	}
}

func TestResolveFMSResourceSetMembers(t *testing.T) {
	st := newTestStore(t)
	vpcARN := ec2ARN("us-east-1", testAccountID, "vpc", "vpc-1")
	sgARN := ec2ARN("us-east-1", fmsOtherAccount, "security-group", "sg-1")
	subnetARN := ec2ARN("us-east-1", testAccountID, "subnet", "subnet-1")
	stageARN := apigatewayARN("us-east-1", "restapis", "api1", "stages", "prod")
	lambdaARN := "arn:aws:lambda:us-east-1:123456789012:function:f"
	vpc := upsertTestResource(t, st, "aws", testAccountID, TypeEC2VPC, vpcARN, "us-east-1", "{}")
	sg := upsertTestResource(t, st, "aws", fmsOtherAccount, TypeEC2SecurityGroup, sgARN, "us-east-1", "{}")
	subnet := upsertTestResource(t, st, "aws", testAccountID, TypeEC2Subnet, subnetARN, "us-east-1", "{}")
	eip := upsertTestResource(t, st, "aws", testAccountID, TypeEC2EIP, ec2ARN("us-east-1", testAccountID, "elastic-ip", "eipalloc-1"), "us-east-1", "{}")
	stage := upsertTestResource(t, st, "aws", fmsOtherAccount, TypeAPIGatewayStage, stageARN, "us-east-1", "{}")
	upsertTestResource(t, st, "aws", testAccountID, TypeLambdaFunction, lambdaARN, "us-east-1", "{}")

	rs1 := upsertTestResource(t, st, "aws", testAccountID, TypeFMSResourceSet, fmsResourceSetARN(testRegion, testAccountID, "rs1"), testRegion, fmsSetAttrs(
		fmsMember(vpcARN),
		fmstypes.Resource{URI: sdkaws.String(sgARN), AccountId: sdkaws.String(fmsOtherAccount)},
		fmstypes.Resource{URI: sdkaws.String(subnetARN)},
		fmsMember(ec2ARN("us-east-1", testAccountID, "eip-allocation", "eipalloc-1")),
		fmstypes.Resource{URI: sdkaws.String(stageARN), AccountId: sdkaws.String(fmsOtherAccount)},
		fmstypes.Resource{URI: sdkaws.String(apigatewayARN("us-east-1", "restapis", "api2", "stages", "prod"))},
		fmsMember(ec2ARN("us-east-1", testAccountID, "instance", "i-unscanned")),
		fmsMember(lambdaARN),
	))
	empty := upsertTestResource(t, st, "aws", testAccountID, TypeFMSResourceSet, fmsResourceSetARN(testRegion, testAccountID, "rs2"), testRegion, fmsSetAttrs())

	if err := resolveFMSResourceSetMembers(newTestAccount(testAccountID), st); err != nil {
		t.Fatalf("err = %v", err)
	}
	want := []string{vpc, sg, subnet, eip, stage}
	slices.Sort(want)
	if got := edgeSources(t, st, rs1, store.RelAttachedTo); !reflect.DeepEqual(got, want) {
		t.Errorf("attached-to edges into rs1 from %v; want %v (not the unscanned instance, the account-less stage or the lambda)", got, want)
	}
	if got := edgeSources(t, st, empty, store.RelAttachedTo); len(got) != 0 {
		t.Errorf("edges into the memberless set = %v; want none", got)
	}
}

// A set row that does not decode is reported and costs no other set its
// edges; a set row without a Resources key yields none.
func TestResolveFMSResourceSetMembers_BadAndEmptyAttrs(t *testing.T) {
	st := newTestStore(t)
	vpcARN := ec2ARN("us-east-1", testAccountID, "vpc", "vpc-1")
	vpc := upsertTestResource(t, st, "aws", testAccountID, TypeEC2VPC, vpcARN, "us-east-1", "{}")
	upsertTestResource(t, st, "aws", testAccountID, TypeFMSResourceSet, fmsResourceSetARN(testRegion, testAccountID, "bad"), testRegion, `{"Resources":{}}`)
	noAttrs := upsertTestResource(t, st, "aws", testAccountID, TypeFMSResourceSet, fmsResourceSetARN(testRegion, testAccountID, "none"), testRegion, "{}")
	good := upsertTestResource(t, st, "aws", testAccountID, TypeFMSResourceSet, fmsResourceSetARN(testRegion, testAccountID, "good"), testRegion, fmsSetAttrs(fmsMember(vpcARN)))

	if err := resolveFMSResourceSetMembers(newTestAccount(testAccountID), st); err == nil {
		t.Error("err = nil; want the bad set's decode error")
	}
	if got := edgeSources(t, st, good, store.RelAttachedTo); !reflect.DeepEqual(got, []string{vpc}) {
		t.Errorf("edges into the good set from %v; want [%s]", got, vpc)
	}
	if got := edgeSources(t, st, noAttrs, store.RelAttachedTo); len(got) != 0 {
		t.Errorf("edges into the attribute-less set = %v; want none", got)
	}
}

func TestFMSMemberOwner(t *testing.T) {
	cases := []struct{ accountID, arn, want string }{
		{"111111111111", "arn:aws:ec2:us-east-1:222222222222:vpc/vpc-1", "111111111111"},
		{"", "arn:aws:ec2:us-east-1:222222222222:vpc/vpc-1", "222222222222"},
		{"", "arn:aws:apigateway:us-east-1::/restapis/a/stages/s", ""},
		{"", "vpc-1", ""},
	}
	for _, c := range cases {
		if got := fmsMemberOwner(c.accountID, c.arn); got != c.want {
			t.Errorf("fmsMemberOwner(%q, %q) = %q; want %q", c.accountID, c.arn, got, c.want)
		}
	}
}

package aws

import (
	"context"
	"strings"
	"testing"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	apigatewaytypes "github.com/aws/aws-sdk-go-v2/service/apigateway/types"
	cfntypes "github.com/aws/aws-sdk-go-v2/service/cloudformation/types"
	"github.com/aws/aws-sdk-go-v2/service/cloudhsmv2"
	cloudhsmv2types "github.com/aws/aws-sdk-go-v2/service/cloudhsmv2/types"
	dstypes "github.com/aws/aws-sdk-go-v2/service/directoryservice/types"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/aws-sdk-go-v2/service/elasticloadbalancing"
	elbtypes "github.com/aws/aws-sdk-go-v2/service/elasticloadbalancing/types"
	"github.com/aws/aws-sdk-go-v2/service/identitystore"
	istypes "github.com/aws/aws-sdk-go-v2/service/identitystore/types"
	"github.com/aws/aws-sdk-go-v2/service/kms"
	kmstypes "github.com/aws/aws-sdk-go-v2/service/kms/types"
	"github.com/aws/aws-sdk-go-v2/service/macie2"
	macie2types "github.com/aws/aws-sdk-go-v2/service/macie2/types"
	rdstypes "github.com/aws/aws-sdk-go-v2/service/rds/types"
	ssotypes "github.com/aws/aws-sdk-go-v2/service/ssoadmin/types"

	"github.com/icearp/disco-cli/store"
)

// TestPartitionARNBuilders_CommercialUnchanged pins that every partition-taking
// core, given "aws", produces exactly the string the builder produced before
// partitions existed — those strings are NativeIDs, and a changed byte gives
// every existing commercial row a new ResourceID. The aws-cn column, in cn-north-1,
// proves the partition argument reaches the output instead of being ignored.
func TestPartitionARNBuilders_CommercialUnchanged(t *testing.T) {
	const acct = "123456789012"
	cases := []struct {
		name       string
		build      func(partition, region string) string
		commercial string // the pre-partition literal form
		china      string
	}{
		{
			"ec2", func(p, region string) string { return ec2ARNIn(p, region, acct, "instance", "i-1") },
			"arn:aws:ec2:us-east-1:123456789012:instance/i-1",
			"arn:aws-cn:ec2:cn-north-1:123456789012:instance/i-1",
		},
		{
			"rds", func(p, region string) string { return rdsARNIn(p, region, acct, "cluster", "c1") },
			"arn:aws:rds:us-east-1:123456789012:cluster:c1",
			"arn:aws-cn:rds:cn-north-1:123456789012:cluster:c1",
		},
		{
			"apigateway", func(p, region string) string { return apigatewayARNIn(p, region, "restapis", "a1") },
			"arn:aws:apigateway:us-east-1::/restapis/a1",
			"arn:aws-cn:apigateway:cn-north-1::/restapis/a1",
		},
		{
			"apigateway empty path", func(p, region string) string { return apigatewayARNIn(p, region) },
			"arn:aws:apigateway:us-east-1::/",
			"arn:aws-cn:apigateway:cn-north-1::/",
		},
		{
			"cloudhsm", func(p, region string) string { return cloudhsmClusterARNIn(p, region, acct, "cluster-1") },
			"arn:aws:cloudhsm:us-east-1:123456789012:cluster/cluster-1",
			"arn:aws-cn:cloudhsm:cn-north-1:123456789012:cluster/cluster-1",
		},
		{
			"log group", func(p, region string) string { return logGroupARNIn(p, acct, region, "/aws/lambda/f") },
			"arn:aws:logs:us-east-1:123456789012:log-group:/aws/lambda/f",
			"arn:aws-cn:logs:cn-north-1:123456789012:log-group:/aws/lambda/f",
		},
		{
			"macie session", func(p, region string) string { return macieSessionNativeIDIn(p, acct, region) },
			"arn:aws:macie2:us-east-1:123456789012:session",
			"arn:aws-cn:macie2:cn-north-1:123456789012:session",
		},
		{
			"identity store user", func(p, region string) string { return identityStoreUserNativeIDIn(p, acct, "d-1", "u-1") },
			"arn:aws:identitystore::123456789012:user/d-1/u-1",
			"arn:aws-cn:identitystore::123456789012:user/d-1/u-1",
		},
		{
			"identity store group", func(p, region string) string { return identityStoreGroupNativeIDIn(p, acct, "d-1", "g-1") },
			"arn:aws:identitystore::123456789012:group/d-1/g-1",
			"arn:aws-cn:identitystore::123456789012:group/d-1/g-1",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.build("aws", "us-east-1"); got != c.commercial {
				t.Errorf("core(aws) = %q; want %q", got, c.commercial)
			}
			if got := c.build("aws-cn", "cn-north-1"); got != c.china {
				t.Errorf("core(aws-cn) = %q; want %q", got, c.china)
			}
		})
	}
}

func TestArnFor(t *testing.T) {
	if got, want := arnFor("aws-us-gov", "s3", "", "", "bucket"), "arn:aws-us-gov:s3:::bucket"; got != want {
		t.Errorf("arnFor(aws-us-gov, s3, \"\", \"\", bucket) = %q; want %q", got, want)
	}
}

func TestPartitionOf(t *testing.T) {
	cases := []struct {
		in     string
		want   string
		wantOK bool
	}{
		{"arn:aws:iam::123456789012:role/R", "aws", true},
		{"arn:aws-cn:iam::123456789012:role/R", "aws-cn", true},
		{"arn:aws-us-gov:sts::123456789012:assumed-role/R/s", "aws-us-gov", true},
		{"arn:aws-eusc:iam::123456789012:role/R", "aws-eusc", true},
		{"arn:aws-iso-b:iam::123456789012:role/R", "aws-iso-b", true},
		// Parses as an ARN, but no partition the SDK knows: must not read as commercial.
		{"arn:aws-mars:iam::123456789012:role/R", "", false},
		{"not-an-arn", "", false},
		{"", "", false},
	}
	for _, c := range cases {
		got, ok := partitionOf(c.in)
		if got != c.want || ok != c.wantOK {
			t.Errorf("partitionOf(%q) = (%q, %v); want (%q, %v)", c.in, got, ok, c.want, c.wantOK)
		}
	}
}

func TestDNSSuffix(t *testing.T) {
	cases := []struct {
		in     string
		want   string
		wantOK bool
	}{
		{"aws", "amazonaws.com", true},
		{"aws-cn", "amazonaws.com.cn", true},
		{"aws-us-gov", "amazonaws.com", true},
		{"aws-eusc", "amazonaws.eu", true},
		{"aws-iso", "c2s.ic.gov", true},
		{"aws-mars", "", false},
		{"", "", false},
	}
	for _, c := range cases {
		got, ok := dnsSuffix(c.in)
		if got != c.want || ok != c.wantOK {
			t.Errorf("dnsSuffix(%q) = (%q, %v); want (%q, %v)", c.in, got, ok, c.want, c.wantOK)
		}
	}
}

// TestEC2ARN verifies EC2's NativeID ARN format. NativeID feeds
// store.ResourceID, so a format change silently breaks all relationships
// referencing EC2 resources.
func TestEC2ARNIn_Commercial(t *testing.T) {
	cases := []struct {
		region, account, rtype, id, want string
	}{
		{
			"us-east-1", "123456789012", "instance", "i-abc123",
			"arn:aws:ec2:us-east-1:123456789012:instance/i-abc123",
		},
		{
			"eu-west-1", "999999999999", "vpc", "vpc-xyz",
			"arn:aws:ec2:eu-west-1:999999999999:vpc/vpc-xyz",
		},
		{
			"us-west-2", "111111111111", "security-group", "sg-001",
			"arn:aws:ec2:us-west-2:111111111111:security-group/sg-001",
		},
	}
	for _, tc := range cases {
		got := ec2ARNIn("aws", tc.region, tc.account, tc.rtype, tc.id)
		if got != tc.want {
			t.Errorf("ec2ARNIn(aws,%q,%q,%q,%q) = %q, want %q",
				tc.region, tc.account, tc.rtype, tc.id, got, tc.want)
		}
	}
}

func TestRDSARNIn_Commercial(t *testing.T) {
	got := rdsARNIn("aws", "us-east-1", "123456789012", "db", "mydb")
	want := "arn:aws:rds:us-east-1:123456789012:db:mydb"
	if got != want {
		t.Errorf("rdsARNIn(aws, …) = %q, want %q", got, want)
	}
}

func TestAPIGatewayARNIn_Commercial(t *testing.T) {
	tests := []struct {
		name string
		in   []string
		want string
	}{
		{"single segment", []string{"restapis"}, "arn:aws:apigateway:us-east-1::/restapis"},
		{"multi segment", []string{"restapis", "abc", "stages", "prod"}, "arn:aws:apigateway:us-east-1::/restapis/abc/stages/prod"},
		{"empty path", nil, "arn:aws:apigateway:us-east-1::/"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := apigatewayARNIn("aws", "us-east-1", tt.in...); got != tt.want {
				t.Errorf("apigatewayARNIn(aws, us-east-1, %v) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestLogGroupARNIn_Commercial(t *testing.T) {
	got := logGroupARNIn("aws", "123456789012", "us-east-1", "/aws/lambda/foo")
	want := "arn:aws:logs:us-east-1:123456789012:log-group:/aws/lambda/foo"
	if got != want {
		t.Errorf("logGroupARNIn(aws, …) = %q, want %q", got, want)
	}
}

func TestMacieSessionNativeIDIn_Commercial(t *testing.T) {
	got := macieSessionNativeIDIn("aws", "123456789012", "us-east-1")
	want := "arn:aws:macie2:us-east-1:123456789012:session"
	if got != want {
		t.Errorf("macieSessionNativeIDIn(aws, …) = %q, want %q", got, want)
	}
}

func TestSSOAssignmentNativeID(t *testing.T) {
	psArn := "arn:aws:sso:::permissionSet/ssoins-1/ps-2"
	got := ssoAssignmentNativeID(psArn, "111122223333", "USER", "u-1")
	want := psArn + "/account/111122223333/USER/u-1"
	if got != want {
		t.Errorf("ssoAssignmentNativeID = %q, want %q", got, want)
	}
}

func TestIdentityStoreNativeIDIn_Commercial(t *testing.T) {
	if got, want := identityStoreUserNativeIDIn("aws", "123456789012", "d-9067", "u-1"),
		"arn:aws:identitystore::123456789012:user/d-9067/u-1"; got != want {
		t.Errorf("identityStoreUserNativeIDIn(aws, …) = %q, want %q", got, want)
	}
	if got, want := identityStoreGroupNativeIDIn("aws", "123456789012", "d-9067", "g-1"),
		"arn:aws:identitystore::123456789012:group/d-9067/g-1"; got != want {
		t.Errorf("identityStoreGroupNativeIDIn(aws, …) = %q, want %q", got, want)
	}
}

// partitionFixtures are the non-commercial partitions each builder family is
// proven against through a real scanner or resolver, each with its own region.
var partitionFixtures = []struct{ partition, region string }{
	{"aws-cn", "cn-north-1"},
	{"aws-us-gov", "us-gov-west-1"},
}

// partitionARN builds arn:{partition}:{service}:{region}:{testAccountID}:{resource}
// for test expectations, independently of arnFor.
func partitionARN(partition, service, region, resource string) string {
	return "arn:" + partition + ":" + service + ":" + region + ":" + testAccountID + ":" + resource
}

// assertEdgeTargets fails unless the edges out of from go to exactly want, so
// an edge to the commercial form of a target fails as loudly as a missing one.
func assertEdgeTargets(t *testing.T, st *store.Store, from string, want ...string) {
	t.Helper()
	rels, err := st.RelationshipsFrom(from)
	if err != nil {
		t.Fatalf("RelationshipsFrom(%s): %v", from, err)
	}
	got := map[string]bool{}
	for _, r := range rels {
		got[r.ToID] = true
	}
	wantSet := map[string]bool{}
	for _, w := range want {
		wantSet[w] = true
		if !got[w] {
			t.Errorf("no edge %s → %s", from, w)
		}
	}
	for to := range got {
		if !wantSet[to] {
			t.Errorf("unexpected edge %s → %s", from, to)
		}
	}
}

// errUnstubbedOpDenied answers the ops a partition test does not stub. It is an
// access denial so the scanner skips that phase with a warning, as it would
// for a role lacking the action, instead of failing the scan.
var errUnstubbedOpDenied = apiErr("AccessDeniedException", "operation not stubbed by this test")

// partitionAccount is an account as loadAccounts leaves it for partition p.
func partitionAccount(partition string) *account {
	acct := newTestAccount(testAccountID)
	acct.Partition = partition
	return acct
}

// onlyRow returns the single row of typ, failing unless there is exactly one.
func onlyRow(t *testing.T, st *store.Store, typ string) store.Resource {
	t.Helper()
	rows := rowsByNativeID(t, st, typ)
	if len(rows) != 1 {
		t.Fatalf("%s rows = %v; want exactly one", typ, rows)
	}
	for _, r := range rows {
		return r
	}
	return store.Resource{}
}

func TestEC2ARNIn_ScannerUsesAccountPartition(t *testing.T) {
	for _, pf := range partitionFixtures {
		t.Run(pf.partition, func(t *testing.T) {
			st := newTestStore(t)
			client := ec2.NewFromConfig(cfgWithStub(stubResponses(t, map[string][]stubCall{
				"DescribeVpcs": {{Output: &ec2.DescribeVpcsOutput{Vpcs: []ec2types.Vpc{{VpcId: ptrStr("vpc-1")}}}}},
			}), pf.region))
			if _, _, err := scanVPCs(context.Background(), client, partitionAccount(pf.partition), pf.region, st, testScanID); err != nil {
				t.Fatalf("scanVPCs: %v", err)
			}
			if got, want := onlyRow(t, st, TypeEC2VPC).NativeID, partitionARN(pf.partition, "ec2", pf.region, "vpc/vpc-1"); got != want {
				t.Errorf("vpc NativeID = %s; want %s", got, want)
			}
		})
	}
}

func TestEC2ARNIn_ResolverUsesAccountPartition(t *testing.T) {
	for _, pf := range partitionFixtures {
		t.Run(pf.partition, func(t *testing.T) {
			st := newTestStore(t)
			acct := partitionAccount(pf.partition)
			ngw := upsertTestResource(t, st, "aws", acct.ID, TypeEC2NatGateway,
				ec2ARNIn(acct.partition(), pf.region, acct.ID, "natgateway", "nat-1"), pf.region,
				mustJSON(ec2types.NatGateway{SubnetId: ptrStr("subnet-1"), VpcId: ptrStr("vpc-1")}))
			if err := resolveNatGatewayRelationships(acct, st); err != nil {
				t.Fatalf("resolveNatGatewayRelationships: %v", err)
			}
			assertEdgeTargets(t, st, ngw,
				store.ResourceID("aws", acct.ID, partitionARN(pf.partition, "ec2", pf.region, "subnet/subnet-1")),
				store.ResourceID("aws", acct.ID, partitionARN(pf.partition, "ec2", pf.region, "vpc/vpc-1")))
		})
	}
}

func TestEC2ARNIn_RouteTargetUsesAccountPartition(t *testing.T) {
	for _, pf := range partitionFixtures {
		t.Run(pf.partition, func(t *testing.T) {
			st := newTestStore(t)
			acct := partitionAccount(pf.partition)
			igw := upsertTestResource(t, st, "aws", acct.ID, TypeEC2InternetGateway,
				ec2ARNIn(acct.partition(), pf.region, acct.ID, "internet-gateway", "igw-1"), pf.region, "{}")
			rt := upsertTestResource(t, st, "aws", acct.ID, TypeEC2RouteTable,
				ec2ARNIn(acct.partition(), pf.region, acct.ID, "route-table", "rtb-1"), pf.region,
				mustJSON(ec2types.RouteTable{Routes: []ec2types.Route{{GatewayId: ptrStr("igw-1")}}}))
			if err := resolveRouteTableRoutes(acct, st); err != nil {
				t.Fatalf("resolveRouteTableRoutes: %v", err)
			}
			assertEdgeTargets(t, st, rt, igw)
		})
	}
}

// Every cfnTypeMap binding that takes a partition must reach the row the
// target's own scanner stores. The stack's NativeID is the StackId
// CloudFormation returns; each target NativeID is built the way its scanner
// builds it (the SDK ARN for RDS and Logs).
func TestCloudFormationStackResources_PartitionBindings(t *testing.T) {
	type target struct{ typ, nativeID string }
	cases := []struct {
		cfnType, physID string
		target          func(p, region string) target
	}{
		{"AWS::EC2::Instance", "i-1", func(p, r string) target {
			return target{TypeEC2Instance, ec2ARNIn(p, r, testAccountID, "instance", "i-1")}
		}},
		{"AWS::EC2::SecurityGroup", "sg-1", func(p, r string) target {
			return target{TypeEC2SecurityGroup, ec2ARNIn(p, r, testAccountID, "security-group", "sg-1")}
		}},
		{"AWS::EC2::VPC", "vpc-1", func(p, r string) target {
			return target{TypeEC2VPC, ec2ARNIn(p, r, testAccountID, "vpc", "vpc-1")}
		}},
		{"AWS::EC2::Subnet", "subnet-1", func(p, r string) target {
			return target{TypeEC2Subnet, ec2ARNIn(p, r, testAccountID, "subnet", "subnet-1")}
		}},
		{"AWS::Logs::LogGroup", "/app/lg", func(p, r string) target {
			return target{TypeLogsLogGroup, logGroupARN(sdkaws.String(partitionARN(p, "logs", r, "log-group:/app/lg:*")))}
		}},
		{"AWS::RDS::DBInstance", "d1", func(p, r string) target {
			return target{TypeRDSDBInstance, partitionARN(p, "rds", r, "db:d1")}
		}},
		{"AWS::RDS::DBCluster", "c1", func(p, r string) target {
			return target{TypeRDSDBCluster, partitionARN(p, "rds", r, "cluster:c1")}
		}},
		{"AWS::ApiGateway::RestApi", "a1", func(p, r string) target {
			return target{TypeAPIGatewayRestAPI, apigatewayARNIn(p, r, "restapis", "a1")}
		}},
		{"AWS::ApiGatewayV2::Api", "h1", func(p, r string) target {
			return target{TypeAPIGatewayV2API, apigatewayARNIn(p, r, "apis", "h1")}
		}},
	}
	for _, c := range cases {
		for _, pf := range partitionFixtures {
			t.Run(c.cfnType+"/"+pf.partition, func(t *testing.T) {
				st := newTestStore(t)
				acct := partitionAccount(pf.partition)
				tgt := c.target(acct.partition(), pf.region)
				if !strings.HasPrefix(tgt.nativeID, "arn:"+pf.partition+":") {
					t.Fatalf("target NativeID %s is not in %s", tgt.nativeID, pf.partition)
				}
				tgtID := upsertTestResource(t, st, "aws", acct.ID, tgt.typ, tgt.nativeID, pf.region, "{}")
				stack := upsertTestResource(t, st, "aws", acct.ID, TypeCloudFormationStack,
					partitionARN(pf.partition, "cloudformation", pf.region, "stack/s1/uuid-1"), pf.region,
					mustJSON(map[string]any{"Resources": []cfntypes.StackResourceSummary{{
						LogicalResourceId: ptrStr("R"), PhysicalResourceId: ptrStr(c.physID),
						ResourceType: ptrStr(c.cfnType), ResourceStatus: cfntypes.ResourceStatusCreateComplete,
					}}}))
				if err := resolveCloudFormationStackResources(acct, st); err != nil {
					t.Fatalf("resolveCloudFormationStackResources: %v", err)
				}
				assertEdgeTargets(t, st, stack, tgtID)
			})
		}
	}
}

// The instance's NativeID is the DBInstanceArn RDS returns.
func TestRDSARNIn_ResolverUsesAccountPartition(t *testing.T) {
	for _, pf := range partitionFixtures {
		t.Run(pf.partition, func(t *testing.T) {
			st := newTestStore(t)
			acct := partitionAccount(pf.partition)
			db := upsertTestResource(t, st, "aws", acct.ID, TypeRDSDBInstance,
				partitionARN(pf.partition, "rds", pf.region, "db:d1"), pf.region,
				mustJSON(rdstypes.DBInstance{DBClusterIdentifier: ptrStr("c1")}))
			if err := resolveRDSInstanceRelationships(acct, st); err != nil {
				t.Fatalf("resolveRDSInstanceRelationships: %v", err)
			}
			assertEdgeTargets(t, st, db, store.ResourceID("aws", acct.ID, partitionARN(pf.partition, "rds", pf.region, "cluster:c1")))
		})
	}
}

func TestAPIGatewayARNIn_ResolverUsesAccountPartition(t *testing.T) {
	for _, pf := range partitionFixtures {
		t.Run(pf.partition, func(t *testing.T) {
			st := newTestStore(t)
			acct := partitionAccount(pf.partition)
			upsertTestResource(t, st, "aws", acct.ID, TypeAPIGatewayRestAPI,
				apigatewayARNIn(acct.partition(), pf.region, "restapis", "a1"), pf.region, "{}")
			stage := upsertTestResource(t, st, "aws", acct.ID, TypeAPIGatewayStage,
				apigatewayARNIn(acct.partition(), pf.region, "restapis", "a1", "stages", "prod"), pf.region,
				mustJSON(apigatewaytypes.Stage{ClientCertificateId: ptrStr("cc1")}))
			if err := resolveAPIGatewayStageRelationships(acct, st); err != nil {
				t.Fatalf("resolveAPIGatewayStageRelationships: %v", err)
			}
			want := "arn:" + pf.partition + ":apigateway:" + pf.region + "::/clientcertificates/cc1"
			assertEdgeTargets(t, st, stage, store.ResourceID("aws", acct.ID, want))
		})
	}
}

// The subscription's NativeID comes from dsDirectoryARN, the builder the
// Directory Service scanner uses.
func TestLogGroupARNIn_DSLogSubscriptionUsesAccountPartition(t *testing.T) {
	for _, pf := range partitionFixtures {
		t.Run(pf.partition, func(t *testing.T) {
			st := newTestStore(t)
			acct := partitionAccount(pf.partition)
			group := upsertTestResource(t, st, "aws", acct.ID, TypeLogsLogGroup,
				partitionARN(pf.partition, "logs", pf.region, "log-group:/aws/ds/d-1"), pf.region, "{}")
			sub := upsertTestResource(t, st, "aws", acct.ID, TypeDSLogSubscription,
				dsDirectoryARN(pf.partition, pf.region, acct.ID, "d-1")+"/log-subscription//aws/ds/d-1", pf.region,
				mustJSON(dstypes.LogSubscription{DirectoryId: ptrStr("d-1"), LogGroupName: ptrStr("/aws/ds/d-1")}))
			if err := resolveDSLogSubscriptions(acct, st); err != nil {
				t.Fatalf("resolveDSLogSubscriptions: %v", err)
			}
			assertEdgeTargets(t, st, sub, group)
		})
	}
}

func TestMacieSessionNativeIDIn_ScannerUsesAccountPartition(t *testing.T) {
	for _, pf := range partitionFixtures {
		t.Run(pf.partition, func(t *testing.T) {
			st := newTestStore(t)
			client := macie2.NewFromConfig(cfgWithStub(stubResponses(t, map[string][]stubCall{
				"GetMacieSession": {{Output: &macie2.GetMacieSessionOutput{Status: macie2types.MacieStatusEnabled}}},
			}), pf.region))
			if _, _, _, err := scanMacieSession(context.Background(), client, partitionAccount(pf.partition), pf.region, st, testScanID); err != nil {
				t.Fatalf("scanMacieSession: %v", err)
			}
			if got, want := onlyRow(t, st, TypeMacieSession).NativeID, partitionARN(pf.partition, "macie2", pf.region, "session"); got != want {
				t.Errorf("session NativeID = %s; want %s", got, want)
			}
		})
	}
}

// Users, groups and memberships come from the real scanner; the membership
// resolver must reach the user and group rows it stored.
func TestIdentityStoreNativeIDIn_MembershipEdgesUseAccountPartition(t *testing.T) {
	for _, pf := range partitionFixtures {
		t.Run(pf.partition, func(t *testing.T) {
			st := newTestStore(t)
			acct := partitionAccount(pf.partition)
			client := identitystore.NewFromConfig(opStubConfig(map[string]func(any) (any, error){
				"ListUsers": func(any) (any, error) {
					return &identitystore.ListUsersOutput{Users: []istypes.User{{UserId: ptrStr("u-1"), IdentityStoreId: ptrStr("d-1")}}}, nil
				},
				"ListGroups": func(any) (any, error) {
					return &identitystore.ListGroupsOutput{Groups: []istypes.Group{{GroupId: ptrStr("g-1"), IdentityStoreId: ptrStr("d-1")}}}, nil
				},
				"ListGroupMemberships": func(any) (any, error) {
					return &identitystore.ListGroupMembershipsOutput{GroupMemberships: []istypes.GroupMembership{{
						MembershipId: ptrStr("m-1"), GroupId: ptrStr("g-1"), IdentityStoreId: ptrStr("d-1"),
						MemberId: &istypes.MemberIdMemberUserId{Value: "u-1"},
					}}}, nil
				},
			}, errUnstubbedOpDenied))
			instances := []ssotypes.InstanceMetadata{{
				InstanceArn:     sdkaws.String("arn:" + pf.partition + ":sso:::instance/ssoins-1"),
				IdentityStoreId: ptrStr("d-1"), OwnerAccountId: ptrStr(testAccountID),
			}}
			if _, _, err := scanIdentityStoreUsersGroups(context.Background(), client, acct, pf.region, instances, st, testScanID); err != nil {
				t.Fatalf("scanIdentityStoreUsersGroups: %v", err)
			}
			user := onlyRow(t, st, TypeIdentityStoreUser)
			group := onlyRow(t, st, TypeIdentityStoreGroup)
			for got, want := range map[string]string{
				user.NativeID:  "arn:" + pf.partition + ":identitystore::" + testAccountID + ":user/d-1/u-1",
				group.NativeID: "arn:" + pf.partition + ":identitystore::" + testAccountID + ":group/d-1/g-1",
			} {
				if got != want {
					t.Errorf("NativeID = %s; want %s", got, want)
				}
			}
			if err := resolveIdentityStoreGroupMembershipRefs(acct, st); err != nil {
				t.Fatalf("resolveIdentityStoreGroupMembershipRefs: %v", err)
			}
			assertEdgeTargets(t, st, onlyRow(t, st, TypeIdentityStoreGroupMembership).ID, user.ID, group.ID)
		})
	}
}

// Both rows come from their real scanners, and the key store's synthetic
// NativeID need not share the cluster's partition for the edge to form.
func TestCloudHSMClusterARNIn_KMSKeyStoreReachesScannedCluster(t *testing.T) {
	for _, pf := range partitionFixtures {
		t.Run(pf.partition, func(t *testing.T) {
			st := newTestStore(t)
			acct := partitionAccount(pf.partition)
			hsm := cloudhsmv2.NewFromConfig(opStubConfig(map[string]func(any) (any, error){
				"DescribeClusters": func(any) (any, error) {
					return &cloudhsmv2.DescribeClustersOutput{Clusters: []cloudhsmv2types.Cluster{{ClusterId: ptrStr("cluster-1")}}}, nil
				},
			}, errUnstubbedOpDenied))
			if _, _, err := scanCloudHSMClusters(context.Background(), hsm, acct, pf.region, st, testScanID); err != nil {
				t.Fatalf("scanCloudHSMClusters: %v", err)
			}
			cluster := onlyRow(t, st, TypeCloudHSMCluster)
			if want := partitionARN(pf.partition, "cloudhsm", pf.region, "cluster/cluster-1"); cluster.NativeID != want {
				t.Errorf("cluster NativeID = %s; want %s", cluster.NativeID, want)
			}
			kmsClient := newKMSTestClient(map[string]func(any) (any, error){
				"DescribeCustomKeyStores": func(any) (any, error) {
					return &kms.DescribeCustomKeyStoresOutput{CustomKeyStores: []kmstypes.CustomKeyStoresListEntry{{
						CustomKeyStoreId: ptrStr("cks-1"), CloudHsmClusterId: ptrStr("cluster-1"),
					}}}, nil
				},
			})
			if _, _, err := scanKMSCustomKeyStores(context.Background(), kmsClient, acct, pf.region, st, testScanID); err != nil {
				t.Fatalf("scanKMSCustomKeyStores: %v", err)
			}
			if err := resolveKMSCustomKeyStoreBackends(acct, st); err != nil {
				t.Fatalf("resolveKMSCustomKeyStoreBackends: %v", err)
			}
			assertEdgeTargets(t, st, onlyRow(t, st, TypeKMSCustomKeyStore).ID, cluster.ID)
		})
	}
}

// Both rows come from their real scanners; the classic load balancer's
// synthetic NativeID need not share the VPC's partition for the edge to form.
func TestEC2ARNIn_ClassicELBReachesScannedVPC(t *testing.T) {
	for _, pf := range partitionFixtures {
		t.Run(pf.partition, func(t *testing.T) {
			st := newTestStore(t)
			acct := partitionAccount(pf.partition)
			ec2Client := ec2.NewFromConfig(cfgWithStub(stubResponses(t, map[string][]stubCall{
				"DescribeVpcs": {{Output: &ec2.DescribeVpcsOutput{Vpcs: []ec2types.Vpc{{VpcId: ptrStr("vpc-1")}}}}},
			}), pf.region))
			if _, _, err := scanVPCs(context.Background(), ec2Client, acct, pf.region, st, testScanID); err != nil {
				t.Fatalf("scanVPCs: %v", err)
			}
			elbClient := elasticloadbalancing.NewFromConfig(cfgWithStub(stubResponses(t, map[string][]stubCall{
				"DescribeLoadBalancers": {{Output: &elasticloadbalancing.DescribeLoadBalancersOutput{
					LoadBalancerDescriptions: []elbtypes.LoadBalancerDescription{{LoadBalancerName: ptrStr("lb-1"), VPCId: ptrStr("vpc-1")}},
				}}},
			}), pf.region))
			if _, _, err := scanELBClassicLoadBalancers(context.Background(), elbClient, acct, pf.region, st, testScanID); err != nil {
				t.Fatalf("scanELBClassicLoadBalancers: %v", err)
			}
			if err := resolveELBClassicRelationships(acct, st); err != nil {
				t.Fatalf("resolveELBClassicRelationships: %v", err)
			}
			assertEdgeTargets(t, st, onlyRow(t, st, TypeELBClassicLoadBalancer).ID, onlyRow(t, st, TypeEC2VPC).ID)
		})
	}
}

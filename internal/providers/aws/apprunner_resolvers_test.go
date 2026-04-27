package aws

import (
	"fmt"
	"testing"

	"codeberg.org/icearp/disco/internal/store"
)

const (
	testARServiceName    = "web-svc"
	testARServiceID      = "00000000000000000000000000000000"
	testARConnectorName  = "private-connector"
	testARConnectorID    = "11111111111111111111111111111111"
	testARSubnet         = "subnet-ar1111"
	testARSG             = "sg-ar2222"
	testARInstanceRole   = "AppRunnerInstanceRole"
	testARAccessRole     = "AppRunnerECRAccessRole"
	testARKMSKeyID       = "ddddeeee-ffff-0000-1111-222233334444"
	testARRepo           = "myapp"
)

func arServiceARN() string {
	return fmt.Sprintf("arn:aws:apprunner:%s:%s:service/%s/%s", testRegion, testAccountID, testARServiceName, testARServiceID)
}

func arConnectorARN() string {
	return fmt.Sprintf("arn:aws:apprunner:%s:%s:vpcconnector/%s/1/%s", testRegion, testAccountID, testARConnectorName, testARConnectorID)
}

func arECRRepoARN() string {
	return fmt.Sprintf("arn:aws:ecr:%s:%s:repository/%s", testRegion, testAccountID, testARRepo)
}

// TestResolveAppRunnerServiceTargets exercises every service edge.
func TestResolveAppRunnerServiceTargets(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	connID := upsertTestResource(t, st, "aws", acct.ID, TypeAppRunnerVPCConnector, arConnectorARN(), testRegion, "{}")

	instRoleARN := fmt.Sprintf("arn:aws:iam::%s:role/%s", testAccountID, testARInstanceRole)
	accessRoleARN := fmt.Sprintf("arn:aws:iam::%s:role/%s", testAccountID, testARAccessRole)
	instRoleID := upsertTestResource(t, st, "aws", acct.ID, TypeIAMRole, instRoleARN, "", "{}")
	accessRoleID := upsertTestResource(t, st, "aws", acct.ID, TypeIAMRole, accessRoleARN, "", "{}")

	repoID := upsertTestResource(t, st, "aws", acct.ID, TypeECRRepository, arECRRepoARN(), testRegion, "{}")

	keyARN := fmt.Sprintf("arn:aws:kms:%s:%s:key/%s", testRegion, testAccountID, testARKMSKeyID)
	kID := upsertTestResource(t, st, "aws", acct.ID, TypeKMSKey, keyARN, testRegion,
		fmt.Sprintf(`{"KeyId":%q,"Arn":%q}`, testARKMSKeyID, keyARN))

	imageID := fmt.Sprintf("%s.dkr.ecr.%s.amazonaws.com/%s:latest", testAccountID, testRegion, testARRepo)
	svcAttrs := fmt.Sprintf(`{
		"ServiceArn":%q,"ServiceName":%q,"Status":"RUNNING",
		"InstanceConfiguration":{"InstanceRoleArn":%q},
		"NetworkConfiguration":{"EgressConfiguration":{"EgressType":"VPC","VpcConnectorArn":%q}},
		"SourceConfiguration":{
			"AuthenticationConfiguration":{"AccessRoleArn":%q},
			"ImageRepository":{"ImageIdentifier":%q,"ImageRepositoryType":"ECR"}
		},
		"EncryptionConfiguration":{"KmsKey":%q}
	}`, arServiceARN(), testARServiceName, instRoleARN, arConnectorARN(), accessRoleARN, imageID, keyARN)
	svcID := upsertTestResource(t, st, "aws", acct.ID, TypeAppRunnerService, arServiceARN(), testRegion, svcAttrs)

	if err := resolveAppRunnerServiceTargets(acct, st); err != nil {
		t.Fatalf("resolveAppRunnerServiceTargets: %v", err)
	}
	rels, err := st.RelationshipsFrom(svcID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	assertRelationship(t, rels, svcID, connID, store.RelUses)
	assertRelationship(t, rels, svcID, instRoleID, store.RelAssumes)
	assertRelationship(t, rels, svcID, accessRoleID, store.RelAssumes)
	assertRelationship(t, rels, svcID, repoID, store.RelUses)
	assertRelationship(t, rels, svcID, kID, store.RelUses)
}

// TestResolveAppRunnerVPCConnectorTargets verifies vpc-connector →
// subnet + SG edges.
func TestResolveAppRunnerVPCConnectorTargets(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	snARN := ec2ARN(testRegion, testAccountID, "subnet", testARSubnet)
	snID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2Subnet, snARN, testRegion, "{}")

	sgARN := ec2ARN(testRegion, testAccountID, "security-group", testARSG)
	sgID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2SecurityGroup, sgARN, testRegion, "{}")

	connAttrs := fmt.Sprintf(`{"VpcConnectorArn":%q,"VpcConnectorName":%q,"Status":"ACTIVE","Subnets":[%q],"SecurityGroups":[%q]}`,
		arConnectorARN(), testARConnectorName, testARSubnet, testARSG)
	connID := upsertTestResource(t, st, "aws", acct.ID, TypeAppRunnerVPCConnector, arConnectorARN(), testRegion, connAttrs)

	if err := resolveAppRunnerVPCConnectorTargets(acct, st); err != nil {
		t.Fatalf("resolveAppRunnerVPCConnectorTargets: %v", err)
	}
	rels, err := st.RelationshipsFrom(connID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	assertRelationship(t, rels, connID, snID, store.RelUses)
	assertRelationship(t, rels, connID, sgID, store.RelUses)
}

func TestApprunnerImageToRepoARN(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"123456789012.dkr.ecr.us-east-1.amazonaws.com/myapp:latest", "arn:aws:ecr:us-east-1:123456789012:repository/myapp"},
		{"123456789012.dkr.ecr.us-east-1.amazonaws.com/team/myapp:v1", "arn:aws:ecr:us-east-1:123456789012:repository/team/myapp"},
		{"public.ecr.aws/lambda/python:latest", ""},
		{"docker.io/library/nginx:latest", ""},
		{"", ""},
	}
	for _, c := range cases {
		if got := apprunnerImageToRepoARN(c.in); got != c.want {
			t.Errorf("apprunnerImageToRepoARN(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

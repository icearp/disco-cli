package aws

import (
	"testing"

	"codeburg.org/icearp/disco/internal/store"
)

func TestResolveInstanceRelationships(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount("123456789012")
	region := "us-east-1"

	instanceARN := ec2ARN(region, acct.ID, "instance", "i-abc123")
	attrsJSON := `{
		"InstanceId": "i-abc123",
		"VpcId":      "vpc-111",
		"SubnetId":   "subnet-222",
		"SecurityGroups": [{"GroupId": "sg-333"}],
		"BlockDeviceMappings": [{"Ebs": {"VolumeId": "vol-444"}}]
	}`
	instID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2Instance, instanceARN, region, attrsJSON)

	vpcID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2VPC,
		ec2ARN(region, acct.ID, "vpc", "vpc-111"), region, "{}")
	subnetID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2Subnet,
		ec2ARN(region, acct.ID, "subnet", "subnet-222"), region, "{}")
	sgID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2SecurityGroup,
		ec2ARN(region, acct.ID, "security-group", "sg-333"), region, "{}")
	volID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2Volume,
		ec2ARN(region, acct.ID, "volume", "vol-444"), region, "{}")

	if err := resolveInstanceRelationships(acct, st); err != nil {
		t.Fatalf("resolveInstanceRelationships: %v", err)
	}

	rels, err := st.RelationshipsFrom(instID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 4 {
		t.Errorf("expected 4 relationships, got %d", len(rels))
	}
	assertRelationship(t, rels, instID, vpcID, store.RelAttachedTo)
	assertRelationship(t, rels, instID, subnetID, store.RelAttachedTo)
	assertRelationship(t, rels, instID, sgID, store.RelUses)
	assertRelationship(t, rels, instID, volID, store.RelAttachedTo)
}

func TestResolveInstanceRelationships_EmptyAttrs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount("123456789012")
	region := "us-east-1"

	instanceARN := ec2ARN(region, acct.ID, "instance", "i-bare")
	instID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2Instance, instanceARN, region, "{}")

	if err := resolveInstanceRelationships(acct, st); err != nil {
		t.Fatalf("resolveInstanceRelationships: %v", err)
	}

	rels, err := st.RelationshipsFrom(instID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 0 {
		t.Errorf("expected 0 relationships for instance with empty attrs, got %d", len(rels))
	}
}

func TestResolveInstanceConnectEndpointRelationships(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	iceARN := "arn:aws:ec2:" + testRegion + ":" + testAccountID + ":instance-connect-endpoint/eice-001"
	iceID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2InstanceConnectEndpoint, iceARN, testRegion,
		`{"SubnetId":"subnet-001","VpcId":"vpc-001"}`)
	subnetID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2Subnet, ec2ARN(testRegion, acct.ID, "subnet", "subnet-001"), testRegion, "{}")
	vpcID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2VPC, ec2ARN(testRegion, acct.ID, "vpc", "vpc-001"), testRegion, "{}")

	if err := resolveInstanceConnectEndpointRelationships(acct, st); err != nil {
		t.Fatalf("resolveInstanceConnectEndpointRelationships: %v", err)
	}
	rels, err := st.RelationshipsFrom(iceID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 2 {
		t.Fatalf("expected 2 relationships, got %d", len(rels))
	}
	assertRelationship(t, rels, iceID, subnetID, store.RelAttachedTo)
	assertRelationship(t, rels, iceID, vpcID, store.RelAttachedTo)
}

func TestResolveInstanceConnectEndpointRelationships_EmptyAttrs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	iceID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2InstanceConnectEndpoint,
		"arn:aws:ec2:"+testRegion+":"+testAccountID+":instance-connect-endpoint/eice-bare", testRegion, "{}")
	if err := resolveInstanceConnectEndpointRelationships(acct, st); err != nil {
		t.Fatalf("resolveInstanceConnectEndpointRelationships: %v", err)
	}
	rels, _ := st.RelationshipsFrom(iceID)
	if len(rels) != 0 {
		t.Errorf("expected 0 relationships, got %d", len(rels))
	}
}

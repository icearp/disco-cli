package aws

import (
	"testing"

	"codeburg.org/icearp/disco/internal/store"
)

// TestResolveInstanceRelationships verifies that an EC2 instance's JSON attributes
// are correctly parsed to produce VPC, subnet, security-group, and volume relationships.
// This test catches wrong JSON field names in instanceAttrs — bugs that are otherwise
// silent (zero relationships, no error).
func TestResolveInstanceRelationships(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount("123456789012")
	region := "us-east-1"

	// Insert the instance with a full attribute blob and region set.
	instanceARN := ec2ARN(region, acct.ID, "instance", "i-abc123")
	attrsJSON := `{
		"InstanceId": "i-abc123",
		"VpcId":      "vpc-111",
		"SubnetId":   "subnet-222",
		"SecurityGroups": [{"GroupId": "sg-333"}],
		"BlockDeviceMappings": [{"Ebs": {"VolumeId": "vol-444"}}]
	}`
	instID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2Instance, instanceARN, region, attrsJSON)

	// Insert the referenced resources — their native IDs must match what the resolver computes
	// using ec2ARN(region, ...). Region must be set on the instance for the resolver to build
	// the correct ARN, so we insert these with the same region.
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

// TestResolveInstanceRelationships_EmptyAttrs verifies that an instance with
// no network attributes produces no relationships and no error.
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

// TestResolveSubnetVPCRelationships verifies that subnets produce an attached-to
// relationship pointing to their parent VPC.
func TestResolveSubnetVPCRelationships(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount("123456789012")
	region := "us-east-1"

	subnetARN := ec2ARN(region, acct.ID, "subnet", "subnet-abc")
	attrsJSON := `{"VpcId": "vpc-xyz"}`
	subnetID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2Subnet, subnetARN, region, attrsJSON)
	vpcID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2VPC,
		ec2ARN(region, acct.ID, "vpc", "vpc-xyz"), region, "{}")

	if err := resolveSubnetVPCRelationships(acct, st); err != nil {
		t.Fatalf("resolveSubnetVPCRelationships: %v", err)
	}

	rels, err := st.RelationshipsFrom(subnetID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 1 {
		t.Fatalf("expected 1 relationship, got %d", len(rels))
	}
	if rels[0].ToID != vpcID || rels[0].Kind != store.RelAttachedTo {
		t.Errorf("expected subnet -[attached-to]-> vpc, got %+v", rels[0])
	}
}

// TestResolveIGWRelationships verifies that an internet gateway produces an
// attached-to relationship for each VPC in its Attachments list.
func TestResolveIGWRelationships(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount("123456789012")
	region := "us-east-1"

	igwARN := ec2ARN(region, acct.ID, "internet-gateway", "igw-001")
	attrsJSON := `{"Attachments": [{"VpcId": "vpc-abc"}]}`
	igwID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2InternetGateway, igwARN, region, attrsJSON)
	vpcID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2VPC,
		ec2ARN(region, acct.ID, "vpc", "vpc-abc"), region, "{}")

	if err := resolveIGWRelationships(acct, st); err != nil {
		t.Fatalf("resolveIGWRelationships: %v", err)
	}

	rels, err := st.RelationshipsFrom(igwID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 1 {
		t.Fatalf("expected 1 relationship, got %d", len(rels))
	}
	if rels[0].ToID != vpcID || rels[0].Kind != store.RelAttachedTo {
		t.Errorf("expected igw -[attached-to]-> vpc, got %+v", rels[0])
	}
}

// assertRelationship fails the test if no relationship with the given
// (from, to, kind) exists in the rels slice.
func assertRelationship(t *testing.T, rels []store.Relationship, fromID, toID, kind string) {
	t.Helper()
	for _, r := range rels {
		if r.FromID == fromID && r.ToID == toID && r.Kind == kind {
			return
		}
	}
	t.Errorf("missing relationship: %s -[%s]-> %s", fromID, kind, toID)
}

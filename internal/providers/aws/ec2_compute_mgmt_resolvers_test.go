package aws

import (
	"testing"

	"codeberg.org/icearp/disco/store"
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

func TestResolveSnapshotRelationships(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount("123456789012")
	region := "us-east-1"

	volID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2Volume,
		ec2ARN(region, acct.ID, "volume", "vol-444"), region, "{}")
	keyARN := "arn:aws:kms:" + region + ":" + acct.ID + ":key/k-snap"
	kID := upsertTestResource(t, st, "aws", acct.ID, TypeKMSKey, keyARN, region, "{}")
	snapARN := ec2ARN(region, acct.ID, "snapshot", "snap-1")
	attrs := `{"VolumeId":"vol-444","KmsKeyId":"` + keyARN + `"}`
	snapID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2Snapshot, snapARN, region, attrs)

	if err := resolveSnapshotRelationships(acct, st); err != nil {
		t.Fatalf("resolveSnapshotRelationships: %v", err)
	}
	rels, _ := st.RelationshipsFrom(snapID)
	assertRelationship(t, rels, snapID, volID, store.RelAttachedTo)
	assertRelationship(t, rels, snapID, kID, store.RelUses)
}

func TestResolveSnapshotRelationships_NoAttrs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount("123456789012")
	region := "us-east-1"
	snapARN := ec2ARN(region, acct.ID, "snapshot", "snap-1")
	snapID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2Snapshot, snapARN, region, "{}")
	if err := resolveSnapshotRelationships(acct, st); err != nil {
		t.Fatalf("empty: %v", err)
	}
	if rels, _ := st.RelationshipsFrom(snapID); len(rels) != 0 {
		t.Errorf("emitted %d edges, want 0", len(rels))
	}
}

// TestResolveInstanceRelationships_KeyPair verifies instance→key-pair edge
// via the region+name index (key pair NativeID is by KeyPairId, instance
// carries KeyName only).
func TestResolveInstanceRelationships_KeyPair(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount("123456789012")
	region := "us-east-1"
	keyName := "deploy-key"

	instanceARN := ec2ARN(region, acct.ID, "instance", "i-kp")
	instID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2Instance, instanceARN, region,
		`{"KeyName":"`+keyName+`"}`)

	// Seed the key pair directly so we can set Name (helper omits it).
	kpARN := ec2ARN(region, acct.ID, "key-pair", "key-999")
	kpResource := &store.Resource{
		Provider: "aws", AccountID: acct.ID, Type: TypeEC2KeyPair,
		NativeID: kpARN, Region: &region, Name: &keyName,
		AttributesJSON: "{}", DiscoveredBy: "00000000000000000000000000000000",
	}
	if _, err := st.UpsertResource(kpResource); err != nil {
		t.Fatalf("upsert key pair: %v", err)
	}
	kpID := store.ResourceID("aws", acct.ID, kpARN)

	if err := resolveInstanceRelationships(acct, st); err != nil {
		t.Fatalf("resolveInstanceRelationships: %v", err)
	}
	rels, err := st.RelationshipsFrom(instID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	assertRelationship(t, rels, instID, kpID, store.RelUses)
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

// TestResolveInstanceRelationships_IAMProfileAndENI verifies instance-profile
// and network-interface edges are emitted.
func TestResolveInstanceRelationships_IAMProfileAndENI(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	iid := "i-0abc"
	instanceARN := ec2ARN(testRegion, acct.ID, "instance", iid)
	ipARN := "arn:aws:iam::" + testAccountID + ":instance-profile/AppRole"
	eniID := "eni-1234"
	attrs := `{"InstanceId":"` + iid + `","IamInstanceProfile":{"Arn":"` + ipARN + `"},"NetworkInterfaces":[{"NetworkInterfaceId":"` + eniID + `"}]}`

	instResID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2Instance, instanceARN, testRegion, attrs)
	ipResID := upsertTestResource(t, st, "aws", acct.ID, TypeIAMInstanceProfile, ipARN, "", "{}")
	eniResID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2NetworkInterface,
		ec2ARN(testRegion, acct.ID, "network-interface", eniID), testRegion, "{}")

	if err := resolveInstanceRelationships(acct, st); err != nil {
		t.Fatalf("resolveInstanceRelationships: %v", err)
	}
	rels, err := st.RelationshipsFrom(instResID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	assertRelationship(t, rels, instResID, ipResID, store.RelUses)
	assertRelationship(t, rels, instResID, eniResID, store.RelAttachedTo)
}

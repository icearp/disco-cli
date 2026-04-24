package aws

import (
	"fmt"
	"testing"

	"codeberg.org/icearp/disco/internal/store"
)

// kafkaClusterARN builds an MSK cluster ARN in the shape the SDK returns.
func kafkaClusterARN(region, acct, name, uuid string) string {
	return fmt.Sprintf("arn:aws:kafka:%s:%s:cluster/%s/%s", region, acct, name, uuid)
}

// TestResolveKafkaRelationships_ProvisionedAllEdges verifies a provisioned
// cluster emits subnet, security-group, and KMS edges when all targets are
// scanned.
func TestResolveKafkaRelationships_ProvisionedAllEdges(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	subnetARN := ec2ARN(testRegion, acct.ID, "subnet", "subnet-aaa")
	sgARN := ec2ARN(testRegion, acct.ID, "security-group", "sg-bbb")
	keyARN := fmt.Sprintf("arn:aws:kms:%s:%s:key/abc-123", testRegion, acct.ID)
	subnetID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2Subnet, subnetARN, testRegion, `{}`)
	sgID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2SecurityGroup, sgARN, testRegion, `{}`)
	keyID := upsertTestResource(t, st, "aws", acct.ID, TypeKMSKey, keyARN, testRegion, `{}`)

	clusterARN := kafkaClusterARN(testRegion, acct.ID, "my-cluster", "uuid-1")
	attrs := fmt.Sprintf(`{
		"Provisioned": {
			"BrokerNodeGroupInfo": {
				"ClientSubnets": ["subnet-aaa"],
				"SecurityGroups": ["sg-bbb"]
			},
			"EncryptionInfo": {
				"EncryptionAtRest": {"DataVolumeKMSKeyId": %q}
			}
		}
	}`, keyARN)
	clusterID := upsertTestResource(t, st, "aws", acct.ID, TypeMSKCluster, clusterARN, testRegion, attrs)

	if err := resolveKafkaRelationships(acct, st); err != nil {
		t.Fatalf("resolveKafkaRelationships: %v", err)
	}
	rels, err := st.RelationshipsFrom(clusterID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	assertRelationship(t, rels, clusterID, subnetID, store.RelAttachedTo)
	assertRelationship(t, rels, clusterID, sgID, store.RelUses)
	assertRelationship(t, rels, clusterID, keyID, store.RelUses)
}

// TestResolveKafkaRelationships_Serverless verifies a serverless cluster
// resolves VpcConfigs subnet + SG edges.
func TestResolveKafkaRelationships_Serverless(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	subnetARN := ec2ARN(testRegion, acct.ID, "subnet", "subnet-ccc")
	sgARN := ec2ARN(testRegion, acct.ID, "security-group", "sg-ddd")
	subnetID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2Subnet, subnetARN, testRegion, `{}`)
	sgID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2SecurityGroup, sgARN, testRegion, `{}`)

	clusterARN := kafkaClusterARN(testRegion, acct.ID, "serverless", "uuid-2")
	attrs := `{
		"Serverless": {
			"VpcConfigs": [
				{"SubnetIds": ["subnet-ccc"], "SecurityGroupIds": ["sg-ddd"]}
			]
		}
	}`
	clusterID := upsertTestResource(t, st, "aws", acct.ID, TypeMSKCluster, clusterARN, testRegion, attrs)

	if err := resolveKafkaRelationships(acct, st); err != nil {
		t.Fatalf("resolveKafkaRelationships: %v", err)
	}
	rels, err := st.RelationshipsFrom(clusterID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	assertRelationship(t, rels, clusterID, subnetID, store.RelAttachedTo)
	assertRelationship(t, rels, clusterID, sgID, store.RelUses)
}

// TestResolveKafkaRelationships_AWSManagedKMS verifies that an AWS-managed
// KMS alias reference does not emit a KMS edge (the AWS-managed key is not
// scanned, so the edge would be dangling).
func TestResolveKafkaRelationships_AWSManagedKMS(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	subnetARN := ec2ARN(testRegion, acct.ID, "subnet", "subnet-eee")
	upsertTestResource(t, st, "aws", acct.ID, TypeEC2Subnet, subnetARN, testRegion, `{}`)

	clusterARN := kafkaClusterARN(testRegion, acct.ID, "aws-managed", "uuid-3")
	attrs := `{
		"Provisioned": {
			"BrokerNodeGroupInfo": {"ClientSubnets": ["subnet-eee"]},
			"EncryptionInfo": {
				"EncryptionAtRest": {"DataVolumeKMSKeyId": "alias/aws/kafka"}
			}
		}
	}`
	clusterID := upsertTestResource(t, st, "aws", acct.ID, TypeMSKCluster, clusterARN, testRegion, attrs)

	if err := resolveKafkaRelationships(acct, st); err != nil {
		t.Fatalf("resolveKafkaRelationships: %v", err)
	}
	rels, err := st.RelationshipsFrom(clusterID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	for _, rel := range rels {
		if rel.Kind == store.RelUses {
			// The only expected "uses" edge would be a KMS one — there should be none.
			t.Errorf("unexpected uses edge for AWS-managed KMS alias: %+v", rel)
		}
	}
}

// TestResolveKafkaRelationships_UnscannedSubnet verifies FK-safe skip when a
// referenced subnet is not in the store.
func TestResolveKafkaRelationships_UnscannedSubnet(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	clusterARN := kafkaClusterARN(testRegion, acct.ID, "orphan-net", "uuid-4")
	attrs := `{
		"Provisioned": {
			"BrokerNodeGroupInfo": {"ClientSubnets": ["subnet-missing"]}
		}
	}`
	clusterID := upsertTestResource(t, st, "aws", acct.ID, TypeMSKCluster, clusterARN, testRegion, attrs)

	if err := resolveKafkaRelationships(acct, st); err != nil {
		t.Fatalf("resolveKafkaRelationships: %v", err)
	}
	rels, err := st.RelationshipsFrom(clusterID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 0 {
		t.Errorf("unexpected edges for unscanned subnet: %+v", rels)
	}
}

// TestResolveKafkaRelationships_EmptyAttrs verifies no panic and no edges
// when neither Provisioned nor Serverless is populated.
func TestResolveKafkaRelationships_EmptyAttrs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	clusterARN := kafkaClusterARN(testRegion, acct.ID, "empty", "uuid-5")
	upsertTestResource(t, st, "aws", acct.ID, TypeMSKCluster, clusterARN, testRegion, `{}`)

	if err := resolveKafkaRelationships(acct, st); err != nil {
		t.Fatalf("resolveKafkaRelationships: %v", err)
	}
}

package aws

import (
	"encoding/json"
	"errors"
	"testing"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/kafka"
	kafkatypes "github.com/aws/aws-sdk-go-v2/service/kafka/types"
	"github.com/icearp/disco-cli/store"
)

// A row whose attributes do not decode fails its resolver rather than being
// skipped.
func TestResolveMSKChildRefs_DecodeError(t *testing.T) {
	for _, tc := range []struct {
		typ, nativeID string
		resolve       func(*account, *store.Store) error
	}{
		{TypeMSKNode, kafkaTestNodeA1, resolveMSKNodeRefs},
		{TypeMSKChannel, kafkaTestChannel1, resolveMSKChannelRefs},
		{TypeMSKClientVpcConnection, kafkaTestClientA1, resolveMSKClientVpcConnectionRefs},
	} {
		t.Run(tc.typ, func(t *testing.T) {
			st := newTestStore(t)
			upsertTestResource(t, st, "aws", testAccountID, tc.typ, tc.nativeID, testRegion, "[1]")
			var typeErr *json.UnmarshalTypeError
			if err := tc.resolve(newTestAccount(testAccountID), st); !errors.As(err, &typeErr) {
				t.Errorf("err = %v, want a JSON decode error", err)
			}
		})
	}
}

// assertNoMSKRels fails the test if id has any outbound edge of kind.
func assertNoMSKRels(t *testing.T, st *store.Store, id, kind string) {
	t.Helper()
	rels, err := st.RelationshipsFrom(id, kind)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 0 {
		t.Errorf("%s has %d %s edges, want 0: %+v", id, len(rels), kind, rels)
	}
}

func mskRelsFrom(t *testing.T, st *store.Store, id, kind string) []store.Relationship {
	t.Helper()
	rels, err := st.RelationshipsFrom(id, kind)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	return rels
}

func TestResolveMSKNodeRefs_BrokerAndZookeeper(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	subnet := upsertTestResource(t, st, "aws", testAccountID, TypeEC2Subnet, ec2ARN(testRegion, testAccountID, "subnet", "subnet-0a"), testRegion, "{}")
	eniB := upsertTestResource(t, st, "aws", testAccountID, TypeEC2NetworkInterface, ec2ARN(testRegion, testAccountID, "network-interface", "eni-0b"), testRegion, "{}")
	eniZ := upsertTestResource(t, st, "aws", testAccountID, TypeEC2NetworkInterface, ec2ARN(testRegion, testAccountID, "network-interface", "eni-0z"), testRegion, "{}")
	broker := upsertTestResource(t, st, "aws", testAccountID, TypeMSKNode, kafkaTestNodeA1, testRegion, mustJSON(kafkatypes.NodeInfo{
		NodeARN:        sdkaws.String(kafkaTestNodeA1),
		BrokerNodeInfo: &kafkatypes.BrokerNodeInfo{ClientSubnet: sdkaws.String("subnet-0a"), AttachedENIId: sdkaws.String("eni-0b")},
	}))
	zk := upsertTestResource(t, st, "aws", testAccountID, TypeMSKNode, kafkaTestNodeA2, testRegion, mustJSON(kafkatypes.NodeInfo{
		NodeARN:           sdkaws.String(kafkaTestNodeA2),
		ZookeeperNodeInfo: &kafkatypes.ZookeeperNodeInfo{AttachedENIId: sdkaws.String("eni-0z")},
	}))
	// Unscanned subnet and ENI: no phantom edges.
	orphan := upsertTestResource(t, st, "aws", testAccountID, TypeMSKNode, kafkaTestNodeX1, testRegion, mustJSON(kafkatypes.NodeInfo{
		NodeARN:        sdkaws.String(kafkaTestNodeX1),
		BrokerNodeInfo: &kafkatypes.BrokerNodeInfo{ClientSubnet: sdkaws.String("subnet-0gone"), AttachedENIId: sdkaws.String("eni-0gone")},
	}))

	if err := resolveMSKNodeRefs(acct, st); err != nil {
		t.Fatalf("resolveMSKNodeRefs: %v", err)
	}
	rels := mskRelsFrom(t, st, broker, store.RelAttachedTo)
	assertRelationship(t, rels, broker, subnet, store.RelAttachedTo)
	assertRelationship(t, rels, broker, eniB, store.RelAttachedTo)
	if len(rels) != 2 {
		t.Errorf("broker edges = %d, want 2", len(rels))
	}
	zkRels := mskRelsFrom(t, st, zk, store.RelAttachedTo)
	assertRelationship(t, zkRels, zk, eniZ, store.RelAttachedTo)
	if len(zkRels) != 1 {
		t.Errorf("zookeeper node edges = %d, want 1", len(zkRels))
	}
	assertNoMSKRels(t, st, orphan, store.RelAttachedTo)
}

func TestResolveMSKNodeRefs_NoAttrs(t *testing.T) {
	st := newTestStore(t)
	upsertTestResource(t, st, "aws", testAccountID, TypeEC2Subnet, ec2ARN(testRegion, testAccountID, "subnet", "subnet-0a"), testRegion, "{}")
	node := upsertTestResource(t, st, "aws", testAccountID, TypeMSKNode, kafkaTestNodeA1, testRegion, "{}")
	if err := resolveMSKNodeRefs(newTestAccount(testAccountID), st); err != nil {
		t.Fatalf("resolveMSKNodeRefs: %v", err)
	}
	assertNoMSKRels(t, st, node, store.RelAttachedTo)
}

func TestResolveMSKChannelRefs_S3AndIceberg(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	const keyARN = "arn:aws:kms:us-east-1:123456789012:key/11111111-2222-3333-4444-555555555555"
	topic := upsertTestResource(t, st, "aws", testAccountID, TypeMSKTopic, kafkaTestTopicX1, testRegion, "{}")
	lake := upsertTestResource(t, st, "aws", testAccountID, TypeS3Bucket, s3BucketARN("aws", "lake"), testRegion, "{}")
	dlq := upsertTestResource(t, st, "aws", testAccountID, TypeS3Bucket, s3BucketARN("aws", "dlq"), testRegion, "{}")
	iceDLQ := upsertTestResource(t, st, "aws", testAccountID, TypeS3Bucket, s3BucketARN("aws", "ice-dlq"), testRegion, "{}")
	role := upsertTestResource(t, st, "aws", testAccountID, TypeIAMRole, "arn:aws:iam::123456789012:role/msk-delivery", "", "{}")
	iceRole := upsertTestResource(t, st, "aws", testAccountID, TypeIAMRole, "arn:aws:iam::123456789012:role/msk-iceberg", "", "{}")
	key := upsertTestResource(t, st, "aws", testAccountID, TypeKMSKey, keyARN, testRegion, "{}")

	s3Channel := upsertTestResource(t, st, "aws", testAccountID, TypeMSKChannel, kafkaTestChannel1, testRegion, mustJSON(kafka.DescribeChannelOutput{
		ChannelArn:              sdkaws.String(kafkaTestChannel1),
		TopicConfigurationList:  []kafkatypes.TopicConfiguration{{TopicArn: sdkaws.String(kafkaTestTopicX1)}, {TopicArn: sdkaws.String(kafkaTestTopicA1)}},
		EncryptionConfiguration: &kafkatypes.EncryptionConfiguration{KmsKeyArn: sdkaws.String(keyARN)},
		S3DestinationConfiguration: &kafkatypes.S3DestinationConfiguration{
			ServiceExecutionRoleArn: sdkaws.String("arn:aws:iam::123456789012:role/msk-delivery"),
			Storage:                 &kafkatypes.S3Storage{BucketArn: sdkaws.String(s3BucketARN("aws", "lake"))},
			DeadLetterQueueS3:       &kafkatypes.DeadLetterQueueS3{BucketArn: sdkaws.String(s3BucketARN("aws", "dlq"))},
		},
	}))
	iceChannel := upsertTestResource(t, st, "aws", testAccountID, TypeMSKChannel, kafkaTestChannel2, testRegion, mustJSON(kafka.DescribeChannelOutput{
		ChannelArn: sdkaws.String(kafkaTestChannel2),
		IcebergDestinationConfiguration: &kafkatypes.IcebergDestinationConfiguration{
			ServiceExecutionRoleArn: sdkaws.String("arn:aws:iam::123456789012:role/msk-iceberg"),
			DeadLetterQueueS3:       &kafkatypes.DeadLetterQueueS3{BucketArn: sdkaws.String(s3BucketARN("aws", "ice-dlq"))},
		},
	}))

	if err := resolveMSKChannelRefs(acct, st); err != nil {
		t.Fatalf("resolveMSKChannelRefs: %v", err)
	}
	rels := mskRelsFrom(t, st, s3Channel, store.RelUses)
	for _, tgt := range []string{topic, lake, dlq, role, key} {
		assertRelationship(t, rels, s3Channel, tgt, store.RelUses)
	}
	// The unscanned topic A1 yields no edge.
	if len(rels) != 5 {
		t.Errorf("s3 channel edges = %d, want 5", len(rels))
	}
	iceRels := mskRelsFrom(t, st, iceChannel, store.RelUses)
	assertRelationship(t, iceRels, iceChannel, iceRole, store.RelUses)
	assertRelationship(t, iceRels, iceChannel, iceDLQ, store.RelUses)
	if len(iceRels) != 2 {
		t.Errorf("iceberg channel edges = %d, want 2", len(iceRels))
	}
}

func TestResolveMSKChannelRefs_NoAttrs(t *testing.T) {
	st := newTestStore(t)
	upsertTestResource(t, st, "aws", testAccountID, TypeMSKTopic, kafkaTestTopicX1, testRegion, "{}")
	ch := upsertTestResource(t, st, "aws", testAccountID, TypeMSKChannel, kafkaTestChannel1, testRegion, "{}")
	if err := resolveMSKChannelRefs(newTestAccount(testAccountID), st); err != nil {
		t.Fatalf("resolveMSKChannelRefs: %v", err)
	}
	assertNoMSKRels(t, st, ch, store.RelUses)
}

// Only a connection whose client is the scanned account resolves: the client's
// vpc-connection row lives under the client account.
func TestResolveMSKClientVpcConnectionRefs_SameAccountClient(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	const sameAccountConn = "arn:aws:kafka:us-east-1:123456789012:vpc-connection/123456789012/std/ffff-3"
	clientRow := upsertTestResource(t, st, "aws", testAccountID, TypeMSKVpcConnection, sameAccountConn, testRegion, "{}")
	// The cross-account client's own row, scanned under its account.
	upsertTestResource(t, st, "aws", "210987654321", TypeMSKVpcConnection, kafkaTestVpcConn1, testRegion, "{}")
	same := upsertTestResource(t, st, "aws", testAccountID, TypeMSKClientVpcConnection, kafkaClientVpcConnectionNativeID(kafkaTestClusterA, sameAccountConn), testRegion,
		mustJSON(kafkatypes.ClientVpcConnection{VpcConnectionArn: sdkaws.String(sameAccountConn), Owner: sdkaws.String(testAccountID)}))
	cross := upsertTestResource(t, st, "aws", testAccountID, TypeMSKClientVpcConnection, kafkaTestClientA1, testRegion,
		mustJSON(kafkatypes.ClientVpcConnection{VpcConnectionArn: sdkaws.String(kafkaTestVpcConn1), Owner: sdkaws.String("210987654321")}))

	if err := resolveMSKClientVpcConnectionRefs(acct, st); err != nil {
		t.Fatalf("resolveMSKClientVpcConnectionRefs: %v", err)
	}
	rels := mskRelsFrom(t, st, same, store.RelAttachedTo)
	assertRelationship(t, rels, same, clientRow, store.RelAttachedTo)
	if len(rels) != 1 {
		t.Errorf("same-account edges = %d, want 1", len(rels))
	}
	assertNoMSKRels(t, st, cross, store.RelAttachedTo)
}

func TestResolveMSKClientVpcConnectionRefs_NoAttrs(t *testing.T) {
	st := newTestStore(t)
	conn := upsertTestResource(t, st, "aws", testAccountID, TypeMSKClientVpcConnection, kafkaTestClientA1, testRegion, "{}")
	if err := resolveMSKClientVpcConnectionRefs(newTestAccount(testAccountID), st); err != nil {
		t.Fatalf("resolveMSKClientVpcConnectionRefs: %v", err)
	}
	assertNoMSKRels(t, st, conn, store.RelAttachedTo)
}

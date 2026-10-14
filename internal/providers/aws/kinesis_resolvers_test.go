package aws

import (
	"encoding/json"
	"errors"
	"testing"

	kinesistypes "github.com/aws/aws-sdk-go-v2/service/kinesis/types"
	"github.com/icearp/disco-cli/store"
)

// A channel uses each scanned source stream its summary names; an unscanned
// source stream gets no edge.
func TestResolveKinesisChannelSourceStreams(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	const ordersARN = "arn:aws:kinesis:us-east-1:123456789012:stream/orders"
	const clicksARN = "arn:aws:kinesis:us-east-1:123456789012:stream/clicks"
	const unscannedARN = "arn:aws:kinesis:us-east-1:123456789012:stream/gone"
	ordersID := upsertTestResource(t, st, "aws", acct.ID, TypeKinesisStream, ordersARN, testRegion, "{}")
	clicksID := upsertTestResource(t, st, "aws", acct.ID, TypeKinesisStream, clicksARN, testRegion, "{}")
	attrs := mustJSON(kinesistypes.ChannelSummary{
		ChannelARN: ptrStr("arn:aws:kinesis:us-east-1:123456789012:channel/c1"),
		Streams: []kinesistypes.ChannelStreamIdentifier{
			{StreamARN: ptrStr(ordersARN)}, {StreamARN: ptrStr(clicksARN)}, {StreamARN: ptrStr(unscannedARN)},
		},
	})
	chID := upsertTestResource(t, st, "aws", acct.ID, TypeKinesisChannel, "arn:aws:kinesis:us-east-1:123456789012:channel/c1", testRegion, attrs)

	if err := resolveKinesisChannelSourceStreams(acct, st); err != nil {
		t.Fatalf("resolveKinesisChannelSourceStreams: %v", err)
	}
	rels, err := st.RelationshipsFrom(chID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	assertRelationship(t, rels, chID, ordersID, store.RelUses)
	assertRelationship(t, rels, chID, clicksID, store.RelUses)
	if len(rels) != 2 {
		t.Errorf("edges = %+v, want exactly orders and clicks", rels)
	}
}

func TestResolveKinesisChannelSourceStreams_NoAttrs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	upsertTestResource(t, st, "aws", acct.ID, TypeKinesisStream, "arn:aws:kinesis:us-east-1:123456789012:stream/orders", testRegion, "{}")
	chID := upsertTestResource(t, st, "aws", acct.ID, TypeKinesisChannel, "arn:aws:kinesis:us-east-1:123456789012:channel/c1", testRegion, "{}")

	if err := resolveKinesisChannelSourceStreams(acct, st); err != nil {
		t.Fatalf("resolveKinesisChannelSourceStreams: %v", err)
	}
	rels, err := st.RelationshipsFrom(chID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 0 {
		t.Errorf("edges = %+v, want none", rels)
	}
}

// Attributes that do not decode are an error, not a silent skip.
func TestResolveKinesisChannelSourceStreams_UndecodableAttrs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	upsertTestResource(t, st, "aws", acct.ID, TypeKinesisStream, "arn:aws:kinesis:us-east-1:123456789012:stream/orders", testRegion, "{}")
	upsertTestResource(t, st, "aws", acct.ID, TypeKinesisChannel, "arn:aws:kinesis:us-east-1:123456789012:channel/c1", testRegion, `{"Streams":"not-a-list"}`)

	var typeErr *json.UnmarshalTypeError
	if err := resolveKinesisChannelSourceStreams(acct, st); !errors.As(err, &typeErr) {
		t.Errorf("err = %v, want a *json.UnmarshalTypeError", err)
	}
}

// TestResolveKinesisStreamRelationships_KMS verifies KMS edge for customer-managed keys.
func TestResolveKinesisStreamRelationships_KMS(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	streamARN := "arn:aws:kinesis:us-east-1:" + testAccountID + ":stream/events"
	keyARN := "arn:aws:kms:us-east-1:" + testAccountID + ":key/abcd"
	attrs := `{"EncryptionType":"KMS","KeyId":"` + keyARN + `"}`

	streamID := upsertTestResource(t, st, "aws", acct.ID, TypeKinesisStream, streamARN, testRegion, attrs)
	keyID := upsertTestResource(t, st, "aws", acct.ID, TypeKMSKey, keyARN, testRegion, "{}")

	if err := resolveKinesisStreamRelationships(acct, st); err != nil {
		t.Fatalf("resolveKinesisStreamRelationships: %v", err)
	}
	rels, err := st.RelationshipsFrom(streamID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	assertRelationship(t, rels, streamID, keyID, store.RelUses)
}

// TestResolveKinesisStreamRelationships_AWSManagedKey verifies alias/aws/kinesis is skipped.
func TestResolveKinesisStreamRelationships_AWSManagedKey(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	streamARN := "arn:aws:kinesis:us-east-1:" + testAccountID + ":stream/events"
	attrs := `{"EncryptionType":"KMS","KeyId":"alias/aws/kinesis"}`
	streamID := upsertTestResource(t, st, "aws", acct.ID, TypeKinesisStream, streamARN, testRegion, attrs)

	if err := resolveKinesisStreamRelationships(acct, st); err != nil {
		t.Fatalf("resolveKinesisStreamRelationships: %v", err)
	}
	rels, _ := st.RelationshipsFrom(streamID)
	if len(rels) != 0 {
		t.Errorf("expected 0 rels for AWS-managed key, got %d", len(rels))
	}
}

func TestResolveKinesisStreamConsumerToStream(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	streamARN := "arn:aws:kinesis:us-east-1:" + testAccountID + ":stream/orders"
	consumerARN := streamARN + "/consumer/proc:1700000000"

	cID := upsertTestResource(t, st, "aws", acct.ID, TypeKinesisStreamConsumer, consumerARN, testRegion, "{}")
	sID := upsertTestResource(t, st, "aws", acct.ID, TypeKinesisStream, streamARN, testRegion, "{}")

	if err := resolveKinesisStreamConsumerToStream(acct, st); err != nil {
		t.Fatalf("resolveKinesisStreamConsumerToStream: %v", err)
	}
	rels, _ := st.RelationshipsFrom(cID)
	assertRelationship(t, rels, cID, sID, store.RelAttachedTo)
}

package aws

import (
	"testing"

	"codeberg.org/icearp/disco/internal/store"
)

// TestResolveFirehoseDeliveryStreamRelationships verifies source, S3 destination,
// and KMS edges.
func TestResolveFirehoseDeliveryStreamRelationships(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	dsARN := "arn:aws:firehose:us-east-1:" + testAccountID + ":deliverystream/main"
	kinesisARN := "arn:aws:kinesis:us-east-1:" + testAccountID + ":stream/src"
	bucketARN := "arn:aws:s3:::logs-bucket"
	keyARN := "arn:aws:kms:us-east-1:" + testAccountID + ":key/abc"

	attrs := `{
		"Source":{"KinesisStreamSourceDescription":{"KinesisStreamARN":"` + kinesisARN + `"}},
		"Destinations":[{
			"ExtendedS3DestinationDescription":{
				"BucketARN":"` + bucketARN + `",
				"EncryptionConfiguration":{"KMSEncryptionConfig":{"AWSKMSKeyARN":"` + keyARN + `"}}
			}
		}]
	}`

	dsID := upsertTestResource(t, st, "aws", acct.ID, TypeFirehoseDeliveryStream, dsARN, testRegion, attrs)
	streamID := upsertTestResource(t, st, "aws", acct.ID, TypeKinesisStream, kinesisARN, testRegion, "{}")
	bucketID := upsertTestResource(t, st, "aws", acct.ID, TypeS3Bucket, bucketARN, testRegion, "{}")
	keyID := upsertTestResource(t, st, "aws", acct.ID, TypeKMSKey, keyARN, testRegion, "{}")

	if err := resolveFirehoseDeliveryStreamRelationships(acct, st); err != nil {
		t.Fatalf("resolveFirehoseDeliveryStreamRelationships: %v", err)
	}
	rels, err := st.RelationshipsFrom(dsID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	assertRelationship(t, rels, dsID, streamID, store.RelUses)
	assertRelationship(t, rels, dsID, bucketID, store.RelRoutesTo)
	assertRelationship(t, rels, dsID, keyID, store.RelUses)
}

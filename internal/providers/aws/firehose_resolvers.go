package aws

import (
	"encoding/json"
	"fmt"
	"strings"

	"codeberg.org/icearp/disco/internal/store"
	"codeberg.org/icearp/disco/internal/util"
)

func init() { registerResolver(resolveFirehoseDeliveryStreamRelationships) }

// firehoseDestinationAttrs picks out the fields we care about across each of
// the DestinationDescription sub-types. The AWS SDK struct is large; we only
// parse the subset that yields edges to resources disco scans.
type firehoseDestinationAttrs struct {
	S3DestinationDescription *struct {
		BucketARN              *string             `json:"BucketARN"`
		EncryptionConfiguration *firehoseKMSWrapper `json:"EncryptionConfiguration"`
	} `json:"S3DestinationDescription"`
	ExtendedS3DestinationDescription *struct {
		BucketARN              *string             `json:"BucketARN"`
		EncryptionConfiguration *firehoseKMSWrapper `json:"EncryptionConfiguration"`
	} `json:"ExtendedS3DestinationDescription"`
}

type firehoseKMSWrapper struct {
	KMSEncryptionConfig *struct {
		AWSKMSKeyARN *string `json:"AWSKMSKeyARN"`
	} `json:"KMSEncryptionConfig"`
}

// resolveFirehoseDeliveryStreamRelationships links delivery streams to:
//   - Kinesis stream source (KinesisStreamAsSource streams)
//   - S3 bucket destinations (regular or extended)
//   - KMS key used for destination encryption
func resolveFirehoseDeliveryStreamRelationships(acct *account, st *store.Store) error {
	streams, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeFirehoseDeliveryStream},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	for _, r := range streams {
		var attrs struct {
			Source *struct {
				KinesisStreamSourceDescription *struct {
					KinesisStreamARN *string `json:"KinesisStreamARN"`
				} `json:"KinesisStreamSourceDescription"`
			} `json:"Source"`
			Destinations []firehoseDestinationAttrs `json:"Destinations"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		// Source → Kinesis stream
		if attrs.Source != nil && attrs.Source.KinesisStreamSourceDescription != nil {
			if arn := sv(attrs.Source.KinesisStreamSourceDescription.KinesisStreamARN); arn != "" {
				srcID := store.ResourceID("aws", acct.ID, TypeKinesisStream, arn)
				if err := st.UpsertRelationship(r.ID, srcID, store.RelUses, "directed", nil); err != nil {
					return fmt.Errorf("upsert firehose→kinesis source: %w", err)
				}
			}
		}
		// Destinations
		for _, d := range attrs.Destinations {
			if err := firehoseEmitS3AndKMS(st, r.ID, acct.ID, d); err != nil {
				return err
			}
		}
	}
	return nil
}

// firehoseEmitS3AndKMS emits routes-to S3 bucket edges and uses KMS edges for
// one destination description.
func firehoseEmitS3AndKMS(st *store.Store, fromID, acctID string, d firehoseDestinationAttrs) error {
	emitS3 := func(bucketARN string) error {
		if bucketARN == "" {
			return nil
		}
		bucketID := store.ResourceID("aws", acctID, TypeS3Bucket, bucketARN)
		if err := st.UpsertRelationship(fromID, bucketID, store.RelRoutesTo, "directed", nil); err != nil {
			return fmt.Errorf("upsert firehose→s3 bucket: %w", err)
		}
		return nil
	}
	emitKMS := func(w *firehoseKMSWrapper) error {
		if w == nil || w.KMSEncryptionConfig == nil {
			return nil
		}
		keyARN := sv(w.KMSEncryptionConfig.AWSKMSKeyARN)
		if keyARN == "" || strings.HasPrefix(keyARN, "alias/aws/") {
			return nil
		}
		keyID := store.ResourceID("aws", acctID, TypeKMSKey, keyARN)
		if err := st.UpsertRelationship(fromID, keyID, store.RelUses, "directed", nil); err != nil {
			return fmt.Errorf("upsert firehose→kms: %w", err)
		}
		return nil
	}
	if d.ExtendedS3DestinationDescription != nil {
		if err := emitS3(sv(d.ExtendedS3DestinationDescription.BucketARN)); err != nil {
			return err
		}
		if err := emitKMS(d.ExtendedS3DestinationDescription.EncryptionConfiguration); err != nil {
			return err
		}
	}
	if d.S3DestinationDescription != nil {
		if err := emitS3(sv(d.S3DestinationDescription.BucketARN)); err != nil {
			return err
		}
		if err := emitKMS(d.S3DestinationDescription.EncryptionConfiguration); err != nil {
			return err
		}
	}
	return nil
}

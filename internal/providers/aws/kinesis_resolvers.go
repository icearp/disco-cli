package aws

import (
	"encoding/json"
	"fmt"
	"strings"

	"codeberg.org/icearp/disco/store"
	"codeberg.org/icearp/disco/internal/util"
)

func init() {
	registerResolver(
		resolveKinesisStreamRelationships,
		EdgeDecl{TypeKinesisStream, TypeKMSKey, store.RelUses},
	)
	registerResolver(
		resolveKinesisStreamConsumerToStream,
		EdgeDecl{TypeKinesisStreamConsumer, TypeKinesisStream, store.RelAttachedTo},
	)
}

// resolveKinesisStreamConsumerToStream wires each enhanced-fan-out consumer
// back to its parent stream. The Consumer SDK struct carries no StreamARN
// field, but the consumer's own ARN encodes the parent stream:
//
//	arn:aws:kinesis:<r>:<a>:stream/<streamName>/consumer/<consumerName>:<ts>
//
// Trim everything from `/consumer/` onward to recover the stream ARN.
func resolveKinesisStreamConsumerToStream(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeKinesisStreamConsumer}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	streamSet, err := scannedIDSet(acct, st, TypeKinesisStream)
	if err != nil {
		return err
	}
	for _, r := range rows {
		i := strings.Index(r.NativeID, "/consumer/")
		if i <= 0 {
			continue
		}
		streamARN := r.NativeID[:i]
		tgt := store.ResourceID("aws", acct.ID, TypeKinesisStream, streamARN)
		if !streamSet[tgt] {
			continue
		}
		if err := st.UpsertRelationship(r.ID, tgt, store.RelAttachedTo, "directed", nil); err != nil {
			return fmt.Errorf("upsert kinesis consumer→stream: %w", err)
		}
	}
	return nil
}

// resolveKinesisStreamRelationships links each stream to its KMS key when
// KMS encryption is enabled.
func resolveKinesisStreamRelationships(acct *account, st *store.Store) error {
	streams, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeKinesisStream},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	kmsIdx, err := loadKMSResolveIndex(acct, st)
	if err != nil {
		return err
	}
	for _, r := range streams {
		var attrs struct {
			EncryptionType *string `json:"EncryptionType"`
			KeyID          *string `json:"KeyID"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if sv(attrs.EncryptionType) != "KMS" {
			continue
		}
		keyID, ok := kmsIdx.resolveKMSKeyID(sv(attrs.KeyID), sv(r.Region), acct.ID)
		if !ok {
			continue
		}
		if err := st.UpsertRelationship(r.ID, keyID, store.RelUses, "directed", nil); err != nil {
			return fmt.Errorf("upsert kinesis-stream→kms: %w", err)
		}
	}
	return nil
}

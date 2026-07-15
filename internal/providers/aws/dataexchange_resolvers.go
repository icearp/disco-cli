package aws

import (
	"encoding/json"
	"fmt"

	"codeberg.org/icearp/disco/internal/util"
	"codeberg.org/icearp/disco/store"
)

func init() {
	registerResolver(
		resolveDataExchangeDataGrantRefs,
		EdgeDecl{TypeDataExchangeDataGrants, TypeDataExchangeDataSets, store.RelUses},
	)
	registerResolver(
		resolveDataExchangeEventActionRefs,
		EdgeDecl{TypeDataExchangeEventActions, TypeDataExchangeDataSets, store.RelUses},
		EdgeDecl{TypeDataExchangeEventActions, TypeS3Bucket, store.RelUses},
		EdgeDecl{TypeDataExchangeEventActions, TypeKMSKey, store.RelUses},
	)
}

// dataExchangeDataSetIndex maps each scanned data set's Id to its resource ID.
// Data grants and event actions reference a data set by bare Id, not ARN.
func dataExchangeDataSetIndex(acct *account, st *store.Store) (map[string]string, error) {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeDataExchangeDataSets}, Limit: util.AllResources,
	})
	if err != nil {
		return nil, err
	}
	idx := make(map[string]string, len(rows))
	for _, r := range rows {
		var a struct {
			ID *string `json:"Id"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &a); err != nil {
			continue
		}
		if id := sv(a.ID); id != "" {
			idx[id] = r.ID
		}
	}
	return idx, nil
}

// resolveDataExchangeDataGrantRefs wires each data grant to the data set it shares.
func resolveDataExchangeDataGrantRefs(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeDataExchangeDataGrants}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	dsIdx, err := dataExchangeDataSetIndex(acct, st)
	if err != nil {
		return err
	}
	for _, r := range rows {
		// SourceDataSetId is the sender's owned data set (input to CreateDataGrant,
		// which disco scans); DataSetId is the receiver's entitled copy, out of
		// scope for an owned-only scan.
		var attrs struct {
			SourceDataSetID *string `json:"SourceDataSetId"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if tgtID, ok := dsIdx[sv(attrs.SourceDataSetID)]; ok {
			if err := st.UpsertRelationship(r.ID, tgtID, store.RelUses, "directed", nil); err != nil {
				return fmt.Errorf("upsert dataexchange grant→data-set: %w", err)
			}
		}
	}
	return nil
}

// resolveDataExchangeEventActionRefs wires each event action to the data set
// whose revision-published event triggers it, and to the S3 bucket + KMS key
// of its auto-export destination.
func resolveDataExchangeEventActionRefs(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeDataExchangeEventActions}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	dsIdx, err := dataExchangeDataSetIndex(acct, st)
	if err != nil {
		return err
	}
	bucketSet, err := scannedIDSet(acct, st, TypeS3Bucket)
	if err != nil {
		return err
	}
	kmsIdx, err := loadKMSResolveIndex(acct, st)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			Event *struct {
				RevisionPublished *struct {
					DataSetID *string `json:"DataSetId"`
				} `json:"RevisionPublished"`
			} `json:"Event"`
			Action *struct {
				ExportRevisionToS3 *struct {
					RevisionDestination *struct {
						Bucket *string `json:"Bucket"`
					} `json:"RevisionDestination"`
					Encryption *struct {
						KmsKeyArn *string `json:"KmsKeyArn"`
					} `json:"Encryption"`
				} `json:"ExportRevisionToS3"`
			} `json:"Action"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if attrs.Event != nil && attrs.Event.RevisionPublished != nil {
			if tgtID, ok := dsIdx[sv(attrs.Event.RevisionPublished.DataSetID)]; ok {
				if err := st.UpsertRelationship(r.ID, tgtID, store.RelUses, "directed", nil); err != nil {
					return fmt.Errorf("upsert dataexchange event-action→data-set: %w", err)
				}
			}
		}
		if attrs.Action == nil || attrs.Action.ExportRevisionToS3 == nil {
			continue
		}
		export := attrs.Action.ExportRevisionToS3
		if export.RevisionDestination != nil {
			if b := sv(export.RevisionDestination.Bucket); b != "" {
				tgtID := store.ResourceID("aws", acct.ID, "arn:aws:s3:::"+b)
				if bucketSet[tgtID] {
					if err := st.UpsertRelationship(r.ID, tgtID, store.RelUses, "directed", nil); err != nil {
						return fmt.Errorf("upsert dataexchange event-action→s3: %w", err)
					}
				}
			}
		}
		if export.Encryption != nil {
			if ref := sv(export.Encryption.KmsKeyArn); ref != "" {
				if keyID, ok := kmsIdx.resolveKMSKeyID(ref, sv(r.Region), acct.ID); ok {
					if err := st.UpsertRelationship(r.ID, keyID, store.RelUses, "directed", nil); err != nil {
						return fmt.Errorf("upsert dataexchange event-action→kms: %w", err)
					}
				}
			}
		}
	}
	return nil
}

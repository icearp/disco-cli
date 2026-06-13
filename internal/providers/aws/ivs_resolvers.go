package aws

import (
	"encoding/json"
	"fmt"

	"codeberg.org/icearp/disco/internal/util"
	"codeberg.org/icearp/disco/store"
)

func init() {
	registerResolver(
		resolveIVSChannelRefs,
		EdgeDecl{TypeIVSChannel, TypeIVSRecordingConfiguration, store.RelUses},
		EdgeDecl{TypeIVSChannel, TypeIVSPlaybackRestrictionPolicy, store.RelUses},
	)
	registerResolver(
		resolveIVSStreamKeyChannel,
		EdgeDecl{TypeIVSStreamKey, TypeIVSChannel, store.RelAttachedTo},
	)
	registerResolver(
		resolveIVSIngestConfigStage,
		EdgeDecl{TypeIVSIngestConfiguration, TypeIVSStage, store.RelAttachedTo},
	)
	registerResolver(
		resolveIVSRecordingConfigS3,
		EdgeDecl{TypeIVSRecordingConfiguration, TypeS3Bucket, store.RelUses},
	)
	registerResolver(
		resolveIVSStorageConfigS3,
		EdgeDecl{TypeIVSStorageConfiguration, TypeS3Bucket, store.RelUses},
	)
	registerResolver(
		resolveIVSStageStorageConfig,
		EdgeDecl{TypeIVSStage, TypeIVSStorageConfiguration, store.RelUses},
	)
}

// resolveIVSStageStorageConfig wires each Stage to the StorageConfiguration
// it auto-records into (AutoParticipantRecordingConfiguration.StorageConfigurationArn).
func resolveIVSStageStorageConfig(acct *account, st *store.Store) error {
	stages, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeIVSStage}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(stages) == 0 {
		return nil
	}
	scSet, err := scannedIDSet(acct, st, TypeIVSStorageConfiguration)
	if err != nil {
		return err
	}
	for _, s := range stages {
		var attrs struct {
			AutoParticipantRecordingConfiguration *struct {
				StorageConfigurationArn *string `json:"StorageConfigurationArn"`
			} `json:"AutoParticipantRecordingConfiguration"`
		}
		if err := json.Unmarshal([]byte(s.AttributesJSON), &attrs); err != nil {
			continue
		}
		if attrs.AutoParticipantRecordingConfiguration == nil {
			continue
		}
		arn := sv(attrs.AutoParticipantRecordingConfiguration.StorageConfigurationArn)
		if arn == "" {
			continue
		}
		tgtID := store.ResourceID("aws", acct.ID, TypeIVSStorageConfiguration, arn)
		if !scSet[tgtID] {
			continue
		}
		if err := st.UpsertRelationship(s.ID, tgtID, store.RelUses, "directed", nil); err != nil {
			return fmt.Errorf("upsert ivs stage→storage-configuration: %w", err)
		}
	}
	return nil
}

// resolveIVSRecordingConfigS3 wires each recording-configuration to the S3
// bucket recordings are written to (DestinationConfiguration.S3.BucketName).
func resolveIVSRecordingConfigS3(acct *account, st *store.Store) error {
	return resolveIVSS3Bucket(acct, st, TypeIVSRecordingConfiguration,
		func(raw []byte) string {
			var attrs struct {
				DestinationConfiguration *struct {
					S3 *struct {
						BucketName *string `json:"BucketName"`
					} `json:"S3"`
				} `json:"DestinationConfiguration"`
			}
			if err := json.Unmarshal(raw, &attrs); err != nil ||
				attrs.DestinationConfiguration == nil ||
				attrs.DestinationConfiguration.S3 == nil {
				return ""
			}
			return sv(attrs.DestinationConfiguration.S3.BucketName)
		}, "recording-configuration")
}

// resolveIVSStorageConfigS3 wires each storage-configuration to its S3 bucket
// (S3.BucketName).
func resolveIVSStorageConfigS3(acct *account, st *store.Store) error {
	return resolveIVSS3Bucket(acct, st, TypeIVSStorageConfiguration,
		func(raw []byte) string {
			var attrs struct {
				S3 *struct {
					BucketName *string `json:"BucketName"`
				} `json:"S3"`
			}
			if err := json.Unmarshal(raw, &attrs); err != nil || attrs.S3 == nil {
				return ""
			}
			return sv(attrs.S3.BucketName)
		}, "storage-configuration")
}

func resolveIVSS3Bucket(acct *account, st *store.Store, sourceType string, extract func([]byte) string, label string) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{sourceType}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	bucketSet, err := scannedIDSet(acct, st, TypeS3Bucket)
	if err != nil {
		return err
	}
	for _, r := range rows {
		bucket := extract([]byte(r.AttributesJSON))
		if bucket == "" {
			continue
		}
		bArn := "arn:aws:s3:::" + bucket
		tgtID := store.ResourceID("aws", acct.ID, TypeS3Bucket, bArn)
		if !bucketSet[tgtID] {
			continue
		}
		if err := st.UpsertRelationship(r.ID, tgtID, store.RelUses, "directed", nil); err != nil {
			return fmt.Errorf("upsert ivs %s→s3: %w", label, err)
		}
	}
	return nil
}

func resolveIVSChannelRefs(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeIVSChannel}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	rcSet, err := scannedIDSet(acct, st, TypeIVSRecordingConfiguration)
	if err != nil {
		return err
	}
	prSet, err := scannedIDSet(acct, st, TypeIVSPlaybackRestrictionPolicy)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			RecordingConfigurationArn    *string `json:"RecordingConfigurationArn"`
			PlaybackRestrictionPolicyArn *string `json:"PlaybackRestrictionPolicyArn"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if rc := sv(attrs.RecordingConfigurationArn); rc != "" {
			tgtID := store.ResourceID("aws", acct.ID, TypeIVSRecordingConfiguration, rc)
			if rcSet[tgtID] {
				if err := st.UpsertRelationship(r.ID, tgtID, store.RelUses, "directed", nil); err != nil {
					return fmt.Errorf("upsert ivs channel→rec: %w", err)
				}
			}
		}
		if pr := sv(attrs.PlaybackRestrictionPolicyArn); pr != "" {
			tgtID := store.ResourceID("aws", acct.ID, TypeIVSPlaybackRestrictionPolicy, pr)
			if prSet[tgtID] {
				if err := st.UpsertRelationship(r.ID, tgtID, store.RelUses, "directed", nil); err != nil {
					return fmt.Errorf("upsert ivs channel→prp: %w", err)
				}
			}
		}
	}
	return nil
}

func resolveIVSStreamKeyChannel(acct *account, st *store.Store) error {
	return resolveIVSAttrEdge(acct, st, TypeIVSStreamKey, TypeIVSChannel, "ChannelArn", store.RelAttachedTo)
}

func resolveIVSIngestConfigStage(acct *account, st *store.Store) error {
	return resolveIVSAttrEdge(acct, st, TypeIVSIngestConfiguration, TypeIVSStage, "StageArn", store.RelAttachedTo)
}

// resolveIVSAttrEdge wires `<source>.<arnField>` → target ARN with
// FK-safe lookup. `arnField` must be a top-level *string ARN attr.
func resolveIVSAttrEdge(acct *account, st *store.Store, sourceType, targetType, arnField, kind string) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{sourceType}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	tgtSet, err := scannedIDSet(acct, st, targetType)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var raw map[string]any
		if err := json.Unmarshal([]byte(r.AttributesJSON), &raw); err != nil {
			continue
		}
		v, ok := raw[arnField].(string)
		if !ok || v == "" {
			continue
		}
		tgtID := store.ResourceID("aws", acct.ID, targetType, v)
		if !tgtSet[tgtID] {
			continue
		}
		if err := st.UpsertRelationship(r.ID, tgtID, kind, "directed", nil); err != nil {
			return fmt.Errorf("upsert ivs %s→%s: %w", sourceType, targetType, err)
		}
	}
	return nil
}

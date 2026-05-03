package aws

import (
	"encoding/json"
	"fmt"

	"codeberg.org/icearp/disco/internal/store"
	"codeberg.org/icearp/disco/internal/util"
)

func init() {
	registerResolver(resolveIVSChannelRefs,
		EdgeDecl{TypeIVSChannel, TypeIVSRecordingConfiguration, store.RelUses},
		EdgeDecl{TypeIVSChannel, TypeIVSPlaybackRestrictionPolicy, store.RelUses},
	)
	registerResolver(resolveIVSStreamKeyChannel,
		EdgeDecl{TypeIVSStreamKey, TypeIVSChannel, store.RelAttachedTo},
	)
	registerResolver(resolveIVSIngestConfigStage,
		EdgeDecl{TypeIVSIngestConfiguration, TypeIVSStage, store.RelAttachedTo},
	)
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

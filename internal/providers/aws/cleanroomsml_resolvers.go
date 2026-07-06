package aws

import (
	"encoding/json"
	"fmt"

	"codeberg.org/icearp/disco/internal/util"
	"codeberg.org/icearp/disco/store"
)

func init() {
	registerResolver(
		resolveCRMLAudienceModelDataset,
		EdgeDecl{TypeCleanRoomsMLAudienceModel, TypeCleanRoomsMLTrainingDataset, store.RelUses},
	)
	registerResolver(
		resolveCRMLConfiguredAudienceModelBase,
		EdgeDecl{TypeCleanRoomsMLConfiguredAudienceModel, TypeCleanRoomsMLAudienceModel, store.RelUses},
	)
	registerResolver(
		resolveCRMLTrainedModelAlgorithmAssociation,
		EdgeDecl{TypeCleanRoomsMLTrainedModel, TypeCleanRoomsMLConfiguredModelAlgorithmAssociation, store.RelUses},
	)
}

// crmlArnEdge wires each source row's ARN-bearing field to a scanned target,
// FK-safe against the target set. arnOf reads the field from AttributesJSON.
func crmlArnEdge(acct *account, st *store.Store, srcType, tgtType string, arnOf func(json.RawMessage) string) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{srcType},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	tgtSet, err := scannedIDSet(acct, st, tgtType)
	if err != nil {
		return err
	}
	for _, r := range rows {
		arn := arnOf(json.RawMessage(r.AttributesJSON))
		if arn == "" {
			continue
		}
		tgtID := store.ResourceID("aws", acct.ID, tgtType, arn)
		if !tgtSet[tgtID] {
			continue
		}
		if err := st.UpsertRelationship(r.ID, tgtID, store.RelUses, "directed", nil); err != nil {
			return fmt.Errorf("upsert crml %s→%s: %w", srcType, tgtType, err)
		}
	}
	return nil
}

func crmlField(raw json.RawMessage, field string) string {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		return ""
	}
	v, ok := m[field]
	if !ok {
		return ""
	}
	var s string
	if err := json.Unmarshal(v, &s); err != nil {
		return ""
	}
	return s
}

func resolveCRMLAudienceModelDataset(acct *account, st *store.Store) error {
	return crmlArnEdge(acct, st, TypeCleanRoomsMLAudienceModel, TypeCleanRoomsMLTrainingDataset,
		func(raw json.RawMessage) string { return crmlField(raw, "TrainingDatasetArn") })
}

func resolveCRMLConfiguredAudienceModelBase(acct *account, st *store.Store) error {
	return crmlArnEdge(acct, st, TypeCleanRoomsMLConfiguredAudienceModel, TypeCleanRoomsMLAudienceModel,
		func(raw json.RawMessage) string { return crmlField(raw, "AudienceModelArn") })
}

func resolveCRMLTrainedModelAlgorithmAssociation(acct *account, st *store.Store) error {
	return crmlArnEdge(acct, st, TypeCleanRoomsMLTrainedModel, TypeCleanRoomsMLConfiguredModelAlgorithmAssociation,
		func(raw json.RawMessage) string { return crmlField(raw, "ConfiguredModelAlgorithmAssociationArn") })
}

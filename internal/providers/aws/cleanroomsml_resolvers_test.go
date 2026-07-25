package aws

import (
	"encoding/json"
	"testing"

	crmltypes "github.com/aws/aws-sdk-go-v2/service/cleanroomsml/types"
	"github.com/icearp/disco-cli/store"
)

func crmlAttrs(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal crml attrs: %v", err)
	}
	return string(b)
}

func TestResolveCleanRoomsMLEdges(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	dsArn := "arn:aws:cleanrooms-ml:" + testRegion + ":" + testAccountID + ":training-dataset/ds-1"
	dsID := upsertTestResource(t, st, "aws", acct.ID, TypeCleanRoomsMLTrainingDataset, dsArn, testRegion,
		crmlAttrs(t, crmltypes.TrainingDatasetSummary{TrainingDatasetArn: &dsArn}))

	amArn := "arn:aws:cleanrooms-ml:" + testRegion + ":" + testAccountID + ":audience-model/am-1"
	amID := upsertTestResource(t, st, "aws", acct.ID, TypeCleanRoomsMLAudienceModel, amArn, testRegion,
		crmlAttrs(t, crmltypes.AudienceModelSummary{AudienceModelArn: &amArn, TrainingDatasetArn: &dsArn}))

	camArn := "arn:aws:cleanrooms-ml:" + testRegion + ":" + testAccountID + ":configured-audience-model/cam-1"
	camID := upsertTestResource(t, st, "aws", acct.ID, TypeCleanRoomsMLConfiguredAudienceModel, camArn, testRegion,
		crmlAttrs(t, crmltypes.ConfiguredAudienceModelSummary{ConfiguredAudienceModelArn: &camArn, AudienceModelArn: &amArn}))

	assocArn := "arn:aws:cleanrooms-ml:" + testRegion + ":" + testAccountID + ":membership/m-1/configured-model-algorithm-association/cmaa-1"
	assocID := upsertTestResource(t, st, "aws", acct.ID, TypeCleanRoomsMLConfiguredModelAlgorithmAssociation, assocArn, testRegion,
		crmlAttrs(t, crmltypes.ConfiguredModelAlgorithmAssociationSummary{ConfiguredModelAlgorithmAssociationArn: &assocArn}))

	tmArn := "arn:aws:cleanrooms-ml:" + testRegion + ":" + testAccountID + ":membership/m-1/trained-model/tm-1"
	tmID := upsertTestResource(t, st, "aws", acct.ID, TypeCleanRoomsMLTrainedModel, tmArn, testRegion,
		crmlAttrs(t, crmltypes.TrainedModelSummary{TrainedModelArn: &tmArn, ConfiguredModelAlgorithmAssociationArn: &assocArn}))

	for _, fn := range []func(*account, *store.Store) error{
		resolveCRMLAudienceModelDataset,
		resolveCRMLConfiguredAudienceModelBase,
		resolveCRMLTrainedModelAlgorithmAssociation,
	} {
		if err := fn(acct, st); err != nil {
			t.Fatalf("resolver: %v", err)
		}
	}

	amRels, _ := st.RelationshipsFrom(amID)
	assertRelationship(t, amRels, amID, dsID, store.RelUses)
	camRels, _ := st.RelationshipsFrom(camID)
	assertRelationship(t, camRels, camID, amID, store.RelUses)
	tmRels, _ := st.RelationshipsFrom(tmID)
	assertRelationship(t, tmRels, tmID, assocID, store.RelUses)
}

// An audience-model whose TrainingDatasetArn points at an unscanned dataset, a
// configured-audience-model with no AudienceModelArn, and a trained-model whose
// association was never scanned all emit no edge.
func TestResolveCleanRoomsMLEdges_NoEdge(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	missingDS := "arn:aws:cleanrooms-ml:" + testRegion + ":" + testAccountID + ":training-dataset/never-scanned"
	amArn := "arn:aws:cleanrooms-ml:" + testRegion + ":" + testAccountID + ":audience-model/am-1"
	amID := upsertTestResource(t, st, "aws", acct.ID, TypeCleanRoomsMLAudienceModel, amArn, testRegion,
		crmlAttrs(t, crmltypes.AudienceModelSummary{AudienceModelArn: &amArn, TrainingDatasetArn: &missingDS}))

	camArn := "arn:aws:cleanrooms-ml:" + testRegion + ":" + testAccountID + ":configured-audience-model/cam-1"
	camID := upsertTestResource(t, st, "aws", acct.ID, TypeCleanRoomsMLConfiguredAudienceModel, camArn, testRegion,
		crmlAttrs(t, crmltypes.ConfiguredAudienceModelSummary{ConfiguredAudienceModelArn: &camArn}))

	tmArn := "arn:aws:cleanrooms-ml:" + testRegion + ":" + testAccountID + ":membership/m-1/trained-model/tm-1"
	missingAssoc := "arn:aws:cleanrooms-ml:" + testRegion + ":" + testAccountID + ":membership/m-1/configured-model-algorithm-association/never"
	tmID := upsertTestResource(t, st, "aws", acct.ID, TypeCleanRoomsMLTrainedModel, tmArn, testRegion,
		crmlAttrs(t, crmltypes.TrainedModelSummary{TrainedModelArn: &tmArn, ConfiguredModelAlgorithmAssociationArn: &missingAssoc}))

	for _, fn := range []func(*account, *store.Store) error{
		resolveCRMLAudienceModelDataset,
		resolveCRMLConfiguredAudienceModelBase,
		resolveCRMLTrainedModelAlgorithmAssociation,
	} {
		if err := fn(acct, st); err != nil {
			t.Fatalf("resolver: %v", err)
		}
	}

	for _, id := range []string{amID, camID, tmID} {
		rels, _ := st.RelationshipsFrom(id)
		if len(rels) != 0 {
			t.Errorf("row %s emitted %d edges, want 0", id, len(rels))
		}
	}
}

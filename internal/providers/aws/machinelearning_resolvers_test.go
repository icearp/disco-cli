package aws

import (
	"testing"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	mltypes "github.com/aws/aws-sdk-go-v2/service/machinelearning/types"
	"github.com/icearp/disco-cli/store"
)

const (
	mlTestRoleARN    = "arn:aws:iam::" + testAccountID + ":role/ml/MLRedshiftRole"
	mlTestPipelineID = "df-0123456789ABCDEF"
)

func mlTestPipelineARN() string {
	return "arn:aws:datapipeline:" + testRegion + ":" + testAccountID + ":pipeline/" + mlTestPipelineID
}

// mlRels returns the edges from id as "kind -> to" keys.
func mlRels(t *testing.T, st *store.Store, id string) map[string]bool {
	t.Helper()
	rels, err := st.RelationshipsFrom(id)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	got := map[string]bool{}
	for _, r := range rels {
		got[r.Kind+" -> "+r.ToID] = true
	}
	return got
}

func mlEdgeKey(kind, toID string) string { return kind + " -> " + toID }

func TestResolveMachineLearningRefs_LinksScannedTargets(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	roleID := upsertTestResource(t, st, "aws", acct.ID, TypeIAMRole, mlTestRoleARN, "", "{}")
	pipeID := upsertTestResource(t, st, "aws", acct.ID, TypeDataPipelinePipeline, mlTestPipelineARN(), testRegion, "{}")

	dsID := upsertTestResource(t, st, "aws", acct.ID, TypeMachineLearningDataSource, mlTestNativeID("datasource", "ds-1"), testRegion, mustJSON(mltypes.DataSource{
		DataSourceId: sdkaws.String("ds-1"),
		RoleARN:      sdkaws.String(mlTestRoleARN),
		RDSMetadata: &mltypes.RDSMetadata{
			DataPipelineId: sdkaws.String(mlTestPipelineID),
			Database:       &mltypes.RDSDatabase{InstanceIdentifier: sdkaws.String("mydb"), DatabaseName: sdkaws.String("sales")},
		},
	}))
	evalDSID := upsertTestResource(t, st, "aws", acct.ID, TypeMachineLearningDataSource, mlTestNativeID("datasource", "ds-2"), testRegion, mustJSON(mltypes.DataSource{DataSourceId: sdkaws.String("ds-2")}))
	modelID := upsertTestResource(t, st, "aws", acct.ID, TypeMachineLearningMLModel, mlTestNativeID("mlmodel", "ml-1"), testRegion, mustJSON(mltypes.MLModel{
		MLModelId: sdkaws.String("ml-1"), TrainingDataSourceId: sdkaws.String("ds-1"),
	}))
	evalID := upsertTestResource(t, st, "aws", acct.ID, TypeMachineLearningEvaluation, mlTestNativeID("evaluation", "ev-1"), testRegion, mustJSON(mltypes.Evaluation{
		EvaluationId: sdkaws.String("ev-1"), MLModelId: sdkaws.String("ml-1"), EvaluationDataSourceId: sdkaws.String("ds-2"),
	}))
	bpID := upsertTestResource(t, st, "aws", acct.ID, TypeMachineLearningBatchPrediction, mlTestNativeID("batchprediction", "bp-1"), testRegion, mustJSON(mltypes.BatchPrediction{
		BatchPredictionId: sdkaws.String("bp-1"), MLModelId: sdkaws.String("ml-1"), BatchPredictionDataSourceId: sdkaws.String("ds-1"),
	}))

	if err := resolveMachineLearningRefs(acct, st); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	for _, c := range []struct {
		name string
		from string
		want []string
	}{
		{"datasource", dsID, []string{mlEdgeKey(store.RelAssumes, roleID), mlEdgeKey(store.RelUses, pipeID)}},
		{"ml-model", modelID, []string{mlEdgeKey(store.RelUses, dsID)}},
		{"evaluation", evalID, []string{mlEdgeKey(store.RelUses, modelID), mlEdgeKey(store.RelUses, evalDSID)}},
		{"batch-prediction", bpID, []string{mlEdgeKey(store.RelUses, modelID), mlEdgeKey(store.RelUses, dsID)}},
		{"datasource without refs", evalDSID, nil},
	} {
		got := mlRels(t, st, c.from)
		if len(got) != len(c.want) {
			t.Errorf("%s edges = %v, want %v", c.name, got, c.want)
		}
		for _, w := range c.want {
			if !got[w] {
				t.Errorf("%s missing edge %s; have %v", c.name, w, got)
			}
		}
	}
}

// References to objects, roles and pipelines that were not scanned produce no
// edge, and a row with no attributes produces none and no error.
func TestResolveMachineLearningRefs_UnscannedAndEmptyAttrs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	dsID := upsertTestResource(t, st, "aws", acct.ID, TypeMachineLearningDataSource, mlTestNativeID("datasource", "ds-1"), testRegion, mustJSON(mltypes.DataSource{
		DataSourceId: sdkaws.String("ds-1"),
		RoleARN:      sdkaws.String(mlTestRoleARN),
		RDSMetadata:  &mltypes.RDSMetadata{DataPipelineId: sdkaws.String(mlTestPipelineID)},
	}))
	bpID := upsertTestResource(t, st, "aws", acct.ID, TypeMachineLearningBatchPrediction, mlTestNativeID("batchprediction", "bp-1"), testRegion, mustJSON(mltypes.BatchPrediction{
		BatchPredictionId: sdkaws.String("bp-1"), MLModelId: sdkaws.String("gone"), BatchPredictionDataSourceId: sdkaws.String("gone"),
	}))
	emptyID := upsertTestResource(t, st, "aws", acct.ID, TypeMachineLearningEvaluation, mlTestNativeID("evaluation", "ev-1"), testRegion, "{}")
	if err := resolveMachineLearningRefs(acct, st); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	for name, id := range map[string]string{"datasource": dsID, "batch-prediction": bpID, "empty evaluation": emptyID} {
		if got := mlRels(t, st, id); len(got) != 0 {
			t.Errorf("%s edges = %v, want none", name, got)
		}
	}
}

func TestResolveMachineLearningRefs_DecodeErrorReturned(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	dsID := upsertTestResource(t, st, "aws", acct.ID, TypeMachineLearningDataSource, mlTestNativeID("datasource", "ds-1"), testRegion, "{}")
	// ListResources orders by name, so the bad row "a" is read before the
	// valid row "b", whose edge must survive the decode error.
	upsertTestResourceNamed(t, st, TypeMachineLearningMLModel, mlTestNativeID("mlmodel", "ml-bad"), testRegion, `["not an object"]`, "a")
	goodID := upsertTestResourceNamed(t, st, TypeMachineLearningMLModel, mlTestNativeID("mlmodel", "ml-1"), testRegion,
		mustJSON(mltypes.MLModel{MLModelId: sdkaws.String("ml-1"), TrainingDataSourceId: sdkaws.String("ds-1")}), "b")
	if err := resolveMachineLearningRefs(acct, st); err == nil {
		t.Fatal("resolve: nil error, want the decode error")
	}
	if got := mlRels(t, st, goodID); !got[mlEdgeKey(store.RelUses, dsID)] {
		t.Errorf("valid row edges = %v, want uses -> %s", got, dsID)
	}
}

package aws

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/icearp/disco-cli/internal/util"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerResolver(
		resolveMachineLearningRefs,
		EdgeDecl{TypeMachineLearningDataSource, TypeIAMRole, store.RelAssumes},
		EdgeDecl{TypeMachineLearningDataSource, TypeDataPipelinePipeline, store.RelUses},
		EdgeDecl{TypeMachineLearningMLModel, TypeMachineLearningDataSource, store.RelUses},
		EdgeDecl{TypeMachineLearningEvaluation, TypeMachineLearningMLModel, store.RelUses},
		EdgeDecl{TypeMachineLearningEvaluation, TypeMachineLearningDataSource, store.RelUses},
		EdgeDecl{TypeMachineLearningBatchPrediction, TypeMachineLearningMLModel, store.RelUses},
		EdgeDecl{TypeMachineLearningBatchPrediction, TypeMachineLearningDataSource, store.RelUses},
	)
}

// mlRefAttrs holds the reference fields of all four Amazon ML element shapes;
// each shape fills only its own. MLModelId is a reference only on an
// evaluation or batch prediction: on an ML model it is the row's own id.
type mlRefAttrs struct {
	RoleARN     *string `json:"RoleARN"`
	RDSMetadata *struct {
		DataPipelineID *string `json:"DataPipelineId"`
	} `json:"RDSMetadata"`
	TrainingDataSourceID        *string `json:"TrainingDataSourceId"`
	EvaluationDataSourceID      *string `json:"EvaluationDataSourceId"`
	BatchPredictionDataSourceID *string `json:"BatchPredictionDataSourceId"`
	MLModelID                   *string `json:"MLModelId"`
}

type mlRef struct {
	nativeID string
	kind     string
}

// refs lists the edge targets the element names. The RDS instance and Redshift
// cluster are not linked: the element gives their identifiers without a
// region, and an identifier is unique only within one account and region.
// RDSMetadata.ResourceRole and ServiceRole are not linked either: they are
// role names, and a name alone cannot be turned into the ARN of a role that
// has a path; linking them needs a role-name index this resolver does not build.
// The Data Pipeline id is unique on its own, so building it in the object's
// region cannot pick the wrong pipeline; a pipeline elsewhere gets no edge.
func (a mlRefAttrs) refs(partition, typ, region, acctID string) []mlRef {
	var out []mlRef
	if arn := sv(a.RoleARN); arn != "" {
		out = append(out, mlRef{arn, store.RelAssumes})
	}
	if a.RDSMetadata != nil {
		if id := sv(a.RDSMetadata.DataPipelineID); id != "" {
			out = append(out, mlRef{arnFor(partition, "datapipeline", region, acctID, "pipeline/"+id), store.RelUses})
		}
	}
	for _, id := range []*string{a.TrainingDataSourceID, a.EvaluationDataSourceID, a.BatchPredictionDataSourceID} {
		if sv(id) != "" {
			out = append(out, mlRef{mlNativeID(partition, region, acctID, "datasource", sv(id)), store.RelUses})
		}
	}
	if id := sv(a.MLModelID); id != "" && typ != TypeMachineLearningMLModel {
		out = append(out, mlRef{mlNativeID(partition, region, acctID, "mlmodel", id), store.RelUses})
	}
	return out
}

// resolveMachineLearningRefs links each Amazon ML object to the objects, IAM
// role and Data Pipeline its element names, when that target was scanned. A
// row whose attributes do not decode is reported and costs no other row its
// edges.
func resolveMachineLearningRefs(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID,
		Types: []string{TypeMachineLearningDataSource, TypeMachineLearningMLModel, TypeMachineLearningEvaluation, TypeMachineLearningBatchPrediction},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	scanned := map[string]bool{}
	for _, r := range rows {
		if r.Type == TypeMachineLearningDataSource || r.Type == TypeMachineLearningMLModel {
			scanned[r.ID] = true
		}
	}
	for _, typ := range []string{TypeIAMRole, TypeDataPipelinePipeline} {
		set, err := scannedIDSet(acct, st, typ)
		if err != nil {
			return err
		}
		for id := range set {
			scanned[id] = true
		}
	}
	var decodeErrs []error
	for _, r := range rows {
		var a mlRefAttrs
		if err := json.Unmarshal([]byte(r.AttributesJSON), &a); err != nil {
			decodeErrs = append(decodeErrs, fmt.Errorf("decode %s attributes of %s: %w", r.Type, r.NativeID, err))
			continue
		}
		for _, ref := range a.refs(acct.partition(), r.Type, sv(r.Region), acct.ID) {
			tgt := store.ResourceID("aws", acct.ID, ref.nativeID)
			if !scanned[tgt] {
				continue
			}
			if err := st.UpsertRelationship(r.ID, tgt, ref.kind, "directed", nil); err != nil {
				return fmt.Errorf("upsert %s -> %s: %w", r.NativeID, ref.nativeID, err)
			}
		}
	}
	return errors.Join(decodeErrs...)
}

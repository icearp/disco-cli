package aws

import (
	"encoding/json"
	"fmt"

	"codeberg.org/icearp/disco/internal/util"
	"codeberg.org/icearp/disco/store"
)

func init() {
	registerResolver(
		resolveOSISPipelineEndpointPipeline,
		EdgeDecl{TypeOSISPipelineEndpoint, TypeOSISPipeline, store.RelAttachedTo},
	)
}

// resolveOSISPipelineEndpointPipeline wires each VPC pipeline endpoint to the
// pipeline it fronts via the PipelineArn attribute (which equals the pipeline
// NativeID). FK-safe against scanned pipelines.
func resolveOSISPipelineEndpointPipeline(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeOSISPipelineEndpoint},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	pipeSet, err := scannedIDSet(acct, st, TypeOSISPipeline)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			PipelineArn *string `json:"PipelineArn"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		pipeARN := sv(attrs.PipelineArn)
		if pipeARN == "" {
			continue
		}
		tgtID := store.ResourceID("aws", acct.ID, pipeARN)
		if !pipeSet[tgtID] {
			continue
		}
		if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
			return fmt.Errorf("upsert osis pipeline-endpoint→pipeline: %w", err)
		}
	}
	return nil
}

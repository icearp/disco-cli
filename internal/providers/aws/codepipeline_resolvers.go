package aws

import (
	"encoding/json"
	"fmt"

	"codeberg.org/icearp/disco/internal/store"
	"codeberg.org/icearp/disco/internal/util"
)

func init() {
	registerResolver(resolveCodePipelineWebhookToPipeline,
		EdgeDecl{TypeCodePipelineWebhook, TypeCodePipelinePipeline, store.RelAttachedTo},
	)
}

// resolveCodePipelineWebhookToPipeline wires each webhook to its target
// pipeline via Definition.TargetPipeline (a pipeline name).
func resolveCodePipelineWebhookToPipeline(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeCodePipelineWebhook}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	pSet, err := scannedIDSet(acct, st, TypeCodePipelinePipeline)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			Definition *struct {
				TargetPipeline *string `json:"TargetPipeline"`
			} `json:"Definition"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if attrs.Definition == nil {
			continue
		}
		n := sv(attrs.Definition.TargetPipeline)
		if n == "" {
			continue
		}
		pARN := fmt.Sprintf("arn:aws:codepipeline:%s:%s:%s", sv(r.Region), acct.ID, n)
		tgtID := store.ResourceID("aws", acct.ID, TypeCodePipelinePipeline, pARN)
		if !pSet[tgtID] {
			continue
		}
		if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
			return fmt.Errorf("upsert codepipeline webhook→pipeline: %w", err)
		}
	}
	return nil
}

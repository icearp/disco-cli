package aws

import (
	"encoding/json"
	"fmt"

	"codeberg.org/icearp/disco/internal/util"
	"codeberg.org/icearp/disco/store"
)

func init() {
	registerResolver(
		resolveBedrockModelServes,
		EdgeDecl{TypeBedrockCustomModelDeployment, TypeBedrockCustomModel, store.RelUses},
		EdgeDecl{TypeBedrockProvisionedModel, TypeBedrockCustomModel, store.RelUses},
	)
}

// resolveBedrockModelServes wires custom-model-deployment → custom-model and
// provisioned-model → custom-model, both via the live ModelArn (the model
// currently associated; ProvisionedModelSummary.DesiredModelArn is the requested
// target mid-update). Both are FK-safe: a deployment/throughput serving a base
// foundation model (not a scanned custom model) emits no edge. Foundation-model
// targets aren't scanned, so those refs are intentionally left unwired.
func resolveBedrockModelServes(acct *account, st *store.Store) error {
	customModels, err := scannedIDSet(acct, st, TypeBedrockCustomModel)
	if err != nil {
		return err
	}
	if len(customModels) == 0 {
		return nil
	}

	link := func(rtype, field string) error {
		rows, lerr := st.ListResources(store.ResourceFilter{
			Providers: []string{"aws"}, AccountID: acct.ID,
			Types: []string{rtype}, Limit: util.AllResources,
		})
		if lerr != nil {
			return lerr
		}
		for _, r := range rows {
			attrs := map[string]json.RawMessage{}
			if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
				continue
			}
			var modelArn string
			if raw, ok := attrs[field]; ok {
				_ = json.Unmarshal(raw, &modelArn)
			}
			if modelArn == "" {
				continue
			}
			tgt := store.ResourceID("aws", acct.ID, TypeBedrockCustomModel, modelArn)
			if !customModels[tgt] {
				continue
			}
			if err := st.UpsertRelationship(r.ID, tgt, store.RelUses, "directed", nil); err != nil {
				return fmt.Errorf("upsert %s→custom-model: %w", rtype, err)
			}
		}
		return nil
	}

	if err := link(TypeBedrockCustomModelDeployment, "ModelArn"); err != nil {
		return err
	}
	return link(TypeBedrockProvisionedModel, "ModelArn")
}

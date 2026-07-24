package aws

import (
	"encoding/json"
	"fmt"

	"github.com/icearp/disco-cli/internal/util"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerResolver(
		resolveBedrockModelServes,
		EdgeDecl{TypeBedrockCustomModelDeployment, TypeBedrockCustomModel, store.RelUses},
		EdgeDecl{TypeBedrockProvisionedModel, TypeBedrockCustomModel, store.RelUses},
	)
}

// resolveBedrockModelServes wires custom-model-deployment → custom-model and
// provisioned-model → custom-model via the live ModelArn (currently-associated
// model; ProvisionedModelSummary.DesiredModelArn is the requested target
// mid-update, not used). FK-safe: refs to an unscanned base foundation model
// emit no edge.
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
			tgt := store.ResourceID("aws", acct.ID, modelArn)
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

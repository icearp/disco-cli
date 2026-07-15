package aws

import (
	"encoding/json"
	"fmt"
	"strings"

	"codeberg.org/icearp/disco/internal/util"
	"codeberg.org/icearp/disco/store"
)

func init() {
	registerResolver(
		resolveARCRegionSwitchRelationships,
		EdgeDecl{TypeARCRegionSwitchPlan, TypeIAMRole, store.RelAssumes},
	)
}

// resolveARCRegionSwitchRelationships wires each plan to its execution role
// (AbbreviatedPlan.ExecutionRole). Workflow.Step targets (Lambda, ASG, ECS,
// Route 53 health check) live on GetPlan body — deferred enrichment.
func resolveARCRegionSwitchRelationships(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeARCRegionSwitchPlan}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	roleSet, err := scannedIDSet(acct, st, TypeIAMRole)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			ExecutionRole *string `json:"ExecutionRole"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		rarn := sv(attrs.ExecutionRole)
		if !strings.Contains(rarn, ":role/") {
			continue
		}
		tgt := store.ResourceID("aws", acct.ID, rarn)
		if !roleSet[tgt] {
			continue
		}
		if err := st.UpsertRelationship(r.ID, tgt, store.RelAssumes, "directed", nil); err != nil {
			return fmt.Errorf("upsert arc-plan→role: %w", err)
		}
	}
	return nil
}

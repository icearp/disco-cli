package aws

import (
	"encoding/json"
	"fmt"

	"codeberg.org/icearp/disco/internal/store"
	"codeberg.org/icearp/disco/internal/util"
)

func init() {
	registerResolver(resolveOmicsWorkflowVersionParent,
		EdgeDecl{TypeOmicsWorkflowVersion, TypeOmicsWorkflow, store.RelAttachedTo},
	)
}

// resolveOmicsWorkflowVersionParent wires each workflow-version to its
// parent workflow via `WorkflowId` bare-ID lookup against an Id index
// built from scanned workflows.
func resolveOmicsWorkflowVersionParent(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeOmicsWorkflowVersion},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	idx, err := omicsWorkflowIDIndex(acct, st)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			WorkflowId *string `json:"WorkflowId"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		wid := sv(attrs.WorkflowId)
		if wid == "" {
			continue
		}
		tgtID, ok := idx[wid]
		if !ok {
			continue
		}
		if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
			return fmt.Errorf("upsert omics workflow-version→workflow: %w", err)
		}
	}
	return nil
}

func omicsWorkflowIDIndex(acct *account, st *store.Store) (map[string]string, error) {
	rows, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeOmicsWorkflow},
		Limit: util.AllResources,
	})
	if err != nil {
		return nil, err
	}
	idx := make(map[string]string, len(rows))
	for _, r := range rows {
		var attrs struct {
			Id *string `json:"Id"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if id := sv(attrs.Id); id != "" {
			idx[id] = r.ID
		}
	}
	return idx, nil
}

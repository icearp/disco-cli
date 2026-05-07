package aws

import (
	"encoding/json"
	"fmt"

	"codeberg.org/icearp/disco/internal/store"
	"codeberg.org/icearp/disco/internal/util"
)

func init() {
	registerResolver(
		resolveOmicsWorkflowVersionParent,
		EdgeDecl{TypeOmicsWorkflowVersion, TypeOmicsWorkflow, store.RelAttachedTo},
	)
	registerResolver(
		resolveOmicsStoreKMS,
		EdgeDecl{TypeOmicsAnnotationStore, TypeKMSKey, store.RelUses},
		EdgeDecl{TypeOmicsVariantStore, TypeKMSKey, store.RelUses},
		EdgeDecl{TypeOmicsReferenceStore, TypeKMSKey, store.RelUses},
		EdgeDecl{TypeOmicsSequenceStore, TypeKMSKey, store.RelUses},
	)
}

// resolveOmicsWorkflowVersionParent wires each workflow-version to its
// parent workflow via `WorkflowID` bare-ID lookup against an ID index
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
			WorkflowID *string `json:"WorkflowId"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		wid := sv(attrs.WorkflowID)
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
			ID *string `json:"Id"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if id := sv(attrs.ID); id != "" {
			idx[id] = r.ID
		}
	}
	return idx, nil
}

// resolveOmicsStoreKMS wires every omics store row (annotation/variant/
// reference/sequence) to the KMS key in SseConfig.KeyArn. Single resolver fans
// over the four types so the KMS index is loaded once per account.
func resolveOmicsStoreKMS(acct *account, st *store.Store) error {
	storeTypes := []string{
		TypeOmicsAnnotationStore,
		TypeOmicsVariantStore,
		TypeOmicsReferenceStore,
		TypeOmicsSequenceStore,
	}
	rows, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: storeTypes, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	idx, err := loadKMSResolveIndex(acct, st)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			SseConfig *struct {
				KeyArn *string `json:"KeyArn"`
			} `json:"SseConfig"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if attrs.SseConfig == nil {
			continue
		}
		ref := sv(attrs.SseConfig.KeyArn)
		if ref == "" {
			continue
		}
		id, ok := idx.resolveKMSKeyID(ref, sv(r.Region), acct.ID)
		if !ok {
			continue
		}
		if err := st.UpsertRelationship(r.ID, id, store.RelUses, "directed", nil); err != nil {
			return fmt.Errorf("upsert omics store→kms: %w", err)
		}
	}
	return nil
}

package aws

import (
	"encoding/json"
	"fmt"

	"codeberg.org/icearp/disco/internal/util"
	"codeberg.org/icearp/disco/store"
)

func init() {
	registerResolver(
		resolveOmicsWorkflowVersionParent,
		EdgeDecl{TypeOmicsWorkflowVersion, TypeOmicsWorkflow, store.RelAttachedTo},
	)
	registerResolver(
		resolveOmicsAnnotationStoreVersionParent,
		EdgeDecl{TypeOmicsAnnotationStoreVersion, TypeOmicsAnnotationStore, store.RelAttachedTo},
	)
	registerResolver(
		resolveOmicsReferenceParent,
		EdgeDecl{TypeOmicsReference, TypeOmicsReferenceStore, store.RelAttachedTo},
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
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeOmicsWorkflowVersion},
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
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeOmicsWorkflow},
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

// resolveOmicsAnnotationStoreVersionParent wires each annotation-store version
// to its parent annotation store via the StoreId attribute, looked up against an
// id index built from scanned annotation stores.
func resolveOmicsAnnotationStoreVersionParent(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeOmicsAnnotationStoreVersion},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	idx, err := omicsStoreIDIndex(acct, st, TypeOmicsAnnotationStore)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			StoreID *string `json:"StoreId"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		sid := sv(attrs.StoreID)
		if sid == "" {
			continue
		}
		tgtID, ok := idx[sid]
		if !ok {
			continue
		}
		if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
			return fmt.Errorf("upsert omics annotation-store-version→annotation-store: %w", err)
		}
	}
	return nil
}

// resolveOmicsReferenceParent wires each reference to its parent reference store
// via the ReferenceStoreId attribute, looked up against an id index built from
// scanned reference stores.
func resolveOmicsReferenceParent(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeOmicsReference},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	idx, err := omicsStoreIDIndex(acct, st, TypeOmicsReferenceStore)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			ReferenceStoreID *string `json:"ReferenceStoreId"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		sid := sv(attrs.ReferenceStoreID)
		if sid == "" {
			continue
		}
		tgtID, ok := idx[sid]
		if !ok {
			continue
		}
		if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
			return fmt.Errorf("upsert omics reference→reference-store: %w", err)
		}
	}
	return nil
}

// omicsStoreIDIndex maps each scanned store row's "Id" attribute to its
// resource ID, for parent lookups keyed on the raw store id.
func omicsStoreIDIndex(acct *account, st *store.Store, rtype string) (map[string]string, error) {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{rtype},
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
		Providers: []string{"aws"}, AccountID: acct.ID, Types: storeTypes, Limit: util.AllResources,
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

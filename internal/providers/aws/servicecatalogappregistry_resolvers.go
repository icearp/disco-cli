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
		resolveSCARAttributeGroupAssocRefs,
		EdgeDecl{TypeSCARAttributeGroupAssociation, TypeSCARApplication, store.RelAttachedTo},
		EdgeDecl{TypeSCARAttributeGroupAssociation, TypeSCARAttributeGroup, store.RelAttachedTo},
	)
	registerResolver(
		resolveSCARResourceAssocApplication,
		EdgeDecl{TypeSCARResourceAssociation, TypeSCARApplication, store.RelAttachedTo},
	)
}

// resolveSCARAttributeGroupAssocRefs wires each ag-association to the
// application (NativeID parent extract via /attribute-group-association/)
// and to the attribute-group (Id field → AG ARN by name index since list
// summary uses Id without ARN).
func resolveSCARAttributeGroupAssocRefs(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeSCARAttributeGroupAssociation}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	appSet, err := scannedIDSet(acct, st, TypeSCARApplication)
	if err != nil {
		return err
	}
	// Build attribute-group index keyed by Id field — AG ARN shape varies
	// across SDK versions but `Id` is stable; attrs JSON of each AG carries
	// `Id` from list summary.
	agRows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeSCARAttributeGroup}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	agByID := map[string]string{}
	for _, ag := range agRows {
		var a struct {
			ID *string `json:"Id"`
		}
		if err := json.Unmarshal([]byte(ag.AttributesJSON), &a); err != nil {
			continue
		}
		if id := sv(a.ID); id != "" {
			agByID[id] = ag.ID
		}
	}
	for _, r := range rows {
		i := strings.Index(r.NativeID, "/attribute-group-association/")
		if i >= 0 {
			parent := r.NativeID[:i]
			tgt := store.ResourceID("aws", acct.ID, parent)
			if appSet[tgt] {
				if err := st.UpsertRelationship(r.ID, tgt, store.RelAttachedTo, "directed", nil); err != nil {
					return fmt.Errorf("upsert appregistry ag-assoc→application: %w", err)
				}
			}
		}
		var attrs struct {
			ID *string `json:"Id"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if agID := sv(attrs.ID); agID != "" {
			if rid, ok := agByID[agID]; ok {
				if err := st.UpsertRelationship(r.ID, rid, store.RelAttachedTo, "directed", nil); err != nil {
					return fmt.Errorf("upsert appregistry ag-assoc→attribute-group: %w", err)
				}
			}
		}
	}
	return nil
}

func resolveSCARResourceAssocApplication(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeSCARResourceAssociation}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	appSet, err := scannedIDSet(acct, st, TypeSCARApplication)
	if err != nil {
		return err
	}
	for _, r := range rows {
		i := strings.Index(r.NativeID, "/resource-association/")
		if i < 0 {
			continue
		}
		parent := r.NativeID[:i]
		tgt := store.ResourceID("aws", acct.ID, parent)
		if !appSet[tgt] {
			continue
		}
		if err := st.UpsertRelationship(r.ID, tgt, store.RelAttachedTo, "directed", nil); err != nil {
			return fmt.Errorf("upsert appregistry resource-assoc→application: %w", err)
		}
	}
	return nil
}

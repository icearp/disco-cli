package aws

import (
	"encoding/json"
	"fmt"

	"codeberg.org/icearp/disco/internal/util"
	"codeberg.org/icearp/disco/store"
)

func init() {
	registerResolver(
		resolveCleanRoomsMembershipCollaboration,
		EdgeDecl{TypeCleanRoomsMembership, TypeCleanRoomsCollaboration, store.RelAttachedTo},
	)
	registerResolver(
		resolveCleanRoomsChildToMembership,
		EdgeDecl{TypeCleanRoomsAnalysisTemplate, TypeCleanRoomsMembership, store.RelAttachedTo},
		EdgeDecl{TypeCleanRoomsAnalysisTemplate, TypeCleanRoomsCollaboration, store.RelAttachedTo},
		EdgeDecl{TypeCleanRoomsConfiguredTableAssociation, TypeCleanRoomsMembership, store.RelAttachedTo},
		EdgeDecl{TypeCleanRoomsConfiguredTableAssociation, TypeCleanRoomsCollaboration, store.RelAttachedTo},
		EdgeDecl{TypeCleanRoomsConfiguredAudienceModelAssociation, TypeCleanRoomsMembership, store.RelAttachedTo},
		EdgeDecl{TypeCleanRoomsConfiguredAudienceModelAssociation, TypeCleanRoomsCollaboration, store.RelAttachedTo},
		EdgeDecl{TypeCleanRoomsIDMappingTable, TypeCleanRoomsMembership, store.RelAttachedTo},
		EdgeDecl{TypeCleanRoomsIDMappingTable, TypeCleanRoomsCollaboration, store.RelAttachedTo},
		EdgeDecl{TypeCleanRoomsIDNamespaceAssociation, TypeCleanRoomsMembership, store.RelAttachedTo},
		EdgeDecl{TypeCleanRoomsIDNamespaceAssociation, TypeCleanRoomsCollaboration, store.RelAttachedTo},
		EdgeDecl{TypeCleanRoomsPrivacyBudgetTemplate, TypeCleanRoomsMembership, store.RelAttachedTo},
		EdgeDecl{TypeCleanRoomsPrivacyBudgetTemplate, TypeCleanRoomsCollaboration, store.RelAttachedTo},
	)
	registerResolver(
		resolveCleanRoomsConfiguredTableAssocToTable,
		EdgeDecl{TypeCleanRoomsConfiguredTableAssociation, TypeCleanRoomsConfiguredTable, store.RelAttachedTo},
	)
	registerResolver(
		resolveCleanRoomsConfiguredAudienceModelAssocToModel,
		EdgeDecl{TypeCleanRoomsConfiguredAudienceModelAssociation, TypeCleanRoomsMLConfiguredAudienceModel, store.RelUses},
	)
}

// resolveCleanRoomsMembershipCollaboration links each membership to its
// collaboration via `CollaborationArn`.
func resolveCleanRoomsMembershipCollaboration(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeCleanRoomsMembership}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	colSet, err := scannedIDSet(acct, st, TypeCleanRoomsCollaboration)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			CollaborationArn *string `json:"CollaborationArn"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		arn := sv(attrs.CollaborationArn)
		if arn == "" {
			continue
		}
		tgtID := store.ResourceID("aws", acct.ID, TypeCleanRoomsCollaboration, arn)
		if !colSet[tgtID] {
			continue
		}
		if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
			return fmt.Errorf("upsert cr membership→collab: %w", err)
		}
	}
	return nil
}

// resolveCleanRoomsChildToMembership wires each per-membership child
// (analysis-template, configured-table-association, id-mapping-table,
// id-namespace-association, privacy-budget-template) to its parent membership
// and collaboration via the summary types' MembershipArn / CollaborationArn
// fields.
func resolveCleanRoomsChildToMembership(acct *account, st *store.Store) error {
	memSet, err := scannedIDSet(acct, st, TypeCleanRoomsMembership)
	if err != nil {
		return err
	}
	colSet, err := scannedIDSet(acct, st, TypeCleanRoomsCollaboration)
	if err != nil {
		return err
	}
	childTypes := []string{
		TypeCleanRoomsAnalysisTemplate,
		TypeCleanRoomsConfiguredTableAssociation,
		TypeCleanRoomsConfiguredAudienceModelAssociation,
		TypeCleanRoomsIDMappingTable,
		TypeCleanRoomsIDNamespaceAssociation,
		TypeCleanRoomsPrivacyBudgetTemplate,
	}
	for _, ctype := range childTypes {
		rows, err := st.ListResources(store.ResourceFilter{
			Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{ctype}, Limit: util.AllResources,
		})
		if err != nil {
			return err
		}
		for _, r := range rows {
			var attrs struct {
				MembershipArn    *string `json:"MembershipArn"`
				CollaborationArn *string `json:"CollaborationArn"`
			}
			if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
				continue
			}
			if m := sv(attrs.MembershipArn); m != "" {
				tgtID := store.ResourceID("aws", acct.ID, TypeCleanRoomsMembership, m)
				if memSet[tgtID] {
					if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
						return fmt.Errorf("upsert cr %s→membership: %w", ctype, err)
					}
				}
			}
			if c := sv(attrs.CollaborationArn); c != "" {
				tgtID := store.ResourceID("aws", acct.ID, TypeCleanRoomsCollaboration, c)
				if colSet[tgtID] {
					if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
						return fmt.Errorf("upsert cr %s→collab: %w", ctype, err)
					}
				}
			}
		}
	}
	return nil
}

// resolveCleanRoomsConfiguredTableAssocToTable wires each configured-table-
// association to its underlying configured-table via `ConfiguredTableArn`.
func resolveCleanRoomsConfiguredTableAssocToTable(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeCleanRoomsConfiguredTableAssociation},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	tblSet, err := scannedIDSet(acct, st, TypeCleanRoomsConfiguredTable)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			ConfiguredTableArn *string `json:"ConfiguredTableArn"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		arn := sv(attrs.ConfiguredTableArn)
		if arn == "" {
			continue
		}
		tgtID := store.ResourceID("aws", acct.ID, TypeCleanRoomsConfiguredTable, arn)
		if !tblSet[tgtID] {
			continue
		}
		if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
			return fmt.Errorf("upsert cr cta→table: %w", err)
		}
	}
	return nil
}

// resolveCleanRoomsConfiguredAudienceModelAssocToModel wires each configured-
// audience-model-association to the cleanrooms-ml configured-audience-model it
// references via `ConfiguredAudienceModelArn` (cross-service, FK-safe).
func resolveCleanRoomsConfiguredAudienceModelAssocToModel(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeCleanRoomsConfiguredAudienceModelAssociation},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	modelSet, err := scannedIDSet(acct, st, TypeCleanRoomsMLConfiguredAudienceModel)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			ConfiguredAudienceModelArn *string `json:"ConfiguredAudienceModelArn"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		arn := sv(attrs.ConfiguredAudienceModelArn)
		if arn == "" {
			continue
		}
		tgtID := store.ResourceID("aws", acct.ID, TypeCleanRoomsMLConfiguredAudienceModel, arn)
		if !modelSet[tgtID] {
			continue
		}
		if err := st.UpsertRelationship(r.ID, tgtID, store.RelUses, "directed", nil); err != nil {
			return fmt.Errorf("upsert cr camassoc→cam: %w", err)
		}
	}
	return nil
}

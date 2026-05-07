package aws

import (
	"encoding/json"
	"fmt"

	"codeberg.org/icearp/disco/internal/store"
	"codeberg.org/icearp/disco/internal/util"
)

func init() {
	registerResolver(
		resolveDetectiveMemberOrgAccount,
		EdgeDecl{TypeDetectiveMember, TypeOrganizationsAccount, store.RelAttachedTo},
	)
	registerResolver(
		resolveDetectiveOrgAdminRefs,
		EdgeDecl{TypeDetectiveOrganizationAdmin, TypeDetectiveGraph, store.RelAttachedTo},
		EdgeDecl{TypeDetectiveOrganizationAdmin, TypeOrganizationsAccount, store.RelAttachedTo},
	)
}

// resolveDetectiveOrgAdminRefs wires each delegated-administrator row to its
// behavior graph (GraphArn) and to the Organizations account row when the org
// tree is scanned.
func resolveDetectiveOrgAdminRefs(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeDetectiveOrganizationAdmin}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	graphSet, err := scannedIDSet(acct, st, TypeDetectiveGraph)
	if err != nil {
		return err
	}
	orgArnByID, _, err := loadOrgTargetIndex(acct, st)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			AccountID *string `json:"AccountId"`
			GraphArn  *string `json:"GraphArn"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if g := sv(attrs.GraphArn); g != "" {
			tgtID := store.ResourceID("aws", acct.ID, TypeDetectiveGraph, g)
			if graphSet[tgtID] {
				if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
					return fmt.Errorf("upsert detective oa→graph: %w", err)
				}
			}
		}
		if a := sv(attrs.AccountID); a != "" && len(orgArnByID) > 0 {
			if orgARN, ok := orgArnByID[a]; ok {
				orgID := store.ResourceID("aws", acct.ID, TypeOrganizationsAccount, orgARN)
				if err := st.UpsertRelationship(r.ID, orgID, store.RelAttachedTo, "directed", nil); err != nil {
					return fmt.Errorf("upsert detective oa→org account: %w", err)
				}
			}
		}
	}
	return nil
}

// detectiveMemberAttrs mirrors the verbatim MemberDetail fields used by the
// resolver. PascalCase tags match mustJSON of the AWS SDK v2 struct.
type detectiveMemberAttrs struct {
	AccountID *string `json:"AccountID"`
}

// resolveDetectiveMemberOrgAccount emits an `attached-to` edge from each
// Detective member row to its corresponding AWS Organizations account, when
// the org tree is also scanned. FK-safe via loadOrgTargetIndex; partial-
// coverage scans (no Org tree) skip silently. Precedent: SSO assignment →
// org account in resolveSSOAccountAssignments.
func resolveDetectiveMemberOrgAccount(acct *account, st *store.Store) error {
	members, err := st.ListResources(store.ResourceFilter{
		Provider:  "aws",
		AccountID: acct.ID,
		Types:     []string{TypeDetectiveMember},
		Limit:     util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(members) == 0 {
		return nil
	}

	orgArnByID, _, err := loadOrgTargetIndex(acct, st)
	if err != nil {
		return err
	}
	if len(orgArnByID) == 0 {
		return nil
	}

	for _, m := range members {
		var attrs detectiveMemberAttrs
		if err := json.Unmarshal([]byte(m.AttributesJSON), &attrs); err != nil {
			continue
		}
		accountID := sv(attrs.AccountID)
		if accountID == "" {
			continue
		}
		orgARN, ok := orgArnByID[accountID]
		if !ok {
			continue
		}
		orgID := store.ResourceID("aws", acct.ID, TypeOrganizationsAccount, orgARN)
		if err := st.UpsertRelationship(m.ID, orgID, store.RelAttachedTo, "directed", nil); err != nil {
			return fmt.Errorf("upsert detective member→org account: %w", err)
		}
	}
	return nil
}

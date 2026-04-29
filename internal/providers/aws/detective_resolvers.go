package aws

import (
	"encoding/json"
	"fmt"

	"codeberg.org/icearp/disco/internal/store"
	"codeberg.org/icearp/disco/internal/util"
)

func init() {
	registerResolver(resolveDetectiveMemberOrgAccount)
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

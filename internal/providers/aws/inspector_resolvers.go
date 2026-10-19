package aws

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/icearp/disco-cli/internal/util"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerResolver(
		resolveInspector2MemberOrgAccount,
		EdgeDecl{TypeInspector2Member, TypeOrganizationsAccount, store.RelAttachedTo},
	)
	registerResolver(
		resolveInspector2DelegatedAdminOrgAccount,
		EdgeDecl{TypeInspector2DelegatedAdminAccount, TypeOrganizationsAccount, store.RelAttachedTo},
	)
}

// resolveInspector2MemberOrgAccount emits an `attached-to` edge from each
// Inspector v2 member row to its corresponding AWS Organizations account.
// Mirrors the Detective + SSO assignment → org-account precedent.
func resolveInspector2MemberOrgAccount(acct *account, st *store.Store) error {
	return resolveInspector2OrgAccountEdges(acct, st, TypeInspector2Member, "member")
}

// resolveInspector2DelegatedAdminOrgAccount emits an `attached-to` edge from
// each Inspector v2 delegated-administrator row to the Organizations account
// it names.
func resolveInspector2DelegatedAdminOrgAccount(acct *account, st *store.Store) error {
	return resolveInspector2OrgAccountEdges(acct, st, TypeInspector2DelegatedAdminAccount, "delegated admin")
}

// resolveInspector2OrgAccountEdges links each srcType row to the
// Organizations account its verbatim `AccountId` attribute names (the Member
// and DelegatedAdminAccount SDK elements both carry it), when the org tree is
// also scanned; partial-coverage scans (no org tree) emit nothing. A row whose
// attributes do not decode is reported in the joined error without costing
// other rows' edges.
func resolveInspector2OrgAccountEdges(acct *account, st *store.Store, srcType, label string) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"},
		AccountID: acct.ID,
		Types:     []string{srcType},
		Limit:     util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	orgArnByID, _, err := loadOrgTargetIndex(acct, st)
	if err != nil {
		return err
	}
	if len(orgArnByID) == 0 {
		return nil
	}

	var decodeErrs []error
	for _, r := range rows {
		var attrs struct {
			AccountID *string `json:"AccountId"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			decodeErrs = append(decodeErrs, fmt.Errorf("decode inspector2 %s %s: %w", label, r.NativeID, err))
			continue
		}
		orgARN, ok := orgArnByID[sv(attrs.AccountID)]
		if !ok {
			continue
		}
		orgID := store.ResourceID("aws", acct.ID, orgARN)
		if err := st.UpsertRelationship(r.ID, orgID, store.RelAttachedTo, "directed", nil); err != nil {
			return fmt.Errorf("upsert inspector2 %s→org account: %w", label, err)
		}
	}
	return errors.Join(decodeErrs...)
}

package aws

import (
	"encoding/json"
	"fmt"
	"strings"

	"codeberg.org/icearp/disco/internal/store"
	"codeberg.org/icearp/disco/internal/util"
)

func init() {
	registerResolver(resolveSSOPermissionSetInstance)
	registerResolver(resolveSSOAccountAssignments)
}

// ssoInstanceIndex pre-loads scanned instances keyed by InstanceArn so the
// permission-set → instance edge resolves without re-querying per row.
type ssoInstanceIndex struct {
	idByArn         map[string]string
	identityStoreID map[string]string // InstanceArn → IdentityStoreID
	ownerAcct       map[string]string // InstanceArn → OwnerAccountID
}

func loadSSOInstanceIndex(acct *account, st *store.Store) (*ssoInstanceIndex, error) {
	rows, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID,
		Types: []string{TypeSSOInstance},
		Limit: util.AllResources,
	})
	if err != nil {
		return nil, err
	}
	idx := &ssoInstanceIndex{
		idByArn:         make(map[string]string, len(rows)),
		identityStoreID: make(map[string]string, len(rows)),
		ownerAcct:       make(map[string]string, len(rows)),
	}
	for _, r := range rows {
		idx.idByArn[r.NativeID] = r.ID
		var meta struct {
			IdentityStoreID *string `json:"IdentityStoreID"`
			OwnerAccountID  *string `json:"OwnerAccountID"`
		}
		_ = json.Unmarshal([]byte(r.AttributesJSON), &meta)
		idx.identityStoreID[r.NativeID] = sv(meta.IdentityStoreID)
		idx.ownerAcct[r.NativeID] = sv(meta.OwnerAccountID)
	}
	return idx, nil
}

// permissionSetArn → instanceArn — encoded inside the permission-set ARN
// shape `arn:aws:sso:::permissionSet/{ssoins-id}/{ps-id}`. The instance
// segment maps back to the canonical instance ARN
// `arn:aws:sso:::instance/{ssoins-id}`.
func instanceArnFromPermissionSetArn(psArn string) string {
	_, tail, ok := strings.Cut(psArn, ":permissionSet/")
	if !ok {
		return ""
	}
	insID, _, ok := strings.Cut(tail, "/")
	if !ok {
		return ""
	}
	return "arn:aws:sso:::instance/" + insID
}

// resolveSSOPermissionSetInstance emits a `contains` edge from each
// SSO instance to every permission-set provisioned under it.
func resolveSSOPermissionSetInstance(acct *account, st *store.Store) error {
	pSets, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID,
		Types: []string{TypeSSOPermissionSet},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(pSets) == 0 {
		return nil
	}
	idx, err := loadSSOInstanceIndex(acct, st)
	if err != nil {
		return err
	}
	for _, ps := range pSets {
		insArn := instanceArnFromPermissionSetArn(ps.NativeID)
		if insArn == "" {
			continue
		}
		insID, ok := idx.idByArn[insArn]
		if !ok {
			continue
		}
		if err := st.UpsertRelationship(insID, ps.ID, store.RelContains, "directed", nil); err != nil {
			return fmt.Errorf("upsert sso instance→permission-set: %w", err)
		}
	}
	return nil
}

// ssoAssignmentAttrs mirrors the AccountAssignment fields used by the
// resolver — the rest stays in raw attrs JSON for query consumers.
type ssoAssignmentAttrs struct {
	AccountID        *string `json:"AccountID"`
	PermissionSetArn *string `json:"PermissionSetArn"`
	PrincipalID      *string `json:"PrincipalID"`
	PrincipalType    string  `json:"PrincipalType"`
}

// resolveSSOAccountAssignments emits the three target edges per assignment:
//   - assignment → permission-set (uses)
//   - assignment → identity-store user/group (uses), FK-safe
//   - assignment → AWS Organizations account (attached-to), FK-safe via
//     loadOrgTargetIndex (only emits when the org tree is also scanned)
//
// Skips edges to targets not in the store rather than failing FK — a scan
// scoped to one part of the org (no Identity Store creds, or no Org scan)
// still produces consistent partial graph coverage.
func resolveSSOAccountAssignments(acct *account, st *store.Store) error {
	assigns, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID,
		Types: []string{TypeSSOAccountAssignment},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(assigns) == 0 {
		return nil
	}

	insIdx, err := loadSSOInstanceIndex(acct, st)
	if err != nil {
		return err
	}

	psIDs, err := resourceIDSet(st, acct.ID, TypeSSOPermissionSet)
	if err != nil {
		return err
	}
	userIDs, err := resourceIDSet(st, acct.ID, TypeIdentityStoreUser)
	if err != nil {
		return err
	}
	groupIDs, err := resourceIDSet(st, acct.ID, TypeIdentityStoreGroup)
	if err != nil {
		return err
	}
	orgArnByID, _, err := loadOrgTargetIndex(acct, st)
	if err != nil {
		return err
	}

	for _, a := range assigns {
		var attrs ssoAssignmentAttrs
		if err := json.Unmarshal([]byte(a.AttributesJSON), &attrs); err != nil {
			continue
		}
		psArn := sv(attrs.PermissionSetArn)
		if psArn != "" {
			psID := store.ResourceID("aws", acct.ID, TypeSSOPermissionSet, psArn)
			if _, ok := psIDs[psID]; ok {
				if err := st.UpsertRelationship(a.ID, psID, store.RelUses, "directed", nil); err != nil {
					return fmt.Errorf("upsert sso assignment→permission-set: %w", err)
				}
			}
		}

		insArn := instanceArnFromPermissionSetArn(psArn)
		identityStoreID := insIdx.identityStoreID[insArn]
		ownerAcct := insIdx.ownerAcct[insArn]
		if ownerAcct == "" {
			ownerAcct = acct.ID
		}
		principalID := sv(attrs.PrincipalID)
		if identityStoreID != "" && principalID != "" {
			switch attrs.PrincipalType {
			case "USER":
				uID := store.ResourceID("aws", acct.ID, TypeIdentityStoreUser, identityStoreUserNativeID(ownerAcct, identityStoreID, principalID))
				if _, ok := userIDs[uID]; ok {
					if err := st.UpsertRelationship(a.ID, uID, store.RelUses, "directed", nil); err != nil {
						return fmt.Errorf("upsert sso assignment→identitystore user: %w", err)
					}
				}
			case "GROUP":
				gID := store.ResourceID("aws", acct.ID, TypeIdentityStoreGroup, identityStoreGroupNativeID(ownerAcct, identityStoreID, principalID))
				if _, ok := groupIDs[gID]; ok {
					if err := st.UpsertRelationship(a.ID, gID, store.RelUses, "directed", nil); err != nil {
						return fmt.Errorf("upsert sso assignment→identitystore group: %w", err)
					}
				}
			}
		}

		if accountID := sv(attrs.AccountID); accountID != "" {
			if orgARN, ok := orgArnByID[accountID]; ok {
				orgID := store.ResourceID("aws", acct.ID, TypeOrganizationsAccount, orgARN)
				if err := st.UpsertRelationship(a.ID, orgID, store.RelAttachedTo, "directed", nil); err != nil {
					return fmt.Errorf("upsert sso assignment→org account: %w", err)
				}
			}
		}
	}
	return nil
}

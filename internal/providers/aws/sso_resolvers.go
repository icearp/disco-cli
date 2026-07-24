package aws

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/icearp/disco-cli/internal/util"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerResolver(
		resolveSSOPermissionSetInstance,
		EdgeDecl{TypeSSOInstance, TypeSSOPermissionSet, store.RelContains},
	)
	registerResolver(
		resolveSSOAccountAssignments,
		EdgeDecl{TypeSSOAccountAssignment, TypeSSOPermissionSet, store.RelUses},
		EdgeDecl{TypeSSOAccountAssignment, TypeIdentityStoreUser, store.RelUses},
		EdgeDecl{TypeSSOAccountAssignment, TypeIdentityStoreGroup, store.RelUses},
		EdgeDecl{TypeSSOAccountAssignment, TypeOrganizationsAccount, store.RelAttachedTo},
	)
	registerResolver(
		resolveSSOApplicationInstance,
		EdgeDecl{TypeSSOApplication, TypeSSOInstance, store.RelAttachedTo},
	)
	registerResolver(
		resolveSSOApplicationAssignmentRefs,
		EdgeDecl{TypeSSOApplicationAssignment, TypeSSOApplication, store.RelAttachedTo},
		EdgeDecl{TypeSSOApplicationAssignment, TypeIdentityStoreUser, store.RelUses},
		EdgeDecl{TypeSSOApplicationAssignment, TypeIdentityStoreGroup, store.RelUses},
	)
	registerResolver(
		resolveSSOAttrConfigInstance,
		EdgeDecl{TypeSSOInstanceAccessControlAttributeConfiguration, TypeSSOInstance, store.RelAttachedTo},
	)
	registerResolver(
		resolveSSOTrustedTokenIssuerInstance,
		EdgeDecl{TypeSSOTrustedTokenIssuer, TypeSSOInstance, store.RelAttachedTo},
	)
}

// ssoInstanceIndex pre-loads scanned instances keyed by InstanceArn so
// permission-set → instance edges resolve without a per-row query.
type ssoInstanceIndex struct {
	idByArn         map[string]string
	identityStoreID map[string]string // InstanceArn → IdentityStoreID
	ownerAcct       map[string]string // InstanceArn → OwnerAccountID
}

func loadSSOInstanceIndex(acct *account, st *store.Store) (*ssoInstanceIndex, error) {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID,
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

// permissionSetArn → instanceArn: the instance id is encoded in the
// permission-set ARN shape `arn:aws:sso:::permissionSet/{ssoins-id}/{ps-id}`;
// maps back to the canonical instance ARN `arn:aws:sso:::instance/{ssoins-id}`.
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
		Providers: []string{"aws"}, AccountID: acct.ID,
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
		Providers: []string{"aws"}, AccountID: acct.ID,
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
			psID := store.ResourceID("aws", acct.ID, psArn)
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
				uID := store.ResourceID("aws", acct.ID, identityStoreUserNativeID(ownerAcct, identityStoreID, principalID))
				if _, ok := userIDs[uID]; ok {
					if err := st.UpsertRelationship(a.ID, uID, store.RelUses, "directed", nil); err != nil {
						return fmt.Errorf("upsert sso assignment→identitystore user: %w", err)
					}
				}
			case "GROUP":
				gID := store.ResourceID("aws", acct.ID, identityStoreGroupNativeID(ownerAcct, identityStoreID, principalID))
				if _, ok := groupIDs[gID]; ok {
					if err := st.UpsertRelationship(a.ID, gID, store.RelUses, "directed", nil); err != nil {
						return fmt.Errorf("upsert sso assignment→identitystore group: %w", err)
					}
				}
			}
		}

		if accountID := sv(attrs.AccountID); accountID != "" {
			if orgARN, ok := orgArnByID[accountID]; ok {
				orgID := store.ResourceID("aws", acct.ID, orgARN)
				if err := st.UpsertRelationship(a.ID, orgID, store.RelAttachedTo, "directed", nil); err != nil {
					return fmt.Errorf("upsert sso assignment→org account: %w", err)
				}
			}
		}
	}
	return nil
}

// resolveSSOApplicationInstance links each SSO application to the Identity
// Center instance it is registered with via InstanceArn in the attrs.
func resolveSSOApplicationInstance(acct *account, st *store.Store) error {
	apps, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID,
		Types: []string{TypeSSOApplication},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(apps) == 0 {
		return nil
	}
	idx, err := loadSSOInstanceIndex(acct, st)
	if err != nil {
		return err
	}
	for _, a := range apps {
		var attrs struct {
			InstanceArn *string `json:"InstanceArn"`
		}
		if err := json.Unmarshal([]byte(a.AttributesJSON), &attrs); err != nil {
			continue
		}
		insArn := sv(attrs.InstanceArn)
		insID, ok := idx.idByArn[insArn]
		if !ok {
			continue
		}
		if err := st.UpsertRelationship(a.ID, insID, store.RelAttachedTo, "directed", nil); err != nil {
			return fmt.Errorf("upsert sso application→instance: %w", err)
		}
	}
	return nil
}

// resolveSSOApplicationAssignmentRefs wires each application-assignment to
// its parent application (NativeID parent-extraction) and to the principal
// identity-store user/group via PrincipalId + the parent application's
// InstanceArn → identity-store ID lookup.
func resolveSSOApplicationAssignmentRefs(acct *account, st *store.Store) error {
	assigns, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID,
		Types: []string{TypeSSOApplicationAssignment},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(assigns) == 0 {
		return nil
	}
	apps, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID,
		Types: []string{TypeSSOApplication},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	appByARN := make(map[string]string, len(apps))
	appInstanceARN := make(map[string]string, len(apps))
	for _, a := range apps {
		appByARN[a.NativeID] = a.ID
		var meta struct {
			InstanceArn *string `json:"InstanceArn"`
		}
		_ = json.Unmarshal([]byte(a.AttributesJSON), &meta)
		appInstanceARN[a.NativeID] = sv(meta.InstanceArn)
	}
	idx, err := loadSSOInstanceIndex(acct, st)
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
	for _, a := range assigns {
		idxAssign := strings.Index(a.NativeID, "/assignment/")
		if idxAssign < 0 {
			continue
		}
		appARN := a.NativeID[:idxAssign]
		appID, ok := appByARN[appARN]
		if !ok {
			continue
		}
		if err := st.UpsertRelationship(a.ID, appID, store.RelAttachedTo, "directed", nil); err != nil {
			return fmt.Errorf("upsert sso app-assignment→application: %w", err)
		}
		var attrs struct {
			PrincipalID   *string `json:"PrincipalId"`
			PrincipalType string  `json:"PrincipalType"`
		}
		if err := json.Unmarshal([]byte(a.AttributesJSON), &attrs); err != nil {
			continue
		}
		pid := sv(attrs.PrincipalID)
		if pid == "" {
			continue
		}
		insArn := appInstanceARN[appARN]
		identityStoreID := idx.identityStoreID[insArn]
		ownerAcct := idx.ownerAcct[insArn]
		if ownerAcct == "" {
			ownerAcct = acct.ID
		}
		if identityStoreID == "" {
			continue
		}
		switch attrs.PrincipalType {
		case "USER":
			uID := store.ResourceID("aws", acct.ID, identityStoreUserNativeID(ownerAcct, identityStoreID, pid))
			if _, ok := userIDs[uID]; ok {
				if err := st.UpsertRelationship(a.ID, uID, store.RelUses, "directed", nil); err != nil {
					return fmt.Errorf("upsert sso app-assignment→identity-store user: %w", err)
				}
			}
		case "GROUP":
			gID := store.ResourceID("aws", acct.ID, identityStoreGroupNativeID(ownerAcct, identityStoreID, pid))
			if _, ok := groupIDs[gID]; ok {
				if err := st.UpsertRelationship(a.ID, gID, store.RelUses, "directed", nil); err != nil {
					return fmt.Errorf("upsert sso app-assignment→identity-store group: %w", err)
				}
			}
		}
	}
	return nil
}

// resolveSSOAttrConfigInstance links each per-instance access-control
// attribute config back to its instance. NativeID format is
// "{instanceArn}/access-control-attribute-configuration".
func resolveSSOAttrConfigInstance(acct *account, st *store.Store) error {
	cfgs, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID,
		Types: []string{TypeSSOInstanceAccessControlAttributeConfiguration},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(cfgs) == 0 {
		return nil
	}
	idx, err := loadSSOInstanceIndex(acct, st)
	if err != nil {
		return err
	}
	for _, c := range cfgs {
		insArn := strings.TrimSuffix(c.NativeID, "/access-control-attribute-configuration")
		insID, ok := idx.idByArn[insArn]
		if !ok {
			continue
		}
		if err := st.UpsertRelationship(c.ID, insID, store.RelAttachedTo, "directed", nil); err != nil {
			return fmt.Errorf("upsert sso attr-config→instance: %w", err)
		}
	}
	return nil
}

// resolveSSOTrustedTokenIssuerInstance wires each trusted token issuer to its
// parent Identity Center instance. The instance ARN is embedded as InstanceArn
// at scan time (the issuer metadata carries no back-reference).
func resolveSSOTrustedTokenIssuerInstance(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID,
		Types: []string{TypeSSOTrustedTokenIssuer}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	idx, err := loadSSOInstanceIndex(acct, st)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			InstanceArn string `json:"InstanceArn"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		insID, ok := idx.idByArn[attrs.InstanceArn]
		if !ok {
			continue
		}
		if err := st.UpsertRelationship(r.ID, insID, store.RelAttachedTo, "directed", nil); err != nil {
			return fmt.Errorf("upsert sso trusted-token-issuer→instance: %w", err)
		}
	}
	return nil
}

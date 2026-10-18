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
		noopAccountAccessHierarchy,
		EdgeDecl{TypeAccountAccessApplication, TypeAccountAccessEntitlement, store.RelContains},
	)
	registerResolver(
		resolveAccountAccessApplicationSSO,
		EdgeDecl{TypeAccountAccessApplication, TypeSSOInstance, store.RelUses},
		EdgeDecl{TypeAccountAccessApplication, TypeSSOApplication, store.RelUses},
	)
	registerResolver(
		resolveAccountAccessEntitlementRole,
		EdgeDecl{TypeAccountAccessEntitlement, TypeIAMRole, store.RelUses},
	)
}

// noopAccountAccessHierarchy is a placeholder: scanAccountAccessEntitlements
// records the application→entitlement contains rows through childFanOut.
func noopAccountAccessHierarchy(_ *account, _ *store.Store) error {
	return nil
}

// resolveAccountAccessApplicationSSO wires each application to the IAM
// Identity Center instance and application named in its identity source
// (IdentitySource.IdentityCenter, as accountAccessApplicationAttrs renders it),
// when they are scanned in the same account.
func resolveAccountAccessApplicationSSO(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeAccountAccessApplication}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	instances, err := scannedIDSet(acct, st, TypeSSOInstance)
	if err != nil {
		return err
	}
	ssoApps, err := scannedIDSet(acct, st, TypeSSOApplication)
	if err != nil {
		return err
	}
	// A row that fails to decode is reported at the end so that it costs the
	// other rows none of their edges.
	var decodeErrs []error
	for _, r := range rows {
		var attrs struct {
			IdentitySource struct {
				IdentityCenter struct {
					InstanceArn    string `json:"InstanceArn"`
					ApplicationArn string `json:"ApplicationArn"`
				} `json:"IdentityCenter"`
			} `json:"IdentitySource"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			decodeErrs = append(decodeErrs, fmt.Errorf("decode %s %s attributes: %w", TypeAccountAccessApplication, r.NativeID, err))
			continue
		}
		ic := attrs.IdentitySource.IdentityCenter
		for _, ref := range []struct {
			arn     string
			scanned map[string]bool
		}{{ic.InstanceArn, instances}, {ic.ApplicationArn, ssoApps}} {
			if ref.arn == "" {
				continue
			}
			targetID := store.ResourceID("aws", acct.ID, ref.arn)
			if !ref.scanned[targetID] {
				continue
			}
			if err := st.UpsertRelationship(r.ID, targetID, store.RelUses, "directed", nil); err != nil {
				return fmt.Errorf("upsert account-access application→sso: %w", err)
			}
		}
	}
	return errors.Join(decodeErrs...)
}

// resolveAccountAccessEntitlementRole wires each entitlement to the IAM role
// it lets its principal assume (Entitlement.PrincipalRole.RoleArn), when that
// role is scanned in the same account. An entitlement can name a role in
// another account; that role's row lives under its own account, so it gets no
// edge here.
func resolveAccountAccessEntitlementRole(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeAccountAccessEntitlement}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	roles, err := scannedIDSet(acct, st, TypeIAMRole)
	if err != nil {
		return err
	}
	var decodeErrs []error
	for _, r := range rows {
		var attrs struct {
			Entitlement struct {
				PrincipalRole struct {
					RoleArn string `json:"RoleArn"`
				} `json:"PrincipalRole"`
			} `json:"Entitlement"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			decodeErrs = append(decodeErrs, fmt.Errorf("decode %s %s attributes: %w", TypeAccountAccessEntitlement, r.NativeID, err))
			continue
		}
		roleARN := attrs.Entitlement.PrincipalRole.RoleArn
		if roleARN == "" {
			continue
		}
		roleID := store.ResourceID("aws", acct.ID, roleARN)
		if !roles[roleID] {
			continue
		}
		if err := st.UpsertRelationship(r.ID, roleID, store.RelUses, "directed", nil); err != nil {
			return fmt.Errorf("upsert account-access entitlement→iam-role: %w", err)
		}
	}
	return errors.Join(decodeErrs...)
}

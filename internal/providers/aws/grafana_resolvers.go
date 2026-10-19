package aws

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	grafanatypes "github.com/aws/aws-sdk-go-v2/service/grafana/types"
	"github.com/icearp/disco-cli/internal/util"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerResolver(
		noopGrafanaHierarchy,
		EdgeDecl{TypeGrafanaWorkspace, TypeGrafanaWorkspaceServiceAccount, store.RelContains},
		EdgeDecl{TypeGrafanaWorkspaceServiceAccount, TypeGrafanaWorkspaceServiceAccountToken, store.RelContains},
		EdgeDecl{TypeGrafanaWorkspace, TypeGrafanaPermission, store.RelContains},
	)
	registerResolver(
		resolveGrafanaPermissionPrincipals,
		EdgeDecl{TypeGrafanaPermission, TypeIdentityStoreUser, store.RelUses},
		EdgeDecl{TypeGrafanaPermission, TypeIdentityStoreGroup, store.RelUses},
	)
}

// noopGrafanaHierarchy only declares the workspace→service-account→token and
// workspace→permission contains edges, which scanGrafana records at scan time
// via childFanOut.
func noopGrafanaHierarchy(_ *account, _ *store.Store) error {
	return nil
}

// resolveGrafanaPermissionPrincipals links each role grant to the Identity
// Store user or group it grants, when that principal is stored. The grant
// names the principal by id alone, without its identity store, so the
// principals are matched on the id their NativeID ends with. Only principals
// stored under the grant's own account are matched, so workspaces in a member
// account resolve nothing when the Identity Center rows were stored by another
// account's scan. A grant whose attributes do not decode is reported and costs
// no other grant its edge.
func resolveGrafanaPermissionPrincipals(acct *account, st *store.Store) error {
	perms, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID,
		Types: []string{TypeGrafanaPermission},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(perms) == 0 {
		return nil
	}
	users, err := identityStorePrincipalIndex(acct, st, TypeIdentityStoreUser)
	if err != nil {
		return err
	}
	groups, err := identityStorePrincipalIndex(acct, st, TypeIdentityStoreGroup)
	if err != nil {
		return err
	}
	var errs []error
	for _, p := range perms {
		var entry grafanatypes.PermissionEntry
		if err := json.Unmarshal([]byte(p.AttributesJSON), &entry); err != nil {
			errs = append(errs, fmt.Errorf("decode grafana permission %s: %w", p.NativeID, err))
			continue
		}
		if entry.User == nil {
			continue
		}
		var principals map[string]string
		switch entry.User.Type {
		case grafanatypes.UserTypeSsoUser:
			principals = users
		case grafanatypes.UserTypeSsoGroup:
			principals = groups
		}
		target, ok := principals[sv(entry.User.Id)]
		if !ok {
			continue
		}
		if err := st.UpsertRelationship(p.ID, target, store.RelUses, "directed", nil); err != nil {
			return fmt.Errorf("upsert grafana permission→identitystore principal: %w", err)
		}
	}
	return errors.Join(errs...)
}

// identityStorePrincipalIndex maps the user or group id that ends the
// NativeID of each stored row of typ (identityStoreUserNativeID,
// identityStoreGroupNativeID) to the row's ResourceID.
func identityStorePrincipalIndex(acct *account, st *store.Store, typ string) (map[string]string, error) {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID,
		Types: []string{typ},
		Limit: util.AllResources,
	})
	if err != nil {
		return nil, err
	}
	index := make(map[string]string, len(rows))
	for _, r := range rows {
		principalID := r.NativeID[strings.LastIndexByte(r.NativeID, '/')+1:]
		index[principalID] = r.ID
	}
	return index, nil
}

package azure

import (
	"encoding/json"
	"fmt"
	"strings"

	"codeberg.org/icearp/disco/internal/store"
	"codeberg.org/icearp/disco/internal/util"
)

func init() { registerResolver(resolveAuthorizationRelationships) }

// resolveAuthorizationRelationships derives edges from RBAC role assignments:
//   - assignment -[uses]-> role-definition (via RoleDefinitionID)
//   - assignment -[attached-to]-> scoped-resource (via Scope, when the scope
//     matches a known resource in the local store)
//
// Principal edges are intentionally deferred: principals (users / groups /
// service principals / managed identities) live in Microsoft Graph and are not
// scanned by disco yet. The PrincipalID is preserved in attributes so that a
// future Entra ID resolver can backfill these edges.
func resolveAuthorizationRelationships(sub *subscription, st *store.Store) error {
	assignments, err := st.ListResources(store.ResourceFilter{
		Provider: "azure", AccountID: sub.ID,
		Types: []string{TypeAuthorizationRoleAssignment},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(assignments) == 0 {
		return nil
	}

	// Build a lowercased index of every Azure resource in this subscription so
	// the assignment Scope (which Azure returns in arbitrary case) can be
	// resolved back to a canonical store ResourceID.
	all, err := st.ListResources(store.ResourceFilter{
		Provider: "azure", AccountID: sub.ID, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	scopeIndex := make(map[string]string, len(all))
	for _, r := range all {
		scopeIndex[strings.ToLower(r.NativeID)] = r.ID
	}

	for _, r := range assignments {
		var attrs struct {
			Properties *struct {
				RoleDefinitionID *string `json:"roleDefinitionId"`
				Scope            *string `json:"scope"`
			} `json:"properties"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if attrs.Properties == nil {
			continue
		}

		// Edge → role definition (FK: same-sub role-definition with matching NativeID).
		if attrs.Properties.RoleDefinitionID != nil {
			defResourceID := store.ResourceID("azure", sub.ID, TypeAuthorizationRoleDefinition, *attrs.Properties.RoleDefinitionID)
			if _, err := st.GetResource(defResourceID); err == nil {
				if err := st.UpsertRelationship(r.ID, defResourceID, store.RelUses, "directed", nil); err != nil {
					return fmt.Errorf("upsert role-assignment→role-definition: %w", err)
				}
			}
		}

		// Edge → scoped resource (when the scope is a concrete resource we know about).
		if attrs.Properties.Scope != nil {
			if toID, ok := scopeIndex[strings.ToLower(*attrs.Properties.Scope)]; ok && toID != r.ID {
				if err := st.UpsertRelationship(r.ID, toID, store.RelAttachedTo, "directed", nil); err != nil {
					return fmt.Errorf("upsert role-assignment→scope: %w", err)
				}
			}
		}
	}
	return nil
}

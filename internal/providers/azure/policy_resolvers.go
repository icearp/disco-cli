package azure

import (
	"encoding/json"
	"fmt"
	"strings"

	"codeberg.org/icearp/disco/internal/util"
	"codeberg.org/icearp/disco/store"
)

func init() {
	registerResolver(resolvePolicyRelationships,
		EdgeDecl{Source: TypePolicyAssignment, Target: TypePolicyDefinition, Kind: store.RelUses},
		EdgeDecl{Source: TypePolicyAssignment, Target: TypePolicySetDefinition, Kind: store.RelUses},
		// scope can be any resource; the canonical scope-container levels:
		EdgeDecl{Source: TypePolicyAssignment, Target: TypeSubscription, Kind: store.RelAttachedTo},
		EdgeDecl{Source: TypePolicyAssignment, Target: TypeManagementGroup, Kind: store.RelAttachedTo},
		EdgeDecl{Source: TypePolicyAssignment, Target: TypeResourcesResourceGroup, Kind: store.RelAttachedTo},
	)
}

// resolvePolicyRelationships derives two edge classes per policy assignment:
//   - assignment -[uses]-> policy-definition OR policy-set-definition (FK on policyDefinitionId)
//   - assignment -[attached-to]-> scoped-resource (FK on scope when it matches a known resource)
//
// Mirrors the RBAC pattern from authorization_resolvers.go. Identity → MSI
// edges (DINE/deployIfNotExists assignments often carry a managed identity)
// covered by the generic consumer resolver.
func resolvePolicyRelationships(sub *subscription, st *store.Store) error {
	assignments, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"azure"}, AccountID: sub.ID,
		Types: []string{TypePolicyAssignment},
		Limit: util.AllResources,
	})
	if err != nil || len(assignments) == 0 {
		return err
	}

	// Per-sub lowercased index of every Azure resource — used both to resolve
	// scope (any resource type) and to FK to policy-definition / policy-set-definition.
	all, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"azure"}, AccountID: sub.ID, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	resourceIndex := make(map[string]string, len(all))
	for _, r := range all {
		resourceIndex[strings.ToLower(r.NativeID)] = r.ID
	}

	// Two classes of FK target live outside the per-sub `all` index above and are
	// merged in here from the tenant account (deduplicated) and the subscription
	// (degraded mode, when no tenant GUID resolved):
	//   - Built-in policy/set definitions are ManagedByProvider, which
	//     ListResources hides by default; their scope-free ARM IDs let an
	//     assignment's policyDefinitionId match directly.
	//   - Management groups are stored under the tenant account, so a per-sub
	//     index misses them; a policy assignment inherited from an ancestor MG
	//     carries that MG's ID as its scope and resolves to an attached-to edge.
	defAccounts := []string{sub.ID}
	if sub.tenantID != "" && sub.tenantID != sub.ID {
		defAccounts = append(defAccounts, sub.tenantID)
	}
	for _, acct := range defAccounts {
		extra, berr := st.ListResources(store.ResourceFilter{
			Providers: []string{"azure"}, AccountID: acct,
			Types:          []string{TypePolicyDefinition, TypePolicySetDefinition, TypeManagementGroup},
			IncludeManaged: true,
			Limit:          util.AllResources,
		})
		if berr != nil {
			return berr
		}
		for _, b := range extra {
			resourceIndex[strings.ToLower(b.NativeID)] = b.ID
		}
	}

	for _, r := range assignments {
		var attrs struct {
			Properties *struct {
				PolicyDefinitionID *string `json:"policyDefinitionId"`
				Scope              *string `json:"scope"`
			} `json:"properties"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil || attrs.Properties == nil {
			continue
		}

		if attrs.Properties.PolicyDefinitionID != nil {
			if toID, ok := resourceIndex[strings.ToLower(*attrs.Properties.PolicyDefinitionID)]; ok && toID != r.ID {
				if err := st.UpsertRelationship(r.ID, toID, store.RelUses, "directed", nil); err != nil {
					return fmt.Errorf("upsert policy-assignment→definition: %w", err)
				}
			}
		}

		if attrs.Properties.Scope != nil {
			if toID, ok := resourceIndex[strings.ToLower(*attrs.Properties.Scope)]; ok && toID != r.ID {
				if err := st.UpsertRelationship(r.ID, toID, store.RelAttachedTo, "directed", nil); err != nil {
					return fmt.Errorf("upsert policy-assignment→scope: %w", err)
				}
			}
		}
	}
	return nil
}

package azure

import (
	"encoding/json"
	"fmt"
	"strings"

	"codeberg.org/icearp/disco/internal/util"
	"codeberg.org/icearp/disco/store"
)

func init() {
	registerResolver(resolveManagedIdentityAssignmentPrincipals)
	registerResolver(resolveManagedIdentityConsumers)
}

// resolveManagedIdentityAssignmentPrincipals derives role-assignment -[uses]->
// user-assigned-identity edges by matching the assignment's principalId
// against each MSI's properties.principalId (the Entra service-principal
// object id Azure issues for the identity). This closes one branch of the
// principal-edge gap left by R3.2 without requiring an Entra ID scanner.
func resolveManagedIdentityAssignmentPrincipals(sub *subscription, st *store.Store) error {
	identities, err := st.ListResources(store.ResourceFilter{
		Provider: "azure", AccountID: sub.ID,
		Types: []string{TypeManagedIdentityUserAssigned},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(identities) == 0 {
		return nil
	}
	principalIndex := make(map[string]string, len(identities))
	for _, r := range identities {
		var attrs struct {
			Properties *struct {
				PrincipalID *string `json:"principalId"`
			} `json:"properties"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if attrs.Properties == nil || attrs.Properties.PrincipalID == nil {
			continue
		}
		principalIndex[strings.ToLower(*attrs.Properties.PrincipalID)] = r.ID
	}
	if len(principalIndex) == 0 {
		return nil
	}

	assignments, err := st.ListResources(store.ResourceFilter{
		Provider: "azure", AccountID: sub.ID,
		Types: []string{TypeAuthorizationRoleAssignment},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	for _, r := range assignments {
		var attrs struct {
			Properties *struct {
				PrincipalID *string `json:"principalId"`
			} `json:"properties"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if attrs.Properties == nil || attrs.Properties.PrincipalID == nil {
			continue
		}
		toID, ok := principalIndex[strings.ToLower(*attrs.Properties.PrincipalID)]
		if !ok {
			continue
		}
		if err := st.UpsertRelationship(r.ID, toID, store.RelUses, "directed", nil); err != nil {
			return fmt.Errorf("upsert role-assignment→msi: %w", err)
		}
	}
	return nil
}

// resolveManagedIdentityConsumers derives consumer -[uses]-> user-assigned-identity
// edges for any Azure resource whose attributes contain an `identity.userAssignedIdentities`
// map (VMs, VMSS, AppService sites, AKS clusters, storage accounts, ...). The
// keys of that map are the full ARM IDs of the referenced identities, so this
// is provider-agnostic: any future scanner that stores its native SDK response
// verbatim will be picked up automatically.
func resolveManagedIdentityConsumers(sub *subscription, st *store.Store) error {
	identities, err := st.ListResources(store.ResourceFilter{
		Provider: "azure", AccountID: sub.ID,
		Types: []string{TypeManagedIdentityUserAssigned},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(identities) == 0 {
		return nil
	}
	msiIndex := make(map[string]string, len(identities))
	for _, r := range identities {
		msiIndex[strings.ToLower(r.NativeID)] = r.ID
	}

	all, err := st.ListResources(store.ResourceFilter{
		Provider: "azure", AccountID: sub.ID, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	for _, r := range all {
		if r.Type == TypeManagedIdentityUserAssigned || r.AttributesJSON == "" {
			continue
		}
		// Two casings appear in SDK responses across packages: top-level "identity"
		// (most arm* packages, lowercase camelCase) and capitalized "Identity"
		// (some older packages). Try both.
		var attrs struct {
			Identity *struct {
				UserAssignedIdentities map[string]json.RawMessage `json:"userAssignedIdentities"`
			} `json:"identity"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil || attrs.Identity == nil {
			var alt struct {
				Identity *struct {
					UserAssignedIdentities map[string]json.RawMessage `json:"UserAssignedIdentities"`
				} `json:"Identity"`
			}
			if err := json.Unmarshal([]byte(r.AttributesJSON), &alt); err != nil || alt.Identity == nil {
				continue
			}
			attrs.Identity = &struct {
				UserAssignedIdentities map[string]json.RawMessage `json:"userAssignedIdentities"`
			}{UserAssignedIdentities: alt.Identity.UserAssignedIdentities}
		}
		for msiNativeID := range attrs.Identity.UserAssignedIdentities {
			toID, ok := msiIndex[strings.ToLower(msiNativeID)]
			if !ok || toID == r.ID {
				continue
			}
			if err := st.UpsertRelationship(r.ID, toID, store.RelUses, "directed", nil); err != nil {
				return fmt.Errorf("upsert consumer→msi: %w", err)
			}
		}
	}
	return nil
}

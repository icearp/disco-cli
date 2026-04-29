package azure

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/store"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armpolicy"
)

func init() { registerService(serviceEntry{name: "azure:policy", fn: scanPolicy}) }

// scanPolicy discovers Azure Policy definitions, set definitions, and
// assignments. Mirrors the RBAC pattern (definitions then assignments) so
// resolvers can FK assignments to definitions locally. Built-ins are returned
// alongside customer-authored definitions and stored under each subscription's
// account_id (acceptable duplication — same logic as RBAC role-definitions).
// Exemptions (`armpolicy.ExemptionsClient`) deferred — narrow customer base.
func scanPolicy(ctx context.Context, sub *subscription, cred *azidentity.DefaultAzureCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	defClient, err := armpolicy.NewDefinitionsClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armpolicy:NewDefinitionsClient: %w", err)
	}
	dt, di, err := azPageScan(ctx, "armpolicy:Definitions.List", sub, st,
		defClient.NewListPager(nil),
		func(page armpolicy.DefinitionsClientListResponse) ([]*store.Resource, [][2]string) {
			var batch []*store.Resource
			for _, d := range page.Value {
				if d == nil || d.ID == nil {
					continue
				}
				name := sv(d.Name)
				managed := d.Properties != nil && d.Properties.PolicyType != nil &&
					*d.Properties.PolicyType == armpolicy.PolicyTypeBuiltIn
				batch = append(batch, &store.Resource{
					Provider: "azure", AccountID: sub.ID, AccountName: &sub.Name,
					Type: TypePolicyDefinition, NativeID: sv(d.ID),
					Name:              &name,
					AttributesJSON:    mustJSON(d),
					DiscoveredBy:      scanID,
					ManagedByProvider: managed,
				})
			}
			return batch, nil
		})
	total += dt
	inserted += di
	if err != nil {
		return total, inserted, err
	}

	setClient, err := armpolicy.NewSetDefinitionsClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return total, inserted, fmt.Errorf("armpolicy:NewSetDefinitionsClient: %w", err)
	}
	st1, si1, err := azPageScan(ctx, "armpolicy:SetDefinitions.List", sub, st,
		setClient.NewListPager(nil),
		func(page armpolicy.SetDefinitionsClientListResponse) ([]*store.Resource, [][2]string) {
			var batch []*store.Resource
			for _, d := range page.Value {
				if d == nil || d.ID == nil {
					continue
				}
				name := sv(d.Name)
				managed := d.Properties != nil && d.Properties.PolicyType != nil &&
					*d.Properties.PolicyType == armpolicy.PolicyTypeBuiltIn
				batch = append(batch, &store.Resource{
					Provider: "azure", AccountID: sub.ID, AccountName: &sub.Name,
					Type: TypePolicySetDefinition, NativeID: sv(d.ID),
					Name:              &name,
					AttributesJSON:    mustJSON(d),
					DiscoveredBy:      scanID,
					ManagedByProvider: managed,
				})
			}
			return batch, nil
		})
	total += st1
	inserted += si1
	if err != nil {
		return total, inserted, err
	}

	asnClient, err := armpolicy.NewAssignmentsClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return total, inserted, fmt.Errorf("armpolicy:NewAssignmentsClient: %w", err)
	}
	at, ai, err := azPageScan(ctx, "armpolicy:Assignments.List", sub, st,
		asnClient.NewListPager(nil),
		func(page armpolicy.AssignmentsClientListResponse) ([]*store.Resource, [][2]string) {
			var batch []*store.Resource
			for _, a := range page.Value {
				if a == nil || a.ID == nil {
					continue
				}
				name := sv(a.Name)
				batch = append(batch, &store.Resource{
					Provider: "azure", AccountID: sub.ID, AccountName: &sub.Name,
					Type: TypePolicyAssignment, NativeID: sv(a.ID),
					Name:           &name,
					AttributesJSON: mustJSON(a),
					DiscoveredBy:   scanID,
				})
			}
			return batch, nil
		})
	total += at
	inserted += ai
	return total, inserted, err
}

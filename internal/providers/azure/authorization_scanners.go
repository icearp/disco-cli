package azure

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/store"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/authorization/armauthorization/v2"
)

func init() {
	registerService(serviceEntry{
		name: "azure:microsoft.authorization",
		fn:   scanAuthorizationNamespace,
		emits: []coverage.TypeDecl{
			{Service: "microsoft.authorization", DiscoType: TypeAuthorizationRoleAssignment},
			{Service: "microsoft.authorization", DiscoType: TypeAuthorizationRoleDefinition},
		},
	})
}

// scanAuthorization discovers Azure RBAC role assignments and role definitions
// scoped to (or visible from) the subscription. Built-in role definitions are
// tenant-scoped but are returned by the subscription-scoped list call; they are
// stored under each subscription's account_id (acceptable duplication — the
// ResourceID hash differs and per-sub resolvers can FK-match locally).
func scanAuthorization(ctx context.Context, sub *subscription, cred *azidentity.DefaultAzureCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	subScope := "/subscriptions/" + sub.ID

	// Role definitions first so the resolver can FK from assignments.
	defClient, err := armauthorization.NewRoleDefinitionsClient(cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armauthorization:NewRoleDefinitionsClient: %w", err)
	}
	dt, di, err := azPageScan(ctx, "armauthorization:RoleDefinitions.List", sub, st,
		defClient.NewListPager(subScope, nil),
		func(page armauthorization.RoleDefinitionsClientListResponse) ([]*store.Resource, [][2]string) {
			var batch []*store.Resource
			for _, rd := range page.Value {
				if rd == nil || rd.ID == nil {
					continue
				}
				name := sv(rd.Name)
				if rd.Properties != nil && rd.Properties.RoleName != nil {
					name = *rd.Properties.RoleName
				}
				managed := rd.Properties != nil && rd.Properties.RoleType != nil &&
					*rd.Properties.RoleType == "BuiltInRole"
				batch = append(batch, &store.Resource{
					Provider: "azure", AccountID: sub.ID, AccountName: &sub.Name,
					Type: TypeAuthorizationRoleDefinition, NativeID: sv(rd.ID),
					Name:              &name,
					AttributesJSON:    mustJSON(rd),
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

	// Role assignments.
	asnClient, err := armauthorization.NewRoleAssignmentsClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return total, inserted, fmt.Errorf("armauthorization:NewRoleAssignmentsClient: %w", err)
	}
	at, ai, err := azPageScan(ctx, "armauthorization:RoleAssignments.ListForSubscription", sub, st,
		asnClient.NewListForSubscriptionPager(nil),
		func(page armauthorization.RoleAssignmentsClientListForSubscriptionResponse) ([]*store.Resource, [][2]string) {
			var batch []*store.Resource
			for _, ra := range page.Value {
				if ra == nil || ra.ID == nil {
					continue
				}
				name := sv(ra.Name)
				batch = append(batch, &store.Resource{
					Provider: "azure", AccountID: sub.ID, AccountName: &sub.Name,
					Type: TypeAuthorizationRoleAssignment, NativeID: sv(ra.ID),
					Name:           &name,
					AttributesJSON: mustJSON(ra),
					DiscoveredBy:   scanID,
				})
			}
			return batch, nil
		})
	total += at
	inserted += ai
	return total, inserted, err
}

// scanAuthorizationNamespace runs every Microsoft.authorization scanner phase concurrently. The
// authorization ARM namespace spans several disco scanners merged under one
// serviceEntry so the service name aligns to the namespace.
func scanAuthorizationNamespace(ctx context.Context, sub *subscription, cred *azidentity.DefaultAzureCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	return azRunPhases(
		func() (int, int, error) { return scanAuthorization(ctx, sub, cred, st, scanID) },
		func() (int, int, error) { return scanPolicy(ctx, sub, cred, st, scanID) },
	)
}

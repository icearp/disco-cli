package azure

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/store"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/managementgroups/armmanagementgroups"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/subscription/armsubscription"
)

func init() {
	registerTenantService(tenantServiceEntry{
		name: "azure:microsoft.management",
		fn:   scanManagementTenant,
		emits: []coverage.TypeDecl{
			{Service: "microsoft.management", DiscoType: TypeManagementGroup},
		},
	})
	registerService(serviceEntry{
		name: "azure:microsoft.resources",
		fn:   scanSubscriptionResource,
		emits: []coverage.TypeDecl{
			{Service: "microsoft.resources", DiscoType: TypeSubscription},
		},
	})
}

// scanManagementTenant discovers Azure Management Groups, which are a
// tenant-level construct (the API list is tenant-wide, not subscription-scoped).
// It runs ONCE per scan as a tenant service and stores each MG under the tenant
// GUID, so a multi-subscription scan keeps a single copy of the MG tree rather
// than one per subscription. When the tenant GUID could not be resolved it falls
// back to the first subscription's ID — still a single deduplicated copy.
func scanManagementTenant(ctx context.Context, subs []subscription, cred azcore.TokenCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	if len(subs) == 0 {
		return 0, 0, nil
	}
	accountID := subs[0].tenantID
	if accountID == "" {
		accountID = subs[0].ID
	}
	mgClient, err := armmanagementgroups.NewClient(cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armmanagementgroups:NewClient: %w", err)
	}
	return scanManagementInto(ctx, accountID, st, scanID, mgClient)
}

// scanManagementInto is the testable core: it pages the (tenant-wide) management
// group list from the supplied client and upserts each MG under accountID. Split
// from scanManagementTenant so tests can drive it with a fake-transport client.
func scanManagementInto(ctx context.Context, accountID string, st *store.Store, scanID string, client *armmanagementgroups.Client) (total, inserted int, err error) {
	// scopeRef satisfies azPageScan's *subscription parameter (used only for the
	// AccessDenied scope label, never for the stored AccountID).
	scopeRef := &subscription{ID: accountID, Name: "tenant"}
	return azPageScan(ctx, "armmanagementgroups:List", scopeRef, st,
		client.NewListPager(nil),
		func(page armmanagementgroups.ClientListResponse) ([]*store.Resource, [][2]string) {
			var batch []*store.Resource
			for _, mg := range page.Value {
				if mg == nil || mg.ID == nil {
					continue
				}
				name := sv(mg.Name)
				batch = append(batch, &store.Resource{
					Provider: "azure", AccountID: accountID,
					Type: TypeManagementGroup, NativeID: sv(mg.ID),
					Name:           &name,
					Region:         regionGlobal,
					AttributesJSON: mustJSON(mg),
					DiscoveredBy:   scanID,
				})
			}
			return batch, nil
		})
}

// scanSubscriptionResource discovers the subscription itself as a first-class
// resource (Microsoft.Resources/subscriptions). Tenant-scoped API run
// per-subscription (duplication accepted as above).
//
// Recording subscription-as-resource closes a gap for scope-attached
// resolvers: policy assignments scoped to `/subscriptions/<id>` could not
// previously FK to a stored resource; they now will.
func scanSubscriptionResource(ctx context.Context, sub *subscription, cred azcore.TokenCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	subClient, err := armsubscription.NewSubscriptionsClient(cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armsubscription:NewSubscriptionsClient: %w", err)
	}
	return azPageScan(ctx, "armsubscription:Subscriptions.List", sub, st,
		subClient.NewListPager(nil),
		func(page armsubscription.SubscriptionsClientListResponse) ([]*store.Resource, [][2]string) {
			var batch []*store.Resource
			for _, s := range page.Value {
				if s == nil || s.ID == nil {
					continue
				}
				name := sv(s.DisplayName)
				batch = append(batch, &store.Resource{
					Provider: "azure", AccountID: sub.ID, AccountName: &sub.Name,
					Type: TypeSubscription, NativeID: sv(s.ID),
					Name:           &name,
					AttributesJSON: mustJSON(s),
					DiscoveredBy:   scanID,
				})
			}
			return batch, nil
		})
}

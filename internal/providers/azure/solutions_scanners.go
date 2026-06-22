package azure

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/store"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/solutions/armmanagedapplications"
)

func init() {
	registerService(serviceEntry{
		name: "azure:microsoft.solutions",
		fn:   scanSolutions,
		emits: []coverage.TypeDecl{
			{Service: "microsoft.solutions", DiscoType: TypeSolutionsApplicationDefinition},
			{Service: "microsoft.solutions", DiscoType: TypeSolutionsApplication},
			{Service: "microsoft.solutions", DiscoType: TypeSolutionsJitRequest},
		},
	})
}

// scanSolutions discovers Managed Applications — application definitions,
// applications, and JIT (just-in-time) access requests. JitRequests has a
// single-call ListBySubscription (no pager), handled inline.
func scanSolutions(ctx context.Context, sub *subscription, cred azcore.TokenCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	return azRunPhases(
		func() (int, int, error) {
			defClient, err := armmanagedapplications.NewApplicationDefinitionsClient(sub.ID, cred, azClientOptions)
			if err != nil {
				return 0, 0, fmt.Errorf("armmanagedapplications:NewApplicationDefinitionsClient: %w", err)
			}
			return azSimpleScan(ctx, "armmanagedapplications:ApplicationDefinitions.ListBySubscription", TypeSolutionsApplicationDefinition, sub, st, scanID,
				defClient.NewListBySubscriptionPager(nil),
				func(p armmanagedapplications.ApplicationDefinitionsClientListBySubscriptionResponse) []*armmanagedapplications.ApplicationDefinition {
					return p.Value
				},
				func(r *armmanagedapplications.ApplicationDefinition) azTrackedBase {
					return azTrackedBase{id: sv(r.ID), name: sv(r.Name), location: sv(r.Location), tags: r.Tags, full: r}
				})
		},
		func() (int, int, error) {
			appClient, err := armmanagedapplications.NewApplicationsClient(sub.ID, cred, azClientOptions)
			if err != nil {
				return 0, 0, fmt.Errorf("armmanagedapplications:NewApplicationsClient: %w", err)
			}
			return azSimpleScan(ctx, "armmanagedapplications:Applications.ListBySubscription", TypeSolutionsApplication, sub, st, scanID,
				appClient.NewListBySubscriptionPager(nil),
				func(p armmanagedapplications.ApplicationsClientListBySubscriptionResponse) []*armmanagedapplications.Application {
					return p.Value
				},
				func(r *armmanagedapplications.Application) azTrackedBase {
					return azTrackedBase{id: sv(r.ID), name: sv(r.Name), location: sv(r.Location), tags: r.Tags, full: r}
				})
		},
		func() (int, int, error) {
			return scanSolutionsJitRequests(ctx, sub, cred, st, scanID)
		},
	)
}

// scanSolutionsJitRequests handles the single-call JitRequests list (no pager).
func scanSolutionsJitRequests(ctx context.Context, sub *subscription, cred azcore.TokenCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	client, err := armmanagedapplications.NewJitRequestsClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armmanagedapplications:NewJitRequestsClient: %w", err)
	}
	resp, err := client.ListBySubscription(ctx, nil)
	if err != nil {
		if isSkippableScanError(err) {
			return 0, 0, skipIfAccessDenied(st, "armmanagedapplications:JitRequests.ListBySubscription", sub.ID, err)
		}
		return 0, 0, fmt.Errorf("armmanagedapplications:JitRequests.ListBySubscription: %w", err)
	}
	// Non-paginated List → feed the one response through azSimpleScan via a
	// one-shot pager so the build+upsert+closure tail matches paged scanners.
	return azSimpleScan(ctx, "armmanagedapplications:JitRequests.ListBySubscription", TypeSolutionsJitRequest, sub, st, scanID,
		&singlePager[armmanagedapplications.JitRequestsClientListBySubscriptionResponse]{page: resp},
		func(p armmanagedapplications.JitRequestsClientListBySubscriptionResponse) []*armmanagedapplications.JitRequestDefinition {
			return p.Value
		},
		func(r *armmanagedapplications.JitRequestDefinition) azTrackedBase {
			return azTrackedBase{id: sv(r.ID), name: sv(r.Name), location: sv(r.Location), tags: r.Tags, full: r}
		})
}

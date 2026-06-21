package azure

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/store"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
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
func scanSolutions(ctx context.Context, sub *subscription, cred *azidentity.DefaultAzureCredential, st *store.Store, scanID string) (total, inserted int, err error) {
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
func scanSolutionsJitRequests(ctx context.Context, sub *subscription, cred *azidentity.DefaultAzureCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	client, err := armmanagedapplications.NewJitRequestsClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armmanagedapplications:NewJitRequestsClient: %w", err)
	}
	resp, err := client.ListBySubscription(ctx, nil)
	if err != nil {
		var respErr *azcore.ResponseError
		if errors.As(err, &respErr) && (respErr.StatusCode == http.StatusForbidden || respErr.StatusCode == http.StatusUnauthorized) {
			return 0, 0, skipIfAccessDenied(st, "armmanagedapplications:JitRequests.ListBySubscription", sub.ID, err)
		}
		return 0, 0, fmt.Errorf("armmanagedapplications:JitRequests.ListBySubscription: %w", err)
	}
	batch, pairs := azTrackedRows(sub, scanID, TypeSolutionsJitRequest, resp.Value,
		func(r *armmanagedapplications.JitRequestDefinition) azTrackedBase {
			return azTrackedBase{id: sv(r.ID), name: sv(r.Name), location: sv(r.Location), tags: r.Tags, full: r}
		})
	if len(batch) == 0 {
		return 0, 0, nil
	}
	n, err := st.UpsertResources(batch)
	if err != nil {
		return 0, 0, fmt.Errorf("upsert solutions jit-requests: %w", err)
	}
	if len(pairs) > 0 {
		if err := st.RecordHierarchyBatch(pairs); err != nil {
			return len(batch), n, fmt.Errorf("closure solutions jit-requests: %w", err)
		}
	}
	return len(batch), n, nil
}

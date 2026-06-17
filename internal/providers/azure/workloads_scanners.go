package azure

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/store"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/workloads/armworkloads"
)

func init() {
	registerService(serviceEntry{
		name: "azure:workloads",
		fn:   scanWorkloads,
		emits: []coverage.TypeDecl{
			// Identity → MSI edges resolved centrally; the SAP virtual instance
			// is the SAP-on-Azure root, so it ships scanner-only.
			{Service: "microsoft.workloads", DiscoType: TypeWorkloadsSAPVirtualInstance, Leaf: true},
		},
	})
}

// scanWorkloads discovers SAP Virtual Instances (SAP on Azure).
func scanWorkloads(ctx context.Context, sub *subscription, cred *azidentity.DefaultAzureCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	client, err := armworkloads.NewSAPVirtualInstancesClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armworkloads:NewSAPVirtualInstancesClient: %w", err)
	}
	return azSimpleScan(ctx, "armworkloads:SAPVirtualInstances.ListBySubscription", TypeWorkloadsSAPVirtualInstance, sub, st, scanID,
		client.NewListBySubscriptionPager(nil),
		func(p armworkloads.SAPVirtualInstancesClientListBySubscriptionResponse) []*armworkloads.SAPVirtualInstance {
			return p.Value
		},
		func(i *armworkloads.SAPVirtualInstance) azTrackedBase {
			return azTrackedBase{id: sv(i.ID), name: sv(i.Name), location: sv(i.Location), tags: i.Tags, full: i}
		})
}

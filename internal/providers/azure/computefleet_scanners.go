package azure

import (
	"context"
	"fmt"

	"github.com/icearp/disco-cli/internal/redact"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/computefleet/armcomputefleet"
)

func init() {
	registerType(restype.Descriptor{Type: TypeComputeFleet, Service: "microsoft.azurefleet", Leaf: true, Redact: []redact.Rule{{Path: "properties.computeProfile.baseVirtualMachineProfile.osProfile.adminPassword", Mode: redact.RedactScalar}, {Path: "properties.computeProfile.baseVirtualMachineProfile.osProfile.customData", Mode: redact.RedactScalar}}})
	registerService(serviceEntry{
		name: "azure:microsoft.azurefleet",
		fn:   scanComputeFleet,
	})
}

// scanComputeFleet discovers Azure Compute Fleets.
func scanComputeFleet(ctx context.Context, sub *subscription, cred azcore.TokenCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	client, err := armcomputefleet.NewFleetsClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armcomputefleet:NewFleetsClient: %w", err)
	}
	return azSimpleScan(ctx, "armcomputefleet:Fleets.ListBySubscription", TypeComputeFleet, sub, st, scanID,
		client.NewListBySubscriptionPager(nil),
		func(p armcomputefleet.FleetsClientListBySubscriptionResponse) []*armcomputefleet.Fleet {
			return p.Value
		},
		func(f *armcomputefleet.Fleet) azTrackedBase {
			return azTrackedBase{id: sv(f.ID), name: sv(f.Name), location: sv(f.Location), tags: f.Tags, full: f}
		})
}

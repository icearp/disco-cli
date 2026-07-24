package azure

import (
	"context"
	"fmt"

	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/devtestlabs/armdevtestlabs"
)

func init() {
	registerType(restype.Descriptor{Type: TypeDevTestLab, Service: "microsoft.devtestlab", Leaf: true})
	registerService(serviceEntry{
		name: "azure:microsoft.devtestlab",
		fn:   scanDevTestLabNamespace,
	})
}

// scanDevTestLab discovers devtestlab resources.
func scanDevTestLab(ctx context.Context, sub *subscription, cred azcore.TokenCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	client, err := armdevtestlabs.NewLabsClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armdevtestlabs:NewLabsClient: %w", err)
	}
	return azSimpleScan(ctx, "armdevtestlabs:Labs.ListBySubscription", TypeDevTestLab, sub, st, scanID,
		client.NewListBySubscriptionPager(nil),
		func(p armdevtestlabs.LabsClientListBySubscriptionResponse) []*armdevtestlabs.Lab { return p.Value },
		func(r *armdevtestlabs.Lab) azTrackedBase {
			return azTrackedBase{id: sv(r.ID), name: sv(r.Name), location: sv(r.Location), tags: r.Tags, full: r}
		})
}

// scanDevTestLabNamespace runs every Microsoft.devtestlab phase concurrently; the
// ARM namespace spans several scanners merged under one serviceEntry so the
// service name matches the namespace.
func scanDevTestLabNamespace(ctx context.Context, sub *subscription, cred azcore.TokenCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	return azRunPhases(
		func() (int, int, error) { return scanDevTestLab(ctx, sub, cred, st, scanID) },
		func() (int, int, error) { return scanDevTestLabs(ctx, sub, cred, st, scanID) },
	)
}

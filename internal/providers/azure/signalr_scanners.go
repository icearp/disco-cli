package azure

import (
	"context"
	"fmt"

	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/signalr/armsignalr"
)

func init() {
	registerType(restype.Descriptor{Type: TypeSignalR, Service: "microsoft.signalrservice", Leaf: true})
	registerService(serviceEntry{
		name: "azure:microsoft.signalrservice",
		fn:   scanSignalRServiceNamespace,
	})
}

// scanSignalR discovers Azure SignalR Service resources.
func scanSignalR(ctx context.Context, sub *subscription, cred azcore.TokenCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	client, err := armsignalr.NewClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armsignalr:NewClient: %w", err)
	}
	return azSimpleScan(ctx, "armsignalr:SignalR.ListBySubscription", TypeSignalR, sub, st, scanID,
		client.NewListBySubscriptionPager(nil),
		func(p armsignalr.ClientListBySubscriptionResponse) []*armsignalr.ResourceInfo { return p.Value },
		func(r *armsignalr.ResourceInfo) azTrackedBase {
			return azTrackedBase{id: sv(r.ID), name: sv(r.Name), location: sv(r.Location), tags: r.Tags, full: r}
		})
}

// scanSignalRServiceNamespace runs every Microsoft.signalrservice scanner phase concurrently. The
// signalrservice ARM namespace spans several disco scanners merged under one
// serviceEntry so the service name matches it.
func scanSignalRServiceNamespace(ctx context.Context, sub *subscription, cred azcore.TokenCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	return azRunPhases(
		func() (int, int, error) { return scanSignalR(ctx, sub, cred, st, scanID) },
		func() (int, int, error) { return scanWebPubSub(ctx, sub, cred, st, scanID) },
	)
}

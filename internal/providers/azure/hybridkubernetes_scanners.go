package azure

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/restype"
	"codeberg.org/icearp/disco/store"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/hybridkubernetes/armhybridkubernetes"
)

func init() {
	registerType(restype.Descriptor{Type: TypeKubernetesConnectedCluster, Service: "microsoft.kubernetes", Leaf: true})
	registerService(serviceEntry{
		name: "azure:microsoft.kubernetes",
		fn:   scanHybridKubernetes,
	})
}

// scanHybridKubernetes discovers Azure Arc-enabled Kubernetes connected clusters.
func scanHybridKubernetes(ctx context.Context, sub *subscription, cred azcore.TokenCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	client, err := armhybridkubernetes.NewConnectedClusterClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armhybridkubernetes:NewConnectedClusterClient: %w", err)
	}
	return azSimpleScan(ctx, "armhybridkubernetes:ConnectedCluster.ListBySubscription", TypeKubernetesConnectedCluster, sub, st, scanID,
		client.NewListBySubscriptionPager(nil),
		func(p armhybridkubernetes.ConnectedClusterClientListBySubscriptionResponse) []*armhybridkubernetes.ConnectedCluster {
			return p.Value
		},
		func(c *armhybridkubernetes.ConnectedCluster) azTrackedBase {
			return azTrackedBase{id: sv(c.ID), name: sv(c.Name), location: sv(c.Location), tags: c.Tags, full: c}
		})
}

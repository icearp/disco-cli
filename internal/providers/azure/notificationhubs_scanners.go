package azure

import (
	"context"
	"fmt"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/notificationhubs/armnotificationhubs"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerType(restype.Descriptor{Type: TypeNotificationHubNamespace, Service: "microsoft.notificationhubs", Leaf: true})
	registerService(serviceEntry{
		name: "azure:microsoft.notificationhubs",
		fn:   scanNotificationHubs,
	})
}

// scanNotificationHubs discovers notificationhubs resources.
func scanNotificationHubs(ctx context.Context, sub *subscription, cred azcore.TokenCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	client, err := armnotificationhubs.NewNamespacesClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armnotificationhubs:NewNamespacesClient: %w", err)
	}
	return azSimpleScan(ctx, "armnotificationhubs:Namespaces.ListAll", TypeNotificationHubNamespace, sub, st, scanID,
		client.NewListAllPager(nil),
		func(p armnotificationhubs.NamespacesClientListAllResponse) []*armnotificationhubs.NamespaceResource {
			return p.Value
		},
		func(r *armnotificationhubs.NamespaceResource) azTrackedBase {
			return azTrackedBase{id: sv(r.ID), name: sv(r.Name), location: sv(r.Location), tags: r.Tags, full: r}
		})
}

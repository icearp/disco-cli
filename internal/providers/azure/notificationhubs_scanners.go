package azure

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/store"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/notificationhubs/armnotificationhubs"
)

func init() {
	registerService(serviceEntry{
		name: "azure:notificationhubs",
		fn:   scanNotificationHubs,
		emits: []coverage.TypeDecl{
			{Service: "microsoft.notificationhubs", DiscoType: TypeNotificationHubNamespace, Leaf: true},
		},
	})
}

// scanNotificationHubs discovers notificationhubs resources.
func scanNotificationHubs(ctx context.Context, sub *subscription, cred *azidentity.DefaultAzureCredential, st *store.Store, scanID string) (total, inserted int, err error) {
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

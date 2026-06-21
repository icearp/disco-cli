package azure

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/store"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/communication/armcommunication/v2"
)

func init() {
	registerService(serviceEntry{
		name: "azure:microsoft.communication",
		fn:   scanCommunication,
		emits: []coverage.TypeDecl{
			{Service: "microsoft.communication", DiscoType: TypeCommunicationService, Leaf: true},
			{Service: "microsoft.communication", DiscoType: TypeCommunicationEmailService, Leaf: true},
		},
	})
}

// scanCommunication discovers Azure Communication Services and Email Services.
func scanCommunication(ctx context.Context, sub *subscription, cred *azidentity.DefaultAzureCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	return azRunPhases(
		func() (int, int, error) {
			svcClient, err := armcommunication.NewServicesClient(sub.ID, cred, azClientOptions)
			if err != nil {
				return 0, 0, fmt.Errorf("armcommunication:NewServicesClient: %w", err)
			}
			return azSimpleScan(ctx, "armcommunication:Services.ListBySubscription", TypeCommunicationService, sub, st, scanID,
				svcClient.NewListBySubscriptionPager(nil),
				func(p armcommunication.ServicesClientListBySubscriptionResponse) []*armcommunication.ServiceResource {
					return p.Value
				},
				func(s *armcommunication.ServiceResource) azTrackedBase {
					return azTrackedBase{id: sv(s.ID), name: sv(s.Name), location: sv(s.Location), tags: s.Tags, full: s}
				})
		},
		func() (int, int, error) {
			emailClient, err := armcommunication.NewEmailServicesClient(sub.ID, cred, azClientOptions)
			if err != nil {
				return 0, 0, fmt.Errorf("armcommunication:NewEmailServicesClient: %w", err)
			}
			return azSimpleScan(ctx, "armcommunication:EmailServices.ListBySubscription", TypeCommunicationEmailService, sub, st, scanID,
				emailClient.NewListBySubscriptionPager(nil),
				func(p armcommunication.EmailServicesClientListBySubscriptionResponse) []*armcommunication.EmailServiceResource {
					return p.Value
				},
				func(e *armcommunication.EmailServiceResource) azTrackedBase {
					return azTrackedBase{id: sv(e.ID), name: sv(e.Name), location: sv(e.Location), tags: e.Tags, full: e}
				})
		},
	)
}

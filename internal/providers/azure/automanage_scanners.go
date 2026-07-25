package azure

import (
	"context"
	"fmt"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/automanage/armautomanage"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerType(restype.Descriptor{Type: TypeAutomanageConfigProfile, Service: "microsoft.automanage", Leaf: true})
	registerType(restype.Descriptor{Type: TypeAutomanageBestPractice, Service: "microsoft.automanage", Leaf: true})
	registerType(restype.Descriptor{Type: TypeAutomanageConfigProfileAssignment, Service: "microsoft.automanage", Leaf: true})
	registerType(restype.Descriptor{Type: TypeAutomanageServicePrincipal, Service: "microsoft.automanage", Leaf: true})
	registerService(serviceEntry{
		name: "azure:microsoft.automanage",
		fn:   scanAutomanage,
	})
}

// scanAutomanage discovers Automanage configuration profiles, the platform
// best-practice catalog, profile assignments, and the service principal.
func scanAutomanage(ctx context.Context, sub *subscription, cred azcore.TokenCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	profiles, err := armautomanage.NewConfigurationProfilesClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armautomanage:NewConfigurationProfilesClient: %w", err)
	}
	bp, err := armautomanage.NewBestPracticesClient(cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armautomanage:NewBestPracticesClient: %w", err)
	}
	assigns, err := armautomanage.NewConfigurationProfileAssignmentsClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armautomanage:NewConfigurationProfileAssignmentsClient: %w", err)
	}
	sps, err := armautomanage.NewServicePrincipalsClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armautomanage:NewServicePrincipalsClient: %w", err)
	}
	return azRunPhases(
		func() (int, int, error) {
			return azSimpleScan(ctx, "armautomanage:ConfigurationProfiles.ListBySubscription", TypeAutomanageConfigProfile, sub, st, scanID,
				profiles.NewListBySubscriptionPager(nil),
				func(p armautomanage.ConfigurationProfilesClientListBySubscriptionResponse) []*armautomanage.ConfigurationProfile {
					return p.Value
				},
				func(r *armautomanage.ConfigurationProfile) azTrackedBase {
					return azTrackedBase{id: sv(r.ID), name: sv(r.Name), location: sv(r.Location), tags: r.Tags, full: r}
				})
		},
		func() (int, int, error) {
			return azSimpleScan(ctx, "armautomanage:BestPractices.ListByTenant", TypeAutomanageBestPractice, sub, st, scanID,
				bp.NewListByTenantPager(nil),
				func(p armautomanage.BestPracticesClientListByTenantResponse) []*armautomanage.BestPractice {
					return p.Value
				},
				func(r *armautomanage.BestPractice) azTrackedBase {
					return azTrackedBase{id: sv(r.ID), name: sv(r.Name), managed: true, full: r}
				})
		},
		func() (int, int, error) {
			return azSimpleScan(ctx, "armautomanage:ConfigurationProfileAssignments.ListBySubscription", TypeAutomanageConfigProfileAssignment, sub, st, scanID,
				assigns.NewListBySubscriptionPager(nil),
				func(p armautomanage.ConfigurationProfileAssignmentsClientListBySubscriptionResponse) []*armautomanage.ConfigurationProfileAssignment {
					return p.Value
				},
				func(r *armautomanage.ConfigurationProfileAssignment) azTrackedBase {
					return azTrackedBase{id: sv(r.ID), name: sv(r.Name), full: r}
				})
		},
		func() (int, int, error) {
			return azSimpleScan(ctx, "armautomanage:ServicePrincipals.ListBySubscription", TypeAutomanageServicePrincipal, sub, st, scanID,
				sps.NewListBySubscriptionPager(nil),
				func(p armautomanage.ServicePrincipalsClientListBySubscriptionResponse) []*armautomanage.ServicePrincipal {
					return p.Value
				},
				func(r *armautomanage.ServicePrincipal) azTrackedBase {
					return azTrackedBase{id: sv(r.ID), name: sv(r.Name), full: r}
				})
		},
	)
}

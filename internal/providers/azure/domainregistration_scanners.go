package azure

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/store"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/domainregistration/armdomainregistration"
)

func init() {
	registerService(serviceEntry{
		name: "azure:microsoft.domainregistration",
		fn:   scanDomainRegistration,
		emits: []coverage.TypeDecl{
			{Service: "microsoft.domainregistration", DiscoType: TypeDomain, Leaf: true},
		},
	})
}

// scanDomainRegistration discovers domainregistration resources.
func scanDomainRegistration(ctx context.Context, sub *subscription, cred azcore.TokenCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	client, err := armdomainregistration.NewDomainsClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armdomainregistration:NewDomainsClient: %w", err)
	}
	return azSimpleScan(ctx, "armdomainregistration:Domains.List", TypeDomain, sub, st, scanID,
		client.NewListPager(nil),
		func(p armdomainregistration.DomainsClientListResponse) []*armdomainregistration.Domain {
			return p.Value
		},
		func(r *armdomainregistration.Domain) azTrackedBase {
			return azTrackedBase{id: sv(r.ID), name: sv(r.Name), location: sv(r.Location), tags: r.Tags, full: r}
		})
}

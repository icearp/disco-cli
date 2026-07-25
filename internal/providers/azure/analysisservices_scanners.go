package azure

import (
	"context"
	"fmt"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/analysisservices/armanalysisservices"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerType(restype.Descriptor{Type: TypeAnalysisServicesServer, Service: "microsoft.analysisservices", Leaf: true})
	registerService(serviceEntry{
		name: "azure:microsoft.analysisservices",
		fn:   scanAnalysisServices,
	})
}

// scanAnalysisServices discovers Azure Analysis Services servers.
func scanAnalysisServices(ctx context.Context, sub *subscription, cred azcore.TokenCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	client, err := armanalysisservices.NewServersClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armanalysisservices:NewServersClient: %w", err)
	}
	return azSimpleScan(ctx, "armanalysisservices:Servers.List", TypeAnalysisServicesServer, sub, st, scanID,
		client.NewListPager(nil),
		func(p armanalysisservices.ServersClientListResponse) []*armanalysisservices.Server { return p.Value },
		func(s *armanalysisservices.Server) azTrackedBase {
			return azTrackedBase{id: sv(s.ID), name: sv(s.Name), location: sv(s.Location), tags: s.Tags, full: s}
		})
}

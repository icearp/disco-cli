package azure

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/store"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/hanaonazure/armhanaonazure"
)

func init() {
	registerService(serviceEntry{
		name: "azure:hanaonazure",
		fn:   scanHanaOnAzure,
		emits: []coverage.TypeDecl{
			{Service: "microsoft.hanaonazure", DiscoType: TypeHanaOnAzureSapMonitor, Leaf: true},
		},
	})
}

// scanHanaOnAzure discovers HANA-on-Azure SAP monitors.
func scanHanaOnAzure(ctx context.Context, sub *subscription, cred *azidentity.DefaultAzureCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	client, err := armhanaonazure.NewSapMonitorsClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armhanaonazure:NewSapMonitorsClient: %w", err)
	}
	return azSimpleScan(ctx, "armhanaonazure:SapMonitors.List", TypeHanaOnAzureSapMonitor, sub, st, scanID,
		client.NewListPager(nil),
		func(p armhanaonazure.SapMonitorsClientListResponse) []*armhanaonazure.SapMonitor { return p.Value },
		func(m *armhanaonazure.SapMonitor) azTrackedBase {
			return azTrackedBase{id: sv(m.ID), name: sv(m.Name), location: sv(m.Location), tags: m.Tags, full: m}
		})
}

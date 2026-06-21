package azure

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/store"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/chaos/armchaos"
)

func init() {
	registerService(serviceEntry{
		name: "azure:microsoft.chaos",
		fn:   scanChaos,
		emits: []coverage.TypeDecl{
			{Service: "microsoft.chaos", DiscoType: TypeChaosExperiment, Leaf: true},
		},
	})
}

// scanChaos discovers Azure Chaos Studio experiments.
func scanChaos(ctx context.Context, sub *subscription, cred azcore.TokenCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	client, err := armchaos.NewExperimentsClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armchaos:NewExperimentsClient: %w", err)
	}
	return azSimpleScan(ctx, "armchaos:Experiments.ListAll", TypeChaosExperiment, sub, st, scanID,
		client.NewListAllPager(nil),
		func(p armchaos.ExperimentsClientListAllResponse) []*armchaos.Experiment { return p.Value },
		func(e *armchaos.Experiment) azTrackedBase {
			return azTrackedBase{id: sv(e.ID), name: sv(e.Name), location: sv(e.Location), tags: e.Tags, full: e}
		})
}

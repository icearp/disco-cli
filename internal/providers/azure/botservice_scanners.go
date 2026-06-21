package azure

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/store"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/botservice/armbotservice"
)

func init() {
	registerService(serviceEntry{
		name: "azure:microsoft.botservice",
		fn:   scanBotService,
		emits: []coverage.TypeDecl{
			{Service: "microsoft.botservice", DiscoType: TypeBotServiceBot, Leaf: true},
		},
	})
}

// scanBotService discovers Azure Bot Service bots.
func scanBotService(ctx context.Context, sub *subscription, cred *azidentity.DefaultAzureCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	client, err := armbotservice.NewBotsClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armbotservice:NewBotsClient: %w", err)
	}
	return azSimpleScan(ctx, "armbotservice:Bots.List", TypeBotServiceBot, sub, st, scanID,
		client.NewListPager(nil),
		func(p armbotservice.BotsClientListResponse) []*armbotservice.Bot { return p.Value },
		func(b *armbotservice.Bot) azTrackedBase {
			return azTrackedBase{id: sv(b.ID), name: sv(b.Name), location: sv(b.Location), tags: b.Tags, full: b}
		})
}

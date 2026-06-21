package azure

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/store"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/healthbot/armhealthbot"
)

func init() {
	registerService(serviceEntry{
		name: "azure:microsoft.healthbot",
		fn:   scanHealthBot,
		emits: []coverage.TypeDecl{
			// Identity → MSI edges resolved centrally; the bot ships scanner-only.
			{Service: "microsoft.healthbot", DiscoType: TypeHealthBot, Leaf: true},
		},
	})
}

// scanHealthBot discovers Azure Health Bot instances.
func scanHealthBot(ctx context.Context, sub *subscription, cred azcore.TokenCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	client, err := armhealthbot.NewBotsClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armhealthbot:NewBotsClient: %w", err)
	}
	return azSimpleScan(ctx, "armhealthbot:Bots.List", TypeHealthBot, sub, st, scanID,
		client.NewListPager(nil),
		func(p armhealthbot.BotsClientListResponse) []*armhealthbot.HealthBot { return p.Value },
		func(b *armhealthbot.HealthBot) azTrackedBase {
			return azTrackedBase{id: sv(b.ID), name: sv(b.Name), location: sv(b.Location), tags: b.Tags, full: b}
		})
}

package azure

import (
	"context"
	"fmt"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/botservice/armbotservice"
	"github.com/icearp/disco-cli/internal/redact"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerType(restype.Descriptor{Type: TypeBotServiceBot, Service: "microsoft.botservice", Leaf: true, Redact: []redact.Rule{{Path: "properties.luisKey", Mode: redact.RedactScalar}, {Path: "properties.developerAppInsightsApiKey", Mode: redact.RedactScalar}, {Path: "properties.publishingCredentials", Mode: redact.RedactScalar}, {Path: "properties.migrationToken", Mode: redact.RedactScalar}}})
	registerService(serviceEntry{
		name: "azure:microsoft.botservice",
		fn:   scanBotService,
	})
}

// scanBotService discovers Azure Bot Service bots.
func scanBotService(ctx context.Context, sub *subscription, cred azcore.TokenCredential, st *store.Store, scanID string) (total, inserted int, err error) {
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

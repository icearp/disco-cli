package azure

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/store"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/cognitiveservices/armcognitiveservices"
)

func init() {
	registerService(serviceEntry{
		name: "azure:cognitiveservices",
		fn:   scanCognitiveServices,
		emits: []coverage.TypeDecl{
			{Service: "microsoft.cognitiveservices", DiscoType: TypeCognitiveServicesAccount},
		},
	})
}

// scanCognitiveServices discovers Azure AI / OpenAI (Cognitive Services) accounts.
func scanCognitiveServices(ctx context.Context, sub *subscription, cred *azidentity.DefaultAzureCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	client, err := armcognitiveservices.NewAccountsClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armcognitiveservices:NewAccountsClient: %w", err)
	}
	return azSimpleScan(ctx, "armcognitiveservices:Accounts.List", TypeCognitiveServicesAccount, sub, st, scanID,
		client.NewListPager(nil),
		func(p armcognitiveservices.AccountsClientListResponse) []*armcognitiveservices.Account {
			return p.Value
		},
		func(a *armcognitiveservices.Account) azTrackedBase {
			return azTrackedBase{id: sv(a.ID), name: sv(a.Name), location: sv(a.Location), tags: a.Tags, full: a}
		})
}

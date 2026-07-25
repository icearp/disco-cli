package azure

import (
	"context"
	"fmt"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/cognitiveservices/armcognitiveservices"
	"github.com/icearp/disco-cli/internal/redact"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerType(restype.Descriptor{Type: TypeCognitiveServicesAccount, Service: "microsoft.cognitiveservices", Redact: []redact.Rule{{Path: "properties.apiProperties.eventHubConnectionString", Mode: redact.RedactScalar}, {Path: "properties.apiProperties.qnaAzureSearchEndpointKey", Mode: redact.RedactScalar}, {Path: "properties.apiProperties.storageAccountConnectionString", Mode: redact.RedactScalar}, {Path: "properties.migrationToken", Mode: redact.RedactScalar}}})
	registerType(restype.Descriptor{Type: TypeCognitiveCommitmentPlan, Service: "microsoft.cognitiveservices", Leaf: true})
	registerService(serviceEntry{
		name: "azure:microsoft.cognitiveservices",
		fn:   scanCognitiveServices,
	})
}

// scanCognitiveServices discovers Azure AI / OpenAI (Cognitive Services)
// accounts and subscription-wide commitment plans.
func scanCognitiveServices(ctx context.Context, sub *subscription, cred azcore.TokenCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	accounts, err := armcognitiveservices.NewAccountsClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armcognitiveservices:NewAccountsClient: %w", err)
	}
	plans, err := armcognitiveservices.NewCommitmentPlansClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armcognitiveservices:NewCommitmentPlansClient: %w", err)
	}
	return azRunPhases(
		func() (int, int, error) {
			return azSimpleScan(ctx, "armcognitiveservices:Accounts.List", TypeCognitiveServicesAccount, sub, st, scanID,
				accounts.NewListPager(nil),
				func(p armcognitiveservices.AccountsClientListResponse) []*armcognitiveservices.Account {
					return p.Value
				},
				func(a *armcognitiveservices.Account) azTrackedBase {
					return azTrackedBase{id: sv(a.ID), name: sv(a.Name), location: sv(a.Location), tags: a.Tags, full: a}
				})
		},
		func() (int, int, error) {
			return azSimpleScan(ctx, "armcognitiveservices:CommitmentPlans.ListPlansBySubscription", TypeCognitiveCommitmentPlan, sub, st, scanID,
				plans.NewListPlansBySubscriptionPager(nil),
				func(p armcognitiveservices.CommitmentPlansClientListPlansBySubscriptionResponse) []*armcognitiveservices.CommitmentPlan {
					return p.Value
				},
				func(c *armcognitiveservices.CommitmentPlan) azTrackedBase {
					return azTrackedBase{id: sv(c.ID), name: sv(c.Name), location: sv(c.Location), tags: c.Tags, full: c}
				})
		},
	)
}

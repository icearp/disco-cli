package azure

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/store"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/elasticsan/armelasticsan"
)

func init() {
	registerService(serviceEntry{
		name: "azure:microsoft.elasticsan",
		fn:   scanElasticSan,
		emits: []coverage.TypeDecl{
			// Private-endpoint → target edges resolved centrally; CMK/network
			// settings live on the child VolumeGroup (not scanned), so the SAN
			// itself ships scanner-only.
			{Service: "microsoft.elasticsan", DiscoType: TypeElasticSan, Leaf: true},
		},
	})
}

// scanElasticSan discovers Azure Elastic SAN resources.
func scanElasticSan(ctx context.Context, sub *subscription, cred azcore.TokenCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	client, err := armelasticsan.NewElasticSansClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armelasticsan:NewElasticSansClient: %w", err)
	}
	return azSimpleScan(ctx, "armelasticsan:ElasticSans.ListBySubscription", TypeElasticSan, sub, st, scanID,
		client.NewListBySubscriptionPager(nil),
		func(p armelasticsan.ElasticSansClientListBySubscriptionResponse) []*armelasticsan.ElasticSan {
			return p.Value
		},
		func(s *armelasticsan.ElasticSan) azTrackedBase {
			return azTrackedBase{id: sv(s.ID), name: sv(s.Name), location: sv(s.Location), tags: s.Tags, full: s}
		})
}

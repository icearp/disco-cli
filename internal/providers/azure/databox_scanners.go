package azure

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/store"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/databox/armdatabox"
)

func init() {
	registerService(serviceEntry{
		name: "azure:databox",
		fn:   scanDataBox,
		emits: []coverage.TypeDecl{
			// Job destinations reference storage accounts only under the
			// expand=details filter (not fetched here), so this ships
			// scanner-only.
			{Service: "microsoft.databox", DiscoType: TypeDataBoxJob, Leaf: true},
		},
	})
}

// scanDataBox discovers Azure Data Box import/export jobs.
func scanDataBox(ctx context.Context, sub *subscription, cred *azidentity.DefaultAzureCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	client, err := armdatabox.NewJobsClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armdatabox:NewJobsClient: %w", err)
	}
	return azSimpleScan(ctx, "armdatabox:Jobs.List", TypeDataBoxJob, sub, st, scanID,
		client.NewListPager(nil),
		func(p armdatabox.JobsClientListResponse) []*armdatabox.JobResource { return p.Value },
		func(j *armdatabox.JobResource) azTrackedBase {
			return azTrackedBase{id: sv(j.ID), name: sv(j.Name), location: sv(j.Location), tags: j.Tags, full: j}
		})
}

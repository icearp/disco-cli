package azure

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/store"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/streamanalytics/armstreamanalytics"
)

func init() {
	registerService(serviceEntry{
		name: "azure:streamanalytics",
		fn:   scanStreamAnalytics,
		emits: []coverage.TypeDecl{
			// Inputs/outputs are child config, not standalone resources;
			// identity edges resolve centrally.
			{Service: "microsoft.streamanalytics", DiscoType: TypeStreamAnalyticsJob, Leaf: true},
		},
	})
}

// scanStreamAnalytics discovers Azure Stream Analytics streaming jobs.
func scanStreamAnalytics(ctx context.Context, sub *subscription, cred *azidentity.DefaultAzureCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	client, err := armstreamanalytics.NewStreamingJobsClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armstreamanalytics:NewStreamingJobsClient: %w", err)
	}
	return azSimpleScan(ctx, "armstreamanalytics:StreamingJobs.List", TypeStreamAnalyticsJob, sub, st, scanID,
		client.NewListPager(nil),
		func(p armstreamanalytics.StreamingJobsClientListResponse) []*armstreamanalytics.StreamingJob {
			return p.Value
		},
		func(j *armstreamanalytics.StreamingJob) azTrackedBase {
			return azTrackedBase{id: sv(j.ID), name: sv(j.Name), location: sv(j.Location), tags: j.Tags, full: j}
		})
}

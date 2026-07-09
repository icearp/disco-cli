package azure

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/redact"
	"codeberg.org/icearp/disco/internal/restype"
	"codeberg.org/icearp/disco/store"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/streamanalytics/armstreamanalytics"
)

func init() {
	registerType(restype.Descriptor{Type: TypeStreamAnalyticsJob, Service: "microsoft.streamanalytics", Leaf: true, Redact: []redact.Rule{{Path: "properties.jobStorageAccount.accountKey", Mode: redact.RedactScalar}}})
	registerType(restype.Descriptor{Type: TypeStreamAnalyticsCluster, Service: "microsoft.streamanalytics", Leaf: true})
	registerService(serviceEntry{
		name: "azure:microsoft.streamanalytics",
		fn:   scanStreamAnalytics,
	})
}

// scanStreamAnalytics discovers Azure Stream Analytics streaming jobs and clusters.
func scanStreamAnalytics(ctx context.Context, sub *subscription, cred azcore.TokenCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	jobs, err := armstreamanalytics.NewStreamingJobsClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armstreamanalytics:NewStreamingJobsClient: %w", err)
	}
	clusters, err := armstreamanalytics.NewClustersClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armstreamanalytics:NewClustersClient: %w", err)
	}
	return azRunPhases(
		func() (int, int, error) {
			return azSimpleScan(ctx, "armstreamanalytics:StreamingJobs.List", TypeStreamAnalyticsJob, sub, st, scanID,
				jobs.NewListPager(nil),
				func(p armstreamanalytics.StreamingJobsClientListResponse) []*armstreamanalytics.StreamingJob {
					return p.Value
				},
				func(j *armstreamanalytics.StreamingJob) azTrackedBase {
					return azTrackedBase{id: sv(j.ID), name: sv(j.Name), location: sv(j.Location), tags: j.Tags, full: j}
				})
		},
		func() (int, int, error) {
			return azSimpleScan(ctx, "armstreamanalytics:Clusters.ListBySubscription", TypeStreamAnalyticsCluster, sub, st, scanID,
				clusters.NewListBySubscriptionPager(nil),
				func(p armstreamanalytics.ClustersClientListBySubscriptionResponse) []*armstreamanalytics.Cluster {
					return p.Value
				},
				func(c *armstreamanalytics.Cluster) azTrackedBase {
					return azTrackedBase{id: sv(c.ID), name: sv(c.Name), location: sv(c.Location), tags: c.Tags, full: c}
				})
		},
	)
}

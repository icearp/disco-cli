package azure

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/restype"
	"codeberg.org/icearp/disco/store"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/databox/armdatabox"
)

func init() {
	registerType(restype.Descriptor{Type: TypeDataBoxJob, Service: "microsoft.databox", Leaf: true})
	registerService(serviceEntry{
		name: "azure:microsoft.databox",
		fn:   scanDataBox,
	})
}

// scanDataBox discovers Azure Data Box import/export jobs.
func scanDataBox(ctx context.Context, sub *subscription, cred azcore.TokenCredential, st *store.Store, scanID string) (total, inserted int, err error) {
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

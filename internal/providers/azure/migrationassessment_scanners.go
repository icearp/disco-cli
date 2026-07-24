package azure

import (
	"context"
	"fmt"

	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/migrationassessment/armmigrationassessment"
)

func init() {
	registerType(restype.Descriptor{Type: TypeMigrateAssessmentProject, Service: "microsoft.migrate", Leaf: true})
	registerService(serviceEntry{
		name: "azure:microsoft.migrate",
		fn:   scanMigrationAssessment,
	})
}

// scanMigrationAssessment discovers migrationassessment resources.
func scanMigrationAssessment(ctx context.Context, sub *subscription, cred azcore.TokenCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	client, err := armmigrationassessment.NewAssessmentProjectsOperationsClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armmigrationassessment:NewAssessmentProjectsOperationsClient: %w", err)
	}
	return azSimpleScan(ctx, "armmigrationassessment:AssessmentProjects.ListBySubscription", TypeMigrateAssessmentProject, sub, st, scanID,
		client.NewListBySubscriptionPager(nil),
		func(p armmigrationassessment.AssessmentProjectsOperationsClientListBySubscriptionResponse) []*armmigrationassessment.AssessmentProject {
			return p.Value
		},
		func(r *armmigrationassessment.AssessmentProject) azTrackedBase {
			return azTrackedBase{id: sv(r.ID), name: sv(r.Name), location: sv(r.Location), tags: r.Tags, full: r}
		})
}

package aws

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/store"
	"github.com/aws/aws-sdk-go-v2/service/deadline"
)

// scanDeadlineBudgets lists the per-farm spending budgets. The budget attaches
// to its farm (NativeID parse); its UsageTrackingResource (queue) is a smithy
// union without a clean discriminator field, so that edge is not wired.
func scanDeadlineBudgets(ctx context.Context, client deadlineAPI, acct *account, region string, st *store.Store, scanID string, fr *dlFarmRef) (int, int, error) {
	pager := deadline.NewListBudgetsPaginator(client, &deadline.ListBudgetsInput{FarmId: &fr.id})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "deadline:ListBudgets", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("deadline:ListBudgets: %w", perr)
		}
		for _, b := range out.Budgets {
			id := sv(b.BudgetId)
			if id == "" {
				continue
			}
			arn := fmt.Sprintf("%s/budget/%s", fr.arn, id)
			label := sv(b.DisplayName)
			if label == "" {
				label = id
			}
			status := string(b.Status)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeDeadlineBudget, NativeID: arn,
				Name: &label, Region: &region, Status: &status,
				AttributesJSON: mustJSON(b), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "deadline budgets")
}

// scanDeadlineVolumes lists the storage volumes of one fleet. ListVolumes
// requires both FarmId and FleetId, so it fans out per (farm, fleet).
func scanDeadlineVolumes(ctx context.Context, client deadlineAPI, acct *account, region string, st *store.Store, scanID string, fr *dlFarmRef, fleetID string) (int, int, error) {
	pager := deadline.NewListVolumesPaginator(client, &deadline.ListVolumesInput{FarmId: &fr.id, FleetId: &fleetID})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "deadline:ListVolumes", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("deadline:ListVolumes: %w", perr)
		}
		for _, v := range out.Volumes {
			id := sv(v.VolumeId)
			if id == "" {
				continue
			}
			arn := fmt.Sprintf("%s/volume/%s", fr.arn, id)
			status := string(v.State)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeDeadlineVolume, NativeID: arn,
				Name: &id, Region: &region, Status: &status,
				AttributesJSON: mustJSON(v), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "deadline volumes")
}

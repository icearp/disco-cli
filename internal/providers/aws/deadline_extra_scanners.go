package aws

import (
	"cmp"
	"context"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/deadline"
	dltypes "github.com/aws/aws-sdk-go-v2/service/deadline/types"
	"github.com/icearp/disco-cli/store"
)

// scanDeadlineBudgets lists the spending budgets of each farm the farm phase
// stored and stores them with their tags. A farm ListBudgets no longer finds
// is skipped. The budget attaches to its farm (NativeID parse); its
// UsageTrackingResource (queue) is a smithy union without a clean
// discriminator field, so that edge is not wired.
func scanDeadlineBudgets(ctx context.Context, client deadlineAPI, acct *account, region string, st *store.Store, scanID string, farms []dlFarmRef, tags rowTagReader, p *phaseTotals) (storeWriteFailed bool) {
	_, stop := storeEachParentTags(ctx, st, acct, region, "deadline:ListBudgets", deadlineNotFound, deadlineFarmParents(farms),
		func(ctx context.Context, farmID string, token *string) ([]dltypes.BudgetSummary, *string, error) {
			out, err := client.ListBudgets(ctx, &deadline.ListBudgetsInput{FarmId: &farmID, NextToken: token, MaxResults: sdkaws.Int32(deadlineMaxResults)})
			if err != nil {
				return nil, nil, err
			}
			return out.Budgets, out.NextToken, nil
		},
		func(farmID string, b dltypes.BudgetSummary) (*store.Resource, string, string) {
			id := sv(b.BudgetId)
			if id == "" {
				return nil, "budgets have an empty BudgetId", cmp.Or(sv(b.DisplayName), "(a budget with no name)") + " of farm " + farmID
			}
			r := deadlineRow(acct, region, scanID, TypeDeadlineBudget, deadlineFarmARN(acct.partition(), region, acct.ID, farmID)+"/budget/"+id, cmp.Or(sv(b.DisplayName), id), b)
			r.Status = nonEmptyPtr(string(b.Status))
			return r, "", ""
		}, tags, p)
	return stop
}

// scanDeadlineVolumes lists the storage volumes of each fleet the fleet phase
// stored and stores them with their tags, each under the ARN the Service
// Reference documents for a volume: its fleet's ARN, then volume/{VolumeId}.
// ListVolumes takes both FarmId and FleetId; a fleet it no longer finds is
// skipped.
func scanDeadlineVolumes(ctx context.Context, client deadlineAPI, acct *account, region string, st *store.Store, scanID string, farms []dlFarmRef, tags rowTagReader, p *phaseTotals) (storeWriteFailed bool) {
	fleets, keys := deadlineFarmChildren(farms, func(f dlFarmRef) []childParent { return f.fleets })
	_, stop := storeEachParentTags(ctx, st, acct, region, "deadline:ListVolumes", deadlineNotFound, fleets,
		func(ctx context.Context, fleetARN string, token *string) ([]dltypes.VolumeSummary, *string, error) {
			k := keys[fleetARN]
			out, err := client.ListVolumes(ctx, &deadline.ListVolumesInput{
				FarmId: &k.farmID, FleetId: &k.id, NextToken: token, MaxResults: sdkaws.Int32(deadlineMaxResults),
			})
			if err != nil {
				return nil, nil, err
			}
			return out.Volumes, out.NextToken, nil
		},
		func(fleetARN string, v dltypes.VolumeSummary) (*store.Resource, string, string) {
			id := sv(v.VolumeId)
			if id == "" {
				return nil, "volumes have an empty VolumeId", "(a volume with no id) of fleet " + fleetARN
			}
			r := deadlineRow(acct, region, scanID, TypeDeadlineVolume, fleetARN+"/volume/"+id, id, v)
			r.Status = nonEmptyPtr(string(v.State))
			return r, "", ""
		}, tags, p)
	return stop
}

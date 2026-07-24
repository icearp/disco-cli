package aws

import (
	"context"
	"fmt"

	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
	"github.com/aws/aws-sdk-go-v2/service/bcmpricingcalculator"
)

// isCostExplorerNotEnabled disambiguates the "User not enabled for cost
// explorer access" / linked-account state from a real IAM denial. Shared by
// BCM Pricing Calculator + Cost Explorer scanners — both gate on the same
// account-level Cost Explorer enablement.
func isCostExplorerNotEnabled(err error) bool {
	return isAccessDeniedWithMessage(err, "not enabled for cost explorer") ||
		isAccessDeniedWithMessage(err, "doesn't have access to cost category")
}

// isMigrationRequiredIAMDeny matches the canned AccessDeniedException AWS
// returns for accounts whose IAM policies still reference the legacy action
// names of a service that has migrated to a new IAM action surface. Body:
// "To use this feature, please obtain the required permissions. Migrate
// the policies in your account to use the new IAM actions." Pre-migration
// is environment policy, not a misconfig of this scanner — silent-skip.
func isMigrationRequiredIAMDeny(err error) bool {
	return isAccessDeniedWithMessage(err, "Migrate the policies in your account to use the new IAM actions")
}

// AWS BCM Pricing Calculator is account-global — the SDK endpoint resolves
// only via us-east-1 (mirrors aws:billing / aws:ce).
const bcmPricingCalculatorRegion = "us-east-1"

func init() {
	registerType(restype.Descriptor{Type: TypeBcmPricingCalculatorBillScenario, Service: "bcmpricingcalculator", Upstream: "AWS::BcmPricingCalculator::BillScenario", Leaf: true})
	registerType(restype.Descriptor{Type: TypeBcmPricingCalculatorBillEstimate, Service: "bcmpricingcalculator", Upstream: "AWS::bcm-pricing-calculator::bill-estimate", Leaf: true})
	registerType(restype.Descriptor{Type: TypeBcmPricingCalculatorWorkloadEstimate, Service: "bcmpricingcalculator", Upstream: "AWS::bcm-pricing-calculator::workload-estimate", Leaf: true})
	registerService(serviceEntry{
		name:   "aws:bcmpricingcalculator",
		global: true,
		fn: func(ctx context.Context, acct *account, _ string, st *store.Store, scanID string) (int, int, error) {
			client := bcmpricingcalculator.NewFromConfig(acct.cfg, func(o *bcmpricingcalculator.Options) { o.Region = bcmPricingCalculatorRegion })
			return scanBcmPricingCalculator(ctx, client, acct, st, scanID)
		},
	})
}

// bcmPricingCalculatorAPI is the narrow surface scanBcmPricingCalculator uses.
type bcmPricingCalculatorAPI interface {
	ListBillScenarios(context.Context, *bcmpricingcalculator.ListBillScenariosInput, ...func(*bcmpricingcalculator.Options)) (*bcmpricingcalculator.ListBillScenariosOutput, error)
	ListBillEstimates(context.Context, *bcmpricingcalculator.ListBillEstimatesInput, ...func(*bcmpricingcalculator.Options)) (*bcmpricingcalculator.ListBillEstimatesOutput, error)
	ListWorkloadEstimates(context.Context, *bcmpricingcalculator.ListWorkloadEstimatesInput, ...func(*bcmpricingcalculator.Options)) (*bcmpricingcalculator.ListWorkloadEstimatesOutput, error)
}

// bcmPricingCalculatorNativeID synthesizes the canonical ARN for a pricing-
// calculator artifact (the SDK summaries carry only an Id). The service is
// account-global, so the ARN's region segment is empty:
// arn:aws:bcm-pricing-calculator::{acct}:{kind}/{id}.
func bcmPricingCalculatorNativeID(acct, kind, id string) string {
	return fmt.Sprintf("arn:aws:bcm-pricing-calculator::%s:%s/%s", acct, kind, id)
}

// bcmPCListErr classifies a BCM Pricing Calculator List* page error into the
// value a phase returns: markServiceDisabled when the account hasn't enabled
// Cost Explorer (self-enableable → (account: disabled)); markServiceNotEntitled
// for the payer-only topology gate (member account can't self-enable →
// (account: not entitled)); nil for the migration-required IAM deny and the
// soft access-denied skip (orchestrator continues to next phase); or a
// wrapped fatal error otherwise.
func bcmPCListErr(st *store.Store, op, acctID, region string, perr error) error {
	switch {
	case isCostExplorerNotEnabled(perr):
		return markServiceDisabled(perr)
	case isPayerAccountOnly(perr):
		return markServiceNotEntitled(perr)
	case isMigrationRequiredIAMDeny(perr):
		return nil
	case isAccessDenied(perr):
		return skipIfAccessDenied(st, op, acctID, region, perr)
	default:
		return fmt.Errorf("%s: %w", op, perr)
	}
}

func scanBcmPricingCalculator(ctx context.Context, client bcmPricingCalculatorAPI, acct *account, st *store.Store, scanID string) (total, inserted int, err error) {
	region := bcmPricingCalculatorRegion
	for _, phase := range []func() (int, int, error){
		func() (int, int, error) {
			return scanBcmPricingCalculatorBillScenarios(ctx, client, acct, region, st, scanID)
		},
		func() (int, int, error) {
			return scanBcmPricingCalculatorBillEstimates(ctx, client, acct, region, st, scanID)
		},
		func() (int, int, error) {
			return scanBcmPricingCalculatorWorkloadEstimates(ctx, client, acct, region, st, scanID)
		},
	} {
		t, i, ferr := phase()
		if ferr != nil {
			return total, inserted, ferr
		}
		total += t
		inserted += i
	}
	return total, inserted, nil
}

func scanBcmPricingCalculatorBillScenarios(ctx context.Context, client bcmPricingCalculatorAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := bcmpricingcalculator.NewListBillScenariosPaginator(client, &bcmpricingcalculator.ListBillScenariosInput{})
	var rows []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			return 0, 0, bcmPCListErr(st, "bcmpricingcalculator:ListBillScenarios", acct.ID, region, perr)
		}
		for _, s := range out.Items {
			id := sv(s.Id)
			if id == "" {
				continue
			}
			name := sv(s.Name)
			status := string(s.Status)
			rows = append(rows, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type:     TypeBcmPricingCalculatorBillScenario,
				NativeID: bcmPricingCalculatorNativeID(acct.ID, "bill-scenario", id),
				Name:     &name, Region: regionGlobal, Status: &status, CreatedAt: tp(s.CreatedAt),
				AttributesJSON: mustJSON(s), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, rows, "bcmpricingcalculator bill-scenarios")
}

func scanBcmPricingCalculatorBillEstimates(ctx context.Context, client bcmPricingCalculatorAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := bcmpricingcalculator.NewListBillEstimatesPaginator(client, &bcmpricingcalculator.ListBillEstimatesInput{})
	var rows []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			return 0, 0, bcmPCListErr(st, "bcmpricingcalculator:ListBillEstimates", acct.ID, region, perr)
		}
		for _, e := range out.Items {
			id := sv(e.Id)
			if id == "" {
				continue
			}
			name := sv(e.Name)
			status := string(e.Status)
			rows = append(rows, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type:     TypeBcmPricingCalculatorBillEstimate,
				NativeID: bcmPricingCalculatorNativeID(acct.ID, "bill-estimate", id),
				Name:     &name, Region: regionGlobal, Status: &status, CreatedAt: tp(e.CreatedAt),
				AttributesJSON: mustJSON(e), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, rows, "bcmpricingcalculator bill-estimates")
}

func scanBcmPricingCalculatorWorkloadEstimates(ctx context.Context, client bcmPricingCalculatorAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := bcmpricingcalculator.NewListWorkloadEstimatesPaginator(client, &bcmpricingcalculator.ListWorkloadEstimatesInput{})
	var rows []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			return 0, 0, bcmPCListErr(st, "bcmpricingcalculator:ListWorkloadEstimates", acct.ID, region, perr)
		}
		for _, e := range out.Items {
			id := sv(e.Id)
			if id == "" {
				continue
			}
			name := sv(e.Name)
			status := string(e.Status)
			rows = append(rows, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type:     TypeBcmPricingCalculatorWorkloadEstimate,
				NativeID: bcmPricingCalculatorNativeID(acct.ID, "workload-estimate", id),
				Name:     &name, Region: regionGlobal, Status: &status, CreatedAt: tp(e.CreatedAt),
				AttributesJSON: mustJSON(e), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, rows, "bcmpricingcalculator workload-estimates")
}

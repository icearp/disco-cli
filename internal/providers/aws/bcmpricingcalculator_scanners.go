package aws

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/internal/store"
	"github.com/aws/aws-sdk-go-v2/service/bcmpricingcalculator"
)

// isCostExplorerNotEnabled disambiguates the "User not enabled for cost
// explorer access" / linked-account state from a real IAM denial. Shared by
// BCM Pricing Calculator + Cost Explorer scanners since both gate on the
// same account-level Cost Explorer enablement.
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

func init() {
	registerService(serviceEntry{
		name: "aws:bcmpricingcalculator",
		fn:   scanBcmPricingCalculator,
		emits: []coverage.TypeDecl{
			{Service: "bcmpricingcalculator", DiscoType: TypeBcmPricingCalculatorBillScenario},
		},
	})
}

// bcmPricingCalculatorAPI is the narrow surface scanBcmPricingCalculator uses.
type bcmPricingCalculatorAPI interface {
	ListBillScenarios(context.Context, *bcmpricingcalculator.ListBillScenariosInput, ...func(*bcmpricingcalculator.Options)) (*bcmpricingcalculator.ListBillScenariosOutput, error)
}

// bcmPricingCalculatorBillScenarioNativeID synthesizes the bill-scenario
// ARN. The SDK summary only carries an Id; the canonical AWS ARN for the
// resource is `arn:aws:bcm-pricing-calculator:{region}:{acct}:bill-scenario/{id}`.
func bcmPricingCalculatorBillScenarioNativeID(region, acct, id string) string {
	return fmt.Sprintf("arn:aws:bcm-pricing-calculator:%s:%s:bill-scenario/%s", region, acct, id)
}

func scanBcmPricingCalculator(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := bcmpricingcalculator.NewFromConfig(acct.cfg, func(o *bcmpricingcalculator.Options) { o.Region = region })
	return scanBcmPricingCalculatorBillScenarios(ctx, client, acct, region, st, scanID)
}

func scanBcmPricingCalculatorBillScenarios(ctx context.Context, client bcmPricingCalculatorAPI, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	pager := bcmpricingcalculator.NewListBillScenariosPaginator(client, &bcmpricingcalculator.ListBillScenariosInput{})
	var rows []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isCostExplorerNotEnabled(perr) {
				return total, inserted, markServiceDisabled(perr)
			}
			if isMigrationRequiredIAMDeny(perr) {
				return total, inserted, nil
			}
			if isAccessDenied(perr) {
				return total, inserted, skipIfAccessDenied(st, "bcmpricingcalculator:ListBillScenarios", acct.ID, region, perr)
			}
			return total, inserted, fmt.Errorf("bcmpricingcalculator:ListBillScenarios: %w", perr)
		}
		for _, s := range out.Items {
			id := sv(s.Id)
			if id == "" {
				continue
			}
			arn := bcmPricingCalculatorBillScenarioNativeID(region, acct.ID, id)
			name := sv(s.Name)
			status := string(s.Status)
			rows = append(rows, &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeBcmPricingCalculatorBillScenario,
				NativeID:       arn,
				Name:           &name,
				Region:         &region,
				Status:         &status,
				CreatedAt:      tp(s.CreatedAt),
				AttributesJSON: mustJSON(s),
				DiscoveredBy:   scanID,
			})
		}
	}
	if len(rows) == 0 {
		return 0, 0, nil
	}
	n, uerr := st.UpsertResources(rows)
	if uerr != nil {
		return total, inserted, fmt.Errorf("upsert bcmpricingcalculator bill scenarios: %w", uerr)
	}
	return len(rows), n, nil
}

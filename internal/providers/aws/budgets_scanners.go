package aws

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/store"
	"github.com/aws/aws-sdk-go-v2/service/budgets"
)

// isBudgetsLinkedAccount disambiguates the "linked account, ask payer to
// enable budgets" state from a real IAM denial.
func isBudgetsLinkedAccount(err error) bool {
	return isAccessDeniedWithMessage(err, "linked account")
}

func init() {
	registerService(serviceEntry{
		name:   "aws:budgets",
		global: true,
		fn:     scanBudgets,
		emits: []coverage.TypeDecl{
			{Service: "budgets", DiscoType: TypeBudgetsBudget, Leaf: true},
			{Service: "budgets", DiscoType: TypeBudgetsBudgetsAction, Leaf: true},
		},
	})
}

type budgetsAPI interface {
	DescribeBudgets(context.Context, *budgets.DescribeBudgetsInput, ...func(*budgets.Options)) (*budgets.DescribeBudgetsOutput, error)
	DescribeBudgetActionsForAccount(context.Context, *budgets.DescribeBudgetActionsForAccountInput, ...func(*budgets.Options)) (*budgets.DescribeBudgetActionsForAccountOutput, error)
}

// scanBudgets discovers Budgets budgets and budget actions. Budgets is a
// global service; gate to us-east-1 to avoid duplicate scans across regions.
func scanBudgets(ctx context.Context, acct *account, _ string, st *store.Store, scanID string) (total, inserted int, err error) {
	region := "us-east-1"
	client := budgets.NewFromConfig(acct.cfg, func(o *budgets.Options) { o.Region = region })

	t, i, ferr := scanBudgetsBudgets(ctx, client, acct, region, st, scanID)
	if ferr != nil {
		return total, inserted, ferr
	}
	total += t
	inserted += i

	t, i, ferr = scanBudgetsActions(ctx, client, acct, region, st, scanID)
	if ferr != nil {
		return total, inserted, ferr
	}
	total += t
	inserted += i
	return total, inserted, nil
}

func scanBudgetsBudgets(ctx context.Context, client budgetsAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var batch []*store.Resource
	var nextToken *string
	for {
		out, err := client.DescribeBudgets(ctx, &budgets.DescribeBudgetsInput{
			AccountId: &acct.ID,
			NextToken: nextToken,
		})
		if err != nil {
			if isBudgetsLinkedAccount(err) {
				return 0, 0, markServiceDisabled(err)
			}
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "budgets:DescribeBudgets", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("budgets:DescribeBudgets: %w", err)
		}
		for _, b := range out.Budgets {
			name := sv(b.BudgetName)
			if name == "" {
				continue
			}
			arn := fmt.Sprintf("arn:aws:budgets::%s:budget/%s", acct.ID, name)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeBudgetsBudget, NativeID: arn,
				Name: &name, Region: regionGlobal,
				AttributesJSON: mustJSON(b), DiscoveredBy: scanID,
			})
		}
		if out.NextToken == nil || *out.NextToken == "" {
			break
		}
		nextToken = out.NextToken
	}
	return upsertBatch(st, batch, "budgets")
}

func scanBudgetsActions(ctx context.Context, client budgetsAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var batch []*store.Resource
	var nextToken *string
	for {
		out, err := client.DescribeBudgetActionsForAccount(ctx, &budgets.DescribeBudgetActionsForAccountInput{
			AccountId: &acct.ID,
			NextToken: nextToken,
		})
		if err != nil {
			if isBudgetsLinkedAccount(err) {
				return 0, 0, markServiceDisabled(err)
			}
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "budgets:DescribeBudgetActionsForAccount", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("budgets:DescribeBudgetActionsForAccount: %w", err)
		}
		for _, a := range out.Actions {
			budgetName := sv(a.BudgetName)
			actionID := sv(a.ActionId)
			if budgetName == "" || actionID == "" {
				continue
			}
			arn := fmt.Sprintf("arn:aws:budgets::%s:budget/%s/action/%s", acct.ID, budgetName, actionID)
			label := actionID
			status := string(a.Status)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeBudgetsBudgetsAction, NativeID: arn,
				Name: &label, Region: regionGlobal, Status: &status,
				AttributesJSON: mustJSON(a), DiscoveredBy: scanID,
			})
		}
		if out.NextToken == nil || *out.NextToken == "" {
			break
		}
		nextToken = out.NextToken
	}
	return upsertBatch(st, batch, "budgets budgets-actions")
}

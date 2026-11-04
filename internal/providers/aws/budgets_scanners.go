package aws

import (
	"cmp"
	"context"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/budgets"
	budgetstypes "github.com/aws/aws-sdk-go-v2/service/budgets/types"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

// isBudgetsLinkedAccount disambiguates the "linked account, ask payer to
// enable budgets" state from a real IAM denial. A linked (member) account
// can't self-enable budgets — the payer must — so callers mark it
// not-entitled, not disabled.
func isBudgetsLinkedAccount(err error) bool {
	return isAccessDeniedWithMessage(err, "linked account")
}

func init() {
	registerType(restype.Descriptor{Type: TypeBudgetsBudget, Service: "budgets"})
	registerType(restype.Descriptor{Type: TypeBudgetsBudgetsAction, Service: "budgets"})
	registerService(serviceEntry{
		name:   "aws:budgets",
		global: true,
		fn:     scanBudgets,
	})
}

type budgetsAPI interface {
	DescribeBudgets(context.Context, *budgets.DescribeBudgetsInput, ...func(*budgets.Options)) (*budgets.DescribeBudgetsOutput, error)
	DescribeBudgetActionsForAccount(context.Context, *budgets.DescribeBudgetActionsForAccountInput, ...func(*budgets.Options)) (*budgets.DescribeBudgetActionsForAccountOutput, error)
	ListTagsForResource(context.Context, *budgets.ListTagsForResourceInput, ...func(*budgets.Options)) (*budgets.ListTagsForResourceOutput, error)
}

const (
	budgetsDescribeBudgetsOp = "budgets:DescribeBudgets"
	budgetsDescribeActionsOp = "budgets:DescribeBudgetActionsForAccount"
	budgetsTagsOp            = "budgets:ListTagsForResource"
	// budgetsBudgetsPageCap is DescribeBudgets' MaxResults maximum (the
	// model's range is 1-1000).
	budgetsBudgetsPageCap int32 = 1000
	// budgetsActionsPageCap is DescribeBudgetActionsForAccount's MaxResults
	// maximum (the model's range is 1-100).
	budgetsActionsPageCap int32 = 100
)

// scanBudgets discovers budgets and budget actions, each with its tags
// (budgetsTags). Global service; called once, in its home region
// (homeRegion). The two listings run as phases, one at a time and in that
// order: the first one's error does not stop the second, except an NXDOMAIN
// with nothing stored, which reports the region unavailable; a failed write
// and ctx's end stop it too (runHostPhases). Each listing is written a page
// at a time, and the tag reads' denials are one warning for the whole scan.
//
// The linked-account refusal (isBudgetsLinkedAccount) of a listing's first
// page while the scan has stored no row reports the account not entitled,
// and the actions listing does not run after it (notEntitledGate). An
// earlier listing's denial is still warned about.
func scanBudgets(ctx context.Context, acct *account, _ string, st *store.Store, scanID string) (int, int, error) {
	region, err := acct.homeRegion("budgets")
	if err != nil {
		return 0, 0, err
	}
	client := budgets.NewFromConfig(acct.cfg, func(o *budgets.Options) { o.Region = region })

	linked := &notEntitledGate{is: isBudgetsLinkedAccount}
	tags := budgetsTags(client).sharingDenials()
	total, inserted, err := runHostPhases(ctx, st, acct, region, "budgets", 0, []hostPhase{
		linked.gatedPhase(budgetsDescribeBudgetsOp, func(p *phaseTotals) bool {
			return scanBudgetsBudgets(ctx, client, acct, region, st, scanID, linked, tags, p)
		}),
		linked.gatedPhase(budgetsDescribeActionsOp, func(p *phaseTotals) bool {
			return scanBudgetsActions(ctx, client, acct, region, st, scanID, linked, tags, p)
		}),
	})
	if notEntitled := linked.notEntitledErr(st, acct, region, err); notEntitled != nil {
		return total, inserted, notEntitled
	}
	tags.warnDenials(st, acct, region)
	return total, inserted, err
}

// budgetsTags reads a row's tags with ListTagsForResource, sent the row's
// NativeID: the ARN budgetARN builds, because neither listed element
// carries an ARN or tags. The Service Reference lists budget
// (budget/${BudgetName}) and budgetAction
// (budget/${BudgetName}/action/${ActionId}) under ListTagsForResource and
// TagResource, each with aws:ResourceTag as a condition key, and CreateBudget
// and CreateBudgetAction take ResourceTags. The op's ResourceARN has no
// pattern, and a budget name cannot hold ":" or "\" (its model pattern), so
// the name goes into the ARN as listed. The op returns every tag at once.
// NotFoundException, its modeled not-found, drops a resource deleted after it
// was listed.
func budgetsTags(client budgetsAPI) rowTagReader {
	return rowTagReader{
		op:       budgetsTagsOp,
		notFound: []string{"NotFoundException"},
		read: func(ctx context.Context, r *store.Resource) (*string, error) {
			arn := r.NativeID
			out, err := client.ListTagsForResource(ctx, &budgets.ListTagsForResourceInput{ResourceARN: &arn})
			if err != nil {
				return nil, err
			}
			return kvTagsJSON(out.ResourceTags, func(t budgetstypes.ResourceTag) (*string, *string) { return t.Key, t.Value }), nil
		},
	}
}

// budgetARN is the ARN of the budget named name, in the Service Reference's
// format arn:${Partition}:budgets::${Account}:budget/${BudgetName}.
func budgetARN(partition, accountID, name string) string {
	return arnFor(partition, "budgets", "", accountID, "budget/"+name)
}

// budgetRow is the row of a listed budget, stored under budgetARN and named
// by its name. A budget without a name is not stored: its ARN could not be
// built. The listing warns about it.
func budgetRow(acct *account, scanID string, b budgetstypes.Budget) (*store.Resource, string, string) {
	name := sv(b.BudgetName)
	if name == "" {
		return nil, "budgets have no name", noIdentifier
	}
	return &store.Resource{
		Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
		Type: TypeBudgetsBudget, NativeID: budgetARN(acct.partition(), acct.ID, name),
		Name: &name, Region: regionGlobal,
		AttributesJSON: mustJSON(b), DiscoveredBy: scanID,
	}, "", ""
}

// budgetActionRow is the row of a listed budget action, stored under its
// budget's ARN with /action/<id> (the Service Reference's budgetAction
// format), named by its id, with its status as its Status. An action without
// a budget name or id is not stored: its ARN could not be built. The listing
// warns about it.
func budgetActionRow(acct *account, scanID string, a budgetstypes.Action) (*store.Resource, string, string) {
	budgetName, actionID := sv(a.BudgetName), sv(a.ActionId)
	if budgetName == "" || actionID == "" {
		return nil, "budget actions have no budget name or action id", cmp.Or(actionID, budgetName, noIdentifier)
	}
	status := string(a.Status)
	return &store.Resource{
		Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
		Type: TypeBudgetsBudgetsAction, NativeID: budgetARN(acct.partition(), acct.ID, budgetName) + "/action/" + actionID,
		Name: &actionID, Region: regionGlobal, Status: &status,
		AttributesJSON: mustJSON(a), DiscoveredBy: scanID,
	}, "", ""
}

// scanBudgetsBudgets stores the account's budgets, each page written as soon
// as its rows' tags are read. A refusal linked keeps is read as an empty
// listing. It reports whether a write failed, after which the scan
// must stop.
func scanBudgetsBudgets(ctx context.Context, client budgetsAPI, acct *account, region string, st *store.Store, scanID string, linked *notEntitledGate, tags rowTagReader, p *phaseTotals) bool {
	return storeTokenPagesTags(ctx, st, acct, region, budgetsDescribeBudgetsOp,
		func(ctx context.Context, token *string) ([]budgetstypes.Budget, *string, error) {
			out, err := client.DescribeBudgets(ctx, &budgets.DescribeBudgetsInput{
				AccountId: &acct.ID, MaxResults: sdkaws.Int32(budgetsBudgetsPageCap), NextToken: token,
			})
			if linked.keepFirstPageRefusal(p, token, err) {
				return nil, nil, nil
			}
			if err != nil {
				return nil, nil, err
			}
			return out.Budgets, out.NextToken, nil
		},
		func(b budgetstypes.Budget) (*store.Resource, string, string) { return budgetRow(acct, scanID, b) },
		tags, p)
}

// scanBudgetsActions stores the account's budget actions, each page written
// as soon as its rows' tags are read. A refusal linked keeps is read as an
// empty listing. It reports whether a write failed, after which
// the scan must stop.
func scanBudgetsActions(ctx context.Context, client budgetsAPI, acct *account, region string, st *store.Store, scanID string, linked *notEntitledGate, tags rowTagReader, p *phaseTotals) bool {
	return storeTokenPagesTags(ctx, st, acct, region, budgetsDescribeActionsOp,
		func(ctx context.Context, token *string) ([]budgetstypes.Action, *string, error) {
			out, err := client.DescribeBudgetActionsForAccount(ctx, &budgets.DescribeBudgetActionsForAccountInput{
				AccountId: &acct.ID, MaxResults: sdkaws.Int32(budgetsActionsPageCap), NextToken: token,
			})
			if linked.keepFirstPageRefusal(p, token, err) {
				return nil, nil, nil
			}
			if err != nil {
				return nil, nil, err
			}
			return out.Actions, out.NextToken, nil
		},
		func(a budgetstypes.Action) (*store.Resource, string, string) { return budgetActionRow(acct, scanID, a) },
		tags, p)
}

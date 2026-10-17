package aws

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/service/computeoptimizerautomation"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

// Compute Optimizer Automation rules are treated as account-global: the
// model's RuleArn pattern (arn:aws:compute-optimizer::<account>:automation-rule/<id>)
// requires an empty region segment, and a rule selects the regions it acts in
// through Criteria.region. The endpoint also resolves in other regions, so a
// regional fan-out would upsert the same regionless ARN once per region; the
// scan reads the rules once, from us-east-1. That every regional endpoint
// serves the same rule set is unverified. Organization rules
// (RuleType OrganizationRule) are owned by the account in their ARN; if a
// member account's listing also returns them they are stored under the member
// account too (unverified).
const acoAutomationRegion = "us-east-1"

func init() {
	registerType(restype.Descriptor{Type: TypeACOAutomationAutomationRule, Service: "aco-automation"})
	registerService(serviceEntry{
		name:   "aws:aco-automation",
		global: true,
		fn: func(ctx context.Context, acct *account, _ string, st *store.Store, scanID string) (int, int, error) {
			client := computeoptimizerautomation.NewFromConfig(acct.cfg, func(o *computeoptimizerautomation.Options) { o.Region = acoAutomationRegion })
			return scanACOAutomationRules(ctx, client, acct, st, scanID)
		},
	})
}

// acoAutomationAPI is the narrow surface scanACOAutomationRules uses.
type acoAutomationAPI interface {
	computeoptimizerautomation.ListAutomationRulesAPIClient
	GetAutomationRule(context.Context, *computeoptimizerautomation.GetAutomationRuleInput, ...func(*computeoptimizerautomation.Options)) (*computeoptimizerautomation.GetAutomationRuleOutput, error)
}

// scanACOAutomationRules lists rule ARNs, then reads each rule with
// GetAutomationRule: only the full rule carries its Criteria and Tags. A
// denial mid-listing warns and keeps the rules listed so far. An account not
// opted in to Compute Optimizer Automation is reported as disabled; the rules
// are not stored until every listed rule is read, so that sentinel never
// discards stored rows.
func scanACOAutomationRules(ctx context.Context, client acoAutomationAPI, acct *account, st *store.Store, scanID string) (int, int, error) {
	region := acoAutomationRegion
	var arns []string
	pager := computeoptimizerautomation.NewListAutomationRulesPaginator(client, &computeoptimizerautomation.ListAutomationRulesInput{},
		func(o *computeoptimizerautomation.ListAutomationRulesPaginatorOptions) {
			o.Limit = 100
			o.StopOnDuplicateToken = true
		})
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAPIErrorCode(err, "OptInRequiredException") {
				return 0, 0, markServiceDisabled(err)
			}
			if isAccessDenied(err) {
				_ = skipIfAccessDenied(st, "aco-automation:ListAutomationRules", acct.ID, region, err)
				break
			}
			return 0, 0, fmt.Errorf("aco-automation:ListAutomationRules: %w", err)
		}
		for _, r := range out.AutomationRules {
			if arn := sv(r.RuleArn); arn != "" {
				arns = append(arns, arn)
			}
		}
	}
	rows, err := describeFanOut(ctx, st, acct, region, "aco-automation:GetAutomationRule", arns, isACOAutomationNotFound,
		func(ctx context.Context, arn string) (*store.Resource, error) {
			out, err := client.GetAutomationRule(ctx, &computeoptimizerautomation.GetAutomationRuleInput{RuleArn: &arn})
			if err != nil {
				return nil, err
			}
			return &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeACOAutomationAutomationRule, NativeID: arn, Name: out.Name, Region: regionGlobal,
				Status: nonEmptyPtr(string(out.Status)), CreatedAt: tp(out.CreatedTimestamp),
				AttributesJSON: mustJSON(out), TagsJSON: awsTagsJSON(out.Tags),
				DiscoveredBy: scanID,
			}, nil
		})
	if err != nil {
		return 0, 0, err
	}
	return upsertBatch(st, rows, "aco-automation automation-rules")
}

// isACOAutomationNotFound matches a rule deleted between its list call and
// GetAutomationRule.
func isACOAutomationNotFound(err error) bool {
	return isAPIErrorCode(err, "ResourceNotFoundException")
}

package aws

import (
	"context"
	"errors"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/service/guardduty"
	gdtypes "github.com/aws/aws-sdk-go-v2/service/guardduty/types"
	"github.com/icearp/disco-cli/store"
)

const (
	gdListCustomRuleAssocsOp = "guardduty:ListCustomDetectionRuleAssociations"
	gdListCustomRuleOrgOp    = "guardduty:ListCustomDetectionRuleOrgConfigurations"
	// gdDetectionRulePageLimit is the documented MaxResults cap
	// (DetectionRuleMaxResults, 1-100) of both list ops.
	gdDetectionRulePageLimit = 100
)

// scanGDCustomDetectionRules stores this account's custom detection rule
// associations and the delegated administrator's organization configurations of
// those rules. The rules themselves are an AWS-owned catalog and are not
// scanned. orgConfigRefused is scanGDOrganizationConfigurations' report that
// the region refused the account its organization configuration; the
// organization-level list is skipped there, since the same delegated
// administrator role is needed for both. The phases are independent, so each
// runs whatever the other returns, and each adds its error to errs under its
// own op for the caller to join.
func scanGDCustomDetectionRules(ctx context.Context, client guardDutyExtAPI, acct *account, region string, st *store.Store, scanID string, orgConfigRefused bool, errs *phaseErrs) (total, inserted int) {
	total, inserted, err := scanGDCustomRuleAssociations(ctx, client, acct, region, st, scanID)
	errs.add(gdListCustomRuleAssocsOp, err)
	if !orgConfigRefused {
		t, i, oerr := scanGDCustomRuleOrgConfigurations(ctx, client, acct, region, st, scanID)
		total += t
		inserted += i
		errs.add(gdListCustomRuleOrgOp, oerr)
	}
	return total, inserted
}

// gdListPhase lists one region-wide custom-detection-rule collection page by
// page and stores the row toRow builds for each item (nil drops the item). The
// ops arrived in SDK v1.89.0 (2026-09-01), so a first page that isOpNotDeployed
// matches is a region whose endpoint does not serve the op yet and skips the
// phase silently. Otherwise a denial warns and any other error is returned;
// either way the rows listed so far are stored.
func gdListPhase[Page, Item any](
	ctx context.Context, st *store.Store, acct *account, region, op string,
	hasMore func() bool, next func(context.Context) (Page, error),
	items func(Page) []Item, toRow func(Item) *store.Resource,
) (int, int, error) {
	var batch []*store.Resource
	var listErr error
	listed := false
	for hasMore() {
		page, err := next(ctx)
		if err != nil {
			switch {
			case !listed && isOpNotDeployed(err):
				return 0, 0, nil
			case isAccessDenied(err):
				_ = skipIfAccessDenied(st, op, acct.ID, region, err)
			default:
				listErr = fmt.Errorf("%s: %w", op, err)
			}
			break
		}
		listed = true
		for _, it := range items(page) {
			if r := toRow(it); r != nil {
				batch = append(batch, r)
			}
		}
	}
	t, i, uerr := upsertBatch(st, batch, op)
	return t, i, errors.Join(listErr, uerr)
}

func scanGDCustomRuleAssociations(ctx context.Context, client guardDutyExtAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := guardduty.NewListCustomDetectionRuleAssociationsPaginator(client, &guardduty.ListCustomDetectionRuleAssociationsInput{}, func(o *guardduty.ListCustomDetectionRuleAssociationsPaginatorOptions) {
		o.Limit = gdDetectionRulePageLimit
		o.StopOnDuplicateToken = true
	})
	next := func(ctx context.Context) (*guardduty.ListCustomDetectionRuleAssociationsOutput, error) {
		return pager.NextPage(ctx)
	}
	return gdListPhase(ctx, st, acct, region, gdListCustomRuleAssocsOp, pager.HasMorePages, next,
		func(out *guardduty.ListCustomDetectionRuleAssociationsOutput) []gdtypes.AssociationSummary {
			return out.RuleAssociations
		},
		func(a gdtypes.AssociationSummary) *store.Resource {
			arn := sv(a.Arn)
			if arn == "" {
				return nil
			}
			return &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeGuardDutyCustomDetectionRuleAssociation, NativeID: arn,
				Name: a.AssociationId, Region: &region, CreatedAt: tp(a.CreatedAt),
				AttributesJSON: mustJSON(a), DiscoveredBy: scanID,
			}
		})
}

// gdRuleOrgConfigNativeID names an organization configuration, which carries
// no ARN. The service keys one by rule and mode (Get, Update and Delete all
// require RuleId and Mode), so both go in, under the rule's path in the
// association ARN shape; RuleId's model pattern ^[a-z0-9]+(-[a-z0-9]+)*$
// admits no "/".
func gdRuleOrgConfigNativeID(partition, region, accountID, ruleID string, mode gdtypes.AssociationMode) string {
	return arnFor(partition, "guardduty", region, accountID, "detection-rule/custom/"+ruleID+"/org-configuration/"+string(mode))
}

// scanGDCustomRuleOrgConfigurations stores the organization configurations
// that the GuardDuty delegated administrator creates
// (CreateCustomDetectionRuleOrgConfiguration "is available only to the
// delegated administrator account").
func scanGDCustomRuleOrgConfigurations(ctx context.Context, client guardDutyExtAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := guardduty.NewListCustomDetectionRuleOrgConfigurationsPaginator(client, &guardduty.ListCustomDetectionRuleOrgConfigurationsInput{}, func(o *guardduty.ListCustomDetectionRuleOrgConfigurationsPaginatorOptions) {
		o.Limit = gdDetectionRulePageLimit
		o.StopOnDuplicateToken = true
	})
	next := func(ctx context.Context) (*guardduty.ListCustomDetectionRuleOrgConfigurationsOutput, error) {
		return pager.NextPage(ctx)
	}
	return gdListPhase(ctx, st, acct, region, gdListCustomRuleOrgOp, pager.HasMorePages, next,
		func(out *guardduty.ListCustomDetectionRuleOrgConfigurationsOutput) []gdtypes.DetectionRuleOrgConfigurationSummary {
			return out.Configurations
		},
		func(c gdtypes.DetectionRuleOrgConfigurationSummary) *store.Resource {
			ruleID := sv(c.RuleId)
			if ruleID == "" || c.Mode == "" {
				return nil
			}
			return &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type:     TypeGuardDutyCustomDetectionRuleOrgConfiguration,
				NativeID: gdRuleOrgConfigNativeID(acct.partition(), region, acct.ID, ruleID, c.Mode),
				Name:     &ruleID, Region: &region, Status: nonEmptyPtr(string(c.Status)), CreatedAt: tp(c.CreatedAt),
				AttributesJSON: mustJSON(c), DiscoveredBy: scanID,
			}
		})
}

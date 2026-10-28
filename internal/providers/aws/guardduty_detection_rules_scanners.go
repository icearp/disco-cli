package aws

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
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

// The custom detection rule ops arrived in SDK v1.89.0 (2026-09-01), so a
// first page that isOpNotDeployed matches is a region whose endpoint does not
// serve the op yet, read as an empty listing (firstPageGapPages). The rules
// themselves are an AWS-owned catalog and are not scanned.

// gdAssocTags reads a custom detection rule association's tags from
// GetCustomDetectionRuleAssociation, sent the RuleId and AssociationId of its
// listed element (the row's attributes). It reads the get rather than
// ListTagsForResource: the Service Reference lists
// customdetectionruleassociation under ListTagsForResource, but that op's
// model admits only ARNs under a detector (the GuardDutyArn pattern), while
// the get's output carries the association's Tags.
// ResourceNotFoundException, the get's modeled not-found, drops an
// association deleted after it was listed.
func gdAssocTags(client guardDutyExtAPI) rowTagReader {
	return rowTagReader{
		op:       "guardduty:GetCustomDetectionRuleAssociation",
		notFound: []string{"ResourceNotFoundException"},
		read: func(ctx context.Context, r *store.Resource) (*string, error) {
			var a gdtypes.AssociationSummary
			if err := json.Unmarshal([]byte(r.AttributesJSON), &a); err != nil {
				return nil, fmt.Errorf("decode the listed association %s: %w", r.NativeID, err)
			}
			out, err := client.GetCustomDetectionRuleAssociation(ctx, &guardduty.GetCustomDetectionRuleAssociationInput{RuleId: a.RuleId, AssociationId: a.AssociationId})
			if err != nil {
				return nil, err
			}
			return mapTagsJSON(out.Tags), nil
		},
	}
}

// scanGDCustomRuleAssociations stores this account's custom detection rule
// associations with their tags (gdAssocTags), a page at a time. Counts and
// errors go to p; it reports whether a write failed, after which the scanner
// must stop.
func scanGDCustomRuleAssociations(ctx context.Context, client guardDutyExtAPI, acct *account, region string, st *store.Store, scanID string, p *phaseTotals) (storeWriteFailed bool) {
	return storeTokenPagesTags(ctx, st, acct, region, gdListCustomRuleAssocsOp,
		firstPageGapPages(isOpNotDeployed, func(ctx context.Context, token *string) ([]gdtypes.AssociationSummary, *string, error) {
			out, err := client.ListCustomDetectionRuleAssociations(ctx, &guardduty.ListCustomDetectionRuleAssociationsInput{NextToken: token, MaxResults: sdkaws.Int32(gdDetectionRulePageLimit)})
			if err != nil {
				return nil, nil, err
			}
			return out.RuleAssociations, out.NextToken, nil
		}),
		func(a gdtypes.AssociationSummary) (*store.Resource, string, string) {
			arn := sv(a.Arn)
			if arn == "" {
				return nil, "custom detection rule association(s) with an empty Arn", cmp.Or(sv(a.AssociationId), "(an association with no id)")
			}
			return &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeGuardDutyCustomDetectionRuleAssociation, NativeID: arn,
				Name: a.AssociationId, Region: &region, CreatedAt: tp(a.CreatedAt),
				AttributesJSON: mustJSON(a), DiscoveredBy: scanID,
			}, "", ""
		}, gdAssocTags(client), p)
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
// delegated administrator account"). They have no tags to read: the Service
// Reference names no resource for them.
func scanGDCustomRuleOrgConfigurations(ctx context.Context, client guardDutyExtAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	return storeTokenList(ctx, st, acct, region, gdListCustomRuleOrgOp,
		firstPageGapPages(isOpNotDeployed, func(ctx context.Context, token *string) ([]gdtypes.DetectionRuleOrgConfigurationSummary, *string, error) {
			out, err := client.ListCustomDetectionRuleOrgConfigurations(ctx, &guardduty.ListCustomDetectionRuleOrgConfigurationsInput{NextToken: token, MaxResults: sdkaws.Int32(gdDetectionRulePageLimit)})
			if err != nil {
				return nil, nil, err
			}
			return out.Configurations, out.NextToken, nil
		}),
		func(c gdtypes.DetectionRuleOrgConfigurationSummary) (*store.Resource, string, string) {
			ruleID := sv(c.RuleId)
			if ruleID == "" || c.Mode == "" {
				return nil, "rule organization configuration(s) with an empty RuleId or Mode", cmp.Or(ruleID, "(a configuration with no rule id)")
			}
			return &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type:     TypeGuardDutyCustomDetectionRuleOrgConfiguration,
				NativeID: gdRuleOrgConfigNativeID(acct.partition(), region, acct.ID, ruleID, c.Mode),
				Name:     &ruleID, Region: &region, Status: nonEmptyPtr(string(c.Status)), CreatedAt: tp(c.CreatedAt),
				AttributesJSON: mustJSON(c), DiscoveredBy: scanID,
			}, "", ""
		})
}

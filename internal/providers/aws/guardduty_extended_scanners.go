package aws

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/guardduty"
	gdtypes "github.com/aws/aws-sdk-go-v2/service/guardduty/types"
	"github.com/icearp/disco-cli/store"
)

// isGuardDutyMemberRestricted matches the BadRequestException a member
// account gets calling a management-account-only GuardDuty op (e.g.
// ListThreatEntitySets, ListTrustedEntitySets — entity sets are managed
// centrally by the delegated admin, propagated read-only to members).
// Code+message disambiguates from real validation errors sharing the same
// code.
func isGuardDutyMemberRestricted(err error) bool {
	return isAPIErrorWithMessage(err, "BadRequestException", "member accounts cannot manage")
}

// guardDutyExtAPI lists ops used by extended GuardDuty phases. Threat / Trusted
// entity-set + threat-intel-set + publishing-destination + organization
// configuration are per-detector; malware-protection-plan and the custom
// detection rule ops are account-wide.
type guardDutyExtAPI interface {
	ListMalwareProtectionPlans(context.Context, *guardduty.ListMalwareProtectionPlansInput, ...func(*guardduty.Options)) (*guardduty.ListMalwareProtectionPlansOutput, error)
	GetMalwareProtectionPlan(context.Context, *guardduty.GetMalwareProtectionPlanInput, ...func(*guardduty.Options)) (*guardduty.GetMalwareProtectionPlanOutput, error)
	ListPublishingDestinations(context.Context, *guardduty.ListPublishingDestinationsInput, ...func(*guardduty.Options)) (*guardduty.ListPublishingDestinationsOutput, error)
	ListThreatEntitySets(context.Context, *guardduty.ListThreatEntitySetsInput, ...func(*guardduty.Options)) (*guardduty.ListThreatEntitySetsOutput, error)
	ListThreatIntelSets(context.Context, *guardduty.ListThreatIntelSetsInput, ...func(*guardduty.Options)) (*guardduty.ListThreatIntelSetsOutput, error)
	ListTrustedEntitySets(context.Context, *guardduty.ListTrustedEntitySetsInput, ...func(*guardduty.Options)) (*guardduty.ListTrustedEntitySetsOutput, error)
	GetThreatEntitySet(context.Context, *guardduty.GetThreatEntitySetInput, ...func(*guardduty.Options)) (*guardduty.GetThreatEntitySetOutput, error)
	GetThreatIntelSet(context.Context, *guardduty.GetThreatIntelSetInput, ...func(*guardduty.Options)) (*guardduty.GetThreatIntelSetOutput, error)
	GetTrustedEntitySet(context.Context, *guardduty.GetTrustedEntitySetInput, ...func(*guardduty.Options)) (*guardduty.GetTrustedEntitySetOutput, error)
	DescribeOrganizationConfiguration(context.Context, *guardduty.DescribeOrganizationConfigurationInput, ...func(*guardduty.Options)) (*guardduty.DescribeOrganizationConfigurationOutput, error)
	ListCustomDetectionRuleAssociations(context.Context, *guardduty.ListCustomDetectionRuleAssociationsInput, ...func(*guardduty.Options)) (*guardduty.ListCustomDetectionRuleAssociationsOutput, error)
	GetCustomDetectionRuleAssociation(context.Context, *guardduty.GetCustomDetectionRuleAssociationInput, ...func(*guardduty.Options)) (*guardduty.GetCustomDetectionRuleAssociationOutput, error)
	ListCustomDetectionRuleOrgConfigurations(context.Context, *guardduty.ListCustomDetectionRuleOrgConfigurationsInput, ...func(*guardduty.Options)) (*guardduty.ListCustomDetectionRuleOrgConfigurationsOutput, error)
	ListTagsForResource(context.Context, *guardduty.ListTagsForResourceInput, ...func(*guardduty.Options)) (*guardduty.ListTagsForResourceOutput, error)
}

const (
	gdTagsOp      = "guardduty:ListTagsForResource"
	gdOrgConfigOp = "guardduty:DescribeOrganizationConfiguration"
)

// gdTags reads a publishing destination's tags with ListTagsForResource,
// sent the row's NativeID: the destination's ARN in the Service Reference's
// ARN format. The Service Reference lists publishingDestination under the
// op and CreatePublishingDestination takes Tags. The op models no not-found
// code, so a destination deleted after it was listed fails its read with
// whatever code GuardDuty sends, and that error is returned with the row
// kept untagged.
func gdTags(client guardDutyExtAPI) rowTagReader {
	return rowTagReader{
		op: gdTagsOp,
		read: func(ctx context.Context, r *store.Resource) (*string, error) {
			out, err := client.ListTagsForResource(ctx, &guardduty.ListTagsForResourceInput{ResourceArn: sdkaws.String(r.NativeID)})
			if err != nil {
				return nil, err
			}
			return mapTagsJSON(out.Tags), nil
		},
	}
}

// errGDOrgConfigRefused marks a detector whose first
// DescribeOrganizationConfiguration page was refused with BadRequestException.
var errGDOrgConfigRefused = errors.New("guardduty organization configuration not read (the account may not be the GuardDuty delegated administrator)")

// scanGDOrganizationConfigurations stores each detector's organization
// configuration, which only the GuardDuty delegated administrator can read.
// Non-admin and standalone accounts are expected to get BadRequestException on
// the first page, but that response was never observed live and GuardDuty uses
// the code for any 400 (a detector deleted since listing, a malformed request).
// So a first-page BadRequestException drops only that detector's configuration
// and records one warning per region, through the same warning sink as an
// AccessDenied; the detector's other rows are kept. A later page is requested
// only after the first succeeded, so a BadRequestException there is not a
// refusal to read the configuration (e.g. a rejected NextToken) and propagates.
// refused reports that some detector's configuration was refused that way, so
// the caller can skip the region's other delegated-administrator-only reads.
func scanGDOrganizationConfigurations(ctx context.Context, client guardDutyExtAPI, acct *account, region string, st *store.Store, scanID string, detectorIDs []string) (total, inserted int, refused bool, err error) {
	parents := make([]childParent, len(detectorIDs))
	for i, did := range detectorIDs {
		parents[i] = childParent{id: did, arn: guardDutyDetectorARN(acct.partition(), region, acct.ID, did)}
	}
	var refusedOnce sync.Once
	warnIfRefused := func(err error) bool {
		if !errors.Is(err, errGDOrgConfigRefused) {
			return false
		}
		refusedOnce.Do(func() {
			refused = true
			_ = skipIfAccessDenied(st, gdOrgConfigOp, acct.ID, region, err)
		})
		return true
	}
	total, inserted, err = childFanOut(ctx, st, acct, region, gdOrgConfigOp, parents, warnIfRefused,
		func(ctx context.Context, p childParent) ([]*store.Resource, error) {
			did := p.id
			pager := guardduty.NewDescribeOrganizationConfigurationPaginator(client, &guardduty.DescribeOrganizationConfigurationInput{DetectorId: &did})
			var attrs *guardduty.DescribeOrganizationConfigurationOutput
			for pager.HasMorePages() {
				out, err := pager.NextPage(ctx)
				if err != nil {
					if attrs == nil && isAPIErrorCode(err, "BadRequestException") {
						return nil, fmt.Errorf("%w: %w", errGDOrgConfigRefused, err)
					}
					return nil, err
				}
				if attrs == nil {
					attrs = out
					continue
				}
				attrs.Features = append(attrs.Features, out.Features...)
			}
			// Page 1 carries the settings; clearing its NextToken keeps a paginated
			// answer identical across scans. ResultMetadata needs no reset:
			// mustJSON drops it.
			attrs.NextToken = nil
			label := "organization-configuration"
			return []*store.Resource{{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeGuardDutyOrganizationConfiguration, NativeID: p.arn + "/organization-configuration",
				Name: &label, Region: &region, AttributesJSON: mustJSON(attrs), DiscoveredBy: scanID,
			}}, nil
		})
	return total, inserted, refused, err
}

// gdPlanTags reads a malware protection plan's tags from
// GetMalwareProtectionPlan, sent the MalwareProtectionPlanId of its listed
// element (the row's attributes). It reads the get rather than
// ListTagsForResource: the Service Reference lists malwareprotectionplan
// under ListTagsForResource, but that op's model admits only ARNs under a
// detector (the GuardDutyArn pattern), while the get's output carries the
// plan's Tags. ResourceNotFoundException, the get's modeled not-found, drops
// a plan deleted after it was listed.
func gdPlanTags(client guardDutyExtAPI) rowTagReader {
	return rowTagReader{
		op:       "guardduty:GetMalwareProtectionPlan",
		notFound: []string{"ResourceNotFoundException"},
		read: func(ctx context.Context, r *store.Resource) (*string, error) {
			var plan gdtypes.MalwareProtectionPlanSummary
			if err := json.Unmarshal([]byte(r.AttributesJSON), &plan); err != nil {
				return nil, fmt.Errorf("decode the listed plan %s: %w", r.NativeID, err)
			}
			out, err := client.GetMalwareProtectionPlan(ctx, &guardduty.GetMalwareProtectionPlanInput{MalwareProtectionPlanId: plan.MalwareProtectionPlanId})
			if err != nil {
				return nil, err
			}
			return mapTagsJSON(out.Tags), nil
		},
	}
}

// scanGDMalwareProtectionPlans stores the account's malware protection plans
// with their tags (gdPlanTags), a page at a time. The listing takes no page
// size. The listed summary carries no ARN, so its NativeID is built in the
// Service Reference's ARN format (GetMalwareProtectionPlan, read here only
// for tags, returns the Arn).
func scanGDMalwareProtectionPlans(ctx context.Context, client guardDutyExtAPI, acct *account, region string, st *store.Store, scanID string, p *phaseTotals) (storeWriteFailed bool) {
	return storeTokenPagesTags(ctx, st, acct, region, "guardduty:ListMalwareProtectionPlans",
		func(ctx context.Context, token *string) ([]gdtypes.MalwareProtectionPlanSummary, *string, error) {
			out, err := client.ListMalwareProtectionPlans(ctx, &guardduty.ListMalwareProtectionPlansInput{NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.MalwareProtectionPlans, out.NextToken, nil
		},
		func(plan gdtypes.MalwareProtectionPlanSummary) (*store.Resource, string, string) {
			id := sv(plan.MalwareProtectionPlanId)
			if id == "" {
				return nil, "malware protection plan(s) with an empty id", "(a malware protection plan with no id)"
			}
			return &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeGuardDutyMalwareProtectionPlan, NativeID: arnFor(acct.partition(), "guardduty", region, acct.ID, "malware-protection-plan/"+id),
				Name: &id, Region: &region, AttributesJSON: mustJSON(plan), DiscoveredBy: scanID,
			}, "", ""
		}, gdPlanTags(client), p)
}

// gdMemberRestricted is the skip of the entity-set listings: a member account
// is refused them (isGuardDutyMemberRestricted), which is its expected state
// rather than a failure, so that detector is skipped silently.
func gdMemberRestricted(_ string, err error) bool { return isGuardDutyMemberRestricted(err) }

// scanGDPublishingDestinations, like the entity and intel set phases below,
// stores the items its op lists for each detector with their tags, the
// detectors read concurrently and each page written as soon as its tags are
// read (storeEachParentPagesTagsSkip). One detector's failure does not stop
// the others, and the pages written before it are kept: a denial warns once
// for the op, and any other error is returned after the rows are stored (the
// per-detector listings model no not-found code).
func scanGDPublishingDestinations(ctx context.Context, client guardDutyExtAPI, acct *account, region string, st *store.Store, scanID string, detectors []childParent, p *phaseTotals) (storeWriteFailed bool) {
	return storeEachParentPagesTagsSkip(ctx, st, acct, region, "guardduty:ListPublishingDestinations", nil, detectors,
		func(ctx context.Context, did string, token *string) ([]gdtypes.Destination, *string, error) {
			out, err := client.ListPublishingDestinations(ctx, &guardduty.ListPublishingDestinationsInput{DetectorId: &did, NextToken: token, MaxResults: sdkaws.Int32(gdPageCap)})
			if err != nil {
				return nil, nil, err
			}
			return out.Destinations, out.NextToken, nil
		},
		func(did string, d gdtypes.Destination) (*store.Resource, string, string) {
			id := sv(d.DestinationId)
			if id == "" {
				return nil, "publishing destination(s) with an empty id", "one of detector " + did
			}
			return &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeGuardDutyPublishingDestination, NativeID: gdDetectorChildARN(acct.partition(), region, acct.ID, did, "publishingdestination", id),
				Name: &id, Region: &region, AttributesJSON: mustJSON(d), DiscoveredBy: scanID,
			}, "", ""
		}, nil, gdTags(client), p)
}

// gdSetTags reads an entity or intel set's tags with op, its get, sent the
// DetectorId and the set's id under idKey from the row's attributes
// (gdIDSetRow); the get's output carries the set's Tags. Not
// ListTagsForResource: a member account lists its administrator's sets under its
// own detector (the listings' SDK docs: "If you use this operation from a member
// account, the ThreatIntelSets associated with the administrator account are
// returned"), so the ARN built from the member's account and detector names no
// resource, while the detector-scoped get asks for exactly what the listing
// returned. Whether a member's get of the administrator's set succeeds was not
// observed live; a get refused as member restricted
// (isGuardDutyMemberRestricted, the refusal the entity-set listings give
// members) is treated as that account's expected state: the row is kept untagged
// and silent. The gets model no not-found code, so a set deleted after it was
// listed fails its read with whatever code GuardDuty sends, and that error is
// returned with the row kept untagged.
func gdSetTags(op, idKey string, get func(ctx context.Context, detectorID, id *string) (map[string]string, error)) rowTagReader {
	return rowTagReader{
		op: op,
		read: func(ctx context.Context, r *store.Resource) (*string, error) {
			var ids map[string]string
			if err := json.Unmarshal([]byte(r.AttributesJSON), &ids); err != nil {
				return nil, fmt.Errorf("decode the listed set %s: %w", r.NativeID, err)
			}
			tags, err := get(ctx, sdkaws.String(ids["DetectorId"]), sdkaws.String(ids[idKey]))
			if isGuardDutyMemberRestricted(err) {
				return nil, nil
			}
			if err != nil {
				return nil, err
			}
			return mapTagsJSON(tags), nil
		},
	}
}

// gdIDSetRow is the row of a detector's child that its listing returns as an
// id only (an entity or intel set, a filter, an IP set): the attributes are
// the detector's id and the child's under idKey (gdSetTags reads them back),
// the Name is the id, and the NativeID is the child's ARN, of kind.
func gdIDSetRow(acct *account, region, scanID, typ, what, kind, idKey, did, id string) (*store.Resource, string, string) {
	if id == "" {
		return nil, what + " with an empty id", "one of detector " + did
	}
	return &store.Resource{
		Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
		Type: typ, NativeID: gdDetectorChildARN(acct.partition(), region, acct.ID, did, kind, id),
		Name: &id, Region: &region, AttributesJSON: mustJSON(map[string]string{"DetectorId": did, idKey: id}), DiscoveredBy: scanID,
	}, "", ""
}

func scanGDThreatEntitySets(ctx context.Context, client guardDutyExtAPI, acct *account, region string, st *store.Store, scanID string, detectors []childParent, p *phaseTotals) (storeWriteFailed bool) {
	return storeEachParentPagesTagsSkip(ctx, st, acct, region, "guardduty:ListThreatEntitySets", nil, detectors,
		func(ctx context.Context, did string, token *string) ([]string, *string, error) {
			out, err := client.ListThreatEntitySets(ctx, &guardduty.ListThreatEntitySetsInput{DetectorId: &did, NextToken: token, MaxResults: sdkaws.Int32(gdPageCap)})
			if err != nil {
				return nil, nil, err
			}
			return out.ThreatEntitySetIds, out.NextToken, nil
		},
		func(did, id string) (*store.Resource, string, string) {
			return gdIDSetRow(acct, region, scanID, TypeGuardDutyThreatEntitySet, "threat entity set(s)", "threatentityset", "ThreatEntitySetId", did, id)
		}, gdMemberRestricted, gdSetTags("guardduty:GetThreatEntitySet", "ThreatEntitySetId", func(ctx context.Context, did, id *string) (map[string]string, error) {
			out, err := client.GetThreatEntitySet(ctx, &guardduty.GetThreatEntitySetInput{DetectorId: did, ThreatEntitySetId: id})
			if err != nil {
				return nil, err
			}
			return out.Tags, nil
		}), p)
}

func scanGDThreatIntelSets(ctx context.Context, client guardDutyExtAPI, acct *account, region string, st *store.Store, scanID string, detectors []childParent, p *phaseTotals) (storeWriteFailed bool) {
	return storeEachParentPagesTagsSkip(ctx, st, acct, region, "guardduty:ListThreatIntelSets", nil, detectors,
		func(ctx context.Context, did string, token *string) ([]string, *string, error) {
			out, err := client.ListThreatIntelSets(ctx, &guardduty.ListThreatIntelSetsInput{DetectorId: &did, NextToken: token, MaxResults: sdkaws.Int32(gdPageCap)})
			if err != nil {
				return nil, nil, err
			}
			return out.ThreatIntelSetIds, out.NextToken, nil
		},
		func(did, id string) (*store.Resource, string, string) {
			return gdIDSetRow(acct, region, scanID, TypeGuardDutyThreatIntelSet, "threat intel set(s)", "threatintelset", "ThreatIntelSetId", did, id)
		}, nil, gdSetTags("guardduty:GetThreatIntelSet", "ThreatIntelSetId", func(ctx context.Context, did, id *string) (map[string]string, error) {
			out, err := client.GetThreatIntelSet(ctx, &guardduty.GetThreatIntelSetInput{DetectorId: did, ThreatIntelSetId: id})
			if err != nil {
				return nil, err
			}
			return out.Tags, nil
		}), p)
}

func scanGDTrustedEntitySets(ctx context.Context, client guardDutyExtAPI, acct *account, region string, st *store.Store, scanID string, detectors []childParent, p *phaseTotals) (storeWriteFailed bool) {
	return storeEachParentPagesTagsSkip(ctx, st, acct, region, "guardduty:ListTrustedEntitySets", nil, detectors,
		func(ctx context.Context, did string, token *string) ([]string, *string, error) {
			out, err := client.ListTrustedEntitySets(ctx, &guardduty.ListTrustedEntitySetsInput{DetectorId: &did, NextToken: token, MaxResults: sdkaws.Int32(gdPageCap)})
			if err != nil {
				return nil, nil, err
			}
			return out.TrustedEntitySetIds, out.NextToken, nil
		},
		func(did, id string) (*store.Resource, string, string) {
			return gdIDSetRow(acct, region, scanID, TypeGuardDutyTrustedEntitySet, "trusted entity set(s)", "trustedentityset", "TrustedEntitySetId", did, id)
		}, gdMemberRestricted, gdSetTags("guardduty:GetTrustedEntitySet", "TrustedEntitySetId", func(ctx context.Context, did, id *string) (map[string]string, error) {
			out, err := client.GetTrustedEntitySet(ctx, &guardduty.GetTrustedEntitySetInput{DetectorId: did, TrustedEntitySetId: id})
			if err != nil {
				return nil, err
			}
			return out.Tags, nil
		}), p)
}

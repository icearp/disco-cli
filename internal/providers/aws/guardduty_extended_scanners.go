package aws

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/aws/aws-sdk-go-v2/service/guardduty"
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
// configuration are per-detector; malware-protection-plan is account-wide.
type guardDutyExtAPI interface {
	ListMalwareProtectionPlans(context.Context, *guardduty.ListMalwareProtectionPlansInput, ...func(*guardduty.Options)) (*guardduty.ListMalwareProtectionPlansOutput, error)
	ListPublishingDestinations(context.Context, *guardduty.ListPublishingDestinationsInput, ...func(*guardduty.Options)) (*guardduty.ListPublishingDestinationsOutput, error)
	ListThreatEntitySets(context.Context, *guardduty.ListThreatEntitySetsInput, ...func(*guardduty.Options)) (*guardduty.ListThreatEntitySetsOutput, error)
	ListThreatIntelSets(context.Context, *guardduty.ListThreatIntelSetsInput, ...func(*guardduty.Options)) (*guardduty.ListThreatIntelSetsOutput, error)
	ListTrustedEntitySets(context.Context, *guardduty.ListTrustedEntitySetsInput, ...func(*guardduty.Options)) (*guardduty.ListTrustedEntitySetsOutput, error)
	ListDetectors(context.Context, *guardduty.ListDetectorsInput, ...func(*guardduty.Options)) (*guardduty.ListDetectorsOutput, error)
	DescribeOrganizationConfiguration(context.Context, *guardduty.DescribeOrganizationConfigurationInput, ...func(*guardduty.Options)) (*guardduty.DescribeOrganizationConfigurationOutput, error)
	ListCustomDetectionRuleAssociations(context.Context, *guardduty.ListCustomDetectionRuleAssociationsInput, ...func(*guardduty.Options)) (*guardduty.ListCustomDetectionRuleAssociationsOutput, error)
	ListCustomDetectionRuleOrgConfigurations(context.Context, *guardduty.ListCustomDetectionRuleOrgConfigurationsInput, ...func(*guardduty.Options)) (*guardduty.ListCustomDetectionRuleOrgConfigurationsOutput, error)
}

// scanGuardDutyExtended runs the extended-coverage phases. Re-lists detectors
// to avoid plumbing IDs through the existing scanGuardDutyDetectors signature.
func scanGuardDutyExtended(ctx context.Context, client guardDutyExtAPI, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	t, i, ferr := scanGDMalwareProtectionPlans(ctx, client, acct, region, st, scanID)
	if ferr != nil {
		return total, inserted, ferr
	}
	total += t
	inserted += i

	var detectorIDs []string
	pager := guardduty.NewListDetectorsPaginator(client, &guardduty.ListDetectorsInput{})
	for pager.HasMorePages() {
		page, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				return total, inserted, nil
			}
			return total, inserted, fmt.Errorf("guardduty:ListDetectors(ext): %w", perr)
		}
		detectorIDs = append(detectorIDs, page.DetectorIds...)
	}

	for _, did := range detectorIDs {
		for _, phase := range []func() (int, int, error){
			func() (int, int, error) {
				return scanGDPublishingDestinations(ctx, client, acct, region, st, scanID, did)
			},
			func() (int, int, error) {
				return scanGDThreatEntitySets(ctx, client, acct, region, st, scanID, did)
			},
			func() (int, int, error) {
				return scanGDThreatIntelSets(ctx, client, acct, region, st, scanID, did)
			},
			func() (int, int, error) {
				return scanGDTrustedEntitySets(ctx, client, acct, region, st, scanID, did)
			},
		} {
			t, i, perr := phase()
			if perr != nil {
				return total, inserted, perr
			}
			total += t
			inserted += i
		}
	}

	t, i, orgRefused, ferr := scanGDOrganizationConfigurations(ctx, client, acct, region, st, scanID, detectorIDs)
	total += t
	inserted += i
	// The custom-detection-rule ops take no detector, but a region without one
	// has GuardDuty off. Rule associations and organization configurations in
	// such a region are not listed; whether they can exist there is unverified.
	if len(detectorIDs) == 0 {
		return total, inserted, ferr
	}
	var errs phaseErrs
	errs.add("guardduty:DescribeOrganizationConfiguration", ferr)
	t, i = scanGDCustomDetectionRules(ctx, client, acct, region, st, scanID, orgRefused, &errs)
	total += t
	inserted += i
	return total, inserted, errs.join(st, acct, region)
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
	const op = "guardduty:DescribeOrganizationConfiguration"
	parents := make([]childParent, len(detectorIDs))
	for i, did := range detectorIDs {
		parents[i] = childParent{id: did, arn: guardDutyDetectorARN(region, acct.ID, did)}
	}
	var refusedOnce sync.Once
	warnIfRefused := func(err error) bool {
		if !errors.Is(err, errGDOrgConfigRefused) {
			return false
		}
		refusedOnce.Do(func() {
			refused = true
			_ = skipIfAccessDenied(st, op, acct.ID, region, err)
		})
		return true
	}
	total, inserted, err = childFanOut(ctx, st, acct, region, op, parents, warnIfRefused,
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

func scanGDMalwareProtectionPlans(ctx context.Context, client guardDutyExtAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	// SDK exposes no paginator; manual NextToken loop.
	input := &guardduty.ListMalwareProtectionPlansInput{}
	var batch []*store.Resource
	for {
		out, err := client.ListMalwareProtectionPlans(ctx, input)
		if err != nil {
			if isAccessDenied(err) {
				_ = skipIfAccessDenied(st, "guardduty:ListMalwareProtectionPlans", acct.ID, region, err)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("guardduty:ListMalwareProtectionPlans: %w", err)
		}
		for _, p := range out.MalwareProtectionPlans {
			id := sv(p.MalwareProtectionPlanId)
			if id == "" {
				continue
			}
			arn := fmt.Sprintf("arn:aws:guardduty:%s:%s:malware-protection-plan/%s", region, acct.ID, id)
			label := id
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeGuardDutyMalwareProtectionPlan, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(p), DiscoveredBy: scanID,
			})
		}
		if out.NextToken == nil || *out.NextToken == "" {
			break
		}
		input.NextToken = out.NextToken
	}
	return upsertBatch(st, batch, "guardduty malware-protection-plans")
}

func scanGDPublishingDestinations(ctx context.Context, client guardDutyExtAPI, acct *account, region string, st *store.Store, scanID, detectorID string) (int, int, error) {
	did := detectorID
	pager := guardduty.NewListPublishingDestinationsPaginator(client, &guardduty.ListPublishingDestinationsInput{DetectorId: &did})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "guardduty:ListPublishingDestinations", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("guardduty:ListPublishingDestinations: %w", perr)
		}
		for _, d := range out.Destinations {
			id := sv(d.DestinationId)
			if id == "" {
				continue
			}
			arn := fmt.Sprintf("arn:aws:guardduty:%s:%s:detector/%s/publishingDestination/%s", region, acct.ID, detectorID, id)
			label := id
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeGuardDutyPublishingDestination, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(d), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "guardduty publishing-destinations")
}

func scanGDThreatEntitySets(ctx context.Context, client guardDutyExtAPI, acct *account, region string, st *store.Store, scanID, detectorID string) (int, int, error) {
	did := detectorID
	pager := guardduty.NewListThreatEntitySetsPaginator(client, &guardduty.ListThreatEntitySetsInput{DetectorId: &did})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isGuardDutyMemberRestricted(perr) {
				return 0, 0, nil
			}
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "guardduty:ListThreatEntitySets", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("guardduty:ListThreatEntitySets: %w", perr)
		}
		for _, id := range out.ThreatEntitySetIds {
			if id == "" {
				continue
			}
			arn := fmt.Sprintf("arn:aws:guardduty:%s:%s:detector/%s/threat-entity-set/%s", region, acct.ID, detectorID, id)
			label := id
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeGuardDutyThreatEntitySet, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(map[string]string{"DetectorId": detectorID, "ThreatEntitySetId": id}), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "guardduty threat-entity-sets")
}

func scanGDThreatIntelSets(ctx context.Context, client guardDutyExtAPI, acct *account, region string, st *store.Store, scanID, detectorID string) (int, int, error) {
	did := detectorID
	pager := guardduty.NewListThreatIntelSetsPaginator(client, &guardduty.ListThreatIntelSetsInput{DetectorId: &did})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "guardduty:ListThreatIntelSets", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("guardduty:ListThreatIntelSets: %w", perr)
		}
		for _, id := range out.ThreatIntelSetIds {
			if id == "" {
				continue
			}
			arn := fmt.Sprintf("arn:aws:guardduty:%s:%s:detector/%s/threatintelset/%s", region, acct.ID, detectorID, id)
			label := id
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeGuardDutyThreatIntelSet, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(map[string]string{"DetectorId": detectorID, "ThreatIntelSetId": id}), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "guardduty threat-intel-sets")
}

func scanGDTrustedEntitySets(ctx context.Context, client guardDutyExtAPI, acct *account, region string, st *store.Store, scanID, detectorID string) (int, int, error) {
	did := detectorID
	pager := guardduty.NewListTrustedEntitySetsPaginator(client, &guardduty.ListTrustedEntitySetsInput{DetectorId: &did})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isGuardDutyMemberRestricted(perr) {
				return 0, 0, nil
			}
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "guardduty:ListTrustedEntitySets", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("guardduty:ListTrustedEntitySets: %w", perr)
		}
		for _, id := range out.TrustedEntitySetIds {
			if id == "" {
				continue
			}
			arn := fmt.Sprintf("arn:aws:guardduty:%s:%s:detector/%s/trusted-entity-set/%s", region, acct.ID, detectorID, id)
			label := id
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeGuardDutyTrustedEntitySet, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(map[string]string{"DetectorId": detectorID, "TrustedEntitySetId": id}), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "guardduty trusted-entity-sets")
}

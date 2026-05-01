package aws

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/store"
	"github.com/aws/aws-sdk-go-v2/service/guardduty"
)

// guardDutyExtAPI lists ops used by extended GuardDuty phases. Threat / Trusted
// entity-set + threat-intel-set + publishing-destination are per-detector;
// malware-protection-plan is account-wide.
type guardDutyExtAPI interface {
	ListMalwareProtectionPlans(context.Context, *guardduty.ListMalwareProtectionPlansInput, ...func(*guardduty.Options)) (*guardduty.ListMalwareProtectionPlansOutput, error)
	ListPublishingDestinations(context.Context, *guardduty.ListPublishingDestinationsInput, ...func(*guardduty.Options)) (*guardduty.ListPublishingDestinationsOutput, error)
	ListThreatEntitySets(context.Context, *guardduty.ListThreatEntitySetsInput, ...func(*guardduty.Options)) (*guardduty.ListThreatEntitySetsOutput, error)
	ListThreatIntelSets(context.Context, *guardduty.ListThreatIntelSetsInput, ...func(*guardduty.Options)) (*guardduty.ListThreatIntelSetsOutput, error)
	ListTrustedEntitySets(context.Context, *guardduty.ListTrustedEntitySetsInput, ...func(*guardduty.Options)) (*guardduty.ListTrustedEntitySetsOutput, error)
	ListDetectors(context.Context, *guardduty.ListDetectorsInput, ...func(*guardduty.Options)) (*guardduty.ListDetectorsOutput, error)
}

// scanGuardDutyExtended runs five extended-coverage phases. Re-lists detectors
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
	return total, inserted, nil
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

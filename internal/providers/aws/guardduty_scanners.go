package aws

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/internal/store"
	"github.com/aws/aws-sdk-go-v2/service/guardduty"
)

func init() {
	registerService(serviceEntry{
		name: "aws:guardduty",
		fn:   scanGuardDuty,
		emits: []coverage.TypeDecl{
			{Service: "guardduty", DiscoType: TypeGuardDutyDetector},
			{Service: "guardduty", DiscoType: TypeGuardDutyFilter},
			{Service: "guardduty", DiscoType: TypeGuardDutyIPSet},
			{Service: "guardduty", DiscoType: TypeGuardDutyMember, Synthetic: true},
		},
	})
}

// guarddutyAPI is the narrow set of GuardDuty operations called by scanGuardDuty.
type guarddutyAPI interface {
	ListDetectors(context.Context, *guardduty.ListDetectorsInput, ...func(*guardduty.Options)) (*guardduty.ListDetectorsOutput, error)
	GetDetector(context.Context, *guardduty.GetDetectorInput, ...func(*guardduty.Options)) (*guardduty.GetDetectorOutput, error)
	ListFilters(context.Context, *guardduty.ListFiltersInput, ...func(*guardduty.Options)) (*guardduty.ListFiltersOutput, error)
	GetFilter(context.Context, *guardduty.GetFilterInput, ...func(*guardduty.Options)) (*guardduty.GetFilterOutput, error)
	ListIPSets(context.Context, *guardduty.ListIPSetsInput, ...func(*guardduty.Options)) (*guardduty.ListIPSetsOutput, error)
	GetIPSet(context.Context, *guardduty.GetIPSetInput, ...func(*guardduty.Options)) (*guardduty.GetIPSetOutput, error)
	ListMembers(context.Context, *guardduty.ListMembersInput, ...func(*guardduty.Options)) (*guardduty.ListMembersOutput, error)
}

// scanGuardDuty discovers GuardDuty detectors and their nested Filters and
// IPSets. GuardDuty permits at most one detector per region, but the SDK
// returns a list for future-proofing — we handle N defensively.
func scanGuardDuty(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := guardduty.NewFromConfig(acct.cfg, func(o *guardduty.Options) { o.Region = region })
	return scanGuardDutyDetectors(ctx, client, acct, region, st, scanID)
}

// scanGuardDutyDetectors holds the testable scan body.
func scanGuardDutyDetectors(ctx context.Context, client guarddutyAPI, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	// List detectors.
	var detectorIDs []string
	pager := guardduty.NewListDetectorsPaginator(client, &guardduty.ListDetectorsInput{})
	for pager.HasMorePages() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "guardduty:ListDetectors", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("guardduty:ListDetectors: %w", err)
		}
		detectorIDs = append(detectorIDs, page.DetectorIds...)
	}
	if len(detectorIDs) == 0 {
		return 0, 0, nil
	}

	type childPair struct {
		r         *store.Resource
		parentARN string
	}
	var (
		detectorBatch []*store.Resource
		filterBatch   []childPair
		ipsetBatch    []childPair
		memberBatch   []childPair
	)
	for _, did := range detectorIDs {
		dArn := fmt.Sprintf("arn:aws:guardduty:%s:%s:detector/%s", region, acct.ID, did)
		desc, err := client.GetDetector(ctx, &guardduty.GetDetectorInput{DetectorId: &did})
		if err != nil {
			if isAccessDenied(err) {
				continue
			}
			return 0, 0, fmt.Errorf("guardduty:GetDetector %s: %w", did, err)
		}
		status := string(desc.Status)
		dr := &store.Resource{
			Provider:       "aws",
			AccountID:      acct.ID,
			AccountName:    &acct.Name,
			Type:           TypeGuardDutyDetector,
			NativeID:       dArn,
			Region:         &region,
			Status:         &status,
			AttributesJSON: mustJSON(desc),
			DiscoveredBy:   scanID,
		}
		if len(desc.Tags) > 0 {
			dr.TagsJSON = mapTagsJSON(desc.Tags)
		}
		detectorBatch = append(detectorBatch, dr)

		// Filters.
		fPager := guardduty.NewListFiltersPaginator(client, &guardduty.ListFiltersInput{DetectorId: &did})
		for fPager.HasMorePages() {
			fp, err := fPager.NextPage(ctx)
			if err != nil {
				if isAccessDenied(err) {
					break
				}
				return 0, 0, fmt.Errorf("guardduty:ListFilters %s: %w", did, err)
			}
			for _, name := range fp.FilterNames {
				fOut, err := client.GetFilter(ctx, &guardduty.GetFilterInput{DetectorId: &did, FilterName: &name})
				if err != nil {
					continue
				}
				arn := fmt.Sprintf("arn:aws:guardduty:%s:%s:detector/%s/filter/%s", region, acct.ID, did, name)
				filterBatch = append(filterBatch, childPair{
					r: &store.Resource{
						Provider:       "aws",
						AccountID:      acct.ID,
						AccountName:    &acct.Name,
						Type:           TypeGuardDutyFilter,
						NativeID:       arn,
						Name:           fOut.Name,
						Region:         &region,
						AttributesJSON: mustJSON(fOut),
						DiscoveredBy:   scanID,
					},
					parentARN: dArn,
				})
			}
		}

		// Members. Only the master/admin detector returns members; non-master
		// accounts get empty pages. Per-detector ARN shape:
		//   arn:aws:guardduty:{r}:{a}:detector/{did}/member/{memberAcctId}
		// Resolver consumes Members to emit edges to the org account row.
		mPager := guardduty.NewListMembersPaginator(client, &guardduty.ListMembersInput{DetectorId: &did})
		for mPager.HasMorePages() {
			mp, err := mPager.NextPage(ctx)
			if err != nil {
				if isAccessDenied(err) {
					break
				}
				return 0, 0, fmt.Errorf("guardduty:ListMembers %s: %w", did, err)
			}
			for _, m := range mp.Members {
				if m.AccountId == nil || *m.AccountId == "" {
					continue
				}
				arn := fmt.Sprintf("arn:aws:guardduty:%s:%s:detector/%s/member/%s", region, acct.ID, did, *m.AccountId)
				memberBatch = append(memberBatch, childPair{
					r: &store.Resource{
						Provider:       "aws",
						AccountID:      acct.ID,
						AccountName:    &acct.Name,
						Type:           TypeGuardDutyMember,
						NativeID:       arn,
						Name:           m.AccountId,
						Region:         &region,
						AttributesJSON: mustJSON(m),
						DiscoveredBy:   scanID,
					},
					parentARN: dArn,
				})
			}
		}

		// IP sets.
		iPager := guardduty.NewListIPSetsPaginator(client, &guardduty.ListIPSetsInput{DetectorId: &did})
		for iPager.HasMorePages() {
			ip, err := iPager.NextPage(ctx)
			if err != nil {
				if isAccessDenied(err) {
					break
				}
				return 0, 0, fmt.Errorf("guardduty:ListIPSets %s: %w", did, err)
			}
			for _, id := range ip.IpSetIds {
				ipOut, err := client.GetIPSet(ctx, &guardduty.GetIPSetInput{DetectorId: &did, IpSetId: &id})
				if err != nil {
					continue
				}
				arn := fmt.Sprintf("arn:aws:guardduty:%s:%s:detector/%s/ipset/%s", region, acct.ID, did, id)
				ipsetBatch = append(ipsetBatch, childPair{
					r: &store.Resource{
						Provider:       "aws",
						AccountID:      acct.ID,
						AccountName:    &acct.Name,
						Type:           TypeGuardDutyIPSet,
						NativeID:       arn,
						Name:           ipOut.Name,
						Region:         &region,
						AttributesJSON: mustJSON(ipOut),
						DiscoveredBy:   scanID,
					},
					parentARN: dArn,
				})
			}
		}
	}

	// Upsert detectors first (closure FK).
	if len(detectorBatch) > 0 {
		n, err := st.UpsertResources(detectorBatch)
		if err != nil {
			return 0, 0, fmt.Errorf("upsert GuardDuty detectors: %w", err)
		}
		total += len(detectorBatch)
		inserted += n
	}
	upsertChildren := func(kind string, batch []childPair) error {
		if len(batch) == 0 {
			return nil
		}
		rs := make([]*store.Resource, len(batch))
		for i, c := range batch {
			rs[i] = c.r
		}
		n, err := st.UpsertResources(rs)
		if err != nil {
			return fmt.Errorf("upsert GuardDuty %s: %w", kind, err)
		}
		total += len(rs)
		inserted += n
		pairs := make([][2]string, len(batch))
		for i, c := range batch {
			pid := store.ResourceID("aws", acct.ID, TypeGuardDutyDetector, c.parentARN)
			pairs[i] = [2]string{c.r.ID, pid}
		}
		if err := st.RecordHierarchyBatch(pairs); err != nil {
			return fmt.Errorf("closure GuardDuty %s: %w", kind, err)
		}
		return nil
	}
	if err := upsertChildren("filters", filterBatch); err != nil {
		return 0, 0, err
	}
	if err := upsertChildren("ipsets", ipsetBatch); err != nil {
		return 0, 0, err
	}
	if err := upsertChildren("members", memberBatch); err != nil {
		return 0, 0, err
	}
	return total, inserted, nil
}

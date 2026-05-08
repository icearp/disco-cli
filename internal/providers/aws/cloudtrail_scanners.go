package aws

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/internal/store"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cloudtrail"
)

func init() {
	registerService(serviceEntry{
		name: "aws:cloudtrail",
		fn:   scanCloudTrail,
		emits: []coverage.TypeDecl{
			{Service: "cloudtrail", DiscoType: TypeCloudTrailTrail},
			{Service: "cloudtrail", DiscoType: TypeCloudTrailEventDataStore},
			{Service: "cloudtrail", DiscoType: TypeCloudTrailChannel},
			{Service: "cloudtrail", DiscoType: TypeCloudTrailDashboard, Leaf: true},
			{Service: "cloudtrail", DiscoType: TypeCloudTrailResourcePolicy},
		},
	})
}

// cloudtrailAPI is the narrow set of CloudTrail operations called by
// scanCloudTrailAll.
type cloudtrailAPI interface {
	DescribeTrails(context.Context, *cloudtrail.DescribeTrailsInput, ...func(*cloudtrail.Options)) (*cloudtrail.DescribeTrailsOutput, error)
	GetTrailStatus(context.Context, *cloudtrail.GetTrailStatusInput, ...func(*cloudtrail.Options)) (*cloudtrail.GetTrailStatusOutput, error)
	ListTags(context.Context, *cloudtrail.ListTagsInput, ...func(*cloudtrail.Options)) (*cloudtrail.ListTagsOutput, error)
	ListEventDataStores(context.Context, *cloudtrail.ListEventDataStoresInput, ...func(*cloudtrail.Options)) (*cloudtrail.ListEventDataStoresOutput, error)
	GetEventDataStore(context.Context, *cloudtrail.GetEventDataStoreInput, ...func(*cloudtrail.Options)) (*cloudtrail.GetEventDataStoreOutput, error)
	ListChannels(context.Context, *cloudtrail.ListChannelsInput, ...func(*cloudtrail.Options)) (*cloudtrail.ListChannelsOutput, error)
	GetChannel(context.Context, *cloudtrail.GetChannelInput, ...func(*cloudtrail.Options)) (*cloudtrail.GetChannelOutput, error)
	ListDashboards(context.Context, *cloudtrail.ListDashboardsInput, ...func(*cloudtrail.Options)) (*cloudtrail.ListDashboardsOutput, error)
	GetDashboard(context.Context, *cloudtrail.GetDashboardInput, ...func(*cloudtrail.Options)) (*cloudtrail.GetDashboardOutput, error)
	GetResourcePolicy(context.Context, *cloudtrail.GetResourcePolicyInput, ...func(*cloudtrail.Options)) (*cloudtrail.GetResourcePolicyOutput, error)
}

// scanCloudTrail discovers CloudTrail trails in one region. DescribeTrails
// with IncludeShadowTrails=false returns only trails whose home region matches
// the current region, preventing duplicates across regions.
func scanCloudTrail(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	cfg := acct.cfg
	cfg.Region = region
	client := cloudtrail.NewFromConfig(cfg)
	return scanCloudTrailAll(ctx, client, acct, region, st, scanID)
}

// scanCloudTrailAll holds the testable scan body.
func scanCloudTrailAll(ctx context.Context, client cloudtrailAPI, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	// Phase 1: classic trails. AccessDenied here does not bar Phase 2 — Lake
	// event-data-store permissions are granted independently.
	out, err := client.DescribeTrails(ctx, &cloudtrail.DescribeTrailsInput{
		IncludeShadowTrails: aws.Bool(false),
	})
	switch {
	case err != nil && isAccessDenied(err):
		_ = skipIfAccessDenied(st, "cloudtrail:DescribeTrails", acct.ID, region, err)
	case err != nil:
		return 0, 0, fmt.Errorf("cloudtrail:DescribeTrails: %w", err)
	default:
		// GetTrailStatus enriches each trail with logging status.
		type trailWithStatus struct {
			Trail  any `json:"Trail"`
			Status any `json:"Status"`
		}
		var batch []*store.Resource
		for _, trail := range out.TrailList {
			arn := sv(trail.TrailARN)
			var statusData any
			if statusOut, sErr := client.GetTrailStatus(ctx, &cloudtrail.GetTrailStatusInput{Name: &arn}); sErr == nil {
				statusData = statusOut
			}
			attrs := trailWithStatus{Trail: trail, Status: statusData}
			r := &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeCloudTrailTrail,
				NativeID:       arn,
				Name:           trail.Name,
				Region:         &region,
				AttributesJSON: mustJSON(attrs),
				DiscoveredBy:   scanID,
			}
			if tagsOut, tErr := client.ListTags(ctx, &cloudtrail.ListTagsInput{ResourceIdList: []string{arn}}); tErr == nil && len(tagsOut.ResourceTagList) > 0 {
				tags := tagsOut.ResourceTagList[0].TagsList
				j := mustJSON(tags)
				r.TagsJSON = &j
			}
			batch = append(batch, r)
		}
		if len(batch) > 0 {
			n, uErr := st.UpsertResources(batch)
			if uErr != nil {
				return 0, 0, fmt.Errorf("upsert CloudTrail trails: %w", uErr)
			}
			total += len(batch)
			inserted += n
		}
	}

	// Phase 2: CloudTrail Lake event data stores. List returns sparse entries;
	// GetEventDataStore supplies KmsKeyId + FederationRoleArn needed by resolvers.
	p := cloudtrail.NewListEventDataStoresPaginator(client, &cloudtrail.ListEventDataStoresInput{})
	t2, n2, err := pageScanConcurrent(ctx, "cloudtrail:ListEventDataStores", acct, region, st,
		p.HasMorePages,
		func(c context.Context) (*cloudtrail.ListEventDataStoresOutput, error) { return p.NextPage(c) },
		func(o *cloudtrail.ListEventDataStoresOutput) []string {
			out := make([]string, 0, len(o.EventDataStores))
			for _, e := range o.EventDataStores {
				if a := sv(e.EventDataStoreArn); a != "" {
					out = append(out, a)
				}
			}
			return out
		},
		func(gctx context.Context, arn string) (*store.Resource, error) {
			detail, gErr := client.GetEventDataStore(gctx, &cloudtrail.GetEventDataStoreInput{EventDataStore: &arn})
			if gErr != nil {
				if isAccessDenied(gErr) {
					return nil, nil
				}
				return nil, fmt.Errorf("cloudtrail:GetEventDataStore %s: %w", arn, gErr)
			}
			status := string(detail.Status)
			return &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeCloudTrailEventDataStore,
				NativeID:       sv(detail.EventDataStoreArn),
				Name:           detail.Name,
				Region:         &region,
				Status:         &status,
				AttributesJSON: mustJSON(detail),
				DiscoveredBy:   scanID,
			}, nil
		}, 0)
	total += t2
	inserted += n2
	if err != nil {
		return total, inserted, err
	}

	t3, n3, eErr := scanCloudTrailExtended(ctx, client, acct, region, st, scanID)
	total += t3
	inserted += n3
	return total, inserted, eErr
}

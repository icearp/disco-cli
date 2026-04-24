package aws

import (
	"context"
	"fmt"
	"sync"

	"codeberg.org/icearp/disco/internal/store"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cloudtrail"
	"golang.org/x/sync/errgroup"
)

func init() { registerService(serviceEntry{name: "aws:cloudtrail", fn: scanCloudTrail}) }

// scanCloudTrail discovers CloudTrail trails in one region. DescribeTrails
// with IncludeShadowTrails=false returns only trails whose home region matches
// the current region, preventing duplicates across regions.
func scanCloudTrail(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := cloudtrail.NewFromConfig(acct.cfg, func(o *cloudtrail.Options) { o.Region = region })

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
	paginator := cloudtrail.NewListEventDataStoresPaginator(client, &cloudtrail.ListEventDataStoresInput{})
	var arns []string
	for paginator.HasMorePages() {
		page, pErr := paginator.NextPage(ctx)
		if pErr != nil {
			if isAccessDenied(pErr) {
				_ = skipIfAccessDenied(st, "cloudtrail:ListEventDataStores", acct.ID, region, pErr)
				return total, inserted, nil
			}
			return total, inserted, fmt.Errorf("cloudtrail:ListEventDataStores: %w", pErr)
		}
		for _, e := range page.EventDataStores {
			if a := sv(e.EventDataStoreArn); a != "" {
				arns = append(arns, a)
			}
		}
	}
	if len(arns) == 0 {
		return total, inserted, nil
	}

	var (
		mu       sync.Mutex
		edsBatch []*store.Resource
	)
	g, gctx := errgroup.WithContext(ctx)
	for _, arn := range arns {
		g.Go(func() error {
			detail, gErr := client.GetEventDataStore(gctx, &cloudtrail.GetEventDataStoreInput{EventDataStore: &arn})
			if gErr != nil {
				if isAccessDenied(gErr) {
					return nil
				}
				return fmt.Errorf("cloudtrail:GetEventDataStore %s: %w", arn, gErr)
			}
			status := string(detail.Status)
			r := &store.Resource{
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
			}
			mu.Lock()
			edsBatch = append(edsBatch, r)
			mu.Unlock()
			return nil
		})
	}
	if gErr := g.Wait(); gErr != nil {
		return total, inserted, gErr
	}
	if len(edsBatch) > 0 {
		n, uErr := st.UpsertResources(edsBatch)
		if uErr != nil {
			return total, inserted, fmt.Errorf("upsert CloudTrail event-data-stores: %w", uErr)
		}
		total += len(edsBatch)
		inserted += n
	}
	return total, inserted, nil
}

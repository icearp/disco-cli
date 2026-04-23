package aws

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/store"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cloudtrail"
)

func init() { registerService(serviceEntry{name: "aws:cloudtrail", fn: scanCloudTrail}) }

// scanCloudTrail discovers CloudTrail trails in one region. DescribeTrails
// with IncludeShadowTrails=false returns only trails whose home region matches
// the current region, preventing duplicates across regions.
func scanCloudTrail(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := cloudtrail.NewFromConfig(acct.cfg, func(o *cloudtrail.Options) { o.Region = region })

	out, err := client.DescribeTrails(ctx, &cloudtrail.DescribeTrailsInput{
		IncludeShadowTrails: aws.Bool(false),
	})
	if err != nil {
		if isAccessDenied(err) {
			return 0, 0, skipIfAccessDenied(st, "cloudtrail:DescribeTrails", acct.ID, region, err)
		}
		return 0, 0, fmt.Errorf("cloudtrail:DescribeTrails: %w", err)
	}
	if len(out.TrailList) == 0 {
		return 0, 0, nil
	}

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
		n, err := st.UpsertResources(batch)
		if err != nil {
			return 0, 0, fmt.Errorf("upsert CloudTrail trails: %w", err)
		}
		total += len(batch)
		inserted += n
	}
	return total, inserted, nil
}

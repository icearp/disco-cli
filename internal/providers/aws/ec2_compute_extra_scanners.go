package aws

import (
	"context"

	"codeberg.org/icearp/disco/internal/restype"
	"codeberg.org/icearp/disco/store"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
)

func init() {
	registerType(restype.Descriptor{Type: TypeEC2SpotInstanceRequest, Service: "ec2", Upstream: "AWS::ec2::spot-instances-request"})
	registerType(restype.Descriptor{Type: TypeEC2InstanceEventWindow, Service: "ec2", Upstream: "AWS::ec2::instance-event-window"})
}

// scanEC2ComputeExtra discovers Spot instance requests and instance event
// windows (scheduled-event maintenance windows).
func scanEC2ComputeExtra(ctx context.Context, client ec2API, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	return runScanners(
		ctx,
		func(ctx context.Context) (int, int, error) {
			return scanSpotInstanceRequests(ctx, client, acct, region, st, scanID)
		},
		func(ctx context.Context) (int, int, error) {
			return scanInstanceEventWindows(ctx, client, acct, region, st, scanID)
		},
	)
}

func scanSpotInstanceRequests(ctx context.Context, client ec2API, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	return ec2PageScan(
		ctx, "ec2:DescribeSpotInstanceRequests", acct, region, st,
		ec2.NewDescribeSpotInstanceRequestsPaginator(client, &ec2.DescribeSpotInstanceRequestsInput{}),
		func(page *ec2.DescribeSpotInstanceRequestsOutput) []*store.Resource {
			var out []*store.Resource
			for _, sir := range page.SpotInstanceRequests {
				id := sv(sir.SpotInstanceRequestId)
				if id == "" {
					continue
				}
				status := string(sir.State)
				out = append(out, &store.Resource{
					Provider:       "aws",
					AccountID:      acct.ID,
					AccountName:    &acct.Name,
					Type:           TypeEC2SpotInstanceRequest,
					NativeID:       ec2ARN(region, acct.ID, "spot-instances-request", id),
					Region:         &region,
					Status:         &status,
					TagsJSON:       awsTagsJSON(sir.Tags),
					AttributesJSON: mustJSON(sir),
					DiscoveredBy:   scanID,
				})
			}
			return out
		},
	)
}

func scanInstanceEventWindows(ctx context.Context, client ec2API, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	return ec2PageScan(
		ctx, "ec2:DescribeInstanceEventWindows", acct, region, st,
		ec2.NewDescribeInstanceEventWindowsPaginator(client, &ec2.DescribeInstanceEventWindowsInput{}),
		func(page *ec2.DescribeInstanceEventWindowsOutput) []*store.Resource {
			var out []*store.Resource
			for _, w := range page.InstanceEventWindows {
				id := sv(w.InstanceEventWindowId)
				if id == "" {
					continue
				}
				status := string(w.State)
				out = append(out, &store.Resource{
					Provider:       "aws",
					AccountID:      acct.ID,
					AccountName:    &acct.Name,
					Type:           TypeEC2InstanceEventWindow,
					NativeID:       ec2ARN(region, acct.ID, "instance-event-window", id),
					Name:           w.Name,
					Region:         &region,
					Status:         &status,
					TagsJSON:       awsTagsJSON(w.Tags),
					AttributesJSON: mustJSON(w),
					DiscoveredBy:   scanID,
				})
			}
			return out
		},
	)
}

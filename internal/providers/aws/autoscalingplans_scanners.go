package aws

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/restype"
	"codeberg.org/icearp/disco/store"
	"github.com/aws/aws-sdk-go-v2/service/autoscalingplans"
)

func init() {
	registerType(restype.Descriptor{Type: TypeAutoScalingPlansScalingPlan, Service: "autoscaling-plans", Upstream: "AWS::AutoScalingPlans::ScalingPlan"})
	registerService(serviceEntry{
		name: "aws:autoscaling-plans",
		fn:   scanAutoScalingPlans,
	})
}

// autoScalingPlansAPI is the narrow set of AutoScalingPlans operations called.
type autoScalingPlansAPI interface {
	DescribeScalingPlans(context.Context, *autoscalingplans.DescribeScalingPlansInput, ...func(*autoscalingplans.Options)) (*autoscalingplans.DescribeScalingPlansOutput, error)
}

// autoScalingPlanARN synthesizes a NativeID for a scaling plan. AWS issues
// no ARN on the SDK shape; identity is (name, version) per AWS docs. Synth
// shape mirrors the parent-child path precedent.
func autoScalingPlanARN(region, accountID, name string, version int64) string {
	return fmt.Sprintf("arn:aws:autoscaling:%s:%s:scalingPlan/%s/%d", region, accountID, name, version)
}

// scanAutoScalingPlans paginates DescribeScalingPlans via manual NextToken
// loop (no SDK paginator helper available for this op).
func scanAutoScalingPlans(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := autoscalingplans.NewFromConfig(acct.cfg, func(o *autoscalingplans.Options) { o.Region = region })
	return scanAutoScalingPlansEntities(ctx, client, acct, region, st, scanID)
}

func scanAutoScalingPlansEntities(ctx context.Context, client autoScalingPlansAPI, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	var nextToken *string
	for {
		out, oerr := client.DescribeScalingPlans(ctx, &autoscalingplans.DescribeScalingPlansInput{NextToken: nextToken})
		if oerr != nil {
			if isAccessDenied(oerr) {
				return total, inserted, skipIfAccessDenied(st, "autoscaling-plans:DescribeScalingPlans", acct.ID, region, oerr)
			}
			return total, inserted, fmt.Errorf("autoscaling-plans:DescribeScalingPlans: %w", oerr)
		}
		batch := make([]*store.Resource, 0, len(out.ScalingPlans))
		for _, p := range out.ScalingPlans {
			name := sv(p.ScalingPlanName)
			if name == "" {
				continue
			}
			var version int64
			if p.ScalingPlanVersion != nil {
				version = *p.ScalingPlanVersion
			}
			arn := autoScalingPlanARN(region, acct.ID, name, version)
			status := string(p.StatusCode)
			batch = append(batch, &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeAutoScalingPlansScalingPlan,
				NativeID:       arn,
				Name:           &name,
				Region:         &region,
				Status:         &status,
				CreatedAt:      tp(p.CreationTime),
				AttributesJSON: mustJSON(p),
				DiscoveredBy:   scanID,
			})
		}
		if len(batch) > 0 {
			n, uerr := st.UpsertResources(batch)
			if uerr != nil {
				return total, inserted, fmt.Errorf("upsert autoscaling-plans: %w", uerr)
			}
			total += len(batch)
			inserted += n
		}
		if out.NextToken == nil || *out.NextToken == "" {
			break
		}
		nextToken = out.NextToken
	}
	return total, inserted, nil
}

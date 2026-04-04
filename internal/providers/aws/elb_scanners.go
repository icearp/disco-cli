package aws

import (
	"context"
	"fmt"

	"codeburg.org/icearp/disco/internal/store"
	"github.com/aws/aws-sdk-go-v2/service/elasticloadbalancingv2"
)

// scanELB discovers Application, Network, and Gateway load balancers (ELBv2)
// in one region.
func scanELB(ctx context.Context, acct *account, region string, st *store.Store, scanID string) error {
	client := elasticloadbalancingv2.NewFromConfig(acct.cfg, func(o *elasticloadbalancingv2.Options) { o.Region = region })

	pager := elasticloadbalancingv2.NewDescribeLoadBalancersPaginator(client, &elasticloadbalancingv2.DescribeLoadBalancersInput{})
	for pager.HasMorePages() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return skipIfAccessDenied("elasticloadbalancing:DescribeLoadBalancers", acct.ID, region, err)
			}
			return fmt.Errorf("elasticloadbalancing:DescribeLoadBalancers: %w", err)
		}
		var batch []*store.Resource
		for _, lb := range page.LoadBalancers {
			name := sv(lb.LoadBalancerName)
			status := string(lb.State.Code)
			lbType := string(lb.Type)
			r := &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           "aws:elasticloadbalancing:load-balancer",
				NativeID:       sv(lb.LoadBalancerArn),
				Name:           &name,
				Region:         &region,
				Status:         &status,
				AttributesJSON: mustJSON(map[string]any{"lb": lb, "type": lbType}),
				ScanID:         scanID,
			}
			batch = append(batch, r)
		}
		if len(batch) > 0 {
			if err := st.UpsertResources(batch); err != nil {
				return fmt.Errorf("upsert load balancers: %w", err)
			}
		}
	}
	return nil
}

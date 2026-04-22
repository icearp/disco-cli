package aws

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/store"
	"github.com/aws/aws-sdk-go-v2/service/elasticloadbalancing"
)

func init() {
	registerService(serviceEntry{name: "aws:elasticloadbalancing", fn: scanELBClassic})
}

// scanELBClassic discovers Classic (v1) load balancers in one region.
// Classic ELBs have no ARN in the API response; we synthesise one as
// arn:aws:elasticloadbalancing:<region>:<accountID>:loadbalancer/<name>.
func scanELBClassic(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := elasticloadbalancing.NewFromConfig(acct.cfg, func(o *elasticloadbalancing.Options) { o.Region = region })

	pager := elasticloadbalancing.NewDescribeLoadBalancersPaginator(client, &elasticloadbalancing.DescribeLoadBalancersInput{})
	for pager.HasMorePages() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied("elasticloadbalancing:DescribeLoadBalancers", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("elasticloadbalancing:DescribeLoadBalancers: %w", err)
		}
		var batch []*store.Resource
		for _, lb := range page.LoadBalancerDescriptions {
			name := sv(lb.LoadBalancerName)
			// Synthesise ARN — classic ELBs predate ARNs in the API.
			nativeID := fmt.Sprintf("arn:aws:elasticloadbalancing:%s:%s:loadbalancer/%s", region, acct.ID, name)
			r := &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeELBClassicLoadBalancer,
				NativeID:       nativeID,
				Name:           &name,
				Region:         &region,
				CreatedAt:      tp(lb.CreatedTime),
				AttributesJSON: mustJSON(lb),
				DiscoveredBy:   scanID,
			}
			batch = append(batch, r)
		}
		if len(batch) > 0 {
			n, err := st.UpsertResources(batch)
			if err != nil {
				return 0, 0, fmt.Errorf("upsert classic load balancers: %w", err)
			}
			total += len(batch)
			inserted += n
		}
	}
	return total, inserted, nil
}

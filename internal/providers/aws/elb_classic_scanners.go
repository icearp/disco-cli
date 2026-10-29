package aws

import (
	"context"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/elasticloadbalancing"
	elbtypes "github.com/aws/aws-sdk-go-v2/service/elasticloadbalancing/types"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerType(restype.Descriptor{Type: TypeELBClassicLoadBalancer, Service: "elasticloadbalancing"})
	registerService(serviceEntry{
		name: "aws:elasticloadbalancing",
		fn:   scanELBClassic,
	})
}

// elbClassicAPI is the narrow set of Classic ELB operations called by
// scanELBClassicLoadBalancers.
type elbClassicAPI interface {
	DescribeLoadBalancers(context.Context, *elasticloadbalancing.DescribeLoadBalancersInput, ...func(*elasticloadbalancing.Options)) (*elasticloadbalancing.DescribeLoadBalancersOutput, error)
	DescribeTags(context.Context, *elasticloadbalancing.DescribeTagsInput, ...func(*elasticloadbalancing.Options)) (*elasticloadbalancing.DescribeTagsOutput, error)
}

// scanELBClassic discovers Classic (v1) load balancers in one region.
// Classic ELBs have no ARN in the API response; we synthesise one as
// arn:{p}:elasticloadbalancing:<region>:<accountID>:loadbalancer/<name>.
func scanELBClassic(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := elasticloadbalancing.NewFromConfig(acct.cfg, func(o *elasticloadbalancing.Options) { o.Region = region })
	return scanELBClassicLoadBalancers(ctx, client, acct, region, st, scanID)
}

// scanELBClassicLoadBalancers stores every Classic load balancer, a page at
// a time, with its tags read by DescribeTags, sent the names of 20 load
// balancers a call (elbClassicTags). A failed page keeps the pages stored
// before it.
func scanELBClassicLoadBalancers(ctx context.Context, client elbClassicAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	bulk := elbClassicTags(client)
	return runHostPhases(ctx, st, acct, region, "elasticloadbalancing", 0, []hostPhase{
		{op: "elasticloadbalancing:DescribeLoadBalancers", run: func(p *phaseTotals) bool {
			return storeTokenPagesTags(ctx, st, acct, region, "elasticloadbalancing:DescribeLoadBalancers",
				func(ctx context.Context, marker *string) ([]elbtypes.LoadBalancerDescription, *string, error) {
					out, err := client.DescribeLoadBalancers(ctx, &elasticloadbalancing.DescribeLoadBalancersInput{Marker: marker, PageSize: aws.Int32(elbPageCap)})
					if err != nil {
						return nil, nil, err
					}
					fetchItemTags(ctx, bulk, out.LoadBalancerDescriptions, func(lb elbtypes.LoadBalancerDescription) *string { return lb.LoadBalancerName })
					return out.LoadBalancerDescriptions, out.NextMarker, nil
				},
				func(lb elbtypes.LoadBalancerDescription) (*store.Resource, string, string) {
					name := sv(lb.LoadBalancerName)
					// One empty name would store every such element under one
					// NativeID, loadbalancer/ with no name.
					if name == "" {
						return nil, "load balancers have no name", noIdentifier
					}
					return &store.Resource{
						Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
						Type:     TypeELBClassicLoadBalancer,
						NativeID: arnFor(acct.partition(), "elasticloadbalancing", region, acct.ID, "loadbalancer/"+name),
						Name:     &name, Region: &region, CreatedAt: tp(lb.CreatedTime),
						AttributesJSON: mustJSON(lb), DiscoveredBy: scanID,
					}, "", ""
				},
				bulk.bulkTagReader(), p)
		}},
	})
}

// elbClassicTags reads a load balancer's tags with DescribeTags, sent its
// name (the row's Name); the op's response names each load balancer it
// describes. LoadBalancerNotFound, the code of its one modeled error
// (AccessPointNotFoundException), drops a load balancer deleted after it was
// listed.
func elbClassicTags(client elbClassicAPI) *bulkRowTags {
	return newBulkRowTags(elbTagsOp, elbTagBatch, []string{"LoadBalancerNotFound"},
		func(r *store.Resource) string { return sv(r.Name) },
		func(ctx context.Context, names []string) (map[string]*string, error) {
			out, err := client.DescribeTags(ctx, &elasticloadbalancing.DescribeTagsInput{LoadBalancerNames: names})
			if err != nil {
				return nil, err
			}
			got := make(map[string]*string, len(out.TagDescriptions))
			for _, d := range out.TagDescriptions {
				got[sv(d.LoadBalancerName)] = kvTagsJSON(d.Tags, func(t elbtypes.Tag) (*string, *string) { return t.Key, t.Value })
			}
			return got, nil
		})
}

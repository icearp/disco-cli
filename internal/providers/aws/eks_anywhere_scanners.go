package aws

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/service/eks"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerType(restype.Descriptor{Type: TypeEKSAnywhereSubscription, Service: "eks", Upstream: "AWS::eks::eks-anywhere-subscription", Leaf: true})
}

type eksAnywhereAPI interface {
	ListEksAnywhereSubscriptions(context.Context, *eks.ListEksAnywhereSubscriptionsInput, ...func(*eks.Options)) (*eks.ListEksAnywhereSubscriptionsOutput, error)
}

// scanEKSAnywhereSubscriptions discovers EKS Anywhere support subscriptions
// (account-wide). The clusters they cover run on-prem, not in AWS, so the
// subscription is Leaf.
func scanEKSAnywhereSubscriptions(ctx context.Context, client eksAnywhereAPI, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	p := eks.NewListEksAnywhereSubscriptionsPaginator(client, &eks.ListEksAnywhereSubscriptionsInput{})
	var batch []*store.Resource
	for p.HasMorePages() {
		out, perr := p.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				return 0, 0, skipIfAccessDenied(st, "eks:ListEksAnywhereSubscriptions", acct.ID, region, perr)
			}
			return 0, 0, fmt.Errorf("eks:ListEksAnywhereSubscriptions: %w", perr)
		}
		for _, s := range out.Subscriptions {
			arn := sv(s.Arn)
			if arn == "" {
				continue
			}
			status := sv(s.Status)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeEKSAnywhereSubscription, NativeID: arn,
				Region: &region, Status: &status, CreatedAt: tp(s.CreatedAt),
				TagsJSON: mapTagsJSON(s.Tags), AttributesJSON: mustJSON(s), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "eks anywhere-subscriptions")
}

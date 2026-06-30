package aws

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/store"
	"github.com/aws/aws-sdk-go-v2/service/swf"
	swftypes "github.com/aws/aws-sdk-go-v2/service/swf/types"
)

func init() {
	registerService(serviceEntry{
		name: "aws:swf",
		fn:   scanSWF,
		emits: []coverage.TypeDecl{
			{Service: "swf", DiscoType: TypeSWFDomain, Leaf: true},
		},
	})
}

// swfAPI is the narrow surface scanSWFDomains uses. ListDomains requires a
// RegistrationStatus and is paginator-native.
type swfAPI interface {
	ListDomains(context.Context, *swf.ListDomainsInput, ...func(*swf.Options)) (*swf.ListDomainsOutput, error)
}

func scanSWF(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := swf.NewFromConfig(acct.cfg, func(o *swf.Options) { o.Region = region })
	return scanSWFDomains(ctx, client, acct, region, st, scanID)
}

func scanSWFDomains(ctx context.Context, client swfAPI, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	pager := swf.NewListDomainsPaginator(client, &swf.ListDomainsInput{
		RegistrationStatus: swftypes.RegistrationStatusRegistered,
	}, func(o *swf.ListDomainsPaginatorOptions) { o.Limit = 100 })
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				return total, inserted, skipIfAccessDenied(st, "swf:ListDomains", acct.ID, region, perr)
			}
			return total, inserted, fmt.Errorf("swf:ListDomains: %w", perr)
		}
		for _, d := range out.DomainInfos {
			arn := sv(d.Arn)
			if arn == "" {
				continue
			}
			name := sv(d.Name)
			status := string(d.Status)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeSWFDomain, NativeID: arn,
				Name: &name, Region: &region, Status: &status,
				AttributesJSON: mustJSON(d), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "swf domains")
}

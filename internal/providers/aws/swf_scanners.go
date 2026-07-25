package aws

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/service/swf"
	swftypes "github.com/aws/aws-sdk-go-v2/service/swf/types"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerType(restype.Descriptor{Type: TypeSWFDomain, Service: "swf", Leaf: true})
	registerService(serviceEntry{
		name: "aws:swf",
		fn:   scanSWF,
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

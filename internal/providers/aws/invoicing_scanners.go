package aws

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/internal/store"
	"github.com/aws/aws-sdk-go-v2/service/invoicing"
)

func init() {
	registerService(serviceEntry{
		name:   "aws:invoicing",
		global: true,
		fn:     scanInvoicing,
		emits: []coverage.TypeDecl{
			{Service: "invoicing", DiscoType: TypeInvoicingInvoiceUnit},
		},
	})
}

type invoicingAPI interface {
	ListInvoiceUnits(context.Context, *invoicing.ListInvoiceUnitsInput, ...func(*invoicing.Options)) (*invoicing.ListInvoiceUnitsOutput, error)
}

// scanInvoicing discovers Invoicing invoice units. Service is global; gate
// to us-east-1 to avoid duplicate scans across regions.
func scanInvoicing(ctx context.Context, acct *account, _ string, st *store.Store, scanID string) (total, inserted int, err error) {
	region := "us-east-1"
	client := invoicing.NewFromConfig(acct.cfg, func(o *invoicing.Options) { o.Region = region })

	var batch []*store.Resource
	var nextToken *string
	for {
		out, err := client.ListInvoiceUnits(ctx, &invoicing.ListInvoiceUnitsInput{NextToken: nextToken})
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "invoicing:ListInvoiceUnits", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("invoicing:ListInvoiceUnits: %w", err)
		}
		for _, u := range out.InvoiceUnits {
			arn := sv(u.InvoiceUnitArn)
			if arn == "" {
				continue
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeInvoicingInvoiceUnit, NativeID: arn,
				Name: u.Name, Region: &region,
				AttributesJSON: mustJSON(u), DiscoveredBy: scanID,
			})
		}
		if out.NextToken == nil || *out.NextToken == "" {
			break
		}
		nextToken = out.NextToken
	}
	return upsertBatch(st, batch, "invoicing invoice-units")
}

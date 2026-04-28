package aws

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/store"
	"github.com/aws/aws-sdk-go-v2/service/lakeformation"
)

func init() { registerService(serviceEntry{name: "aws:lakeformation", fn: scanLakeFormation}) }

// lakeformationAPI is the narrow set of Lake Formation operations called by
// scanLakeFormationResources.
type lakeformationAPI interface {
	ListResources(context.Context, *lakeformation.ListResourcesInput, ...func(*lakeformation.Options)) (*lakeformation.ListResourcesOutput, error)
}

// scanLakeFormation discovers Lake Formation registered data locations in one
// region. ListResources returns the full ResourceInfo body (RoleArn,
// ResourceArn, federation flags) so no Describe fan-out needed. Permissions,
// LF-tags, and data-cells filters deferred until the Glue catalog is also
// scanned (cross-resource FK targets).
func scanLakeFormation(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := lakeformation.NewFromConfig(acct.cfg, func(o *lakeformation.Options) { o.Region = region })
	return scanLakeFormationResources(ctx, client, acct, region, st, scanID)
}

// scanLakeFormationResources holds the testable scan body.
func scanLakeFormationResources(ctx context.Context, client lakeformationAPI, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	pager := lakeformation.NewListResourcesPaginator(client, &lakeformation.ListResourcesInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "lakeformation:ListResources", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("lakeformation:ListResources: %w", perr)
		}
		for _, r := range out.ResourceInfoList {
			arn := sv(r.ResourceArn)
			if arn == "" {
				continue
			}
			batch = append(batch, &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeLakeFormationResource,
				NativeID:       arn,
				Region:         &region,
				AttributesJSON: mustJSON(r),
				DiscoveredBy:   scanID,
			})
		}
	}
	if len(batch) == 0 {
		return 0, 0, nil
	}
	n, uerr := st.UpsertResources(batch)
	if uerr != nil {
		return 0, 0, fmt.Errorf("upsert lakeformation resources: %w", uerr)
	}
	return len(batch), n, nil
}

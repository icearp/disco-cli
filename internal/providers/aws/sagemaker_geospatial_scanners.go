package aws

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/store"
	"github.com/aws/aws-sdk-go-v2/service/sagemakergeospatial"
)

func init() {
	registerService(serviceEntry{
		name: "aws:sagemaker-geospatial",
		fn:   scanSageMakerGeospatial,
		emits: []coverage.TypeDecl{
			{Service: "sagemaker-geospatial", DiscoType: TypeSageMakerGeospatialRasterDataCollection, Leaf: true},
		},
	})
}

// sagemakerGeospatialAPI is the narrow surface used by the geospatial scanner.
type sagemakerGeospatialAPI interface {
	ListRasterDataCollections(context.Context, *sagemakergeospatial.ListRasterDataCollectionsInput, ...func(*sagemakergeospatial.Options)) (*sagemakergeospatial.ListRasterDataCollectionsOutput, error)
}

func scanSageMakerGeospatial(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := sagemakergeospatial.NewFromConfig(acct.cfg, func(o *sagemakergeospatial.Options) { o.Region = region })
	return scanSageMakerGeospatialRasterDataCollections(ctx, client, acct, region, st, scanID)
}

// scanSageMakerGeospatialRasterDataCollections lists the AWS-provided raster
// data collections. These are a managed catalogue (not user-created), so each
// row is flagged ManagedByProvider.
func scanSageMakerGeospatialRasterDataCollections(ctx context.Context, client sagemakerGeospatialAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := sagemakergeospatial.NewListRasterDataCollectionsPaginator(client, &sagemakergeospatial.ListRasterDataCollectionsInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "sagemaker-geospatial:ListRasterDataCollections", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("sagemaker-geospatial:ListRasterDataCollections: %w", perr)
		}
		for _, c := range out.RasterDataCollectionSummaries {
			arn := sv(c.Arn)
			if arn == "" {
				continue
			}
			name := sv(c.Name)
			batch = append(batch, &store.Resource{
				Provider:          "aws",
				AccountID:         acct.ID,
				AccountName:       &acct.Name,
				Type:              TypeSageMakerGeospatialRasterDataCollection,
				NativeID:          arn,
				Name:              &name,
				Region:            &region,
				AttributesJSON:    mustJSON(c),
				DiscoveredBy:      scanID,
				ManagedByProvider: true,
			})
		}
	}
	return upsertBatch(st, batch, "sagemaker-geospatial raster data collections")
}

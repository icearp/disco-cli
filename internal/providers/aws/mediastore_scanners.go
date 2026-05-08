package aws

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/internal/store"
	"github.com/aws/aws-sdk-go-v2/service/mediastore"
)

func init() {
	registerService(serviceEntry{
		name: "aws:media-store",
		fn:   scanMediaStore,
		emits: []coverage.TypeDecl{
			{Service: "media-store", DiscoType: TypeMediaStoreContainer, Leaf: true},
		},
	})
}

// scanMediaStore discovers Elemental MediaStore containers.
func scanMediaStore(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := mediastore.NewFromConfig(acct.cfg, func(o *mediastore.Options) { o.Region = region })

	var batch []*store.Resource
	var nextToken *string
	for {
		out, err := client.ListContainers(ctx, &mediastore.ListContainersInput{NextToken: nextToken})
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "mediastore:ListContainers", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("mediastore:ListContainers: %w", err)
		}
		for _, c := range out.Containers {
			arn := sv(c.ARN)
			if arn == "" {
				continue
			}
			status := string(c.Status)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeMediaStoreContainer, NativeID: arn,
				Name: c.Name, Region: &region, Status: &status,
				AttributesJSON: mustJSON(c), DiscoveredBy: scanID,
			})
		}
		if out.NextToken == nil || *out.NextToken == "" {
			break
		}
		nextToken = out.NextToken
	}
	return upsertBatch(st, batch, "mediastore containers")
}

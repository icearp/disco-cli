package aws

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/internal/store"
	"github.com/aws/aws-sdk-go-v2/service/s3outposts"
)

func init() {
	registerService(serviceEntry{
		name: "aws:s3outposts",
		fn:   scanS3Outposts,
		emits: []coverage.TypeDecl{
			{Service: "s3outposts", DiscoType: TypeS3OutpostsEndpoint},
		},
	})
}

type s3outpostsAPI interface {
	ListEndpoints(context.Context, *s3outposts.ListEndpointsInput, ...func(*s3outposts.Options)) (*s3outposts.ListEndpointsOutput, error)
}

// scanS3Outposts discovers S3 Outposts endpoints. AccessPoint, Bucket, and
// BucketPolicy are skip-logged: they live under the S3Control SDK and require
// chained per-Outpost / per-bucket fan-out across SDK boundaries.
func scanS3Outposts(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := s3outposts.NewFromConfig(acct.cfg, func(o *s3outposts.Options) { o.Region = region })

	pager := s3outposts.NewListEndpointsPaginator(client, &s3outposts.ListEndpointsInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "s3outposts:ListEndpoints", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("s3outposts:ListEndpoints: %w", err)
		}
		for _, e := range out.Endpoints {
			arn := sv(e.EndpointArn)
			if arn == "" {
				continue
			}
			label := arn
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeS3OutpostsEndpoint, NativeID: arn,
				Name: &label, Region: &region,
				AttributesJSON: mustJSON(e), DiscoveredBy: scanID,
			})
		}
	}
	t, i, ferr := upsertBatch(st, batch, "s3outposts endpoints")
	return t, i, ferr
}

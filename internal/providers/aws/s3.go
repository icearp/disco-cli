package aws

import (
	"context"
	"fmt"

	"codeburg.org/icearp/disco/internal/store"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

// scanS3 discovers S3 buckets. S3 is a global service; buckets are returned
// without region info from ListBuckets, but each bucket has a region that can
// be fetched separately. We store the bucket-level region in attributes.
func scanS3(ctx context.Context, acct *account, st *store.Store, scanID string) error {
	// S3 ListBuckets uses a regional endpoint but returns all buckets globally.
	client := s3.NewFromConfig(acct.cfg, func(o *s3.Options) { o.Region = "us-east-1" })

	out, err := client.ListBuckets(ctx, &s3.ListBucketsInput{})
	if err != nil {
		if isAccessDenied(err) {
			return skipIfAccessDenied("s3:ListBuckets", acct.ID, "global", err)
		}
		return fmt.Errorf("s3:ListBuckets: %w", err)
	}

	var batch []*store.Resource
	for _, b := range out.Buckets {
		name := sv(b.Name)
		arn := fmt.Sprintf("arn:aws:s3:::%s", name)
		r := &store.Resource{
			Provider:       "aws",
			AccountID:      acct.ID,
			AccountName:    &acct.Name,
			Type:           "aws:s3:bucket",
			NativeID:       arn,
			Name:           &name,
			AttributesJSON: mustJSON(b),
			ScanID:         scanID,
		}
		batch = append(batch, r)
	}
	if len(batch) > 0 {
		if err := st.UpsertResources(batch); err != nil {
			return fmt.Errorf("upsert S3 buckets: %w", err)
		}
	}
	return nil
}

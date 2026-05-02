package aws

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/internal/store"
	"github.com/aws/aws-sdk-go-v2/service/s3vectors"
)

func init() {
	registerService(serviceEntry{
		name: "aws:s3vectors",
		fn:   scanS3Vectors,
		emits: []coverage.TypeDecl{
			{Service: "s3vectors", DiscoType: TypeS3VectorsVectorBucket},
			{Service: "s3vectors", DiscoType: TypeS3VectorsIndex},
			{Service: "s3vectors", DiscoType: TypeS3VectorsVectorBucketPolicy},
		},
	})
}

type s3vectorsAPI interface {
	ListVectorBuckets(context.Context, *s3vectors.ListVectorBucketsInput, ...func(*s3vectors.Options)) (*s3vectors.ListVectorBucketsOutput, error)
	ListIndexes(context.Context, *s3vectors.ListIndexesInput, ...func(*s3vectors.Options)) (*s3vectors.ListIndexesOutput, error)
	GetVectorBucketPolicy(context.Context, *s3vectors.GetVectorBucketPolicyInput, ...func(*s3vectors.Options)) (*s3vectors.GetVectorBucketPolicyOutput, error)
}

// scanS3Vectors discovers S3 Vectors vector buckets, indexes, and per-bucket
// policies. ARNs native on bucket and index; policy synth ARN = {bucketArn}/policy.
func scanS3Vectors(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := s3vectors.NewFromConfig(acct.cfg, func(o *s3vectors.Options) { o.Region = region })

	bucketARNs, t, i, ferr := scanS3VVectorBuckets(ctx, client, acct, region, st, scanID)
	if ferr != nil {
		return total, inserted, ferr
	}
	total += t
	inserted += i

	t, i, ferr = scanS3VIndexes(ctx, client, acct, region, st, scanID)
	if ferr != nil {
		return total, inserted, ferr
	}
	total += t
	inserted += i

	for _, ba := range bucketARNs {
		t, i, ferr = scanS3VBucketPolicy(ctx, client, acct, region, st, scanID, ba)
		if ferr != nil {
			return total, inserted, ferr
		}
		total += t
		inserted += i
	}
	return total, inserted, nil
}

func scanS3VVectorBuckets(ctx context.Context, client s3vectorsAPI, acct *account, region string, st *store.Store, scanID string) ([]string, int, int, error) {
	pager := s3vectors.NewListVectorBucketsPaginator(client, &s3vectors.ListVectorBucketsInput{})
	var batch []*store.Resource
	var arns []string
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return nil, 0, 0, skipIfAccessDenied(st, "s3vectors:ListVectorBuckets", acct.ID, region, err)
			}
			return nil, 0, 0, fmt.Errorf("s3vectors:ListVectorBuckets: %w", err)
		}
		for _, b := range out.VectorBuckets {
			arn := sv(b.VectorBucketArn)
			if arn == "" {
				continue
			}
			arns = append(arns, arn)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeS3VectorsVectorBucket, NativeID: arn,
				Name: b.VectorBucketName, Region: &region,
				AttributesJSON: mustJSON(b), DiscoveredBy: scanID,
			})
		}
	}
	t, i, err := upsertBatch(st, batch, "s3vectors vector-buckets")
	return arns, t, i, err
}

func scanS3VIndexes(ctx context.Context, client s3vectorsAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := s3vectors.NewListIndexesPaginator(client, &s3vectors.ListIndexesInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "s3vectors:ListIndexes", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("s3vectors:ListIndexes: %w", err)
		}
		for _, idx := range out.Indexes {
			arn := sv(idx.IndexArn)
			if arn == "" {
				continue
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeS3VectorsIndex, NativeID: arn,
				Name: idx.IndexName, Region: &region,
				AttributesJSON: mustJSON(idx), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "s3vectors indexes")
}

func scanS3VBucketPolicy(ctx context.Context, client s3vectorsAPI, acct *account, region string, st *store.Store, scanID string, bucketARN string) (int, int, error) {
	ba := bucketARN
	out, err := client.GetVectorBucketPolicy(ctx, &s3vectors.GetVectorBucketPolicyInput{VectorBucketArn: &ba})
	if err != nil {
		if isAccessDenied(err) || isAPIErrorCode(err, "NotFoundException", "ResourceNotFoundException") {
			return 0, 0, nil
		}
		return 0, 0, fmt.Errorf("s3vectors:GetVectorBucketPolicy: %w", err)
	}
	if sv(out.Policy) == "" {
		return 0, 0, nil
	}
	arn := ba + "/policy"
	label := "policy"
	r := &store.Resource{
		Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
		Type: TypeS3VectorsVectorBucketPolicy, NativeID: arn,
		Name: &label, Region: &region,
		AttributesJSON: mustJSON(out), DiscoveredBy: scanID,
	}
	return upsertBatch(st, []*store.Resource{r}, "s3vectors vector-bucket-policies")
}

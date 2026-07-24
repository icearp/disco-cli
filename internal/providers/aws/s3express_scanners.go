package aws

import (
	"context"
	"fmt"

	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3control"
)

func init() {
	registerType(restype.Descriptor{Type: TypeS3ExpressDirectoryBucket, Service: "s3express", Leaf: true})
	registerType(restype.Descriptor{Type: TypeS3ExpressAccessPoint, Service: "s3express", Leaf: true})
	registerType(restype.Descriptor{Type: TypeS3ExpressBucketPolicy, Service: "s3express", Leaf: true})
	registerService(serviceEntry{
		name: "aws:s3express",
		fn:   scanS3Express,
	})
}

type s3ExpressS3API interface {
	ListDirectoryBuckets(context.Context, *s3.ListDirectoryBucketsInput, ...func(*s3.Options)) (*s3.ListDirectoryBucketsOutput, error)
	GetBucketPolicy(context.Context, *s3.GetBucketPolicyInput, ...func(*s3.Options)) (*s3.GetBucketPolicyOutput, error)
}

type s3ExpressControlAPI interface {
	ListAccessPointsForDirectoryBuckets(context.Context, *s3control.ListAccessPointsForDirectoryBucketsInput, ...func(*s3control.Options)) (*s3control.ListAccessPointsForDirectoryBucketsOutput, error)
}

// scanS3Express discovers S3 Express directory buckets, per-bucket policies,
// and per-bucket access points. DirectoryBucket/BucketPolicy come from the s3
// SDK; access points come from s3control's ListAccessPointsForDirectoryBuckets.
func scanS3Express(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	s3c := s3.NewFromConfig(acct.cfg, func(o *s3.Options) { o.Region = region })
	ctlc := s3control.NewFromConfig(acct.cfg, func(o *s3control.Options) { o.Region = region })

	bucketNames, t, i, ferr := scanS3EDirectoryBuckets(ctx, s3c, acct, region, st, scanID)
	if ferr != nil {
		return total, inserted, ferr
	}
	total += t
	inserted += i

	for _, name := range bucketNames {
		t, i, ferr = scanS3EBucketPolicy(ctx, s3c, acct, region, st, scanID, name)
		if ferr != nil {
			return total, inserted, ferr
		}
		total += t
		inserted += i

		t, i, ferr = scanS3EAccessPoints(ctx, ctlc, acct, region, st, scanID, name)
		if ferr != nil {
			return total, inserted, ferr
		}
		total += t
		inserted += i
	}
	return total, inserted, nil
}

func scanS3EDirectoryBuckets(ctx context.Context, client s3ExpressS3API, acct *account, region string, st *store.Store, scanID string) ([]string, int, int, error) {
	var batch []*store.Resource
	var names []string
	var continuation *string
	for {
		out, err := client.ListDirectoryBuckets(ctx, &s3.ListDirectoryBucketsInput{ContinuationToken: continuation})
		if err != nil {
			if isAccessDenied(err) {
				return nil, 0, 0, skipIfAccessDenied(st, "s3:ListDirectoryBuckets", acct.ID, region, err)
			}
			return nil, 0, 0, fmt.Errorf("s3:ListDirectoryBuckets: %w", err)
		}
		for _, b := range out.Buckets {
			arn := sv(b.BucketArn)
			name := sv(b.Name)
			if name == "" {
				continue
			}
			if arn == "" {
				arn = fmt.Sprintf("arn:aws:s3express:%s:%s:bucket/%s", region, acct.ID, name)
			}
			names = append(names, name)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeS3ExpressDirectoryBucket, NativeID: arn,
				Name: b.Name, Region: &region,
				AttributesJSON: mustJSON(b), DiscoveredBy: scanID,
			})
		}
		if out.ContinuationToken == nil || *out.ContinuationToken == "" {
			break
		}
		continuation = out.ContinuationToken
	}
	t, i, err := upsertBatch(st, batch, "s3express directory-buckets")
	return names, t, i, err
}

func scanS3EBucketPolicy(ctx context.Context, client s3ExpressS3API, acct *account, region string, st *store.Store, scanID string, bucketName string) (int, int, error) {
	bn := bucketName
	out, err := client.GetBucketPolicy(ctx, &s3.GetBucketPolicyInput{Bucket: &bn})
	if err != nil {
		if isAccessDenied(err) || isAPIErrorCode(err, "NoSuchBucketPolicy") {
			return 0, 0, nil
		}
		return 0, 0, fmt.Errorf("s3:GetBucketPolicy(directory): %w", err)
	}
	if sv(out.Policy) == "" {
		return 0, 0, nil
	}
	arn := fmt.Sprintf("arn:aws:s3express:%s:%s:bucket/%s/policy", region, acct.ID, bn)
	label := "policy"
	r := &store.Resource{
		Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
		Type: TypeS3ExpressBucketPolicy, NativeID: arn,
		Name: &label, Region: &region,
		AttributesJSON: mustJSON(out), DiscoveredBy: scanID,
	}
	return upsertBatch(st, []*store.Resource{r}, "s3express bucket-policies")
}

func scanS3EAccessPoints(ctx context.Context, client s3ExpressControlAPI, acct *account, region string, st *store.Store, scanID string, bucketName string) (int, int, error) {
	bn := bucketName
	aid := acct.ID
	var batch []*store.Resource
	var nextToken *string
	for {
		out, err := client.ListAccessPointsForDirectoryBuckets(ctx, &s3control.ListAccessPointsForDirectoryBucketsInput{
			AccountId:       &aid,
			DirectoryBucket: &bn,
			NextToken:       nextToken,
		})
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "s3control:ListAccessPointsForDirectoryBuckets", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("s3control:ListAccessPointsForDirectoryBuckets: %w", err)
		}
		for _, ap := range out.AccessPointList {
			arn := sv(ap.AccessPointArn)
			if arn == "" {
				continue
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeS3ExpressAccessPoint, NativeID: arn,
				Name: ap.Name, Region: &region,
				AttributesJSON: mustJSON(ap), DiscoveredBy: scanID,
			})
		}
		if out.NextToken == nil || *out.NextToken == "" {
			break
		}
		nextToken = out.NextToken
	}
	return upsertBatch(st, batch, "s3express access-points")
}

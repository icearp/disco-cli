package aws

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/internal/store"
	"github.com/aws/aws-sdk-go-v2/service/s3control"
	"github.com/aws/aws-sdk-go-v2/service/s3outposts"
)

func init() {
	registerService(serviceEntry{
		name: "aws:s3outposts",
		fn:   scanS3Outposts,
		emits: []coverage.TypeDecl{
			{Service: "s3outposts", DiscoType: TypeS3OutpostsEndpoint},
			{Service: "s3outposts", DiscoType: TypeS3OutpostsBucket},
			{Service: "s3outposts", DiscoType: TypeS3OutpostsAccessPoint},
			{Service: "s3outposts", DiscoType: TypeS3OutpostsBucketPolicy},
		},
	})
}

type s3outpostsAPI interface {
	ListEndpoints(context.Context, *s3outposts.ListEndpointsInput, ...func(*s3outposts.Options)) (*s3outposts.ListEndpointsOutput, error)
}

// scanS3Outposts discovers S3 Outposts endpoints, regional buckets, access
// points, and bucket policies. ListOutpostsWithS3 short-circuits empty (no
// Outposts), so the cross-SDK fan-out has zero cost in non-Outposts accounts.
func scanS3Outposts(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := s3outposts.NewFromConfig(acct.cfg, func(o *s3outposts.Options) { o.Region = region })
	t1, i1, err := scanS3OutpostsEndpoints(ctx, client, acct, region, st, scanID)
	if err != nil {
		return t1, i1, err
	}
	t2, i2, err := scanS3OutpostsBucketTree(ctx, client, acct, region, st, scanID)
	if err != nil {
		return t1 + t2, i1 + i2, err
	}
	return t1 + t2, i1 + i2, nil
}

func scanS3OutpostsEndpoints(ctx context.Context, client *s3outposts.Client, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
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
	return upsertBatch(st, batch, "s3outposts endpoints")
}

// scanS3OutpostsBucketTree enumerates Outposts via s3outposts.ListOutpostsWithS3,
// then per-Outpost regional buckets via s3control.ListRegionalBuckets, then per
// bucket access points via s3control.ListAccessPoints (Bucket=outpost-bucket-arn)
// and bucket policy via s3control.GetBucketPolicy.
func scanS3OutpostsBucketTree(ctx context.Context, oclient *s3outposts.Client, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	// Cheap pre-check: zero outposts means nothing else to do.
	var outpostIDs []string
	var nextToken *string
	for {
		out, lerr := oclient.ListOutpostsWithS3(ctx, &s3outposts.ListOutpostsWithS3Input{NextToken: nextToken})
		if lerr != nil {
			if isAccessDenied(lerr) {
				return 0, 0, skipIfAccessDenied(st, "s3outposts:ListOutpostsWithS3", acct.ID, region, lerr)
			}
			return 0, 0, fmt.Errorf("s3outposts:ListOutpostsWithS3: %w", lerr)
		}
		for _, o := range out.Outposts {
			if id := sv(o.OutpostId); id != "" {
				outpostIDs = append(outpostIDs, id)
			}
		}
		if out.NextToken == nil || *out.NextToken == "" {
			break
		}
		nextToken = out.NextToken
	}
	if len(outpostIDs) == 0 {
		return 0, 0, nil
	}

	cclient := s3control.NewFromConfig(acct.cfg, func(o *s3control.Options) { o.Region = region })

	var buckets []*store.Resource
	var aps []*store.Resource
	var pols []*store.Resource
	for _, opID := range outpostIDs {
		opIDCopy := opID
		var btoken *string
		for {
			bout, berr := cclient.ListRegionalBuckets(ctx, &s3control.ListRegionalBucketsInput{
				AccountId: &acct.ID,
				OutpostId: &opIDCopy,
				NextToken: btoken,
			})
			if berr != nil {
				if isAccessDenied(berr) {
					return 0, 0, skipIfAccessDenied(st, "s3control:ListRegionalBuckets", acct.ID, region, berr)
				}
				return 0, 0, fmt.Errorf("s3control:ListRegionalBuckets: %w", berr)
			}
			for _, b := range bout.RegionalBucketList {
				bArn := sv(b.BucketArn)
				if bArn == "" {
					continue
				}
				buckets = append(buckets, &store.Resource{
					Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
					Type: TypeS3OutpostsBucket, NativeID: bArn,
					Name: b.Bucket, Region: &region,
					AttributesJSON: mustJSON(b), DiscoveredBy: scanID,
				})
				// Access points scoped to this bucket.
				var aptoken *string
				for {
					apout, aperr := cclient.ListAccessPoints(ctx, &s3control.ListAccessPointsInput{
						AccountId: &acct.ID,
						Bucket:    &bArn,
						NextToken: aptoken,
					})
					if aperr != nil {
						if isAccessDenied(aperr) {
							_ = skipIfAccessDenied(st, "s3control:ListAccessPoints", acct.ID, region, aperr)
							break
						}
						return 0, 0, fmt.Errorf("s3control:ListAccessPoints: %w", aperr)
					}
					for _, ap := range apout.AccessPointList {
						apArn := sv(ap.AccessPointArn)
						if apArn == "" {
							continue
						}
						aps = append(aps, &store.Resource{
							Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
							Type: TypeS3OutpostsAccessPoint, NativeID: apArn,
							Name: ap.Name, Region: &region,
							AttributesJSON: mustJSON(ap), DiscoveredBy: scanID,
						})
					}
					if apout.NextToken == nil || *apout.NextToken == "" {
						break
					}
					aptoken = apout.NextToken
				}
				// Bucket policy. Synth NativeID {bucketArn}/policy.
				pout, perr := cclient.GetBucketPolicy(ctx, &s3control.GetBucketPolicyInput{
					AccountId: &acct.ID,
					Bucket:    &bArn,
				})
				if perr != nil {
					if isAccessDenied(perr) || isAPIErrorCode(perr, "NoSuchBucketPolicy") {
						continue
					}
					return 0, 0, fmt.Errorf("s3control:GetBucketPolicy: %w", perr)
				}
				polArn := bArn + "/policy"
				polLabel := polArn
				pols = append(pols, &store.Resource{
					Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
					Type: TypeS3OutpostsBucketPolicy, NativeID: polArn,
					Name: &polLabel, Region: &region,
					AttributesJSON: mustJSON(pout), DiscoveredBy: scanID,
				})
			}
			if bout.NextToken == nil || *bout.NextToken == "" {
				break
			}
			btoken = bout.NextToken
		}
	}
	tb, ib, err := upsertBatch(st, buckets, "s3outposts buckets")
	if err != nil {
		return tb, ib, err
	}
	ta, ia, err := upsertBatch(st, aps, "s3outposts access-points")
	if err != nil {
		return tb + ta, ib + ia, err
	}
	tp, ip, err := upsertBatch(st, pols, "s3outposts bucket-policies")
	return tb + ta + tp, ib + ia + ip, err
}

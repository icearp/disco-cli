package aws

import (
	"context"
	"fmt"
	"sync"

	"codeberg.org/icearp/disco/internal/store"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	"golang.org/x/sync/errgroup"
	"golang.org/x/sync/semaphore"
)

func init() {
	registerService(serviceEntry{
		name:   "aws:s3",
		global: true,
		fn: func(ctx context.Context, acct *account, _ string, st *store.Store, scanID string) (total, inserted int, err error) {
			return scanS3(ctx, acct, st, scanID)
		},
	})
}

// scanS3 discovers S3 buckets. S3 is a global service; buckets are returned
// without region info from ListBuckets, but each bucket has a region that can
// be fetched separately. We store the bucket-level region in attributes.
func scanS3(ctx context.Context, acct *account, st *store.Store, scanID string) (total, inserted int, err error) {
	// S3 ListBuckets uses a regional endpoint but returns all buckets globally.
	client := s3.NewFromConfig(acct.cfg, func(o *s3.Options) { o.Region = "us-east-1" })

	out, err := client.ListBuckets(ctx, &s3.ListBucketsInput{})
	if err != nil {
		if isAccessDenied(err) {
			return 0, 0, skipIfAccessDenied(st, "s3:ListBuckets", acct.ID, "global", err)
		}
		return 0, 0, fmt.Errorf("s3:ListBuckets: %w", err)
	}

	var batch []*store.Resource
	for _, b := range out.Buckets {
		name := sv(b.Name)
		arn := fmt.Sprintf("arn:aws:s3:::%s", name)
		r := &store.Resource{
			Provider:       "aws",
			AccountID:      acct.ID,
			AccountName:    &acct.Name,
			Type:           TypeS3Bucket,
			NativeID:       arn,
			Name:           &name,
			CreatedAt:      tp(b.CreationDate),
			AttributesJSON: mustJSON(b),
			DiscoveredBy:   scanID,
		}
		batch = append(batch, r)
	}
	if len(batch) > 0 {
		n, err := st.UpsertResources(batch)
		if err != nil {
			return 0, 0, fmt.Errorf("upsert S3 buckets: %w", err)
		}
		total += len(batch)
		inserted += n
	}
	t, n, err := scanS3BucketPolicies(ctx, acct, client, out.Buckets, st, scanID)
	total += t
	inserted += n
	if err != nil {
		return total, inserted, err
	}
	// Populate per-bucket SSE config on the account for use by
	// resolveS3BucketEncryptionRelationships. No resources are upserted — the
	// config only exists to drive the bucket→KMS edge.
	if err := scanS3BucketEncryptions(ctx, acct, client, out.Buckets); err != nil {
		return total, inserted, err
	}
	return total, inserted, nil
}

// scanS3BucketEncryptions fetches GetBucketEncryption for each bucket
// concurrently and stores the result in acct.s3BucketEncryption, keyed by
// bucket name. Buckets without an explicit encryption config return
// ServerSideEncryptionConfigurationNotFoundError and are silently skipped.
// AccessDenied is also tolerated (best-effort).
func scanS3BucketEncryptions(ctx context.Context, acct *account, client *s3.Client, buckets []s3types.Bucket) error {
	const maxConcurrent = 20
	sem := semaphore.NewWeighted(maxConcurrent)
	acct.s3BucketEncryption = make(map[string]s3BucketEncryptionEntry)
	g, gctx := errgroup.WithContext(ctx)
	for _, b := range buckets {
		g.Go(func() error {
			if err := sem.Acquire(gctx, 1); err != nil {
				return err
			}
			defer sem.Release(1)
			name := sv(b.Name)

			region, err := s3BucketRegion(gctx, client, name)
			if err != nil {
				if isAccessDenied(err) {
					return nil
				}
				return fmt.Errorf("s3:GetBucketLocation %s: %w", name, err)
			}

			rc := s3.NewFromConfig(acct.cfg, func(o *s3.Options) { o.Region = region })
			out, err := rc.GetBucketEncryption(gctx, &s3.GetBucketEncryptionInput{Bucket: &name})
			if err != nil {
				// No explicit config = default SSE-S3; very common — skip.
				if isAPIErrorCode(err, "ServerSideEncryptionConfigurationNotFoundError") || isAccessDenied(err) {
					return nil
				}
				return fmt.Errorf("s3:GetBucketEncryption %s: %w", name, err)
			}
			if out.ServerSideEncryptionConfiguration == nil {
				return nil
			}
			acct.s3BucketEncryptionMu.Lock()
			acct.s3BucketEncryption[name] = s3BucketEncryptionEntry{
				Region: region,
				Config: out.ServerSideEncryptionConfiguration,
			}
			acct.s3BucketEncryptionMu.Unlock()
			return nil
		})
	}
	return g.Wait()
}

// scanS3BucketPolicies fetches the bucket policy for each bucket concurrently.
// Buckets with no policy (NoSuchBucketPolicy) are silently skipped.
// Each GetBucketPolicy call uses a client pinned to the bucket's home region —
// using the wrong region endpoint causes a 301 PermanentRedirect error.
func scanS3BucketPolicies(ctx context.Context, acct *account, client *s3.Client, buckets []s3types.Bucket, st *store.Store, scanID string) (total, inserted int, err error) {
	const maxConcurrent = 20
	sem := semaphore.NewWeighted(maxConcurrent)
	var (
		mu    sync.Mutex
		batch []*store.Resource
	)
	g, gctx := errgroup.WithContext(ctx)
	for _, b := range buckets {
		g.Go(func() error {
			if err := sem.Acquire(gctx, 1); err != nil {
				return err
			}
			defer sem.Release(1)
			name := sv(b.Name)

			// Resolve the bucket's home region before fetching the policy.
			// S3 returns 301 PermanentRedirect when the client region doesn't
			// match the bucket's region. GetBucketLocation works from any endpoint.
			region, err := s3BucketRegion(gctx, client, name)
			if err != nil {
				if isAccessDenied(err) {
					return nil // best-effort
				}
				return fmt.Errorf("s3:GetBucketLocation %s: %w", name, err)
			}

			// Use a region-specific client for the policy fetch.
			rc := s3.NewFromConfig(acct.cfg, func(o *s3.Options) { o.Region = region })
			out, err := rc.GetBucketPolicy(gctx, &s3.GetBucketPolicyInput{Bucket: &name})
			if err != nil {
				// No policy on this bucket — expected and common.
				if isAPIErrorCode(err, "NoSuchBucketPolicy") || isAccessDenied(err) {
					return nil
				}
				return fmt.Errorf("s3:GetBucketPolicy %s: %w", name, err)
			}
			arn := fmt.Sprintf("arn:aws:s3:::%s/policy", name)
			r := &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeS3BucketPolicy,
				NativeID:       arn,
				Name:           sp(name + "/policy"),
				AttributesJSON: mustJSON(out),
				DiscoveredBy:   scanID,
			}
			mu.Lock()
			batch = append(batch, r)
			mu.Unlock()
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return 0, 0, err
	}
	if len(batch) > 0 {
		n, err := st.UpsertResources(batch)
		if err != nil {
			return 0, 0, fmt.Errorf("upsert S3 bucket policies: %w", err)
		}
		total += len(batch)
		inserted += n
	}
	return total, inserted, nil
}

// s3BucketRegion returns the home region of an S3 bucket.
// GetBucketLocation returns an empty LocationConstraint for us-east-1 buckets.
func s3BucketRegion(ctx context.Context, client *s3.Client, bucket string) (string, error) {
	out, err := client.GetBucketLocation(ctx, &s3.GetBucketLocationInput{Bucket: &bucket})
	if err != nil {
		return "", err
	}
	if out.LocationConstraint == "" {
		return "us-east-1", nil
	}
	return string(out.LocationConstraint), nil
}

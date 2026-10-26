package aws

import (
	"context"
	"errors"
	"fmt"
	"sync"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	smithyhttp "github.com/aws/smithy-go/transport/http"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
	"golang.org/x/sync/errgroup"
	"golang.org/x/sync/semaphore"
)

func init() {
	registerType(restype.Descriptor{Type: TypeS3Bucket, Service: "s3"})
	registerType(restype.Descriptor{Type: TypeS3BucketPolicy, Service: "s3"})
	registerType(restype.Descriptor{Type: TypeS3BucketAnalyticsConfiguration, Service: "s3"})
	registerType(restype.Descriptor{Type: TypeS3BucketIntelligentTieringConfiguration, Service: "s3"})
	registerType(restype.Descriptor{Type: TypeS3BucketInventoryConfiguration, Service: "s3"})
	registerType(restype.Descriptor{Type: TypeS3BucketMetricsConfiguration, Service: "s3"})
	registerService(serviceEntry{
		name:   "aws:s3",
		global: true,
		fn: func(ctx context.Context, acct *account, _ string, st *store.Store, scanID string) (total, inserted int, err error) {
			return scanS3(ctx, acct, st, scanID)
		},
	})
}

// s3API is the narrow set of S3 operations called by the global ListBuckets
// phase + per-bucket sub-phases. s3 sub-phases construct PER-REGION clients
// (each bucket's home region), so the iface lift is intentionally SHALLOW:
// only ListBuckets / GetBucketLocation / HeadBucket (top-level client) are bound here;
// per-bucket clients (GetBucketEncryption / GetBucketPolicy) stay constructed
// inline. Lifting those would need a region→client factory abstraction; defer.
type s3API interface {
	ListBuckets(context.Context, *s3.ListBucketsInput, ...func(*s3.Options)) (*s3.ListBucketsOutput, error)
	GetBucketLocation(context.Context, *s3.GetBucketLocationInput, ...func(*s3.Options)) (*s3.GetBucketLocationOutput, error)
	HeadBucket(context.Context, *s3.HeadBucketInput, ...func(*s3.Options)) (*s3.HeadBucketOutput, error)
}

// scanS3 discovers S3 buckets. S3 is a global service; ListBuckets returns no
// region info, but each bucket's region can be fetched separately and is
// stored in attributes.
func scanS3(ctx context.Context, acct *account, st *store.Store, scanID string) (total, inserted int, err error) {
	// S3 ListBuckets uses a regional endpoint but returns all the partition's
	// buckets from any of them, so it is called once, in S3's once-region.
	region, err := acct.onceRegion("s3")
	if err != nil {
		return 0, 0, err
	}
	client := s3.NewFromConfig(acct.cfg, func(o *s3.Options) { o.Region = region })

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
		arn := s3BucketARN(name)
		r := &store.Resource{
			Provider:       "aws",
			AccountID:      acct.ID,
			AccountName:    &acct.Name,
			Region:         regionGlobal,
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
	// Populate per-bucket SSE config on the account for
	// resolveS3BucketEncryptionRelationships. No resources upserted — the
	// config only exists to drive the bucket→KMS edge.
	if err := scanS3BucketEncryptions(ctx, acct, client, out.Buckets); err != nil {
		return total, inserted, err
	}
	t, n, err = scanS3BucketConfigs(ctx, acct, client, out.Buckets, st, scanID)
	return total + t, inserted + n, err
}

// s3BucketGone reports a bucket deleted between ListBuckets and a per-bucket call.
func s3BucketGone(err error) bool { return isAPIErrorCode(err, "NoSuchBucket") }

// s3BucketRegionGone is s3BucketGone for an s3BucketRegion error, which may
// come from HeadBucket: HeadBucket's response has no body, so it reports a
// missing bucket as its modeled NotFound rather than NoSuchBucket.
func s3BucketRegionGone(err error) bool { return s3BucketGone(err) || isAPIErrorCode(err, "NotFound") }

// s3HomedBucket is a bucket listed this scan with the S3 client for its home
// region; S3 answers a request sent to any other region with 301 PermanentRedirect.
type s3HomedBucket struct {
	name   string
	client *s3.Client
}

// s3MetricsConfigAttrs is a metrics configuration with its Filter's union
// member named in filterMember. The SDK marshals the member as a bare
// {"Value":...}, so a Prefix and an AccessPointArn filter would otherwise both
// store as {"Value":"<string>"}.
type s3MetricsConfigAttrs struct {
	s3types.MetricsConfiguration
	FilterMember string `json:"filterMember,omitempty"`
}

func newS3MetricsConfigAttrs(c s3types.MetricsConfiguration) s3MetricsConfigAttrs {
	out := s3MetricsConfigAttrs{MetricsConfiguration: c}
	switch f := c.Filter.(type) {
	case *s3types.MetricsFilterMemberPrefix:
		out.FilterMember = "Prefix"
	case *s3types.MetricsFilterMemberAccessPointArn:
		out.FilterMember = "AccessPointArn"
	case *s3types.MetricsFilterMemberTag:
		out.FilterMember = "Tag"
	case *s3types.MetricsFilterMemberAnd:
		out.FilterMember = "And"
	case *s3types.UnknownUnionMember:
		out.FilterMember = f.Tag
	}
	return out
}

// scanS3BucketConfigs stores each bucket's analytics, intelligent-tiering,
// inventory and metrics configurations under the bucket. A failing kind does
// not stop the others; their errors are joined.
func scanS3BucketConfigs(ctx context.Context, acct *account, client s3API, buckets []s3types.Bucket, st *store.Store, scanID string) (total, inserted int, err error) {
	homed, err := s3HomedBuckets(ctx, acct, client, buckets, st)
	if err != nil {
		return 0, 0, err
	}
	var errs []error
	for _, scan := range []func(context.Context, *account, map[string]s3HomedBucket, *store.Store, string) (int, int, error){
		scanS3AnalyticsConfigs, scanS3IntelligentTieringConfigs, scanS3InventoryConfigs, scanS3MetricsConfigs,
	} {
		t, n, err := scan(ctx, acct, homed, st, scanID)
		total += t
		inserted += n
		errs = append(errs, err)
	}
	return total, inserted, errors.Join(errs...)
}

// s3HomedBuckets looks up each bucket's home region for the configuration
// phase, keyed by bucket name, sharing one client per region; the policy and
// encryption phases do their own lookups. A bucket deleted since ListBuckets, or
// whose location is denied (warned once), is left out, as is one whose region
// is unknown (warned per bucket, see s3BucketRegion).
func s3HomedBuckets(ctx context.Context, acct *account, client s3API, buckets []s3types.Bucket, st *store.Store) (map[string]s3HomedBucket, error) {
	var (
		mu      sync.Mutex
		regions = map[string]string{}
	)
	errs := &fanOutErrs{st: st, acct: acct, region: sv(regionGlobal), op: "s3:GetBucketLocation", isSkip: s3BucketRegionGone}
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(fanoutMed)
	for _, b := range buckets {
		name := sv(b.Name)
		g.Go(func() error {
			region, err := s3BucketRegion(gctx, acct, client, name)
			var unknown *s3BucketRegionUnknownError
			if errors.As(err, &unknown) {
				st.ReportWarning(store.ScanWarning{Provider: "aws", Service: "s3:HeadBucket", Scope: acct.ID, Message: unknown.Error()})
				return nil
			}
			if err != nil {
				return errs.keyErr(name, err)
			}
			mu.Lock()
			regions[name] = region
			mu.Unlock()
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return nil, err
	}
	clients := map[string]*s3.Client{}
	homed := make(map[string]s3HomedBucket, len(regions))
	for name, region := range regions {
		if clients[region] == nil {
			clients[region] = s3.NewFromConfig(acct.cfg, func(o *s3.Options) { o.Region = region })
		}
		homed[name] = s3HomedBucket{name: name, client: clients[region]}
	}
	return homed, nil
}

// s3BucketConfigFanOut runs list for every homed bucket and stores the rows
// under the bucket.
func s3BucketConfigFanOut(
	ctx context.Context, acct *account, homed map[string]s3HomedBucket, st *store.Store, op string,
	list func(context.Context, s3HomedBucket) ([]*store.Resource, error),
) (int, int, error) {
	parents := make([]childParent, 0, len(homed))
	for name := range homed {
		parents = append(parents, childParent{id: name, arn: s3BucketARN(name)})
	}
	return childFanOut(ctx, st, acct, sv(regionGlobal), op, parents, s3BucketGone,
		func(ctx context.Context, p childParent) ([]*store.Resource, error) {
			return list(ctx, homed[p.id])
		})
}

func s3BucketARN(name string) string { return "arn:aws:s3:::" + name }

// s3BucketConfigRow builds the row for one configuration of bucket. The
// configurations have no ARN, so the NativeID is {bucketARN}/<kind>/{id};
// an empty id drops the row.
func s3BucketConfigRow(acct *account, scanID, typ, kind, bucket string, id *string, status string, cfg any) *store.Resource {
	if sv(id) == "" {
		return nil
	}
	return &store.Resource{
		Provider:       "aws",
		AccountID:      acct.ID,
		AccountName:    &acct.Name,
		Region:         regionGlobal,
		Type:           typ,
		NativeID:       s3BucketARN(bucket) + "/" + kind + "/" + *id,
		Name:           id,
		Status:         nonEmptyPtr(status),
		AttributesJSON: mustJSON(cfg),
		DiscoveredBy:   scanID,
	}
}

// s3LastPage is lastPage for the ListBucket*Configurations ops, which have no
// SDK paginator: a page that is not truncated is also the last, and a
// truncated page without a token ends the listing with
// errTruncatedWithoutToken.
func s3LastPage(truncated *bool, next, sent *string) (bool, error) {
	if !sdkaws.ToBool(truncated) {
		return true, nil
	}
	if sv(next) == "" {
		return true, errTruncatedWithoutToken
	}
	return lastPage(sent, next)
}

func scanS3AnalyticsConfigs(ctx context.Context, acct *account, homed map[string]s3HomedBucket, st *store.Store, scanID string) (int, int, error) {
	return s3BucketConfigFanOut(ctx, acct, homed, st, "s3:ListBucketAnalyticsConfigurations",
		func(ctx context.Context, b s3HomedBucket) ([]*store.Resource, error) {
			in := &s3.ListBucketAnalyticsConfigurationsInput{Bucket: &b.name}
			var rows []*store.Resource
			for {
				out, err := b.client.ListBucketAnalyticsConfigurations(ctx, in)
				if err != nil {
					return nil, err
				}
				for _, c := range out.AnalyticsConfigurationList {
					if r := s3BucketConfigRow(acct, scanID, TypeS3BucketAnalyticsConfiguration, "analytics-configuration", b.name, c.Id, "", c); r != nil {
						rows = append(rows, r)
					}
				}
				if done, err := s3LastPage(out.IsTruncated, out.NextContinuationToken, in.ContinuationToken); done {
					return rows, err
				}
				in.ContinuationToken = out.NextContinuationToken
			}
		})
}

func scanS3IntelligentTieringConfigs(ctx context.Context, acct *account, homed map[string]s3HomedBucket, st *store.Store, scanID string) (int, int, error) {
	return s3BucketConfigFanOut(ctx, acct, homed, st, "s3:ListBucketIntelligentTieringConfigurations",
		func(ctx context.Context, b s3HomedBucket) ([]*store.Resource, error) {
			in := &s3.ListBucketIntelligentTieringConfigurationsInput{Bucket: &b.name}
			var rows []*store.Resource
			for {
				out, err := b.client.ListBucketIntelligentTieringConfigurations(ctx, in)
				if err != nil {
					return nil, err
				}
				for _, c := range out.IntelligentTieringConfigurationList {
					if r := s3BucketConfigRow(acct, scanID, TypeS3BucketIntelligentTieringConfiguration, "intelligent-tiering-configuration", b.name, c.Id, string(c.Status), c); r != nil {
						rows = append(rows, r)
					}
				}
				if done, err := s3LastPage(out.IsTruncated, out.NextContinuationToken, in.ContinuationToken); done {
					return rows, err
				}
				in.ContinuationToken = out.NextContinuationToken
			}
		})
}

func scanS3InventoryConfigs(ctx context.Context, acct *account, homed map[string]s3HomedBucket, st *store.Store, scanID string) (int, int, error) {
	return s3BucketConfigFanOut(ctx, acct, homed, st, "s3:ListBucketInventoryConfigurations",
		func(ctx context.Context, b s3HomedBucket) ([]*store.Resource, error) {
			in := &s3.ListBucketInventoryConfigurationsInput{Bucket: &b.name}
			var rows []*store.Resource
			for {
				out, err := b.client.ListBucketInventoryConfigurations(ctx, in)
				if err != nil {
					return nil, err
				}
				for _, c := range out.InventoryConfigurationList {
					if r := s3BucketConfigRow(acct, scanID, TypeS3BucketInventoryConfiguration, "inventory-configuration", b.name, c.Id, "", c); r != nil {
						rows = append(rows, r)
					}
				}
				if done, err := s3LastPage(out.IsTruncated, out.NextContinuationToken, in.ContinuationToken); done {
					return rows, err
				}
				in.ContinuationToken = out.NextContinuationToken
			}
		})
}

func scanS3MetricsConfigs(ctx context.Context, acct *account, homed map[string]s3HomedBucket, st *store.Store, scanID string) (int, int, error) {
	return s3BucketConfigFanOut(ctx, acct, homed, st, "s3:ListBucketMetricsConfigurations",
		func(ctx context.Context, b s3HomedBucket) ([]*store.Resource, error) {
			in := &s3.ListBucketMetricsConfigurationsInput{Bucket: &b.name}
			var rows []*store.Resource
			for {
				out, err := b.client.ListBucketMetricsConfigurations(ctx, in)
				if err != nil {
					return nil, err
				}
				for _, c := range out.MetricsConfigurationList {
					if r := s3BucketConfigRow(acct, scanID, TypeS3BucketMetricsConfiguration, "metrics-configuration", b.name, c.Id, "", newS3MetricsConfigAttrs(c)); r != nil {
						rows = append(rows, r)
					}
				}
				if done, err := s3LastPage(out.IsTruncated, out.NextContinuationToken, in.ContinuationToken); done {
					return rows, err
				}
				in.ContinuationToken = out.NextContinuationToken
			}
		})
}

// scanS3BucketEncryptions fetches GetBucketEncryption for each bucket
// concurrently and stores the result in acct.s3BucketEncryption, keyed by
// bucket name. Buckets without explicit encryption config return
// ServerSideEncryptionConfigurationNotFoundError and are silently skipped;
// AccessDenied is also tolerated (best-effort).
func scanS3BucketEncryptions(ctx context.Context, acct *account, client s3API, buckets []s3types.Bucket) error {
	sem := semaphore.NewWeighted(fanoutHigh)
	acct.s3BucketEncryption = make(map[string]s3BucketEncryptionEntry)
	g, gctx := errgroup.WithContext(ctx)
	for _, b := range buckets {
		g.Go(func() error {
			if err := sem.Acquire(gctx, 1); err != nil {
				return err
			}
			defer sem.Release(1)
			name := sv(b.Name)

			region, err := s3BucketRegion(gctx, acct, client, name)
			var unknown *s3BucketRegionUnknownError
			if errors.As(err, &unknown) {
				return nil // s3HomedBuckets warns, naming the bucket
			}
			if err != nil {
				if isAccessDenied(err) || s3BucketRegionGone(err) {
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
// the wrong region endpoint causes a 301 PermanentRedirect error.
func scanS3BucketPolicies(ctx context.Context, acct *account, client s3API, buckets []s3types.Bucket, st *store.Store, scanID string) (total, inserted int, err error) {
	sem := semaphore.NewWeighted(fanoutHigh)
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

			// Resolve the bucket's home region before fetching the policy:
			// S3 returns 301 PermanentRedirect on region mismatch.
			// GetBucketLocation works from any endpoint.
			region, err := s3BucketRegion(gctx, acct, client, name)
			var unknown *s3BucketRegionUnknownError
			if errors.As(err, &unknown) {
				return nil // s3HomedBuckets warns, naming the bucket
			}
			if err != nil {
				if isAccessDenied(err) || s3BucketRegionGone(err) {
					return nil // best-effort; a vanished bucket has no policy to read
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
			arn := s3BucketARN(name) + "/policy"
			r := &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Region:         regionGlobal,
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

// s3BucketRegion returns the region an S3 bucket lives in, from
// GetBucketLocation. The legacy constraint "EU" is eu-west-1, and an empty
// constraint is us-east-1 for an aws account (the S3 API reference for
// GetBucketLocation). Outside aws what an empty constraint means is not
// documented, so the region is read from HeadBucket's x-amz-bucket-region
// instead; when S3 answers without naming one, a *s3BucketRegionUnknownError is
// returned rather than a guess (see s3HeadBucketRegion).
func s3BucketRegion(ctx context.Context, acct *account, client s3API, bucket string) (string, error) {
	out, err := client.GetBucketLocation(ctx, &s3.GetBucketLocationInput{Bucket: &bucket})
	if err != nil {
		return "", err
	}
	switch {
	case out.LocationConstraint == s3types.BucketLocationConstraintEu:
		return "eu-west-1", nil
	case out.LocationConstraint != "":
		return string(out.LocationConstraint), nil
	case acct.partition() == "aws":
		return "us-east-1", nil
	}
	return s3HeadBucketRegion(ctx, client, bucket)
}

// s3HeadBucketRegion reads the bucket's region from HeadBucket. S3 sends
// x-amz-bucket-region on a success and on the 301 it answers a request sent to
// another region with, so a redirect still names the region. Only an answer
// from S3 that names no region is a *s3BucketRegionUnknownError; a cancelled
// context, a transient failure, a vanished bucket or a request that got no
// HTTP answer returns the raw error, for the caller to classify as it does
// every other per-bucket error.
func s3HeadBucketRegion(ctx context.Context, client s3API, bucket string) (string, error) {
	out, err := client.HeadBucket(ctx, &s3.HeadBucketInput{Bucket: &bucket})
	if err == nil {
		if r := sv(out.BucketRegion); r != "" {
			return r, nil
		}
		return "", &s3BucketRegionUnknownError{Bucket: bucket, Cause: errors.New("s3:HeadBucket returned no BucketRegion")}
	}
	var re *smithyhttp.ResponseError
	answered := errors.As(err, &re) && re.Response != nil
	if answered {
		if r := re.Response.Header.Get("x-amz-bucket-region"); r != "" {
			return r, nil
		}
	}
	if !answered || ctx.Err() != nil || isTransientNetworkError(err) || s3BucketRegionGone(err) {
		return "", err
	}
	return "", &s3BucketRegionUnknownError{Bucket: bucket, Cause: err}
}

// s3BucketRegionUnknownError reports a bucket outside the aws partition whose
// GetBucketLocation constraint is empty and whose HeadBucket answer names no
// region.
// Its row is still stored; the per-bucket reads that need the region skip it.
type s3BucketRegionUnknownError struct {
	Bucket string
	Cause  error
}

func (e *s3BucketRegionUnknownError) Error() string {
	return fmt.Sprintf("bucket %s: GetBucketLocation returned an empty LocationConstraint and HeadBucket named no region (%v); its policy, encryption and configuration reads are skipped", e.Bucket, e.Cause)
}

func (e *s3BucketRegionUnknownError) Unwrap() error { return e.Cause }

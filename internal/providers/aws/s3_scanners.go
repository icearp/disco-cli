package aws

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"sync"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"
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

// s3API is the narrow set of S3 operations the scan sends from its
// top-level client: ListBuckets, GetBucketLocation and HeadBucket. Every
// per-bucket call (GetBucketTagging, GetBucketPolicy, GetBucketEncryption and
// the configuration listings) goes to a client built from acct.cfg for the
// bucket's home region, one per region: s3ListedHomes builds those for the
// tag reads, s3HomedBuckets for the others. Tests stub those calls through
// acct.cfg, not through this interface.
type s3API interface {
	ListBuckets(context.Context, *s3.ListBucketsInput, ...func(*s3.Options)) (*s3.ListBucketsOutput, error)
	GetBucketLocation(context.Context, *s3.GetBucketLocationInput, ...func(*s3.Options)) (*s3.GetBucketLocationOutput, error)
	HeadBucket(context.Context, *s3.HeadBucketInput, ...func(*s3.Options)) (*s3.HeadBucketOutput, error)
}

const (
	// s3ListBucketsCap is the model's MaxBuckets cap (1 to 10000). Sending it
	// also makes S3 return each bucket's BucketRegion, which it leaves out of
	// a ListBuckets request without parameters.
	s3ListBucketsCap = 10000

	s3TagsOp = "s3:GetBucketTagging"
)

// scanS3 discovers S3 buckets with their tags, then each bucket's home
// region, policy, default encryption and configurations, one phase at a time: a phase's error
// does not stop the later ones; a failed write and ctx's end do
// (runHostPhases). The per-bucket phases read only the buckets stored this
// scan. S3 is a global service: ListBuckets uses a regional endpoint but
// returns all the partition's buckets from any of them, so it is called once,
// in S3's once-region; each per-bucket call goes to the bucket's home region.
func scanS3(ctx context.Context, acct *account, st *store.Store, scanID string) (int, int, error) {
	region, err := acct.onceRegion("s3")
	if err != nil {
		return 0, 0, err
	}
	client := s3.NewFromConfig(acct.cfg, func(o *s3.Options) { o.Region = region })
	var (
		buckets []string
		homed   map[string]s3HomedBucket
	)
	return runHostPhases(ctx, st, acct, sv(regionGlobal), "s3", 0, []hostPhase{
		{op: "s3:ListBuckets", run: func(p *phaseTotals) bool {
			var failed bool
			buckets, failed = scanS3Buckets(ctx, client, acct, st, scanID, p)
			return failed
		}},
		// One region lookup per bucket serves every later phase, so each
		// lookup error is reported once, under the op that failed.
		{op: "s3:GetBucketLocation", run: func(p *phaseTotals) bool {
			var err error
			homed, err = s3HomedBuckets(ctx, acct, client, buckets, st)
			return addOpErrsPhase(p, "s3:GetBucketLocation", 0, 0, err)
		}},
		countedPhase(0, "s3:GetBucketPolicy", func() (int, int, error) {
			return scanS3BucketPolicies(ctx, acct, homed, st, scanID)
		}),
		// Populate per-bucket SSE config on the account for
		// resolveS3BucketEncryptionRelationships. No resources upserted — the
		// config only exists to drive the bucket→KMS edge.
		countedPhase(0, "s3:GetBucketEncryption", func() (int, int, error) {
			return 0, 0, scanS3BucketEncryptions(ctx, acct, homed)
		}),
		countedPhase(0, "s3:ListBucketAnalyticsConfigurations", func() (int, int, error) {
			return scanS3AnalyticsConfigs(ctx, acct, homed, st, scanID)
		}),
		countedPhase(0, "s3:ListBucketIntelligentTieringConfigurations", func() (int, int, error) {
			return scanS3IntelligentTieringConfigs(ctx, acct, homed, st, scanID)
		}),
		countedPhase(0, "s3:ListBucketInventoryConfigurations", func() (int, int, error) {
			return scanS3InventoryConfigs(ctx, acct, homed, st, scanID)
		}),
		countedPhase(0, "s3:ListBucketMetricsConfigurations", func() (int, int, error) {
			return scanS3MetricsConfigs(ctx, acct, homed, st, scanID)
		}),
	})
}

// scanS3Buckets stores the partition's buckets with their tags, a page at a
// time (s3BucketTags), and returns the names of those stored. A bucket listed
// without a name is warned about rather than stored: its ARN would be the
// bare arn:<partition>:s3::: of every such bucket.
func scanS3Buckets(ctx context.Context, client s3API, acct *account, st *store.Store, scanID string, p *phaseTotals) (names []string, storeWriteFailed bool) {
	homes := s3ListedHomes{cfg: acct.cfg}
	stored, failed := storeParentPagesTags(ctx, st, acct, sv(regionGlobal), "s3:ListBuckets", "buckets", "Name",
		func(ctx context.Context, token *string) ([]s3types.Bucket, *string, error) {
			out, err := client.ListBuckets(ctx, &s3.ListBucketsInput{ContinuationToken: token, MaxBuckets: sdkaws.Int32(s3ListBucketsCap)})
			if err != nil {
				return nil, nil, err
			}
			return out.Buckets, out.ContinuationToken, nil
		},
		func(b s3types.Bucket) (*store.Resource, string, string) {
			name := sv(b.Name)
			if name == "" {
				return nil, "buckets have an empty Name", cmp.Or(sv(b.BucketArn), "(a bucket with no name)")
			}
			arn := s3BucketARN(acct.partition(), name)
			homes.add(arn, name, sv(b.BucketRegion))
			return &store.Resource{
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
			}, "", ""
		},
		func(b s3types.Bucket) string { return sv(b.Name) },
		s3BucketTags(&homes), p)
	return childParentIDs(stored), failed
}

// s3ListedHomes is, for each bucket row built from the listing with a
// BucketRegion, the bucket's name and an S3 client for that region, one
// client per region. S3 answers a request sent to any other region with 301
// PermanentRedirect.
type s3ListedHomes struct {
	cfg     sdkaws.Config
	mu      sync.Mutex
	clients map[string]*s3.Client    // by region
	buckets map[string]s3HomedBucket // by bucket ARN
}

func (h *s3ListedHomes) add(arn, name, region string) {
	if region == "" {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.buckets == nil {
		h.clients, h.buckets = map[string]*s3.Client{}, map[string]s3HomedBucket{}
	}
	c, ok := h.clients[region]
	if !ok {
		c = s3.NewFromConfig(h.cfg, func(o *s3.Options) { o.Region = region })
		h.clients[region] = c
	}
	h.buckets[arn] = s3HomedBucket{name: name, region: region, client: c}
}

func (h *s3ListedHomes) get(arn string) (s3HomedBucket, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	b, ok := h.buckets[arn]
	return b, ok
}

// errS3NoListedRegion is the tag read of a bucket ListBuckets listed without
// a BucketRegion. S3 documents one for every bucket of a request that sets a
// parameter, as this scan sets MaxBuckets, so its absence is an error rather
// than a reason to look the region up another way.
var errS3NoListedRegion = errors.New("ListBuckets returned no BucketRegion for the bucket, so its tags were not read; its policy, encryption and configurations are still read from the region GetBucketLocation names")

// s3BucketTags reads a bucket row's tags with GetBucketTagging, sent the
// bucket's name from its home region (homes). S3 answers NoSuchTagSet for a
// bucket without tags, documented on the op as its special error; that is no
// tags, not a failure. The op's model lists no error code, so a bucket
// deleted between ListBuckets and this read (NoSuchBucket) is kept and its
// error returned. ListBuckets returns only the caller's own buckets.
func s3BucketTags(homes *s3ListedHomes) rowTagReader {
	return rowTagReader{
		op: s3TagsOp,
		read: func(ctx context.Context, r *store.Resource) (*string, error) {
			b, ok := homes.get(r.NativeID)
			if !ok {
				return nil, errS3NoListedRegion
			}
			out, err := b.client.GetBucketTagging(ctx, &s3.GetBucketTaggingInput{Bucket: &b.name})
			if isAPIErrorCode(err, "NoSuchTagSet") {
				return nil, nil
			}
			if err != nil {
				return nil, err
			}
			return kvTagsJSON(out.TagSet, func(t s3types.Tag) (*string, *string) { return t.Key, t.Value }), nil
		},
	}
}

// s3BucketGone reports a bucket deleted between ListBuckets and a per-bucket call.
func s3BucketGone(err error) bool { return isAPIErrorCode(err, "NoSuchBucket") }

// s3BucketRegionGone is s3BucketGone for an s3BucketRegion error, which may
// come from HeadBucket: HeadBucket's response has no body, so it reports a
// missing bucket as its modeled NotFound rather than NoSuchBucket.
func s3BucketRegionGone(err error) bool { return s3BucketGone(err) || isAPIErrorCode(err, "NotFound") }

// s3HomedBucket is a bucket listed this scan with its home region and the S3
// client for it; S3 answers a request sent to any other region with 301
// PermanentRedirect.
type s3HomedBucket struct {
	name, region string
	client       *s3.Client
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

// s3HomedBuckets looks up each bucket's home region for the per-bucket
// phases, keyed by bucket name, sharing one client per region. A bucket
// deleted since ListBuckets, or whose location is denied (warned once), is
// left out, as is one whose region is unknown (warned per bucket, see
// s3BucketRegion). Any other failure leaves its bucket out and is returned
// beside the buckets found, so one bucket's failure costs no other bucket
// its reads; the errors are opErrs, each under the op that failed
// (GetBucketLocation, or the HeadBucket s3BucketRegion falls back to).
func s3HomedBuckets(ctx context.Context, acct *account, client s3API, buckets []string, st *store.Store) (map[string]s3HomedBucket, error) {
	type lookup struct {
		region string
		err    error
	}
	reads, stopped := readEachKey(ctx, buckets, func(ctx context.Context, name string) lookup {
		region, err := s3BucketRegion(ctx, acct, client, name)
		return lookup{region, err}
	})
	ops := []string{"s3:GetBucketLocation", "s3:HeadBucket"}
	keyErrs := map[string]*fanOutErrs{}
	failed := map[string][]error{}
	for _, op := range ops {
		keyErrs[op] = &fanOutErrs{st: st, acct: acct, region: sv(regionGlobal), op: op, isSkip: s3BucketRegionGone}
	}
	clients := map[string]*s3.Client{}
	homed := make(map[string]s3HomedBucket, len(reads))
	for i, l := range reads {
		name := buckets[i]
		var unknown *s3BucketRegionUnknownError
		switch {
		case errors.As(l.err, &unknown):
			st.ReportWarning(store.ScanWarning{Provider: "aws", Service: "s3:HeadBucket", Scope: acct.ID, Message: unknown.Error()})
		case l.err != nil:
			op := s3RegionLookupOp(l.err)
			if err := keyErrs[op].keyErr(name, l.err); err != nil {
				failed[op] = append(failed[op], err)
			}
		default:
			if clients[l.region] == nil {
				clients[l.region] = s3.NewFromConfig(acct.cfg, func(o *s3.Options) { o.Region = l.region })
			}
			homed[name] = s3HomedBucket{name: name, region: l.region, client: clients[l.region]}
		}
	}
	if stopped != nil {
		failed[ops[0]] = append(failed[ops[0]], fmt.Errorf("%s: %w", ops[0], stopped))
	}
	// One member per op, so a demoted op is one warning, not one per bucket.
	var errs phaseErrs
	for _, op := range ops {
		errs.add(op, errors.Join(failed[op]...))
	}
	if len(errs) == 0 {
		return homed, nil
	}
	return homed, opErrs{errs}
}

// s3RegionLookupOp is the op an s3BucketRegion error came from: HeadBucket,
// which it asks when GetBucketLocation names no region outside aws, or
// GetBucketLocation.
func s3RegionLookupOp(err error) string {
	var oe *smithy.OperationError
	if errors.As(err, &oe) && oe.OperationName == "HeadBucket" {
		return "s3:HeadBucket"
	}
	return "s3:GetBucketLocation"
}

// s3BucketConfigFanOut runs list for every homed bucket and stores the rows
// under the bucket.
func s3BucketConfigFanOut(
	ctx context.Context, acct *account, homed map[string]s3HomedBucket, st *store.Store, op string,
	list func(context.Context, s3HomedBucket) ([]*store.Resource, error),
) (int, int, error) {
	parents := make([]childParent, 0, len(homed))
	for name := range homed {
		parents = append(parents, childParent{id: name, arn: s3BucketARN(acct.partition(), name)})
	}
	return childFanOut(ctx, st, acct, sv(regionGlobal), op, parents, s3BucketGone,
		func(ctx context.Context, p childParent) ([]*store.Resource, error) {
			return list(ctx, homed[p.id])
		})
}

func s3BucketARN(partition, name string) string { return arnFor(partition, "s3", "", "", name) }

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
		NativeID:       s3BucketARN(acct.partition(), bucket) + "/" + kind + "/" + *id,
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

// scanS3BucketEncryptions fetches GetBucketEncryption for each homed bucket
// concurrently and stores the result in acct.s3BucketEncryption, keyed by
// bucket name. Buckets without explicit encryption config return
// ServerSideEncryptionConfigurationNotFoundError and are silently skipped;
// AccessDenied is also tolerated (best-effort).
func scanS3BucketEncryptions(ctx context.Context, acct *account, homed map[string]s3HomedBucket) error {
	sem := semaphore.NewWeighted(fanoutHigh)
	acct.s3BucketEncryption = make(map[string]s3BucketEncryptionEntry)
	g, gctx := errgroup.WithContext(ctx)
	for name, b := range homed {
		g.Go(func() error {
			if err := sem.Acquire(gctx, 1); err != nil {
				return err
			}
			defer sem.Release(1)

			out, err := b.client.GetBucketEncryption(gctx, &s3.GetBucketEncryptionInput{Bucket: &b.name})
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
				Region: b.region,
				Config: out.ServerSideEncryptionConfiguration,
			}
			acct.s3BucketEncryptionMu.Unlock()
			return nil
		})
	}
	return g.Wait()
}

// scanS3BucketPolicies fetches the bucket policy for each homed bucket
// concurrently, from the bucket's home region (S3 answers any other region
// with 301 PermanentRedirect). Buckets with no policy (NoSuchBucketPolicy)
// are silently skipped.
func scanS3BucketPolicies(ctx context.Context, acct *account, homed map[string]s3HomedBucket, st *store.Store, scanID string) (total, inserted int, err error) {
	sem := semaphore.NewWeighted(fanoutHigh)
	var (
		mu    sync.Mutex
		batch []*store.Resource
	)
	g, gctx := errgroup.WithContext(ctx)
	for name, b := range homed {
		g.Go(func() error {
			if err := sem.Acquire(gctx, 1); err != nil {
				return err
			}
			defer sem.Release(1)

			out, err := b.client.GetBucketPolicy(gctx, &s3.GetBucketPolicyInput{Bucket: &b.name})
			if err != nil {
				// No policy on this bucket — expected and common.
				if isAPIErrorCode(err, "NoSuchBucketPolicy") || isAccessDenied(err) {
					return nil
				}
				return fmt.Errorf("s3:GetBucketPolicy %s: %w", name, err)
			}
			arn := s3BucketARN(acct.partition(), name) + "/policy"
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

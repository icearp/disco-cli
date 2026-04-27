// Package aws implements cloud resource discovery for Amazon Web Services.
// It makes per-service API calls using the AWS SDK v2 and follows the
// two-phase scan pattern: resources are written first, relationships second.
package aws

import (
	"context"
	"errors"
	"fmt"
	"net"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"codeberg.org/icearp/disco/internal/providers"
	"codeberg.org/icearp/disco/internal/store"
	"codeberg.org/icearp/disco/internal/util"
	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	acmtypes "github.com/aws/aws-sdk-go-v2/service/acm/types"
	cloudfronttypes "github.com/aws/aws-sdk-go-v2/service/cloudfront/types"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	ecrtypes "github.com/aws/aws-sdk-go-v2/service/ecr/types"
	ecstypes "github.com/aws/aws-sdk-go-v2/service/ecs/types"
	elasticachetypes "github.com/aws/aws-sdk-go-v2/service/elasticache/types"
	firehosetypes "github.com/aws/aws-sdk-go-v2/service/firehose/types"
	iamtypes "github.com/aws/aws-sdk-go-v2/service/iam/types"
	kinesistypes "github.com/aws/aws-sdk-go-v2/service/kinesis/types"
	rdstypes "github.com/aws/aws-sdk-go-v2/service/rds/types"
	route53types "github.com/aws/aws-sdk-go-v2/service/route53/types"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	smithy "github.com/aws/smithy-go"
	"golang.org/x/sync/semaphore"
)

const (
	// maxConcurrentServices caps the number of service scanners running in parallel
	// per region to avoid hitting AWS API rate limits.
	maxConcurrentServices = 10
	// serviceTimeout is the per-service hard deadline. A misbehaving API endpoint
	// won't stall the entire scan beyond this duration.
	serviceTimeout = 5 * time.Minute
)

func init() { providers.Register(&Scanner{}) }

// Scanner implements providers.Scanner for AWS.
type Scanner struct {
	serviceFilter  []string // nil = scan all registered services
	regionOverride []string // non-nil overrides all per-account and default regions
	profile        string   // "" = default AWS credential chain
}

func (s *Scanner) Name() string { return "aws" }

// SetServiceFilter restricts the scan to the named services (e.g. "aws:ec2", "aws:iam").
// An empty or nil slice scans all registered services.
func (s *Scanner) SetServiceFilter(services []string) { s.serviceFilter = services }

// SetRegionOverride forces all accounts to scan only the given regions,
// ignoring both per-account and default_regions config values.
func (s *Scanner) SetRegionOverride(regions []string) { s.regionOverride = regions }

// SetProfile selects a named credential profile from ~/.aws/config.
// An empty string uses the default credential chain.
func (s *Scanner) SetProfile(profile string) { s.profile = profile }

// ServiceNames returns the names of all services this scanner will report.
func (s *Scanner) ServiceNames() []string {
	svcs := filteredServices(s.serviceFilter)
	names := make([]string, len(svcs))
	for i, svc := range svcs {
		names[i] = svc.name
	}
	return names
}

// Scan discovers all AWS resources across all configured accounts and regions.
// Errors are reported via st.ReportError and never abort the scan: a failure
// in one service / resolver / account does not stop the others.
func (s *Scanner) Scan(ctx context.Context, st *store.Store, scanID string) error {
	accounts, err := loadAccounts(ctx, s.profile, s.regionOverride)
	if err != nil {
		st.ReportError(store.ScanError{
			Provider: "aws", Service: "load-accounts", Scope: "", Message: err.Error(),
		})
		return nil
	}
	for i := range accounts {
		scanAccount(ctx, &accounts[i], s.serviceFilter, st, scanID)
	}
	return nil
}

// scanAccount runs phase 1 (resources) then phase 2 (relationships) for one
// account. Errors are reported via st.ReportError and never propagate — a
// service failure does not abort sibling services or the relationship phase.
func scanAccount(ctx context.Context, acct *account, services []string, st *store.Store, scanID string) {
	// Phase 1a: global services (once per account, region is irrelevant).
	// Use a plain WaitGroup with a semaphore — errgroup would cancel siblings
	// on first error, which we explicitly do not want anymore.
	sem := semaphore.NewWeighted(maxConcurrentServices)
	var wg0 sync.WaitGroup
	for _, svc := range filteredServices(services) {
		if !svc.global {
			continue
		}
		wg0.Go(func() {
			if err := sem.Acquire(ctx, 1); err != nil {
				return
			}
			defer sem.Release(1)
			svcCtx, cancel := context.WithTimeout(ctx, serviceTimeout)
			defer cancel()
			total, inserted, err := svc.fn(svcCtx, acct, "", st, scanID)
			if err != nil {
				if isTransientNetworkError(err) {
					_ = skipIfTransient(st, svc.name, acct.ID, "", err)
					st.ReportService(svc.name, 0, 0, 0)
					return
				}
				st.ReportError(store.ScanError{
					Provider: "aws", Service: svc.name, Scope: acct.ID, Message: err.Error(),
				})
				st.ReportService(svc.name, total, inserted, 1)
				return
			}
			st.ReportService(svc.name, total, inserted, 0)
		})
	}
	wg0.Wait()

	// Phase 1b: regional services — all regions sequentially, parallel within each.
	for _, region := range acct.Regions {
		scanRegion(ctx, acct, region, services, st, scanID)
	}

	// Phase 2: derive relationships now that all resources exist in the DB.
	st.ReportResolveStart("aws")
	var counter atomic.Int64
	resolveRelationships(ctx, acct, st.WithRelCounter(&counter))
	st.ReportResolveComplete("aws", int(counter.Load()))
}

// resolveRelationships is phase 2: after all resources are written to the DB,
// derive relationships from the JSON attributes stored on each resource.
// Using ResourceID to compute stable IDs means we never need to read back
// resources just to get their primary keys. A failure in one resolver is
// reported and does not stop the others — partial graph beats no graph.
func resolveRelationships(_ context.Context, acct *account, st *store.Store) {
	for _, r := range registeredResolvers {
		if err := r.fn(acct, st); err != nil {
			st.ReportError(store.ScanError{
				Provider: "aws",
				Service:  "resolve:" + r.name,
				Scope:    acct.ID,
				Message:  err.Error(),
			})
		}
	}
}

// scanRegion runs all regional service scanners in parallel, bounded by
// maxConcurrentServices to avoid API rate limits. Service failures are
// reported and never abort sibling services.
func scanRegion(ctx context.Context, acct *account, region string, services []string, st *store.Store, scanID string) {
	sem := semaphore.NewWeighted(maxConcurrentServices)
	var wg sync.WaitGroup
	for _, svc := range filteredServices(services) {
		if svc.global {
			continue
		}
		wg.Go(func() {
			if err := sem.Acquire(ctx, 1); err != nil {
				return
			}
			defer sem.Release(1)
			svcCtx, cancel := context.WithTimeout(ctx, serviceTimeout)
			defer cancel()
			total, inserted, err := svc.fn(svcCtx, acct, region, st, scanID)
			if err != nil {
				if errors.Is(err, errServiceDisabled) {
					st.ReportService(svc.name, 0, 0, 0, true)
					return
				}
				if isTransientNetworkError(err) {
					_ = skipIfTransient(st, svc.name, acct.ID, region, err)
					st.ReportService(svc.name, 0, 0, 0, false)
					return
				}
				st.ReportError(store.ScanError{
					Provider: "aws", Service: svc.name, Scope: acct.ID + "/" + region, Message: err.Error(),
				})
				st.ReportService(svc.name, total, inserted, 1, false)
				return
			}
			st.ReportService(svc.name, total, inserted, 0, false)
		})
	}
	wg.Wait()
}

// filteredServices returns the services to run. When filter is non-empty, only
// services whose name appears in filter are returned.
func filteredServices(filter []string) []serviceEntry {
	if len(filter) == 0 {
		return registeredServices
	}
	allowed := make(map[string]bool, len(filter))
	for _, name := range filter {
		allowed[name] = true
	}
	var out []serviceEntry
	for _, svc := range registeredServices {
		if allowed[svc.name] {
			out = append(out, svc)
		}
	}
	return out
}

// — shared helpers used across all service files in this package —

// account holds the resolved SDK config and scan scope for one AWS account.
type account struct {
	ID      string
	Name    string
	Regions []string
	cfg     sdkaws.Config // credentials + endpoint config; region is set per client

	// s3BucketEncryption holds per-bucket SSE config fetched during the S3 scan,
	// keyed by bucket name. Populated by scanS3BucketEncryptions, consumed by
	// resolveS3BucketEncryptionRelationships. Ephemeral (per scan run) — the
	// resulting KMS edges persist in relationships; the config itself does not.
	s3BucketEncryption   map[string]s3BucketEncryptionEntry
	s3BucketEncryptionMu sync.Mutex
}

// s3BucketEncryptionEntry carries a bucket's SSE configuration alongside the
// bucket's home region. Region is needed by the resolver so bare KMS key IDs
// and aliases in KMSMasterKeyID can be normalized to full cross-region ARNs.
type s3BucketEncryptionEntry struct {
	Region string
	Config *s3types.ServerSideEncryptionConfiguration
}

func mustJSON(v any) string   { return util.MustJSON(v) }
func sv(p *string) string     { return util.Sv(p) }
func tp(t *time.Time) *string { return util.TimeRFC3339(t) }

// sp returns a pointer to s.
func sp(s string) *string { return &s }

// ec2ARN builds a standard EC2 ARN: arn:aws:ec2:{region}:{account}:{type}/{id}
func ec2ARN(region, accountID, resourceType, id string) string {
	return fmt.Sprintf("arn:aws:ec2:%s:%s:%s/%s", region, accountID, resourceType, id)
}

// awsTag is the set of AWS SDK tag types that carry Key and Value string pointers.
type awsTag interface {
	acmtypes.Tag | cloudfronttypes.Tag | ec2types.Tag | ecrtypes.Tag | ecstypes.Tag | elasticachetypes.Tag | firehosetypes.Tag | iamtypes.Tag | kinesistypes.Tag | rdstypes.Tag | route53types.Tag
}

// awsTagsJSON converts any AWS SDK tag slice to a JSON-encoded {key:value} map.
// Returns nil when the slice is empty.
func awsTagsJSON[T awsTag](tags []T) *string {
	if len(tags) == 0 {
		return nil
	}
	m := make(map[string]string, len(tags))
	for _, t := range tags {
		var k, v *string
		switch tt := any(t).(type) {
		case acmtypes.Tag:
			k, v = tt.Key, tt.Value
		case cloudfronttypes.Tag:
			k, v = tt.Key, tt.Value
		case ec2types.Tag:
			k, v = tt.Key, tt.Value
		case ecrtypes.Tag:
			k, v = tt.Key, tt.Value
		case ecstypes.Tag:
			k, v = tt.Key, tt.Value
		case elasticachetypes.Tag:
			k, v = tt.Key, tt.Value
		case firehosetypes.Tag:
			k, v = tt.Key, tt.Value
		case iamtypes.Tag:
			k, v = tt.Key, tt.Value
		case kinesistypes.Tag:
			k, v = tt.Key, tt.Value
		case rdstypes.Tag:
			k, v = tt.Key, tt.Value
		case route53types.Tag:
			k, v = tt.Key, tt.Value
		}
		if k != nil && v != nil {
			m[*k] = *v
		}
	}
	s := mustJSON(m)
	return &s
}

// mapTagsJSON converts a map[string]string tag map to a JSON-encoded {key:value}
// string pointer. Used by services whose SDK returns tags as a plain map rather
// than a slice of typed Tag structs. Returns nil when the map is empty.
func mapTagsJSON(tags map[string]string) *string {
	if len(tags) == 0 {
		return nil
	}
	s := mustJSON(tags)
	return &s
}

// isAPIErrorCode reports whether err is a Smithy APIError whose ErrorCode()
// matches any of the given codes. Single choke point for AWS error-code
// predicates in this package — use directly for one-off checks, or wrap in
// a named helper (see isAccessDenied) for codes reused across many sites.
func isAPIErrorCode(err error, codes ...string) bool {
	var ae smithy.APIError
	if !errors.As(err, &ae) {
		return false
	}
	return slices.Contains(codes, ae.ErrorCode())
}

// isAccessDenied reports whether err is an AWS permission error. Such errors
// are expected when the scanning role lacks access to a specific service or
// region and should be logged then skipped rather than aborting the scan.
func isAccessDenied(err error) bool {
	return isAPIErrorCode(err,
		"AccessDenied", "UnauthorizedOperation", "AuthFailure",
		"AccessDeniedException", "NotAuthorized", "ForbiddenException")
}

// errServiceDisabled is a sentinel returned by per-service scanners when they
// detect that the AWS service itself is not enabled in the calling account or
// region (Macie not enabled, Shield Advanced not subscribed, Security Hub
// hub not present, etc). The scanRegion / scanAccount dispatch loop detects
// it via errors.Is and surfaces "(service disabled)" on the progress line —
// no warning, no error report. Wrap upstream errors via markServiceDisabled
// so the original message is preserved for debugging if anyone unwraps.
var errServiceDisabled = errors.New("aws service not enabled")

// markServiceDisabled wraps the upstream "feature not enabled" error so the
// dispatch loop can identify it via errors.Is(err, errServiceDisabled). Per-
// service helpers (isShieldNotSubscribed, isSecurityHubNotEnabled, Macie's
// "Macie is not enabled" message check) return this from their phase-1
// detection step instead of nil.
func markServiceDisabled(err error) error {
	return fmt.Errorf("%w: %s", errServiceDisabled, err.Error())
}

// skipIfAccessDenied records the error as a scan warning and returns nil,
// allowing the caller to continue scanning other services. Warnings are
// collected by the orchestrator (cmd/scan.go) and rendered as a grouped
// block after the scan completes — no inline log line interleaves with
// the aligned progress output. The caller must already have verified the
// error is an access-denied shape via isAccessDenied.
func skipIfAccessDenied(st *store.Store, service, accountID, region string, err error) error {
	scope := accountID
	if region != "" && region != "global" {
		scope = accountID + "/" + region
	}
	st.ReportWarning(store.ScanWarning{
		Provider: "aws",
		Service:  service,
		Scope:    scope,
		Message:  err.Error(),
	})
	return nil
}

// isTransientNetworkError reports whether err looks like a momentary network
// or service-side glitch rather than a permanent failure. The SDK v2 retryer
// already handles throttling and 5xx within its budget; anything that reaches
// us is post-retry. A one-off DNS blip or endpoint flap should warn-skip and
// let sibling services continue, matching the AccessDenied policy — otherwise
// a single hiccup aborts the whole scan.
//
// Context cancellation (Ctrl-C, parent timeout) is deliberately NOT treated
// as transient: those indicate the scan should stop.
func isTransientNetworkError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.Canceled) {
		return false
	}
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return true
	}
	var opErr *net.OpError
	if errors.As(err, &opErr) {
		return true
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return true
	}
	return isAPIErrorCode(err,
		"RequestTimeout", "RequestTimeoutException", "RequestCanceled",
		"ServiceUnavailable", "ServiceUnavailableException",
		"InternalServerError", "InternalFailure", "InternalServerErrorException")
}

// skipIfTransient mirrors skipIfAccessDenied for transient network/service
// errors. Records a ScanWarning and returns nil so the caller can continue.
// The caller must have verified the error shape via isTransientNetworkError.
func skipIfTransient(st *store.Store, service, accountID, region string, err error) error {
	scope := accountID
	if region != "" && region != "global" {
		scope = accountID + "/" + region
	}
	st.ReportWarning(store.ScanWarning{
		Provider: "aws",
		Service:  service,
		Scope:    scope,
		Message:  "transient: " + err.Error(),
	})
	return nil
}

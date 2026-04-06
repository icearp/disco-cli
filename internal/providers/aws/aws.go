// Package aws implements cloud resource discovery for Amazon Web Services.
// It makes per-service API calls using the AWS SDK v2 and follows the
// two-phase scan pattern: resources are written first, relationships second.
package aws

import (
	"context"
	"errors"
	"fmt"
	"log"
	"time"

	"codeburg.org/icearp/disco/internal/providers"
	"codeburg.org/icearp/disco/internal/store"
	"codeburg.org/icearp/disco/internal/util"
	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	iamtypes "github.com/aws/aws-sdk-go-v2/service/iam/types"
	rdstypes "github.com/aws/aws-sdk-go-v2/service/rds/types"
	smithy "github.com/aws/smithy-go"
	"golang.org/x/sync/errgroup"
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
	serviceFilter []string // nil = scan all registered services
}

func (s *Scanner) Name() string { return "aws" }

// SetServiceFilter restricts the scan to the named services (e.g. "aws:ec2", "aws:iam").
// An empty or nil slice scans all registered services.
func (s *Scanner) SetServiceFilter(services []string) { s.serviceFilter = services }

// Scan discovers all AWS resources across all configured accounts and regions.
func (s *Scanner) Scan(ctx context.Context, st *store.Store, scanID string) error {
	accounts, err := loadAccounts(ctx)
	if err != nil {
		return fmt.Errorf("aws: load accounts: %w", err)
	}
	for i := range accounts {
		if err := scanAccount(ctx, &accounts[i], s.serviceFilter, st, scanID); err != nil {
			return fmt.Errorf("aws account %s: %w", accounts[i].ID, err)
		}
	}
	return nil
}

// scanAccount runs phase 1 (resources) then phase 2 (relationships) for one account.
func scanAccount(ctx context.Context, acct *account, services []string, st *store.Store, scanID string) error {
	// Phase 1a: global services (once per account, region is irrelevant).
	sem := semaphore.NewWeighted(maxConcurrentServices)
	g0, ctx0 := errgroup.WithContext(ctx)
	for _, svc := range filteredServices(services) {
		if !svc.global {
			continue
		}
		svc := svc
		g0.Go(func() error {
			if err := sem.Acquire(ctx0, 1); err != nil {
				return err
			}
			defer sem.Release(1)
			svcCtx, cancel := context.WithTimeout(ctx0, serviceTimeout)
			defer cancel()
			if err := svc.fn(svcCtx, acct, "", st, scanID); err != nil {
				return err
			}
			st.ReportService(svc.name)
			return nil
		})
	}
	if err := g0.Wait(); err != nil {
		return err
	}

	// Phase 1b: regional services — all regions sequentially, parallel within each.
	for _, region := range acct.Regions {
		if err := scanRegion(ctx, acct, region, services, st, scanID); err != nil {
			return fmt.Errorf("region %s: %w", region, err)
		}
	}

	// Phase 2: derive relationships now that all resources exist in the DB.
	return resolveRelationships(ctx, acct, st)
}

// resolveRelationships is phase 2: after all resources are written to the DB,
// derive relationships from the JSON attributes stored on each resource.
// Using ResourceID to compute stable IDs means we never need to read back
// resources just to get their primary keys.
func resolveRelationships(_ context.Context, acct *account, st *store.Store) error {
	for _, r := range registeredResolvers {
		if err := r.fn(acct, st); err != nil {
			return err
		}
	}
	return nil
}

// scanRegion runs all regional service scanners in parallel, bounded by
// maxConcurrentServices to avoid API rate limits.
func scanRegion(ctx context.Context, acct *account, region string, services []string, st *store.Store, scanID string) error {
	sem := semaphore.NewWeighted(maxConcurrentServices)
	g, gctx := errgroup.WithContext(ctx)
	for _, svc := range filteredServices(services) {
		if svc.global {
			continue
		}
		svc := svc
		g.Go(func() error {
			if err := sem.Acquire(gctx, 1); err != nil {
				return err
			}
			defer sem.Release(1)
			svcCtx, cancel := context.WithTimeout(gctx, serviceTimeout)
			defer cancel()
			if err := svc.fn(svcCtx, acct, region, st, scanID); err != nil {
				return err
			}
			st.ReportService(svc.name)
			return nil
		})
	}
	return g.Wait()
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
}

func mustJSON(v any) string    { return util.MustJSON(v) }
func sv(p *string) string      { return util.Sv(p) }
func tp(t *time.Time) *string  { return util.TimeRFC3339(t) }

// sp returns a pointer to s.
func sp(s string) *string { return &s }

// ec2ARN builds a standard EC2 ARN: arn:aws:ec2:{region}:{account}:{type}/{id}
func ec2ARN(region, accountID, resourceType, id string) string {
	return fmt.Sprintf("arn:aws:ec2:%s:%s:%s/%s", region, accountID, resourceType, id)
}

// awsTag is the set of AWS SDK tag types that carry Key and Value string pointers.
type awsTag interface {
	ec2types.Tag | iamtypes.Tag | rdstypes.Tag
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
		case ec2types.Tag:
			k, v = tt.Key, tt.Value
		case iamtypes.Tag:
			k, v = tt.Key, tt.Value
		case rdstypes.Tag:
			k, v = tt.Key, tt.Value
		}
		if k != nil && v != nil {
			m[*k] = *v
		}
	}
	s := mustJSON(m)
	return &s
}

// isAccessDenied reports whether err is an AWS permission error. Such errors
// are expected when the scanning role lacks access to a specific service or
// region and should be logged then skipped rather than aborting the scan.
func isAccessDenied(err error) bool {
	var ae smithy.APIError
	if errors.As(err, &ae) {
		switch ae.ErrorCode() {
		case "AccessDenied", "UnauthorizedOperation", "AuthFailure",
			"AccessDeniedException", "NotAuthorized", "ForbiddenException":
			return true
		}
	}
	return false
}

// skipIfAccessDenied logs the error and returns nil when it is an access-denied
// error, allowing the caller to continue scanning other services.
func skipIfAccessDenied(service, accountID, region string, err error) error {
	log.Printf("warn: aws %s %s/%s: %v (skipping)", service, accountID, region, err)
	return nil
}

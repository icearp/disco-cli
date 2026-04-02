// Package aws implements cloud resource discovery for Amazon Web Services.
// It makes per-service API calls using the AWS SDK v2 and follows the
// two-phase scan pattern: resources are written first, relationships second.
package aws

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"

	"codeburg.org/icearp/disco/internal/providers"
	"codeburg.org/icearp/disco/internal/store"
	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	smithy "github.com/aws/smithy-go"
	"golang.org/x/sync/errgroup"
)

func init() { providers.Register(&Scanner{}) }

// Scanner implements providers.Scanner for AWS.
type Scanner struct{}

func (s *Scanner) Name() string { return "aws" }

// Scan discovers all AWS resources across all configured accounts and regions.
func (s *Scanner) Scan(ctx context.Context, st *store.Store, scanID string) error {
	accounts, err := loadAccounts(ctx)
	if err != nil {
		return fmt.Errorf("aws: load accounts: %w", err)
	}
	for i := range accounts {
		if err := scanAccount(ctx, &accounts[i], st, scanID); err != nil {
			return fmt.Errorf("aws account %s: %w", accounts[i].ID, err)
		}
	}
	return nil
}

// scanAccount runs phase 1 (resources) then phase 2 (relationships) for one account.
func scanAccount(ctx context.Context, acct *account, st *store.Store, scanID string) error {
	// IAM and S3 are global — scanned once per account, not per region.
	g0, ctx0 := errgroup.WithContext(ctx)
	g0.Go(func() error { return scanIAM(ctx0, acct, st, scanID) })
	g0.Go(func() error { return scanS3(ctx0, acct, st, scanID) })
	if err := g0.Wait(); err != nil {
		return err
	}

	// Regional services: run all regions sequentially (parallel within each region).
	for _, region := range acct.Regions {
		if err := scanRegion(ctx, acct, region, st, scanID); err != nil {
			return fmt.Errorf("region %s: %w", region, err)
		}
	}

	// Phase 2: derive relationships now that all resources exist in the DB.
	return resolveRelationships(ctx, acct, st)
}

// scanRegion runs all regional service scanners in parallel.
func scanRegion(ctx context.Context, acct *account, region string, st *store.Store, scanID string) error {
	g, ctx := errgroup.WithContext(ctx)
	g.Go(func() error { return scanEC2(ctx, acct, region, st, scanID) })
	g.Go(func() error { return scanRDS(ctx, acct, region, st, scanID) })
	g.Go(func() error { return scanLambda(ctx, acct, region, st, scanID) })
	g.Go(func() error { return scanELB(ctx, acct, region, st, scanID) })
	g.Go(func() error { return scanEKS(ctx, acct, region, st, scanID) })
	g.Go(func() error { return scanDynamoDB(ctx, acct, region, st, scanID) })
	g.Go(func() error { return scanSQS(ctx, acct, region, st, scanID) })
	g.Go(func() error { return scanSNS(ctx, acct, region, st, scanID) })
	return g.Wait()
}

// — shared helpers used across all service files in this package —

// account holds the resolved SDK config and scan scope for one AWS account.
type account struct {
	ID      string
	Name    string
	Regions []string
	cfg     sdkaws.Config // credentials + endpoint config; region is set per client
}

// mustJSON marshals v to a JSON string. Returns "{}" if marshalling fails —
// this should never happen for well-formed AWS SDK response structs.
func mustJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return "{}"
	}
	return string(b)
}

// sv dereferences a string pointer, returning "" for nil.
func sv(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

// sp returns a pointer to s.
func sp(s string) *string { return &s }

// ec2ARN builds a standard EC2 ARN: arn:aws:ec2:{region}:{account}:{type}/{id}
func ec2ARN(region, accountID, resourceType, id string) string {
	return fmt.Sprintf("arn:aws:ec2:%s:%s:%s/%s", region, accountID, resourceType, id)
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

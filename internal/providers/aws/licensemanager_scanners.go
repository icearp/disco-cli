package aws

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/store"
	"github.com/aws/aws-sdk-go-v2/service/licensemanager"
)

// isLicenseManagerNotSetUp disambiguates the "Service role not found" setup
// gap from a real IAM denial. License Manager requires creating the
// AWSServiceRoleForAWSLicenseManagerRole; missing it surfaces as
// AccessDeniedException with the same "Service role not found" message.
func isLicenseManagerNotSetUp(err error) bool {
	return isAccessDeniedWithMessage(err, "Service role not found")
}

func init() {
	registerService(serviceEntry{
		name:   "aws:license-manager",
		global: true,
		fn:     scanLicenseManager,
		emits: []coverage.TypeDecl{
			{Service: "license-manager", DiscoType: TypeLicenseManagerLicense, Leaf: true},
			{Service: "license-manager", DiscoType: TypeLicenseManagerGrant},
		},
	})
}

type licenseManagerAPI interface {
	ListLicenses(context.Context, *licensemanager.ListLicensesInput, ...func(*licensemanager.Options)) (*licensemanager.ListLicensesOutput, error)
	ListDistributedGrants(context.Context, *licensemanager.ListDistributedGrantsInput, ...func(*licensemanager.Options)) (*licensemanager.ListDistributedGrantsOutput, error)
}

// scanLicenseManager discovers License Manager licenses (issued by this
// account) and distributed grants. License Manager is global; gate to
// us-east-1 to avoid duplicate scans across regions.
func scanLicenseManager(ctx context.Context, acct *account, _ string, st *store.Store, scanID string) (total, inserted int, err error) {
	region := "us-east-1"
	client := licensemanager.NewFromConfig(acct.cfg, func(o *licensemanager.Options) { o.Region = region })

	t, i, ferr := scanLMLicenses(ctx, client, acct, region, st, scanID)
	if ferr != nil {
		return total, inserted, ferr
	}
	total += t
	inserted += i

	t, i, ferr = scanLMGrants(ctx, client, acct, region, st, scanID)
	if ferr != nil {
		return total, inserted, ferr
	}
	total += t
	inserted += i
	return total, inserted, nil
}

func scanLMLicenses(ctx context.Context, client licenseManagerAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var batch []*store.Resource
	var nextToken *string
	for {
		out, err := client.ListLicenses(ctx, &licensemanager.ListLicensesInput{NextToken: nextToken})
		if err != nil {
			if isLicenseManagerNotSetUp(err) {
				return 0, 0, markServiceDisabled(err)
			}
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "license-manager:ListLicenses", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("license-manager:ListLicenses: %w", err)
		}
		for _, l := range out.Licenses {
			arn := sv(l.LicenseArn)
			if arn == "" {
				continue
			}
			status := string(l.Status)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeLicenseManagerLicense, NativeID: arn,
				Name: l.LicenseName, Region: regionGlobal, Status: &status,
				AttributesJSON: mustJSON(l), DiscoveredBy: scanID,
			})
		}
		if out.NextToken == nil || *out.NextToken == "" {
			break
		}
		nextToken = out.NextToken
	}
	return upsertBatch(st, batch, "license-manager licenses")
}

func scanLMGrants(ctx context.Context, client licenseManagerAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var batch []*store.Resource
	var nextToken *string
	for {
		out, err := client.ListDistributedGrants(ctx, &licensemanager.ListDistributedGrantsInput{NextToken: nextToken})
		if err != nil {
			if isLicenseManagerNotSetUp(err) {
				return 0, 0, markServiceDisabled(err)
			}
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "license-manager:ListDistributedGrants", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("license-manager:ListDistributedGrants: %w", err)
		}
		for _, g := range out.Grants {
			arn := sv(g.GrantArn)
			if arn == "" {
				continue
			}
			status := string(g.GrantStatus)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeLicenseManagerGrant, NativeID: arn,
				Name: g.GrantName, Region: regionGlobal, Status: &status,
				AttributesJSON: mustJSON(g), DiscoveredBy: scanID,
			})
		}
		if out.NextToken == nil || *out.NextToken == "" {
			break
		}
		nextToken = out.NextToken
	}
	return upsertBatch(st, batch, "license-manager grants")
}

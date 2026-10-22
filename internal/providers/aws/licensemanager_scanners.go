package aws

import (
	"context"
	"errors"
	"fmt"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/aws/retry"
	"github.com/aws/aws-sdk-go-v2/service/licensemanager"
	lmtypes "github.com/aws/aws-sdk-go-v2/service/licensemanager/types"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

// isLicenseManagerNotSetUp distinguishes the "Service role not found" setup gap
// from a real IAM denial. License Manager requires the
// AWSServiceRoleForAWSLicenseManagerRole; its absence surfaces as
// AccessDeniedException with that same message.
func isLicenseManagerNotSetUp(err error) bool {
	return isAccessDeniedWithMessage(err, "Service role not found")
}

func init() {
	registerType(restype.Descriptor{Type: TypeLicenseManagerLicense, Service: "license-manager"})
	registerType(restype.Descriptor{Type: TypeLicenseManagerGrant, Service: "license-manager"})
	registerType(restype.Descriptor{Type: TypeLicenseManagerLicenseConfiguration, Service: "license-manager"})
	registerType(restype.Descriptor{Type: TypeLicenseManagerReportGenerator, Service: "license-manager"})
	registerType(restype.Descriptor{Type: TypeLicenseManagerLicenseAssetGroup, Service: "license-manager"})
	registerType(restype.Descriptor{Type: TypeLicenseManagerLicenseAssetRuleset, Service: "license-manager"})
	registerType(restype.Descriptor{Type: TypeLicenseManagerReceivedLicense, Service: "license-manager"})
	registerType(restype.Descriptor{Type: TypeLicenseManagerToken, Service: "license-manager"})
	registerService(serviceEntry{
		name:   "aws:license-manager",
		global: true,
		fn:     scanLicenseManager,
	})
}

type licenseManagerAPI interface {
	ListLicenses(context.Context, *licensemanager.ListLicensesInput, ...func(*licensemanager.Options)) (*licensemanager.ListLicensesOutput, error)
	ListDistributedGrants(context.Context, *licensemanager.ListDistributedGrantsInput, ...func(*licensemanager.Options)) (*licensemanager.ListDistributedGrantsOutput, error)
	ListLicenseConfigurations(context.Context, *licensemanager.ListLicenseConfigurationsInput, ...func(*licensemanager.Options)) (*licensemanager.ListLicenseConfigurationsOutput, error)
	ListLicenseManagerReportGenerators(context.Context, *licensemanager.ListLicenseManagerReportGeneratorsInput, ...func(*licensemanager.Options)) (*licensemanager.ListLicenseManagerReportGeneratorsOutput, error)
	ListLicenseAssetGroups(context.Context, *licensemanager.ListLicenseAssetGroupsInput, ...func(*licensemanager.Options)) (*licensemanager.ListLicenseAssetGroupsOutput, error)
	ListLicenseAssetRulesets(context.Context, *licensemanager.ListLicenseAssetRulesetsInput, ...func(*licensemanager.Options)) (*licensemanager.ListLicenseAssetRulesetsOutput, error)
	ListReceivedLicenses(context.Context, *licensemanager.ListReceivedLicensesInput, ...func(*licensemanager.Options)) (*licensemanager.ListReceivedLicensesOutput, error)
	ListTokens(context.Context, *licensemanager.ListTokensInput, ...func(*licensemanager.Options)) (*licensemanager.ListTokensOutput, error)
}

// lmPageSize is the maximum MaxResults of ListReceivedLicenses and ListTokens (1-100).
const lmPageSize = 100

// scanLicenseManager discovers License Manager licenses, grants, license
// configurations, report generators, asset groups and rulesets, received
// licenses and tokens. Global service; gated to us-east-1 to avoid duplicate
// scans across regions.
func scanLicenseManager(ctx context.Context, acct *account, _ string, st *store.Store, scanID string) (total, inserted int, err error) {
	region := "us-east-1"
	// The SDK neither lists RateLimitExceededException, License Manager's HTTP
	// 429 throttle, among its throttle codes nor retries a 429 by status, so
	// without this a single throttle fails the call unretried. It gets standard
	// backoff only: adaptive mode's client-side rate limiting does not learn
	// from a code added this way.
	client := licensemanager.NewFromConfig(acct.cfg, func(o *licensemanager.Options) {
		o.Region = region
		o.Retryer = retry.AddWithErrorCodes(o.Retryer, "RateLimitExceededException")
	})

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

	for _, phase := range []func() (int, int, error){
		func() (int, int, error) { return scanLMLicenseConfigurations(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanLMReportGenerators(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanLMLicenseAssetGroups(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanLMLicenseAssetRulesets(ctx, client, acct, region, st, scanID) },
	} {
		t, i, ferr = phase()
		if ferr != nil {
			return total, inserted, ferr
		}
		total += t
		inserted += i
	}

	// The received-license and token phases run last and independently, so
	// neither one's failure stops the other or any earlier phase. A transient
	// error is demoted before the join so it cannot mask the other's hard error.
	var errs []error
	for _, phase := range []struct {
		op  string
		run func() (int, int, error)
	}{
		{"license-manager:ListReceivedLicenses", func() (int, int, error) { return scanLMReceivedLicenses(ctx, client, acct, region, st, scanID) }},
		{"license-manager:ListTokens", func() (int, int, error) { return scanLMTokens(ctx, client, acct, region, st, scanID) }},
	} {
		t, i, ferr = phase.run()
		total += t
		inserted += i
		errs = append(errs, demoteTransient(st, phase.op, acct, region, ferr))
	}
	return total, inserted, errors.Join(errs...)
}

// lmReceivedLicenseNativeID keys a received license apart from the license
// itself. ListReceivedLicenses returns the issuer's LicenseArn, which is the
// NativeID ListLicenses gives the license in the issuing account, and nothing
// in the API stops an account granting its own license to itself. A license
// ARN ends in its l- id, so no license ARN ends in "/received".
func lmReceivedLicenseNativeID(licenseArn string) string {
	return licenseArn + "/received"
}

// lmTokenNativeID keys a token under its license. A token has no ARN, and
// CreateToken requires the license the token is for.
func lmTokenNativeID(licenseArn, tokenID string) string {
	return licenseArn + "/token/" + tokenID
}

// scanLMReceivedLicenses stores the licenses granted to this account, with
// this account's received status (PENDING_ACCEPT, ACTIVE, REJECTED, …) as the
// row status. A DELETED license is skipped. A denial on a later page keeps the
// rows of the pages already read.
func scanLMReceivedLicenses(ctx context.Context, client licenseManagerAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	const op = "license-manager:ListReceivedLicenses"
	var (
		batch   []*store.Resource
		pageErr error
	)
	input := &licensemanager.ListReceivedLicensesInput{MaxResults: sdkaws.Int32(lmPageSize)}
	for {
		out, err := client.ListReceivedLicenses(ctx, input)
		// No isLicenseManagerNotSetUp check, unlike the sibling phases: every earlier
		// phase already succeeded, so a not-set-up error here can only be a partial
		// IAM denial, and marking the service disabled would discard their counts.
		if isAccessDenied(err) {
			_ = skipIfAccessDenied(st, op, acct.ID, region, err)
			break
		}
		if err != nil {
			return 0, 0, fmt.Errorf("%s: %w", op, err)
		}
		for _, l := range out.Licenses {
			arn := sv(l.LicenseArn)
			// Status is the issuer's; a license its issuer deleted is gone for
			// every receiver whatever this account's received status says.
			if arn == "" || l.Status == lmtypes.LicenseStatusDeleted {
				continue
			}
			var received string
			if l.ReceivedMetadata != nil {
				received = string(l.ReceivedMetadata.ReceivedStatus)
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeLicenseManagerReceivedLicense, NativeID: lmReceivedLicenseNativeID(arn),
				Name: l.LicenseName, Region: regionGlobal, Status: nonEmptyPtr(received),
				AttributesJSON: mustJSON(l), DiscoveredBy: scanID,
			})
		}
		// ListReceivedLicenses has no paginator; a repeated token would loop forever.
		var done bool
		if done, pageErr = lastPage(input.NextToken, out.NextToken); done {
			break
		}
		input.NextToken = out.NextToken
	}
	return upsertListed(st, batch, op, pageErr)
}

// lmTokenDeleted is the TokenData.Status of a deleted token; the model
// documents AVAILABLE and DELETED but types the field as a plain string.
const lmTokenDeleted = "DELETED"

// scanLMTokens stores the account's license tokens. ListTokens returns token
// metadata only; the JWT itself is returned once, by CreateToken. A DELETED
// token is skipped. A denial on a later page keeps the rows of the pages
// already read.
func scanLMTokens(ctx context.Context, client licenseManagerAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	const op = "license-manager:ListTokens"
	var (
		batch   []*store.Resource
		pageErr error
	)
	input := &licensemanager.ListTokensInput{MaxResults: sdkaws.Int32(lmPageSize)}
	for {
		out, err := client.ListTokens(ctx, input)
		// No isLicenseManagerNotSetUp check, unlike the sibling phases: every earlier
		// phase already succeeded, so a not-set-up error here can only be a partial
		// IAM denial, and marking the service disabled would discard their counts.
		if isAccessDenied(err) {
			_ = skipIfAccessDenied(st, op, acct.ID, region, err)
			break
		}
		if err != nil {
			return 0, 0, fmt.Errorf("%s: %w", op, err)
		}
		for _, tok := range out.Tokens {
			id, licenseArn := sv(tok.TokenId), sv(tok.LicenseArn)
			if id == "" || licenseArn == "" || sv(tok.Status) == lmTokenDeleted {
				continue
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeLicenseManagerToken, NativeID: lmTokenNativeID(licenseArn, id),
				Region: regionGlobal, Status: nonEmptyPtr(sv(tok.Status)),
				AttributesJSON: mustJSON(tok), DiscoveredBy: scanID,
			})
		}
		// ListTokens has no paginator; a repeated token would loop forever.
		var done bool
		if done, pageErr = lastPage(input.NextToken, out.NextToken); done {
			break
		}
		input.NextToken = out.NextToken
	}
	return upsertListed(st, batch, op, pageErr)
}

func scanLMLicenseConfigurations(ctx context.Context, client licenseManagerAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var batch []*store.Resource
	var nextToken *string
	for {
		out, err := client.ListLicenseConfigurations(ctx, &licensemanager.ListLicenseConfigurationsInput{NextToken: nextToken})
		if err != nil {
			if isLicenseManagerNotSetUp(err) {
				return 0, 0, markServiceDisabled(err)
			}
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "license-manager:ListLicenseConfigurations", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("license-manager:ListLicenseConfigurations: %w", err)
		}
		for _, c := range out.LicenseConfigurations {
			arn := sv(c.LicenseConfigurationArn)
			if arn == "" {
				continue
			}
			status := sv(c.Status)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeLicenseManagerLicenseConfiguration, NativeID: arn,
				Name: c.Name, Region: regionGlobal, Status: &status,
				AttributesJSON: mustJSON(c), DiscoveredBy: scanID,
			})
		}
		if out.NextToken == nil || *out.NextToken == "" {
			break
		}
		nextToken = out.NextToken
	}
	return upsertBatch(st, batch, "license-manager license-configurations")
}

func scanLMReportGenerators(ctx context.Context, client licenseManagerAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var batch []*store.Resource
	var nextToken *string
	for {
		out, err := client.ListLicenseManagerReportGenerators(ctx, &licensemanager.ListLicenseManagerReportGeneratorsInput{NextToken: nextToken})
		if err != nil {
			if isLicenseManagerNotSetUp(err) {
				return 0, 0, markServiceDisabled(err)
			}
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "license-manager:ListLicenseManagerReportGenerators", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("license-manager:ListLicenseManagerReportGenerators: %w", err)
		}
		for _, g := range out.ReportGenerators {
			arn := sv(g.LicenseManagerReportGeneratorArn)
			if arn == "" {
				continue
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeLicenseManagerReportGenerator, NativeID: arn,
				Name: g.ReportGeneratorName, Region: regionGlobal,
				AttributesJSON: mustJSON(g), DiscoveredBy: scanID,
			})
		}
		if out.NextToken == nil || *out.NextToken == "" {
			break
		}
		nextToken = out.NextToken
	}
	return upsertBatch(st, batch, "license-manager report-generators")
}

func scanLMLicenseAssetGroups(ctx context.Context, client licenseManagerAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var batch []*store.Resource
	var nextToken *string
	for {
		out, err := client.ListLicenseAssetGroups(ctx, &licensemanager.ListLicenseAssetGroupsInput{NextToken: nextToken})
		if err != nil {
			if isLicenseManagerNotSetUp(err) {
				return 0, 0, markServiceDisabled(err)
			}
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "license-manager:ListLicenseAssetGroups", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("license-manager:ListLicenseAssetGroups: %w", err)
		}
		for _, g := range out.LicenseAssetGroups {
			arn := sv(g.LicenseAssetGroupArn)
			if arn == "" {
				continue
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeLicenseManagerLicenseAssetGroup, NativeID: arn,
				Name: g.Name, Region: regionGlobal,
				AttributesJSON: mustJSON(g), DiscoveredBy: scanID,
			})
		}
		if out.NextToken == nil || *out.NextToken == "" {
			break
		}
		nextToken = out.NextToken
	}
	return upsertBatch(st, batch, "license-manager license-asset-groups")
}

func scanLMLicenseAssetRulesets(ctx context.Context, client licenseManagerAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var batch []*store.Resource
	var nextToken *string
	for {
		out, err := client.ListLicenseAssetRulesets(ctx, &licensemanager.ListLicenseAssetRulesetsInput{NextToken: nextToken})
		if err != nil {
			if isLicenseManagerNotSetUp(err) {
				return 0, 0, markServiceDisabled(err)
			}
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "license-manager:ListLicenseAssetRulesets", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("license-manager:ListLicenseAssetRulesets: %w", err)
		}
		for _, rs := range out.LicenseAssetRulesets {
			arn := sv(rs.LicenseAssetRulesetArn)
			if arn == "" {
				continue
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeLicenseManagerLicenseAssetRuleset, NativeID: arn,
				Name: rs.Name, Region: regionGlobal,
				AttributesJSON: mustJSON(rs), DiscoveredBy: scanID,
			})
		}
		if out.NextToken == nil || *out.NextToken == "" {
			break
		}
		nextToken = out.NextToken
	}
	return upsertBatch(st, batch, "license-manager license-asset-rulesets")
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

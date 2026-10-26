package aws

import (
	"context"
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
// licenses and tokens. Global service; called once, in its home region
// (homeRegion; us-east-1 for aws), to avoid duplicate scans across regions.
func scanLicenseManager(ctx context.Context, acct *account, _ string, st *store.Store, scanID string) (total, inserted int, err error) {
	region, err := acct.homeRegion("licensemanager")
	if err != nil {
		return 0, 0, err
	}
	// The SDK neither lists RateLimitExceededException, License Manager's HTTP
	// 429 throttle, among its throttle codes nor retries a 429 by status, so
	// without this a single throttle fails the call unretried. It gets standard
	// backoff only: adaptive mode's client-side rate limiting does not learn
	// from a code added this way.
	client := licensemanager.NewFromConfig(acct.cfg, func(o *licensemanager.Options) {
		o.Region = region
		o.Retryer = retry.AddWithErrorCodes(o.Retryer, "RateLimitExceededException")
	})

	// Every phase runs whatever the others returned. A "Service role not
	// found" denial means License Manager is not set up. It is reported as the
	// disabled sentinel, which discards the scan's counts, only when nothing
	// was stored and no phase failed otherwise; else each such phase is warned
	// about as the partial IAM denial it then is.
	var errs, notSetUp phaseErrs
	for _, phase := range []struct {
		op  string
		run func() (int, int, error)
	}{
		{lmListLicensesOp, func() (int, int, error) { return scanLMLicenses(ctx, client, acct, region, st, scanID) }},
		{lmListGrantsOp, func() (int, int, error) { return scanLMGrants(ctx, client, acct, region, st, scanID) }},
		{lmListConfigurationsOp, func() (int, int, error) {
			return scanLMLicenseConfigurations(ctx, client, acct, region, st, scanID)
		}},
		{lmListReportGeneratorsOp, func() (int, int, error) {
			return scanLMReportGenerators(ctx, client, acct, region, st, scanID)
		}},
		{lmListAssetGroupsOp, func() (int, int, error) {
			return scanLMLicenseAssetGroups(ctx, client, acct, region, st, scanID)
		}},
		{lmListAssetRulesetsOp, func() (int, int, error) {
			return scanLMLicenseAssetRulesets(ctx, client, acct, region, st, scanID)
		}},
		{lmListReceivedLicensesOp, func() (int, int, error) {
			return scanLMReceivedLicenses(ctx, client, acct, region, st, scanID)
		}},
		{lmListTokensOp, func() (int, int, error) { return scanLMTokens(ctx, client, acct, region, st, scanID) }},
	} {
		t, i, ferr := phase.run()
		total += t
		inserted += i
		if isLicenseManagerNotSetUp(ferr) {
			notSetUp.add(phase.op, ferr)
			continue
		}
		errs.add(phase.op, ferr)
	}
	if len(notSetUp) > 0 && total == 0 && len(errs) == 0 {
		return 0, 0, markServiceDisabled(notSetUp[0].err)
	}
	for _, n := range notSetUp {
		_ = skipIfAccessDenied(st, n.op, acct.ID, region, n.err)
	}
	return total, inserted, errs.join(st, acct, region)
}

const (
	lmListLicensesOp         = "license-manager:ListLicenses"
	lmListGrantsOp           = "license-manager:ListDistributedGrants"
	lmListConfigurationsOp   = "license-manager:ListLicenseConfigurations"
	lmListReportGeneratorsOp = "license-manager:ListLicenseManagerReportGenerators"
	lmListAssetGroupsOp      = "license-manager:ListLicenseAssetGroups"
	lmListAssetRulesetsOp    = "license-manager:ListLicenseAssetRulesets"
	lmListReceivedLicensesOp = "license-manager:ListReceivedLicenses"
	lmListTokensOp           = "license-manager:ListTokens"
)

// lmListPhase stores one License Manager listing. page reads the page after
// token and returns its rows and the next token. The pages read before an
// error are stored. A "Service role not found" denial on the first page is
// returned for scanLicenseManager to judge; any other denial warns. Any other
// error, and a repeated token, is returned with the stored pages' counts.
func lmListPhase(st *store.Store, acct *account, region, op string, page func(token *string) ([]*store.Resource, *string, error)) (int, int, error) {
	var (
		batch   []*store.Resource
		token   *string
		listErr error
	)
	for {
		rows, next, err := page(token)
		if token == nil && isLicenseManagerNotSetUp(err) {
			return 0, 0, err
		}
		if isAccessDenied(err) {
			_ = skipIfAccessDenied(st, op, acct.ID, region, err)
			break
		}
		if err != nil {
			listErr = err
			break
		}
		batch = append(batch, rows...)
		// No License Manager list op has a paginator; a repeated token would
		// loop forever.
		var done bool
		if done, listErr = lastPage(token, next); done {
			break
		}
		token = next
	}
	return upsertListed(st, batch, op, listErr)
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
// row status. A DELETED license is skipped.
func scanLMReceivedLicenses(ctx context.Context, client licenseManagerAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var arnless skipTally
	total, inserted, err := lmListPhase(st, acct, region, lmListReceivedLicensesOp, func(token *string) ([]*store.Resource, *string, error) {
		out, err := client.ListReceivedLicenses(ctx, &licensemanager.ListReceivedLicensesInput{MaxResults: sdkaws.Int32(lmPageSize), NextToken: token})
		if err != nil {
			return nil, nil, err
		}
		var rows []*store.Resource
		for _, l := range out.Licenses {
			// Status is the issuer's; a license its issuer deleted is gone for
			// every receiver whatever this account's received status says.
			if l.Status == lmtypes.LicenseStatusDeleted {
				continue
			}
			arn := sv(l.LicenseArn)
			if arn == "" {
				arnless.skipped(fmt.Sprintf("%q", sv(l.LicenseName)), nil)
				continue
			}
			var received string
			if l.ReceivedMetadata != nil {
				received = string(l.ReceivedMetadata.ReceivedStatus)
			}
			rows = append(rows, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeLicenseManagerReceivedLicense, NativeID: lmReceivedLicenseNativeID(arn),
				Name: l.LicenseName, Region: regionGlobal, Status: nonEmptyPtr(received),
				AttributesJSON: mustJSON(l), DiscoveredBy: scanID,
			})
		}
		return rows, out.NextToken, nil
	})
	warnNotStored(st, lmListReceivedLicensesOp, acct, region, &arnless, "received licenses have no LicenseArn")
	return total, inserted, err
}

// lmTokenDeleted is the TokenData.Status of a deleted token; the model
// documents AVAILABLE and DELETED but types the field as a plain string.
const lmTokenDeleted = "DELETED"

// scanLMTokens stores the account's license tokens. ListTokens returns token
// metadata only; the JWT itself is returned once, by CreateToken. A DELETED
// token is skipped.
func scanLMTokens(ctx context.Context, client licenseManagerAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var unkeyed skipTally
	total, inserted, err := lmListPhase(st, acct, region, lmListTokensOp, func(token *string) ([]*store.Resource, *string, error) {
		out, err := client.ListTokens(ctx, &licensemanager.ListTokensInput{MaxResults: sdkaws.Int32(lmPageSize), NextToken: token})
		if err != nil {
			return nil, nil, err
		}
		var rows []*store.Resource
		for _, tok := range out.Tokens {
			if sv(tok.Status) == lmTokenDeleted {
				continue
			}
			id, licenseArn := sv(tok.TokenId), sv(tok.LicenseArn)
			if id == "" || licenseArn == "" {
				unkeyed.skipped(fmt.Sprintf("token %q of license %q", id, licenseArn), nil)
				continue
			}
			rows = append(rows, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeLicenseManagerToken, NativeID: lmTokenNativeID(licenseArn, id),
				Region: regionGlobal, Status: nonEmptyPtr(sv(tok.Status)),
				AttributesJSON: mustJSON(tok), DiscoveredBy: scanID,
			})
		}
		return rows, out.NextToken, nil
	})
	warnNotStored(st, lmListTokensOp, acct, region, &unkeyed, "tokens have no TokenId or LicenseArn")
	return total, inserted, err
}

// lmAppendARNRow appends the row of a listed item keyed by its ARN, or tallies
// the item under its name in arnless when it has no ARN.
func lmAppendARNRow(rows []*store.Resource, acct *account, scanID, rtype string, arn, name, status *string, attrs any, arnless *skipTally) []*store.Resource {
	if sv(arn) == "" {
		arnless.skipped(fmt.Sprintf("%q", sv(name)), nil)
		return rows
	}
	return append(rows, &store.Resource{
		Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
		Type: rtype, NativeID: *arn,
		Name: name, Region: regionGlobal, Status: status,
		AttributesJSON: mustJSON(attrs), DiscoveredBy: scanID,
	})
}

// scanLMARNList is lmListPhase for a listing whose items are keyed by their
// ARN. list reads the page after token and tallies the items without an ARN
// in arnless; they are warned about once, naming kind.
func scanLMARNList(st *store.Store, acct *account, region, op, kind string, list func(token *string, arnless *skipTally) ([]*store.Resource, *string, error)) (int, int, error) {
	var arnless skipTally
	total, inserted, err := lmListPhase(st, acct, region, op, func(token *string) ([]*store.Resource, *string, error) {
		return list(token, &arnless)
	})
	warnNotStored(st, op, acct, region, &arnless, kind+" have no ARN")
	return total, inserted, err
}

func scanLMLicenses(ctx context.Context, client licenseManagerAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	return scanLMARNList(st, acct, region, lmListLicensesOp, "licenses", func(token *string, arnless *skipTally) ([]*store.Resource, *string, error) {
		out, err := client.ListLicenses(ctx, &licensemanager.ListLicensesInput{NextToken: token})
		if err != nil {
			return nil, nil, err
		}
		var rows []*store.Resource
		for _, l := range out.Licenses {
			status := string(l.Status)
			rows = lmAppendARNRow(rows, acct, scanID, TypeLicenseManagerLicense, l.LicenseArn, l.LicenseName, &status, l, arnless)
		}
		return rows, out.NextToken, nil
	})
}

func scanLMGrants(ctx context.Context, client licenseManagerAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	return scanLMARNList(st, acct, region, lmListGrantsOp, "grants", func(token *string, arnless *skipTally) ([]*store.Resource, *string, error) {
		out, err := client.ListDistributedGrants(ctx, &licensemanager.ListDistributedGrantsInput{NextToken: token})
		if err != nil {
			return nil, nil, err
		}
		var rows []*store.Resource
		for _, g := range out.Grants {
			status := string(g.GrantStatus)
			rows = lmAppendARNRow(rows, acct, scanID, TypeLicenseManagerGrant, g.GrantArn, g.GrantName, &status, g, arnless)
		}
		return rows, out.NextToken, nil
	})
}

func scanLMLicenseConfigurations(ctx context.Context, client licenseManagerAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	return scanLMARNList(st, acct, region, lmListConfigurationsOp, "license configurations", func(token *string, arnless *skipTally) ([]*store.Resource, *string, error) {
		out, err := client.ListLicenseConfigurations(ctx, &licensemanager.ListLicenseConfigurationsInput{NextToken: token})
		if err != nil {
			return nil, nil, err
		}
		var rows []*store.Resource
		for _, c := range out.LicenseConfigurations {
			status := sv(c.Status)
			rows = lmAppendARNRow(rows, acct, scanID, TypeLicenseManagerLicenseConfiguration, c.LicenseConfigurationArn, c.Name, &status, c, arnless)
		}
		return rows, out.NextToken, nil
	})
}

func scanLMReportGenerators(ctx context.Context, client licenseManagerAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	return scanLMARNList(st, acct, region, lmListReportGeneratorsOp, "report generators", func(token *string, arnless *skipTally) ([]*store.Resource, *string, error) {
		out, err := client.ListLicenseManagerReportGenerators(ctx, &licensemanager.ListLicenseManagerReportGeneratorsInput{NextToken: token})
		if err != nil {
			return nil, nil, err
		}
		var rows []*store.Resource
		for _, g := range out.ReportGenerators {
			rows = lmAppendARNRow(rows, acct, scanID, TypeLicenseManagerReportGenerator, g.LicenseManagerReportGeneratorArn, g.ReportGeneratorName, nil, g, arnless)
		}
		return rows, out.NextToken, nil
	})
}

func scanLMLicenseAssetGroups(ctx context.Context, client licenseManagerAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	return scanLMARNList(st, acct, region, lmListAssetGroupsOp, "license asset groups", func(token *string, arnless *skipTally) ([]*store.Resource, *string, error) {
		out, err := client.ListLicenseAssetGroups(ctx, &licensemanager.ListLicenseAssetGroupsInput{NextToken: token})
		if err != nil {
			return nil, nil, err
		}
		var rows []*store.Resource
		for _, g := range out.LicenseAssetGroups {
			rows = lmAppendARNRow(rows, acct, scanID, TypeLicenseManagerLicenseAssetGroup, g.LicenseAssetGroupArn, g.Name, nil, g, arnless)
		}
		return rows, out.NextToken, nil
	})
}

func scanLMLicenseAssetRulesets(ctx context.Context, client licenseManagerAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	return scanLMARNList(st, acct, region, lmListAssetRulesetsOp, "license asset rulesets", func(token *string, arnless *skipTally) ([]*store.Resource, *string, error) {
		out, err := client.ListLicenseAssetRulesets(ctx, &licensemanager.ListLicenseAssetRulesetsInput{NextToken: token})
		if err != nil {
			return nil, nil, err
		}
		var rows []*store.Resource
		for _, rs := range out.LicenseAssetRulesets {
			rows = lmAppendARNRow(rows, acct, scanID, TypeLicenseManagerLicenseAssetRuleset, rs.LicenseAssetRulesetArn, rs.Name, nil, rs, arnless)
		}
		return rows, out.NextToken, nil
	})
}

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
			{Service: "license-manager", DiscoType: TypeLicenseManagerLicenseConfiguration, Leaf: true},
			{Service: "license-manager", DiscoType: TypeLicenseManagerReportGenerator, Leaf: true},
			{Service: "license-manager", DiscoType: TypeLicenseManagerLicenseAssetGroup, Leaf: true},
			{Service: "license-manager", DiscoType: TypeLicenseManagerLicenseAssetRuleset, Leaf: true},
		},
	})
}

type licenseManagerAPI interface {
	ListLicenses(context.Context, *licensemanager.ListLicensesInput, ...func(*licensemanager.Options)) (*licensemanager.ListLicensesOutput, error)
	ListDistributedGrants(context.Context, *licensemanager.ListDistributedGrantsInput, ...func(*licensemanager.Options)) (*licensemanager.ListDistributedGrantsOutput, error)
	ListLicenseConfigurations(context.Context, *licensemanager.ListLicenseConfigurationsInput, ...func(*licensemanager.Options)) (*licensemanager.ListLicenseConfigurationsOutput, error)
	ListLicenseManagerReportGenerators(context.Context, *licensemanager.ListLicenseManagerReportGeneratorsInput, ...func(*licensemanager.Options)) (*licensemanager.ListLicenseManagerReportGeneratorsOutput, error)
	ListLicenseAssetGroups(context.Context, *licensemanager.ListLicenseAssetGroupsInput, ...func(*licensemanager.Options)) (*licensemanager.ListLicenseAssetGroupsOutput, error)
	ListLicenseAssetRulesets(context.Context, *licensemanager.ListLicenseAssetRulesetsInput, ...func(*licensemanager.Options)) (*licensemanager.ListLicenseAssetRulesetsOutput, error)
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
	return total, inserted, nil
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

package aws

import (
	"context"
	"fmt"
	"strings"

	"codeberg.org/icearp/disco/internal/store"
	"github.com/aws/aws-sdk-go-v2/service/inspector2"
)

// isInspector2FeatureNotEnabled disambiguates the "Invoking account is not
// enabled" feature-gate state on Inspector v2 sub-phases (CIS scans, code
// security) from a real IAM denial.
func isInspector2FeatureNotEnabled(err error) bool {
	return isAccessDenied(err) && strings.Contains(err.Error(), "Invoking account is not enabled")
}

// scanInspector2Extended discovers Inspector v2 CIS scan configurations,
// code security integrations, and code security scan configurations. ARNs
// native on every type.
func scanInspector2Extended(ctx context.Context, client inspector2API, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	t, i, ferr := scanInspector2CisConfigs(ctx, client, acct, region, st, scanID)
	if ferr != nil {
		return total, inserted, ferr
	}
	total += t
	inserted += i

	t, i, ferr = scanInspector2CodeSecurityIntegrations(ctx, client, acct, region, st, scanID)
	if ferr != nil {
		return total, inserted, ferr
	}
	total += t
	inserted += i

	t, i, ferr = scanInspector2CodeSecurityScanConfigs(ctx, client, acct, region, st, scanID)
	if ferr != nil {
		return total, inserted, ferr
	}
	total += t
	inserted += i
	return total, inserted, nil
}

func scanInspector2CisConfigs(ctx context.Context, client inspector2API, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := inspector2.NewListCisScanConfigurationsPaginator(client, &inspector2.ListCisScanConfigurationsInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isInspector2FeatureNotEnabled(err) {
				return 0, 0, markServiceDisabled(err)
			}
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "inspector2:ListCisScanConfigurations", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("inspector2:ListCisScanConfigurations: %w", err)
		}
		for _, c := range out.ScanConfigurations {
			arn := sv(c.ScanConfigurationArn)
			if arn == "" {
				continue
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeInspector2CisScanConfiguration, NativeID: arn,
				Name: c.ScanName, Region: &region,
				AttributesJSON: mustJSON(c), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "inspector2 cis-scan-configurations")
}

func scanInspector2CodeSecurityIntegrations(ctx context.Context, client inspector2API, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var batch []*store.Resource
	var nextToken *string
	for {
		out, err := client.ListCodeSecurityIntegrations(ctx, &inspector2.ListCodeSecurityIntegrationsInput{NextToken: nextToken})
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "inspector2:ListCodeSecurityIntegrations", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("inspector2:ListCodeSecurityIntegrations: %w", err)
		}
		for _, c := range out.Integrations {
			arn := sv(c.IntegrationArn)
			if arn == "" {
				continue
			}
			status := string(c.Status)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeInspector2CodeSecurityIntegration, NativeID: arn,
				Name: c.Name, Region: &region, Status: &status,
				AttributesJSON: mustJSON(c), DiscoveredBy: scanID,
			})
		}
		if out.NextToken == nil || *out.NextToken == "" {
			break
		}
		nextToken = out.NextToken
	}
	return upsertBatch(st, batch, "inspector2 code-security-integrations")
}

func scanInspector2CodeSecurityScanConfigs(ctx context.Context, client inspector2API, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var batch []*store.Resource
	var nextToken *string
	for {
		out, err := client.ListCodeSecurityScanConfigurations(ctx, &inspector2.ListCodeSecurityScanConfigurationsInput{NextToken: nextToken})
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "inspector2:ListCodeSecurityScanConfigurations", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("inspector2:ListCodeSecurityScanConfigurations: %w", err)
		}
		for _, c := range out.Configurations {
			arn := sv(c.ScanConfigurationArn)
			if arn == "" {
				continue
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeInspector2CodeSecurityScanConfiguration, NativeID: arn,
				Name: c.Name, Region: &region,
				AttributesJSON: mustJSON(c), DiscoveredBy: scanID,
			})
		}
		if out.NextToken == nil || *out.NextToken == "" {
			break
		}
		nextToken = out.NextToken
	}
	return upsertBatch(st, batch, "inspector2 code-security-scan-configurations")
}

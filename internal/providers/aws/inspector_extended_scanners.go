package aws

import (
	"context"
	"fmt"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/inspector2"
	i2types "github.com/aws/aws-sdk-go-v2/service/inspector2/types"
	"github.com/icearp/disco-cli/store"
)

// isInspector2FeatureNotEnabled disambiguates the "Invoking account is not
// enabled" feature-gate state on Inspector v2 sub-phases (CIS scans, code
// security) from a real IAM denial.
func isInspector2FeatureNotEnabled(err error) bool {
	return isAccessDeniedWithMessage(err, "Invoking account is not enabled")
}

// scanInspector2CisConfigs stores the CIS scan configurations
// ListCisScanConfigurations returns, those read before a failed page
// included. A first page refused with the not-enabled denial returns the
// service-disabled sentinel; on a later page the same denial is warned about
// like any other, keeping the pages read before it.
func scanInspector2CisConfigs(ctx context.Context, client inspector2API, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var notEnabled error
	total, inserted, err := storeTokenList(ctx, st, acct, region, inspector2CisConfigsOp,
		func(ctx context.Context, token *string) ([]i2types.CisScanConfiguration, *string, error) {
			out, err := client.ListCisScanConfigurations(ctx, &inspector2.ListCisScanConfigurationsInput{NextToken: token, MaxResults: sdkaws.Int32(inspector2PageLimit)})
			if token == nil && isInspector2FeatureNotEnabled(err) {
				notEnabled = err
				return nil, nil, nil
			}
			if err != nil {
				return nil, nil, err
			}
			return out.ScanConfigurations, out.NextToken, nil
		},
		func(c i2types.CisScanConfiguration) (*store.Resource, string, string) {
			if sv(c.ScanConfigurationArn) == "" {
				return nil, "CIS scan configurations have an empty ScanConfigurationArn", sv(c.ScanName)
			}
			return &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeInspector2CisScanConfiguration, NativeID: *c.ScanConfigurationArn,
				Name: c.ScanName, Region: &region,
				TagsJSON: mapTagsJSON(c.Tags), AttributesJSON: mustJSON(c), DiscoveredBy: scanID,
			}, "", ""
		})
	if notEnabled != nil {
		return total, inserted, markServiceDisabled(notEnabled)
	}
	return total, inserted, err
}

func scanInspector2CodeSecurityIntegrations(ctx context.Context, client inspector2API, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var batch []*store.Resource
	var nextToken *string
	for {
		out, err := client.ListCodeSecurityIntegrations(ctx, &inspector2.ListCodeSecurityIntegrationsInput{NextToken: nextToken})
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, inspector2CodeSecurityIntegrationsOp, acct.ID, region, err)
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
				return 0, 0, skipIfAccessDenied(st, inspector2CodeSecurityScanConfigsOp, acct.ID, region, err)
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

// scanInspector2Connectors lists Inspector v2 multicloud connectors (e.g. the
// Azure tenant/subscription connectors Inspector scans through). A connector
// carries scope and health, no credential material, so no Redact rule.
func scanInspector2Connectors(ctx context.Context, client inspector2API, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := inspector2.NewListConnectorsPaginator(client, &inspector2.ListConnectorsInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				_ = skipIfAccessDenied(st, inspector2ConnectorsOp, acct.ID, region, err)
				break
			}
			return 0, 0, fmt.Errorf("inspector2:ListConnectors: %w", err)
		}
		for _, c := range out.Items {
			arn := sv(c.ConnectorArn)
			if arn == "" {
				continue
			}
			r := &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeInspector2Connector, NativeID: arn,
				Name: c.Name, Region: &region, CreatedAt: tp(c.CreatedAt),
				TagsJSON: mapTagsJSON(c.Tags), AttributesJSON: mustJSON(c), DiscoveredBy: scanID,
			}
			if status := string(c.EnablementStatus); status != "" {
				r.Status = &status
			}
			batch = append(batch, r)
		}
	}
	return upsertBatch(st, batch, "inspector2 connectors")
}

// inspector2DelegatedAdminNativeID synthesises an identifier for a delegated
// administrator row: Inspector v2 issues no ARN for it. It is keyed by the
// listing account, mirroring the Detective organization-admin shape.
func inspector2DelegatedAdminNativeID(partition, region, listingAccountID, adminAccountID string) string {
	return arnFor(partition, "inspector2", region, listingAccountID, "delegated-admin/"+adminAccountID)
}

// scanInspector2DelegatedAdmins lists the organization's Inspector v2
// delegated administrator in one region (the designation is per region).
// GetDelegatedAdminAccount is not called: it answers from member accounts
// too, so every scanned member would store the same administrator under its
// own NativeID. Which error ListDelegatedAdminAccounts returns to an account
// that may not call it is unverified live, so a denial warns like any other.
func scanInspector2DelegatedAdmins(ctx context.Context, client inspector2API, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := inspector2.NewListDelegatedAdminAccountsPaginator(client, &inspector2.ListDelegatedAdminAccountsInput{},
		func(o *inspector2.ListDelegatedAdminAccountsPaginatorOptions) { o.Limit = 5 })
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			// scanInspector2CisConfigs reports a not-enabled account through
			// the service-disabled sentinel; a warning here would be an
			// IAM-style warning no policy change can fix.
			if isInspector2FeatureNotEnabled(err) {
				break
			}
			if isAccessDenied(err) {
				_ = skipIfAccessDenied(st, inspector2DelegatedAdminsOp, acct.ID, region, err)
				break
			}
			return 0, 0, fmt.Errorf("inspector2:ListDelegatedAdminAccounts: %w", err)
		}
		for _, a := range out.DelegatedAdminAccounts {
			adminID := sv(a.AccountId)
			if adminID == "" {
				continue
			}
			r := &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type:     TypeInspector2DelegatedAdminAccount,
				NativeID: inspector2DelegatedAdminNativeID(acct.partition(), region, acct.ID, adminID),
				Name:     &adminID, Region: &region,
				AttributesJSON: mustJSON(a), DiscoveredBy: scanID,
			}
			if status := string(a.Status); status != "" {
				r.Status = &status
			}
			batch = append(batch, r)
		}
	}
	return upsertBatch(st, batch, "inspector2 delegated-admin-accounts")
}

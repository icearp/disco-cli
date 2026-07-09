package aws

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/restype"
	"codeberg.org/icearp/disco/store"
	"github.com/aws/aws-sdk-go-v2/service/workspacesweb"
)

func init() {
	registerType(restype.Descriptor{Type: TypeWSWBrowserSettings, Service: "workspaces-web"})
	registerType(restype.Descriptor{Type: TypeWSWDataProtectionSettings, Service: "workspaces-web"})
	registerType(restype.Descriptor{Type: TypeWSWIdentityProvider, Service: "workspaces-web"})
	registerType(restype.Descriptor{Type: TypeWSWIPAccessSettings, Service: "workspaces-web"})
	registerType(restype.Descriptor{Type: TypeWSWNetworkSettings, Service: "workspaces-web"})
	registerType(restype.Descriptor{Type: TypeWSWPortal, Service: "workspaces-web"})
	registerType(restype.Descriptor{Type: TypeWSWSessionLogger, Service: "workspaces-web"})
	registerType(restype.Descriptor{Type: TypeWSWTrustStore, Service: "workspaces-web", Leaf: true})
	registerType(restype.Descriptor{Type: TypeWSWUserAccessLoggingSettings, Service: "workspaces-web"})
	registerType(restype.Descriptor{Type: TypeWSWUserSettings, Service: "workspaces-web"})
	registerService(serviceEntry{
		name: "aws:workspaces-web",
		fn:   scanWorkSpacesWeb,
	})
}

type wswAPI interface {
	ListBrowserSettings(context.Context, *workspacesweb.ListBrowserSettingsInput, ...func(*workspacesweb.Options)) (*workspacesweb.ListBrowserSettingsOutput, error)
	ListDataProtectionSettings(context.Context, *workspacesweb.ListDataProtectionSettingsInput, ...func(*workspacesweb.Options)) (*workspacesweb.ListDataProtectionSettingsOutput, error)
	ListIdentityProviders(context.Context, *workspacesweb.ListIdentityProvidersInput, ...func(*workspacesweb.Options)) (*workspacesweb.ListIdentityProvidersOutput, error)
	ListIpAccessSettings(context.Context, *workspacesweb.ListIpAccessSettingsInput, ...func(*workspacesweb.Options)) (*workspacesweb.ListIpAccessSettingsOutput, error)
	ListNetworkSettings(context.Context, *workspacesweb.ListNetworkSettingsInput, ...func(*workspacesweb.Options)) (*workspacesweb.ListNetworkSettingsOutput, error)
	ListPortals(context.Context, *workspacesweb.ListPortalsInput, ...func(*workspacesweb.Options)) (*workspacesweb.ListPortalsOutput, error)
	ListSessionLoggers(context.Context, *workspacesweb.ListSessionLoggersInput, ...func(*workspacesweb.Options)) (*workspacesweb.ListSessionLoggersOutput, error)
	ListTrustStores(context.Context, *workspacesweb.ListTrustStoresInput, ...func(*workspacesweb.Options)) (*workspacesweb.ListTrustStoresOutput, error)
	ListUserAccessLoggingSettings(context.Context, *workspacesweb.ListUserAccessLoggingSettingsInput, ...func(*workspacesweb.Options)) (*workspacesweb.ListUserAccessLoggingSettingsOutput, error)
	ListUserSettings(context.Context, *workspacesweb.ListUserSettingsInput, ...func(*workspacesweb.Options)) (*workspacesweb.ListUserSettingsOutput, error)
}

func scanWorkSpacesWeb(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := workspacesweb.NewFromConfig(acct.cfg, func(o *workspacesweb.Options) { o.Region = region })

	// Phase 1: portals (collect ARNs for per-portal identity-provider fan-out).
	portalARNs, t, i, ferr := scanWSWPortals(ctx, client, acct, region, st, scanID)
	if ferr != nil {
		return total, inserted, ferr
	}
	total += t
	inserted += i

	// Per-portal phase: identity-providers.
	for _, pa := range portalARNs {
		t, i, perr := scanWSWIdentityProviders(ctx, client, acct, region, st, scanID, pa)
		if perr != nil {
			return total, inserted, perr
		}
		total += t
		inserted += i
	}

	// Top-level setting phases.
	for _, phase := range []func() (int, int, error){
		func() (int, int, error) { return scanWSWBrowserSettings(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanWSWDataProtectionSettings(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanWSWIPAccessSettings(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanWSWNetworkSettings(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanWSWSessionLoggers(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanWSWTrustStores(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) {
			return scanWSWUserAccessLoggingSettings(ctx, client, acct, region, st, scanID)
		},
		func() (int, int, error) { return scanWSWUserSettings(ctx, client, acct, region, st, scanID) },
	} {
		t, i, perr := phase()
		if perr != nil {
			return total, inserted, perr
		}
		total += t
		inserted += i
	}
	return total, inserted, nil
}

func scanWSWPortals(ctx context.Context, client wswAPI, acct *account, region string, st *store.Store, scanID string) ([]string, int, int, error) {
	pager := workspacesweb.NewListPortalsPaginator(client, &workspacesweb.ListPortalsInput{})
	var batch []*store.Resource
	var arns []string
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "workspaces-web:ListPortals", acct.ID, region, perr)
				return nil, 0, 0, nil
			}
			return nil, 0, 0, fmt.Errorf("workspaces-web:ListPortals: %w", perr)
		}
		for _, p := range out.Portals {
			arn := sv(p.PortalArn)
			if arn == "" {
				continue
			}
			label := sv(p.DisplayName)
			if label == "" {
				label = arn
			}
			arns = append(arns, arn)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeWSWPortal, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(p), DiscoveredBy: scanID,
			})
		}
	}
	t, i, err := upsertBatch(st, batch, "workspaces-web portals")
	return arns, t, i, err
}

func scanWSWIdentityProviders(ctx context.Context, client wswAPI, acct *account, region string, st *store.Store, scanID, portalARN string) (int, int, error) {
	pa := portalARN
	pager := workspacesweb.NewListIdentityProvidersPaginator(client, &workspacesweb.ListIdentityProvidersInput{PortalArn: &pa})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "workspaces-web:ListIdentityProviders", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("workspaces-web:ListIdentityProviders: %w", perr)
		}
		for _, ip := range out.IdentityProviders {
			arn := sv(ip.IdentityProviderArn)
			if arn == "" {
				continue
			}
			label := sv(ip.IdentityProviderName)
			if label == "" {
				label = arn
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeWSWIdentityProvider, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(ip), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "workspaces-web identity-providers")
}

func scanWSWBrowserSettings(ctx context.Context, client wswAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := workspacesweb.NewListBrowserSettingsPaginator(client, &workspacesweb.ListBrowserSettingsInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "workspaces-web:ListBrowserSettings", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("workspaces-web:ListBrowserSettings: %w", perr)
		}
		for _, b := range out.BrowserSettings {
			arn := sv(b.BrowserSettingsArn)
			if arn == "" {
				continue
			}
			label := arn
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeWSWBrowserSettings, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(b), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "workspaces-web browser-settings")
}

func scanWSWDataProtectionSettings(ctx context.Context, client wswAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := workspacesweb.NewListDataProtectionSettingsPaginator(client, &workspacesweb.ListDataProtectionSettingsInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "workspaces-web:ListDataProtectionSettings", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("workspaces-web:ListDataProtectionSettings: %w", perr)
		}
		for _, d := range out.DataProtectionSettings {
			arn := sv(d.DataProtectionSettingsArn)
			if arn == "" {
				continue
			}
			label := sv(d.DisplayName)
			if label == "" {
				label = arn
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeWSWDataProtectionSettings, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(d), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "workspaces-web data-protection-settings")
}

func scanWSWIPAccessSettings(ctx context.Context, client wswAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := workspacesweb.NewListIpAccessSettingsPaginator(client, &workspacesweb.ListIpAccessSettingsInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "workspaces-web:ListIpAccessSettings", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("workspaces-web:ListIpAccessSettings: %w", perr)
		}
		for _, ip := range out.IpAccessSettings {
			arn := sv(ip.IpAccessSettingsArn)
			if arn == "" {
				continue
			}
			label := sv(ip.DisplayName)
			if label == "" {
				label = arn
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeWSWIPAccessSettings, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(ip), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "workspaces-web ip-access-settings")
}

func scanWSWNetworkSettings(ctx context.Context, client wswAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := workspacesweb.NewListNetworkSettingsPaginator(client, &workspacesweb.ListNetworkSettingsInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "workspaces-web:ListNetworkSettings", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("workspaces-web:ListNetworkSettings: %w", perr)
		}
		for _, n := range out.NetworkSettings {
			arn := sv(n.NetworkSettingsArn)
			if arn == "" {
				continue
			}
			label := arn
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeWSWNetworkSettings, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(n), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "workspaces-web network-settings")
}

func scanWSWSessionLoggers(ctx context.Context, client wswAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := workspacesweb.NewListSessionLoggersPaginator(client, &workspacesweb.ListSessionLoggersInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "workspaces-web:ListSessionLoggers", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("workspaces-web:ListSessionLoggers: %w", perr)
		}
		for _, sl := range out.SessionLoggers {
			arn := sv(sl.SessionLoggerArn)
			if arn == "" {
				continue
			}
			label := sv(sl.DisplayName)
			if label == "" {
				label = arn
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeWSWSessionLogger, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(sl), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "workspaces-web session-loggers")
}

func scanWSWTrustStores(ctx context.Context, client wswAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := workspacesweb.NewListTrustStoresPaginator(client, &workspacesweb.ListTrustStoresInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "workspaces-web:ListTrustStores", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("workspaces-web:ListTrustStores: %w", perr)
		}
		for _, ts := range out.TrustStores {
			arn := sv(ts.TrustStoreArn)
			if arn == "" {
				continue
			}
			label := arn
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeWSWTrustStore, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(ts), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "workspaces-web trust-stores")
}

func scanWSWUserAccessLoggingSettings(ctx context.Context, client wswAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := workspacesweb.NewListUserAccessLoggingSettingsPaginator(client, &workspacesweb.ListUserAccessLoggingSettingsInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "workspaces-web:ListUserAccessLoggingSettings", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("workspaces-web:ListUserAccessLoggingSettings: %w", perr)
		}
		for _, u := range out.UserAccessLoggingSettings {
			arn := sv(u.UserAccessLoggingSettingsArn)
			if arn == "" {
				continue
			}
			label := arn
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeWSWUserAccessLoggingSettings, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(u), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "workspaces-web user-access-logging-settings")
}

func scanWSWUserSettings(ctx context.Context, client wswAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := workspacesweb.NewListUserSettingsPaginator(client, &workspacesweb.ListUserSettingsInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "workspaces-web:ListUserSettings", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("workspaces-web:ListUserSettings: %w", perr)
		}
		for _, u := range out.UserSettings {
			arn := sv(u.UserSettingsArn)
			if arn == "" {
				continue
			}
			label := arn
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeWSWUserSettings, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(u), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "workspaces-web user-settings")
}

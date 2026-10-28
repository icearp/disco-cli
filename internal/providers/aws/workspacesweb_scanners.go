package aws

import (
	"cmp"
	"context"

	"github.com/aws/aws-sdk-go-v2/service/workspacesweb"
	wswtypes "github.com/aws/aws-sdk-go-v2/service/workspacesweb/types"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerType(restype.Descriptor{Type: TypeWSWBrowserSettings, Service: "workspaces-web"})
	registerType(restype.Descriptor{Type: TypeWSWDataProtectionSettings, Service: "workspaces-web"})
	registerType(restype.Descriptor{Type: TypeWSWIdentityProvider, Service: "workspaces-web"})
	registerType(restype.Descriptor{Type: TypeWSWIPAccessSettings, Service: "workspaces-web"})
	registerType(restype.Descriptor{Type: TypeWSWNetworkSettings, Service: "workspaces-web"})
	registerType(restype.Descriptor{Type: TypeWSWPortal, Service: "workspaces-web"})
	registerType(restype.Descriptor{Type: TypeWSWSessionLogger, Service: "workspaces-web"})
	registerType(restype.Descriptor{Type: TypeWSWTrustStore, Service: "workspaces-web"})
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
	ListTagsForResource(context.Context, *workspacesweb.ListTagsForResourceInput, ...func(*workspacesweb.Options)) (*workspacesweb.ListTagsForResourceOutput, error)
}

// wswTags reads a row's tags with ListTagsForResource, sent the row's
// NativeID, which is the ARN its listing returned. The op does not page.
// TagResource lists every type this scanner stores in the Service Reference,
// and every Create op of those types takes Tags.
func wswTags(client wswAPI) rowTagReader {
	return rowTagReader{
		op:       "workspaces-web:ListTagsForResource",
		notFound: []string{"ResourceNotFoundException"},
		read: func(ctx context.Context, r *store.Resource) (*string, error) {
			arn := r.NativeID
			out, err := client.ListTagsForResource(ctx, &workspacesweb.ListTagsForResourceInput{ResourceArn: &arn})
			if err != nil {
				return nil, err
			}
			return kvTagsJSON(out.Tags, func(t wswtypes.Tag) (*string, *string) { return t.Key, t.Value }), nil
		},
	}
}

// scanWorkSpacesWeb discovers WorkSpaces Secure Browser portals, their
// identity providers and the settings resources portals use. Every listing
// runs as its own phase: a phase's error does not stop the later ones; a
// failed write and ctx's end do (runHostPhases). Identity providers are
// listed per portal stored this scan, since their op needs a PortalArn.
//
// No listing is sent MaxResults: the model's MaxResults range trait sets only
// a minimum, and its documentation names no maximum.
func scanWorkSpacesWeb(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	client := workspacesweb.NewFromConfig(acct.cfg, func(o *workspacesweb.Options) { o.Region = region })
	tags := wswTags(client)
	var portals []childParent
	return runHostPhases(ctx, st, acct, region, "workspaces-web", 0, []hostPhase{
		{op: "workspaces-web:ListPortals", run: func(p *phaseTotals) (failed bool) {
			portals, failed = scanWSWPortals(ctx, client, acct, region, st, scanID, tags, p)
			return failed
		}},
		{op: "workspaces-web:ListIdentityProviders", run: func(p *phaseTotals) bool {
			return scanWSWIdentityProviders(ctx, client, acct, region, st, scanID, portals, tags, p)
		}},
		{op: "workspaces-web:ListBrowserSettings", run: func(p *phaseTotals) bool {
			return scanWSWBrowserSettings(ctx, client, acct, region, st, scanID, tags, p)
		}},
		{op: "workspaces-web:ListDataProtectionSettings", run: func(p *phaseTotals) bool {
			return scanWSWDataProtectionSettings(ctx, client, acct, region, st, scanID, tags, p)
		}},
		{op: "workspaces-web:ListIpAccessSettings", run: func(p *phaseTotals) bool {
			return scanWSWIPAccessSettings(ctx, client, acct, region, st, scanID, tags, p)
		}},
		{op: "workspaces-web:ListNetworkSettings", run: func(p *phaseTotals) bool {
			return scanWSWNetworkSettings(ctx, client, acct, region, st, scanID, tags, p)
		}},
		{op: "workspaces-web:ListSessionLoggers", run: func(p *phaseTotals) bool {
			return scanWSWSessionLoggers(ctx, client, acct, region, st, scanID, tags, p)
		}},
		{op: "workspaces-web:ListTrustStores", run: func(p *phaseTotals) bool {
			return scanWSWTrustStores(ctx, client, acct, region, st, scanID, tags, p)
		}},
		{op: "workspaces-web:ListUserAccessLoggingSettings", run: func(p *phaseTotals) bool {
			return scanWSWUserAccessLoggingSettings(ctx, client, acct, region, st, scanID, tags, p)
		}},
		{op: "workspaces-web:ListUserSettings", run: func(p *phaseTotals) bool {
			return scanWSWUserSettings(ctx, client, acct, region, st, scanID, tags, p)
		}},
	})
}

// wswRow is the row of a listed element whose NativeID is its ARN, named by
// name, or by its ARN when name is empty. An element without an ARN is not
// stored; what names the elements in the warning.
func wswRow(acct *account, region, scanID, typ, what, arn, name string, item any) (*store.Resource, string, string) {
	if arn == "" {
		return nil, what + " have no ARN", cmp.Or(name, "(an element with no identifier)")
	}
	label := cmp.Or(name, arn)
	return &store.Resource{
		Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
		Type: typ, NativeID: arn,
		Name: &label, Region: &region, AttributesJSON: mustJSON(item), DiscoveredBy: scanID,
	}, "", ""
}

// scanWSWPortals stores the portals with their tags and returns the stored
// ones, keyed by PortalArn, the parents of the identity-provider phase.
func scanWSWPortals(ctx context.Context, client wswAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) ([]childParent, bool) {
	return storeParentListTags(ctx, st, acct, region, "workspaces-web:ListPortals", "portals", "PortalArn",
		func(ctx context.Context, token *string) ([]wswtypes.PortalSummary, *string, error) {
			out, err := client.ListPortals(ctx, &workspacesweb.ListPortalsInput{NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.Portals, out.NextToken, nil
		},
		func(it wswtypes.PortalSummary) (*store.Resource, string, string) {
			return wswRow(acct, region, scanID, TypeWSWPortal, "portals", sv(it.PortalArn), sv(it.DisplayName), it)
		},
		func(it wswtypes.PortalSummary) string { return sv(it.PortalArn) },
		tags, p)
}

// scanWSWIdentityProviders lists the identity providers of each portal the
// portal phase stored and stores them with their tags (storeEachParentTags).
// ListIdentityProviders models no not-found code, so a portal deleted since
// it was listed fails its listing like any other error.
func scanWSWIdentityProviders(ctx context.Context, client wswAPI, acct *account, region string, st *store.Store, scanID string, portals []childParent, tags rowTagReader, p *phaseTotals) bool {
	_, failed := storeEachParentTags(ctx, st, acct, region, "workspaces-web:ListIdentityProviders", nil, portals,
		func(ctx context.Context, portal string, token *string) ([]wswtypes.IdentityProviderSummary, *string, error) {
			out, err := client.ListIdentityProviders(ctx, &workspacesweb.ListIdentityProvidersInput{PortalArn: &portal, NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.IdentityProviders, out.NextToken, nil
		},
		func(_ string, it wswtypes.IdentityProviderSummary) (*store.Resource, string, string) {
			return wswRow(acct, region, scanID, TypeWSWIdentityProvider, "identity providers", sv(it.IdentityProviderArn), sv(it.IdentityProviderName), it)
		}, tags, p)
	return failed
}

func scanWSWBrowserSettings(ctx context.Context, client wswAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) bool {
	return storeTokenListTags(ctx, st, acct, region, "workspaces-web:ListBrowserSettings",
		func(ctx context.Context, token *string) ([]wswtypes.BrowserSettingsSummary, *string, error) {
			out, err := client.ListBrowserSettings(ctx, &workspacesweb.ListBrowserSettingsInput{NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.BrowserSettings, out.NextToken, nil
		},
		func(it wswtypes.BrowserSettingsSummary) (*store.Resource, string, string) {
			return wswRow(acct, region, scanID, TypeWSWBrowserSettings, "browser settings", sv(it.BrowserSettingsArn), "", it)
		}, tags, p)
}

func scanWSWDataProtectionSettings(ctx context.Context, client wswAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) bool {
	return storeTokenListTags(ctx, st, acct, region, "workspaces-web:ListDataProtectionSettings",
		func(ctx context.Context, token *string) ([]wswtypes.DataProtectionSettingsSummary, *string, error) {
			out, err := client.ListDataProtectionSettings(ctx, &workspacesweb.ListDataProtectionSettingsInput{NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.DataProtectionSettings, out.NextToken, nil
		},
		func(it wswtypes.DataProtectionSettingsSummary) (*store.Resource, string, string) {
			return wswRow(acct, region, scanID, TypeWSWDataProtectionSettings, "data protection settings", sv(it.DataProtectionSettingsArn), sv(it.DisplayName), it)
		}, tags, p)
}

func scanWSWIPAccessSettings(ctx context.Context, client wswAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) bool {
	return storeTokenListTags(ctx, st, acct, region, "workspaces-web:ListIpAccessSettings",
		func(ctx context.Context, token *string) ([]wswtypes.IpAccessSettingsSummary, *string, error) {
			out, err := client.ListIpAccessSettings(ctx, &workspacesweb.ListIpAccessSettingsInput{NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.IpAccessSettings, out.NextToken, nil
		},
		func(it wswtypes.IpAccessSettingsSummary) (*store.Resource, string, string) {
			return wswRow(acct, region, scanID, TypeWSWIPAccessSettings, "IP access settings", sv(it.IpAccessSettingsArn), sv(it.DisplayName), it)
		}, tags, p)
}

func scanWSWNetworkSettings(ctx context.Context, client wswAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) bool {
	return storeTokenListTags(ctx, st, acct, region, "workspaces-web:ListNetworkSettings",
		func(ctx context.Context, token *string) ([]wswtypes.NetworkSettingsSummary, *string, error) {
			out, err := client.ListNetworkSettings(ctx, &workspacesweb.ListNetworkSettingsInput{NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.NetworkSettings, out.NextToken, nil
		},
		func(it wswtypes.NetworkSettingsSummary) (*store.Resource, string, string) {
			return wswRow(acct, region, scanID, TypeWSWNetworkSettings, "network settings", sv(it.NetworkSettingsArn), "", it)
		}, tags, p)
}

func scanWSWSessionLoggers(ctx context.Context, client wswAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) bool {
	return storeTokenListTags(ctx, st, acct, region, "workspaces-web:ListSessionLoggers",
		func(ctx context.Context, token *string) ([]wswtypes.SessionLoggerSummary, *string, error) {
			out, err := client.ListSessionLoggers(ctx, &workspacesweb.ListSessionLoggersInput{NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.SessionLoggers, out.NextToken, nil
		},
		func(it wswtypes.SessionLoggerSummary) (*store.Resource, string, string) {
			return wswRow(acct, region, scanID, TypeWSWSessionLogger, "session loggers", sv(it.SessionLoggerArn), sv(it.DisplayName), it)
		}, tags, p)
}

func scanWSWTrustStores(ctx context.Context, client wswAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) bool {
	return storeTokenListTags(ctx, st, acct, region, "workspaces-web:ListTrustStores",
		func(ctx context.Context, token *string) ([]wswtypes.TrustStoreSummary, *string, error) {
			out, err := client.ListTrustStores(ctx, &workspacesweb.ListTrustStoresInput{NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.TrustStores, out.NextToken, nil
		},
		func(it wswtypes.TrustStoreSummary) (*store.Resource, string, string) {
			return wswRow(acct, region, scanID, TypeWSWTrustStore, "trust stores", sv(it.TrustStoreArn), "", it)
		}, tags, p)
}

func scanWSWUserAccessLoggingSettings(ctx context.Context, client wswAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) bool {
	return storeTokenListTags(ctx, st, acct, region, "workspaces-web:ListUserAccessLoggingSettings",
		func(ctx context.Context, token *string) ([]wswtypes.UserAccessLoggingSettingsSummary, *string, error) {
			out, err := client.ListUserAccessLoggingSettings(ctx, &workspacesweb.ListUserAccessLoggingSettingsInput{NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.UserAccessLoggingSettings, out.NextToken, nil
		},
		func(it wswtypes.UserAccessLoggingSettingsSummary) (*store.Resource, string, string) {
			return wswRow(acct, region, scanID, TypeWSWUserAccessLoggingSettings, "user access logging settings", sv(it.UserAccessLoggingSettingsArn), "", it)
		}, tags, p)
}

func scanWSWUserSettings(ctx context.Context, client wswAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) bool {
	return storeTokenListTags(ctx, st, acct, region, "workspaces-web:ListUserSettings",
		func(ctx context.Context, token *string) ([]wswtypes.UserSettingsSummary, *string, error) {
			out, err := client.ListUserSettings(ctx, &workspacesweb.ListUserSettingsInput{NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.UserSettings, out.NextToken, nil
		},
		func(it wswtypes.UserSettingsSummary) (*store.Resource, string, string) {
			return wswRow(acct, region, scanID, TypeWSWUserSettings, "user settings", sv(it.UserSettingsArn), "", it)
		}, tags, p)
}

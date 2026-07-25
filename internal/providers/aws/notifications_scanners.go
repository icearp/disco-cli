package aws

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/service/notifications"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerType(restype.Descriptor{Type: TypeNotificationsChannelAssociation, Service: "notifications"})
	registerType(restype.Descriptor{Type: TypeNotificationsEventRule, Service: "notifications"})
	registerType(restype.Descriptor{Type: TypeNotificationsManagedNotificationAdditionalChannelAssoc, Service: "notifications", Leaf: true, Managed: true})
	registerType(restype.Descriptor{Type: TypeNotificationsNotificationConfiguration, Service: "notifications", Leaf: true})
	registerType(restype.Descriptor{Type: TypeNotificationsNotificationHub, Service: "notifications", Leaf: true})
	registerType(restype.Descriptor{Type: TypeNotificationsOrganizationalUnitAssociation, Service: "notifications"})
	registerType(restype.Descriptor{Type: TypeNotificationsManagedNotificationConfiguration, Service: "notifications", Leaf: true, Managed: true})
	registerService(serviceEntry{
		name:   "aws:notifications",
		global: true,
		fn:     scanNotifications,
	})
}

type notifsAPI interface {
	ListNotificationConfigurations(context.Context, *notifications.ListNotificationConfigurationsInput, ...func(*notifications.Options)) (*notifications.ListNotificationConfigurationsOutput, error)
	ListNotificationHubs(context.Context, *notifications.ListNotificationHubsInput, ...func(*notifications.Options)) (*notifications.ListNotificationHubsOutput, error)
	ListChannels(context.Context, *notifications.ListChannelsInput, ...func(*notifications.Options)) (*notifications.ListChannelsOutput, error)
	ListEventRules(context.Context, *notifications.ListEventRulesInput, ...func(*notifications.Options)) (*notifications.ListEventRulesOutput, error)
	ListOrganizationalUnits(context.Context, *notifications.ListOrganizationalUnitsInput, ...func(*notifications.Options)) (*notifications.ListOrganizationalUnitsOutput, error)
	ListManagedNotificationConfigurations(context.Context, *notifications.ListManagedNotificationConfigurationsInput, ...func(*notifications.Options)) (*notifications.ListManagedNotificationConfigurationsOutput, error)
	ListManagedNotificationChannelAssociations(context.Context, *notifications.ListManagedNotificationChannelAssociationsInput, ...func(*notifications.Options)) (*notifications.ListManagedNotificationChannelAssociationsOutput, error)
}

// Notifications is a global service callable from us-east-1; gate other regions out.
func scanNotifications(ctx context.Context, acct *account, _ string, st *store.Store, scanID string) (total, inserted int, err error) {
	region := "us-east-1"
	client := notifications.NewFromConfig(acct.cfg, func(o *notifications.Options) { o.Region = region })

	configARNs, t, i, ferr := scanNotifConfigs(ctx, client, acct, region, st, scanID)
	if ferr != nil {
		return total, inserted, ferr
	}
	total += t
	inserted += i

	t, i, ferr = scanNotifHubs(ctx, client, acct, region, st, scanID)
	if ferr != nil {
		return total, inserted, ferr
	}
	total += t
	inserted += i

	for _, carn := range configARNs {
		for _, phase := range []func() (int, int, error){
			func() (int, int, error) {
				return scanNotifChannelAssociations(ctx, client, acct, region, st, scanID, carn)
			},
			func() (int, int, error) { return scanNotifEventRules(ctx, client, acct, region, st, scanID, carn) },
			func() (int, int, error) { return scanNotifOUAssocs(ctx, client, acct, region, st, scanID, carn) },
		} {
			t, i, perr := phase()
			if perr != nil {
				return total, inserted, perr
			}
			total += t
			inserted += i
		}
	}

	mgmdARNs, t, i, ferr := scanNotifManagedConfigs(ctx, client, acct, region, st, scanID)
	if ferr != nil {
		return total, inserted, ferr
	}
	total += t
	inserted += i
	for _, marn := range mgmdARNs {
		t, i, perr := scanNotifManagedChannelAssocs(ctx, client, acct, region, st, scanID, marn)
		if perr != nil {
			return total, inserted, perr
		}
		total += t
		inserted += i
	}
	return total, inserted, nil
}

func scanNotifConfigs(ctx context.Context, client notifsAPI, acct *account, region string, st *store.Store, scanID string) ([]string, int, int, error) {
	pager := notifications.NewListNotificationConfigurationsPaginator(client, &notifications.ListNotificationConfigurationsInput{})
	var batch []*store.Resource
	var arns []string
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "notifications:ListNotificationConfigurations", acct.ID, region, perr)
				return nil, 0, 0, nil
			}
			return nil, 0, 0, fmt.Errorf("notifications:ListNotificationConfigurations: %w", perr)
		}
		for _, c := range out.NotificationConfigurations {
			arn := sv(c.Arn)
			if arn == "" {
				continue
			}
			label := sv(c.Name)
			if label == "" {
				label = arn
			}
			arns = append(arns, arn)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeNotificationsNotificationConfiguration, NativeID: arn,
				Name: &label, Region: regionGlobal, AttributesJSON: mustJSON(c), DiscoveredBy: scanID,
			})
		}
	}
	t, i, err := upsertBatch(st, batch, "notifications notification-configurations")
	return arns, t, i, err
}

func scanNotifHubs(ctx context.Context, client notifsAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	out, err := client.ListNotificationHubs(ctx, &notifications.ListNotificationHubsInput{})
	if err != nil {
		if isAccessDenied(err) {
			_ = skipIfAccessDenied(st, "notifications:ListNotificationHubs", acct.ID, region, err)
			return 0, 0, nil
		}
		return 0, 0, fmt.Errorf("notifications:ListNotificationHubs: %w", err)
	}
	var batch []*store.Resource
	for _, h := range out.NotificationHubs {
		hubReg := sv(h.NotificationHubRegion)
		if hubReg == "" {
			continue
		}
		arn := fmt.Sprintf("arn:aws:notifications::%s:notification-hub/%s", acct.ID, hubReg)
		label := hubReg
		batch = append(batch, &store.Resource{
			Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
			Type: TypeNotificationsNotificationHub, NativeID: arn,
			Name: &label, Region: regionGlobal, AttributesJSON: mustJSON(h), DiscoveredBy: scanID,
		})
	}
	return upsertBatch(st, batch, "notifications notification-hubs")
}

func scanNotifChannelAssociations(ctx context.Context, client notifsAPI, acct *account, region string, st *store.Store, scanID, configARN string) (int, int, error) {
	carn := configARN
	pager := notifications.NewListChannelsPaginator(client, &notifications.ListChannelsInput{NotificationConfigurationArn: &carn})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "notifications:ListChannels", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("notifications:ListChannels: %w", perr)
		}
		for _, channelARN := range out.Channels {
			if channelARN == "" {
				continue
			}
			arn := fmt.Sprintf("%s/channel-association/%s", configARN, channelARN)
			label := channelARN
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeNotificationsChannelAssociation, NativeID: arn,
				Name: &label, Region: regionGlobal, AttributesJSON: mustJSON(map[string]string{"NotificationConfigurationArn": configARN, "ChannelArn": channelARN}), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "notifications channel-associations")
}

func scanNotifEventRules(ctx context.Context, client notifsAPI, acct *account, region string, st *store.Store, scanID, configARN string) (int, int, error) {
	carn := configARN
	pager := notifications.NewListEventRulesPaginator(client, &notifications.ListEventRulesInput{NotificationConfigurationArn: &carn})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "notifications:ListEventRules", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("notifications:ListEventRules: %w", perr)
		}
		for _, e := range out.EventRules {
			arn := sv(e.Arn)
			if arn == "" {
				continue
			}
			label := sv(e.EventType)
			if label == "" {
				label = arn
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeNotificationsEventRule, NativeID: arn,
				Name: &label, Region: regionGlobal, AttributesJSON: mustJSON(e), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "notifications event-rules")
}

func scanNotifOUAssocs(ctx context.Context, client notifsAPI, acct *account, region string, st *store.Store, scanID, configARN string) (int, int, error) {
	carn := configARN
	pager := notifications.NewListOrganizationalUnitsPaginator(client, &notifications.ListOrganizationalUnitsInput{NotificationConfigurationArn: &carn})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "notifications:ListOrganizationalUnits", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("notifications:ListOrganizationalUnits: %w", perr)
		}
		for _, ouID := range out.OrganizationalUnits {
			if ouID == "" {
				continue
			}
			arn := fmt.Sprintf("%s/ou-association/%s", configARN, ouID)
			label := ouID
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeNotificationsOrganizationalUnitAssociation, NativeID: arn,
				Name: &label, Region: regionGlobal, AttributesJSON: mustJSON(map[string]string{"NotificationConfigurationArn": configARN, "OrganizationalUnitId": ouID}), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "notifications ou-associations")
}

func scanNotifManagedConfigs(ctx context.Context, client notifsAPI, acct *account, region string, st *store.Store, scanID string) ([]string, int, int, error) {
	// ManagedNotificationConfigurations are AWS-managed (not user-created),
	// flagged ManagedByProvider, and serve as parent context for the
	// ManagedNotificationAdditionalChannelAssociation child fan-out.
	pager := notifications.NewListManagedNotificationConfigurationsPaginator(client, &notifications.ListManagedNotificationConfigurationsInput{})
	var arns []string
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "notifications:ListManagedNotificationConfigurations", acct.ID, region, perr)
				return nil, 0, 0, nil
			}
			return nil, 0, 0, fmt.Errorf("notifications:ListManagedNotificationConfigurations: %w", perr)
		}
		for _, c := range out.ManagedNotificationConfigurations {
			arn := sv(c.Arn)
			if arn == "" {
				continue
			}
			arns = append(arns, arn)
			label := sv(c.Name)
			if label == "" {
				label = arn
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeNotificationsManagedNotificationConfiguration, NativeID: arn,
				Name: &label, Region: regionGlobal, AttributesJSON: mustJSON(c), DiscoveredBy: scanID,
			})
		}
	}
	t, i, err := upsertBatch(st, batch, "notifications managed-notification-configurations")
	return arns, t, i, err
}

func scanNotifManagedChannelAssocs(ctx context.Context, client notifsAPI, acct *account, region string, st *store.Store, scanID, mcARN string) (int, int, error) {
	marn := mcARN
	pager := notifications.NewListManagedNotificationChannelAssociationsPaginator(client, &notifications.ListManagedNotificationChannelAssociationsInput{ManagedNotificationConfigurationArn: &marn})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "notifications:ListManagedNotificationChannelAssociations", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("notifications:ListManagedNotificationChannelAssociations: %w", perr)
		}
		for _, a := range out.ChannelAssociations {
			cid := sv(a.ChannelIdentifier)
			if cid == "" {
				continue
			}
			arn := fmt.Sprintf("%s/managed-additional-channel-association/%s", mcARN, cid)
			label := cid
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeNotificationsManagedNotificationAdditionalChannelAssoc, NativeID: arn,
				Name: &label, Region: regionGlobal, AttributesJSON: mustJSON(a), DiscoveredBy: scanID,
				// Always AWS-default — emitted only by AWS-managed
				// notification configurations.
			})
		}
	}
	return upsertBatch(st, batch, "notifications managed-notification-additional-channel-associations")
}

package aws

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/restype"
	"codeberg.org/icearp/disco/store"
	"github.com/aws/aws-sdk-go-v2/service/iotwireless"
)

func init() {
	registerType(restype.Descriptor{Type: TypeIoTWirelessDestination, Service: "iotwireless"})
	registerType(restype.Descriptor{Type: TypeIoTWirelessDeviceProfile, Service: "iotwireless", Leaf: true})
	registerType(restype.Descriptor{Type: TypeIoTWirelessFuotaTask, Service: "iotwireless", Leaf: true})
	registerType(restype.Descriptor{Type: TypeIoTWirelessMulticastGroup, Service: "iotwireless", Leaf: true})
	registerType(restype.Descriptor{Type: TypeIoTWirelessNetworkAnalyzerConfiguration, Service: "iotwireless", Leaf: true})
	registerType(restype.Descriptor{Type: TypeIoTWirelessPartnerAccount, Service: "iotwireless", Leaf: true})
	registerType(restype.Descriptor{Type: TypeIoTWirelessServiceProfile, Service: "iotwireless", Leaf: true})
	registerType(restype.Descriptor{Type: TypeIoTWirelessTaskDefinition, Service: "iotwireless", Leaf: true})
	registerType(restype.Descriptor{Type: TypeIoTWirelessWirelessDevice, Service: "iotwireless"})
	registerType(restype.Descriptor{Type: TypeIoTWirelessWirelessDeviceImportTask, Service: "iotwireless", Leaf: true})
	registerType(restype.Descriptor{Type: TypeIoTWirelessWirelessGateway, Service: "iotwireless", Leaf: true})
	registerService(serviceEntry{
		name: "aws:iotwireless",
		fn:   scanIoTWireless,
	})
}

type iotWirelessAPI interface {
	ListDestinations(context.Context, *iotwireless.ListDestinationsInput, ...func(*iotwireless.Options)) (*iotwireless.ListDestinationsOutput, error)
	ListDeviceProfiles(context.Context, *iotwireless.ListDeviceProfilesInput, ...func(*iotwireless.Options)) (*iotwireless.ListDeviceProfilesOutput, error)
	ListFuotaTasks(context.Context, *iotwireless.ListFuotaTasksInput, ...func(*iotwireless.Options)) (*iotwireless.ListFuotaTasksOutput, error)
	ListMulticastGroups(context.Context, *iotwireless.ListMulticastGroupsInput, ...func(*iotwireless.Options)) (*iotwireless.ListMulticastGroupsOutput, error)
	ListNetworkAnalyzerConfigurations(context.Context, *iotwireless.ListNetworkAnalyzerConfigurationsInput, ...func(*iotwireless.Options)) (*iotwireless.ListNetworkAnalyzerConfigurationsOutput, error)
	ListPartnerAccounts(context.Context, *iotwireless.ListPartnerAccountsInput, ...func(*iotwireless.Options)) (*iotwireless.ListPartnerAccountsOutput, error)
	ListServiceProfiles(context.Context, *iotwireless.ListServiceProfilesInput, ...func(*iotwireless.Options)) (*iotwireless.ListServiceProfilesOutput, error)
	ListWirelessDevices(context.Context, *iotwireless.ListWirelessDevicesInput, ...func(*iotwireless.Options)) (*iotwireless.ListWirelessDevicesOutput, error)
	ListWirelessDeviceImportTasks(context.Context, *iotwireless.ListWirelessDeviceImportTasksInput, ...func(*iotwireless.Options)) (*iotwireless.ListWirelessDeviceImportTasksOutput, error)
	ListWirelessGateways(context.Context, *iotwireless.ListWirelessGatewaysInput, ...func(*iotwireless.Options)) (*iotwireless.ListWirelessGatewaysOutput, error)
	ListWirelessGatewayTaskDefinitions(context.Context, *iotwireless.ListWirelessGatewayTaskDefinitionsInput, ...func(*iotwireless.Options)) (*iotwireless.ListWirelessGatewayTaskDefinitionsOutput, error)
}

func scanIoTWireless(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := iotwireless.NewFromConfig(acct.cfg, func(o *iotwireless.Options) { o.Region = region })

	for _, phase := range []func() (int, int, error){
		func() (int, int, error) { return scanIWDestinations(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanIWDeviceProfiles(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanIWFuotaTasks(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanIWMulticastGroups(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanIWNetworkAnalyzerConfigs(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanIWPartnerAccounts(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanIWServiceProfiles(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanIWWirelessDevices(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) {
			return scanIWWirelessDeviceImportTasks(ctx, client, acct, region, st, scanID)
		},
		func() (int, int, error) { return scanIWWirelessGateways(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanIWGatewayTaskDefs(ctx, client, acct, region, st, scanID) },
	} {
		t, i, ferr := phase()
		if ferr != nil {
			return total, inserted, ferr
		}
		total += t
		inserted += i
	}
	return total, inserted, nil
}

func scanIWDestinations(ctx context.Context, client iotWirelessAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := iotwireless.NewListDestinationsPaginator(client, &iotwireless.ListDestinationsInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "iotwireless:ListDestinations", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("iotwireless:ListDestinations: %w", perr)
		}
		for _, d := range out.DestinationList {
			arn := sv(d.Arn)
			if arn == "" {
				continue
			}
			label := sv(d.Name)
			if label == "" {
				label = arn
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeIoTWirelessDestination, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(d), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "iotwireless destinations")
}

func scanIWDeviceProfiles(ctx context.Context, client iotWirelessAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := iotwireless.NewListDeviceProfilesPaginator(client, &iotwireless.ListDeviceProfilesInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "iotwireless:ListDeviceProfiles", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("iotwireless:ListDeviceProfiles: %w", perr)
		}
		for _, d := range out.DeviceProfileList {
			arn := sv(d.Arn)
			if arn == "" {
				continue
			}
			label := sv(d.Name)
			if label == "" {
				label = sv(d.Id)
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeIoTWirelessDeviceProfile, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(d), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "iotwireless device-profiles")
}

func scanIWFuotaTasks(ctx context.Context, client iotWirelessAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := iotwireless.NewListFuotaTasksPaginator(client, &iotwireless.ListFuotaTasksInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "iotwireless:ListFuotaTasks", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("iotwireless:ListFuotaTasks: %w", perr)
		}
		for _, f := range out.FuotaTaskList {
			arn := sv(f.Arn)
			if arn == "" {
				continue
			}
			label := sv(f.Name)
			if label == "" {
				label = sv(f.Id)
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeIoTWirelessFuotaTask, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(f), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "iotwireless fuota-tasks")
}

func scanIWMulticastGroups(ctx context.Context, client iotWirelessAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := iotwireless.NewListMulticastGroupsPaginator(client, &iotwireless.ListMulticastGroupsInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "iotwireless:ListMulticastGroups", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("iotwireless:ListMulticastGroups: %w", perr)
		}
		for _, m := range out.MulticastGroupList {
			arn := sv(m.Arn)
			if arn == "" {
				continue
			}
			label := sv(m.Name)
			if label == "" {
				label = sv(m.Id)
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeIoTWirelessMulticastGroup, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(m), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "iotwireless multicast-groups")
}

func scanIWNetworkAnalyzerConfigs(ctx context.Context, client iotWirelessAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := iotwireless.NewListNetworkAnalyzerConfigurationsPaginator(client, &iotwireless.ListNetworkAnalyzerConfigurationsInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "iotwireless:ListNetworkAnalyzerConfigurations", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("iotwireless:ListNetworkAnalyzerConfigurations: %w", perr)
		}
		for _, n := range out.NetworkAnalyzerConfigurationList {
			arn := sv(n.Arn)
			if arn == "" {
				continue
			}
			label := sv(n.Name)
			if label == "" {
				label = arn
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeIoTWirelessNetworkAnalyzerConfiguration, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(n), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "iotwireless network-analyzer-configurations")
}

// scanIWPartnerAccounts — Sidewalk-only; manual NextToken loop (no paginator).
func scanIWPartnerAccounts(ctx context.Context, client iotWirelessAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var batch []*store.Resource
	var token *string
	for {
		out, err := client.ListPartnerAccounts(ctx, &iotwireless.ListPartnerAccountsInput{NextToken: token})
		if err != nil {
			// Per-region feature gap ("not authorized to call this API in this
			// region <r>"): Sidewalk partner accounts gated to a region subset,
			// distinct from canonical IAM-deny shape.
			if isAccessDeniedWithMessage(err, "not authorized to call this API in this region") {
				return 0, 0, nil
			}
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "iotwireless:ListPartnerAccounts", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("iotwireless:ListPartnerAccounts: %w", err)
		}
		for _, s := range out.Sidewalk {
			arn := sv(s.Arn)
			if arn == "" {
				continue
			}
			label := sv(s.AmazonId)
			if label == "" {
				label = arn
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeIoTWirelessPartnerAccount, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(s), DiscoveredBy: scanID,
			})
		}
		if out.NextToken == nil || *out.NextToken == "" {
			break
		}
		token = out.NextToken
	}
	return upsertBatch(st, batch, "iotwireless partner-accounts")
}

func scanIWServiceProfiles(ctx context.Context, client iotWirelessAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := iotwireless.NewListServiceProfilesPaginator(client, &iotwireless.ListServiceProfilesInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "iotwireless:ListServiceProfiles", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("iotwireless:ListServiceProfiles: %w", perr)
		}
		for _, s := range out.ServiceProfileList {
			arn := sv(s.Arn)
			if arn == "" {
				continue
			}
			label := sv(s.Name)
			if label == "" {
				label = sv(s.Id)
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeIoTWirelessServiceProfile, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(s), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "iotwireless service-profiles")
}

func scanIWWirelessDevices(ctx context.Context, client iotWirelessAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := iotwireless.NewListWirelessDevicesPaginator(client, &iotwireless.ListWirelessDevicesInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "iotwireless:ListWirelessDevices", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("iotwireless:ListWirelessDevices: %w", perr)
		}
		for _, d := range out.WirelessDeviceList {
			arn := sv(d.Arn)
			if arn == "" {
				continue
			}
			label := sv(d.Name)
			if label == "" {
				label = sv(d.Id)
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeIoTWirelessWirelessDevice, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(d), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "iotwireless wireless-devices")
}

func scanIWWirelessDeviceImportTasks(ctx context.Context, client iotWirelessAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var batch []*store.Resource
	var token *string
	for {
		out, err := client.ListWirelessDeviceImportTasks(ctx, &iotwireless.ListWirelessDeviceImportTasksInput{NextToken: token})
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "iotwireless:ListWirelessDeviceImportTasks", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("iotwireless:ListWirelessDeviceImportTasks: %w", err)
		}
		for _, t := range out.WirelessDeviceImportTaskList {
			arn := sv(t.Arn)
			if arn == "" {
				continue
			}
			label := sv(t.Id)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeIoTWirelessWirelessDeviceImportTask, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(t), DiscoveredBy: scanID,
			})
		}
		if out.NextToken == nil || *out.NextToken == "" {
			break
		}
		token = out.NextToken
	}
	return upsertBatch(st, batch, "iotwireless wireless-device-import-tasks")
}

func scanIWWirelessGateways(ctx context.Context, client iotWirelessAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := iotwireless.NewListWirelessGatewaysPaginator(client, &iotwireless.ListWirelessGatewaysInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "iotwireless:ListWirelessGateways", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("iotwireless:ListWirelessGateways: %w", perr)
		}
		for _, g := range out.WirelessGatewayList {
			arn := sv(g.Arn)
			if arn == "" {
				continue
			}
			label := sv(g.Name)
			if label == "" {
				label = sv(g.Id)
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeIoTWirelessWirelessGateway, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(g), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "iotwireless wireless-gateways")
}

func scanIWGatewayTaskDefs(ctx context.Context, client iotWirelessAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var batch []*store.Resource
	var token *string
	for {
		out, err := client.ListWirelessGatewayTaskDefinitions(ctx, &iotwireless.ListWirelessGatewayTaskDefinitionsInput{NextToken: token})
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "iotwireless:ListWirelessGatewayTaskDefinitions", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("iotwireless:ListWirelessGatewayTaskDefinitions: %w", err)
		}
		for _, t := range out.TaskDefinitions {
			arn := sv(t.Arn)
			if arn == "" {
				continue
			}
			label := sv(t.Id)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeIoTWirelessTaskDefinition, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(t), DiscoveredBy: scanID,
			})
		}
		if out.NextToken == nil || *out.NextToken == "" {
			break
		}
		token = out.NextToken
	}
	return upsertBatch(st, batch, "iotwireless task-definitions")
}

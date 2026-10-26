package aws

import (
	"context"
	"errors"
	"fmt"
	"maps"

	"github.com/aws/aws-sdk-go-v2/service/iotwireless"
	iwtypes "github.com/aws/aws-sdk-go-v2/service/iotwireless/types"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerType(restype.Descriptor{Type: TypeIoTWirelessDestination, Service: "iotwireless"})
	registerType(restype.Descriptor{Type: TypeIoTWirelessDeviceProfile, Service: "iotwireless"})
	registerType(restype.Descriptor{Type: TypeIoTWirelessEventConfiguration, Service: "iotwireless"})
	registerType(restype.Descriptor{Type: TypeIoTWirelessFuotaTask, Service: "iotwireless"})
	registerType(restype.Descriptor{Type: TypeIoTWirelessMulticastGroup, Service: "iotwireless"})
	registerType(restype.Descriptor{Type: TypeIoTWirelessNetworkAnalyzerConfiguration, Service: "iotwireless"})
	registerType(restype.Descriptor{Type: TypeIoTWirelessPartnerAccount, Service: "iotwireless"})
	registerType(restype.Descriptor{Type: TypeIoTWirelessServiceProfile, Service: "iotwireless"})
	registerType(restype.Descriptor{Type: TypeIoTWirelessTaskDefinition, Service: "iotwireless"})
	registerType(restype.Descriptor{Type: TypeIoTWirelessWirelessDevice, Service: "iotwireless"})
	registerType(restype.Descriptor{Type: TypeIoTWirelessWirelessDeviceImportTask, Service: "iotwireless"})
	registerType(restype.Descriptor{Type: TypeIoTWirelessWirelessGateway, Service: "iotwireless"})
	registerService(serviceEntry{
		name: "aws:iotwireless",
		fn:   scanIoTWireless,
	})
}

type iotWirelessAPI interface {
	ListDestinations(context.Context, *iotwireless.ListDestinationsInput, ...func(*iotwireless.Options)) (*iotwireless.ListDestinationsOutput, error)
	ListDeviceProfiles(context.Context, *iotwireless.ListDeviceProfilesInput, ...func(*iotwireless.Options)) (*iotwireless.ListDeviceProfilesOutput, error)
	ListEventConfigurations(context.Context, *iotwireless.ListEventConfigurationsInput, ...func(*iotwireless.Options)) (*iotwireless.ListEventConfigurationsOutput, error)
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

func scanIoTWireless(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	client := iotwireless.NewFromConfig(acct.cfg, func(o *iotwireless.Options) { o.Region = region })
	return scanIoTWirelessAll(ctx, client, acct, region, st, scanID)
}

// scanIoTWirelessAll runs the older phases, which stop at their first hard
// error, then the event-configuration phase over the parents they stored, so a
// failure on either side costs the other no rows. The older side's transient
// error is demoted to a warning only beside a hard error: joined, it would be
// buried in the hard error instead of reported under its op; the
// event-configuration phase tallies its own transients into a warning.
func scanIoTWirelessAll(ctx context.Context, client iotWirelessAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	parents := iwEventParents{}
	total, inserted, err := scanIWOlderPhases(ctx, client, acct, region, st, scanID, parents)
	te, ie, eerr := scanIWEventConfigurations(ctx, client, acct, region, st, scanID, parents)
	return total + te, inserted + ie, demoteBeside(st, "iotwireless", acct, region, err, eerr)
}

// scanIWOlderPhases runs the phases that predate event configurations,
// stopping at the first hard error. The partner-account, wireless-device and
// wireless-gateway phases record what they stored in parents.
func scanIWOlderPhases(ctx context.Context, client iotWirelessAPI, acct *account, region string, st *store.Store, scanID string, parents iwEventParents) (total, inserted int, err error) {
	for _, phase := range []func() (int, int, error){
		func() (int, int, error) { return scanIWDestinations(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanIWDeviceProfiles(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanIWFuotaTasks(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanIWMulticastGroups(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanIWNetworkAnalyzerConfigs(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanIWPartnerAccounts(ctx, client, acct, region, st, scanID, parents) },
		func() (int, int, error) { return scanIWServiceProfiles(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanIWWirelessDevices(ctx, client, acct, region, st, scanID, parents) },
		func() (int, int, error) {
			return scanIWWirelessDeviceImportTasks(ctx, client, acct, region, st, scanID)
		},
		func() (int, int, error) {
			return scanIWWirelessGateways(ctx, client, acct, region, st, scanID, parents)
		},
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
func scanIWPartnerAccounts(ctx context.Context, client iotWirelessAPI, acct *account, region string, st *store.Store, scanID string, parents iwEventParents) (int, int, error) {
	var batch []*store.Resource
	listed := iwEventParents{}
	var token *string
	for {
		out, err := client.ListPartnerAccounts(ctx, &iotwireless.ListPartnerAccountsInput{NextToken: token})
		if err != nil {
			// Per-region feature gap ("not authorized to call this API in this
			// region <r>"): Sidewalk partner accounts gated to a region subset,
			// distinct from canonical IAM-deny shape.
			if isAccessDeniedWithMessage(err, iwRegionGatedMessage) {
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
			listed.add(iwtypes.IdentifierTypePartnerAccountId, sv(s.AmazonId), arn)
		}
		if out.NextToken == nil || *out.NextToken == "" {
			break
		}
		token = out.NextToken
	}
	return upsertIWParents(st, batch, "iotwireless partner-accounts", parents, listed)
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

func scanIWWirelessDevices(ctx context.Context, client iotWirelessAPI, acct *account, region string, st *store.Store, scanID string, parents iwEventParents) (int, int, error) {
	pager := iotwireless.NewListWirelessDevicesPaginator(client, &iotwireless.ListWirelessDevicesInput{})
	var batch []*store.Resource
	listed := iwEventParents{}
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
			listed.add(iwtypes.IdentifierTypeWirelessDeviceId, sv(d.Id), arn)
			if d.LoRaWAN != nil {
				listed.add(iwtypes.IdentifierTypeDevEui, sv(d.LoRaWAN.DevEui), arn)
			}
		}
	}
	return upsertIWParents(st, batch, "iotwireless wireless-devices", parents, listed)
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

func scanIWWirelessGateways(ctx context.Context, client iotWirelessAPI, acct *account, region string, st *store.Store, scanID string, parents iwEventParents) (int, int, error) {
	pager := iotwireless.NewListWirelessGatewaysPaginator(client, &iotwireless.ListWirelessGatewaysInput{})
	var batch []*store.Resource
	listed := iwEventParents{}
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
			listed.add(iwtypes.IdentifierTypeWirelessGatewayId, sv(g.Id), arn)
			if g.LoRaWAN != nil {
				listed.add(iwtypes.IdentifierTypeGatewayEui, sv(g.LoRaWAN.GatewayEui), arn)
			}
		}
	}
	return upsertIWParents(st, batch, "iotwireless wireless-gateways", parents, listed)
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

const (
	iwListEventConfigurationsOp = "iotwireless:ListEventConfigurations"
	// iwEventConfigPageLimit is the model's MaxResults cap.
	iwEventConfigPageLimit = 250
	// iwRegionGatedMessage is how IoT Wireless refuses a Sidewalk API in a
	// region Sidewalk is not offered in, under an AccessDeniedException code.
	iwRegionGatedMessage = "not authorized to call this API in this region"
)

// iwEventParents maps, per identifier type, each identifier an event
// configuration can name its resource by to the NativeID of that resource as
// the older phases stored it this scan.
type iwEventParents map[iwtypes.IdentifierType]map[string]string

func (p iwEventParents) add(idType iwtypes.IdentifierType, identifier, nativeID string) {
	if identifier == "" {
		return
	}
	if p[idType] == nil {
		p[idType] = map[string]string{}
	}
	p[idType][identifier] = nativeID
}

// upsertIWParents stores batch and, only once it is stored, records the
// identifiers listed for it in parents, so no event configuration is linked
// under a parent row that does not exist.
func upsertIWParents(st *store.Store, batch []*store.Resource, label string, parents, listed iwEventParents) (int, int, error) {
	total, inserted, err := upsertBatch(st, batch, label)
	if err != nil {
		return 0, 0, err
	}
	for idType, ids := range listed {
		if parents[idType] == nil {
			parents[idType] = map[string]string{}
		}
		maps.Copy(parents[idType], ids)
	}
	return total, inserted, nil
}

// iwEventConfigParentKinds is the Service Reference ARN resource type of each
// identifier type that is the id in that ARN
// (arn:{p}:iotwireless:{region}:{account}:{kind}/{id}). Sidewalk is the only
// partner type the model has.
var iwEventConfigParentKinds = map[iwtypes.IdentifierType]string{
	iwtypes.IdentifierTypeWirelessDeviceId:  "WirelessDevice",
	iwtypes.IdentifierTypeWirelessGatewayId: "WirelessGateway",
	iwtypes.IdentifierTypePartnerAccountId:  "SidewalkAccount",
}

// iwEventConfigNativeID is the NativeID of an event configuration: an ARN in
// the Service Reference format built from the returned identifier, plus
// "/event-configuration". The op returns no ARN. For a wireless device or
// gateway id that ARN matches the parent row's NativeID in the aws partition
// (the partition is hard-coded, as elsewhere in this package). Any other identifier type
// (an EUI) is not the id in an ARN, so the identifier type stands in for the
// ARN resource type (DevEui/{eui}); no IoT Wireless ARN resource type is
// named after an identifier type, so the forms cannot collide.
func iwEventConfigNativeID(partition, region, accountID string, item iwtypes.EventConfigurationItem) string {
	kind, ok := iwEventConfigParentKinds[item.IdentifierType]
	if !ok {
		kind = string(item.IdentifierType)
	}
	return arnFor(partition, "iotwireless", region, accountID, kind+"/"+sv(item.Identifier)+"/event-configuration")
}

// scanIWEventConfigurations stores the event notification configuration of
// every resource that has at least one event topic enabled, listed once per
// resource type, and links each under the partner account, wireless device
// or wireless gateway it names when an older phase stored that parent this
// scan. Each resource type is listed independently and keeps the rows of the
// pages it read: a Sidewalk region gap is skipped silently as
// ListPartnerAccounts does; a denial warns once for the op; a transient error
// is tallied into one warning; anything else, ValidationException included
// (the resource type is the SDK's own enum value, so it means a bad request),
// is returned after the other types' rows are stored.
func scanIWEventConfigurations(ctx context.Context, client iotWirelessAPI, acct *account, region string, st *store.Store, scanID string, parents iwEventParents) (int, int, error) {
	var (
		children  []containedChild
		transient transientTally
		denied    bool
		errs      []error
	)
	for _, rt := range iwtypes.EventNotificationResourceType("").Values() {
		items, err := listIWEventConfigurations(ctx, client, rt)
		for _, item := range items {
			if sv(item.Identifier) == "" || item.IdentifierType == "" {
				continue
			}
			child := containedChild{row: &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeIoTWirelessEventConfiguration, NativeID: iwEventConfigNativeID(acct.partition(), region, acct.ID, item),
				Name: item.Identifier, Region: &region, AttributesJSON: mustJSON(item), DiscoveredBy: scanID,
			}}
			if parentARN, ok := parents[item.IdentifierType][sv(item.Identifier)]; ok {
				child.parentID = store.ResourceID("aws", acct.ID, parentARN)
			}
			children = append(children, child)
		}
		switch {
		case err == nil:
		case isAccessDeniedWithMessage(err, iwRegionGatedMessage):
		case isAccessDenied(err):
			if !denied {
				denied = true
				_ = skipIfAccessDenied(st, iwListEventConfigurationsOp, acct.ID, region, err)
			}
		case isTransientNetworkError(err):
			transient.skipped(string(rt), err)
		default:
			errs = append(errs, fmt.Errorf("%s %s: %w", iwListEventConfigurationsOp, rt, err))
		}
	}
	transient.warnSkipped(st, iwListEventConfigurationsOp, acct, region)
	total, inserted, err := upsertContained(st, "iotwireless event-configurations", children)
	return total, inserted, errors.Join(append(errs, err)...)
}

// listIWEventConfigurations lists the event configurations of one resource
// type, returning the items of every page read before an error.
func listIWEventConfigurations(ctx context.Context, client iotWirelessAPI, rt iwtypes.EventNotificationResourceType) ([]iwtypes.EventConfigurationItem, error) {
	in := &iotwireless.ListEventConfigurationsInput{ResourceType: rt, MaxResults: iwEventConfigPageLimit}
	var items []iwtypes.EventConfigurationItem
	for {
		out, err := client.ListEventConfigurations(ctx, in)
		if err != nil {
			return items, err
		}
		items = append(items, out.EventConfigurationsList...)
		// ListEventConfigurations has no paginator; a repeated token would
		// loop forever.
		if done, err := lastPage(in.NextToken, out.NextToken); done {
			return items, err
		}
		in.NextToken = out.NextToken
	}
}

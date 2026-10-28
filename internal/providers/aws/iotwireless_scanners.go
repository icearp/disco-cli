package aws

import (
	"cmp"
	"context"
	"errors"
	"fmt"

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
	ListTagsForResource(context.Context, *iotwireless.ListTagsForResourceInput, ...func(*iotwireless.Options)) (*iotwireless.ListTagsForResourceOutput, error)
}

func scanIoTWireless(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	client := iotwireless.NewFromConfig(acct.cfg, func(o *iotwireless.Options) { o.Region = region })
	return scanIoTWirelessAll(ctx, client, acct, region, st, scanID)
}

const (
	iwTagsOp = "iotwireless:ListTagsForResource"
	// iwPageLimit is the MaxResults range maximum of every IoT Wireless
	// listing the scan calls.
	iwPageLimit = 250
)

// iwNotFoundCodes is the not-found code ListTagsForResource models, for a
// resource deleted after it was listed.
var iwNotFoundCodes = []string{"ResourceNotFoundException"}

// iwTags reads a row's tags with ListTagsForResource, sent the row's
// NativeID, which is the Arn its listing returned. The Service Reference
// lists the ARN resource type of every listing it reads under
// ListTagsForResource (event configurations have no ARN and are not read):
// Destination, DeviceProfile, FuotaTask, ImportTask, MulticastGroup,
// NetworkAnalyzerConfiguration, ServiceProfile, SidewalkAccount,
// WirelessDevice, WirelessGateway and WirelessGatewayTaskDefinition. The op
// returns every tag in one response (it takes no page token).
func iwTags(client iotWirelessAPI) rowTagReader {
	return rowTagReader{
		op:       iwTagsOp,
		notFound: iwNotFoundCodes,
		read: func(ctx context.Context, r *store.Resource) (*string, error) {
			out, err := client.ListTagsForResource(ctx, &iotwireless.ListTagsForResourceInput{ResourceArn: &r.NativeID})
			if err != nil {
				return nil, err
			}
			return kvTagsJSON(out.Tags, func(t iwtypes.Tag) (*string, *string) { return t.Key, t.Value }), nil
		},
	}
}

// scanIoTWirelessAll runs every listing as its own phase, one at a time: a
// phase's error does not stop the later ones; a failed write and ctx's end
// do (runHostPhases). The event-configuration phase runs last, over the
// partner accounts, wireless devices and wireless gateways the earlier
// phases stored.
func scanIoTWirelessAll(ctx context.Context, client iotWirelessAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	tags := iwTags(client)
	parents := iwEventParents{}
	return runHostPhases(ctx, st, acct, region, "iotwireless", 0, []hostPhase{
		{op: "iotwireless:ListDestinations", run: func(p *phaseTotals) bool {
			return scanIWDestinations(ctx, client, acct, region, st, scanID, tags, p)
		}},
		{op: "iotwireless:ListDeviceProfiles", run: func(p *phaseTotals) bool {
			return scanIWDeviceProfiles(ctx, client, acct, region, st, scanID, tags, p)
		}},
		{op: "iotwireless:ListFuotaTasks", run: func(p *phaseTotals) bool {
			return scanIWFuotaTasks(ctx, client, acct, region, st, scanID, tags, p)
		}},
		{op: "iotwireless:ListMulticastGroups", run: func(p *phaseTotals) bool {
			return scanIWMulticastGroups(ctx, client, acct, region, st, scanID, tags, p)
		}},
		{op: "iotwireless:ListNetworkAnalyzerConfigurations", run: func(p *phaseTotals) bool {
			return scanIWNetworkAnalyzerConfigs(ctx, client, acct, region, st, scanID, tags, p)
		}},
		{op: "iotwireless:ListPartnerAccounts", run: func(p *phaseTotals) bool {
			return scanIWPartnerAccounts(ctx, client, acct, region, st, scanID, tags, parents, p)
		}},
		{op: "iotwireless:ListServiceProfiles", run: func(p *phaseTotals) bool {
			return scanIWServiceProfiles(ctx, client, acct, region, st, scanID, tags, p)
		}},
		{op: "iotwireless:ListWirelessDevices", run: func(p *phaseTotals) bool {
			return scanIWWirelessDevices(ctx, client, acct, region, st, scanID, tags, parents, p)
		}},
		{op: "iotwireless:ListWirelessDeviceImportTasks", run: func(p *phaseTotals) bool {
			return scanIWWirelessDeviceImportTasks(ctx, client, acct, region, st, scanID, tags, p)
		}},
		{op: "iotwireless:ListWirelessGateways", run: func(p *phaseTotals) bool {
			return scanIWWirelessGateways(ctx, client, acct, region, st, scanID, tags, parents, p)
		}},
		{op: "iotwireless:ListWirelessGatewayTaskDefinitions", run: func(p *phaseTotals) bool {
			return scanIWGatewayTaskDefs(ctx, client, acct, region, st, scanID, tags, p)
		}},
		countedPhase(0, iwListEventConfigurationsOp, func() (int, int, error) {
			return scanIWEventConfigurations(ctx, client, acct, region, st, scanID, parents)
		}),
	})
}

// iwRow is the row of a listed element, stored under its Arn and named
// label. An element without an Arn is not stored: what names the elements
// in the warning, and key (its name or id) names the element.
func iwRow(acct *account, region, scanID, typ, what string, arn *string, label, key string, item any) (*store.Resource, string, string) {
	if sv(arn) == "" {
		return nil, what + " have no Arn", cmp.Or(key, "(an element with no identifier)")
	}
	return &store.Resource{
		Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
		Type: typ, NativeID: sv(arn),
		Name: &label, Region: &region, AttributesJSON: mustJSON(item), DiscoveredBy: scanID,
	}, "", ""
}

// iwIdent is one identifier an event configuration can name its resource by.
type iwIdent struct {
	idType iwtypes.IdentifierType
	id     string
}

// storeIWParentList is storeTokenListTags for a listing whose rows can be
// named by an event configuration: ids gives each row's identifiers, which
// are recorded in parents for every row kept, so that no event configuration
// is linked under a row the tag read dropped. storeParentListTags does not
// fit: it keys each parent on one id, while a wireless device or gateway is
// named by its id and by its EUI, and it warns about rows without that id as
// stored without their children, which an event configuration is not.
func storeIWParentList[Item any](
	ctx context.Context, st *store.Store, acct *account, region, op string,
	page func(ctx context.Context, token *string) ([]Item, *string, error),
	row func(Item) (*store.Resource, string, string),
	ids func(Item) []iwIdent,
	tags rowTagReader, parents iwEventParents, p *phaseTotals,
) (storeWriteFailed bool) {
	var (
		errs   rowTagErrs
		idents = map[*store.Resource][]iwIdent{}
		kept   []*store.Resource
	)
	readTags := tags.hook(ctx, st, acct, region, &errs)
	total, inserted, err := storeTokenListWith(ctx, st, acct, region, op, page, func(it Item) (*store.Resource, string, string) {
		r, why, key := row(it)
		if r != nil {
			idents[r] = ids(it)
		}
		return r, why, key
	}, func(rows []*store.Resource) ([]*store.Resource, func()) {
		var warn func()
		kept, warn = readTags(rows)
		return kept, warn
	})
	for _, r := range kept {
		for _, in := range idents[r] {
			parents.add(in.idType, in.id, r.NativeID)
		}
	}
	return errs.addPhase(p, tags.op, op, total, inserted, err)
}

func scanIWDestinations(ctx context.Context, client iotWirelessAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) bool {
	return storeTokenListTags(ctx, st, acct, region, "iotwireless:ListDestinations",
		func(ctx context.Context, token *string) ([]iwtypes.Destinations, *string, error) {
			out, err := client.ListDestinations(ctx, &iotwireless.ListDestinationsInput{MaxResults: iwPageLimit, NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.DestinationList, out.NextToken, nil
		},
		func(d iwtypes.Destinations) (*store.Resource, string, string) {
			return iwRow(acct, region, scanID, TypeIoTWirelessDestination, "destinations", d.Arn, cmp.Or(sv(d.Name), sv(d.Arn)), sv(d.Name), d)
		}, tags, p)
}

func scanIWDeviceProfiles(ctx context.Context, client iotWirelessAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) bool {
	return storeTokenListTags(ctx, st, acct, region, "iotwireless:ListDeviceProfiles",
		func(ctx context.Context, token *string) ([]iwtypes.DeviceProfile, *string, error) {
			out, err := client.ListDeviceProfiles(ctx, &iotwireless.ListDeviceProfilesInput{MaxResults: iwPageLimit, NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.DeviceProfileList, out.NextToken, nil
		},
		func(d iwtypes.DeviceProfile) (*store.Resource, string, string) {
			return iwRow(acct, region, scanID, TypeIoTWirelessDeviceProfile, "device profiles", d.Arn, cmp.Or(sv(d.Name), sv(d.Id)), cmp.Or(sv(d.Name), sv(d.Id)), d)
		}, tags, p)
}

func scanIWFuotaTasks(ctx context.Context, client iotWirelessAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) bool {
	return storeTokenListTags(ctx, st, acct, region, "iotwireless:ListFuotaTasks",
		func(ctx context.Context, token *string) ([]iwtypes.FuotaTask, *string, error) {
			out, err := client.ListFuotaTasks(ctx, &iotwireless.ListFuotaTasksInput{MaxResults: iwPageLimit, NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.FuotaTaskList, out.NextToken, nil
		},
		func(f iwtypes.FuotaTask) (*store.Resource, string, string) {
			return iwRow(acct, region, scanID, TypeIoTWirelessFuotaTask, "FUOTA tasks", f.Arn, cmp.Or(sv(f.Name), sv(f.Id)), cmp.Or(sv(f.Name), sv(f.Id)), f)
		}, tags, p)
}

func scanIWMulticastGroups(ctx context.Context, client iotWirelessAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) bool {
	return storeTokenListTags(ctx, st, acct, region, "iotwireless:ListMulticastGroups",
		func(ctx context.Context, token *string) ([]iwtypes.MulticastGroup, *string, error) {
			out, err := client.ListMulticastGroups(ctx, &iotwireless.ListMulticastGroupsInput{MaxResults: iwPageLimit, NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.MulticastGroupList, out.NextToken, nil
		},
		func(m iwtypes.MulticastGroup) (*store.Resource, string, string) {
			return iwRow(acct, region, scanID, TypeIoTWirelessMulticastGroup, "multicast groups", m.Arn, cmp.Or(sv(m.Name), sv(m.Id)), cmp.Or(sv(m.Name), sv(m.Id)), m)
		}, tags, p)
}

func scanIWNetworkAnalyzerConfigs(ctx context.Context, client iotWirelessAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) bool {
	return storeTokenListTags(ctx, st, acct, region, "iotwireless:ListNetworkAnalyzerConfigurations",
		func(ctx context.Context, token *string) ([]iwtypes.NetworkAnalyzerConfigurations, *string, error) {
			out, err := client.ListNetworkAnalyzerConfigurations(ctx, &iotwireless.ListNetworkAnalyzerConfigurationsInput{MaxResults: iwPageLimit, NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.NetworkAnalyzerConfigurationList, out.NextToken, nil
		},
		func(n iwtypes.NetworkAnalyzerConfigurations) (*store.Resource, string, string) {
			return iwRow(acct, region, scanID, TypeIoTWirelessNetworkAnalyzerConfiguration, "network analyzer configurations", n.Arn, cmp.Or(sv(n.Name), sv(n.Arn)), sv(n.Name), n)
		}, tags, p)
}

// scanIWPartnerAccounts stores the Sidewalk partner accounts, the only
// partner type the model has. A first page refused with the region-gated
// message is Sidewalk not being offered in this region, and ends the listing
// silently.
func scanIWPartnerAccounts(ctx context.Context, client iotWirelessAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, parents iwEventParents, p *phaseTotals) bool {
	return storeIWParentList(ctx, st, acct, region, "iotwireless:ListPartnerAccounts",
		firstPageGapPages(iwRegionGated,
			func(ctx context.Context, token *string) ([]iwtypes.SidewalkAccountInfoWithFingerprint, *string, error) {
				out, err := client.ListPartnerAccounts(ctx, &iotwireless.ListPartnerAccountsInput{MaxResults: iwPageLimit, NextToken: token})
				if err != nil {
					return nil, nil, err
				}
				return out.Sidewalk, out.NextToken, nil
			}),
		func(s iwtypes.SidewalkAccountInfoWithFingerprint) (*store.Resource, string, string) {
			return iwRow(acct, region, scanID, TypeIoTWirelessPartnerAccount, "partner accounts", s.Arn, cmp.Or(sv(s.AmazonId), sv(s.Arn)), sv(s.AmazonId), s)
		},
		func(s iwtypes.SidewalkAccountInfoWithFingerprint) []iwIdent {
			return []iwIdent{{iwtypes.IdentifierTypePartnerAccountId, sv(s.AmazonId)}}
		}, tags, parents, p)
}

func scanIWServiceProfiles(ctx context.Context, client iotWirelessAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) bool {
	return storeTokenListTags(ctx, st, acct, region, "iotwireless:ListServiceProfiles",
		func(ctx context.Context, token *string) ([]iwtypes.ServiceProfile, *string, error) {
			out, err := client.ListServiceProfiles(ctx, &iotwireless.ListServiceProfilesInput{MaxResults: iwPageLimit, NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.ServiceProfileList, out.NextToken, nil
		},
		func(s iwtypes.ServiceProfile) (*store.Resource, string, string) {
			return iwRow(acct, region, scanID, TypeIoTWirelessServiceProfile, "service profiles", s.Arn, cmp.Or(sv(s.Name), sv(s.Id)), cmp.Or(sv(s.Name), sv(s.Id)), s)
		}, tags, p)
}

func scanIWWirelessDevices(ctx context.Context, client iotWirelessAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, parents iwEventParents, p *phaseTotals) bool {
	return storeIWParentList(ctx, st, acct, region, "iotwireless:ListWirelessDevices",
		func(ctx context.Context, token *string) ([]iwtypes.WirelessDeviceStatistics, *string, error) {
			out, err := client.ListWirelessDevices(ctx, &iotwireless.ListWirelessDevicesInput{MaxResults: iwPageLimit, NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.WirelessDeviceList, out.NextToken, nil
		},
		func(d iwtypes.WirelessDeviceStatistics) (*store.Resource, string, string) {
			return iwRow(acct, region, scanID, TypeIoTWirelessWirelessDevice, "wireless devices", d.Arn, cmp.Or(sv(d.Name), sv(d.Id)), cmp.Or(sv(d.Name), sv(d.Id)), d)
		},
		func(d iwtypes.WirelessDeviceStatistics) []iwIdent {
			ids := []iwIdent{{iwtypes.IdentifierTypeWirelessDeviceId, sv(d.Id)}}
			if d.LoRaWAN != nil {
				ids = append(ids, iwIdent{iwtypes.IdentifierTypeDevEui, sv(d.LoRaWAN.DevEui)})
			}
			return ids
		}, tags, parents, p)
}

func scanIWWirelessDeviceImportTasks(ctx context.Context, client iotWirelessAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) bool {
	return storeTokenListTags(ctx, st, acct, region, "iotwireless:ListWirelessDeviceImportTasks",
		func(ctx context.Context, token *string) ([]iwtypes.WirelessDeviceImportTask, *string, error) {
			out, err := client.ListWirelessDeviceImportTasks(ctx, &iotwireless.ListWirelessDeviceImportTasksInput{MaxResults: iwPageLimit, NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.WirelessDeviceImportTaskList, out.NextToken, nil
		},
		func(t iwtypes.WirelessDeviceImportTask) (*store.Resource, string, string) {
			return iwRow(acct, region, scanID, TypeIoTWirelessWirelessDeviceImportTask, "wireless device import tasks", t.Arn, sv(t.Id), sv(t.Id), t)
		}, tags, p)
}

func scanIWWirelessGateways(ctx context.Context, client iotWirelessAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, parents iwEventParents, p *phaseTotals) bool {
	return storeIWParentList(ctx, st, acct, region, "iotwireless:ListWirelessGateways",
		func(ctx context.Context, token *string) ([]iwtypes.WirelessGatewayStatistics, *string, error) {
			out, err := client.ListWirelessGateways(ctx, &iotwireless.ListWirelessGatewaysInput{MaxResults: iwPageLimit, NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.WirelessGatewayList, out.NextToken, nil
		},
		func(g iwtypes.WirelessGatewayStatistics) (*store.Resource, string, string) {
			return iwRow(acct, region, scanID, TypeIoTWirelessWirelessGateway, "wireless gateways", g.Arn, cmp.Or(sv(g.Name), sv(g.Id)), cmp.Or(sv(g.Name), sv(g.Id)), g)
		},
		func(g iwtypes.WirelessGatewayStatistics) []iwIdent {
			ids := []iwIdent{{iwtypes.IdentifierTypeWirelessGatewayId, sv(g.Id)}}
			if g.LoRaWAN != nil {
				ids = append(ids, iwIdent{iwtypes.IdentifierTypeGatewayEui, sv(g.LoRaWAN.GatewayEui)})
			}
			return ids
		}, tags, parents, p)
}

func scanIWGatewayTaskDefs(ctx context.Context, client iotWirelessAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) bool {
	return storeTokenListTags(ctx, st, acct, region, "iotwireless:ListWirelessGatewayTaskDefinitions",
		func(ctx context.Context, token *string) ([]iwtypes.UpdateWirelessGatewayTaskEntry, *string, error) {
			out, err := client.ListWirelessGatewayTaskDefinitions(ctx, &iotwireless.ListWirelessGatewayTaskDefinitionsInput{MaxResults: iwPageLimit, NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.TaskDefinitions, out.NextToken, nil
		},
		func(t iwtypes.UpdateWirelessGatewayTaskEntry) (*store.Resource, string, string) {
			return iwRow(acct, region, scanID, TypeIoTWirelessTaskDefinition, "task definitions", t.Arn, sv(t.Id), sv(t.Id), t)
		}, tags, p)
}

const (
	iwListEventConfigurationsOp = "iotwireless:ListEventConfigurations"
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
// gateway id that ARN, built in the account's partition, matches the parent
// row's NativeID, the Arn its listing returned. Any other identifier type
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
// pages it read: a Sidewalk region gap on its first page is skipped silently
// as ListPartnerAccounts does (on a later page it is the denial it is); a
// denial warns once for the op; a transient error is tallied into one
// warning; anything else, ValidationException included (the resource type is
// the SDK's own enum value, so it means a bad request), is returned after the
// other types' rows are stored. The warnings are sent only once the rows are
// written.
func scanIWEventConfigurations(ctx context.Context, client iotWirelessAPI, acct *account, region string, st *store.Store, scanID string, parents iwEventParents) (int, int, error) {
	var (
		children  []containedChild
		transient transientTally
		denied    error
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
		case isAccessDenied(err):
			denied = cmp.Or(denied, err)
		case isTransientNetworkError(err):
			transient.skipped(string(rt), err)
		default:
			errs = append(errs, fmt.Errorf("%s %s: %w", iwListEventConfigurationsOp, rt, err))
		}
	}
	total, inserted, err := upsertContained(st, "iotwireless event-configurations", children)
	if errors.Is(err, store.ErrStoreWrite) {
		return total, inserted, errors.Join(append(errs, err)...)
	}
	if denied != nil {
		_ = skipIfAccessDenied(st, iwListEventConfigurationsOp, acct.ID, region, denied)
	}
	transient.warnSkipped(st, iwListEventConfigurationsOp, acct, region)
	return total, inserted, errors.Join(append(errs, err)...)
}

// listIWEventConfigurations lists the event configurations of one resource
// type, returning the items of every page read before an error. The
// region-gated refusal on the first page reads as an empty listing.
func listIWEventConfigurations(ctx context.Context, client iotWirelessAPI, rt iwtypes.EventNotificationResourceType) ([]iwtypes.EventConfigurationItem, error) {
	return readTokenPages(ctx, firstPageGapPages(iwRegionGated, func(ctx context.Context, token *string) ([]iwtypes.EventConfigurationItem, *string, error) {
		out, err := client.ListEventConfigurations(ctx, &iotwireless.ListEventConfigurationsInput{ResourceType: rt, MaxResults: iwPageLimit, NextToken: token})
		if err != nil {
			return nil, nil, err
		}
		return out.EventConfigurationsList, out.NextToken, nil
	}))
}

// iwRegionGated reports whether err is the region-gated refusal.
func iwRegionGated(err error) bool { return isAccessDeniedWithMessage(err, iwRegionGatedMessage) }

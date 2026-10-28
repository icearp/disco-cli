package aws

import (
	"cmp"
	"context"
	"fmt"
	"time"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/iotmanagedintegrations"
	imitypes "github.com/aws/aws-sdk-go-v2/service/iotmanagedintegrations/types"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerType(restype.Descriptor{Type: TypeIoTManagedIntegrationsAccountAssociation, Service: "iotmanagedintegrations"})
	registerType(restype.Descriptor{Type: TypeIoTManagedIntegrationsCredentialLocker, Service: "iotmanagedintegrations"})
	registerType(restype.Descriptor{Type: TypeIoTManagedIntegrationsManagedThing, Service: "iotmanagedintegrations"})
	registerType(restype.Descriptor{Type: TypeIoTManagedIntegrationsOtaTask, Service: "iotmanagedintegrations"})
	registerType(restype.Descriptor{Type: TypeIoTManagedIntegrationsProvisioningProfile, Service: "iotmanagedintegrations"})
	registerType(restype.Descriptor{Type: TypeIoTManagedIntegrationsCloudConnector, Service: "iotmanagedintegrations"})
	registerType(restype.Descriptor{Type: TypeIoTManagedIntegrationsConnectorDestination, Service: "iotmanagedintegrations"})
	registerType(restype.Descriptor{Type: TypeIoTManagedIntegrationsDestination, Service: "iotmanagedintegrations"})
	registerType(restype.Descriptor{Type: TypeIoTManagedIntegrationsEventLogConfiguration, Service: "iotmanagedintegrations"})
	registerType(restype.Descriptor{Type: TypeIoTManagedIntegrationsNotificationConfiguration, Service: "iotmanagedintegrations"})
	registerType(restype.Descriptor{Type: TypeIoTManagedIntegrationsOtaTaskConfiguration, Service: "iotmanagedintegrations"})
	registerType(restype.Descriptor{Type: TypeIoTManagedIntegrationsManagedThingAccountAssociation, Service: "iotmanagedintegrations"})
	registerService(serviceEntry{
		name: "aws:iotmanagedintegrations",
		fn:   scanIoTManagedIntegrations,
	})
}

type iotManagedIntegrationsAPI interface {
	ListAccountAssociations(context.Context, *iotmanagedintegrations.ListAccountAssociationsInput, ...func(*iotmanagedintegrations.Options)) (*iotmanagedintegrations.ListAccountAssociationsOutput, error)
	ListCredentialLockers(context.Context, *iotmanagedintegrations.ListCredentialLockersInput, ...func(*iotmanagedintegrations.Options)) (*iotmanagedintegrations.ListCredentialLockersOutput, error)
	ListManagedThings(context.Context, *iotmanagedintegrations.ListManagedThingsInput, ...func(*iotmanagedintegrations.Options)) (*iotmanagedintegrations.ListManagedThingsOutput, error)
	ListOtaTasks(context.Context, *iotmanagedintegrations.ListOtaTasksInput, ...func(*iotmanagedintegrations.Options)) (*iotmanagedintegrations.ListOtaTasksOutput, error)
	ListProvisioningProfiles(context.Context, *iotmanagedintegrations.ListProvisioningProfilesInput, ...func(*iotmanagedintegrations.Options)) (*iotmanagedintegrations.ListProvisioningProfilesOutput, error)
	ListCloudConnectors(context.Context, *iotmanagedintegrations.ListCloudConnectorsInput, ...func(*iotmanagedintegrations.Options)) (*iotmanagedintegrations.ListCloudConnectorsOutput, error)
	ListConnectorDestinations(context.Context, *iotmanagedintegrations.ListConnectorDestinationsInput, ...func(*iotmanagedintegrations.Options)) (*iotmanagedintegrations.ListConnectorDestinationsOutput, error)
	ListDestinations(context.Context, *iotmanagedintegrations.ListDestinationsInput, ...func(*iotmanagedintegrations.Options)) (*iotmanagedintegrations.ListDestinationsOutput, error)
	ListEventLogConfigurations(context.Context, *iotmanagedintegrations.ListEventLogConfigurationsInput, ...func(*iotmanagedintegrations.Options)) (*iotmanagedintegrations.ListEventLogConfigurationsOutput, error)
	ListNotificationConfigurations(context.Context, *iotmanagedintegrations.ListNotificationConfigurationsInput, ...func(*iotmanagedintegrations.Options)) (*iotmanagedintegrations.ListNotificationConfigurationsOutput, error)
	ListOtaTaskConfigurations(context.Context, *iotmanagedintegrations.ListOtaTaskConfigurationsInput, ...func(*iotmanagedintegrations.Options)) (*iotmanagedintegrations.ListOtaTaskConfigurationsOutput, error)
	ListManagedThingAccountAssociations(context.Context, *iotmanagedintegrations.ListManagedThingAccountAssociationsInput, ...func(*iotmanagedintegrations.Options)) (*iotmanagedintegrations.ListManagedThingAccountAssociationsOutput, error)
	ListTagsForResource(context.Context, *iotmanagedintegrations.ListTagsForResourceInput, ...func(*iotmanagedintegrations.Options)) (*iotmanagedintegrations.ListTagsForResourceOutput, error)
}

const (
	// imiPageCap is the model's MaxResults cap (1 to 1000) of every listing
	// that sends one.
	imiPageCap = 1000

	imiTagsOp = "iotmanagedintegrations:ListTagsForResource"

	// imiUnauthorized is the denial code the model gives ListTagsForResource,
	// ListManagedThings and ListProvisioningProfiles besides (or instead of)
	// AccessDeniedException; isAccessDenied does not match it.
	imiUnauthorized = "UnauthorizedException"
)

func isIMIUnauthorized(err error) bool { return isAPIErrorCode(err, imiUnauthorized) }

// scanIoTManagedIntegrations discovers the managed integrations resources of
// one region. Every listing runs as its own phase, one at a time, children
// after their parents: a contains pair whose parent row is absent is dropped
// with a warning, and the thing-association phase reads the account
// associations and managed things stored by earlier phases. A phase's error
// does not stop the later ones; a failed write and ctx's end do; a region
// whose first listing finds no endpoint runs no other phase (runHostPhases).
// The five types with ARNs have their tags read per row (imiTags), and the
// tag denials of the whole scan are one warning.
//
// Destinations and notification configurations are not tag-read: the
// ListTagsForResource and TagResource models accept only the ARNs of the
// five types above, those two have no ARN, and the Tags of their Create and
// Get operations are marked deprecated in the model.
func scanIoTManagedIntegrations(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	client := iotmanagedintegrations.NewFromConfig(acct.cfg, func(o *iotmanagedintegrations.Options) { o.Region = region })
	tags := imiTags(client).sharingDenials()
	var assocs, things []childParent
	total, inserted, err := runHostPhases(ctx, st, acct, region, "iotmanagedintegrations", 0, []hostPhase{
		{op: "iotmanagedintegrations:ListAccountAssociations", run: func(p *phaseTotals) (failed bool) {
			assocs, failed = scanIMIAccountAssociations(ctx, client, acct, region, st, scanID, tags, p)
			return failed
		}},
		{op: "iotmanagedintegrations:ListCredentialLockers", run: func(p *phaseTotals) bool {
			return scanIMICredentialLockers(ctx, client, acct, region, st, scanID, tags, p)
		}},
		{op: "iotmanagedintegrations:ListManagedThings", run: func(p *phaseTotals) (failed bool) {
			things, failed = scanIMIManagedThings(ctx, client, acct, region, st, scanID, tags, p)
			return failed
		}},
		{op: "iotmanagedintegrations:ListOtaTasks", run: func(p *phaseTotals) bool {
			return scanIMIOtaTasks(ctx, client, acct, region, st, scanID, tags, p)
		}},
		{op: "iotmanagedintegrations:ListProvisioningProfiles", run: func(p *phaseTotals) bool {
			return scanIMIProvisioningProfiles(ctx, client, acct, region, st, scanID, tags, p)
		}},
		countedPhase(0, "iotmanagedintegrations:ListCloudConnectors", func() (int, int, error) {
			return scanIMICloudConnectors(ctx, client, acct, region, st, scanID)
		}),
		countedPhase(0, "iotmanagedintegrations:ListConnectorDestinations", func() (int, int, error) {
			return scanIMIConnectorDestinations(ctx, client, acct, region, st, scanID)
		}),
		countedPhase(0, "iotmanagedintegrations:ListDestinations", func() (int, int, error) {
			return scanIMIDestinations(ctx, client, acct, region, st, scanID)
		}),
		countedPhase(0, "iotmanagedintegrations:ListEventLogConfigurations", func() (int, int, error) {
			return scanIMIEventLogConfigurations(ctx, client, acct, region, st, scanID)
		}),
		countedPhase(0, "iotmanagedintegrations:ListNotificationConfigurations", func() (int, int, error) {
			return scanIMINotificationConfigurations(ctx, client, acct, region, st, scanID)
		}),
		countedPhase(0, "iotmanagedintegrations:ListOtaTaskConfigurations", func() (int, int, error) {
			return scanIMIOtaTaskConfigurations(ctx, client, acct, region, st, scanID)
		}),
		countedPhase(0, imiThingLinksOp, func() (int, int, error) {
			return scanIMIManagedThingAccountAssociations(ctx, client, acct, region, st, scanID, assocs, things)
		}),
	})
	tags.warnDenials(st, acct, region)
	return total, inserted, err
}

// imiTags reads a row's tags with ListTagsForResource, sent the row's
// NativeID, the ARN its list element carries; the op answers with every tag
// at once (no page token). ResourceNotFoundException, its modeled not-found,
// drops a resource deleted after it was listed. The op models
// UnauthorizedException and no AccessDeniedException, so that code is a
// denial too. The listings read here take no cross-account scope in the
// model, so no row is read as another account's (untaggedIfOtherAccount).
func imiTags(client iotManagedIntegrationsAPI) rowTagReader {
	return rowTagReader{
		op:       imiTagsOp,
		notFound: []string{"ResourceNotFoundException"},
		denied:   []string{imiUnauthorized},
		read: func(ctx context.Context, r *store.Resource) (*string, error) {
			out, err := client.ListTagsForResource(ctx, &iotmanagedintegrations.ListTagsForResourceInput{ResourceArn: sdkaws.String(r.NativeID)})
			if err != nil {
				return nil, err
			}
			return mapTagsJSON(out.Tags), nil
		},
	}
}

// imiARNRow is the row of a listed item stored under the ARN it carries, the
// item verbatim as the attributes. An item without an ARN is not stored: one
// empty NativeID would merge every such item into one resource; what names
// the items in that warning, key the item.
func imiARNRow(acct *account, region, scanID, typ, what string, arn, name *string, status string, created *time.Time, key string, item any) (*store.Resource, string, string) {
	if sv(arn) == "" {
		return nil, what + " have an empty ARN", key
	}
	return &store.Resource{
		Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
		Type: typ, NativeID: sv(arn), Name: name, Status: nonEmptyPtr(status),
		Region: &region, CreatedAt: tp(created), AttributesJSON: mustJSON(item), DiscoveredBy: scanID,
	}, "", ""
}

// scanIMIAccountAssociations stores the region's account associations with
// their tags, a page at a time, and returns those stored with an
// AccountAssociationId, the parents of the thing-association phase.
func scanIMIAccountAssociations(ctx context.Context, client iotManagedIntegrationsAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) ([]childParent, bool) {
	return storeParentPagesTags(ctx, st, acct, region, "iotmanagedintegrations:ListAccountAssociations", "account associations", "AccountAssociationId",
		func(ctx context.Context, token *string) ([]imitypes.AccountAssociationItem, *string, error) {
			out, err := client.ListAccountAssociations(ctx, &iotmanagedintegrations.ListAccountAssociationsInput{NextToken: token, MaxResults: sdkaws.Int32(imiPageCap)})
			if err != nil {
				return nil, nil, err
			}
			return out.Items, out.NextToken, nil
		},
		func(a imitypes.AccountAssociationItem) (*store.Resource, string, string) {
			return imiARNRow(acct, region, scanID, TypeIoTManagedIntegrationsAccountAssociation, "account associations",
				a.Arn, a.Name, string(a.AssociationState), nil, cmp.Or(sv(a.Name), sv(a.AccountAssociationId), "(an account association with no name)"), a)
		},
		func(a imitypes.AccountAssociationItem) string { return sv(a.AccountAssociationId) },
		tags, p)
}

// scanIMICredentialLockers stores the region's credential lockers with their
// tags, a page at a time.
func scanIMICredentialLockers(ctx context.Context, client iotManagedIntegrationsAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) (storeWriteFailed bool) {
	return storeTokenPagesTags(ctx, st, acct, region, "iotmanagedintegrations:ListCredentialLockers",
		func(ctx context.Context, token *string) ([]imitypes.CredentialLockerSummary, *string, error) {
			out, err := client.ListCredentialLockers(ctx, &iotmanagedintegrations.ListCredentialLockersInput{NextToken: token, MaxResults: sdkaws.Int32(imiPageCap)})
			if err != nil {
				return nil, nil, err
			}
			return out.Items, out.NextToken, nil
		},
		func(l imitypes.CredentialLockerSummary) (*store.Resource, string, string) {
			return imiARNRow(acct, region, scanID, TypeIoTManagedIntegrationsCredentialLocker, "credential lockers",
				l.Arn, l.Name, "", l.CreatedAt, cmp.Or(sv(l.Name), sv(l.Id), "(a credential locker with no name)"), l)
		}, tags, p)
}

// scanIMIManagedThings stores the region's managed things with their tags, a
// page at a time, and returns those stored with an Id, the parents of the
// thing-association phase. The op models UnauthorizedException as a denial
// beside AccessDeniedException: it ends the listing, keeping the pages read
// before it, with one warning once they are written (listDenial).
func scanIMIManagedThings(ctx context.Context, client iotManagedIntegrationsAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) ([]childParent, bool) {
	const op = "iotmanagedintegrations:ListManagedThings"
	d := listDenial{isDenied: isIMIUnauthorized, unread: "managed things on later pages"}
	parents, storeWriteFailed := storeParentPagesTags(ctx, st, acct, region, op, "managed things", "Id",
		deniedPages(&d, func(ctx context.Context, token *string) ([]imitypes.ManagedThingSummary, *string, error) {
			out, err := client.ListManagedThings(ctx, &iotmanagedintegrations.ListManagedThingsInput{NextToken: token, MaxResults: sdkaws.Int32(imiPageCap)})
			if err != nil {
				return nil, nil, err
			}
			return out.Items, out.NextToken, nil
		}),
		func(t imitypes.ManagedThingSummary) (*store.Resource, string, string) {
			return imiARNRow(acct, region, scanID, TypeIoTManagedIntegrationsManagedThing, "managed things",
				t.Arn, t.Name, string(t.ProvisioningStatus), t.CreatedAt, cmp.Or(sv(t.Name), sv(t.Id), "(a managed thing with no name)"), t)
		},
		func(t imitypes.ManagedThingSummary) string { return sv(t.Id) },
		tags, p)
	// No storeWriteFailed guard: each page is written before the next is
	// asked for, so a failed write ends the listing before any denial, and a
	// denial yields an empty last page that writes nothing.
	d.warn(st, op, acct, region)
	return parents, storeWriteFailed
}

// isIMIOtaEndpointGap reports the answer ListOtaTasks gives an account that
// has not registered a custom endpoint and onboarded a managed thing: a 403
// the SDK reads as UnknownError, "Please register the custom endpoint and
// onboard the managed thing". Such an account has no OTA tasks.
func isIMIOtaEndpointGap(err error) bool {
	return isAPIErrorWithMessage(err, "UnknownError", "register the custom endpoint")
}

// scanIMIOtaTasks stores the region's OTA tasks with their tags, a page at a
// time. An account without the custom endpoint OTA tasks need
// (isIMIOtaEndpointGap) has none: that answer on the first page is an empty
// listing, and on a later page an error.
func scanIMIOtaTasks(ctx context.Context, client iotManagedIntegrationsAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) (storeWriteFailed bool) {
	return storeTokenPagesTags(ctx, st, acct, region, "iotmanagedintegrations:ListOtaTasks",
		firstPageGapPages(isIMIOtaEndpointGap, func(ctx context.Context, token *string) ([]imitypes.OtaTaskSummary, *string, error) {
			out, err := client.ListOtaTasks(ctx, &iotmanagedintegrations.ListOtaTasksInput{NextToken: token, MaxResults: sdkaws.Int32(imiPageCap)})
			if err != nil {
				return nil, nil, err
			}
			return out.Tasks, out.NextToken, nil
		}),
		func(t imitypes.OtaTaskSummary) (*store.Resource, string, string) {
			return imiARNRow(acct, region, scanID, TypeIoTManagedIntegrationsOtaTask, "OTA tasks",
				t.TaskArn, nil, string(t.Status), t.CreatedAt, cmp.Or(sv(t.TaskId), "(an OTA task with no id)"), t)
		}, tags, p)
}

// scanIMIProvisioningProfiles stores the region's provisioning profiles with
// their tags, a page at a time. Its UnauthorizedException is a denial, as for
// scanIMIManagedThings.
func scanIMIProvisioningProfiles(ctx context.Context, client iotManagedIntegrationsAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) (storeWriteFailed bool) {
	const op = "iotmanagedintegrations:ListProvisioningProfiles"
	d := listDenial{isDenied: isIMIUnauthorized, unread: "provisioning profiles on later pages"}
	storeWriteFailed = storeTokenPagesTags(ctx, st, acct, region, op,
		deniedPages(&d, func(ctx context.Context, token *string) ([]imitypes.ProvisioningProfileSummary, *string, error) {
			out, err := client.ListProvisioningProfiles(ctx, &iotmanagedintegrations.ListProvisioningProfilesInput{NextToken: token, MaxResults: sdkaws.Int32(imiPageCap)})
			if err != nil {
				return nil, nil, err
			}
			return out.Items, out.NextToken, nil
		}),
		func(pp imitypes.ProvisioningProfileSummary) (*store.Resource, string, string) {
			return imiARNRow(acct, region, scanID, TypeIoTManagedIntegrationsProvisioningProfile, "provisioning profiles",
				pp.Arn, pp.Name, string(pp.Status), nil, cmp.Or(sv(pp.Name), sv(pp.Id), "(a provisioning profile with no name)"), pp)
		}, tags, p)
	// No storeWriteFailed guard, as in scanIMIManagedThings.
	d.warn(st, op, acct, region)
	return storeWriteFailed
}

// imiARN synthesizes an ARN-shaped NativeID for the types whose List element
// carries no ARN. The shape follows the Service Reference ARN formats of the
// service's ARN-bearing kinds, with the partition pinned to aws
// (arn:{p}:iotmanagedintegrations:{r}:{a}:{kind}/{id});
// none of the kinds passed here is listed there, so these are not AWS ARNs.
func imiARN(partition, region, accountID, kind, id string) string {
	return arnFor(partition, "iotmanagedintegrations", region, accountID, kind+"/"+id)
}

// imiRow is the type-specific part of one stored row: nativeID "" drops the
// item (presence skip); a non-empty parentNativeID records the row as contained
// by that resource.
type imiRow struct {
	nativeID, parentNativeID string
	name                     *string
}

// scanIMIList stores one row per item of an account-wide List op (the item
// verbatim as attributes) through pageScan, then records the contains pairs
// of the rows stored. AccessDenied keeps the pages already stored.
func scanIMIList[P, S any](
	ctx context.Context, acct *account, region string, st *store.Store, scanID, rtype, op string,
	hasMore func() bool, next func(context.Context) (P, error), items func(P) []S, row func(S) imiRow,
) (int, int, error) {
	var pairs [][2]string
	total, inserted, err := pageScan(ctx, op, acct, region, st, hasMore, next, items, func(it S) *store.Resource {
		r := row(it)
		if r.nativeID == "" {
			return nil
		}
		if r.parentNativeID != "" {
			pairs = append(pairs, [2]string{store.ResourceID("aws", acct.ID, r.nativeID), store.ResourceID("aws", acct.ID, r.parentNativeID)})
		}
		return &store.Resource{
			Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
			Type: rtype, NativeID: r.nativeID, Name: r.name,
			Region: &region, AttributesJSON: mustJSON(it), DiscoveredBy: scanID,
		}
	})
	if err != nil {
		return total, inserted, err
	}
	if err := st.RecordHierarchyBatch(pairs); err != nil {
		return total, inserted, fmt.Errorf("closure %s: %w", op, err)
	}
	return total, inserted, nil
}

func scanIMICloudConnectors(ctx context.Context, client iotManagedIntegrationsAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	p := iotmanagedintegrations.NewListCloudConnectorsPaginator(client, &iotmanagedintegrations.ListCloudConnectorsInput{})
	return scanIMIList(ctx, acct, region, st, scanID,
		TypeIoTManagedIntegrationsCloudConnector, "iotmanagedintegrations:ListCloudConnectors",
		p.HasMorePages,
		func(c context.Context) (*iotmanagedintegrations.ListCloudConnectorsOutput, error) {
			return p.NextPage(c)
		},
		func(o *iotmanagedintegrations.ListCloudConnectorsOutput) []imitypes.ConnectorItem { return o.Items },
		func(c imitypes.ConnectorItem) imiRow {
			id := sv(c.Id)
			if id == "" {
				return imiRow{}
			}
			return imiRow{nativeID: imiARN(acct.partition(), region, acct.ID, "cloud-connector", id), name: c.Name}
		})
}

func scanIMIConnectorDestinations(ctx context.Context, client iotManagedIntegrationsAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	p := iotmanagedintegrations.NewListConnectorDestinationsPaginator(client, &iotmanagedintegrations.ListConnectorDestinationsInput{})
	return scanIMIList(ctx, acct, region, st, scanID,
		TypeIoTManagedIntegrationsConnectorDestination, "iotmanagedintegrations:ListConnectorDestinations",
		p.HasMorePages,
		func(c context.Context) (*iotmanagedintegrations.ListConnectorDestinationsOutput, error) {
			return p.NextPage(c)
		},
		func(o *iotmanagedintegrations.ListConnectorDestinationsOutput) []imitypes.ConnectorDestinationSummary {
			return o.ConnectorDestinationList
		},
		func(d imitypes.ConnectorDestinationSummary) imiRow {
			id := sv(d.Id)
			if id == "" {
				return imiRow{}
			}
			r := imiRow{nativeID: imiARN(acct.partition(), region, acct.ID, "connector-destination", id), name: d.Name}
			if connectorID := sv(d.CloudConnectorId); connectorID != "" {
				r.parentNativeID = imiARN(acct.partition(), region, acct.ID, "cloud-connector", connectorID)
			}
			return r
		})
}

// Destinations are addressed by name; DeliveryDestinationArn is the delivery
// target (e.g. a Kinesis stream), which several destinations may share.
func scanIMIDestinations(ctx context.Context, client iotManagedIntegrationsAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	p := iotmanagedintegrations.NewListDestinationsPaginator(client, &iotmanagedintegrations.ListDestinationsInput{})
	return scanIMIList(ctx, acct, region, st, scanID,
		TypeIoTManagedIntegrationsDestination, "iotmanagedintegrations:ListDestinations",
		p.HasMorePages,
		func(c context.Context) (*iotmanagedintegrations.ListDestinationsOutput, error) { return p.NextPage(c) },
		func(o *iotmanagedintegrations.ListDestinationsOutput) []imitypes.DestinationSummary {
			return o.DestinationList
		},
		func(d imitypes.DestinationSummary) imiRow {
			name := sv(d.Name)
			if name == "" {
				return imiRow{}
			}
			return imiRow{nativeID: imiARN(acct.partition(), region, acct.ID, "destination", name), name: d.Name}
		})
}

func scanIMIEventLogConfigurations(ctx context.Context, client iotManagedIntegrationsAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	p := iotmanagedintegrations.NewListEventLogConfigurationsPaginator(client, &iotmanagedintegrations.ListEventLogConfigurationsInput{})
	return scanIMIList(ctx, acct, region, st, scanID,
		TypeIoTManagedIntegrationsEventLogConfiguration, "iotmanagedintegrations:ListEventLogConfigurations",
		p.HasMorePages,
		func(c context.Context) (*iotmanagedintegrations.ListEventLogConfigurationsOutput, error) {
			return p.NextPage(c)
		},
		func(o *iotmanagedintegrations.ListEventLogConfigurationsOutput) []imitypes.EventLogConfigurationSummary {
			return o.EventLogConfigurationList
		},
		func(c imitypes.EventLogConfigurationSummary) imiRow {
			id := sv(c.Id)
			if id == "" {
				return imiRow{}
			}
			return imiRow{nativeID: imiARN(acct.partition(), region, acct.ID, "event-log-configuration", id)}
		})
}

// A notification configuration has no id of its own: the API keys it by event
// type, one per event type per account and region.
func scanIMINotificationConfigurations(ctx context.Context, client iotManagedIntegrationsAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	p := iotmanagedintegrations.NewListNotificationConfigurationsPaginator(client, &iotmanagedintegrations.ListNotificationConfigurationsInput{})
	return scanIMIList(ctx, acct, region, st, scanID,
		TypeIoTManagedIntegrationsNotificationConfiguration, "iotmanagedintegrations:ListNotificationConfigurations",
		p.HasMorePages,
		func(c context.Context) (*iotmanagedintegrations.ListNotificationConfigurationsOutput, error) {
			return p.NextPage(c)
		},
		func(o *iotmanagedintegrations.ListNotificationConfigurationsOutput) []imitypes.NotificationConfigurationSummary {
			return o.NotificationConfigurationList
		},
		func(c imitypes.NotificationConfigurationSummary) imiRow {
			eventType := string(c.EventType)
			if eventType == "" {
				return imiRow{}
			}
			return imiRow{nativeID: imiARN(acct.partition(), region, acct.ID, "notification-configuration", eventType), name: &eventType}
		})
}

func scanIMIOtaTaskConfigurations(ctx context.Context, client iotManagedIntegrationsAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	p := iotmanagedintegrations.NewListOtaTaskConfigurationsPaginator(client, &iotmanagedintegrations.ListOtaTaskConfigurationsInput{})
	return scanIMIList(ctx, acct, region, st, scanID,
		TypeIoTManagedIntegrationsOtaTaskConfiguration, "iotmanagedintegrations:ListOtaTaskConfigurations",
		p.HasMorePages,
		func(c context.Context) (*iotmanagedintegrations.ListOtaTaskConfigurationsOutput, error) {
			return p.NextPage(c)
		},
		func(o *iotmanagedintegrations.ListOtaTaskConfigurationsOutput) []imitypes.OtaTaskConfigurationSummary {
			return o.Items
		},
		func(c imitypes.OtaTaskConfigurationSummary) imiRow {
			id := sv(c.TaskConfigurationId)
			if id == "" {
				return imiRow{}
			}
			return imiRow{nativeID: imiARN(acct.partition(), region, acct.ID, "ota-task-configuration", id), name: c.Name}
		})
}

const imiThingLinksOp = "iotmanagedintegrations:ListManagedThingAccountAssociations"

// scanIMIManagedThingAccountAssociations stores the links between the managed
// things and the account associations stored earlier in this scan, each
// under its thing. The listing is fanned out over the account associations,
// filtered by AccountAssociationId (an account typically has far fewer
// account associations than devices), the associations read concurrently
// (readEachKey) and each classified on its own (parentOutcomes): one
// association's failure costs the others nothing, and the pages it read
// before it are kept. A link has no id of its own; it is keyed by account
// association under its thing's ARN, looked up by the item's ManagedThingId
// among the things stored earlier in this scan. A link whose thing is not
// among them (created after that phase, or on a page the thing listing did
// not read) is warned about and not stored: there is no ARN to key it under,
// and a synthesized one would only produce a contains pair to an absent
// parent. With no thing stored, no association is read.
//
// This is storeTokenEachParent with each row recorded under its thing
// (parentOf), not under the association it was read from: parentOutcomes
// then writes the closure before any warning, so a failed write of the links
// or their closure sends none.
//
// The op models no not-found code, and a code the SDK does not model is
// deliberately not tolerated: if the service rejects the id of an account
// association deleted mid-scan, that error is returned after the other
// associations' links are stored.
func scanIMIManagedThingAccountAssociations(ctx context.Context, client iotManagedIntegrationsAPI, acct *account, region string, st *store.Store, scanID string, assocs, things []childParent) (int, int, error) {
	if len(things) == 0 {
		return 0, 0, nil
	}
	thingARN := make(map[string]string, len(things))
	for _, t := range things {
		thingARN[t.id] = t.arn
	}
	assocIDs := childParentIDs(assocs)
	reads, stopped := readEachKey(ctx, assocIDs, func(ctx context.Context, assoc string) parentItems[imitypes.ManagedThingAssociation] {
		return readParentPages(ctx, assoc, func(ctx context.Context, assoc string, token *string) ([]imitypes.ManagedThingAssociation, *string, error) {
			out, err := client.ListManagedThingAccountAssociations(ctx, &iotmanagedintegrations.ListManagedThingAccountAssociationsInput{
				AccountAssociationId: &assoc, NextToken: token, MaxResults: sdkaws.Int32(imiPageCap),
			})
			if err != nil {
				return nil, nil, err
			}
			return out.Items, out.NextToken, nil
		})
	})
	// The rows are built in association order after the reads, so parentOf
	// needs no lock.
	parentOf := map[*store.Resource]string{}
	out := parentOutcomes{op: imiThingLinksOp, parentOf: parentOf}
	for i, read := range reads {
		addParentRows(&out, assocIDs[i], read, func(assoc string, a imitypes.ManagedThingAssociation) (*store.Resource, string, string) {
			key := cmp.Or(sv(a.ManagedThingId), "(a link with no managed thing id)") + " of account association " + assoc
			// The key uses the queried id, so an item the filter let through
			// for another association would be stored under the wrong one.
			if got := sv(a.AccountAssociationId); got != "" && got != assoc {
				return nil, "managed thing links name an account association other than the one queried", key
			}
			parent := thingARN[sv(a.ManagedThingId)]
			if parent == "" {
				return nil, "managed thing links name a managed thing this scan did not store", key
			}
			r := &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type:     TypeIoTManagedIntegrationsManagedThingAccountAssociation,
				NativeID: parent + "/account-association/" + assoc,
				Status:   nonEmptyPtr(string(a.ManagedThingAssociationStatus)),
				Region:   &region, AttributesJSON: mustJSON(a), DiscoveredBy: scanID,
			}
			parentOf[r] = store.ResourceID("aws", acct.ID, parent)
			return r, "", ""
		})
	}
	out.stopped(stopped)
	return out.store(st, acct, region)
}

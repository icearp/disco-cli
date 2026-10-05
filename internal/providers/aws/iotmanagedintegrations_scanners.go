package aws

import (
	"context"
	"fmt"
	"sync"

	"github.com/aws/aws-sdk-go-v2/service/iotmanagedintegrations"
	imitypes "github.com/aws/aws-sdk-go-v2/service/iotmanagedintegrations/types"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
	"golang.org/x/sync/errgroup"
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
}

func scanIoTManagedIntegrations(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := iotmanagedintegrations.NewFromConfig(acct.cfg, func(o *iotmanagedintegrations.Options) { o.Region = region })

	var (
		assocIDs  []string
		thingARNs map[string]string
	)
	// Children run after their parents: a contains pair whose parent row is
	// absent is dropped with a warning, and the thing-association phase reads
	// the ids the account-association and managed-thing phases return.
	for _, phase := range []func() (int, int, error){
		func() (t, i int, err error) {
			assocIDs, t, i, err = scanIMIAccountAssociations(ctx, client, acct, region, st, scanID)
			return t, i, err
		},
		func() (int, int, error) { return scanIMICredentialLockers(ctx, client, acct, region, st, scanID) },
		func() (t, i int, err error) {
			thingARNs, t, i, err = scanIMIManagedThings(ctx, client, acct, region, st, scanID)
			return t, i, err
		},
		func() (int, int, error) { return scanIMIOtaTasks(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanIMIProvisioningProfiles(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanIMICloudConnectors(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanIMIConnectorDestinations(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanIMIDestinations(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanIMIEventLogConfigurations(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) {
			return scanIMINotificationConfigurations(ctx, client, acct, region, st, scanID)
		},
		func() (int, int, error) { return scanIMIOtaTaskConfigurations(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) {
			return scanIMIManagedThingAccountAssociations(ctx, client, acct, region, st, scanID, assocIDs, thingARNs)
		},
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

// scanIMIAccountAssociations also returns the AccountAssociationIds of the
// stored associations, the fan-out of the thing-association phase.
func scanIMIAccountAssociations(ctx context.Context, client iotManagedIntegrationsAPI, acct *account, region string, st *store.Store, scanID string) ([]string, int, int, error) {
	var (
		batch []*store.Resource
		ids   []string
	)
	var nextToken *string
	for {
		out, perr := client.ListAccountAssociations(ctx, &iotmanagedintegrations.ListAccountAssociationsInput{NextToken: nextToken})
		if perr != nil {
			if isAccessDenied(perr) {
				return nil, 0, 0, skipIfAccessDenied(st, "iotmanagedintegrations:ListAccountAssociations", acct.ID, region, perr)
			}
			return nil, 0, 0, fmt.Errorf("iotmanagedintegrations:ListAccountAssociations: %w", perr)
		}
		for _, a := range out.Items {
			arn := sv(a.Arn)
			if arn == "" {
				continue
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeIoTManagedIntegrationsAccountAssociation, NativeID: arn,
				Name: a.Name, Region: &region,
				AttributesJSON: mustJSON(a), DiscoveredBy: scanID,
			})
			if id := sv(a.AccountAssociationId); id != "" {
				ids = append(ids, id)
			}
		}
		if out.NextToken == nil || *out.NextToken == "" {
			break
		}
		nextToken = out.NextToken
	}
	total, inserted, err := upsertBatch(st, batch, "iotmanagedintegrations account-associations")
	if err != nil {
		return nil, 0, 0, err
	}
	return ids, total, inserted, nil
}

func scanIMICredentialLockers(ctx context.Context, client iotManagedIntegrationsAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var batch []*store.Resource
	var nextToken *string
	for {
		out, perr := client.ListCredentialLockers(ctx, &iotmanagedintegrations.ListCredentialLockersInput{NextToken: nextToken})
		if perr != nil {
			if isAccessDenied(perr) {
				return 0, 0, skipIfAccessDenied(st, "iotmanagedintegrations:ListCredentialLockers", acct.ID, region, perr)
			}
			return 0, 0, fmt.Errorf("iotmanagedintegrations:ListCredentialLockers: %w", perr)
		}
		for _, l := range out.Items {
			arn := sv(l.Arn)
			if arn == "" {
				continue
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeIoTManagedIntegrationsCredentialLocker, NativeID: arn,
				Name: l.Name, Region: &region,
				AttributesJSON: mustJSON(l), DiscoveredBy: scanID,
			})
		}
		if out.NextToken == nil || *out.NextToken == "" {
			break
		}
		nextToken = out.NextToken
	}
	return upsertBatch(st, batch, "iotmanagedintegrations credential-lockers")
}

// scanIMIManagedThings also returns ManagedThingId -> ARN for the stored
// things, the parents of the thing-association phase.
func scanIMIManagedThings(ctx context.Context, client iotManagedIntegrationsAPI, acct *account, region string, st *store.Store, scanID string) (map[string]string, int, int, error) {
	var batch []*store.Resource
	thingARNs := map[string]string{}
	var nextToken *string
	for {
		out, perr := client.ListManagedThings(ctx, &iotmanagedintegrations.ListManagedThingsInput{NextToken: nextToken})
		if perr != nil {
			if isAccessDenied(perr) {
				return nil, 0, 0, skipIfAccessDenied(st, "iotmanagedintegrations:ListManagedThings", acct.ID, region, perr)
			}
			return nil, 0, 0, fmt.Errorf("iotmanagedintegrations:ListManagedThings: %w", perr)
		}
		for _, t := range out.Items {
			arn := sv(t.Arn)
			if arn == "" {
				continue
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeIoTManagedIntegrationsManagedThing, NativeID: arn,
				Name: t.Name, Region: &region,
				AttributesJSON: mustJSON(t), DiscoveredBy: scanID,
			})
			if id := sv(t.Id); id != "" {
				thingARNs[id] = arn
			}
		}
		if out.NextToken == nil || *out.NextToken == "" {
			break
		}
		nextToken = out.NextToken
	}
	total, inserted, err := upsertBatch(st, batch, "iotmanagedintegrations managed-things")
	if err != nil {
		return nil, 0, 0, err
	}
	return thingARNs, total, inserted, nil
}

func scanIMIOtaTasks(ctx context.Context, client iotManagedIntegrationsAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var batch []*store.Resource
	var nextToken *string
	for {
		out, perr := client.ListOtaTasks(ctx, &iotmanagedintegrations.ListOtaTasksInput{NextToken: nextToken})
		if perr != nil {
			if isAccessDenied(perr) {
				return 0, 0, skipIfAccessDenied(st, "iotmanagedintegrations:ListOtaTasks", acct.ID, region, perr)
			}
			// OTA tasks need a registered custom endpoint + onboarded managed
			// thing; unconfigured accounts 403 with "Please register the custom
			// endpoint and onboard the managed thing" — silent per-op skip
			// (sibling IMI phases still scan).
			if isAPIErrorWithMessage(perr, "UnknownError", "register the custom endpoint") {
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("iotmanagedintegrations:ListOtaTasks: %w", perr)
		}
		for _, t := range out.Tasks {
			arn := sv(t.TaskArn)
			if arn == "" {
				continue
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeIoTManagedIntegrationsOtaTask, NativeID: arn,
				Region: &region, AttributesJSON: mustJSON(t), DiscoveredBy: scanID,
			})
		}
		if out.NextToken == nil || *out.NextToken == "" {
			break
		}
		nextToken = out.NextToken
	}
	return upsertBatch(st, batch, "iotmanagedintegrations ota-tasks")
}

func scanIMIProvisioningProfiles(ctx context.Context, client iotManagedIntegrationsAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var batch []*store.Resource
	var nextToken *string
	for {
		out, perr := client.ListProvisioningProfiles(ctx, &iotmanagedintegrations.ListProvisioningProfilesInput{NextToken: nextToken})
		if perr != nil {
			if isAccessDenied(perr) {
				return 0, 0, skipIfAccessDenied(st, "iotmanagedintegrations:ListProvisioningProfiles", acct.ID, region, perr)
			}
			return 0, 0, fmt.Errorf("iotmanagedintegrations:ListProvisioningProfiles: %w", perr)
		}
		for _, p := range out.Items {
			arn := sv(p.Arn)
			if arn == "" {
				continue
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeIoTManagedIntegrationsProvisioningProfile, NativeID: arn,
				Name: p.Name, Region: &region,
				AttributesJSON: mustJSON(p), DiscoveredBy: scanID,
			})
		}
		if out.NextToken == nil || *out.NextToken == "" {
			break
		}
		nextToken = out.NextToken
	}
	return upsertBatch(st, batch, "iotmanagedintegrations provisioning-profiles")
}

// imiARN synthesizes an ARN-shaped NativeID for the types whose List element
// carries no ARN. The shape follows the Service Reference ARN formats of the
// service's ARN-bearing kinds, with the partition pinned to aws
// (arn:aws:iotmanagedintegrations:{r}:{a}:{kind}/{id});
// none of the kinds passed here is listed there, so these are not AWS ARNs.
func imiARN(region, accountID, kind, id string) string {
	return "arn:aws:iotmanagedintegrations:" + region + ":" + accountID + ":" + kind + "/" + id
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
			return imiRow{nativeID: imiARN(region, acct.ID, "cloud-connector", id), name: c.Name}
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
			r := imiRow{nativeID: imiARN(region, acct.ID, "connector-destination", id), name: d.Name}
			if connectorID := sv(d.CloudConnectorId); connectorID != "" {
				r.parentNativeID = imiARN(region, acct.ID, "cloud-connector", connectorID)
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
			return imiRow{nativeID: imiARN(region, acct.ID, "destination", name), name: d.Name}
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
			return imiRow{nativeID: imiARN(region, acct.ID, "event-log-configuration", id)}
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
			return imiRow{nativeID: imiARN(region, acct.ID, "notification-configuration", eventType), name: &eventType}
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
			return imiRow{nativeID: imiARN(region, acct.ID, "ota-task-configuration", id), name: c.Name}
		})
}

// The association list is fanned out over the account associations stored
// earlier in this scan, filtered by AccountAssociationId: an account typically
// has far fewer account associations than devices. A link has no id of its own; it is keyed
// by account association under its thing's ARN, looked up by the item's
// ManagedThingId among the things stored earlier in this scan. An item whose
// thing is not there (ListManagedThings denied, or the thing created after
// that phase) is skipped: there is no ARN to key it under, and a synthesized
// one would only produce a contains pair to an absent parent.
//
// The op models no ResourceNotFound, and a code the SDK does not model is
// deliberately not tolerated: if the service rejects the id of an account
// association deleted mid-scan, that error fails the phase.
func scanIMIManagedThingAccountAssociations(ctx context.Context, client iotManagedIntegrationsAPI, acct *account, region string, st *store.Store, scanID string, assocIDs []string, thingARNs map[string]string) (int, int, error) {
	const op = "iotmanagedintegrations:ListManagedThingAccountAssociations"
	if len(thingARNs) == 0 {
		return 0, 0, nil
	}
	var (
		mu       sync.Mutex
		batch    []*store.Resource
		pairs    [][2]string
		denyOnce sync.Once
	)
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(fanoutMed)
	for _, assocID := range assocIDs {
		g.Go(func() error {
			rows, err := listIMIAssociationLinks(gctx, client, acct, region, scanID, assocID, thingARNs)
			if err != nil && !isAccessDenied(err) {
				return fmt.Errorf("%s %s: %w", op, assocID, err)
			}
			if err != nil {
				denyOnce.Do(func() { _ = skipIfAccessDenied(st, op, acct.ID, region, err) })
			}
			mu.Lock()
			defer mu.Unlock()
			for _, l := range rows {
				batch = append(batch, l.row)
				pairs = append(pairs, [2]string{store.ResourceID("aws", acct.ID, l.row.NativeID), store.ResourceID("aws", acct.ID, l.thingARN)})
			}
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return 0, 0, err
	}
	total, inserted, err := upsertBatch(st, batch, "iotmanagedintegrations managed-thing-account-associations")
	if err != nil {
		return 0, 0, err
	}
	if err := st.RecordHierarchyBatch(pairs); err != nil {
		return 0, 0, fmt.Errorf("closure %s: %w", op, err)
	}
	return total, inserted, nil
}

type imiLink struct {
	row      *store.Resource
	thingARN string
}

// listIMIAssociationLinks returns one account association's links; on error
// it also returns the links from the pages read before it.
func listIMIAssociationLinks(ctx context.Context, client iotManagedIntegrationsAPI, acct *account, region, scanID, assocID string, thingARNs map[string]string) ([]imiLink, error) {
	p := iotmanagedintegrations.NewListManagedThingAccountAssociationsPaginator(client,
		&iotmanagedintegrations.ListManagedThingAccountAssociationsInput{AccountAssociationId: &assocID})
	var links []imiLink
	for p.HasMorePages() {
		out, err := p.NextPage(ctx)
		if err != nil {
			return links, err
		}
		for _, a := range out.Items {
			// The key uses the queried id, so an item the filter let through
			// for another association would be stored under the wrong one.
			if got := sv(a.AccountAssociationId); got != "" && got != assocID {
				continue
			}
			thingARN := thingARNs[sv(a.ManagedThingId)]
			if thingARN == "" {
				continue
			}
			r := &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type:     TypeIoTManagedIntegrationsManagedThingAccountAssociation,
				NativeID: thingARN + "/account-association/" + assocID,
				Region:   &region, AttributesJSON: mustJSON(a), DiscoveredBy: scanID,
			}
			if status := string(a.ManagedThingAssociationStatus); status != "" {
				r.Status = &status
			}
			links = append(links, imiLink{row: r, thingARN: thingARN})
		}
	}
	return links, nil
}

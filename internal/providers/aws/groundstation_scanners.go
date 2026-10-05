package aws

import (
	"context"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/groundstation"
	gstypes "github.com/aws/aws-sdk-go-v2/service/groundstation/types"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerType(restype.Descriptor{Type: TypeGroundStationConfig, Service: "ground-station"})
	registerType(restype.Descriptor{Type: TypeGroundStationDataflowEndpointGroup, Service: "ground-station"})
	registerType(restype.Descriptor{Type: TypeGroundStationMissionProfile, Service: "ground-station"})
	registerType(restype.Descriptor{Type: TypeGroundStationEphemeris, Service: "ground-station"})
	registerService(serviceEntry{
		name: "aws:ground-station",
		fn:   scanGroundStation,
	})
}

type groundStationAPI interface {
	ListConfigs(context.Context, *groundstation.ListConfigsInput, ...func(*groundstation.Options)) (*groundstation.ListConfigsOutput, error)
	ListDataflowEndpointGroups(context.Context, *groundstation.ListDataflowEndpointGroupsInput, ...func(*groundstation.Options)) (*groundstation.ListDataflowEndpointGroupsOutput, error)
	ListMissionProfiles(context.Context, *groundstation.ListMissionProfilesInput, ...func(*groundstation.Options)) (*groundstation.ListMissionProfilesOutput, error)
	ListEphemerides(context.Context, *groundstation.ListEphemeridesInput, ...func(*groundstation.Options)) (*groundstation.ListEphemeridesOutput, error)
}

// scanGroundStation discovers GroundStation configs, dataflow endpoint
// groups, mission profiles and ephemerides. Satellites are scanned once per
// account by groundstation_satellites_scanners.go.
// AWS::GroundStation::DataflowEndpointGroupV2
// is skip-logged: SDK exposes only CreateDataflowEndpointGroupV2, no list
// endpoint.
func scanGroundStation(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := groundstation.NewFromConfig(acct.cfg, func(o *groundstation.Options) { o.Region = region })

	for _, phase := range []func() (int, int, error){
		func() (int, int, error) { return scanGSConfigs(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanGSDataflowEndpointGroups(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanGSMissionProfiles(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanGSEphemerides(ctx, client, acct, region, st, scanID) },
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

func scanGSConfigs(ctx context.Context, client groundStationAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := groundstation.NewListConfigsPaginator(client, &groundstation.ListConfigsInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "groundstation:ListConfigs", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("groundstation:ListConfigs: %w", err)
		}
		for _, c := range out.ConfigList {
			arn := sv(c.ConfigArn)
			if arn == "" {
				continue
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeGroundStationConfig, NativeID: arn,
				Name: c.Name, Region: &region,
				AttributesJSON: mustJSON(c), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "groundstation configs")
}

func scanGSDataflowEndpointGroups(ctx context.Context, client groundStationAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := groundstation.NewListDataflowEndpointGroupsPaginator(client, &groundstation.ListDataflowEndpointGroupsInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "groundstation:ListDataflowEndpointGroups", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("groundstation:ListDataflowEndpointGroups: %w", err)
		}
		for _, d := range out.DataflowEndpointGroupList {
			arn := sv(d.DataflowEndpointGroupArn)
			if arn == "" {
				continue
			}
			id := sv(d.DataflowEndpointGroupId)
			label := id
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeGroundStationDataflowEndpointGroup, NativeID: arn,
				Name: &label, Region: &region,
				AttributesJSON: mustJSON(d), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "groundstation dataflow-endpoint-groups")
}

func scanGSMissionProfiles(ctx context.Context, client groundStationAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := groundstation.NewListMissionProfilesPaginator(client, &groundstation.ListMissionProfilesInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "groundstation:ListMissionProfiles", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("groundstation:ListMissionProfiles: %w", err)
		}
		for _, m := range out.MissionProfileList {
			arn := sv(m.MissionProfileArn)
			if arn == "" {
				continue
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeGroundStationMissionProfile, NativeID: arn,
				Name: m.Name, Region: &region,
				AttributesJSON: mustJSON(m), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "groundstation mission-profiles")
}

// ListEphemerides requires a window and returns the ephemerides whose
// expiration time falls inside it; the fixed epoch-to-2200 window spans every
// plausible expiration. The call is account-wide rather than fanned out per
// satellite because SatelliteId is optional on CreateEphemeris: an ephemeris
// bound to no satellite would be missed by a per-satellite filter. StatusList
// is sent with every known status because the API does not document what
// omitting it returns.
var (
	gsEphemerisWindowStart = time.Unix(0, 0).UTC()
	gsEphemerisWindowEnd   = time.Date(2200, 1, 1, 0, 0, 0, 0, time.UTC)
)

// scanGSEphemerides lists the account's ephemerides. EphemerisItem carries no
// ARN, so NativeID is built in the Service Reference format
// arn:aws:groundstation:{region}:{account}:ephemeris/{id}. Whether ephemerides
// are regional is UNVERIFIED: no AWS source shows an ephemeris ARN or says, and
// a satellite (account-global) reports one current ephemeris across ground
// stations in several regions. If they prove global, this lane stores one
// duplicate row per scanned region and the phase belongs with satellites.
func scanGSEphemerides(ctx context.Context, client groundStationAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := groundstation.NewListEphemeridesPaginator(client, &groundstation.ListEphemeridesInput{
		StartTime:  &gsEphemerisWindowStart,
		EndTime:    &gsEphemerisWindowEnd,
		StatusList: gstypes.EphemerisStatus("").Values(),
	})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				_ = skipIfAccessDenied(st, "groundstation:ListEphemerides", acct.ID, region, err)
				break
			}
			return 0, 0, fmt.Errorf("groundstation:ListEphemerides: %w", err)
		}
		for _, e := range out.Ephemerides {
			id := sv(e.EphemerisId)
			if id == "" {
				continue
			}
			label := sv(e.Name)
			if label == "" {
				label = id
			}
			r := &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type:     TypeGroundStationEphemeris,
				NativeID: "arn:aws:groundstation:" + region + ":" + acct.ID + ":ephemeris/" + id,
				Name:     &label, Region: &region, CreatedAt: tp(e.CreationTime),
				AttributesJSON: mustJSON(e), DiscoveredBy: scanID,
				// Service-managed ephemerides are the defaults AWS derives
				// from Space-Track, not ones the account uploaded.
				ManagedByProvider: e.EphemerisType == gstypes.EphemerisTypeServiceManaged,
			}
			if status := string(e.Status); status != "" {
				r.Status = &status
			}
			batch = append(batch, r)
		}
	}
	return upsertBatch(st, batch, "groundstation ephemerides")
}

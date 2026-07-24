package aws

import (
	"context"
	"fmt"

	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
	"github.com/aws/aws-sdk-go-v2/service/groundstation"
)

func init() {
	registerType(restype.Descriptor{Type: TypeGroundStationConfig, Service: "ground-station", Leaf: true})
	registerType(restype.Descriptor{Type: TypeGroundStationDataflowEndpointGroup, Service: "ground-station", Leaf: true})
	registerType(restype.Descriptor{Type: TypeGroundStationMissionProfile, Service: "ground-station", Leaf: true})
	registerService(serviceEntry{
		name: "aws:ground-station",
		fn:   scanGroundStation,
	})
}

type groundStationAPI interface {
	ListConfigs(context.Context, *groundstation.ListConfigsInput, ...func(*groundstation.Options)) (*groundstation.ListConfigsOutput, error)
	ListDataflowEndpointGroups(context.Context, *groundstation.ListDataflowEndpointGroupsInput, ...func(*groundstation.Options)) (*groundstation.ListDataflowEndpointGroupsOutput, error)
	ListMissionProfiles(context.Context, *groundstation.ListMissionProfilesInput, ...func(*groundstation.Options)) (*groundstation.ListMissionProfilesOutput, error)
}

// scanGroundStation discovers GroundStation configs, dataflow endpoint
// groups, and mission profiles. AWS::GroundStation::DataflowEndpointGroupV2
// is skip-logged: SDK exposes only CreateDataflowEndpointGroupV2, no list
// endpoint.
func scanGroundStation(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := groundstation.NewFromConfig(acct.cfg, func(o *groundstation.Options) { o.Region = region })

	for _, phase := range []func() (int, int, error){
		func() (int, int, error) { return scanGSConfigs(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanGSDataflowEndpointGroups(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanGSMissionProfiles(ctx, client, acct, region, st, scanID) },
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

package aws

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/service/groundstation"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

// Satellites are account-global: ListSatellites returns region-less ARNs
// (arn:aws:groundstation::{account}:satellite/{id}) and every regional endpoint
// lists the same set. Scanned from the regional aws:ground-station lane, each
// region would upsert the same NativeID with its own Region and version-split
// the row several times per scan, so they are scanned once per account from a
// fixed home endpoint instead. DNS cannot pick the home: groundstation.us-east-1,
// us-east-2 and us-west-2 all resolve and all are in the SDK endpoint table.
// us-west-2 hosts ground stations (Oregon 1, Alaska 1, Hawaii 1 in the user
// guide's location table) while us-east-1 hosts none, so it is a full Ground
// Station region rather than an endpoint-only one.
const groundStationSatelliteRegion = "us-west-2"

func init() {
	registerType(restype.Descriptor{Type: TypeGroundStationSatellite, Service: "ground-station"})
	registerService(serviceEntry{
		name:   "aws:ground-station-satellites",
		global: true,
		fn: func(ctx context.Context, acct *account, _ string, st *store.Store, scanID string) (int, int, error) {
			client := groundstation.NewFromConfig(acct.cfg, func(o *groundstation.Options) { o.Region = groundStationSatelliteRegion })
			return scanGSSatellites(ctx, client, acct, st, scanID)
		},
	})
}

type groundStationSatelliteAPI interface {
	ListSatellites(context.Context, *groundstation.ListSatellitesInput, ...func(*groundstation.Options)) (*groundstation.ListSatellitesOutput, error)
}

// scanGSSatellites lists the satellites onboarded to this account. Per the AWS
// Ground Station User Guide every satellite, public broadcast ones such as Aqua
// and Terra included, must be onboarded to an account before it can be used,
// so the list is the account's own and not a public catalog.
func scanGSSatellites(ctx context.Context, client groundStationSatelliteAPI, acct *account, st *store.Store, scanID string) (int, int, error) {
	pager := groundstation.NewListSatellitesPaginator(client, &groundstation.ListSatellitesInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				_ = skipIfAccessDenied(st, "groundstation:ListSatellites", acct.ID, groundStationSatelliteRegion, err)
				break
			}
			return 0, 0, fmt.Errorf("groundstation:ListSatellites: %w", err)
		}
		for _, s := range out.Satellites {
			arn := sv(s.SatelliteArn)
			if arn == "" {
				continue
			}
			label := sv(s.SatelliteId)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeGroundStationSatellite, NativeID: arn,
				Name: &label, Region: regionGlobal,
				AttributesJSON: mustJSON(s), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "groundstation satellites")
}

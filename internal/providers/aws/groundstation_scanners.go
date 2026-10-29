package aws

import (
	"cmp"
	"context"
	"strings"
	"time"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
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
	ListTagsForResource(context.Context, *groundstation.ListTagsForResourceInput, ...func(*groundstation.Options)) (*groundstation.ListTagsForResourceOutput, error)
	DescribeEphemeris(context.Context, *groundstation.DescribeEphemerisInput, ...func(*groundstation.Options)) (*groundstation.DescribeEphemerisOutput, error)
}

const (
	gsListConfigsOp                = "groundstation:ListConfigs"
	gsListDataflowEndpointGroupsOp = "groundstation:ListDataflowEndpointGroups"
	gsListMissionProfilesOp        = "groundstation:ListMissionProfiles"
	gsListEphemeridesOp            = "groundstation:ListEphemerides"
	gsTagsOp                       = "groundstation:ListTagsForResource"
	gsDescribeEphemerisOp          = "groundstation:DescribeEphemeris"
	// gsPageCap is the MaxResults maximum (PaginationMaxResults) the model
	// gives every Ground Station listing read here.
	gsPageCap = 100
)

// gsNotFound is the not-found code ListTagsForResource and DescribeEphemeris
// model, for a resource deleted after it was listed.
var gsNotFound = []string{"ResourceNotFoundException"}

// scanGroundStation discovers Ground Station configs, dataflow endpoint
// groups, mission profiles and ephemerides, each with its tags. Satellites
// are scanned once per account by groundstation_satellites_scanners.go.
// AWS::GroundStation::DataflowEndpointGroupV2 is skip-logged: the SDK
// exposes only CreateDataflowEndpointGroupV2, no list endpoint.
//
// Every listing runs as its own phase, one at a time and in that order: a
// phase's error does not stop the later ones; a failed write and ctx's end
// do, and so does a first phase that stores nothing and fails only with
// NXDOMAIN, since the region does not serve the service (runHostPhases).
// Each listing is written a page at a time, and each tag op's denials are one
// warning for the whole scan.
func scanGroundStation(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	client := groundstation.NewFromConfig(acct.cfg, func(o *groundstation.Options) { o.Region = region })
	tags := gsTags(client).sharingDenials()
	ephemerisTags := gsEphemerisTags(client).sharingDenials()
	total, inserted, err := runHostPhases(ctx, st, acct, region, "groundstation", 0, []hostPhase{
		{op: gsListConfigsOp, run: func(p *phaseTotals) bool {
			return scanGSConfigs(ctx, client, acct, region, st, scanID, tags, p)
		}},
		{op: gsListDataflowEndpointGroupsOp, run: func(p *phaseTotals) bool {
			return scanGSDataflowEndpointGroups(ctx, client, acct, region, st, scanID, tags, p)
		}},
		{op: gsListMissionProfilesOp, run: func(p *phaseTotals) bool {
			return scanGSMissionProfiles(ctx, client, acct, region, st, scanID, tags, p)
		}},
		{op: gsListEphemeridesOp, run: func(p *phaseTotals) bool {
			return scanGSEphemerides(ctx, client, acct, region, st, scanID, ephemerisTags, p)
		}},
	})
	tags.warnDenials(st, acct, region)
	ephemerisTags.warnDenials(st, acct, region)
	return total, inserted, err
}

// gsTags reads a config's, dataflow endpoint group's or mission profile's
// tags with ListTagsForResource, sent the row's NativeID: the ARN its listing
// returned. The Service Reference lists each of those types on
// ListTagsForResource, and each type's Create op takes Tags. The op returns
// every tag at once. ResourceNotFoundException, its modeled not-found, drops
// a resource deleted after it was listed.
func gsTags(client groundStationAPI) rowTagReader {
	return rowTagReader{
		op:       gsTagsOp,
		notFound: gsNotFound,
		read: func(ctx context.Context, r *store.Resource) (*string, error) {
			arn := r.NativeID
			out, err := client.ListTagsForResource(ctx, &groundstation.ListTagsForResourceInput{ResourceArn: &arn})
			if err != nil {
				return nil, err
			}
			return mapTagsJSON(out.Tags), nil
		},
	}
}

// gsEphemerisTags reads an ephemeris's tags from its DescribeEphemeris
// response, which carries them (Tags), sent the ephemeris id. The Service
// Reference leaves ephemerides off ListTagsForResource (TagResource lists
// them), and the ephemeris ARN is built rather than returned, so the op
// that names the ephemeris by its listed id is read instead. Only the tags
// are taken: the row keeps its listed EphemerisItem as its attributes.
// The response models the supplied ephemeris data as unbounded, so each
// read may download the uploaded body; its size is unverified live.
// ResourceNotFoundException, DescribeEphemeris' modeled not-found, drops an
// ephemeris deleted after it was listed.
//
// A service-managed ephemeris (ManagedByProvider) is not read: AWS derives
// it, whether it can carry tags is unverified, and a refusal would fail
// every scan of an account with one. Such a row keeps the TagsJSON its
// builder set.
func gsEphemerisTags(client groundStationAPI) rowTagReader {
	return rowTagReader{
		op:       gsDescribeEphemerisOp,
		notFound: gsNotFound,
		read: func(ctx context.Context, r *store.Resource) (*string, error) {
			if r.ManagedByProvider {
				return r.TagsJSON, nil
			}
			// The NativeID is scanGSEphemerides' ".../ephemeris/{id}", and an
			// ephemeris id is a UUID (model pattern Uuid), so it holds no "/".
			_, id, _ := strings.Cut(r.NativeID, ":ephemeris/")
			out, err := client.DescribeEphemeris(ctx, &groundstation.DescribeEphemerisInput{EphemerisId: &id})
			if err != nil {
				return nil, err
			}
			return mapTagsJSON(out.Tags), nil
		},
	}
}

// gsRow is the row of a listed element of typ under arn, the ARN its
// listing returned, named name. An element without an ARN is not stored
// (arnRow): what names such elements in the listing's warning, key the
// element.
func gsRow(acct *account, region, scanID, typ, what string, arn, name *string, key string, element any) (*store.Resource, string, string) {
	return arnRow(what, arn, &key, &store.Resource{
		Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
		Type: typ, NativeID: sv(arn),
		Name: name, Region: &region,
		AttributesJSON: mustJSON(element), DiscoveredBy: scanID,
	})
}

// scanGSConfigs stores the configs, each page written as soon as its rows'
// tags are read. It reports whether a write failed, after which the scan
// must stop.
func scanGSConfigs(ctx context.Context, client groundStationAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) bool {
	return storeTokenPagesTags(ctx, st, acct, region, gsListConfigsOp,
		func(ctx context.Context, token *string) ([]gstypes.ConfigListItem, *string, error) {
			out, err := client.ListConfigs(ctx, &groundstation.ListConfigsInput{MaxResults: sdkaws.Int32(gsPageCap), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.ConfigList, out.NextToken, nil
		},
		func(c gstypes.ConfigListItem) (*store.Resource, string, string) {
			return gsRow(acct, region, scanID, TypeGroundStationConfig, "configs", c.ConfigArn, c.Name, cmp.Or(sv(c.Name), sv(c.ConfigId)), c)
		}, tags, p)
}

// scanGSDataflowEndpointGroups stores the dataflow endpoint groups, each
// named by its id (the listed element has no name) and each page written as
// soon as its rows' tags are read. It reports whether a write failed, after
// which the scan must stop.
func scanGSDataflowEndpointGroups(ctx context.Context, client groundStationAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) bool {
	return storeTokenPagesTags(ctx, st, acct, region, gsListDataflowEndpointGroupsOp,
		func(ctx context.Context, token *string) ([]gstypes.DataflowEndpointListItem, *string, error) {
			out, err := client.ListDataflowEndpointGroups(ctx, &groundstation.ListDataflowEndpointGroupsInput{MaxResults: sdkaws.Int32(gsPageCap), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.DataflowEndpointGroupList, out.NextToken, nil
		},
		func(d gstypes.DataflowEndpointListItem) (*store.Resource, string, string) {
			label := sv(d.DataflowEndpointGroupId)
			return gsRow(acct, region, scanID, TypeGroundStationDataflowEndpointGroup, "dataflow endpoint groups", d.DataflowEndpointGroupArn, &label, label, d)
		}, tags, p)
}

// scanGSMissionProfiles stores the mission profiles, each page written as
// soon as its rows' tags are read. It reports whether a write failed, after
// which the scan must stop.
func scanGSMissionProfiles(ctx context.Context, client groundStationAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) bool {
	return storeTokenPagesTags(ctx, st, acct, region, gsListMissionProfilesOp,
		func(ctx context.Context, token *string) ([]gstypes.MissionProfileListItem, *string, error) {
			out, err := client.ListMissionProfiles(ctx, &groundstation.ListMissionProfilesInput{MaxResults: sdkaws.Int32(gsPageCap), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.MissionProfileList, out.NextToken, nil
		},
		func(m gstypes.MissionProfileListItem) (*store.Resource, string, string) {
			return gsRow(acct, region, scanID, TypeGroundStationMissionProfile, "mission profiles", m.MissionProfileArn, m.Name, cmp.Or(sv(m.Name), sv(m.MissionProfileId)), m)
		}, tags, p)
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

// scanGSEphemerides stores the account's ephemerides, each page written as
// soon as its rows' tags are read (gsEphemerisTags). EphemerisItem carries no
// ARN, so NativeID is built in the Service Reference format
// arn:{p}:groundstation:{region}:{account}:ephemeris/{id}. Whether ephemerides are
// regional is UNVERIFIED: no AWS source shows an ephemeris ARN or says, and a
// satellite (account-global) reports one current ephemeris across ground
// stations in several regions. If they prove global, this lane stores one
// duplicate row per scanned region and the phase belongs with satellites. It
// reports whether a write failed, after which the scan must stop.
func scanGSEphemerides(ctx context.Context, client groundStationAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) bool {
	return storeTokenPagesTags(ctx, st, acct, region, gsListEphemeridesOp,
		func(ctx context.Context, token *string) ([]gstypes.EphemerisItem, *string, error) {
			out, err := client.ListEphemerides(ctx, &groundstation.ListEphemeridesInput{
				StartTime:  &gsEphemerisWindowStart,
				EndTime:    &gsEphemerisWindowEnd,
				StatusList: gstypes.EphemerisStatus("").Values(),
				MaxResults: sdkaws.Int32(gsPageCap),
				NextToken:  token,
			})
			if err != nil {
				return nil, nil, err
			}
			return out.Ephemerides, out.NextToken, nil
		},
		func(e gstypes.EphemerisItem) (*store.Resource, string, string) {
			id := sv(e.EphemerisId)
			if id == "" {
				return nil, "ephemerides have no id", cmp.Or(sv(e.Name), noIdentifier)
			}
			label := cmp.Or(sv(e.Name), id)
			return &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type:     TypeGroundStationEphemeris,
				NativeID: arnFor(acct.partition(), "groundstation", region, acct.ID, "ephemeris/"+id),
				Name:     &label, Region: &region, CreatedAt: tp(e.CreationTime),
				Status:         nonEmptyPtr(string(e.Status)),
				AttributesJSON: mustJSON(e), DiscoveredBy: scanID,
				// Service-managed ephemerides are the defaults AWS derives
				// from Space-Track, not ones the account uploaded.
				ManagedByProvider: e.EphemerisType == gstypes.EphemerisTypeServiceManaged,
			}, "", ""
		}, tags, p)
}

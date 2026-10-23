package aws

import (
	"context"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/mediapackage"
	mptypes "github.com/aws/aws-sdk-go-v2/service/mediapackage/types"
	"github.com/aws/aws-sdk-go-v2/service/mediapackagevod"
	mpvodtypes "github.com/aws/aws-sdk-go-v2/service/mediapackagevod/types"
	"github.com/icearp/disco-cli/internal/redact"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	// ListChannels returns each ingest endpoint's password in the clear.
	registerType(restype.Descriptor{Type: TypeMediaPackageChannel, Service: "mediapackage", Redact: []redact.Rule{
		{Path: "HlsIngest.IngestEndpoints[*].Password", Mode: redact.RedactScalar},
	}})
	registerType(restype.Descriptor{Type: TypeMediaPackageOriginEndpoint, Service: "mediapackage"})
	registerType(restype.Descriptor{Type: TypeMediaPackageAsset, Service: "mediapackage"})
	registerType(restype.Descriptor{Type: TypeMediaPackagePackagingConfiguration, Service: "mediapackage"})
	registerType(restype.Descriptor{Type: TypeMediaPackagePackagingGroup, Service: "mediapackage"})
	registerService(serviceEntry{
		name: "aws:mediapackage",
		fn:   scanMediaPackage,
	})
}

const (
	mpListChannelsOp                = "mediapackage:ListChannels"
	mpListOriginEndpointsOp         = "mediapackage:ListOriginEndpoints"
	mpListAssetsOp                  = "mediapackagevod:ListAssets"
	mpListPackagingConfigurationsOp = "mediapackagevod:ListPackagingConfigurations"
	mpListPackagingGroupsOp         = "mediapackagevod:ListPackagingGroups"
)

// mpMaxResults is the page cap the Smithy model sets for every listing here.
const mpMaxResults int32 = 1000

type mpV1API interface {
	ListChannels(context.Context, *mediapackage.ListChannelsInput, ...func(*mediapackage.Options)) (*mediapackage.ListChannelsOutput, error)
	ListOriginEndpoints(context.Context, *mediapackage.ListOriginEndpointsInput, ...func(*mediapackage.Options)) (*mediapackage.ListOriginEndpointsOutput, error)
}

type mpVodAPI interface {
	ListAssets(context.Context, *mediapackagevod.ListAssetsInput, ...func(*mediapackagevod.Options)) (*mediapackagevod.ListAssetsOutput, error)
	ListPackagingConfigurations(context.Context, *mediapackagevod.ListPackagingConfigurationsInput, ...func(*mediapackagevod.Options)) (*mediapackagevod.ListPackagingConfigurationsOutput, error)
	ListPackagingGroups(context.Context, *mediapackagevod.ListPackagingGroupsInput, ...func(*mediapackagevod.Options)) (*mediapackagevod.ListPackagingGroupsOutput, error)
}

// scanMediaPackage discovers MediaPackage v1 channels + origin endpoints
// (live), plus VOD assets, packaging configs, packaging groups.
func scanMediaPackage(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	v1 := mediapackage.NewFromConfig(acct.cfg, func(o *mediapackage.Options) { o.Region = region })
	vod := mediapackagevod.NewFromConfig(acct.cfg, func(o *mediapackagevod.Options) { o.Region = region })
	return scanMediaPackageAll(ctx, v1, vod, acct, region, st, scanID)
}

// scanMediaPackageAll runs every listing and joins their errors; only a store
// write failure stops it.
func scanMediaPackageAll(ctx context.Context, v1 mpV1API, vod mpVodAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var phases phaseTotals
	for _, phase := range []struct {
		op string
		fn func() (int, int, error)
	}{
		{mpListChannelsOp, func() (int, int, error) { return scanMPChannels(ctx, v1, acct, region, st, scanID) }},
		{mpListOriginEndpointsOp, func() (int, int, error) { return scanMPOriginEndpoints(ctx, v1, acct, region, st, scanID) }},
		{mpListAssetsOp, func() (int, int, error) { return scanMPAssets(ctx, vod, acct, region, st, scanID) }},
		{mpListPackagingConfigurationsOp, func() (int, int, error) { return scanMPPackagingConfigs(ctx, vod, acct, region, st, scanID) }},
		{mpListPackagingGroupsOp, func() (int, int, error) { return scanMPPackagingGroups(ctx, vod, acct, region, st, scanID) }},
	} {
		t, i, err := phase.fn()
		if phases.addPhase(phase.op, t, i, err) {
			break
		}
	}
	return phases.result(st, acct, region)
}

// mpRow builds the row of a listed MediaPackage element, keyed by its Arn and
// named by its Id; it returns why with the Id as key when the Arn is empty.
func mpRow(acct *account, region, scanID, rtype string, arn, id *string, element any, tags map[string]string, why string) (*store.Resource, string, string) {
	if sv(arn) == "" {
		return nil, why, sv(id)
	}
	return &store.Resource{
		Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
		Type: rtype, NativeID: *arn, Name: id, Region: &region,
		AttributesJSON: mustJSON(element), TagsJSON: mapTagsJSON(tags), DiscoveredBy: scanID,
	}, "", ""
}

func scanMPChannels(ctx context.Context, client mpV1API, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	return storeTokenList(ctx, st, acct, region, mpListChannelsOp,
		func(ctx context.Context, token *string) ([]mptypes.Channel, *string, error) {
			out, err := client.ListChannels(ctx, &mediapackage.ListChannelsInput{NextToken: token, MaxResults: sdkaws.Int32(mpMaxResults)})
			if err != nil {
				return nil, nil, err
			}
			return out.Channels, out.NextToken, nil
		},
		func(c mptypes.Channel) (*store.Resource, string, string) {
			return mpRow(acct, region, scanID, TypeMediaPackageChannel, c.Arn, c.Id, c, c.Tags, "channels have an empty Arn")
		})
}

func scanMPOriginEndpoints(ctx context.Context, client mpV1API, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	return storeTokenList(ctx, st, acct, region, mpListOriginEndpointsOp,
		func(ctx context.Context, token *string) ([]mptypes.OriginEndpoint, *string, error) {
			out, err := client.ListOriginEndpoints(ctx, &mediapackage.ListOriginEndpointsInput{NextToken: token, MaxResults: sdkaws.Int32(mpMaxResults)})
			if err != nil {
				return nil, nil, err
			}
			return out.OriginEndpoints, out.NextToken, nil
		},
		func(e mptypes.OriginEndpoint) (*store.Resource, string, string) {
			return mpRow(acct, region, scanID, TypeMediaPackageOriginEndpoint, e.Arn, e.Id, e, e.Tags, "origin endpoints have an empty Arn")
		})
}

func scanMPAssets(ctx context.Context, client mpVodAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	return storeTokenList(ctx, st, acct, region, mpListAssetsOp,
		func(ctx context.Context, token *string) ([]mpvodtypes.AssetShallow, *string, error) {
			out, err := client.ListAssets(ctx, &mediapackagevod.ListAssetsInput{NextToken: token, MaxResults: sdkaws.Int32(mpMaxResults)})
			if err != nil {
				return nil, nil, err
			}
			return out.Assets, out.NextToken, nil
		},
		func(a mpvodtypes.AssetShallow) (*store.Resource, string, string) {
			return mpRow(acct, region, scanID, TypeMediaPackageAsset, a.Arn, a.Id, a, a.Tags, "assets have an empty Arn")
		})
}

func scanMPPackagingConfigs(ctx context.Context, client mpVodAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	return storeTokenList(ctx, st, acct, region, mpListPackagingConfigurationsOp,
		func(ctx context.Context, token *string) ([]mpvodtypes.PackagingConfiguration, *string, error) {
			out, err := client.ListPackagingConfigurations(ctx, &mediapackagevod.ListPackagingConfigurationsInput{NextToken: token, MaxResults: sdkaws.Int32(mpMaxResults)})
			if err != nil {
				return nil, nil, err
			}
			return out.PackagingConfigurations, out.NextToken, nil
		},
		func(p mpvodtypes.PackagingConfiguration) (*store.Resource, string, string) {
			return mpRow(acct, region, scanID, TypeMediaPackagePackagingConfiguration, p.Arn, p.Id, p, p.Tags, "packaging configurations have an empty Arn")
		})
}

func scanMPPackagingGroups(ctx context.Context, client mpVodAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	return storeTokenList(ctx, st, acct, region, mpListPackagingGroupsOp,
		func(ctx context.Context, token *string) ([]mpvodtypes.PackagingGroup, *string, error) {
			out, err := client.ListPackagingGroups(ctx, &mediapackagevod.ListPackagingGroupsInput{NextToken: token, MaxResults: sdkaws.Int32(mpMaxResults)})
			if err != nil {
				return nil, nil, err
			}
			return out.PackagingGroups, out.NextToken, nil
		},
		func(p mpvodtypes.PackagingGroup) (*store.Resource, string, string) {
			return mpRow(acct, region, scanID, TypeMediaPackagePackagingGroup, p.Arn, p.Id, p, p.Tags, "packaging groups have an empty Arn")
		})
}

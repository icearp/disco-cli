package aws

import (
	"context"
	"fmt"

	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
	"github.com/aws/aws-sdk-go-v2/service/mediapackage"
	"github.com/aws/aws-sdk-go-v2/service/mediapackagevod"
)

func init() {
	registerType(restype.Descriptor{Type: TypeMediaPackageChannel, Service: "mediapackage", Leaf: true})
	registerType(restype.Descriptor{Type: TypeMediaPackageOriginEndpoint, Service: "mediapackage"})
	registerType(restype.Descriptor{Type: TypeMediaPackageAsset, Service: "mediapackage"})
	registerType(restype.Descriptor{Type: TypeMediaPackagePackagingConfiguration, Service: "mediapackage"})
	registerType(restype.Descriptor{Type: TypeMediaPackagePackagingGroup, Service: "mediapackage", Leaf: true})
	registerService(serviceEntry{
		name: "aws:mediapackage",
		fn:   scanMediaPackage,
	})
}

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
func scanMediaPackage(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	v1 := mediapackage.NewFromConfig(acct.cfg, func(o *mediapackage.Options) { o.Region = region })
	vod := mediapackagevod.NewFromConfig(acct.cfg, func(o *mediapackagevod.Options) { o.Region = region })

	for _, phase := range []func() (int, int, error){
		func() (int, int, error) { return scanMPChannels(ctx, v1, acct, region, st, scanID) },
		func() (int, int, error) { return scanMPOriginEndpoints(ctx, v1, acct, region, st, scanID) },
		func() (int, int, error) { return scanMPAssets(ctx, vod, acct, region, st, scanID) },
		func() (int, int, error) { return scanMPPackagingConfigs(ctx, vod, acct, region, st, scanID) },
		func() (int, int, error) { return scanMPPackagingGroups(ctx, vod, acct, region, st, scanID) },
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

func scanMPChannels(ctx context.Context, client mpV1API, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := mediapackage.NewListChannelsPaginator(client, &mediapackage.ListChannelsInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "mediapackage:ListChannels", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("mediapackage:ListChannels: %w", err)
		}
		for _, c := range out.Channels {
			arn := sv(c.Arn)
			if arn == "" {
				continue
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeMediaPackageChannel, NativeID: arn,
				Name: c.Id, Region: &region,
				AttributesJSON: mustJSON(c), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "mediapackage channels")
}

func scanMPOriginEndpoints(ctx context.Context, client mpV1API, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := mediapackage.NewListOriginEndpointsPaginator(client, &mediapackage.ListOriginEndpointsInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "mediapackage:ListOriginEndpoints", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("mediapackage:ListOriginEndpoints: %w", err)
		}
		for _, e := range out.OriginEndpoints {
			arn := sv(e.Arn)
			if arn == "" {
				continue
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeMediaPackageOriginEndpoint, NativeID: arn,
				Name: e.Id, Region: &region,
				AttributesJSON: mustJSON(e), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "mediapackage origin-endpoints")
}

func scanMPAssets(ctx context.Context, client mpVodAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := mediapackagevod.NewListAssetsPaginator(client, &mediapackagevod.ListAssetsInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "mediapackagevod:ListAssets", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("mediapackagevod:ListAssets: %w", err)
		}
		for _, a := range out.Assets {
			arn := sv(a.Arn)
			if arn == "" {
				continue
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeMediaPackageAsset, NativeID: arn,
				Name: a.Id, Region: &region,
				AttributesJSON: mustJSON(a), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "mediapackage assets")
}

func scanMPPackagingConfigs(ctx context.Context, client mpVodAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := mediapackagevod.NewListPackagingConfigurationsPaginator(client, &mediapackagevod.ListPackagingConfigurationsInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "mediapackagevod:ListPackagingConfigurations", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("mediapackagevod:ListPackagingConfigurations: %w", err)
		}
		for _, p := range out.PackagingConfigurations {
			arn := sv(p.Arn)
			if arn == "" {
				continue
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeMediaPackagePackagingConfiguration, NativeID: arn,
				Name: p.Id, Region: &region,
				AttributesJSON: mustJSON(p), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "mediapackage packaging-configurations")
}

func scanMPPackagingGroups(ctx context.Context, client mpVodAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := mediapackagevod.NewListPackagingGroupsPaginator(client, &mediapackagevod.ListPackagingGroupsInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "mediapackagevod:ListPackagingGroups", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("mediapackagevod:ListPackagingGroups: %w", err)
		}
		for _, p := range out.PackagingGroups {
			arn := sv(p.Arn)
			if arn == "" {
				continue
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeMediaPackagePackagingGroup, NativeID: arn,
				Name: p.Id, Region: &region,
				AttributesJSON: mustJSON(p), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "mediapackage packaging-groups")
}

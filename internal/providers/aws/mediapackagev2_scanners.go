package aws

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/service/mediapackagev2"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerType(restype.Descriptor{Type: TypeMediaPackageV2ChannelGroup, Service: "mediapackagev2", Leaf: true})
	registerType(restype.Descriptor{Type: TypeMediaPackageV2Channel, Service: "mediapackagev2"})
	registerType(restype.Descriptor{Type: TypeMediaPackageV2ChannelPolicy, Service: "mediapackagev2"})
	registerType(restype.Descriptor{Type: TypeMediaPackageV2OriginEndpoint, Service: "mediapackagev2"})
	registerType(restype.Descriptor{Type: TypeMediaPackageV2OriginEndpointPolicy, Service: "mediapackagev2"})
	registerService(serviceEntry{
		name: "aws:mediapackagev2",
		fn:   scanMediaPackageV2,
	})
}

type mediaPackageV2API interface {
	ListChannelGroups(context.Context, *mediapackagev2.ListChannelGroupsInput, ...func(*mediapackagev2.Options)) (*mediapackagev2.ListChannelGroupsOutput, error)
	ListChannels(context.Context, *mediapackagev2.ListChannelsInput, ...func(*mediapackagev2.Options)) (*mediapackagev2.ListChannelsOutput, error)
	ListOriginEndpoints(context.Context, *mediapackagev2.ListOriginEndpointsInput, ...func(*mediapackagev2.Options)) (*mediapackagev2.ListOriginEndpointsOutput, error)
	GetChannelPolicy(context.Context, *mediapackagev2.GetChannelPolicyInput, ...func(*mediapackagev2.Options)) (*mediapackagev2.GetChannelPolicyOutput, error)
	GetOriginEndpointPolicy(context.Context, *mediapackagev2.GetOriginEndpointPolicyInput, ...func(*mediapackagev2.Options)) (*mediapackagev2.GetOriginEndpointPolicyOutput, error)
}

// scanMediaPackageV2 discovers MediaPackage v2 channel groups, channels,
// origin endpoints, and per-channel / per-origin-endpoint IAM policies.
func scanMediaPackageV2(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := mediapackagev2.NewFromConfig(acct.cfg, func(o *mediapackagev2.Options) { o.Region = region })

	groupNames, t, i, ferr := scanMP2ChannelGroups(ctx, client, acct, region, st, scanID)
	if ferr != nil {
		return total, inserted, ferr
	}
	total += t
	inserted += i

	type chRef struct{ group, name string }
	var channels []chRef
	for _, g := range groupNames {
		refs, tt, ii, cerr := scanMP2Channels(ctx, client, acct, region, st, scanID, g)
		if cerr != nil {
			return total, inserted, cerr
		}
		total += tt
		inserted += ii
		for _, r := range refs {
			channels = append(channels, chRef{g, r})
		}
	}

	type oeRef struct{ group, channel, name string }
	var origins []oeRef
	for _, c := range channels {
		t, i, ferr = scanMP2ChannelPolicy(ctx, client, acct, region, st, scanID, c.group, c.name)
		if ferr != nil {
			return total, inserted, ferr
		}
		total += t
		inserted += i

		refs, tt, ii, oerr := scanMP2OriginEndpoints(ctx, client, acct, region, st, scanID, c.group, c.name)
		if oerr != nil {
			return total, inserted, oerr
		}
		total += tt
		inserted += ii
		for _, r := range refs {
			origins = append(origins, oeRef{c.group, c.name, r})
		}
	}

	for _, o := range origins {
		t, i, ferr = scanMP2OriginEndpointPolicy(ctx, client, acct, region, st, scanID, o.group, o.channel, o.name)
		if ferr != nil {
			return total, inserted, ferr
		}
		total += t
		inserted += i
	}
	return total, inserted, nil
}

func scanMP2ChannelGroups(ctx context.Context, client mediaPackageV2API, acct *account, region string, st *store.Store, scanID string) ([]string, int, int, error) {
	pager := mediapackagev2.NewListChannelGroupsPaginator(client, &mediapackagev2.ListChannelGroupsInput{})
	var batch []*store.Resource
	var names []string
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return nil, 0, 0, skipIfAccessDenied(st, "mediapackagev2:ListChannelGroups", acct.ID, region, err)
			}
			return nil, 0, 0, fmt.Errorf("mediapackagev2:ListChannelGroups: %w", err)
		}
		for _, g := range out.Items {
			arn := sv(g.Arn)
			if arn == "" {
				continue
			}
			if n := sv(g.ChannelGroupName); n != "" {
				names = append(names, n)
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeMediaPackageV2ChannelGroup, NativeID: arn,
				Name: g.ChannelGroupName, Region: &region,
				AttributesJSON: mustJSON(g), DiscoveredBy: scanID,
			})
		}
	}
	t, i, err := upsertBatch(st, batch, "mediapackagev2 channel-groups")
	return names, t, i, err
}

func scanMP2Channels(ctx context.Context, client mediaPackageV2API, acct *account, region string, st *store.Store, scanID string, groupName string) ([]string, int, int, error) {
	gn := groupName
	pager := mediapackagev2.NewListChannelsPaginator(client, &mediapackagev2.ListChannelsInput{ChannelGroupName: &gn})
	var batch []*store.Resource
	var names []string
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return nil, 0, 0, skipIfAccessDenied(st, "mediapackagev2:ListChannels", acct.ID, region, err)
			}
			return nil, 0, 0, fmt.Errorf("mediapackagev2:ListChannels: %w", err)
		}
		for _, c := range out.Items {
			arn := sv(c.Arn)
			if arn == "" {
				continue
			}
			if n := sv(c.ChannelName); n != "" {
				names = append(names, n)
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeMediaPackageV2Channel, NativeID: arn,
				Name: c.ChannelName, Region: &region,
				AttributesJSON: mustJSON(c), DiscoveredBy: scanID,
			})
		}
	}
	t, i, err := upsertBatch(st, batch, "mediapackagev2 channels")
	return names, t, i, err
}

// scanMP2ChannelPolicy synthesizes ARN: parent channel ARN + /policy.
func scanMP2ChannelPolicy(ctx context.Context, client mediaPackageV2API, acct *account, region string, st *store.Store, scanID string, groupName, channelName string) (int, int, error) {
	gn, cn := groupName, channelName
	out, err := client.GetChannelPolicy(ctx, &mediapackagev2.GetChannelPolicyInput{ChannelGroupName: &gn, ChannelName: &cn})
	if err != nil {
		if isAccessDenied(err) || isAPIErrorCode(err, "ResourceNotFoundException") {
			return 0, 0, nil
		}
		return 0, 0, fmt.Errorf("mediapackagev2:GetChannelPolicy: %w", err)
	}
	if sv(out.Policy) == "" {
		return 0, 0, nil
	}
	arn := fmt.Sprintf("arn:aws:mediapackagev2:%s:%s:channelGroup/%s/channel/%s/policy", region, acct.ID, gn, cn)
	label := cn
	r := &store.Resource{
		Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
		Type: TypeMediaPackageV2ChannelPolicy, NativeID: arn,
		Name: &label, Region: &region,
		AttributesJSON: mustJSON(out), DiscoveredBy: scanID,
	}
	return upsertBatch(st, []*store.Resource{r}, "mediapackagev2 channel-policies")
}

func scanMP2OriginEndpoints(ctx context.Context, client mediaPackageV2API, acct *account, region string, st *store.Store, scanID string, groupName, channelName string) ([]string, int, int, error) {
	gn, cn := groupName, channelName
	pager := mediapackagev2.NewListOriginEndpointsPaginator(client, &mediapackagev2.ListOriginEndpointsInput{ChannelGroupName: &gn, ChannelName: &cn})
	var batch []*store.Resource
	var names []string
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return nil, 0, 0, skipIfAccessDenied(st, "mediapackagev2:ListOriginEndpoints", acct.ID, region, err)
			}
			return nil, 0, 0, fmt.Errorf("mediapackagev2:ListOriginEndpoints: %w", err)
		}
		for _, e := range out.Items {
			arn := sv(e.Arn)
			if arn == "" {
				continue
			}
			if n := sv(e.OriginEndpointName); n != "" {
				names = append(names, n)
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeMediaPackageV2OriginEndpoint, NativeID: arn,
				Name: e.OriginEndpointName, Region: &region,
				AttributesJSON: mustJSON(e), DiscoveredBy: scanID,
			})
		}
	}
	t, i, err := upsertBatch(st, batch, "mediapackagev2 origin-endpoints")
	return names, t, i, err
}

func scanMP2OriginEndpointPolicy(ctx context.Context, client mediaPackageV2API, acct *account, region string, st *store.Store, scanID string, groupName, channelName, endpointName string) (int, int, error) {
	gn, cn, en := groupName, channelName, endpointName
	out, err := client.GetOriginEndpointPolicy(ctx, &mediapackagev2.GetOriginEndpointPolicyInput{
		ChannelGroupName:   &gn,
		ChannelName:        &cn,
		OriginEndpointName: &en,
	})
	if err != nil {
		if isAccessDenied(err) || isAPIErrorCode(err, "ResourceNotFoundException") {
			return 0, 0, nil
		}
		return 0, 0, fmt.Errorf("mediapackagev2:GetOriginEndpointPolicy: %w", err)
	}
	if sv(out.Policy) == "" {
		return 0, 0, nil
	}
	arn := fmt.Sprintf("arn:aws:mediapackagev2:%s:%s:channelGroup/%s/channel/%s/originEndpoint/%s/policy", region, acct.ID, gn, cn, en)
	label := en
	r := &store.Resource{
		Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
		Type: TypeMediaPackageV2OriginEndpointPolicy, NativeID: arn,
		Name: &label, Region: &region,
		AttributesJSON: mustJSON(out), DiscoveredBy: scanID,
	}
	return upsertBatch(st, []*store.Resource{r}, "mediapackagev2 origin-endpoint-policies")
}

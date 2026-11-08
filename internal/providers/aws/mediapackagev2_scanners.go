package aws

import (
	"context"
	"sync"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/mediapackagev2"
	mp2types "github.com/aws/aws-sdk-go-v2/service/mediapackagev2/types"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerType(restype.Descriptor{Type: TypeMediaPackageV2ChannelGroup, Service: "mediapackagev2"})
	registerType(restype.Descriptor{Type: TypeMediaPackageV2Channel, Service: "mediapackagev2"})
	registerType(restype.Descriptor{Type: TypeMediaPackageV2ChannelPolicy, Service: "mediapackagev2", Untaggable: true})
	registerType(restype.Descriptor{Type: TypeMediaPackageV2OriginEndpoint, Service: "mediapackagev2"})
	registerType(restype.Descriptor{Type: TypeMediaPackageV2OriginEndpointPolicy, Service: "mediapackagev2", Untaggable: true})
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
	ListTagsForResource(context.Context, *mediapackagev2.ListTagsForResourceInput, ...func(*mediapackagev2.Options)) (*mediapackagev2.ListTagsForResourceOutput, error)
}

const (
	mp2TagsOp = "mediapackagev2:ListTagsForResource"
	// mp2PageCap is the MaxResults every listing sends: the maximum of the
	// range the model gives all three (1-100; the service's default is 10).
	mp2PageCap = 100
)

// mp2Gone is the not-found code ListChannels, ListOriginEndpoints,
// GetChannelPolicy and GetOriginEndpointPolicy model: for a listing, its
// parent was deleted since it was listed; for a policy read, the channel or
// origin endpoint may have no policy or may have been deleted, and either
// way there is no policy to store.
var mp2Gone = []string{"ResourceNotFoundException"}

// mp2Path is the names a stored channel or origin endpoint's child calls are
// sent, which take no ARN: its channel group's, its channel's and, for an
// origin endpoint, its own.
type mp2Path struct{ group, channel, endpoint string }

// mp2Paths holds the path of each channel and origin endpoint listed this
// scan, by its ARN, the id its child phases key on. Rows are built on
// concurrent per-parent reads, so it is locked.
type mp2Paths struct {
	mu    sync.Mutex
	byARN map[string]mp2Path
}

func (p *mp2Paths) noteMP2Path(arn string, path mp2Path) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.byARN[arn] = path
}

func (p *mp2Paths) mp2PathOf(arn string) mp2Path {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.byARN[arn]
}

// scanMediaPackageV2 discovers the MediaPackage v2 channel groups of one
// region, each group's channels, each channel's policy and origin endpoints,
// and each origin endpoint's policy. Every listing runs as its own phase, one
// at a time: a phase's error does not stop the later ones; a failed write and
// ctx's end do (runHostPhases). Each child phase reads the parents stored
// this scan, those parents concurrently. Channel groups, channels and origin
// endpoints have their tags read per row (mp2Tags), whose denials are one
// warning for the whole scan.
func scanMediaPackageV2(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (int, error) {
	client := mediapackagev2.NewFromConfig(acct.cfg, func(o *mediapackagev2.Options) { o.Region = region })
	tags := mp2Tags(client).sharingDenials()
	paths := &mp2Paths{byARN: map[string]mp2Path{}}
	var groups, channels, endpoints []childParent
	total, err := runHostPhases(ctx, st, acct, region, "mediapackagev2", 0, []hostPhase{
		{op: "mediapackagev2:ListChannelGroups", run: func(p *phaseTotals) (failed bool) {
			groups, failed = scanMP2ChannelGroups(ctx, client, acct, region, st, scanID, tags, p)
			return failed
		}},
		{op: "mediapackagev2:ListChannels", run: func(p *phaseTotals) (failed bool) {
			channels, failed = scanMP2Channels(ctx, client, acct, region, st, scanID, groups, paths, tags, p)
			return failed
		}},
		{op: "mediapackagev2:GetChannelPolicy", run: func(p *phaseTotals) bool {
			return scanMP2ChannelPolicies(ctx, client, acct, region, st, scanID, channels, paths, p)
		}},
		{op: "mediapackagev2:ListOriginEndpoints", run: func(p *phaseTotals) (failed bool) {
			endpoints, failed = scanMP2OriginEndpoints(ctx, client, acct, region, st, scanID, channels, paths, tags, p)
			return failed
		}},
		{op: "mediapackagev2:GetOriginEndpointPolicy", run: func(p *phaseTotals) bool {
			return scanMP2OriginEndpointPolicies(ctx, client, acct, region, st, scanID, endpoints, paths, p)
		}},
	})
	tags.warnDenials(st, acct, region)
	return total, err
}

// mp2Tags reads a row's tags with ListTagsForResource, sent the row's
// NativeID: the ARN its listing returned. The Service Reference lists the
// channel group, channel and origin endpoint (and the harvest job, not
// scanned) under ListTagsForResource, and each Create* input carries tags.
// The op does not page its tags. Its model lists no not-found code, so a
// resource deleted after it was listed keeps its row untagged and fails the
// read (which code AWS answers it with is unverified live). No element of
// these listings names an owning account and the service models no sharing,
// so every row is taken as this account's own.
func mp2Tags(client mediaPackageV2API) rowTagReader {
	return rowTagReader{
		op: mp2TagsOp,
		read: func(ctx context.Context, r *store.Resource) (*string, error) {
			out, err := client.ListTagsForResource(ctx, &mediapackagev2.ListTagsForResourceInput{ResourceArn: sdkaws.String(r.NativeID)})
			if err != nil {
				return nil, err
			}
			return mapTagsJSON(out.Tags), nil
		},
	}
}

// mp2Row is the row of a listed element stored under the ARN it carries, the
// element verbatim as the attributes; an element without an ARN is not
// stored (arnRow). what names the listing's elements.
func mp2Row(acct *account, region, scanID, typ, what string, arn, name *string, item any) (*store.Resource, string, string) {
	return arnRow(what, arn, name, &store.Resource{
		Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
		Type: typ, NativeID: sv(arn),
		Name: name, Region: &region, AttributesJSON: mustJSON(item), DiscoveredBy: scanID,
	})
}

// scanMP2ChannelGroups stores the channel groups with their tags, a page at
// a time, and returns the stored ones that have a name, the parents of the
// channel phase, each keyed by that name. It reports whether a write failed,
// after which the scan must stop.
func scanMP2ChannelGroups(ctx context.Context, client mediaPackageV2API, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) ([]childParent, bool) {
	return storeParentPagesTags(ctx, st, acct, region, "mediapackagev2:ListChannelGroups", "channel groups", "ChannelGroupName",
		func(ctx context.Context, token *string) ([]mp2types.ChannelGroupListConfiguration, *string, error) {
			out, err := client.ListChannelGroups(ctx, &mediapackagev2.ListChannelGroupsInput{MaxResults: sdkaws.Int32(mp2PageCap), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.Items, out.NextToken, nil
		},
		func(g mp2types.ChannelGroupListConfiguration) (*store.Resource, string, string) {
			return mp2Row(acct, region, scanID, TypeMediaPackageV2ChannelGroup, "channel groups", g.Arn, g.ChannelGroupName, g)
		},
		func(g mp2types.ChannelGroupListConfiguration) string { return sv(g.ChannelGroupName) },
		tags, p)
}

// scanMP2Channels stores each channel group's channels with their tags, each
// page written as soon as its tags are read; one group's failure costs the
// others none of theirs. It returns the stored channels that have a name, the
// parents of the channel policy and origin endpoint phases, each keyed by its
// ARN with its path noted in paths. It reports whether a write failed, after
// which the scan must stop.
func scanMP2Channels(ctx context.Context, client mediaPackageV2API, acct *account, region string, st *store.Store, scanID string, groups []childParent, paths *mp2Paths, tags rowTagReader, p *phaseTotals) ([]childParent, bool) {
	return storeNestedParentPagesTags(ctx, st, acct, region, "mediapackagev2:ListChannels", "channels", "ChannelName", mp2Gone, groups,
		func(ctx context.Context, group string, token *string) ([]mp2types.ChannelListConfiguration, *string, error) {
			out, err := client.ListChannels(ctx, &mediapackagev2.ListChannelsInput{ChannelGroupName: &group, MaxResults: sdkaws.Int32(mp2PageCap), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.Items, out.NextToken, nil
		},
		func(group string, c mp2types.ChannelListConfiguration) (*store.Resource, string, string) {
			r, why, key := mp2Row(acct, region, scanID, TypeMediaPackageV2Channel, "channels", c.Arn, c.ChannelName, c)
			if r != nil {
				paths.noteMP2Path(r.NativeID, mp2Path{group: group, channel: sv(c.ChannelName)})
			}
			return r, why, key
		},
		func(c mp2types.ChannelListConfiguration) string {
			if sv(c.ChannelName) == "" {
				return ""
			}
			return sv(c.Arn)
		},
		tags, p)
}

// scanMP2OriginEndpoints stores each channel's origin endpoints with their
// tags, each page written as soon as its tags are read; one channel's
// failure costs the others none of theirs. It returns the stored endpoints
// that have a name, the parents of the origin endpoint policy phase, each
// keyed by its ARN with its path noted in paths. It reports whether a write
// failed, after which the scan must stop.
func scanMP2OriginEndpoints(ctx context.Context, client mediaPackageV2API, acct *account, region string, st *store.Store, scanID string, channels []childParent, paths *mp2Paths, tags rowTagReader, p *phaseTotals) ([]childParent, bool) {
	return storeNestedParentPagesTags(ctx, st, acct, region, "mediapackagev2:ListOriginEndpoints", "origin endpoints", "OriginEndpointName", mp2Gone, channels,
		func(ctx context.Context, channelARN string, token *string) ([]mp2types.OriginEndpointListConfiguration, *string, error) {
			c := paths.mp2PathOf(channelARN)
			out, err := client.ListOriginEndpoints(ctx, &mediapackagev2.ListOriginEndpointsInput{
				ChannelGroupName: &c.group, ChannelName: &c.channel, MaxResults: sdkaws.Int32(mp2PageCap), NextToken: token,
			})
			if err != nil {
				return nil, nil, err
			}
			return out.Items, out.NextToken, nil
		},
		func(channelARN string, e mp2types.OriginEndpointListConfiguration) (*store.Resource, string, string) {
			r, why, key := mp2Row(acct, region, scanID, TypeMediaPackageV2OriginEndpoint, "origin endpoints", e.Arn, e.OriginEndpointName, e)
			if r != nil {
				c := paths.mp2PathOf(channelARN)
				paths.noteMP2Path(r.NativeID, mp2Path{group: c.group, channel: c.channel, endpoint: sv(e.OriginEndpointName)})
			}
			return r, why, key
		},
		func(e mp2types.OriginEndpointListConfiguration) string {
			if sv(e.OriginEndpointName) == "" {
				return ""
			}
			return sv(e.Arn)
		},
		tags, p)
}

// mp2PolicyRow is the row of a channel's or origin endpoint's policy, stored
// under its parent's ARN + "/policy" and named name; nil when the policy is
// empty.
func mp2PolicyRow(acct *account, region, scanID, typ, parentARN, name string, policy *string, out any) *store.Resource {
	if sv(policy) == "" {
		return nil
	}
	return &store.Resource{
		Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
		Type: typ, NativeID: parentARN + "/policy",
		Name: &name, Region: &region,
		AttributesJSON: mustJSON(out), DiscoveredBy: scanID,
	}
}

// scanMP2ChannelPolicies stores the policy of each channel stored this scan,
// read with one GetChannelPolicy call per channel, the channels concurrently
// (storeEachParentPages, each channel's "listing" that one call): a channel
// without a policy (ResourceNotFoundException, or an empty Policy) has no
// row, a denied channel is warned about once, and any other error is
// returned after the other channels' policies are stored. It reports whether
// a write failed, after which the scan must stop.
func scanMP2ChannelPolicies(ctx context.Context, client mediaPackageV2API, acct *account, region string, st *store.Store, scanID string, channels []childParent, paths *mp2Paths, p *phaseTotals) bool {
	return storeEachParentPages(ctx, st, acct, region, "mediapackagev2:GetChannelPolicy", mp2Gone, channels,
		func(ctx context.Context, channelARN string, _ *string) ([]*store.Resource, *string, error) {
			c := paths.mp2PathOf(channelARN)
			out, err := client.GetChannelPolicy(ctx, &mediapackagev2.GetChannelPolicyInput{ChannelGroupName: &c.group, ChannelName: &c.channel})
			if err != nil {
				return nil, nil, err
			}
			if r := mp2PolicyRow(acct, region, scanID, TypeMediaPackageV2ChannelPolicy, channelARN, c.channel, out.Policy, out); r != nil {
				return []*store.Resource{r}, nil, nil
			}
			return nil, nil, nil
		},
		func(_ string, r *store.Resource) (*store.Resource, string, string) { return r, "", "" },
		p)
}

// scanMP2OriginEndpointPolicies is scanMP2ChannelPolicies for the origin
// endpoints stored this scan, read with GetOriginEndpointPolicy.
func scanMP2OriginEndpointPolicies(ctx context.Context, client mediaPackageV2API, acct *account, region string, st *store.Store, scanID string, endpoints []childParent, paths *mp2Paths, p *phaseTotals) bool {
	return storeEachParentPages(ctx, st, acct, region, "mediapackagev2:GetOriginEndpointPolicy", mp2Gone, endpoints,
		func(ctx context.Context, endpointARN string, _ *string) ([]*store.Resource, *string, error) {
			e := paths.mp2PathOf(endpointARN)
			out, err := client.GetOriginEndpointPolicy(ctx, &mediapackagev2.GetOriginEndpointPolicyInput{
				ChannelGroupName: &e.group, ChannelName: &e.channel, OriginEndpointName: &e.endpoint,
			})
			if err != nil {
				return nil, nil, err
			}
			if r := mp2PolicyRow(acct, region, scanID, TypeMediaPackageV2OriginEndpointPolicy, endpointARN, e.endpoint, out.Policy, out); r != nil {
				return []*store.Resource{r}, nil, nil
			}
			return nil, nil, nil
		},
		func(_ string, r *store.Resource) (*store.Resource, string, string) { return r, "", "" },
		p)
}

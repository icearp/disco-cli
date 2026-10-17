package aws

import (
	"context"
	"fmt"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/repostspace"
	repostspacetypes "github.com/aws/aws-sdk-go-v2/service/repostspace/types"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerType(restype.Descriptor{Type: TypeRepostspaceSpace, Service: "repostspace"})
	registerType(restype.Descriptor{Type: TypeRepostspaceChannel, Service: "repostspace"})
	registerService(serviceEntry{
		name: "aws:repostspace",
		fn:   scanRepostspace,
	})
}

type repostspaceAPI interface {
	ListSpaces(context.Context, *repostspace.ListSpacesInput, ...func(*repostspace.Options)) (*repostspace.ListSpacesOutput, error)
	ListChannels(context.Context, *repostspace.ListChannelsInput, ...func(*repostspace.Options)) (*repostspace.ListChannelsOutput, error)
}

const repostspaceListChannelsOp = "repostspace:ListChannels"

// repostspacePageLimit is the maxResults cap the model documents for
// ListSpaces and ListChannels.
const repostspacePageLimit = 100

// scanRepostspace discovers AWS re:Post Private spaces and their channels.
func scanRepostspace(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := repostspace.NewFromConfig(acct.cfg, func(o *repostspace.Options) { o.Region = region })
	return scanRepostspaceAll(ctx, client, acct, region, st, scanID)
}

// scanRepostspaceAll runs the space phase, then the channel phase over the
// spaces it stored. A failed space phase stores nothing and lists no parents,
// so it returns alone. The channel phase tallies its transient errors per
// space, so its error is never transient and needs no demotion.
func scanRepostspaceAll(ctx context.Context, client repostspaceAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	total, inserted, spaces, err := scanRepostspaceSpaces(ctx, client, acct, region, st, scanID)
	if err != nil {
		return total, inserted, err
	}
	t, i, err := scanRepostspaceChannels(ctx, client, acct, region, st, scanID, spaces)
	return total + t, inserted + i, err
}

// scanRepostspaceSpaces stores the region's spaces, NativeID = the space ARN
// (SpaceData.Arn), and returns the stored spaces that carry a SpaceId as the
// parents of the channel phase.
func scanRepostspaceSpaces(ctx context.Context, client repostspaceAPI, acct *account, region string, st *store.Store, scanID string) (total, inserted int, spaces []childParent, err error) {
	pager := repostspace.NewListSpacesPaginator(client, &repostspace.ListSpacesInput{},
		func(o *repostspace.ListSpacesPaginatorOptions) { o.Limit = repostspacePageLimit })
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, nil, skipIfAccessDenied(st, "repostspace:ListSpaces", acct.ID, region, err)
			}
			return 0, 0, nil, fmt.Errorf("repostspace:ListSpaces: %w", err)
		}
		for _, s := range out.Spaces {
			arn := sv(s.Arn)
			if arn == "" {
				continue
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeRepostspaceSpace, NativeID: arn,
				Name: s.Name, Region: &region, Status: s.Status,
				AttributesJSON: mustJSON(s), DiscoveredBy: scanID,
			})
			if id := sv(s.SpaceId); id != "" {
				spaces = append(spaces, childParent{id: id, arn: arn})
			}
		}
	}
	total, inserted, err = upsertBatch(st, batch, "repostspace spaces")
	if err != nil {
		return 0, 0, nil, err
	}
	return total, inserted, spaces, nil
}

// repostspaceChannelNativeID keys a channel under its space: the API returns
// no channel ARN and the Service Reference defines only the space ARN.
// ChannelId is the model's unique channel id (24 characters, no pattern, so
// it goes last); the name is free text unique only per space.
func repostspaceChannelNativeID(spaceARN, channelID string) string {
	return spaceARN + "/channel/" + channelID
}

// scanRepostspaceChannels stores each space's channels that are not deleted,
// linked under the space. Per space: ResourceNotFoundException (deleted
// since listing) is skipped silently, a denial warns once, a transient error
// after the SDK's retries is skipped with one warning for all such spaces,
// ValidationException is skipped with one warning for all such spaces, and
// anything else fails the phase, storing none of its rows.
//
// The model omits ResourceNotFoundException from ListChannels (and
// ListSpaces) only; every other space-keyed op declares it. That ListChannels
// answers a deleted space with it is unverified, but otherwise one space
// deleted between the two listings would fail the region's channel phase.
// Spaces are fanned out to whatever their free-text provisioning
// status, and how the service answers for one still creating or being
// deleted has not been observed; if it is ValidationException, failing the
// phase would fail it on every scan, and skipping silently would hide a real
// input error, so it warns and keeps the siblings.
func scanRepostspaceChannels(ctx context.Context, client repostspaceAPI, acct *account, region string, st *store.Store, scanID string, spaces []childParent) (int, int, error) {
	isGone := func(err error) bool { return isAPIErrorCode(err, "ResourceNotFoundException") }
	return childFanOutSkipNotReady(ctx, st, acct, region, repostspaceListChannelsOp, spaces, isGone, byParentARN,
		"space(s)", "the space may not be ready (creating or deleting; unverified)",
		func(ctx context.Context, space childParent) ([]*store.Resource, error) {
			pager := repostspace.NewListChannelsPaginator(client,
				&repostspace.ListChannelsInput{SpaceId: sdkaws.String(space.id)},
				func(o *repostspace.ListChannelsPaginatorOptions) { o.Limit = repostspacePageLimit })
			var rows []*store.Resource
			for pager.HasMorePages() {
				out, err := pager.NextPage(ctx)
				if err != nil {
					return nil, err
				}
				for _, c := range out.Channels {
					id := sv(c.ChannelId)
					if id == "" || c.ChannelStatus == repostspacetypes.ChannelStatusDeleted {
						continue
					}
					rows = append(rows, &store.Resource{
						Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
						Type: TypeRepostspaceChannel, NativeID: repostspaceChannelNativeID(space.arn, id),
						Name: c.ChannelName, Region: &region, Status: nonEmptyPtr(string(c.ChannelStatus)),
						CreatedAt: tp(c.CreateDateTime), AttributesJSON: mustJSON(c), DiscoveredBy: scanID,
					})
				}
			}
			return rows, nil
		})
}

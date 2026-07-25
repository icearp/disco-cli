package aws

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/service/repostspace"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerType(restype.Descriptor{Type: TypeRepostspaceSpace, Service: "repostspace", Leaf: true})
	registerService(serviceEntry{
		name: "aws:repostspace",
		fn:   scanRepostspace,
	})
}

type repostspaceAPI interface {
	ListSpaces(context.Context, *repostspace.ListSpacesInput, ...func(*repostspace.Options)) (*repostspace.ListSpacesOutput, error)
}

// scanRepostspace discovers AWS re:Post Private spaces; NativeID = the space
// ARN (SpaceData.Arn).
func scanRepostspace(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := repostspace.NewFromConfig(acct.cfg, func(o *repostspace.Options) { o.Region = region })
	return scanRepostspaceSpaces(ctx, client, acct, region, st, scanID)
}

func scanRepostspaceSpaces(ctx context.Context, client repostspaceAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := repostspace.NewListSpacesPaginator(client, &repostspace.ListSpacesInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "repostspace:ListSpaces", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("repostspace:ListSpaces: %w", err)
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
		}
	}
	return upsertBatch(st, batch, "repostspace spaces")
}

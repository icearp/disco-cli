package aws

import (
	"context"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/gameliftstreams"
	glstypes "github.com/aws/aws-sdk-go-v2/service/gameliftstreams/types"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

const (
	glsListApplicationsOp = "gameliftstreams:ListApplications"
	glsListStreamGroupsOp = "gameliftstreams:ListStreamGroups"
	glsTagsOp             = "gameliftstreams:ListTagsForResource"
	// glsPageCap is the MaxResults range maximum (1-100) the model gives
	// ListApplications and ListStreamGroups.
	glsPageCap int32 = 100
)

func init() {
	registerType(restype.Descriptor{Type: TypeGameLiftStreamsApplication, Service: "gameliftstreams"})
	registerType(restype.Descriptor{Type: TypeGameLiftStreamsStreamGroup, Service: "gameliftstreams"})
	registerService(serviceEntry{
		name: "aws:gameliftstreams",
		fn: func(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (int, error) {
			client := gameliftstreams.NewFromConfig(acct.cfg, func(o *gameliftstreams.Options) { o.Region = region })
			return scanGameLiftStreams(ctx, client, acct, region, st, scanID)
		},
	})
}

type gameLiftStreamsAPI interface {
	ListApplications(context.Context, *gameliftstreams.ListApplicationsInput, ...func(*gameliftstreams.Options)) (*gameliftstreams.ListApplicationsOutput, error)
	ListStreamGroups(context.Context, *gameliftstreams.ListStreamGroupsInput, ...func(*gameliftstreams.Options)) (*gameliftstreams.ListStreamGroupsOutput, error)
	ListTagsForResource(context.Context, *gameliftstreams.ListTagsForResourceInput, ...func(*gameliftstreams.Options)) (*gameliftstreams.ListTagsForResourceOutput, error)
}

// scanGameLiftStreams discovers GameLift Streams applications and stream
// groups, each with its tags (gameLiftStreamsTags). Both listings run as
// phases, one at a time and in that order: a phase's error does not stop the
// later one; a failed write and ctx's end do, and so does a first phase that
// stores nothing and fails only with NXDOMAIN (runHostPhases). Each listing
// is written a page at a time, and the tag reads' denials are one warning for
// the whole scan.
func scanGameLiftStreams(ctx context.Context, client gameLiftStreamsAPI, acct *account, region string, st *store.Store, scanID string) (int, error) {
	tags := gameLiftStreamsTags(client).sharingDenials()
	total, err := runHostPhases(ctx, st, acct, region, "gameliftstreams", 0, []hostPhase{
		{op: glsListApplicationsOp, run: func(p *phaseTotals) bool {
			return scanGameLiftStreamsApplications(ctx, client, acct, region, st, scanID, tags, p)
		}},
		{op: glsListStreamGroupsOp, run: func(p *phaseTotals) bool {
			return scanGameLiftStreamsStreamGroups(ctx, client, acct, region, st, scanID, tags, p)
		}},
	})
	tags.warnDenials(st, acct, region)
	return total, err
}

// gameLiftStreamsTags reads an application's or stream group's tags with
// ListTagsForResource, sent the row's NativeID: the ARN its listed element
// carries. The Service Reference lists both resources for the op and gives
// them aws:ResourceTag as a condition key, and CreateApplication and
// CreateStreamGroup take Tags; neither listed element nor GetApplication's or
// GetStreamGroup's body carries them. The op returns every tag at once.
// Neither listing can return another account's resources (the service has no
// sharing), and none is created by AWS, so every row is read. The op's model
// lists no not-found error, so a resource deleted between its listing and
// its tag read is not dropped: the row is kept untagged and the read's error
// handled as any other (readRowTags).
func gameLiftStreamsTags(client gameLiftStreamsAPI) rowTagReader {
	return rowTagReader{
		op: glsTagsOp,
		read: func(ctx context.Context, r *store.Resource) (*string, error) {
			out, err := client.ListTagsForResource(ctx, &gameliftstreams.ListTagsForResourceInput{ResourceArn: sdkaws.String(r.NativeID)})
			if err != nil {
				return nil, err
			}
			return mapTagsJSON(out.Tags), nil
		},
	}
}

// scanGameLiftStreamsApplications stores the applications, each page written
// as soon as its rows' tags are read. It reports whether a write failed,
// after which the scan must stop.
func scanGameLiftStreamsApplications(ctx context.Context, client gameLiftStreamsAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) bool {
	return storeTokenPagesTags(ctx, st, acct, region, glsListApplicationsOp,
		func(ctx context.Context, token *string) ([]glstypes.ApplicationSummary, *string, error) {
			out, err := client.ListApplications(ctx, &gameliftstreams.ListApplicationsInput{MaxResults: sdkaws.Int32(glsPageCap), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.Items, out.NextToken, nil
		},
		func(a glstypes.ApplicationSummary) (*store.Resource, string, string) {
			status := string(a.Status)
			return arnRow("applications", a.Arn, a.Id, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeGameLiftStreamsApplication, NativeID: sv(a.Arn),
				Name: a.Id, Region: &region, Status: &status,
				AttributesJSON: mustJSON(a), DiscoveredBy: scanID,
			})
		}, tags, p)
}

// scanGameLiftStreamsStreamGroups stores the stream groups, each page
// written as soon as its rows' tags are read. It reports whether a write
// failed, after which the scan must stop.
func scanGameLiftStreamsStreamGroups(ctx context.Context, client gameLiftStreamsAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) bool {
	return storeTokenPagesTags(ctx, st, acct, region, glsListStreamGroupsOp,
		func(ctx context.Context, token *string) ([]glstypes.StreamGroupSummary, *string, error) {
			out, err := client.ListStreamGroups(ctx, &gameliftstreams.ListStreamGroupsInput{MaxResults: sdkaws.Int32(glsPageCap), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.Items, out.NextToken, nil
		},
		func(g glstypes.StreamGroupSummary) (*store.Resource, string, string) {
			status := string(g.Status)
			return arnRow("stream groups", g.Arn, g.Id, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeGameLiftStreamsStreamGroup, NativeID: sv(g.Arn),
				Name: g.Id, Region: &region, Status: &status,
				AttributesJSON: mustJSON(g), DiscoveredBy: scanID,
			})
		}, tags, p)
}

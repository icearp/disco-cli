package aws

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/service/gameliftstreams"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerType(restype.Descriptor{Type: TypeGameLiftStreamsApplication, Service: "gameliftstreams", Leaf: true})
	registerType(restype.Descriptor{Type: TypeGameLiftStreamsStreamGroup, Service: "gameliftstreams", Upstream: "AWS::gameliftstreams::stream group"})
	registerService(serviceEntry{
		name: "aws:gameliftstreams",
		fn:   scanGameLiftStreams,
	})
}

type gameLiftStreamsAPI interface {
	ListApplications(context.Context, *gameliftstreams.ListApplicationsInput, ...func(*gameliftstreams.Options)) (*gameliftstreams.ListApplicationsOutput, error)
	ListStreamGroups(context.Context, *gameliftstreams.ListStreamGroupsInput, ...func(*gameliftstreams.Options)) (*gameliftstreams.ListStreamGroupsOutput, error)
}

// scanGameLiftStreams discovers GameLift Streams applications and stream groups.
func scanGameLiftStreams(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := gameliftstreams.NewFromConfig(acct.cfg, func(o *gameliftstreams.Options) { o.Region = region })

	for _, phase := range []func() (int, int, error){
		func() (int, int, error) {
			return scanGameLiftStreamsApplications(ctx, client, acct, region, st, scanID)
		},
		func() (int, int, error) {
			return scanGameLiftStreamsStreamGroups(ctx, client, acct, region, st, scanID)
		},
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

func scanGameLiftStreamsApplications(ctx context.Context, client gameLiftStreamsAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := gameliftstreams.NewListApplicationsPaginator(client, &gameliftstreams.ListApplicationsInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "gameliftstreams:ListApplications", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("gameliftstreams:ListApplications: %w", err)
		}
		for _, a := range out.Items {
			arn := sv(a.Arn)
			if arn == "" {
				continue
			}
			status := string(a.Status)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeGameLiftStreamsApplication, NativeID: arn,
				Name: a.Id, Region: &region, Status: &status,
				AttributesJSON: mustJSON(a), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "gameliftstreams applications")
}

func scanGameLiftStreamsStreamGroups(ctx context.Context, client gameLiftStreamsAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := gameliftstreams.NewListStreamGroupsPaginator(client, &gameliftstreams.ListStreamGroupsInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "gameliftstreams:ListStreamGroups", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("gameliftstreams:ListStreamGroups: %w", err)
		}
		for _, g := range out.Items {
			arn := sv(g.Arn)
			if arn == "" {
				continue
			}
			status := string(g.Status)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeGameLiftStreamsStreamGroup, NativeID: arn,
				Name: g.Id, Region: &region, Status: &status,
				AttributesJSON: mustJSON(g), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "gameliftstreams stream-groups")
}

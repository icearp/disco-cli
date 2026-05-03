package aws

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/internal/store"
	"github.com/aws/aws-sdk-go-v2/service/chimesdkidentity"
)

func init() {
	registerService(serviceEntry{
		name: "aws:chime",
		fn:   scanChime,
		emits: []coverage.TypeDecl{
			{Service: "chime", DiscoType: TypeChimeAppInstance},
		},
	})
}

type chimeAPI interface {
	ListAppInstances(context.Context, *chimesdkidentity.ListAppInstancesInput, ...func(*chimesdkidentity.Options)) (*chimesdkidentity.ListAppInstancesOutput, error)
}

// scanChime discovers Chime SDK AppInstances. Per-region — Chime SDK is
// available in a fixed list of control regions; unsupported regions surface
// as endpoint or access errors and are tolerated by the dispatcher.
func scanChime(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := chimesdkidentity.NewFromConfig(acct.cfg, func(o *chimesdkidentity.Options) { o.Region = region })

	var batch []*store.Resource
	var nextToken *string
	for {
		out, err := client.ListAppInstances(ctx, &chimesdkidentity.ListAppInstancesInput{NextToken: nextToken})
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "chime:ListAppInstances", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("chime:ListAppInstances: %w", err)
		}
		for _, a := range out.AppInstances {
			arn := sv(a.AppInstanceArn)
			if arn == "" {
				continue
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeChimeAppInstance, NativeID: arn,
				Name: a.Name, Region: &region,
				AttributesJSON: mustJSON(a), DiscoveredBy: scanID,
			})
		}
		if out.NextToken == nil || *out.NextToken == "" {
			break
		}
		nextToken = out.NextToken
	}
	return upsertBatch(st, batch, "chime app-instances")
}

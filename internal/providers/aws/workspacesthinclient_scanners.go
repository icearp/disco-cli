package aws

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/service/workspacesthinclient"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerType(restype.Descriptor{Type: TypeWorkSpacesThinClientEnvironment, Service: "workspaces-thin-client", Upstream: "AWS::WorkSpacesThinClient::Environment", Leaf: true})
	registerService(serviceEntry{
		name: "aws:workspaces-thin-client",
		fn:   scanWorkSpacesThinClient,
	})
}

// scanWorkSpacesThinClient discovers WorkSpaces Thin Client environments.
func scanWorkSpacesThinClient(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := workspacesthinclient.NewFromConfig(acct.cfg, func(o *workspacesthinclient.Options) { o.Region = region })

	var batch []*store.Resource
	var nextToken *string
	for {
		out, err := client.ListEnvironments(ctx, &workspacesthinclient.ListEnvironmentsInput{NextToken: nextToken})
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "workspaces-thin-client:ListEnvironments", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("workspaces-thin-client:ListEnvironments: %w", err)
		}
		for _, e := range out.Environments {
			arn := sv(e.Arn)
			if arn == "" {
				continue
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeWorkSpacesThinClientEnvironment, NativeID: arn,
				Name: e.Name, Region: &region,
				AttributesJSON: mustJSON(e), DiscoveredBy: scanID,
			})
		}
		if out.NextToken == nil || *out.NextToken == "" {
			break
		}
		nextToken = out.NextToken
	}
	return upsertBatch(st, batch, "workspaces-thin-client environments")
}

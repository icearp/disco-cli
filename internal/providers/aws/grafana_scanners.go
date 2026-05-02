package aws

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/internal/store"
	"github.com/aws/aws-sdk-go-v2/service/grafana"
)

func init() {
	registerService(serviceEntry{
		name: "aws:grafana",
		fn:   scanGrafana,
		emits: []coverage.TypeDecl{
			{Service: "grafana", DiscoType: TypeGrafanaWorkspace},
		},
	})
}

type grafanaAPI interface {
	ListWorkspaces(context.Context, *grafana.ListWorkspacesInput, ...func(*grafana.Options)) (*grafana.ListWorkspacesOutput, error)
}

// scanGrafana discovers Amazon Managed Grafana workspaces.
func scanGrafana(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := grafana.NewFromConfig(acct.cfg, func(o *grafana.Options) { o.Region = region })

	var batch []*store.Resource
	var nextToken *string
	for {
		out, err := client.ListWorkspaces(ctx, &grafana.ListWorkspacesInput{NextToken: nextToken})
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "grafana:ListWorkspaces", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("grafana:ListWorkspaces: %w", err)
		}
		for _, w := range out.Workspaces {
			id := sv(w.Id)
			if id == "" {
				continue
			}
			arn := fmt.Sprintf("arn:aws:grafana:%s:%s:/workspaces/%s", region, acct.ID, id)
			status := string(w.Status)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeGrafanaWorkspace, NativeID: arn,
				Name: w.Name, Region: &region, Status: &status,
				AttributesJSON: mustJSON(w), DiscoveredBy: scanID,
			})
		}
		if out.NextToken == nil || *out.NextToken == "" {
			break
		}
		nextToken = out.NextToken
	}
	return upsertBatch(st, batch, "grafana workspaces")
}

package aws

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/store"
	"github.com/aws/aws-sdk-go-v2/service/launchwizard"
)

func init() {
	registerService(serviceEntry{
		name: "aws:launch-wizard",
		fn:   scanLaunchWizard,
		emits: []coverage.TypeDecl{
			{Service: "launch-wizard", DiscoType: TypeLaunchWizardDeployment, Leaf: true},
		},
	})
}

// scanLaunchWizard discovers Launch Wizard deployments. Synth ARN:
// arn:aws:launchwizard:{r}:{a}:deployment/{id}.
func scanLaunchWizard(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := launchwizard.NewFromConfig(acct.cfg, func(o *launchwizard.Options) { o.Region = region })

	var batch []*store.Resource
	var nextToken *string
	for {
		out, err := client.ListDeployments(ctx, &launchwizard.ListDeploymentsInput{NextToken: nextToken})
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "launch-wizard:ListDeployments", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("launch-wizard:ListDeployments: %w", err)
		}
		for _, d := range out.Deployments {
			id := sv(d.Id)
			if id == "" {
				continue
			}
			arn := fmt.Sprintf("arn:aws:launchwizard:%s:%s:deployment/%s", region, acct.ID, id)
			status := string(d.Status)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeLaunchWizardDeployment, NativeID: arn,
				Name: d.Name, Region: &region, Status: &status,
				AttributesJSON: mustJSON(d), DiscoveredBy: scanID,
			})
		}
		if out.NextToken == nil || *out.NextToken == "" {
			break
		}
		nextToken = out.NextToken
	}
	return upsertBatch(st, batch, "launch-wizard deployments")
}

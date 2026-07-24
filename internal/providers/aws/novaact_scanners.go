package aws

import (
	"context"
	"fmt"

	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
	"github.com/aws/aws-sdk-go-v2/service/novaact"
)

func init() {
	registerType(restype.Descriptor{Type: TypeNovaActWorkflowDefinition, Service: "nova-act", Upstream: "AWS::NovaAct::WorkflowDefinition", Leaf: true})
	registerService(serviceEntry{
		name: "aws:nova-act",
		fn:   scanNovaAct,
	})
}

// scanNovaAct discovers Nova Act workflow definitions.
func scanNovaAct(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := novaact.NewFromConfig(acct.cfg, func(o *novaact.Options) { o.Region = region })

	var batch []*store.Resource
	var nextToken *string
	for {
		out, err := client.ListWorkflowDefinitions(ctx, &novaact.ListWorkflowDefinitionsInput{NextToken: nextToken})
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "nova-act:ListWorkflowDefinitions", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("nova-act:ListWorkflowDefinitions: %w", err)
		}
		for _, w := range out.WorkflowDefinitionSummaries {
			arn := sv(w.WorkflowDefinitionArn)
			if arn == "" {
				continue
			}
			status := string(w.Status)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeNovaActWorkflowDefinition, NativeID: arn,
				Name: w.WorkflowDefinitionName, Region: &region, Status: &status,
				AttributesJSON: mustJSON(w), DiscoveredBy: scanID,
			})
		}
		if out.NextToken == nil || *out.NextToken == "" {
			break
		}
		nextToken = out.NextToken
	}
	return upsertBatch(st, batch, "nova-act workflow-definitions")
}

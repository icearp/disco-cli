package aws

import (
	"context"
	"fmt"

	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
	"github.com/aws/aws-sdk-go-v2/service/mwaaserverless"
)

func init() {
	registerType(restype.Descriptor{Type: TypeMWAAServerlessWorkflow, Service: "mwaa-serverless", Upstream: "AWS::MWAAServerless::Workflow", Leaf: true})
	registerService(serviceEntry{
		name: "aws:mwaa-serverless",
		fn:   scanMWAAServerless,
	})
}

// scanMWAAServerless discovers MWAA Serverless workflows.
func scanMWAAServerless(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := mwaaserverless.NewFromConfig(acct.cfg, func(o *mwaaserverless.Options) { o.Region = region })

	var batch []*store.Resource
	var nextToken *string
	for {
		out, err := client.ListWorkflows(ctx, &mwaaserverless.ListWorkflowsInput{NextToken: nextToken})
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "mwaa-serverless:ListWorkflows", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("mwaa-serverless:ListWorkflows: %w", err)
		}
		for _, w := range out.Workflows {
			arn := sv(w.WorkflowArn)
			if arn == "" {
				continue
			}
			status := string(w.WorkflowStatus)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeMWAAServerlessWorkflow, NativeID: arn,
				Name: w.Name, Region: &region, Status: &status,
				AttributesJSON: mustJSON(w), DiscoveredBy: scanID,
			})
		}
		if out.NextToken == nil || *out.NextToken == "" {
			break
		}
		nextToken = out.NextToken
	}
	return upsertBatch(st, batch, "mwaa-serverless workflows")
}

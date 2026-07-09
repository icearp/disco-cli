package aws

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/restype"
	"codeberg.org/icearp/disco/store"
	"github.com/aws/aws-sdk-go-v2/service/mwaa"
)

func init() {
	registerType(restype.Descriptor{Type: TypeMWAAEnvironment, Service: "mwaa"})
	registerService(serviceEntry{
		name: "aws:mwaa",
		fn:   scanMWAA,
	})
}

// scanMWAA discovers Managed Workflows for Apache Airflow environments.
// ListEnvironments returns names; GetEnvironment supplies the ARN-bearing
// body.
func scanMWAA(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := mwaa.NewFromConfig(acct.cfg, func(o *mwaa.Options) { o.Region = region })

	var names []string
	var nextToken *string
	for {
		out, err := client.ListEnvironments(ctx, &mwaa.ListEnvironmentsInput{NextToken: nextToken})
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "mwaa:ListEnvironments", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("mwaa:ListEnvironments: %w", err)
		}
		names = append(names, out.Environments...)
		if out.NextToken == nil || *out.NextToken == "" {
			break
		}
		nextToken = out.NextToken
	}

	var batch []*store.Resource
	for _, name := range names {
		nm := name
		out, err := client.GetEnvironment(ctx, &mwaa.GetEnvironmentInput{Name: &nm})
		if err != nil {
			if isAccessDenied(err) {
				continue
			}
			return 0, 0, fmt.Errorf("mwaa:GetEnvironment %s: %w", nm, err)
		}
		if out.Environment == nil {
			continue
		}
		arn := sv(out.Environment.Arn)
		if arn == "" {
			continue
		}
		status := string(out.Environment.Status)
		batch = append(batch, &store.Resource{
			Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
			Type: TypeMWAAEnvironment, NativeID: arn,
			Name: out.Environment.Name, Region: &region, Status: &status,
			AttributesJSON: mustJSON(out.Environment), DiscoveredBy: scanID,
		})
	}
	return upsertBatch(st, batch, "mwaa environments")
}

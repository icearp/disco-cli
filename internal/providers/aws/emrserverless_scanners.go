package aws

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/service/emrserverless"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerType(restype.Descriptor{Type: TypeEMRServerlessApplication, Service: "emr-serverless", Upstream: "AWS::EMRServerless::Application", Leaf: true})
	registerService(serviceEntry{
		name: "aws:emr-serverless",
		fn:   scanEMRServerless,
	})
}

// scanEMRServerless discovers EMR Serverless applications.
func scanEMRServerless(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := emrserverless.NewFromConfig(acct.cfg, func(o *emrserverless.Options) { o.Region = region })

	var batch []*store.Resource
	var nextToken *string
	for {
		out, err := client.ListApplications(ctx, &emrserverless.ListApplicationsInput{NextToken: nextToken})
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "emr-serverless:ListApplications", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("emr-serverless:ListApplications: %w", err)
		}
		for _, a := range out.Applications {
			arn := sv(a.Arn)
			if arn == "" {
				continue
			}
			status := string(a.State)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeEMRServerlessApplication, NativeID: arn,
				Name: a.Name, Region: &region, Status: &status,
				AttributesJSON: mustJSON(a), DiscoveredBy: scanID,
			})
		}
		if out.NextToken == nil || *out.NextToken == "" {
			break
		}
		nextToken = out.NextToken
	}
	return upsertBatch(st, batch, "emr-serverless applications")
}

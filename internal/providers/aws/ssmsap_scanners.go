package aws

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/internal/store"
	"github.com/aws/aws-sdk-go-v2/service/ssmsap"
)

func init() {
	registerService(serviceEntry{
		name: "aws:systems-manager-sap",
		fn:   scanSSMSAP,
		emits: []coverage.TypeDecl{
			{Service: "systems-manager-sap", DiscoType: TypeSSMSAPApplication, Leaf: true},
		},
	})
}

// scanSSMSAP discovers Systems Manager for SAP applications.
func scanSSMSAP(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := ssmsap.NewFromConfig(acct.cfg, func(o *ssmsap.Options) { o.Region = region })

	var batch []*store.Resource
	var nextToken *string
	for {
		out, err := client.ListApplications(ctx, &ssmsap.ListApplicationsInput{NextToken: nextToken})
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "ssm-sap:ListApplications", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("ssm-sap:ListApplications: %w", err)
		}
		for _, a := range out.Applications {
			arn := sv(a.Arn)
			if arn == "" {
				continue
			}
			status := string(a.DiscoveryStatus)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeSSMSAPApplication, NativeID: arn,
				Name: a.Id, Region: &region, Status: &status,
				AttributesJSON: mustJSON(a), DiscoveredBy: scanID,
			})
		}
		if out.NextToken == nil || *out.NextToken == "" {
			break
		}
		nextToken = out.NextToken
	}
	return upsertBatch(st, batch, "ssm-sap applications")
}

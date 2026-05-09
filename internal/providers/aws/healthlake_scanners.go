package aws

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/store"
	"github.com/aws/aws-sdk-go-v2/service/healthlake"
)

func init() {
	registerService(serviceEntry{
		name: "aws:health-lake",
		fn:   scanHealthLake,
		emits: []coverage.TypeDecl{
			{Service: "health-lake", DiscoType: TypeHealthLakeFHIRDatastore},
		},
	})
}

// scanHealthLake discovers HealthLake FHIR datastores.
func scanHealthLake(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := healthlake.NewFromConfig(acct.cfg, func(o *healthlake.Options) { o.Region = region })

	var batch []*store.Resource
	var nextToken *string
	for {
		out, err := client.ListFHIRDatastores(ctx, &healthlake.ListFHIRDatastoresInput{NextToken: nextToken})
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "health-lake:ListFHIRDatastores", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("health-lake:ListFHIRDatastores: %w", err)
		}
		for _, d := range out.DatastorePropertiesList {
			arn := sv(d.DatastoreArn)
			if arn == "" {
				continue
			}
			status := string(d.DatastoreStatus)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeHealthLakeFHIRDatastore, NativeID: arn,
				Name: d.DatastoreName, Region: &region, Status: &status,
				AttributesJSON: mustJSON(d), DiscoveredBy: scanID,
			})
		}
		if out.NextToken == nil || *out.NextToken == "" {
			break
		}
		nextToken = out.NextToken
	}
	return upsertBatch(st, batch, "health-lake fhir-datastores")
}

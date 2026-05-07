package aws

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/internal/store"
	"github.com/aws/aws-sdk-go-v2/service/medicalimaging"
)

func init() {
	registerService(serviceEntry{
		name: "aws:health-imaging",
		fn:   scanHealthImaging,
		emits: []coverage.TypeDecl{
			{Service: "health-imaging", DiscoType: TypeHealthImagingDatastore},
		},
	})
}

// scanHealthImaging discovers HealthImaging (Medical Imaging) datastores.
func scanHealthImaging(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := medicalimaging.NewFromConfig(acct.cfg, func(o *medicalimaging.Options) { o.Region = region })

	var batch []*store.Resource
	var nextToken *string
	for {
		out, err := client.ListDatastores(ctx, &medicalimaging.ListDatastoresInput{NextToken: nextToken})
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "medical-imaging:ListDatastores", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("medical-imaging:ListDatastores: %w", err)
		}
		for _, d := range out.DatastoreSummaries {
			arn := sv(d.DatastoreArn)
			id := sv(d.DatastoreId)
			if arn == "" {
				if id == "" {
					continue
				}
				arn = fmt.Sprintf("arn:aws:medical-imaging:%s:%s:datastore/%s", region, acct.ID, id)
			}
			status := string(d.DatastoreStatus)
			attrsJSON := mustJSON(d)
			if gout, gerr := client.GetDatastore(ctx, &medicalimaging.GetDatastoreInput{DatastoreId: d.DatastoreId}); gerr == nil && gout.DatastoreProperties != nil {
				attrsJSON = mustJSON(gout.DatastoreProperties)
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeHealthImagingDatastore, NativeID: arn,
				Name: d.DatastoreName, Region: &region, Status: &status,
				AttributesJSON: attrsJSON, DiscoveredBy: scanID,
			})
		}
		if out.NextToken == nil || *out.NextToken == "" {
			break
		}
		nextToken = out.NextToken
	}
	return upsertBatch(st, batch, "health-imaging datastores")
}

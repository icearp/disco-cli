package aws

import (
	"context"
	"fmt"

	"codeburg.org/icearp/disco/internal/store"
	"github.com/aws/aws-sdk-go-v2/service/rds"
)

func init() { registerService(serviceEntry{name: "aws:rds", fn: scanRDS}) }

// scanRDS discovers RDS DB instances in one region.
func scanRDS(ctx context.Context, acct *account, region string, st *store.Store, scanID string) error {
	client := rds.NewFromConfig(acct.cfg, func(o *rds.Options) { o.Region = region })

	pager := rds.NewDescribeDBInstancesPaginator(client, &rds.DescribeDBInstancesInput{})
	for pager.HasMorePages() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return skipIfAccessDenied("rds:DescribeDBInstances", acct.ID, region, err)
			}
			return fmt.Errorf("rds:DescribeDBInstances: %w", err)
		}
		var batch []*store.Resource
		for _, db := range page.DBInstances {
			name := sv(db.DBInstanceIdentifier)
			r := &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeRDSDBInstance,
				NativeID:       sv(db.DBInstanceArn),
				Name:           &name,
				Region:         &region,
				Zone:           db.AvailabilityZone,
				CreatedAt:      tp(db.InstanceCreateTime),
				Status:         db.DBInstanceStatus,
				TagsJSON:       awsTagsJSON(db.TagList),
				AttributesJSON: mustJSON(db),
				DiscoveredBy:         scanID,
			}
			batch = append(batch, r)
		}
		if len(batch) > 0 {
			if err := st.UpsertResources(batch); err != nil {
				return fmt.Errorf("upsert RDS instances: %w", err)
			}
		}
	}
	return nil
}

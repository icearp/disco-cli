package aws

import (
	"context"
	"fmt"

	"codeburg.org/icearp/disco/internal/store"
	"github.com/aws/aws-sdk-go-v2/service/rds"
	rdstypes "github.com/aws/aws-sdk-go-v2/service/rds/types"
)

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
				Type:           "aws:rds:db-instance",
				NativeID:       sv(db.DBInstanceArn),
				Name:           &name,
				Region:         &region,
				Status:         db.DBInstanceStatus,
				TagsJSON:       rdsTagsJSON(db.TagList),
				AttributesJSON: mustJSON(db),
				ScanID:         scanID,
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

func rdsTagsJSON(tags []rdstypes.Tag) *string {
	if len(tags) == 0 {
		return nil
	}
	m := make(map[string]string, len(tags))
	for _, t := range tags {
		if t.Key != nil && t.Value != nil {
			m[*t.Key] = *t.Value
		}
	}
	s := mustJSON(m)
	return &s
}

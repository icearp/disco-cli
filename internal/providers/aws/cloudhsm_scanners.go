package aws

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/store"
	"github.com/aws/aws-sdk-go-v2/service/cloudhsmv2"
)

func init() {
	registerService(serviceEntry{
		name: "aws:cloudhsm",
		fn:   scanCloudHSM,
		emits: []coverage.TypeDecl{
			{Service: "cloudhsm", DiscoType: TypeCloudHSMCluster},
			{Service: "cloudhsm", DiscoType: TypeCloudHSMBackup},
		},
	})
}

type cloudHSMAPI interface {
	DescribeClusters(context.Context, *cloudhsmv2.DescribeClustersInput, ...func(*cloudhsmv2.Options)) (*cloudhsmv2.DescribeClustersOutput, error)
	DescribeBackups(context.Context, *cloudhsmv2.DescribeBackupsInput, ...func(*cloudhsmv2.Options)) (*cloudhsmv2.DescribeBackupsOutput, error)
}

// scanCloudHSM discovers CloudHSM v2 clusters and their backups. The "cloudhsm"
// service segment mirrors the Service Reference (the SDK/CFN spell it v2).
func scanCloudHSM(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := cloudhsmv2.NewFromConfig(acct.cfg, func(o *cloudhsmv2.Options) { o.Region = region })

	for _, phase := range []func() (int, int, error){
		func() (int, int, error) { return scanCloudHSMClusters(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanCloudHSMBackups(ctx, client, acct, region, st, scanID) },
	} {
		t, i, ferr := phase()
		if ferr != nil {
			return total, inserted, ferr
		}
		total += t
		inserted += i
	}
	return total, inserted, nil
}

func scanCloudHSMClusters(ctx context.Context, client cloudHSMAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var batch []*store.Resource
	pager := cloudhsmv2.NewDescribeClustersPaginator(client, &cloudhsmv2.DescribeClustersInput{})
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "cloudhsm:DescribeClusters", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("cloudhsm:DescribeClusters: %w", err)
		}
		for _, c := range out.Clusters {
			id := sv(c.ClusterId)
			if id == "" {
				continue
			}
			// CloudHSM clusters carry no ARN field; synthesize the canonical shape.
			arn := fmt.Sprintf("arn:aws:cloudhsm:%s:%s:cluster/%s", region, acct.ID, id)
			status := string(c.State)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeCloudHSMCluster, NativeID: arn,
				Name: c.ClusterId, Region: &region, Status: &status,
				AttributesJSON: mustJSON(c), CreatedAt: tp(c.CreateTimestamp),
				TagsJSON: awsTagsJSON(c.TagList), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "cloudhsm clusters")
}

func scanCloudHSMBackups(ctx context.Context, client cloudHSMAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var batch []*store.Resource
	pager := cloudhsmv2.NewDescribeBackupsPaginator(client, &cloudhsmv2.DescribeBackupsInput{})
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "cloudhsm:DescribeBackups", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("cloudhsm:DescribeBackups: %w", err)
		}
		for _, b := range out.Backups {
			arn := sv(b.BackupArn)
			if arn == "" {
				continue
			}
			status := string(b.BackupState)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeCloudHSMBackup, NativeID: arn,
				Name: b.BackupId, Region: &region, Status: &status,
				AttributesJSON: mustJSON(b), CreatedAt: tp(b.CreateTimestamp),
				TagsJSON: awsTagsJSON(b.TagList), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "cloudhsm backups")
}

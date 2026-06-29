package aws

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/store"
	"github.com/aws/aws-sdk-go-v2/service/fsx"
)

func scanFSxBackups(ctx context.Context, client fsxAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := fsx.NewDescribeBackupsPaginator(client, &fsx.DescribeBackupsInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "fsx:DescribeBackups", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("fsx:DescribeBackups: %w", err)
		}
		for _, b := range out.Backups {
			arn := sv(b.ResourceARN)
			if arn == "" {
				continue
			}
			label := sv(b.BackupId)
			lc := string(b.Lifecycle)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeFSxBackup, NativeID: arn,
				Name: &label, Region: &region, Status: &lc,
				AttributesJSON: mustJSON(b), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "fsx backups")
}

func scanFSxFileCaches(ctx context.Context, client fsxAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := fsx.NewDescribeFileCachesPaginator(client, &fsx.DescribeFileCachesInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "fsx:DescribeFileCaches", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("fsx:DescribeFileCaches: %w", err)
		}
		for _, c := range out.FileCaches {
			arn := sv(c.ResourceARN)
			if arn == "" {
				continue
			}
			label := sv(c.FileCacheId)
			lc := string(c.Lifecycle)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeFSxFileCache, NativeID: arn,
				Name: &label, Region: &region, Status: &lc,
				AttributesJSON: mustJSON(c), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "fsx file-caches")
}

package aws

import (
	"context"

	"github.com/aws/aws-sdk-go-v2/service/fsx"
	fsxtypes "github.com/aws/aws-sdk-go-v2/service/fsx/types"
	"github.com/icearp/disco-cli/store"
)

func scanFSxBackups(ctx context.Context, client fsxAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	return storeTokenList(ctx, st, acct, region, fsxDescribeBackupsOp,
		func(ctx context.Context, token *string) ([]fsxtypes.Backup, *string, error) {
			out, err := client.DescribeBackups(ctx, &fsx.DescribeBackupsInput{NextToken: token, MaxResults: fsxMaxResults})
			if err != nil {
				return nil, nil, err
			}
			return out.Backups, out.NextToken, nil
		},
		func(b fsxtypes.Backup) (*store.Resource, string, string) {
			id := sv(b.BackupId)
			return fsxRow(acct, region, scanID, TypeFSxBackup, "backups", fsxElement{
				arn: sv(b.ResourceARN), id: id, name: nonEmptyPtr(id), lifecycle: string(b.Lifecycle), tags: b.Tags,
			}, b)
		})
}

func scanFSxFileCaches(ctx context.Context, client fsxAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	return storeTokenList(ctx, st, acct, region, fsxDescribeFileCachesOp,
		func(ctx context.Context, token *string) ([]fsxtypes.FileCache, *string, error) {
			out, err := client.DescribeFileCaches(ctx, &fsx.DescribeFileCachesInput{NextToken: token, MaxResults: fsxMaxResults})
			// FSx File Cache is per-account/region opt-in; when not enabled AWS
			// returns BadRequest "The requested feature is not enabled for this
			// AWS account": account state, not failure, so the listing is empty.
			// Only the first page means that; a later one is a failure.
			if token == nil && isAPIErrorWithMessage(err, "BadRequest", "requested feature is not enabled") {
				return nil, nil, nil
			}
			if err != nil {
				return nil, nil, err
			}
			return out.FileCaches, out.NextToken, nil
		},
		func(c fsxtypes.FileCache) (*store.Resource, string, string) {
			id := sv(c.FileCacheId)
			return fsxRow(acct, region, scanID, TypeFSxFileCache, "file caches", fsxElement{
				arn: sv(c.ResourceARN), id: id, name: nonEmptyPtr(id), lifecycle: string(c.Lifecycle),
			}, c)
		})
}

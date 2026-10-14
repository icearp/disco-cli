package aws

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/service/backup"
	"github.com/icearp/disco-cli/store"
)

// scanBackupAccessPoints lists the read-only S3 access points onto recovery
// points, account- and region-wide.
func scanBackupAccessPoints(ctx context.Context, client backupAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	const op = "backup:ListBackupAccessPoints"
	pager := backup.NewListBackupAccessPointsPaginator(client, &backup.ListBackupAccessPointsInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				_ = skipIfAccessDenied(st, op, acct.ID, region, err)
				break
			}
			return 0, 0, fmt.Errorf("%s: %w", op, err)
		}
		for _, ap := range out.BackupAccessPoints {
			arn := sv(ap.AccessPointArn)
			if arn == "" {
				continue
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeBackupAccessPoint, NativeID: arn,
				Name: ap.Name, Status: nonEmptyPtr(string(ap.Status)), Region: &region,
				CreatedAt: tp(ap.CreationTime), AttributesJSON: mustJSON(ap), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "backup access-points")
}

// scanBackupRestoreAccessVaults lists, per logically air-gapped vault, the
// restore access vaults granted from it. The NativeID is
// {airGappedVaultARN}/restore-access-backup-vault/{restoreAccessVaultARN}
// because the restore access vault's own ARN is a backup-vault ARN: when it
// lives in this account ListBackupVaults already stores that ARN as a vault
// row, and one NativeID for two types merges them into one version chain.
func scanBackupRestoreAccessVaults(ctx context.Context, client backupAPI, acct *account, region string, st *store.Store, scanID string, airGapped []childParent) (int, int, error) {
	return childFanOut(ctx, st, acct, region, "backup:ListRestoreAccessBackupVaults", airGapped, isBackupVaultGone,
		func(ctx context.Context, p childParent) ([]*store.Resource, error) {
			pager := backup.NewListRestoreAccessBackupVaultsPaginator(client, &backup.ListRestoreAccessBackupVaultsInput{BackupVaultName: &p.id})
			var rows []*store.Resource
			for pager.HasMorePages() {
				out, err := pager.NextPage(ctx)
				if err != nil {
					return nil, err
				}
				for _, v := range out.RestoreAccessBackupVaults {
					vaultARN := sv(v.RestoreAccessBackupVaultArn)
					if vaultARN == "" {
						continue
					}
					rows = append(rows, &store.Resource{
						Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
						Type:     TypeBackupRestoreAccessBackupVault,
						NativeID: p.arn + "/restore-access-backup-vault/" + vaultARN,
						Name:     &vaultARN, Status: nonEmptyPtr(string(v.VaultState)), Region: &region,
						CreatedAt: tp(v.CreationDate), AttributesJSON: mustJSON(v), DiscoveredBy: scanID,
					})
				}
			}
			return rows, nil
		})
}

// isBackupVaultGone matches a vault deleted between ListBackupVaults and the
// per-vault call.
func isBackupVaultGone(err error) bool {
	return isAPIErrorCode(err, "ResourceNotFoundException")
}

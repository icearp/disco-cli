package aws

import (
	"cmp"
	"context"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/backup"
	backuptypes "github.com/aws/aws-sdk-go-v2/service/backup/types"
	"github.com/icearp/disco-cli/store"
)

// backupAccessPointPageCap is ListBackupAccessPoints' MaxResults: the maximum
// of its model's range trait, lower than the other listings' backupPageCap.
const backupAccessPointPageCap = 100

// scanBackupAccessPoints stores the read-only S3 access points onto recovery
// points, account- and region-wide, with their tags.
func scanBackupAccessPoints(ctx context.Context, client backupAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) bool {
	return storeTokenListTags(ctx, st, acct, region, "backup:ListBackupAccessPoints",
		func(ctx context.Context, token *string) ([]backuptypes.ListAccessPointsMember, *string, error) {
			out, err := client.ListBackupAccessPoints(ctx, &backup.ListBackupAccessPointsInput{NextToken: token, MaxResults: sdkaws.Int32(backupAccessPointPageCap)})
			if err != nil {
				return nil, nil, err
			}
			return out.BackupAccessPoints, out.NextToken, nil
		},
		func(ap backuptypes.ListAccessPointsMember) (*store.Resource, string, string) {
			arn := sv(ap.AccessPointArn)
			if arn == "" {
				return nil, "access points have no AccessPointArn", cmp.Or(sv(ap.Name), bkNoName)
			}
			r := bkRow(acct, region, scanID, TypeBackupAccessPoint, arn, ap.Name, ap)
			r.Status = nonEmptyPtr(string(ap.Status))
			r.CreatedAt = tp(ap.CreationTime)
			return r, "", ""
		}, tags, p)
}

// scanBackupRestoreAccessVaults lists, per logically air-gapped vault the
// vault phase stored, the restore access vaults granted from it, linked under
// it. A vault ListRestoreAccessBackupVaults no longer finds was deleted since
// it was listed and is skipped. The NativeID is
// {airGappedVaultARN}/restore-access-backup-vault/{restoreAccessVaultARN}
// because the restore access vault's own ARN is a backup-vault ARN: when it
// lives in this account ListBackupVaults already stores that ARN as a vault
// row, and one NativeID for two types merges them into one version chain.
func scanBackupRestoreAccessVaults(ctx context.Context, client backupAPI, acct *account, region string, st *store.Store, scanID string, airGapped []childParent) (int, int, error) {
	return storeChildrenEachParent(ctx, st, acct, region, "backup:ListRestoreAccessBackupVaults", backupNotFound, airGapped, nil, nil,
		func(ctx context.Context, vault childParent, token *string) ([]backuptypes.RestoreAccessBackupVaultListMember, *string, error) {
			out, err := client.ListRestoreAccessBackupVaults(ctx, &backup.ListRestoreAccessBackupVaultsInput{
				BackupVaultName: sdkaws.String(vault.id), NextToken: token, MaxResults: sdkaws.Int32(backupPageCap),
			})
			if err != nil {
				return nil, nil, err
			}
			return out.RestoreAccessBackupVaults, out.NextToken, nil
		},
		func(vault childParent, v backuptypes.RestoreAccessBackupVaultListMember) (*store.Resource, string, string) {
			vaultARN := sv(v.RestoreAccessBackupVaultArn)
			if vaultARN == "" {
				return nil, "restore access backup vaults have no RestoreAccessBackupVaultArn", "in vault " + vault.id
			}
			r := bkRow(acct, region, scanID, TypeBackupRestoreAccessBackupVault, vault.arn+"/restore-access-backup-vault/"+vaultARN, &vaultARN, v)
			r.Status = nonEmptyPtr(string(v.VaultState))
			r.CreatedAt = tp(v.CreationDate)
			return r, "", ""
		})
}

package aws

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/service/backup"
	"github.com/icearp/disco-cli/internal/util"
	"github.com/icearp/disco-cli/store"
)

// scanBackupHoldsAndRecoveryPoints lists legal holds (account-wide) and recovery
// points (per vault — ListRecoveryPointsByBackupVault requires a vault name, so
// it fans out over vaults already scanned in this region).
func scanBackupHoldsAndRecoveryPoints(ctx context.Context, client backupAPI, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	lhTotal, lhInserted, ferr := scanBackupLegalHolds(ctx, client, acct, region, st, scanID)
	if ferr != nil {
		return lhTotal, lhInserted, ferr
	}
	rpTotal, rpInserted, ferr := scanBackupRecoveryPoints(ctx, client, acct, region, st, scanID)
	if ferr != nil {
		return lhTotal + rpTotal, lhInserted + rpInserted, ferr
	}
	return lhTotal + rpTotal, lhInserted + rpInserted, nil
}

func scanBackupLegalHolds(ctx context.Context, client backupAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := backup.NewListLegalHoldsPaginator(client, &backup.ListLegalHoldsInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "backup:ListLegalHolds", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("backup:ListLegalHolds: %w", perr)
		}
		for _, h := range out.LegalHolds {
			arn := sv(h.LegalHoldArn)
			if arn == "" {
				continue
			}
			label := sv(h.Title)
			if label == "" {
				label = arn
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeBackupLegalHold, NativeID: arn,
				Name: &label, Region: &region, CreatedAt: tp(h.CreationDate),
				AttributesJSON: mustJSON(h), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "backup legal-holds")
}

// scanBackupRecoveryPoints fans out ListRecoveryPointsByBackupVault over the
// vaults already scanned in this region. Recovery points are the actual backups;
// cardinality scales with the account's backup history.
func scanBackupRecoveryPoints(ctx context.Context, client backupAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	vaults, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID,
		Types: []string{TypeBackupVault, TypeBackupLogicallyAirGappedVault}, Limit: util.AllResources,
	})
	if err != nil {
		return 0, 0, err
	}
	var total, inserted int
	for _, v := range vaults {
		if v.Region == nil || *v.Region != region || v.Name == nil || *v.Name == "" {
			continue
		}
		t, i, ferr := scanBackupRecoveryPointsForVault(ctx, client, acct, region, st, scanID, *v.Name)
		if ferr != nil {
			return total, inserted, ferr
		}
		total += t
		inserted += i
	}
	return total, inserted, nil
}

func scanBackupRecoveryPointsForVault(ctx context.Context, client backupAPI, acct *account, region string, st *store.Store, scanID, vaultName string) (int, int, error) {
	// Recovery points can reach tens of thousands per vault, so request max
	// page size and upsert per page rather than buffering the whole vault's
	// history in memory.
	pager := backup.NewListRecoveryPointsByBackupVaultPaginator(client, &backup.ListRecoveryPointsByBackupVaultInput{BackupVaultName: &vaultName},
		func(o *backup.ListRecoveryPointsByBackupVaultPaginatorOptions) { o.Limit = 1000 })
	var total, inserted int
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "backup:ListRecoveryPointsByBackupVault", acct.ID, region, perr)
				return total, inserted, nil
			}
			return total, inserted, fmt.Errorf("backup:ListRecoveryPointsByBackupVault(%s): %w", vaultName, perr)
		}
		batch := make([]*store.Resource, 0, len(out.RecoveryPoints))
		for _, rp := range out.RecoveryPoints {
			arn := sv(rp.RecoveryPointArn)
			if arn == "" {
				continue
			}
			label := sv(rp.ResourceName)
			if label == "" {
				label = arn
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeBackupRecoveryPoint, NativeID: arn,
				Name: &label, Region: &region, CreatedAt: tp(rp.CreationDate),
				AttributesJSON: mustJSON(rp), DiscoveredBy: scanID,
			})
		}
		t, i, err := upsertBatch(st, batch, "backup recovery-points")
		if err != nil {
			return total, inserted, err
		}
		total += t
		inserted += i
	}
	return total, inserted, nil
}

package aws

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/internal/store"
	"github.com/aws/aws-sdk-go-v2/service/backup"
	backuptypes "github.com/aws/aws-sdk-go-v2/service/backup/types"
)

func init() {
	registerService(serviceEntry{
		name: "aws:backup",
		fn:   scanBackup,
		emits: []coverage.TypeDecl{
			{Service: "backup", DiscoType: TypeBackupVault},
			{Service: "backup", DiscoType: TypeBackupLogicallyAirGappedVault},
			{Service: "backup", DiscoType: TypeBackupPlan},
			{Service: "backup", DiscoType: TypeBackupSelection},
		},
	})
}

// backupAPI is the narrow set of Backup operations called by scanBackupAll.
type backupAPI interface {
	ListBackupVaults(context.Context, *backup.ListBackupVaultsInput, ...func(*backup.Options)) (*backup.ListBackupVaultsOutput, error)
	ListBackupPlans(context.Context, *backup.ListBackupPlansInput, ...func(*backup.Options)) (*backup.ListBackupPlansOutput, error)
	ListBackupSelections(context.Context, *backup.ListBackupSelectionsInput, ...func(*backup.Options)) (*backup.ListBackupSelectionsOutput, error)
}

// scanBackup discovers AWS Backup vaults, plans, and per-plan selections.
// Vaults and plans carry native ARNs from the list APIs. Selections have no
// list-level ARN so one is synthesised from the parent plan ID + selection ID
// for a stable NativeID across scans.
func scanBackup(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := backup.NewFromConfig(acct.cfg, func(o *backup.Options) { o.Region = region })
	return scanBackupAll(ctx, client, acct, region, st, scanID)
}

// scanBackupAll holds the testable scan body.
func scanBackupAll(ctx context.Context, client backupAPI, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	// Vaults.
	var vaultBatch []*store.Resource
	vPager := backup.NewListBackupVaultsPaginator(client, &backup.ListBackupVaultsInput{})
	for vPager.HasMorePages() {
		page, err := vPager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "backup:ListBackupVaults", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("backup:ListBackupVaults: %w", err)
		}
		for _, v := range page.BackupVaultList {
			arn := sv(v.BackupVaultArn)
			if arn == "" {
				continue
			}
			// VaultType=LOGICALLY_AIR_GAPPED_BACKUP_VAULT splits to its own
			// disco type so the AWS::Backup::LogicallyAirGappedBackupVault
			// CFN row maps to a real scanner row.
			vtype := TypeBackupVault
			if v.VaultType == backuptypes.VaultTypeLogicallyAirGappedBackupVault {
				vtype = TypeBackupLogicallyAirGappedVault
			}
			vaultBatch = append(vaultBatch, &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           vtype,
				NativeID:       arn,
				Name:           v.BackupVaultName,
				Region:         &region,
				CreatedAt:      tp(v.CreationDate),
				AttributesJSON: mustJSON(v),
				DiscoveredBy:   scanID,
			})
		}
	}
	if len(vaultBatch) > 0 {
		n, err := st.UpsertResources(vaultBatch)
		if err != nil {
			return 0, 0, fmt.Errorf("upsert Backup vaults: %w", err)
		}
		total += len(vaultBatch)
		inserted += n
	}

	// Plans and selections.
	var (
		planBatch     []*store.Resource
		selectionPair []struct {
			r         *store.Resource
			parentARN string
		}
	)
	pPager := backup.NewListBackupPlansPaginator(client, &backup.ListBackupPlansInput{})
	for pPager.HasMorePages() {
		page, err := pPager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				break
			}
			return 0, 0, fmt.Errorf("backup:ListBackupPlans: %w", err)
		}
		for _, p := range page.BackupPlansList {
			planARN := sv(p.BackupPlanArn)
			planID := sv(p.BackupPlanId)
			if planARN == "" || planID == "" {
				continue
			}
			planBatch = append(planBatch, &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeBackupPlan,
				NativeID:       planARN,
				Name:           p.BackupPlanName,
				Region:         &region,
				CreatedAt:      tp(p.CreationDate),
				AttributesJSON: mustJSON(p),
				DiscoveredBy:   scanID,
			})

			// List selections for this plan.
			sPager := backup.NewListBackupSelectionsPaginator(client, &backup.ListBackupSelectionsInput{BackupPlanId: &planID})
			for sPager.HasMorePages() {
				sp, err := sPager.NextPage(ctx)
				if err != nil {
					if isAccessDenied(err) {
						break
					}
					return 0, 0, fmt.Errorf("backup:ListBackupSelections %s: %w", planID, err)
				}
				for _, s := range sp.BackupSelectionsList {
					selID := sv(s.SelectionId)
					if selID == "" {
						continue
					}
					selARN := fmt.Sprintf("arn:aws:backup:%s:%s:backup-plan:%s/selection/%s", region, acct.ID, planID, selID)
					selectionPair = append(selectionPair, struct {
						r         *store.Resource
						parentARN string
					}{
						r: &store.Resource{
							Provider:       "aws",
							AccountID:      acct.ID,
							AccountName:    &acct.Name,
							Type:           TypeBackupSelection,
							NativeID:       selARN,
							Name:           s.SelectionName,
							Region:         &region,
							CreatedAt:      tp(s.CreationDate),
							AttributesJSON: mustJSON(s),
							DiscoveredBy:   scanID,
						},
						parentARN: planARN,
					})
				}
			}
		}
	}
	if len(planBatch) > 0 {
		n, err := st.UpsertResources(planBatch)
		if err != nil {
			return 0, 0, fmt.Errorf("upsert Backup plans: %w", err)
		}
		total += len(planBatch)
		inserted += n
	}
	if len(selectionPair) > 0 {
		rs := make([]*store.Resource, len(selectionPair))
		for i, p := range selectionPair {
			rs[i] = p.r
		}
		n, err := st.UpsertResources(rs)
		if err != nil {
			return 0, 0, fmt.Errorf("upsert Backup selections: %w", err)
		}
		total += len(rs)
		inserted += n
		pairs := make([][2]string, len(selectionPair))
		for i, p := range selectionPair {
			parentID := store.ResourceID("aws", acct.ID, TypeBackupPlan, p.parentARN)
			pairs[i] = [2]string{p.r.ID, parentID}
		}
		if err := st.RecordHierarchyBatch(pairs); err != nil {
			return 0, 0, fmt.Errorf("closure Backup selections: %w", err)
		}
	}

	return total, inserted, nil
}

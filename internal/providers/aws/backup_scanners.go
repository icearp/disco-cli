package aws

import (
	"context"
	"errors"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/service/backup"
	backuptypes "github.com/aws/aws-sdk-go-v2/service/backup/types"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerType(restype.Descriptor{Type: TypeBackupVault, Service: "backup"})
	registerType(restype.Descriptor{Type: TypeBackupLogicallyAirGappedVault, Service: "backup"})
	registerType(restype.Descriptor{Type: TypeBackupPlan, Service: "backup"})
	registerType(restype.Descriptor{Type: TypeBackupSelection, Service: "backup"})
	registerType(restype.Descriptor{Type: TypeBackupFramework, Service: "backup"})
	registerType(restype.Descriptor{Type: TypeBackupReportPlan, Service: "backup"})
	registerType(restype.Descriptor{Type: TypeBackupRestoreTestingPlan, Service: "backup"})
	registerType(restype.Descriptor{Type: TypeBackupRestoreTestingSelection, Service: "backup"})
	registerType(restype.Descriptor{Type: TypeBackupTieringConfiguration, Service: "backup"})
	registerType(restype.Descriptor{Type: TypeBackupLegalHold, Service: "backup"})
	registerType(restype.Descriptor{Type: TypeBackupRecoveryPoint, Service: "backup"})
	registerType(restype.Descriptor{Type: TypeBackupAccessPoint, Service: "backup"})
	registerType(restype.Descriptor{Type: TypeBackupRestoreAccessBackupVault, Service: "backup"})
	registerService(serviceEntry{
		name: "aws:backup",
		fn:   scanBackup,
	})
}

// backupAPI is the narrow set of Backup operations called by scanBackupAll.
type backupAPI interface {
	ListBackupVaults(context.Context, *backup.ListBackupVaultsInput, ...func(*backup.Options)) (*backup.ListBackupVaultsOutput, error)
	ListBackupPlans(context.Context, *backup.ListBackupPlansInput, ...func(*backup.Options)) (*backup.ListBackupPlansOutput, error)
	GetBackupPlan(context.Context, *backup.GetBackupPlanInput, ...func(*backup.Options)) (*backup.GetBackupPlanOutput, error)
	ListBackupSelections(context.Context, *backup.ListBackupSelectionsInput, ...func(*backup.Options)) (*backup.ListBackupSelectionsOutput, error)
	ListFrameworks(context.Context, *backup.ListFrameworksInput, ...func(*backup.Options)) (*backup.ListFrameworksOutput, error)
	ListReportPlans(context.Context, *backup.ListReportPlansInput, ...func(*backup.Options)) (*backup.ListReportPlansOutput, error)
	ListRestoreTestingPlans(context.Context, *backup.ListRestoreTestingPlansInput, ...func(*backup.Options)) (*backup.ListRestoreTestingPlansOutput, error)
	ListRestoreTestingSelections(context.Context, *backup.ListRestoreTestingSelectionsInput, ...func(*backup.Options)) (*backup.ListRestoreTestingSelectionsOutput, error)
	ListTieringConfigurations(context.Context, *backup.ListTieringConfigurationsInput, ...func(*backup.Options)) (*backup.ListTieringConfigurationsOutput, error)
	ListLegalHolds(context.Context, *backup.ListLegalHoldsInput, ...func(*backup.Options)) (*backup.ListLegalHoldsOutput, error)
	ListRecoveryPointsByBackupVault(context.Context, *backup.ListRecoveryPointsByBackupVaultInput, ...func(*backup.Options)) (*backup.ListRecoveryPointsByBackupVaultOutput, error)
	ListBackupAccessPoints(context.Context, *backup.ListBackupAccessPointsInput, ...func(*backup.Options)) (*backup.ListBackupAccessPointsOutput, error)
	ListRestoreAccessBackupVaults(context.Context, *backup.ListRestoreAccessBackupVaultsInput, ...func(*backup.Options)) (*backup.ListRestoreAccessBackupVaultsOutput, error)
}

// scanBackup runs every AWS Backup phase for one region on a client pinned to
// that region. Phases that list parents run before the phases that fan out
// over them, and each runs whatever the earlier ones returned. Errors join
// through phaseErrs, which demotes the transient ones to warnings only beside
// a hard error, so a transient-only failure still makes the scan partial.
func scanBackup(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := backup.NewFromConfig(acct.cfg, func(o *backup.Options) { o.Region = region })
	var errs phaseErrs
	add := func(op string, t, i int, err error) {
		total += t
		inserted += i
		errs.add(op, err)
	}
	airGapped, t, i, ferr := scanBackupAll(ctx, client, acct, region, st, scanID)
	add("backup", t, i, ferr)
	t, i, ferr = scanBackupExtended(ctx, client, acct, region, st, scanID)
	add("backup", t, i, ferr)
	t, i, ferr = scanBackupHoldsAndRecoveryPoints(ctx, client, acct, region, st, scanID)
	add("backup:ListRecoveryPointsByBackupVault", t, i, ferr)
	t, i, ferr = scanBackupAccessPoints(ctx, client, acct, region, st, scanID)
	add("backup:ListBackupAccessPoints", t, i, ferr)
	t, i, ferr = scanBackupRestoreAccessVaults(ctx, client, acct, region, st, scanID, airGapped)
	add("backup:ListRestoreAccessBackupVaults", t, i, ferr)
	return total, inserted, errs.join(st, acct, region)
}

// backupPlanReads tallies the plans and selections kept or dropped by the
// per-plan reads, for one warning per cause after the rows are stored.
type backupPlanReads struct {
	planDenied          skipTally
	planTransient       transientTally
	planNoID            skipTally
	selectionsDenied    skipTally
	selectionsTransient transientTally
	selectionNoID       skipTally
}

// warn reports each tally once under the op that caused it; call it only
// after the plans are stored.
func (r *backupPlanReads) warn(st *store.Store, getOp, selectionsOp string, acct *account, region string) {
	const kept = "%d backup plan(s) stored as their ListBackupPlans element, without the plan body, first %s: %w"
	if r.planDenied.count > 0 {
		_ = skipIfAccessDenied(st, getOp, acct.ID, region, fmt.Errorf(kept, r.planDenied.count, r.planDenied.first, r.planDenied.err))
	}
	if r.planTransient.count > 0 {
		_ = skipIfTransient(st, getOp, acct.ID, region, fmt.Errorf(kept, r.planTransient.count, r.planTransient.first, r.planTransient.err))
	}
	if r.selectionsDenied.count > 0 {
		_ = skipIfAccessDenied(st, selectionsOp, acct.ID, region, fmt.Errorf(
			"%d backup plan(s) stored without their selections from the denied page on, first %s: %w",
			r.selectionsDenied.count, r.selectionsDenied.first, r.selectionsDenied.err))
	}
	if r.selectionsTransient.count > 0 {
		_ = skipIfTransient(st, selectionsOp, acct.ID, region, fmt.Errorf(
			"%d backup plan(s) stored without their selections from the failed page on, first %s: %w",
			r.selectionsTransient.count, r.selectionsTransient.first, r.selectionsTransient.err))
	}
}

// backupPlanAttrs returns the plan's GetBackupPlan body. A denied or transient
// read keeps the plan as its list element, a different shape, and returns
// that error as keptErr for the caller to tally once the plan is kept; gone
// reports a plan deleted since it was listed (ResourceNotFoundException, in
// the op's model); any other error is returned as err.
func backupPlanAttrs(ctx context.Context, client backupAPI, p backuptypes.BackupPlansListMember) (attrs string, gone bool, keptErr, err error) {
	out, err := client.GetBackupPlan(ctx, &backup.GetBackupPlanInput{BackupPlanId: p.BackupPlanId})
	switch {
	case err == nil:
		return mustJSON(out), false, nil, nil
	case isAPIErrorCode(err, "ResourceNotFoundException"):
		return "", true, nil, nil
	case isTransientNetworkError(err) || isAccessDenied(err):
		return mustJSON(p), false, err, nil
	default:
		return "", false, nil, fmt.Errorf("backup:GetBackupPlan %s: %w", sv(p.BackupPlanId), err)
	}
}

// scanBackupAll holds the testable scan body. It also returns the logically
// air-gapped vaults it listed, which parent the restore access vaults. Plans
// do not depend on vaults, so they are scanned whatever the vault listing
// returned; a denied vault listing keeps the pages read before it and warns.
func scanBackupAll(ctx context.Context, client backupAPI, acct *account, region string, st *store.Store, scanID string) (airGapped []childParent, total, inserted int, err error) {
	var (
		vaultBatch []*store.Resource
		noARN      skipTally
		vaultErr   error
	)
	vPager := backup.NewListBackupVaultsPaginator(client, &backup.ListBackupVaultsInput{})
	for vPager.HasMorePages() {
		page, err := vPager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				_ = skipIfAccessDenied(st, "backup:ListBackupVaults", acct.ID, region, fmt.Errorf(
					"listing truncated, vaults on this and later pages not stored: %w", err))
			} else {
				vaultErr = fmt.Errorf("backup:ListBackupVaults: %w", err)
			}
			break
		}
		for _, v := range page.BackupVaultList {
			arn := sv(v.BackupVaultArn)
			if arn == "" {
				noARN.skipped(fmt.Sprintf("%q", sv(v.BackupVaultName)), nil)
				continue
			}
			// VaultType=LOGICALLY_AIR_GAPPED_BACKUP_VAULT splits to its own
			// disco type so the AWS::Backup::LogicallyAirGappedBackupVault
			// CFN row maps to a real scanner row.
			vtype := TypeBackupVault
			if v.VaultType == backuptypes.VaultTypeLogicallyAirGappedBackupVault {
				vtype = TypeBackupLogicallyAirGappedVault
				if name := sv(v.BackupVaultName); name != "" {
					airGapped = append(airGapped, childParent{id: name, arn: arn})
				}
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
	warnNotStored(st, "backup:ListBackupVaults", acct, region, &noARN, "backup vaults have no BackupVaultArn")
	if len(vaultBatch) > 0 {
		n, err := st.UpsertResources(vaultBatch)
		if err != nil {
			return nil, 0, 0, errors.Join(vaultErr, fmt.Errorf("upsert Backup vaults: %w", err))
		}
		total += len(vaultBatch)
		inserted += n
	}

	var errs phaseErrs
	errs.add("backup:ListBackupVaults", vaultErr)
	t, i, err := scanBackupPlans(ctx, client, acct, region, st, scanID)
	errs.add("backup:ListBackupPlans", err)
	return airGapped, total + t, inserted + i, errs.join(st, acct, region)
}

// scanBackupPlans stores every plan, from its GetBackupPlan body (see
// backupPlanAttrs for when the list element is kept instead), and each plan's
// selections, linked under it. A plan GetBackupPlan or ListBackupSelections no
// longer finds was deleted since it was listed and is skipped with its
// selections. A denied listing keeps the pages read before it and warns. Any
// other listing error stops the listing. Any other GetBackupPlan error does
// not store that plan; any other ListBackupSelections error stores the plan
// with the selections read before it. Either way the scan goes on to the next
// plan, and every such error is returned after what was read is stored.
func scanBackupPlans(ctx context.Context, client backupAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var (
		rows  []containedChild
		reads backupPlanReads
		errs  phaseErrs
	)
	pPager := backup.NewListBackupPlansPaginator(client, &backup.ListBackupPlansInput{})
	for pPager.HasMorePages() {
		page, err := pPager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				_ = skipIfAccessDenied(st, "backup:ListBackupPlans", acct.ID, region, fmt.Errorf(
					"listing truncated, plans on this and later pages not stored: %w", err))
			} else {
				errs.add("backup:ListBackupPlans", fmt.Errorf("backup:ListBackupPlans: %w", err))
			}
			break
		}
		for _, p := range page.BackupPlansList {
			planARN, planID := sv(p.BackupPlanArn), sv(p.BackupPlanId)
			if planARN == "" || planID == "" {
				reads.planNoID.skipped(fmt.Sprintf("%q", sv(p.BackupPlanName)), nil)
				continue
			}
			attrsJSON, gone, keptErr, err := backupPlanAttrs(ctx, client, p)
			if err != nil {
				errs.add("backup:GetBackupPlan", err)
				continue
			}
			if gone {
				continue
			}
			sels, gone, err := listBackupSelections(ctx, client, acct, region, scanID, planARN, planID, &reads)
			if gone {
				continue
			}
			errs.add("backup:ListBackupSelections", err)
			// Tallied only once the plan is known to be kept, since the
			// warning says it was stored.
			if keptErr != nil && !reads.planTransient.drop(planARN, keptErr) {
				reads.planDenied.skipped(planARN, keptErr)
			}
			rows = append(rows, containedChild{row: &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeBackupPlan,
				NativeID:       planARN,
				Name:           p.BackupPlanName,
				Region:         &region,
				CreatedAt:      tp(p.CreationDate),
				AttributesJSON: attrsJSON,
				DiscoveredBy:   scanID,
			}})
			rows = append(rows, sels...)
		}
	}
	warnNotStored(st, "backup:ListBackupPlans", acct, region, &reads.planNoID, "backup plans have no BackupPlanArn or BackupPlanId")
	warnNotStored(st, "backup:ListBackupSelections", acct, region, &reads.selectionNoID, "backup selections have no SelectionId")
	total, inserted, err := upsertContained(st, "backup plans", rows)
	if err != nil {
		return total, inserted, errors.Join(errs.join(st, acct, region), err)
	}
	reads.warn(st, "backup:GetBackupPlan", "backup:ListBackupSelections", acct, region)
	return total, inserted, errs.join(st, acct, region)
}

// listBackupSelections builds one plan's selection rows, linked under the
// plan. A denied or transient page keeps the pages read before it and is
// tallied in reads. gone reports ResourceNotFoundException (in the op's
// model): the plan was deleted since it was read. Any other error is returned
// with the earlier pages' rows.
func listBackupSelections(ctx context.Context, client backupAPI, acct *account, region, scanID, planARN, planID string, reads *backupPlanReads) (rows []containedChild, gone bool, err error) {
	planResID := store.ResourceID("aws", acct.ID, planARN)
	sPager := backup.NewListBackupSelectionsPaginator(client, &backup.ListBackupSelectionsInput{BackupPlanId: &planID})
	for sPager.HasMorePages() {
		sp, err := sPager.NextPage(ctx)
		if err != nil {
			switch {
			case isAPIErrorCode(err, "ResourceNotFoundException"):
				return nil, true, nil
			case reads.selectionsTransient.drop(planARN, err):
				return rows, false, nil
			case isAccessDenied(err):
				reads.selectionsDenied.skipped(planARN, err)
				return rows, false, nil
			}
			return rows, false, fmt.Errorf("backup:ListBackupSelections %s: %w", planID, err)
		}
		for _, s := range sp.BackupSelectionsList {
			selID := sv(s.SelectionId)
			if selID == "" {
				reads.selectionNoID.skipped(planARN, nil)
				continue
			}
			rows = append(rows, containedChild{
				row: &store.Resource{
					Provider:       "aws",
					AccountID:      acct.ID,
					AccountName:    &acct.Name,
					Type:           TypeBackupSelection,
					NativeID:       arnFor(acct.partition(), "backup", region, acct.ID, "backup-plan:"+planID+"/selection/"+selID),
					Name:           s.SelectionName,
					Region:         &region,
					CreatedAt:      tp(s.CreationDate),
					AttributesJSON: mustJSON(s),
					DiscoveredBy:   scanID,
				},
				parentID: planResID,
			})
		}
	}
	return rows, false, nil
}

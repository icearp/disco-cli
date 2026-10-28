package aws

import (
	"cmp"
	"context"
	"maps"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
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
	ListTags(context.Context, *backup.ListTagsInput, ...func(*backup.Options)) (*backup.ListTagsOutput, error)
}

// backupPageCap is the MaxResults every Backup listing is sent: the maximum
// of the model's range trait for each list op this scanner calls, ListTags
// included, except ListBackupAccessPoints (backupAccessPointPageCap).
const backupPageCap = 1000

// backupNotFound is the code the model lists, for every op this scanner calls
// with a resource's id or ARN, for a resource that does not exist.
var backupNotFound = []string{"ResourceNotFoundException"}

// bkNoName keys the warning about an element with no ARN when it has no
// name either.
const bkNoName = "(an element with no name)"

// backupTags reads a row's tags with ListTags, sent the row's NativeID, the
// ARN its listing returned. The op pages its tags; every page is read, and a
// failed page fails the read. The Service Reference lists every type this
// scanner reads it for (backup vault, plan, framework, report plan, restore
// testing plan, tiering configuration, legal hold, recovery point and access
// point) under ListTags and TagResource.
//
// A row whose ARN names another account is read under otherAccountGap. Such
// rows may be listed: ListBackupVaults takes ByShared ("sort the list of vaults
// by shared vaults"), and a RESTORE_ACCESS_BACKUP_VAULT vault gives access to
// the recovery points of a logically air-gapped vault, which may be another
// account's; whether an unfiltered listing returns such rows is not verified.
// Every type read here has the account in its own ARNs, so the check passes
// this account's rows on.
func backupTags(client backupAPI, acctID string) rowTagReader {
	return rowTagReader{
		op:       "backup:ListTags",
		notFound: backupNotFound,
		read: func(ctx context.Context, r *store.Resource) (*string, error) {
			pages, err := readTokenPages(ctx, func(ctx context.Context, token *string) ([]map[string]string, *string, error) {
				out, err := client.ListTags(ctx, &backup.ListTagsInput{
					ResourceArn: sdkaws.String(r.NativeID), MaxResults: sdkaws.Int32(backupPageCap), NextToken: token,
				})
				if err != nil {
					return nil, nil, err
				}
				return []map[string]string{out.Tags}, out.NextToken, nil
			})
			if otherAccountGap(r.NativeID, acctID, err, backupNotFound) {
				return nil, nil
			}
			if err != nil {
				return nil, err
			}
			tags := map[string]string{}
			for _, page := range pages {
				maps.Copy(tags, page)
			}
			return mapTagsJSON(tags), nil
		},
	}
}

func scanBackup(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	client := backup.NewFromConfig(acct.cfg, func(o *backup.Options) { o.Region = region })
	return scanBackupAll(ctx, client, acct, region, st, scanID)
}

// scanBackupAll runs every AWS Backup listing as its own phase, the taggable
// types' rows each with their tags. A phase's error does not stop the later
// ones; a failed write and ctx's end do (runHostPhases). A child phase reads
// the parents its parent phase stored this scan: recovery points per vault,
// restore access vaults per logically air-gapped vault, selections per plan
// and restore testing selections per restore testing plan. The phases run
// one at a time, so each phase's own bound is the bound on the calls in
// flight: fanoutMed for most (readEachKey), but up to fanoutMed recovery
// point listings plus fanoutMed ListTags calls in the recovery point phase,
// which tag-reads each vault's pages while other vaults are listed
// (storeEachParentPagesTags).
func scanBackupAll(ctx context.Context, client backupAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	tags := backupTags(client, acct.ID)
	var vaults, airGapped, plans, testingPlans []childParent
	return runHostPhases(ctx, st, acct, region, "backup", 0, []hostPhase{
		{op: "backup:ListBackupVaults", run: func(p *phaseTotals) (failed bool) {
			vaults, airGapped, failed = scanBackupVaults(ctx, client, acct, region, st, scanID, tags, p)
			return failed
		}},
		{op: "backup:ListBackupPlans", run: func(p *phaseTotals) (failed bool) {
			plans, failed = scanBackupPlans(ctx, client, acct, region, st, scanID, tags, p)
			return failed
		}},
		countedPhase(0, "backup:ListBackupSelections", func() (int, int, error) {
			return scanBackupSelections(ctx, client, acct, region, st, scanID, plans)
		}),
		{op: "backup:ListFrameworks", run: func(p *phaseTotals) bool {
			return scanBackupFrameworks(ctx, client, acct, region, st, scanID, tags, p)
		}},
		{op: "backup:ListReportPlans", run: func(p *phaseTotals) bool {
			return scanBackupReportPlans(ctx, client, acct, region, st, scanID, tags, p)
		}},
		{op: "backup:ListRestoreTestingPlans", run: func(p *phaseTotals) (failed bool) {
			testingPlans, failed = scanBackupRestoreTestingPlans(ctx, client, acct, region, st, scanID, tags, p)
			return failed
		}},
		countedPhase(0, "backup:ListRestoreTestingSelections", func() (int, int, error) {
			return scanBackupRestoreTestingSelections(ctx, client, acct, region, st, scanID, testingPlans)
		}),
		{op: "backup:ListTieringConfigurations", run: func(p *phaseTotals) bool {
			return scanBackupTieringConfigurations(ctx, client, acct, region, st, scanID, tags, p)
		}},
		{op: "backup:ListLegalHolds", run: func(p *phaseTotals) bool {
			return scanBackupLegalHolds(ctx, client, acct, region, st, scanID, tags, p)
		}},
		{op: "backup:ListRecoveryPointsByBackupVault", run: func(p *phaseTotals) bool {
			return scanBackupRecoveryPoints(ctx, client, acct, region, st, scanID, vaults, tags, p)
		}},
		{op: "backup:ListBackupAccessPoints", run: func(p *phaseTotals) bool {
			return scanBackupAccessPoints(ctx, client, acct, region, st, scanID, tags, p)
		}},
		countedPhase(0, "backup:ListRestoreAccessBackupVaults", func() (int, int, error) {
			return scanBackupRestoreAccessVaults(ctx, client, acct, region, st, scanID, airGapped)
		}),
	})
}

// bkRow is the row of a listed element stored under its ARN, the element
// verbatim as the attributes.
func bkRow(acct *account, region, scanID, typ, arn string, name *string, item any) *store.Resource {
	return &store.Resource{
		Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
		Type: typ, NativeID: arn,
		Name: name, Region: &region, AttributesJSON: mustJSON(item), DiscoveredBy: scanID,
	}
}

// scanBackupVaults stores the vaults with their tags and returns the stored
// ones keyed by name: every vault, the parents of the recovery-point phase,
// and the logically air-gapped ones, the parents of the restore-access-vault
// phase. VaultType LOGICALLY_AIR_GAPPED_BACKUP_VAULT is stored as its own
// type, so the AWS::Backup::LogicallyAirGappedBackupVault CFN row maps to a
// scanner row.
func scanBackupVaults(ctx context.Context, client backupAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) (vaults, airGapped []childParent, storeWriteFailed bool) {
	isAirGapped := map[string]bool{}
	vaults, failed := storeParentListTags(ctx, st, acct, region, "backup:ListBackupVaults", "backup vaults", "BackupVaultName",
		func(ctx context.Context, token *string) ([]backuptypes.BackupVaultListMember, *string, error) {
			out, err := client.ListBackupVaults(ctx, &backup.ListBackupVaultsInput{NextToken: token, MaxResults: sdkaws.Int32(backupPageCap)})
			if err != nil {
				return nil, nil, err
			}
			return out.BackupVaultList, out.NextToken, nil
		},
		func(v backuptypes.BackupVaultListMember) (*store.Resource, string, string) {
			arn := sv(v.BackupVaultArn)
			if arn == "" {
				return nil, "backup vaults have no BackupVaultArn", cmp.Or(sv(v.BackupVaultName), bkNoName)
			}
			typ := TypeBackupVault
			if v.VaultType == backuptypes.VaultTypeLogicallyAirGappedBackupVault {
				typ = TypeBackupLogicallyAirGappedVault
				isAirGapped[arn] = true
			}
			r := bkRow(acct, region, scanID, typ, arn, v.BackupVaultName, v)
			r.CreatedAt = tp(v.CreationDate)
			return r, "", ""
		},
		func(v backuptypes.BackupVaultListMember) string { return sv(v.BackupVaultName) },
		tags, p)
	for _, v := range vaults {
		if isAirGapped[v.arn] {
			airGapped = append(airGapped, v)
		}
	}
	return vaults, airGapped, failed
}

// scanBackupPlans stores the plans, each as its GetBackupPlan body, with
// their tags, and returns the stored ones keyed by BackupPlanId, the parents
// of the selection phase. A plan GetBackupPlan no longer finds was deleted
// since it was listed and is not stored; any other GetBackupPlan failure
// stores the plan as its list element (readRowDetails).
func scanBackupPlans(ctx context.Context, client backupAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) ([]childParent, bool) {
	const op = "backup:ListBackupPlans"
	ids := map[*store.Resource]string{}
	d := detailedRows{getOp: "backup:GetBackupPlan", what: "backup plan(s)", notFound: backupNotFound, tags: tags}
	total, inserted, plans, err := storeParentListWith(ctx, st, acct, region, op, "backup plans", "BackupPlanId",
		func(ctx context.Context, token *string) ([]backuptypes.BackupPlansListMember, *string, error) {
			out, err := client.ListBackupPlans(ctx, &backup.ListBackupPlansInput{NextToken: token, MaxResults: sdkaws.Int32(backupPageCap)})
			if err != nil {
				return nil, nil, err
			}
			return out.BackupPlansList, out.NextToken, nil
		},
		func(it backuptypes.BackupPlansListMember) (*store.Resource, string, string) {
			arn, id := sv(it.BackupPlanArn), sv(it.BackupPlanId)
			if arn == "" || id == "" {
				return nil, "backup plans have no BackupPlanArn or BackupPlanId", cmp.Or(sv(it.BackupPlanName), bkNoName)
			}
			r := bkRow(acct, region, scanID, TypeBackupPlan, arn, it.BackupPlanName, it)
			r.CreatedAt = tp(it.CreationDate)
			ids[r] = id
			return r, "", ""
		},
		func(it backuptypes.BackupPlansListMember) string { return sv(it.BackupPlanId) },
		d.before(ctx, st, acct, region, func(ctx context.Context, r *store.Resource) (any, error) {
			return client.GetBackupPlan(ctx, &backup.GetBackupPlanInput{BackupPlanId: sdkaws.String(ids[r])})
		}))
	if d.addPhase(p, op, total, inserted, err) {
		return nil, true
	}
	return plans, false
}

// scanBackupSelections stores each stored plan's selections, linked under
// it. A plan ListBackupSelections no longer finds was deleted since the plan
// phase read it; its row stays stored. The NativeID is synthetic, since a
// selection has no ARN: {planARN}/selection/{SelectionId}, with the plan ARN
// rebuilt in the backup-plan: form the resolvers trim it back to.
func scanBackupSelections(ctx context.Context, client backupAPI, acct *account, region string, st *store.Store, scanID string, plans []childParent) (int, int, error) {
	return storeChildrenEachParent(ctx, st, acct, region, "backup:ListBackupSelections", backupNotFound, plans, nil, nil,
		func(ctx context.Context, plan childParent, token *string) ([]backuptypes.BackupSelectionsListMember, *string, error) {
			out, err := client.ListBackupSelections(ctx, &backup.ListBackupSelectionsInput{
				BackupPlanId: sdkaws.String(plan.id), NextToken: token, MaxResults: sdkaws.Int32(backupPageCap),
			})
			if err != nil {
				return nil, nil, err
			}
			return out.BackupSelectionsList, out.NextToken, nil
		},
		func(plan childParent, s backuptypes.BackupSelectionsListMember) (*store.Resource, string, string) {
			selID := sv(s.SelectionId)
			if selID == "" {
				return nil, "backup selections have no SelectionId", cmp.Or(sv(s.SelectionName), bkNoName)
			}
			nativeID := arnFor(acct.partition(), "backup", region, acct.ID, "backup-plan:"+plan.id+"/selection/"+selID)
			r := bkRow(acct, region, scanID, TypeBackupSelection, nativeID, s.SelectionName, s)
			r.CreatedAt = tp(s.CreationDate)
			return r, "", ""
		})
}

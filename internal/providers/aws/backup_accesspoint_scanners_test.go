package aws

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/backup"
	backuptypes "github.com/aws/aws-sdk-go-v2/service/backup/types"
	"github.com/aws/smithy-go"
	"github.com/icearp/disco-cli/store"
)

var (
	bkDenied   = &smithy.GenericAPIError{Code: "AccessDeniedException", Message: "User: arn:aws:iam::123456789012:user/t is not authorized to perform: backup:List"}
	bkBadInput = &smithy.GenericAPIError{Code: "InvalidParameterValueException", Message: "bad"}
	bkGone     = &smithy.GenericAPIError{Code: "ResourceNotFoundException", Message: "vault not found"}
	bkStart    = time.Date(2026, 8, 10, 9, 0, 0, 0, time.UTC)
)

func bkClient(handlers map[string]func(in any) (any, error)) *backup.Client {
	return backup.NewFromConfig(opStubConfig(handlers, bkDenied))
}

func bkVaultARN(name string) string {
	return fmt.Sprintf("arn:aws:backup:%s:%s:backup-vault:%s", testRegion, testAccountID, name)
}

func bkAccessPointARN(name string) string {
	return fmt.Sprintf("arn:aws:backup:%s:%s:accesspoint/%s", testRegion, testAccountID, name)
}

func bkAccessPoint(name string) backuptypes.ListAccessPointsMember {
	return backuptypes.ListAccessPointsMember{
		AccessPointArn: ptrStr(bkAccessPointARN(name)), Name: ptrStr(name),
		Status: backuptypes.AccessPointStatusAvailable, CreationTime: &bkStart,
		RecoveryPointArn: ptrStr("arn:aws:backup:us-east-1:123456789012:recovery-point:rp-" + name),
	}
}

// bkAccessPointHandler serves page one (ap1) then, after fail is checked, page
// two (ap2 and an element with no ARN).
func bkAccessPointHandler(fail error) func(in any) (any, error) {
	return func(in any) (any, error) {
		if in.(*backup.ListBackupAccessPointsInput).NextToken == nil {
			return &backup.ListBackupAccessPointsOutput{BackupAccessPoints: []backuptypes.ListAccessPointsMember{bkAccessPoint("ap1")}, NextToken: ptrStr("p2")}, nil
		}
		if fail != nil {
			return nil, fail
		}
		return &backup.ListBackupAccessPointsOutput{BackupAccessPoints: []backuptypes.ListAccessPointsMember{bkAccessPoint("ap2"), {Name: ptrStr("no-arn")}}}, nil
	}
}

func scanBkAccessPoints(t *testing.T, st *store.Store, handler func(in any) (any, error)) (int, error) {
	t.Helper()
	client := bkClient(map[string]func(any) (any, error){"ListBackupAccessPoints": handler})
	total, _, err := scanBackupAccessPoints(context.Background(), client, newTestAccount(testAccountID), testRegion, st, testScanID)
	return total, err
}

func TestScanBackupAccessPoints_StoresEveryPageSkipsARNless(t *testing.T) {
	st := newTestStore(t)
	total, err := scanBkAccessPoints(t, st, bkAccessPointHandler(nil))
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	rows := rowsByNativeID(t, st, TypeBackupAccessPoint)
	if total != 2 || len(rows) != 2 {
		t.Fatalf("total = %d, rows = %v; want ap1 and ap2", total, rows)
	}
	row, ok := rows[bkAccessPointARN("ap2")]
	if !ok {
		t.Fatalf("missing %s in %v", bkAccessPointARN("ap2"), rows)
	}
	if sv(row.Name) != "ap2" || sv(row.Status) != "AVAILABLE" || sv(row.Region) != testRegion ||
		sv(row.CreatedAt) != sv(tp(&bkStart)) || !jsonHasString(t, row.AttributesJSON, "RecoveryPointArn", "arn:aws:backup:us-east-1:123456789012:recovery-point:rp-ap2") {
		t.Errorf("row name/status/region/created = %q/%q/%q/%q, attrs = %s", sv(row.Name), sv(row.Status), sv(row.Region), sv(row.CreatedAt), row.AttributesJSON)
	}
}

func TestScanBackupAccessPoints_Empty(t *testing.T) {
	st := newTestStore(t)
	total, err := scanBkAccessPoints(t, st, func(any) (any, error) { return &backup.ListBackupAccessPointsOutput{}, nil })
	if err != nil || total != 0 || len(rowsByNativeID(t, st, TypeBackupAccessPoint)) != 0 {
		t.Errorf("total = %d, err = %v; want 0 rows, nil", total, err)
	}
}

// A denial on page two warns once and keeps page one's row; any other error
// fails the phase.
func TestScanBackupAccessPoints_FailedPage(t *testing.T) {
	t.Run("denied", func(t *testing.T) {
		st := newTestStore(t)
		warns := countOpWarnings(st, "backup:ListBackupAccessPoints")
		total, err := scanBkAccessPoints(t, st, bkAccessPointHandler(bkDenied))
		if err != nil {
			t.Fatalf("err = %v, want nil", err)
		}
		if *warns != 1 {
			t.Errorf("warnings = %d, want 1", *warns)
		}
		if _, ok := rowsByNativeID(t, st, TypeBackupAccessPoint)[bkAccessPointARN("ap1")]; total != 1 || !ok {
			t.Errorf("total = %d, want page one's ap1 stored", total)
		}
	})
	t.Run("other", func(t *testing.T) {
		st := newTestStore(t)
		_, err := scanBkAccessPoints(t, st, bkAccessPointHandler(bkBadInput))
		assertAPICode(t, err, "InvalidParameterValueException")
	})
}

func bkRestoreVaultARN(acct, name string) string {
	return fmt.Sprintf("arn:aws:backup:%s:%s:backup-vault:%s", testRegion, acct, name)
}

func bkRestoreNativeID(parent, restoreVaultARN string) string {
	return bkVaultARN(parent) + "/restore-access-backup-vault/" + restoreVaultARN
}

// bkRestoreHandler lists, for vault "lag1", one restore access vault per page
// over two pages plus one element with no ARN; for any other vault one restore
// access vault in another account. A vault in fail returns that error.
func bkRestoreHandler(calls *parentCalls, fail map[string]error) func(in any) (any, error) {
	return func(in any) (any, error) {
		p := in.(*backup.ListRestoreAccessBackupVaultsInput)
		name := sv(p.BackupVaultName)
		calls.add(name)
		if err := fail[name]; err != nil {
			return nil, err
		}
		if name != "lag1" {
			return &backup.ListRestoreAccessBackupVaultsOutput{RestoreAccessBackupVaults: []backuptypes.RestoreAccessBackupVaultListMember{
				{RestoreAccessBackupVaultArn: ptrStr(bkRestoreVaultARN("999999999999", "ra-"+name))},
			}}, nil
		}
		if p.NextToken == nil {
			return &backup.ListRestoreAccessBackupVaultsOutput{NextToken: ptrStr("p2"), RestoreAccessBackupVaults: []backuptypes.RestoreAccessBackupVaultListMember{
				{RestoreAccessBackupVaultArn: ptrStr(bkRestoreVaultARN(testAccountID, "ra1")), VaultState: backuptypes.VaultStateAvailable, CreationDate: &bkStart, ApprovalDate: &bkStart},
			}}, nil
		}
		return &backup.ListRestoreAccessBackupVaultsOutput{RestoreAccessBackupVaults: []backuptypes.RestoreAccessBackupVaultListMember{
			{RestoreAccessBackupVaultArn: ptrStr(bkRestoreVaultARN(testAccountID, "ra2"))},
			{VaultState: backuptypes.VaultStateAvailable},
		}}, nil
	}
}

func bkAirGapped(names ...string) []childParent {
	var out []childParent
	for _, n := range names {
		out = append(out, childParent{id: n, arn: bkVaultARN(n)})
	}
	return out
}

func scanBkRestore(t *testing.T, st *store.Store, calls *parentCalls, fail map[string]error, parents []childParent) (int, error) {
	t.Helper()
	client := bkClient(map[string]func(any) (any, error){"ListRestoreAccessBackupVaults": bkRestoreHandler(calls, fail)})
	total, _, err := scanBackupRestoreAccessVaults(context.Background(), client, newTestAccount(testAccountID), testRegion, st, testScanID, parents)
	return total, err
}

func TestScanBackupRestoreAccessVaults_PairsUnderEveryAirGappedVault(t *testing.T) {
	st := newTestStore(t)
	upsertTestResource(t, st, "aws", testAccountID, TypeBackupLogicallyAirGappedVault, bkVaultARN("lag1"), testRegion, "{}")
	upsertTestResource(t, st, "aws", testAccountID, TypeBackupLogicallyAirGappedVault, bkVaultARN("lag2"), testRegion, "{}")
	var calls parentCalls
	total, err := scanBkRestore(t, st, &calls, nil, bkAirGapped("lag1", "lag2"))
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if got := calls.distinct(); !slices.Equal(got, []string{"lag1", "lag2"}) {
		t.Errorf("vaults queried = %v, want [lag1 lag2]", got)
	}
	ra1 := bkRestoreNativeID("lag1", bkRestoreVaultARN(testAccountID, "ra1"))
	ra2 := bkRestoreNativeID("lag1", bkRestoreVaultARN(testAccountID, "ra2"))
	foreign := bkRestoreNativeID("lag2", bkRestoreVaultARN("999999999999", "ra-lag2"))
	rows := rowsByNativeID(t, st, TypeBackupRestoreAccessBackupVault)
	var got []string
	for id := range rows {
		got = append(got, id)
	}
	slices.Sort(got)
	want := []string{ra1, ra2, foreign}
	slices.Sort(want)
	if total != 3 || !slices.Equal(got, want) {
		t.Fatalf("total = %d, rows = %v; want %v", total, got, want)
	}
	assertContainsNative(t, st, bkVaultARN("lag1"), ra1)
	assertContainsNative(t, st, bkVaultARN("lag1"), ra2)
	assertContainsNative(t, st, bkVaultARN("lag2"), foreign)
	assertContainsEdgesDeclared(t, st)

	row := rows[ra1]
	var attrs backuptypes.RestoreAccessBackupVaultListMember
	if err := json.Unmarshal([]byte(row.AttributesJSON), &attrs); err != nil {
		t.Fatalf("attrs: %v", err)
	}
	if sv(row.Name) != bkRestoreVaultARN(testAccountID, "ra1") || sv(row.Status) != "AVAILABLE" || sv(row.Region) != testRegion ||
		sv(row.CreatedAt) != sv(tp(&bkStart)) || attrs.ApprovalDate == nil || !attrs.ApprovalDate.Equal(bkStart) {
		t.Errorf("row name/status/region/created = %q/%q/%q/%q, attrs = %s", sv(row.Name), sv(row.Status), sv(row.Region), sv(row.CreatedAt), row.AttributesJSON)
	}
	if rows[ra2].Status != nil {
		t.Errorf("ra2 status = %q, want unset for an absent VaultState", sv(rows[ra2].Status))
	}
}

func TestScanBackupRestoreAccessVaults_NoAirGappedVaultsNoCall(t *testing.T) {
	st := newTestStore(t)
	var calls parentCalls
	total, err := scanBkRestore(t, st, &calls, nil, nil)
	if err != nil || total != 0 || len(calls.distinct()) != 0 {
		t.Fatalf("total = %d, err = %v, calls = %v; want 0, nil, none", total, err, calls.distinct())
	}
}

// A failed vault drops only itself: a vault deleted since listing is skipped
// silently, a denial warns once however many vaults hit it, and any other
// error fails the phase.
func TestScanBackupRestoreAccessVaults_FailedVault(t *testing.T) {
	for _, tc := range []struct {
		name     string
		err      error
		wantWarn int
	}{
		{"vault gone", bkGone, 0},
		{"denied", bkDenied, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := countOpWarnings(st, "backup:ListRestoreAccessBackupVaults")
			var calls parentCalls
			total, err := scanBkRestore(t, st, &calls, map[string]error{"bad1": tc.err, "bad2": tc.err}, bkAirGapped("bad1", "ok", "bad2"))
			if err != nil {
				t.Fatalf("err = %v, want nil", err)
			}
			if *warns != tc.wantWarn {
				t.Errorf("warnings = %d, want %d", *warns, tc.wantWarn)
			}
			id := bkRestoreNativeID("ok", bkRestoreVaultARN("999999999999", "ra-ok"))
			if _, ok := rowsByNativeID(t, st, TypeBackupRestoreAccessBackupVault)[id]; total != 1 || !ok {
				t.Errorf("total = %d, want the healthy vault's row stored", total)
			}
		})
	}
	t.Run("other", func(t *testing.T) {
		st := newTestStore(t)
		var calls parentCalls
		_, err := scanBkRestore(t, st, &calls, map[string]error{"bad": bkBadInput}, bkAirGapped("bad"))
		assertAPICode(t, err, "InvalidParameterValueException")
	})
}

// bkTopLevelHandlers answers the vault list (one standard vault, one
// air-gapped vault, and the restore access vault ra1 that lag1 lists), the last pre-existing phase (recovery points) and the new phases;
// every other op is denied and warn-skipped by its phase.
func bkTopLevelHandlers(calls *parentCalls) map[string]func(in any) (any, error) {
	return map[string]func(in any) (any, error){
		"ListBackupVaults": func(any) (any, error) {
			return &backup.ListBackupVaultsOutput{BackupVaultList: []backuptypes.BackupVaultListMember{
				{BackupVaultArn: ptrStr(bkVaultARN("std")), BackupVaultName: ptrStr("std")},
				{BackupVaultArn: ptrStr(bkVaultARN("lag1")), BackupVaultName: ptrStr("lag1"), VaultType: backuptypes.VaultTypeLogicallyAirGappedBackupVault},
				{BackupVaultArn: ptrStr(bkRestoreVaultARN(testAccountID, "ra1")), BackupVaultName: ptrStr("ra1"), VaultType: backuptypes.VaultTypeRestoreAccessBackupVault},
			}}, nil
		},
		"ListRecoveryPointsByBackupVault": func(in any) (any, error) {
			name := sv(in.(*backup.ListRecoveryPointsByBackupVaultInput).BackupVaultName)
			return &backup.ListRecoveryPointsByBackupVaultOutput{RecoveryPoints: []backuptypes.RecoveryPointByBackupVault{
				{RecoveryPointArn: ptrStr("arn:aws:backup:us-east-1:123456789012:recovery-point:in-" + name)},
			}}, nil
		},
		"ListBackupAccessPoints":        bkAccessPointHandler(nil),
		"ListRestoreAccessBackupVaults": bkRestoreHandler(calls, nil),
	}
}

func TestScanBackup_StoresAccessPointsAndRestoreAccessVaults(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	var calls parentCalls
	acct.cfg = opStubConfig(bkTopLevelHandlers(&calls), bkDenied)

	if _, _, err := scanBackup(context.Background(), acct, testRegion, st, testScanID); err != nil {
		t.Fatalf("scanBackup: %v", err)
	}
	if got := calls.distinct(); !slices.Equal(got, []string{"lag1"}) {
		t.Errorf("restore access listed for %v, want only the air-gapped vault [lag1]", got)
	}
	if n := len(rowsByNativeID(t, st, TypeBackupAccessPoint)); n != 2 {
		t.Errorf("access point rows = %d, want 2", n)
	}
	assertContainsNative(t, st, bkVaultARN("lag1"), bkRestoreNativeID("lag1", bkRestoreVaultARN(testAccountID, "ra1")))
}

// A same-account restore access vault is both a vault ListBackupVaults lists
// and a restore access grant its air-gapped vault lists; the two rows must keep
// separate identities, not merge into one version chain.
func TestScanBackup_RestoreAccessVaultKeepsIdentityApartFromVaultRow(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	var calls parentCalls
	acct.cfg = opStubConfig(bkTopLevelHandlers(&calls), bkDenied)

	if _, _, err := scanBackup(context.Background(), acct, testRegion, st, testScanID); err != nil {
		t.Fatalf("scanBackup: %v", err)
	}
	ra1 := bkRestoreVaultARN(testAccountID, "ra1")
	vault, ok := rowsByNativeID(t, st, TypeBackupVault)[ra1]
	if !ok {
		t.Fatalf("no %s row for %s", TypeBackupVault, ra1)
	}
	var grantIDs []string
	for _, r := range rowsByNativeID(t, st, TypeBackupRestoreAccessBackupVault) {
		if sv(r.Name) == ra1 {
			grantIDs = append(grantIDs, r.ID)
		}
	}
	if len(grantIDs) != 1 || grantIDs[0] == vault.ID {
		t.Errorf("restore access rows for %s = %v, vault row id = %s; want one row with its own id", ra1, grantIDs, vault.ID)
	}
}

// The client is pinned to the scanned region, not the config's default.
func TestScanBackup_CallsGoToScannedRegion(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	var calls, regions parentCalls
	cfg := opStubConfig(bkTopLevelHandlers(&calls), bkDenied)
	cfg.Region = "ap-southeast-2"
	acct.cfg = recordCallRegions(cfg, &regions)

	if _, _, err := scanBackup(context.Background(), acct, "eu-west-1", st, testScanID); err != nil {
		t.Fatalf("scanBackup: %v", err)
	}
	if got := regions.distinct(); !slices.Equal(got, []string{"eu-west-1"}) {
		t.Errorf("calls went to regions %v, want [eu-west-1]", got)
	}
	if row, ok := rowsByNativeID(t, st, TypeBackupAccessPoint)[bkAccessPointARN("ap1")]; !ok || sv(row.Region) != "eu-west-1" {
		t.Errorf("access point row = %+v (found %v), want region eu-west-1", row, ok)
	}
}

// Each new phase runs after every pre-existing phase, and its hard error
// reaches the caller.
func TestScanBackup_NewPhaseErrorAfterExistingPhases(t *testing.T) {
	for _, op := range []string{"ListBackupAccessPoints", "ListRestoreAccessBackupVaults"} {
		t.Run(op, func(t *testing.T) {
			st := newTestStore(t)
			acct := newTestAccount(testAccountID)
			var calls parentCalls
			handlers := bkTopLevelHandlers(&calls)
			var failed atomic.Bool
			handlers[op] = func(any) (any, error) {
				failed.Store(true)
				return nil, bkBadInput
			}
			acct.cfg = opStubConfig(handlers, bkDenied)

			_, _, err := scanBackup(context.Background(), acct, testRegion, st, testScanID)
			if !failed.Load() {
				t.Fatalf("%s never called", op)
			}
			assertAPICode(t, err, "InvalidParameterValueException")
			if n := len(rowsByNativeID(t, st, TypeBackupRecoveryPoint)); n != 3 {
				t.Errorf("recovery point rows = %d, want 3: %s ran before the last pre-existing phase", n, op)
			}
		})
	}
}

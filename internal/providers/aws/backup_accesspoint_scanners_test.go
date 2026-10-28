package aws

import (
	"context"
	"encoding/json"
	"slices"
	"testing"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/backup"
	backuptypes "github.com/aws/aws-sdk-go-v2/service/backup/types"
	"github.com/icearp/disco-cli/store"
)

func bkClient(handlers map[string]func(in any) (any, error)) *backup.Client {
	return backup.NewFromConfig(opStubConfig(handlers, bkDenied))
}

// An access point is stored with its Name, Status, CreationTime and listed
// element.
func TestScanBackup_AccessPointFields(t *testing.T) {
	st := newTestStore(t)
	if _, _, err := bkScan(context.Background(), st, bkHandlers(newBKCalls())); err != nil {
		t.Fatalf("scan: %v", err)
	}
	k := bkKindOf(t, "ListBackupAccessPoints")
	row, ok := bkRowOf(t, st, TypeBackupAccessPoint, bkKindARN(k, "bare"))
	if !ok {
		t.Fatalf("access point %s missing", bkKindARN(k, "bare"))
	}
	if sv(row.Name) != "ap-bare" || sv(row.Status) != "AVAILABLE" || sv(row.CreatedAt) != sv(tp(&bkStart)) ||
		!jsonHasString(t, row.AttributesJSON, "RecoveryPointArn", bkARN("recovery-point:of-ap-bare")) {
		t.Errorf("row name/status/created = %q/%q/%q, attrs = %s", sv(row.Name), sv(row.Status), sv(row.CreatedAt), row.AttributesJSON)
	}
}

// bkRestoreHandler lists, for vault "lag1", one restore access vault per page
// over two pages; for any other vault one restore access vault in another
// account. A vault in fail returns that error.
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
				{RestoreAccessBackupVaultArn: sdkaws.String(bkRestoreVaultARN(bkOtherAccount, "ra-"+name))},
			}}, nil
		}
		if p.NextToken == nil {
			return &backup.ListRestoreAccessBackupVaultsOutput{NextToken: sdkaws.String("p2"), RestoreAccessBackupVaults: []backuptypes.RestoreAccessBackupVaultListMember{
				{RestoreAccessBackupVaultArn: sdkaws.String(bkRestoreVaultARN(testAccountID, "ra1")), VaultState: backuptypes.VaultStateAvailable, CreationDate: &bkStart, ApprovalDate: &bkStart},
			}}, nil
		}
		return &backup.ListRestoreAccessBackupVaultsOutput{RestoreAccessBackupVaults: []backuptypes.RestoreAccessBackupVaultListMember{
			{RestoreAccessBackupVaultArn: sdkaws.String(bkRestoreVaultARN(testAccountID, "ra2"))},
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
	foreign := bkRestoreNativeID("lag2", bkRestoreVaultARN(bkOtherAccount, "ra-lag2"))
	rows := rowsByNativeID(t, st, TypeBackupRestoreAccessBackupVault)
	got := nativeIDs(rows)
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
// error fails the phase after the other vaults' rows are stored.
func TestScanBackupRestoreAccessVaults_FailedVault(t *testing.T) {
	for _, tc := range []struct {
		name     string
		err      error
		code     string
		wantWarn int
	}{
		{"vault gone", bkGone, "", 0},
		{"denied", bkDenied, "", 1},
		{"other", bkBadInput, "InvalidParameterValueException", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := countOpWarnings(st, "backup:ListRestoreAccessBackupVaults")
			var calls parentCalls
			total, err := scanBkRestore(t, st, &calls, map[string]error{"bad1": tc.err, "bad2": tc.err}, bkAirGapped("bad1", "ok", "bad2"))
			if tc.code != "" {
				assertAPICode(t, err, tc.code)
			} else if err != nil {
				t.Fatalf("err = %v, want nil", err)
			}
			if *warns != tc.wantWarn {
				t.Errorf("warnings = %d, want %d", *warns, tc.wantWarn)
			}
			id := bkRestoreNativeID("ok", bkRestoreVaultARN(bkOtherAccount, "ra-ok"))
			if _, ok := rowsByNativeID(t, st, TypeBackupRestoreAccessBackupVault)[id]; total != 1 || !ok {
				t.Errorf("total = %d, want the healthy vault's row stored", total)
			}
		})
	}
}

// A same-account restore access vault is both a vault ListBackupVaults lists
// and a restore access grant its air-gapped vault lists; the two rows must keep
// separate identities, not merge into one version chain.
func TestScanBackup_RestoreAccessVaultKeepsIdentityApartFromVaultRow(t *testing.T) {
	st := newTestStore(t)
	ra := bkRestoreVaultARN(testAccountID, "ra-vault-bare")
	h := bkHandlers(newBKCalls())
	vaults := h["ListBackupVaults"]
	h["ListBackupVaults"] = func(in any) (any, error) {
		out, err := vaults(in)
		if err != nil || bacField[*string](in, "NextToken") == nil {
			return out, err
		}
		page := *out.(*backup.ListBackupVaultsOutput)
		page.BackupVaultList = append(slices.Clone(page.BackupVaultList), backuptypes.BackupVaultListMember{
			BackupVaultArn: &ra, BackupVaultName: sdkaws.String("ra-vault-bare"), VaultType: backuptypes.VaultTypeRestoreAccessBackupVault,
		})
		return &page, nil
	}
	h["ListRestoreAccessBackupVaults"] = func(any) (any, error) {
		return &backup.ListRestoreAccessBackupVaultsOutput{RestoreAccessBackupVaults: []backuptypes.RestoreAccessBackupVaultListMember{{RestoreAccessBackupVaultArn: &ra}}}, nil
	}

	if _, _, err := bkScan(context.Background(), st, h); err != nil {
		t.Fatalf("scanBackup: %v", err)
	}
	vault, ok := bkRowOf(t, st, TypeBackupVault, ra)
	if !ok {
		t.Fatalf("no %s row for %s", TypeBackupVault, ra)
	}
	grant, ok := bkRowOf(t, st, TypeBackupRestoreAccessBackupVault, bkRestoreNativeID("vault-bare", ra))
	if !ok || grant.ID == vault.ID {
		t.Errorf("restore access row stored = %v, id = %s, vault row id = %s; want its own id", ok, grant.ID, vault.ID)
	}
}

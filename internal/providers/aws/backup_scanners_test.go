package aws

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sync"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/backup"
	backuptypes "github.com/aws/aws-sdk-go-v2/service/backup/types"
	"github.com/icearp/disco-cli/internal/util"
	"github.com/icearp/disco-cli/store"
)

// stubBackup is an in-memory backupAPI used to verify variant vault dispatch.
type stubBackup struct {
	vaults     []backuptypes.BackupVaultListMember
	plans      []backuptypes.BackupPlansListMember
	selections map[string][]backuptypes.BackupSelectionsListMember
}

func (s *stubBackup) ListBackupVaults(_ context.Context, _ *backup.ListBackupVaultsInput, _ ...func(*backup.Options)) (*backup.ListBackupVaultsOutput, error) {
	return &backup.ListBackupVaultsOutput{BackupVaultList: s.vaults}, nil
}

func (s *stubBackup) ListBackupPlans(_ context.Context, _ *backup.ListBackupPlansInput, _ ...func(*backup.Options)) (*backup.ListBackupPlansOutput, error) {
	return &backup.ListBackupPlansOutput{BackupPlansList: s.plans}, nil
}

func (s *stubBackup) GetBackupPlan(_ context.Context, _ *backup.GetBackupPlanInput, _ ...func(*backup.Options)) (*backup.GetBackupPlanOutput, error) {
	return &backup.GetBackupPlanOutput{}, nil
}

func (s *stubBackup) ListBackupSelections(_ context.Context, in *backup.ListBackupSelectionsInput, _ ...func(*backup.Options)) (*backup.ListBackupSelectionsOutput, error) {
	id := ""
	if in.BackupPlanId != nil {
		id = *in.BackupPlanId
	}
	return &backup.ListBackupSelectionsOutput{BackupSelectionsList: s.selections[id]}, nil
}

func (s *stubBackup) ListFrameworks(_ context.Context, _ *backup.ListFrameworksInput, _ ...func(*backup.Options)) (*backup.ListFrameworksOutput, error) {
	return &backup.ListFrameworksOutput{}, nil
}

func (s *stubBackup) ListReportPlans(_ context.Context, _ *backup.ListReportPlansInput, _ ...func(*backup.Options)) (*backup.ListReportPlansOutput, error) {
	return &backup.ListReportPlansOutput{}, nil
}

func (s *stubBackup) ListRestoreTestingPlans(_ context.Context, _ *backup.ListRestoreTestingPlansInput, _ ...func(*backup.Options)) (*backup.ListRestoreTestingPlansOutput, error) {
	return &backup.ListRestoreTestingPlansOutput{}, nil
}

func (s *stubBackup) ListRestoreTestingSelections(_ context.Context, _ *backup.ListRestoreTestingSelectionsInput, _ ...func(*backup.Options)) (*backup.ListRestoreTestingSelectionsOutput, error) {
	return &backup.ListRestoreTestingSelectionsOutput{}, nil
}

func (s *stubBackup) ListTieringConfigurations(_ context.Context, _ *backup.ListTieringConfigurationsInput, _ ...func(*backup.Options)) (*backup.ListTieringConfigurationsOutput, error) {
	return &backup.ListTieringConfigurationsOutput{}, nil
}

func (s *stubBackup) ListLegalHolds(_ context.Context, _ *backup.ListLegalHoldsInput, _ ...func(*backup.Options)) (*backup.ListLegalHoldsOutput, error) {
	return &backup.ListLegalHoldsOutput{}, nil
}

func (s *stubBackup) ListRecoveryPointsByBackupVault(_ context.Context, _ *backup.ListRecoveryPointsByBackupVaultInput, _ ...func(*backup.Options)) (*backup.ListRecoveryPointsByBackupVaultOutput, error) {
	return &backup.ListRecoveryPointsByBackupVaultOutput{}, nil
}

func (s *stubBackup) ListBackupAccessPoints(_ context.Context, _ *backup.ListBackupAccessPointsInput, _ ...func(*backup.Options)) (*backup.ListBackupAccessPointsOutput, error) {
	return &backup.ListBackupAccessPointsOutput{}, nil
}

func (s *stubBackup) ListRestoreAccessBackupVaults(_ context.Context, _ *backup.ListRestoreAccessBackupVaultsInput, _ ...func(*backup.Options)) (*backup.ListRestoreAccessBackupVaultsOutput, error) {
	return &backup.ListRestoreAccessBackupVaultsOutput{}, nil
}

func TestScanBackup_VaultVariantSplit(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	stdARN := fmt.Sprintf("arn:aws:backup:%s:%s:backup-vault:Default", testRegion, acct.ID)
	airARN := fmt.Sprintf("arn:aws:backup:%s:%s:backup-vault:AirGapped", testRegion, acct.ID)
	stdName := "Default"
	airName := "AirGapped"

	stub := &stubBackup{
		vaults: []backuptypes.BackupVaultListMember{
			{BackupVaultArn: &stdARN, BackupVaultName: &stdName},
			{
				BackupVaultArn:  &airARN,
				BackupVaultName: &airName,
				VaultType:       backuptypes.VaultTypeLogicallyAirGappedBackupVault,
			},
		},
	}

	airGapped, total, _, err := scanBackupAll(context.Background(), stub, acct, testRegion, st, testScanID)
	if err != nil {
		t.Fatalf("scanBackupAll: %v", err)
	}
	if total != 2 {
		t.Fatalf("total=%d want 2", total)
	}
	if want := []childParent{{id: airName, arn: airARN}}; !slices.Equal(airGapped, want) {
		t.Errorf("air-gapped parents = %v, want %v", airGapped, want)
	}

	stdID := store.ResourceID("aws", acct.ID, stdARN)
	airID := store.ResourceID("aws", acct.ID, airARN)
	if _, err := st.GetResource(stdID); err != nil {
		t.Errorf("standard vault missing: %v", err)
	}
	if _, err := st.GetResource(airID); err != nil {
		t.Errorf("logically-air-gapped vault missing: %v", err)
	}
}

func bkPlanARN(id string) string {
	return fmt.Sprintf("arn:aws:backup:%s:%s:backup-plan:%s", testRegion, testAccountID, id)
}

func bkSelectionARN(planID string) string {
	return bkPlanARN(planID) + "/selection/sel-" + planID
}

// bkPlanHandlers adds to bkTopLevelHandlers plans p1, p2 (second page) and p3,
// each with one selection, plus a plan and a selection that carry no id.
// GetBackupPlan and ListBackupSelections fail for the plan ids in their maps.
func bkPlanHandlers(getFail, selFail map[string]error) map[string]func(in any) (any, error) {
	var calls parentCalls
	h := bkTopLevelHandlers(&calls)
	plan := func(id string) backuptypes.BackupPlansListMember {
		return backuptypes.BackupPlansListMember{BackupPlanArn: ptrStr(bkPlanARN(id)), BackupPlanId: ptrStr(id), BackupPlanName: ptrStr("name-" + id)}
	}
	h["ListBackupPlans"] = func(in any) (any, error) {
		if in.(*backup.ListBackupPlansInput).NextToken == nil {
			return &backup.ListBackupPlansOutput{BackupPlansList: []backuptypes.BackupPlansListMember{plan("p1"), {BackupPlanName: ptrStr("idless")}}, NextToken: ptrStr("t2")}, nil
		}
		return &backup.ListBackupPlansOutput{BackupPlansList: []backuptypes.BackupPlansListMember{plan("p2"), plan("p3")}}, nil
	}
	h["GetBackupPlan"] = func(in any) (any, error) {
		id := sv(in.(*backup.GetBackupPlanInput).BackupPlanId)
		if err := getFail[id]; err != nil {
			return nil, err
		}
		return &backup.GetBackupPlanOutput{BackupPlanId: &id, BackupPlan: &backuptypes.BackupPlan{BackupPlanName: ptrStr("name-" + id)}}, nil
	}
	h["ListBackupSelections"] = func(in any) (any, error) {
		id := sv(in.(*backup.ListBackupSelectionsInput).BackupPlanId)
		if err := selFail[id]; err != nil {
			return nil, err
		}
		return &backup.ListBackupSelectionsOutput{BackupSelectionsList: []backuptypes.BackupSelectionsListMember{
			{SelectionId: ptrStr("sel-" + id), BackupPlanId: &id}, {BackupPlanId: &id},
		}}, nil
	}
	return h
}

// bkPlanBody reports whether a stored plan holds the GetBackupPlan body
// rather than its ListBackupPlans element.
func bkPlanBody(t *testing.T, r store.Resource) bool {
	t.Helper()
	var attrs struct {
		BackupPlan     *backuptypes.BackupPlan
		BackupPlanName *string
	}
	if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
		t.Fatalf("attrs of %s: %v", r.NativeID, err)
	}
	if attrs.BackupPlan == nil && attrs.BackupPlanName == nil {
		t.Fatalf("attrs of %s = %s, neither the body nor the list element", r.NativeID, r.AttributesJSON)
	}
	return attrs.BackupPlan != nil
}

// Plan and selection reads: a denial or transient error on GetBackupPlan keeps
// the plan as its list element and its selections, warning once per cause; a
// plan GetBackupPlan or ListBackupSelections no longer finds is skipped with
// its selections; a denied or transient ListBackupSelections keeps the plan
// and warns once. Any other error fails the scan after the other plans are
// stored, reports every stored row in the total, and still runs the later
// phases. An id-less plan or selection is not stored
// and warns once per op.
func TestScanBackup_PlanReadErrors(t *testing.T) {
	throttled := apiErr("ThrottlingException", "Rate exceeded")
	cases := []struct {
		name             string
		getFail, selFail map[string]error
		wantPlans        []string // plan ids stored; a * suffix marks the list element kept
		wantSelections   []string
		wantWarns        map[string]int
		wantCode         string
	}{
		{"read", nil, nil, []string{"p1", "p2", "p3"}, []string{"p1", "p2", "p3"}, nil, ""},
		{
			"plan denied",
			map[string]error{"p1": bkDenied, "p3": bkDenied},
			nil,
			[]string{"p1*", "p2", "p3*"},
			[]string{"p1", "p2", "p3"},
			map[string]int{"backup:GetBackupPlan": 1},
			"",
		},
		{
			"plan transient",
			map[string]error{"p1": throttled, "p3": throttled},
			nil,
			[]string{"p1*", "p2", "p3*"},
			[]string{"p1", "p2", "p3"},
			map[string]int{"backup:GetBackupPlan": 1},
			"",
		},
		{
			"plan gone",
			map[string]error{"p2": apiErr("ResourceNotFoundException", "gone")},
			nil,
			[]string{"p1", "p3"},
			[]string{"p1", "p3"},
			nil, "",
		},
		{
			"plan other error",
			map[string]error{"p2": bkBadInput},
			nil,
			[]string{"p1", "p3"},
			[]string{"p1", "p3"},
			nil, "InvalidParameterValueException",
		},
		{
			"selections denied", nil,
			map[string]error{"p1": bkDenied, "p2": bkDenied},
			[]string{"p1", "p2", "p3"},
			[]string{"p3"},
			map[string]int{"backup:ListBackupSelections": 1},
			"",
		},
		{
			"selections transient", nil,
			map[string]error{"p1": throttled, "p2": throttled},
			[]string{"p1", "p2", "p3"},
			[]string{"p3"},
			map[string]int{"backup:ListBackupSelections": 1},
			"",
		},
		{
			// The dropped plan is not warned about as stored.
			"plan denied, selections plan gone",
			map[string]error{"p2": bkDenied},
			map[string]error{"p2": apiErr("ResourceNotFoundException", "gone")},
			[]string{"p1", "p3"},
			[]string{"p1", "p3"},
			nil, "",
		},
		{
			"selections plan gone", nil,
			map[string]error{"p2": apiErr("ResourceNotFoundException", "gone")},
			[]string{"p1", "p3"},
			[]string{"p1", "p3"},
			nil, "",
		},
		{
			"selections other error", nil,
			map[string]error{"p2": bkBadInput},
			[]string{"p1", "p2", "p3"},
			[]string{"p1", "p3"},
			nil, "InvalidParameterValueException",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := map[string]int{}
			var mu sync.Mutex
			st.OnWarn = func(w store.ScanWarning) {
				mu.Lock()
				warns[w.Service]++
				mu.Unlock()
			}
			acct := newTestAccount(testAccountID)
			acct.cfg = opStubConfig(bkPlanHandlers(tc.getFail, tc.selFail), bkDenied)
			total, _, err := scanBackup(context.Background(), acct, testRegion, st, testScanID)
			if tc.wantCode != "" {
				assertAPICode(t, err, tc.wantCode)
			} else if err != nil {
				t.Fatalf("scanBackup: %v", err)
			}

			plans := rowsByNativeID(t, st, TypeBackupPlan)
			var gotPlans []string
			for _, id := range []string{"p1", "p2", "p3"} {
				if r, ok := plans[bkPlanARN(id)]; ok {
					if !bkPlanBody(t, r) {
						id += "*"
					}
					gotPlans = append(gotPlans, id)
				}
			}
			if !slices.Equal(gotPlans, tc.wantPlans) || len(plans) != len(tc.wantPlans) {
				t.Errorf("plans = %v (%d rows), want %v", gotPlans, len(plans), tc.wantPlans)
			}
			var wantSel []string
			for _, id := range tc.wantSelections {
				wantSel = append(wantSel, bkSelectionARN(id))
				assertContainsNative(t, st, bkPlanARN(id), bkSelectionARN(id))
			}
			if got := nativeIDs(rowsByNativeID(t, st, TypeBackupSelection)); !slices.Equal(got, wantSel) {
				t.Errorf("selections = %v, want %v", got, wantSel)
			}

			want := map[string]int{"backup:ListBackupPlans": 1}
			if len(tc.wantSelections) > 0 {
				want["backup:ListBackupSelections"] = 1
			}
			for op, n := range tc.wantWarns {
				want[op] += n
			}
			for _, op := range []string{"backup:GetBackupPlan", "backup:ListBackupPlans", "backup:ListBackupSelections"} {
				if warns[op] != want[op] {
					t.Errorf("%s warnings = %d, want %d", op, warns[op], want[op])
				}
			}

			all, lerr := st.ListResources(store.ResourceFilter{IncludeManaged: true, Limit: util.AllResources})
			if lerr != nil {
				t.Fatal(lerr)
			}
			if total != len(all) {
				t.Errorf("total = %d, want %d, every row the scan stored", total, len(all))
			}
			if n := len(rowsByNativeID(t, st, TypeBackupAccessPoint)); n != 2 {
				t.Errorf("access point rows = %d, want 2: later phases must run", n)
			}
		})
	}
}

// A denied ListBackupPlans page keeps the plans of the pages before it and
// warns once.
func TestScanBackup_ListBackupPlansDeniedKeepsEarlierPages(t *testing.T) {
	st := newTestStore(t)
	warns := countOpWarnings(st, "backup:ListBackupPlans")
	h := bkPlanHandlers(nil, nil)
	pages := h["ListBackupPlans"]
	h["ListBackupPlans"] = func(in any) (any, error) {
		if in.(*backup.ListBackupPlansInput).NextToken != nil {
			return nil, bkDenied
		}
		return pages(in)
	}
	acct := newTestAccount(testAccountID)
	acct.cfg = opStubConfig(h, bkDenied)
	if _, _, err := scanBackup(context.Background(), acct, testRegion, st, testScanID); err != nil {
		t.Fatalf("scanBackup: %v", err)
	}
	// One for the denial, one for page one's id-less plan.
	if *warns != 2 {
		t.Errorf("backup:ListBackupPlans warnings = %d, want 2", *warns)
	}
	if got := nativeIDs(rowsByNativeID(t, st, TypeBackupPlan)); !slices.Equal(got, []string{bkPlanARN("p1")}) {
		t.Errorf("plans = %v, want only page one's %s", got, bkPlanARN("p1"))
	}
}

// A vault listed without an ARN is not stored and is warned about once.
func TestScanBackup_ARNlessVaultWarns(t *testing.T) {
	st := newTestStore(t)
	warns := countOpWarnings(st, "backup:ListBackupVaults")
	stdARN := fmt.Sprintf("arn:aws:backup:%s:%s:backup-vault:Default", testRegion, testAccountID)
	stub := &stubBackup{vaults: []backuptypes.BackupVaultListMember{
		{BackupVaultArn: &stdARN, BackupVaultName: ptrStr("Default")}, {BackupVaultName: ptrStr("arnless")},
	}}
	_, total, _, err := scanBackupAll(context.Background(), stub, newTestAccount(testAccountID), testRegion, st, testScanID)
	if err != nil || total != 1 || *warns != 1 {
		t.Errorf("total = %d, warnings = %d, err = %v; want 1, 1, nil", total, *warns, err)
	}
}

// A plan kept as its list element is warned about only once it is stored: a
// failed write returns the store error and no "stored as" warning.
func TestScanBackup_KeptPlanWarnsOnlyOnceStored(t *testing.T) {
	st, closeStore := openClosingStore(t)
	warns := countOpWarnings(st, "backup:GetBackupPlan")
	h := bkPlanHandlers(map[string]error{"p1": bkDenied}, nil)
	get := h["GetBackupPlan"]
	h["GetBackupPlan"] = func(in any) (any, error) {
		closeStore()
		return get(in)
	}
	acct := newTestAccount(testAccountID)
	acct.cfg = opStubConfig(h, bkDenied)
	_, _, err := scanBackup(context.Background(), acct, testRegion, st, testScanID)
	if !errors.Is(err, store.ErrStoreWrite) {
		t.Errorf("err = %v, want the store write failure", err)
	}
	if *warns != 0 {
		t.Errorf("backup:GetBackupPlan warnings = %d, want 0: the plan was never stored", *warns)
	}
}

// bkVaultPages serves the bkTopLevelHandlers vaults as page one and fails page
// two with err.
func bkVaultPages(h map[string]func(in any) (any, error), err error) {
	first := h["ListBackupVaults"]
	h["ListBackupVaults"] = func(in any) (any, error) {
		if in.(*backup.ListBackupVaultsInput).NextToken != nil {
			return nil, err
		}
		out, ferr := first(in)
		if ferr != nil {
			return nil, ferr
		}
		page := *out.(*backup.ListBackupVaultsOutput)
		page.NextToken = ptrStr("t2")
		return &page, nil
	}
}

// A failed vault listing keeps the vaults of the pages before it and does not
// stop the plans, which do not depend on vaults: a denial warns, any other
// error is returned after the plans are stored.
func TestScanBackup_ListBackupVaultsErrorStillScansPlans(t *testing.T) {
	cases := []struct {
		name      string
		err       error
		wantWarns int
		wantCode  string
	}{
		{"denied", bkDenied, 1, ""},
		{"other error", bkBadInput, 0, "InvalidParameterValueException"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := countOpWarnings(st, "backup:ListBackupVaults")
			h := bkPlanHandlers(nil, nil)
			bkVaultPages(h, tc.err)
			acct := newTestAccount(testAccountID)
			acct.cfg = opStubConfig(h, bkDenied)
			_, _, err := scanBackup(context.Background(), acct, testRegion, st, testScanID)
			if tc.wantCode != "" {
				assertAPICode(t, err, tc.wantCode)
			} else if err != nil {
				t.Fatalf("scanBackup: %v", err)
			}
			if *warns != tc.wantWarns {
				t.Errorf("backup:ListBackupVaults warnings = %d, want %d", *warns, tc.wantWarns)
			}
			if got := nativeIDs(rowsByNativeID(t, st, TypeBackupVault)); !slices.Contains(got, bkVaultARN("std")) {
				t.Errorf("vaults = %v, want page one's %s kept", got, bkVaultARN("std"))
			}
			if n := len(rowsByNativeID(t, st, TypeBackupPlan)); n != 3 {
				t.Errorf("plan rows = %d, want 3: plans must be scanned whatever the vault listing returned", n)
			}
		})
	}
}

// Transient phase errors with no hard error reach the dispatcher as they are,
// so the scan is partial; beside a hard error, in the same phase or another,
// they are demoted to warnings so the hard error is not read as transient.
func TestScanBackup_PhaseErrorJoin(t *testing.T) {
	throttled := apiErr("ThrottlingException", "Rate exceeded")
	vaultsThrottled := func() map[string]func(in any) (any, error) {
		h := bkPlanHandlers(nil, nil)
		h["ListBackupVaults"] = func(any) (any, error) { return nil, throttled }
		return h
	}
	t.Run("lone transient", func(t *testing.T) {
		st := newTestStore(t)
		acct := newTestAccount(testAccountID)
		acct.cfg = opStubConfig(vaultsThrottled(), bkDenied)
		_, _, err := scanBackup(context.Background(), acct, testRegion, st, testScanID)
		if !isTransientNetworkError(err) {
			t.Errorf("err = %v, want the transient ListBackupVaults error", err)
		}
	})
	t.Run("two transients", func(t *testing.T) {
		st := newTestStore(t)
		h := vaultsThrottled()
		h["ListBackupAccessPoints"] = func(any) (any, error) { return nil, throttled }
		acct := newTestAccount(testAccountID)
		acct.cfg = opStubConfig(h, bkDenied)
		_, _, err := scanBackup(context.Background(), acct, testRegion, st, testScanID)
		if !isTransientNetworkError(err) {
			t.Errorf("err = %v, want the transient errors: nothing hard to demote them beside", err)
		}
	})
	// warnOp is the label the demoted transient warns under: its own op inside
	// scanBackupAll, the phase's label "backup" once scanBackup joins phases.
	hard := []struct {
		name, op, warnOp string
	}{
		{"beside hard plans error", "GetBackupPlan", "backup:ListBackupVaults"},
		{"beside hard later phase", "ListBackupAccessPoints", "backup"},
	}
	for _, tc := range hard {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := countOpWarnings(st, tc.warnOp)
			h := vaultsThrottled()
			h[tc.op] = func(any) (any, error) { return nil, bkBadInput }
			acct := newTestAccount(testAccountID)
			acct.cfg = opStubConfig(h, bkDenied)
			_, _, err := scanBackup(context.Background(), acct, testRegion, st, testScanID)
			assertAPICode(t, err, "InvalidParameterValueException")
			if isTransientNetworkError(err) {
				t.Errorf("err = %v reads as transient; the hard error must not be masked", err)
			}
			if *warns != 1 {
				t.Errorf("%s warnings = %d, want 1 for the demoted transient", tc.warnOp, *warns)
			}
		})
	}
}

// A failed closure write leaves the plans and selections stored before it
// counted.
func TestScanBackup_ClosureFailureKeepsCounts(t *testing.T) {
	st := newTestStore(t)
	refuseClosureWrites(t, st)
	acct := newTestAccount(testAccountID)
	acct.cfg = opStubConfig(bkPlanHandlers(nil, nil), bkDenied)
	total, _, err := scanBackup(context.Background(), acct, testRegion, st, testScanID)
	if !errors.Is(err, store.ErrStoreWrite) {
		t.Errorf("err = %v; want the closure's store write failure", err)
	}
	if n := len(rowsByNativeID(t, st, TypeBackupPlan)); n != 3 {
		t.Fatalf("plan rows = %d; want 3 stored", n)
	}
	if n := countStoredRows(t, st); total != n {
		t.Errorf("total = %d; want %d, every row stored", total, n)
	}
}

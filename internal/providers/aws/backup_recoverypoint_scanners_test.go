package aws

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/backup"
	backuptypes "github.com/aws/aws-sdk-go-v2/service/backup/types"
	"github.com/icearp/disco-cli/internal/util"
	"github.com/icearp/disco-cli/store"
)

// A child listing that fails for one parent keeps every other parent's rows:
// a parent it no longer finds is skipped silently; a denial warns once under
// the op; any other error is returned after the rows are stored.
func TestScanBackup_ChildListingFailsForOneParent(t *testing.T) {
	for _, child := range []struct {
		op, parent string
		parentOf   func(in any) string
		lost       int
	}{
		{"ListRecoveryPointsByBackupVault", "vault-bare", func(in any) string {
			return sv(in.(*backup.ListRecoveryPointsByBackupVaultInput).BackupVaultName)
		}, 4},
		{"ListRestoreTestingSelections", "rtp-bare", func(in any) string {
			return sv(in.(*backup.ListRestoreTestingSelectionsInput).RestoreTestingPlanName)
		}, 1},
	} {
		for _, c := range []struct {
			name      string
			err       error
			code      string
			wantWarns int
		}{
			{"not found", bkGone, "", 0},
			{"denied", bkDenied, "", 1},
			{"other error", bkBadInput, "InvalidParameterValueException", 0},
		} {
			t.Run(child.op+"/"+c.name, func(t *testing.T) {
				st := newTestStore(t)
				all := countWarnings(st)
				warns := bacWarnings(st, "backup:"+child.op)
				h := bkHandlers(newBKCalls())
				list := h[child.op]
				h[child.op] = func(in any) (any, error) {
					if child.parentOf(in) == child.parent {
						return nil, c.err
					}
					return list(in)
				}

				total, _, err := bkScan(context.Background(), st, h)
				if c.code != "" {
					assertGlueOutcome(t, err, outcomeError)
					assertAPICode(t, err, c.code)
				} else if err != nil {
					t.Fatalf("err = %v; want nil", err)
				}
				if total != bkTotal-child.lost {
					t.Errorf("total = %d; want %d", total, bkTotal-child.lost)
				}
				if len(*warns) != c.wantWarns || *all != c.wantWarns {
					t.Errorf("backup:%s warnings = %v (all ops %d); want %d", child.op, *warns, *all, c.wantWarns)
				}
			})
		}
	}
}

// Recovery points are listed only for the vaults the vault phase stored this
// scan, not for a vault row an earlier scan left in the store.
func TestScanBackup_RecoveryPointsOnlyOfVaultsListedThisScan(t *testing.T) {
	st := newTestStore(t)
	stale := "stale"
	if _, err := st.UpsertResource(&store.Resource{
		Provider: "aws", AccountID: testAccountID, Type: TypeBackupVault, NativeID: bkVaultARN(stale),
		Name: &stale, Region: sdkaws.String(testRegion), AttributesJSON: "{}", DiscoveredBy: testScanID,
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	calls := newBKCalls()
	if _, _, err := bkScan(context.Background(), st, bkHandlers(calls)); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if got := calls.sortedParents("ListRecoveryPointsByBackupVault"); !slices.Equal(got, []string{"vault-bare", "vault-tagged"}) {
		t.Errorf("ListRecoveryPointsByBackupVault vaults = %v; want [vault-bare vault-tagged]", got)
	}
}

// A legal hold without a Title is named by its ARN; a recovery point without
// a ResourceName likewise (TestScanBackup_StoresTagsOfEveryType's EBS one).
func TestScanBackup_LegalHoldWithoutTitleNamedByARN(t *testing.T) {
	st := newTestStore(t)
	arn := bkARN("legal-hold:untitled")
	h := bkHandlers(newBKCalls())
	h["ListLegalHolds"] = func(any) (any, error) {
		return &backup.ListLegalHoldsOutput{LegalHolds: []backuptypes.LegalHold{{LegalHoldArn: &arn, CreationDate: &bkStart}}}, nil
	}
	if _, _, err := bkScan(context.Background(), st, h); err != nil {
		t.Fatalf("scan: %v", err)
	}
	r, ok := bkRowOf(t, st, TypeBackupLegalHold, arn)
	if !ok || sv(r.Name) != arn || sv(r.CreatedAt) != sv(tp(&bkStart)) {
		t.Errorf("legal hold stored = %v, Name = %q, CreatedAt = %q; want named by its ARN, created %s", ok, sv(r.Name), sv(r.CreatedAt), sv(tp(&bkStart)))
	}
}

// bkStoredWithTags reports whether st holds the recovery point nativeID with
// tags. It does not fail the test, so a stub may call it from the scanner's
// goroutine.
func bkStoredWithTags(st *store.Store, nativeID string) bool {
	rows, err := st.ListResources(store.ResourceFilter{Providers: []string{"aws"}, Types: []string{TypeBackupRecoveryPoint}, Limit: util.AllResources})
	if err != nil {
		return false
	}
	for _, r := range rows {
		if r.NativeID == nativeID {
			return r.TagsJSON != nil
		}
	}
	return false
}

// Each page of recovery points is written, with its tags, before the next
// page is read, so the rows of an earlier page survive a later page's
// failure and ctx's end. A vault deleted after its first page was read warns
// once under the listing's op, as only part of it was stored.
func TestScanBackup_RecoveryPointPagesWrittenAsRead(t *testing.T) {
	isNil := func(err error) bool { return err == nil }
	for _, c := range []struct {
		name      string
		vault     string // whose second page fails; "" for none
		err       error
		isWanted  func(error) bool
		wantWarns int
	}{
		{"every page read", "", nil, isNil, 0},
		{"later page fails", "vault-tagged", bkMissing, func(err error) bool {
			return classifyServiceError(err) == outcomeError && isAPIErrorCode(err, "MissingParameterValueException")
		}, 0},
		{"ctx ends", "vault-tagged", context.Canceled, func(err error) bool { return errors.Is(err, context.Canceled) }, 0},
		{"vault gone after a page", "vault-tagged", bkGone, isNil, 1},
	} {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			all := countWarnings(st)
			warns := bacWarnings(st, "backup:ListRecoveryPointsByBackupVault")
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			h := bkHandlers(newBKCalls())
			list := h["ListRecoveryPointsByBackupVault"]
			var (
				mu          sync.Mutex
				storedFirst = map[string]bool{}
			)
			h["ListRecoveryPointsByBackupVault"] = func(in any) (any, error) {
				req := in.(*backup.ListRecoveryPointsByBackupVaultInput)
				vault := sv(req.BackupVaultName)
				if req.NextToken == nil {
					return list(in)
				}
				mu.Lock()
				storedFirst[vault] = bkStoredWithTags(st, bkRecoveryPointARN(vault, "tagged"))
				mu.Unlock()
				if vault != c.vault {
					return list(in)
				}
				if errors.Is(c.err, context.Canceled) {
					cancel()
				}
				return nil, c.err
			}

			_, _, err := bkScan(ctx, st, h)
			if !c.isWanted(err) {
				t.Fatalf("err = %v; want %s's", err, c.name)
			}
			if len(*warns) != c.wantWarns || *all != c.wantWarns {
				t.Errorf("backup:ListRecoveryPointsByBackupVault warnings = %v (all ops %d); want %d", *warns, *all, c.wantWarns)
			}
			for _, vault := range []string{"vault-tagged", "vault-bare"} {
				if (c.vault == "" || vault == c.vault) && !storedFirst[vault] {
					t.Errorf("%s: first page not stored with tags before the second was read", vault)
				}
			}
			if r, ok := bkRowOf(t, st, TypeBackupRecoveryPoint, bkRecoveryPointARN("vault-tagged", "tagged")); !ok || r.TagsJSON == nil {
				t.Errorf("vault-tagged's first-page recovery point stored = %v, tagged = %v; want both", ok, ok && r.TagsJSON != nil)
			}
		})
	}
}

// A tag read failure on recovery points of two vaults and two pages warns
// once for the phase: a denial, and a Backup ARN ListTags refuses with
// InvalidParameterValueException, whose rows are kept untagged and are no
// scan error. The refusal is tolerated for recovery points only: on a vault
// it stays an error.
func TestScanBackup_RecoveryPointTagFailuresWarnOncePerPhase(t *testing.T) {
	failing := []string{bkRecoveryPointARN("vault-tagged", "tagged"), bkRecoveryPointARN("vault-bare", "bare")}
	// The warning names the first vault's row for a denial (parent order)
	// and the smallest ARN for a refusal (skippedLeast), not the first read
	// to finish.
	for _, c := range []struct {
		name  string
		err   error
		first string
	}{
		{"denied", bkDenied, failing[0]},
		{"refused ARN", bkBadInput, slices.Min(failing)},
	} {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			all := countWarnings(st)
			warns := bacWarnings(st, bkTagsOp)
			calls := newBKCalls()
			for _, arn := range failing {
				calls.errs[arn] = c.err
			}
			total, _, err := bkScan(context.Background(), st, bkHandlers(calls))
			if err != nil {
				t.Fatalf("err = %v; want nil", err)
			}
			if total != bkTotal {
				t.Errorf("total = %d; want %d", total, bkTotal)
			}
			if len(*warns) != 1 || *all != 1 {
				t.Fatalf("%s warnings = %v (all ops %d); want 1", bkTagsOp, *warns, *all)
			}
			if !strings.Contains((*warns)[0].Message, "first "+c.first+":") {
				t.Errorf("warning = %q; want it to name %s first", (*warns)[0].Message, c.first)
			}
			for _, arn := range failing {
				if r, ok := bkRowOf(t, st, TypeBackupRecoveryPoint, arn); !ok || r.TagsJSON != nil {
					t.Errorf("%s stored = %v, TagsJSON = %v; want stored untagged", arn, ok, r.TagsJSON)
				}
			}
		})
	}
	t.Run("refused ARN, write failed", func(t *testing.T) {
		st := newTestStore(t)
		warns := countWarnings(st)
		refuseStoreWrites(t, st, TypeBackupRecoveryPoint)
		calls := newBKCalls()
		// Both vaults' first-page recovery points are refused: whichever
		// vault is read first has its refusal tallied before writes fail.
		calls.errs[bkRecoveryPointARN("vault-tagged", "tagged")] = bkBadInput
		calls.errs[bkRecoveryPointARN("vault-bare", "tagged")] = bkBadInput
		_, _, err := bkScan(context.Background(), st, bkHandlers(calls))
		if !errors.Is(err, store.ErrStoreWrite) {
			t.Fatalf("err = %v; want store.ErrStoreWrite", err)
		}
		if *warns != 0 {
			t.Errorf("warnings = %d; want 0 after the failed write", *warns)
		}
		// The vaults are read concurrently, so each may read its first page
		// before the other's write fails; no second page follows a failed
		// write (TestStoreEachParentPagesTags_FailedWriteStops pins the stop).
		if n := calls.count("ListRecoveryPointsByBackupVault"); n > 2 {
			t.Errorf("ListRecoveryPointsByBackupVault called %d times; want at most 2, one first page per vault", n)
		}
	})
	t.Run("refused vault ARN", func(t *testing.T) {
		calls := newBKCalls()
		calls.errs[bkVaultARN("vault-tagged")] = bkBadInput
		_, _, err := bkScan(context.Background(), newTestStore(t), bkHandlers(calls))
		assertGlueOutcome(t, err, outcomeError)
		assertAPICode(t, err, "InvalidParameterValueException")
	})
}

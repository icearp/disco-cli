package aws

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"maps"
	"net"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/backup"
	backuptypes "github.com/aws/aws-sdk-go-v2/service/backup/types"
	"github.com/icearp/disco-cli/store"
)

var (
	bkDenied   = apiErr("AccessDeniedException", "User: arn:aws:iam::123456789012:user/t is not authorized to perform: backup:List")
	bkBadInput = apiErr("InvalidParameterValueException", "bad")
	bkMissing  = apiErr("MissingParameterValueException", "missing")
	bkGone     = apiErr("ResourceNotFoundException", "not found")
	bkThrottle = apiErr("ThrottlingException", "Rate exceeded")
	bkStart    = time.Date(2026, 8, 10, 9, 0, 0, 0, time.UTC)
)

const (
	bkTagsOp = "backup:ListTags"
	// bkTotal is every row the full fixture stores: two of each account-wide
	// listing's type (eight listings), one selection per plan and per restore
	// testing plan, four recovery points per vault and one restore access
	// vault under the air-gapped vault.
	bkTotal = 29
	// bkOtherAccount owns the restore access vaults and the shared rows.
	bkOtherAccount = "999999999999"
)

func bkARNIn(acctID, resource string) string {
	return "arn:aws:backup:" + testRegion + ":" + acctID + ":" + resource
}

func bkARN(resource string) string { return bkARNIn(testAccountID, resource) }

// bkKind is one account-wide listing of the fixture: the op, the ARN resource
// prefix and name label of its resources, the type its "tagged" and "bare"
// resources are stored as, and page, which builds the op's output from an
// element's ARN, name and the next token.
type bkKind struct {
	op, resource, label string
	typ, bareTyp        string
	page                func(arn, name, next *string) any
}

func bkKinds() []bkKind {
	return []bkKind{
		{"ListBackupVaults", "backup-vault:", "vault", TypeBackupVault, TypeBackupLogicallyAirGappedVault, func(arn, name, next *string) any {
			v := backuptypes.BackupVaultListMember{BackupVaultArn: arn, BackupVaultName: name, CreationDate: &bkStart}
			if strings.HasSuffix(*name, "-bare") {
				v.VaultType = backuptypes.VaultTypeLogicallyAirGappedBackupVault
			}
			return &backup.ListBackupVaultsOutput{BackupVaultList: []backuptypes.BackupVaultListMember{v}, NextToken: next}
		}},
		{"ListBackupPlans", "backup-plan:", "plan", TypeBackupPlan, TypeBackupPlan, func(arn, name, next *string) any {
			return &backup.ListBackupPlansOutput{BackupPlansList: []backuptypes.BackupPlansListMember{{BackupPlanArn: arn, BackupPlanId: name, BackupPlanName: name, CreationDate: &bkStart}}, NextToken: next}
		}},
		{"ListFrameworks", "framework:", "fw", TypeBackupFramework, TypeBackupFramework, func(arn, name, next *string) any {
			return &backup.ListFrameworksOutput{Frameworks: []backuptypes.Framework{{FrameworkArn: arn, FrameworkName: name}}, NextToken: next}
		}},
		{"ListReportPlans", "report-plan:", "report", TypeBackupReportPlan, TypeBackupReportPlan, func(arn, name, next *string) any {
			return &backup.ListReportPlansOutput{ReportPlans: []backuptypes.ReportPlan{{ReportPlanArn: arn, ReportPlanName: name}}, NextToken: next}
		}},
		{"ListRestoreTestingPlans", "restore-testing-plan:", "rtp", TypeBackupRestoreTestingPlan, TypeBackupRestoreTestingPlan, func(arn, name, next *string) any {
			return &backup.ListRestoreTestingPlansOutput{RestoreTestingPlans: []backuptypes.RestoreTestingPlanForList{{RestoreTestingPlanArn: arn, RestoreTestingPlanName: name}}, NextToken: next}
		}},
		{"ListTieringConfigurations", "tiering-configuration:", "tier", TypeBackupTieringConfiguration, TypeBackupTieringConfiguration, func(arn, name, next *string) any {
			return &backup.ListTieringConfigurationsOutput{TieringConfigurations: []backuptypes.TieringConfigurationsListMember{{TieringConfigurationArn: arn, TieringConfigurationName: name}}, NextToken: next}
		}},
		{"ListLegalHolds", "legal-hold:", "hold", TypeBackupLegalHold, TypeBackupLegalHold, func(arn, name, next *string) any {
			return &backup.ListLegalHoldsOutput{LegalHolds: []backuptypes.LegalHold{{LegalHoldArn: arn, Title: name, CreationDate: &bkStart}}, NextToken: next}
		}},
		{"ListBackupAccessPoints", "accesspoint/", "ap", TypeBackupAccessPoint, TypeBackupAccessPoint, func(arn, name, next *string) any {
			return &backup.ListBackupAccessPointsOutput{BackupAccessPoints: []backuptypes.ListAccessPointsMember{{
				AccessPointArn: arn, Name: name, Status: backuptypes.AccessPointStatusAvailable, CreationTime: &bkStart,
				RecoveryPointArn: sdkaws.String(bkARN("recovery-point:of-" + *name)),
			}}, NextToken: next}
		}},
	}
}

func bkKindOf(t *testing.T, op string) bkKind {
	t.Helper()
	for _, k := range bkKinds() {
		if k.op == op {
			return k
		}
	}
	t.Fatalf("no fixture kind for %s", op)
	return bkKind{}
}

// bkName is the listed name of kind k's resource suffix, "tagged" or "bare".
func bkName(k bkKind, suffix string) string { return k.label + "-" + suffix }

func bkKindARN(k bkKind, suffix string) string { return bkARN(k.resource + bkName(k, suffix)) }

func bkVaultARN(name string) string { return bkARN("backup-vault:" + name) }

func bkPlanARN(id string) string { return bkARN("backup-plan:" + id) }

func bkSelectionARN(planID string) string { return bkPlanARN(planID) + "/selection/sel-" + planID }

func bkTestingSelectionARN(plan string) string {
	return bkARN("restore-testing-plan:" + plan + "/selection/rts-" + plan)
}

// bkRecoveryPointARN is the Backup ARN of vault's recovery point suffix.
func bkRecoveryPointARN(vault, suffix string) string {
	return bkARN("recovery-point:" + vault + "-" + suffix)
}

// bkGovRecoveryPointARN is a Backup ARN of vault's recovery point in another
// partition, so that a check for a commercial "arn:aws:backup" prefix fails.
func bkGovRecoveryPointARN(vault string) string {
	return "arn:aws-us-gov:backup:us-gov-west-1:" + testAccountID + ":recovery-point:" + vault + "-gov"
}

// bkSnapshotRecoveryPointARN is the ARN of vault's recovery point of an EBS
// volume: an EC2 snapshot ARN, which has no account.
func bkSnapshotRecoveryPointARN(vault string) string {
	return "arn:aws:ec2:" + testRegion + "::snapshot/snap-" + vault
}

func bkRestoreVaultARN(acctID, name string) string {
	return bkARNIn(acctID, "backup-vault:"+name)
}

func bkRestoreNativeID(parent, restoreVaultARN string) string {
	return bkVaultARN(parent) + "/restore-access-backup-vault/" + restoreVaultARN
}

// bkCalls records the stubbed calls: how often each op ran, the MaxResults
// each was sent (unset when none), the parent each child listing was sent,
// and the ARN of each first-page ListTags call. It answers ListTags from tags
// or errs, both keyed by ARN, one tag per page so that every tagged row's
// read pages. Reads run concurrently.
type bkCalls struct {
	mu         sync.Mutex
	ops        map[string]int
	maxResults map[string][]int32
	unset      map[string]int
	parents    map[string][]string
	tagARNs    []string
	tags       map[string]map[string]string
	errs       map[string]error
}

// bkTags is the tags the fixture gives a "tagged" resource of type typ: two,
// so that its ListTags read takes two pages.
func bkTags(typ string) map[string]string { return map[string]string{"kind": typ, "page": "two"} }

func newBKCalls() *bkCalls {
	c := &bkCalls{
		ops: map[string]int{}, maxResults: map[string][]int32{}, unset: map[string]int{}, parents: map[string][]string{},
		tags: map[string]map[string]string{}, errs: map[string]error{},
	}
	for _, w := range bkWants() {
		if w.tags != nil {
			c.tags[w.arn] = w.tags
		}
	}
	return c
}

func (c *bkCalls) record(op string, in any) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.ops[op]++
	if n := bacField[*int32](in, "MaxResults"); n != nil {
		c.maxResults[op] = append(c.maxResults[op], *n)
	} else {
		c.unset[op]++
	}
}

func (c *bkCalls) recordParent(op, parent string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.parents[op] = append(c.parents[op], parent)
}

func (c *bkCalls) count(op string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.ops[op]
}

// sortedParents is the distinct parents op was sent, sorted.
func (c *bkCalls) sortedParents(op string) []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	got := slices.Clone(c.parents[op])
	slices.Sort(got)
	return slices.Compact(got)
}

func (c *bkCalls) listTags(in any) (any, error) {
	req := in.(*backup.ListTagsInput)
	arn := sv(req.ResourceArn)
	c.record("ListTags", in)
	c.mu.Lock()
	if req.NextToken == nil {
		c.tagARNs = append(c.tagARNs, arn)
	}
	err, tags := c.errs[arn], c.tags[arn]
	c.mu.Unlock()
	if err != nil {
		return nil, err
	}
	keys := slices.Sorted(maps.Keys(tags))
	if len(keys) == 0 {
		return &backup.ListTagsOutput{}, nil
	}
	i := 0
	if req.NextToken != nil {
		i, _ = strconv.Atoi(*req.NextToken)
	}
	out := &backup.ListTagsOutput{Tags: map[string]string{keys[i]: tags[keys[i]]}}
	if i+1 < len(keys) {
		out.NextToken = sdkaws.String(strconv.Itoa(i + 1))
	}
	return out, nil
}

// twoPages answers k's op with "tagged" on the first page and "bare" on the
// second.
func (c *bkCalls) twoPages(k bkKind) func(any) (any, error) {
	return func(in any) (any, error) {
		c.record(k.op, in)
		suffix, next := "tagged", sdkaws.String("t2")
		if bacField[*string](in, "NextToken") != nil {
			suffix, next = "bare", nil
		}
		arn, name := bkKindARN(k, suffix), bkName(k, suffix)
		return k.page(&arn, &name, next), nil
	}
}

func bkHandlers(c *bkCalls) map[string]func(any) (any, error) {
	h := map[string]func(any) (any, error){
		"ListTags": c.listTags,
		"GetBackupPlan": func(in any) (any, error) {
			c.record("GetBackupPlan", in)
			id := sv(in.(*backup.GetBackupPlanInput).BackupPlanId)
			c.recordParent("GetBackupPlan", id)
			return &backup.GetBackupPlanOutput{BackupPlanId: &id, BackupPlanArn: sdkaws.String(bkPlanARN(id)), BackupPlan: &backuptypes.BackupPlan{BackupPlanName: &id}}, nil
		},
		"ListBackupSelections": func(in any) (any, error) {
			c.record("ListBackupSelections", in)
			id := sv(in.(*backup.ListBackupSelectionsInput).BackupPlanId)
			c.recordParent("ListBackupSelections", id)
			return &backup.ListBackupSelectionsOutput{BackupSelectionsList: []backuptypes.BackupSelectionsListMember{
				{SelectionId: sdkaws.String("sel-" + id), SelectionName: sdkaws.String("sel-name-" + id), BackupPlanId: &id, CreationDate: &bkStart},
			}}, nil
		},
		"ListRestoreTestingSelections": func(in any) (any, error) {
			c.record("ListRestoreTestingSelections", in)
			plan := sv(in.(*backup.ListRestoreTestingSelectionsInput).RestoreTestingPlanName)
			c.recordParent("ListRestoreTestingSelections", plan)
			return &backup.ListRestoreTestingSelectionsOutput{RestoreTestingSelections: []backuptypes.RestoreTestingSelectionForList{
				{RestoreTestingSelectionName: sdkaws.String("rts-" + plan), RestoreTestingPlanName: &plan},
			}}, nil
		},
		"ListRecoveryPointsByBackupVault": func(in any) (any, error) {
			c.record("ListRecoveryPointsByBackupVault", in)
			req := in.(*backup.ListRecoveryPointsByBackupVaultInput)
			vault := sv(req.BackupVaultName)
			c.recordParent("ListRecoveryPointsByBackupVault", vault)
			if req.NextToken == nil {
				return &backup.ListRecoveryPointsByBackupVaultOutput{RecoveryPoints: []backuptypes.RecoveryPointByBackupVault{
					{RecoveryPointArn: sdkaws.String(bkRecoveryPointARN(vault, "tagged")), ResourceName: sdkaws.String("res-" + vault + "-tagged"), CreationDate: &bkStart},
				}, NextToken: sdkaws.String("t2")}, nil
			}
			return &backup.ListRecoveryPointsByBackupVaultOutput{RecoveryPoints: []backuptypes.RecoveryPointByBackupVault{
				{RecoveryPointArn: sdkaws.String(bkRecoveryPointARN(vault, "bare")), ResourceName: sdkaws.String("res-" + vault + "-bare")},
				{RecoveryPointArn: sdkaws.String(bkGovRecoveryPointARN(vault)), ResourceName: sdkaws.String("res-" + vault + "-gov")},
				{RecoveryPointArn: sdkaws.String(bkSnapshotRecoveryPointARN(vault)), ResourceType: sdkaws.String("EBS")},
			}}, nil
		},
		"ListRestoreAccessBackupVaults": func(in any) (any, error) {
			c.record("ListRestoreAccessBackupVaults", in)
			vault := sv(in.(*backup.ListRestoreAccessBackupVaultsInput).BackupVaultName)
			c.recordParent("ListRestoreAccessBackupVaults", vault)
			return &backup.ListRestoreAccessBackupVaultsOutput{RestoreAccessBackupVaults: []backuptypes.RestoreAccessBackupVaultListMember{
				{RestoreAccessBackupVaultArn: sdkaws.String(bkRestoreVaultARN(bkOtherAccount, "ra-"+vault)), VaultState: backuptypes.VaultStateAvailable, CreationDate: &bkStart},
			}}, nil
		},
	}
	for _, k := range bkKinds() {
		h[k.op] = c.twoPages(k)
	}
	return h
}

// bkScan runs the scan's real entry point against handlers; every op they do
// not answer is denied.
func bkScan(ctx context.Context, st *store.Store, handlers map[string]func(any) (any, error)) (int, int, error) {
	acct := newTestAccount(testAccountID)
	acct.cfg = opStubConfig(handlers, bkDenied)
	return scanBackup(ctx, acct, testRegion, st, testScanID)
}

func bkRowOf(t *testing.T, st *store.Store, typ, nativeID string) (store.Resource, bool) {
	t.Helper()
	r, ok := rowsByNativeID(t, st, typ)[nativeID]
	return r, ok
}

// bkWant is one row the full fixture stores: its type, NativeID, Name, tags
// (nil for none), whether its NativeID is sent to ListTags, and whether its
// element carries a creation time (bkStart), stored as CreatedAt.
type bkWant struct {
	typ, arn, name string
	tags           map[string]string
	read, created  bool
}

func bkWants() []bkWant {
	var want []bkWant
	for _, k := range bkKinds() {
		for _, suffix := range []string{"tagged", "bare"} {
			typ := k.typ
			if suffix == "bare" {
				typ = k.bareTyp
			}
			w := bkWant{
				typ: typ, arn: bkKindARN(k, suffix), name: bkName(k, suffix), read: true,
				created: slices.Contains([]string{"ListBackupVaults", "ListBackupPlans", "ListLegalHolds", "ListBackupAccessPoints"}, k.op),
			}
			if suffix == "tagged" {
				w.tags = bkTags(typ)
			}
			want = append(want, w)
		}
	}
	for _, suffix := range []string{"tagged", "bare"} {
		plan, rtp, vault := "plan-"+suffix, "rtp-"+suffix, "vault-"+suffix
		want = append(want,
			bkWant{typ: TypeBackupSelection, arn: bkSelectionARN(plan), name: "sel-name-" + plan, created: true},
			bkWant{typ: TypeBackupRestoreTestingSelection, arn: bkTestingSelectionARN(rtp), name: "rts-" + rtp},
			bkWant{typ: TypeBackupRecoveryPoint, arn: bkRecoveryPointARN(vault, "tagged"), name: "res-" + vault + "-tagged", tags: bkTags(TypeBackupRecoveryPoint), read: true, created: true},
			bkWant{typ: TypeBackupRecoveryPoint, arn: bkRecoveryPointARN(vault, "bare"), name: "res-" + vault + "-bare", read: true},
			bkWant{typ: TypeBackupRecoveryPoint, arn: bkGovRecoveryPointARN(vault), name: "res-" + vault + "-gov", read: true},
			bkWant{typ: TypeBackupRecoveryPoint, arn: bkSnapshotRecoveryPointARN(vault), name: bkSnapshotRecoveryPointARN(vault)},
		)
	}
	ra := bkRestoreVaultARN(bkOtherAccount, "ra-vault-bare")
	return append(want, bkWant{typ: TypeBackupRestoreAccessBackupVault, arn: bkRestoreNativeID("vault-bare", ra), name: ra, created: true})
}

// Every type stores both pages of its listing under its ARN, named as listed,
// with the tags ListTags returns for its ARN over every page, and nil for a
// resource without tags. Exactly the ARN of every row of a taggable type is
// sent to ListTags, except a recovery point whose ARN is not a Backup ARN.
// Every listing and ListTags is sent MaxResults 1000, except
// ListBackupAccessPoints, sent 100. No error or warning.
func TestScanBackup_StoresTagsOfEveryType(t *testing.T) {
	st := newTestStore(t)
	warns := countWarnings(st)
	calls := newBKCalls()

	total, inserted, err := bkScan(context.Background(), st, bkHandlers(calls))
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != bkTotal || inserted != bkTotal {
		t.Errorf("total, inserted = %d, %d; want %d, %d", total, inserted, bkTotal, bkTotal)
	}
	if *warns != 0 {
		t.Errorf("warnings = %d; want 0", *warns)
	}
	if n := countStoredRows(t, st); n != bkTotal {
		t.Errorf("stored rows = %d; want %d", n, bkTotal)
	}
	var wantARNs []string
	for _, w := range bkWants() {
		if w.read {
			wantARNs = append(wantARNs, w.arn)
		}
		r, ok := bkRowOf(t, st, w.typ, w.arn)
		if !ok {
			t.Errorf("%s %s missing", w.typ, w.arn)
			continue
		}
		if got := sv(r.Name); got != w.name {
			t.Errorf("%s %s Name = %q; want %q", w.typ, w.arn, got, w.name)
		}
		if sv(r.Region) != testRegion {
			t.Errorf("%s %s Region = %q; want %s", w.typ, w.arn, sv(r.Region), testRegion)
		}
		if got, want := r.CreatedAt != nil, w.created; got != want || (got && sv(r.CreatedAt) != sv(tp(&bkStart))) {
			t.Errorf("%s %s CreatedAt = %q; want set to %s: %v", w.typ, w.arn, sv(r.CreatedAt), sv(tp(&bkStart)), want)
		}
		if w.tags != nil {
			assertTagsMap(t, r.TagsJSON, w.tags)
		} else if r.TagsJSON != nil {
			t.Errorf("%s %s TagsJSON = %q; want nil", w.typ, w.arn, *r.TagsJSON)
		}
	}
	slices.Sort(wantARNs)
	slices.Sort(calls.tagARNs)
	if !slices.Equal(calls.tagARNs, wantARNs) {
		t.Errorf("ListTags ARNs = %v\nwant %v", calls.tagARNs, wantARNs)
	}
	for op, sent := range calls.maxResults {
		want := int32(1000)
		if op == "ListBackupAccessPoints" {
			want = 100
		}
		for _, n := range sent {
			if n != want {
				t.Errorf("%s MaxResults = %d; want %d", op, n, want)
			}
		}
	}
	for op, n := range calls.unset {
		if op != "GetBackupPlan" {
			t.Errorf("%s sent no MaxResults on %d calls", op, n)
		}
	}
}

// The air-gapped vault is stored as its own type; every vault stored is a
// parent of the recovery-point listing, only the air-gapped one of the
// restore-access-vault listing; each plan is a parent of the selection
// listing, by id, and each restore testing plan of its selection listing, by
// name. Selections are linked under their plans and restore access vaults
// under their air-gapped vault.
func TestScanBackup_ChildListingsGetTheParentsStored(t *testing.T) {
	st := newTestStore(t)
	calls := newBKCalls()
	if _, _, err := bkScan(context.Background(), st, bkHandlers(calls)); err != nil {
		t.Fatalf("scan: %v", err)
	}
	for _, c := range []struct {
		op   string
		want []string
	}{
		{"ListRecoveryPointsByBackupVault", []string{"vault-bare", "vault-tagged"}},
		{"ListRestoreAccessBackupVaults", []string{"vault-bare"}},
		{"ListBackupSelections", []string{"plan-bare", "plan-tagged"}},
		{"GetBackupPlan", []string{"plan-bare", "plan-tagged"}},
		{"ListRestoreTestingSelections", []string{"rtp-bare", "rtp-tagged"}},
	} {
		if got := calls.sortedParents(c.op); !slices.Equal(got, c.want) {
			t.Errorf("%s parents = %v; want %v", c.op, got, c.want)
		}
	}
	if _, ok := bkRowOf(t, st, TypeBackupLogicallyAirGappedVault, bkVaultARN("vault-bare")); !ok {
		t.Errorf("air-gapped vault not stored as %s", TypeBackupLogicallyAirGappedVault)
	}
	for _, plan := range []string{"plan-tagged", "plan-bare"} {
		assertContainsNative(t, st, bkPlanARN(plan), bkSelectionARN(plan))
	}
	assertContainsNative(t, st, bkVaultARN("vault-bare"), bkRestoreNativeID("vault-bare", bkRestoreVaultARN(bkOtherAccount, "ra-vault-bare")))
	assertContainsEdgesDeclared(t, st)
}

// A rescan stores the same rows without inserting any.
func TestScanBackup_RescanInsertsNone(t *testing.T) {
	st := newTestStore(t)
	calls := newBKCalls()
	if _, _, err := bkScan(context.Background(), st, bkHandlers(calls)); err != nil {
		t.Fatalf("first scan: %v", err)
	}
	total, inserted, err := bkScan(context.Background(), st, bkHandlers(calls))
	if err != nil {
		t.Fatalf("rescan: %v", err)
	}
	if total != bkTotal || inserted != 0 {
		t.Errorf("rescan total, inserted = %d, %d; want %d, 0", total, inserted, bkTotal)
	}
}

// A failed tag read: ResourceNotFoundException, the op's modeled not-found,
// drops the row silently, and a dropped parent's children are not listed; a
// denial keeps the row untagged and warns once under backup:ListTags; a
// throttle after retries keeps it and is a transient error; any other error
// keeps it and is a hard error. Each holds for a parent listing's row (a
// vault), a row also read with GetBackupPlan (a plan) and a child listing's
// row (a recovery point).
func TestScanBackup_TagReadFailures(t *testing.T) {
	for _, target := range []struct {
		typ, arn string
		goneLost int // rows lost when the target is not found: itself and its children
	}{
		{TypeBackupVault, bkVaultARN("vault-tagged"), 5},
		{TypeBackupPlan, bkPlanARN("plan-tagged"), 2},
		{TypeBackupRecoveryPoint, bkRecoveryPointARN("vault-bare", "tagged"), 1},
	} {
		t.Run(target.typ, func(t *testing.T) {
			for _, c := range []struct {
				name      string
				err       error
				outcome   serviceOutcome
				code      string
				kept      bool
				wantWarns int
			}{
				{"not found", bkGone, 0, "", false, 0},
				{"denied", bkDenied, 0, "", true, 1},
				{"transient", bkThrottle, outcomeTransient, "", true, 0},
				{"other error", bkMissing, outcomeError, "MissingParameterValueException", true, 0},
			} {
				t.Run(c.name, func(t *testing.T) {
					st := newTestStore(t)
					all := countWarnings(st)
					warnings := bacWarnings(st, bkTagsOp)
					calls := newBKCalls()
					calls.errs[target.arn] = c.err

					total, _, err := bkScan(context.Background(), st, bkHandlers(calls))
					if c.outcome == 0 && err != nil {
						t.Fatalf("err = %v; want nil", err)
					}
					if c.outcome != 0 {
						assertGlueOutcome(t, err, c.outcome)
					}
					if c.code != "" {
						assertAPICode(t, err, c.code)
					}
					wantTotal := bkTotal
					if !c.kept {
						wantTotal -= target.goneLost
					}
					if total != wantTotal {
						t.Errorf("total = %d; want %d", total, wantTotal)
					}
					if n := countStoredRows(t, st); n != wantTotal {
						t.Errorf("stored rows = %d; want %d", n, wantTotal)
					}
					r, ok := bkRowOf(t, st, target.typ, target.arn)
					if ok != c.kept {
						t.Fatalf("row stored = %v; want %v", ok, c.kept)
					}
					if ok && r.TagsJSON != nil {
						t.Errorf("TagsJSON = %q; want nil", *r.TagsJSON)
					}
					if len(*warnings) != c.wantWarns || *all != c.wantWarns {
						t.Fatalf("%s warnings = %v (all ops %d); want %d", bkTagsOp, *warnings, *all, c.wantWarns)
					}
					for _, w := range *warnings {
						if w.Scope != testAccountID+"/"+testRegion {
							t.Errorf("warning Scope = %q; want %s/%s", w.Scope, testAccountID, testRegion)
						}
					}
				})
			}
		})
	}
}

// Two denied tag reads in one phase send one warning.
func TestScanBackup_TagDenialsWarnOnce(t *testing.T) {
	st := newTestStore(t)
	warnings := bacWarnings(st, bkTagsOp)
	calls := newBKCalls()
	k := bkKindOf(t, "ListLegalHolds")
	calls.errs[bkKindARN(k, "tagged")] = bkDenied
	calls.errs[bkKindARN(k, "bare")] = bkDenied

	if _, _, err := bkScan(context.Background(), st, bkHandlers(calls)); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if len(*warnings) != 1 {
		t.Errorf("%s warnings = %d; want 1", bkTagsOp, len(*warnings))
	}
	if got := len(rowsByNativeID(t, st, k.typ)); got != 2 {
		t.Errorf("legal holds = %d; want 2", got)
	}
}

// A row whose ARN names another account (a vault shared in, a recovery point
// in it) is kept untagged and silent when ListTags denies it or does not find
// it; this account's rows keep the usual rules (TestScanBackup_TagReadFailures).
func TestScanBackup_OtherAccountRowKeptOnTagGap(t *testing.T) {
	shared := bkRestoreVaultARN(bkOtherAccount, "shared")
	sharedRP := bkARNIn(bkOtherAccount, "recovery-point:shared-1")
	for _, target := range []struct{ typ, arn string }{
		{TypeBackupVault, shared},
		{TypeBackupRecoveryPoint, sharedRP},
	} {
		for _, c := range []struct {
			name string
			err  error
		}{{"denied", bkDenied}, {"not found", bkGone}} {
			t.Run(target.typ+"/"+c.name, func(t *testing.T) {
				st := newTestStore(t)
				warns := countWarnings(st)
				calls := newBKCalls()
				calls.errs[target.arn] = c.err
				h := bkHandlers(calls)
				vaults := h["ListBackupVaults"]
				h["ListBackupVaults"] = func(in any) (any, error) {
					if bacField[*string](in, "NextToken") != nil {
						return vaults(in)
					}
					return &backup.ListBackupVaultsOutput{BackupVaultList: []backuptypes.BackupVaultListMember{
						{BackupVaultArn: sdkaws.String(shared), BackupVaultName: sdkaws.String("shared")},
					}, NextToken: sdkaws.String("t2")}, nil
				}
				points := h["ListRecoveryPointsByBackupVault"]
				h["ListRecoveryPointsByBackupVault"] = func(in any) (any, error) {
					if sv(in.(*backup.ListRecoveryPointsByBackupVaultInput).BackupVaultName) != "shared" {
						return points(in)
					}
					return &backup.ListRecoveryPointsByBackupVaultOutput{RecoveryPoints: []backuptypes.RecoveryPointByBackupVault{
						{RecoveryPointArn: sdkaws.String(sharedRP)},
					}}, nil
				}

				if _, _, err := bkScan(context.Background(), st, h); err != nil {
					t.Fatalf("scan: %v", err)
				}
				r, ok := bkRowOf(t, st, target.typ, target.arn)
				if !ok {
					t.Fatalf("%s %s not stored", target.typ, target.arn)
				}
				if r.TagsJSON != nil {
					t.Errorf("TagsJSON = %q; want nil", *r.TagsJSON)
				}
				if *warns != 0 {
					t.Errorf("warnings = %d; want 0", *warns)
				}
			})
		}
	}
}

// A ListTags read whose next token repeats keeps the row untagged and fails
// the scan with errRepeatedNextToken.
func TestScanBackup_TagReadRepeatedToken(t *testing.T) {
	st := newTestStore(t)
	calls := newBKCalls()
	h := bkHandlers(calls)
	target := bkKindARN(bkKindOf(t, "ListReportPlans"), "tagged")
	h["ListTags"] = func(in any) (any, error) {
		if sv(in.(*backup.ListTagsInput).ResourceArn) == target {
			return &backup.ListTagsOutput{Tags: map[string]string{"k": "v"}, NextToken: sdkaws.String("same")}, nil
		}
		return calls.listTags(in)
	}

	total, _, err := bkScan(context.Background(), st, h)
	if !errors.Is(err, errRepeatedNextToken) {
		t.Fatalf("err = %v; want errRepeatedNextToken", err)
	}
	if total != bkTotal {
		t.Errorf("total = %d; want %d", total, bkTotal)
	}
	if r, ok := bkRowOf(t, st, TypeBackupReportPlan, target); !ok || r.TagsJSON != nil {
		t.Errorf("report plan stored = %v, TagsJSON = %v; want stored untagged", ok, r.TagsJSON)
	}
}

// A throttled tag read beside a hard failure of the same op is demoted to one
// warning, so the hard error decides the outcome.
func TestScanBackup_TagThrottleDemotedBesideHardError(t *testing.T) {
	st := newTestStore(t)
	warns := countOpWarnings(st, bkTagsOp)
	calls := newBKCalls()
	calls.errs[bkKindARN(bkKindOf(t, "ListFrameworks"), "tagged")] = bkThrottle
	calls.errs[bkKindARN(bkKindOf(t, "ListTieringConfigurations"), "bare")] = bkBadInput

	_, _, err := bkScan(context.Background(), st, bkHandlers(calls))
	assertGlueOutcome(t, err, outcomeError)
	assertAPICode(t, err, "InvalidParameterValueException")
	if *warns != 1 {
		t.Errorf("%s warnings = %d; want 1", bkTagsOp, *warns)
	}
}

// A failed write stops the scan before any later listing and sends no
// warning about the rows it lost.
func TestScanBackup_FailedWriteStopsWithoutWarning(t *testing.T) {
	st := newTestStore(t)
	warns := countWarnings(st)
	refuseStoreWrites(t, st, TypeBackupVault)
	calls := newBKCalls()
	calls.errs[bkVaultARN("vault-tagged")] = bkDenied

	_, _, err := bkScan(context.Background(), st, bkHandlers(calls))
	if !errors.Is(err, store.ErrStoreWrite) {
		t.Fatalf("err = %v; want store.ErrStoreWrite", err)
	}
	if *warns != 0 {
		t.Errorf("warnings = %d; want 0", *warns)
	}
	for _, op := range []string{"ListBackupPlans", "ListRecoveryPointsByBackupVault", "ListBackupAccessPoints"} {
		if n := calls.count(op); n != 0 {
			t.Errorf("%s called %d times after the failed write", op, n)
		}
	}
}

// A plan kept as its list element is warned about only once it is stored: a
// failed write returns the store error and no "stored as" warning.
func TestScanBackup_KeptPlanWarnsOnlyOnceStored(t *testing.T) {
	st, closeStore := openClosingStore(t)
	warns := countOpWarnings(st, "backup:GetBackupPlan")
	h := bkHandlers(newBKCalls())
	h["GetBackupPlan"] = func(any) (any, error) {
		closeStore()
		return nil, bkDenied
	}
	_, _, err := bkScan(context.Background(), st, h)
	if !errors.Is(err, store.ErrStoreWrite) {
		t.Errorf("err = %v, want the store write failure", err)
	}
	if *warns != 0 {
		t.Errorf("backup:GetBackupPlan warnings = %d, want 0: the plan was never stored", *warns)
	}
}

// A listing's failed page keeps the pages read before it, and its error does
// not stop the listings after it.
func TestScanBackup_ListFailureKeepsPagesAndRunsEveryPhase(t *testing.T) {
	for _, op := range []string{"ListBackupVaults", "ListFrameworks"} {
		t.Run(op, func(t *testing.T) {
			st := newTestStore(t)
			handlers := bkHandlers(newBKCalls())
			handlers[op] = failOnCall(2, bkBadInput, handlers[op])

			_, _, err := bkScan(context.Background(), st, handlers)
			assertGlueOutcome(t, err, outcomeError)
			assertAPICode(t, err, "InvalidParameterValueException")
			k := bkKindOf(t, op)
			if _, ok := bkRowOf(t, st, k.typ, bkKindARN(k, "tagged")); !ok {
				t.Errorf("%s row from the page before the failure missing", k.typ)
			}
			if got := len(rowsByNativeID(t, st, TypeBackupRestoreAccessBackupVault)); op == "ListFrameworks" && got != 1 {
				t.Errorf("restore access vaults (last phase) = %d; want 1", got)
			}
			if got := len(rowsByNativeID(t, st, TypeBackupAccessPoint)); got != 2 {
				t.Errorf("access points (a later phase) = %d; want 2", got)
			}
		})
	}
}

// A repeated token ends the listing as an error, keeping its pages.
func TestScanBackup_RepeatedTokenKeepsPages(t *testing.T) {
	st := newTestStore(t)
	handlers := bkHandlers(newBKCalls())
	k := bkKindOf(t, "ListReportPlans")
	handlers[k.op] = func(in any) (any, error) {
		suffix := "tagged"
		if bacField[*string](in, "NextToken") != nil {
			suffix = "bare"
		}
		arn, name := bkKindARN(k, suffix), bkName(k, suffix)
		return k.page(&arn, &name, sdkaws.String("same")), nil
	}

	total, _, err := bkScan(context.Background(), st, handlers)
	if !errors.Is(err, errRepeatedNextToken) {
		t.Fatalf("err = %v; want errRepeatedNextToken", err)
	}
	if total != bkTotal {
		t.Errorf("total = %d; want %d", total, bkTotal)
	}
	for _, suffix := range []string{"tagged", "bare"} {
		if _, ok := bkRowOf(t, st, k.typ, bkKindARN(k, suffix)); !ok {
			t.Errorf("report plan %s from the pages read missing", suffix)
		}
	}
}

// An element without an ARN is not stored and is warned about once under its
// listing, keyed by its name, or by a fixed text when it has none.
func TestScanBackup_ElementWithoutARNWarns(t *testing.T) {
	cases := []struct {
		op   string
		out  any
		key  string
		lost int
	}{
		{"ListBackupVaults", &backup.ListBackupVaultsOutput{BackupVaultList: []backuptypes.BackupVaultListMember{{}, {BackupVaultName: sdkaws.String("named")}}}, bkNoName, 11},
		{"ListBackupPlans", &backup.ListBackupPlansOutput{BackupPlansList: []backuptypes.BackupPlansListMember{{BackupPlanArn: sdkaws.String(bkPlanARN("no-id")), BackupPlanName: sdkaws.String("no-id")}}}, "no-id", 4},
		{"ListFrameworks", &backup.ListFrameworksOutput{Frameworks: []backuptypes.Framework{{}}}, bkNoName, 2},
		{"ListReportPlans", &backup.ListReportPlansOutput{ReportPlans: []backuptypes.ReportPlan{{}}}, bkNoName, 2},
		{"ListRestoreTestingPlans", &backup.ListRestoreTestingPlansOutput{RestoreTestingPlans: []backuptypes.RestoreTestingPlanForList{{}}}, bkNoName, 4},
		{"ListTieringConfigurations", &backup.ListTieringConfigurationsOutput{TieringConfigurations: []backuptypes.TieringConfigurationsListMember{{}}}, bkNoName, 2},
		{"ListLegalHolds", &backup.ListLegalHoldsOutput{LegalHolds: []backuptypes.LegalHold{{LegalHoldId: sdkaws.String("hold-id")}}}, "hold-id", 2},
		{"ListBackupAccessPoints", &backup.ListBackupAccessPointsOutput{BackupAccessPoints: []backuptypes.ListAccessPointsMember{{}}}, bkNoName, 2},
		{"ListBackupSelections", &backup.ListBackupSelectionsOutput{BackupSelectionsList: []backuptypes.BackupSelectionsListMember{{}}}, bkNoName, 2},
		{"ListRestoreTestingSelections", &backup.ListRestoreTestingSelectionsOutput{RestoreTestingSelections: []backuptypes.RestoreTestingSelectionForList{{}}}, "in plan rtp-", 2},
		{"ListRecoveryPointsByBackupVault", &backup.ListRecoveryPointsByBackupVaultOutput{RecoveryPoints: []backuptypes.RecoveryPointByBackupVault{{}}}, "in vault vault-", 8},
		{"ListRestoreAccessBackupVaults", &backup.ListRestoreAccessBackupVaultsOutput{RestoreAccessBackupVaults: []backuptypes.RestoreAccessBackupVaultListMember{{}}}, "in vault vault-bare", 1},
	}
	for _, c := range cases {
		t.Run(c.op, func(t *testing.T) {
			st := newTestStore(t)
			all := countWarnings(st)
			warns := bacWarnings(st, "backup:"+c.op)
			handlers := bkHandlers(newBKCalls())
			handlers[c.op] = func(any) (any, error) { return c.out, nil }

			total, _, err := bkScan(context.Background(), st, handlers)
			if err != nil {
				t.Fatalf("scan: %v", err)
			}
			if total != bkTotal-c.lost {
				t.Errorf("total = %d; want %d", total, bkTotal-c.lost)
			}
			if len(*warns) != 1 || *all != 1 {
				t.Fatalf("backup:%s warnings = %v (all ops %d); want 1", c.op, *warns, *all)
			}
			if w := (*warns)[0]; w.Scope != testAccountID+"/"+testRegion || !strings.Contains(w.Message, c.key) {
				t.Errorf("warning = %+v; want Scope %s/%s and the element keyed by %q", w, testAccountID, testRegion, c.key)
			}
		})
	}
}

// A denied listing warns once under its own op and stores nothing it would
// have listed; every other listing still runs. A child listing denied for
// every parent warns once. A denied GetBackupPlan keeps the plans as their
// list elements.
func TestScanBackup_DeniedListingWarnsUnderItsOp(t *testing.T) {
	lost := map[string]int{
		"ListBackupVaults": 11, "ListBackupPlans": 4, "ListRestoreTestingPlans": 4,
		"ListBackupSelections": 2, "ListRestoreTestingSelections": 2,
		"ListRecoveryPointsByBackupVault": 8, "ListRestoreAccessBackupVaults": 1, "GetBackupPlan": 0,
	}
	for _, k := range bkKinds() {
		if _, ok := lost[k.op]; !ok {
			lost[k.op] = 2
		}
	}
	for op, n := range lost {
		t.Run(op, func(t *testing.T) {
			st := newTestStore(t)
			all := countWarnings(st)
			warnings := bacWarnings(st, "backup:"+op)
			handlers := bkHandlers(newBKCalls())
			delete(handlers, op)

			total, _, err := bkScan(context.Background(), st, handlers)
			if err != nil {
				t.Fatalf("err = %v; want nil", err)
			}
			if total != bkTotal-n {
				t.Errorf("total = %d; want %d", total, bkTotal-n)
			}
			if len(*warnings) != 1 || *all != 1 {
				t.Errorf("backup:%s warnings = %v (all ops %d); want 1", op, *warnings, *all)
			}
		})
	}
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

// A plan is stored as its GetBackupPlan body. A plan GetBackupPlan no longer
// finds is dropped silently, with no tag read and no selection listing; any
// other failure keeps it as its list element with its tags and selections: a
// denial warns once under backup:GetBackupPlan, a throttle after retries is a
// transient error, any other error a hard one.
func TestScanBackup_PlanReadFailures(t *testing.T) {
	failed := bkPlanARN("plan-tagged")
	for _, c := range []struct {
		name      string
		err       error
		outcome   serviceOutcome
		code      string
		wantWarns int
	}{
		{"read", nil, 0, "", 0},
		{"not found", bkGone, 0, "", 0},
		{"denied", bkDenied, 0, "", 1},
		{"transient", bkThrottle, outcomeTransient, "", 0},
		{"other error", bkBadInput, outcomeError, "InvalidParameterValueException", 0},
	} {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			all := countWarnings(st)
			warns := bacWarnings(st, "backup:GetBackupPlan")
			calls := newBKCalls()
			h := bkHandlers(calls)
			get := h["GetBackupPlan"]
			h["GetBackupPlan"] = func(in any) (any, error) {
				if c.err != nil && bkPlanARN(sv(in.(*backup.GetBackupPlanInput).BackupPlanId)) == failed {
					return nil, c.err
				}
				return get(in)
			}

			total, _, err := bkScan(context.Background(), st, h)
			if c.outcome == 0 && err != nil {
				t.Fatalf("err = %v; want nil", err)
			}
			if c.outcome != 0 {
				assertGlueOutcome(t, err, c.outcome)
				assertAPICode(t, err, cmp.Or(c.code, "ThrottlingException"))
			}
			if len(*warns) != c.wantWarns || *all != c.wantWarns {
				t.Errorf("backup:GetBackupPlan warnings = %v (all ops %d); want %d", *warns, *all, c.wantWarns)
			}
			r, ok := bkRowOf(t, st, TypeBackupPlan, failed)
			if c.err == bkGone {
				if ok || total != bkTotal-2 {
					t.Errorf("plan stored = %v, total = %d; want dropped with its selection, %d", ok, total, bkTotal-2)
				}
				if slices.Contains(calls.tagARNs, failed) || slices.Contains(calls.sortedParents("ListBackupSelections"), "plan-tagged") {
					t.Errorf("a dropped plan was read: ListTags %v, selections %v", calls.tagARNs, calls.sortedParents("ListBackupSelections"))
				}
				return
			}
			if !ok || total != bkTotal {
				t.Fatalf("plan stored = %v, total = %d; want stored, %d", ok, total, bkTotal)
			}
			if body := bkPlanBody(t, r); body != (c.err == nil) {
				t.Errorf("plan holds the GetBackupPlan body = %v; want %v", body, c.err == nil)
			}
			assertTagsMap(t, r.TagsJSON, bkTags(TypeBackupPlan))
			assertContainsNative(t, st, failed, bkSelectionARN("plan-tagged"))
		})
	}
}

// A selection listing that fails for one plan keeps the other plan's
// selections and the plan row: a plan it no longer finds is skipped silently;
// a denial warns once; any other error is returned, a transient one too, so
// that the scan is partial rather than warned about.
func TestScanBackup_SelectionsPerPlan(t *testing.T) {
	for _, c := range []struct {
		name      string
		err       error
		outcome   serviceOutcome
		code      string
		wantWarns int
	}{
		{"not found", bkGone, 0, "", 0},
		{"denied", bkDenied, 0, "", 1},
		{"transient", bkThrottle, outcomeTransient, "", 0},
		{"other error", bkBadInput, outcomeError, "InvalidParameterValueException", 0},
	} {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			all := countWarnings(st)
			warns := bacWarnings(st, "backup:ListBackupSelections")
			h := bkHandlers(newBKCalls())
			list := h["ListBackupSelections"]
			h["ListBackupSelections"] = func(in any) (any, error) {
				if sv(in.(*backup.ListBackupSelectionsInput).BackupPlanId) == "plan-bare" {
					return nil, c.err
				}
				return list(in)
			}

			total, _, err := bkScan(context.Background(), st, h)
			if c.outcome == 0 && err != nil {
				t.Fatalf("err = %v; want nil", err)
			}
			if c.outcome != 0 {
				assertGlueOutcome(t, err, c.outcome)
			}
			if c.code != "" {
				assertAPICode(t, err, c.code)
			}
			if total != bkTotal-1 {
				t.Errorf("total = %d; want %d", total, bkTotal-1)
			}
			if _, ok := bkRowOf(t, st, TypeBackupPlan, bkPlanARN("plan-bare")); !ok {
				t.Error("plan-bare not stored")
			}
			assertContainsNative(t, st, bkPlanARN("plan-tagged"), bkSelectionARN("plan-tagged"))
			if len(*warns) != c.wantWarns || *all != c.wantWarns {
				t.Errorf("backup:ListBackupSelections warnings = %v (all ops %d); want %d", *warns, *all, c.wantWarns)
			}
		})
	}
}

// A failed closure write leaves the plans and selections stored before it
// counted, and stops the scan.
func TestScanBackup_ClosureFailureKeepsCounts(t *testing.T) {
	st := newTestStore(t)
	refuseClosureWrites(t, st)
	calls := newBKCalls()
	total, _, err := bkScan(context.Background(), st, bkHandlers(calls))
	if !errors.Is(err, store.ErrStoreWrite) {
		t.Errorf("err = %v; want the closure's store write failure", err)
	}
	if n := len(rowsByNativeID(t, st, TypeBackupSelection)); n != 2 {
		t.Fatalf("selection rows = %d; want 2 stored", n)
	}
	if n := countStoredRows(t, st); total != n {
		t.Errorf("total = %d; want %d, every row stored", total, n)
	}
	if n := calls.count("ListFrameworks"); n != 0 {
		t.Errorf("ListFrameworks called %d times after the failed write", n)
	}
}

// Transient phase errors with no hard error reach the dispatcher as they are,
// so the scan is partial; beside a hard error of another phase they are
// demoted to one warning under their op.
func TestScanBackup_PhaseErrorJoin(t *testing.T) {
	throttledVaults := func() map[string]func(in any) (any, error) {
		h := bkHandlers(newBKCalls())
		h["ListBackupVaults"] = func(any) (any, error) { return nil, bkThrottle }
		return h
	}
	t.Run("lone transient", func(t *testing.T) {
		_, _, err := bkScan(context.Background(), newTestStore(t), throttledVaults())
		assertGlueOutcome(t, err, outcomeTransient)
	})
	t.Run("two transients", func(t *testing.T) {
		h := throttledVaults()
		h["ListBackupAccessPoints"] = func(any) (any, error) { return nil, bkThrottle }
		_, _, err := bkScan(context.Background(), newTestStore(t), h)
		assertGlueOutcome(t, err, outcomeTransient)
	})
	t.Run("beside a hard error", func(t *testing.T) {
		st := newTestStore(t)
		warns := countOpWarnings(st, "backup:ListBackupVaults")
		h := throttledVaults()
		h["ListBackupAccessPoints"] = func(any) (any, error) { return nil, bkBadInput }
		_, _, err := bkScan(context.Background(), st, h)
		assertGlueOutcome(t, err, outcomeError)
		assertAPICode(t, err, "InvalidParameterValueException")
		if *warns != 1 {
			t.Errorf("backup:ListBackupVaults warnings = %d; want 1 for the demoted transient", *warns)
		}
	})
}

// Once ctx ends no further phase starts, and the stop is one member of the
// error, although the phase in flight saw it too.
func TestScanBackup_ContextEndStopsOnce(t *testing.T) {
	st := newTestStore(t)
	calls := newBKCalls()
	handlers := bkHandlers(calls)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	list := handlers["ListFrameworks"]
	handlers["ListFrameworks"] = func(in any) (any, error) {
		if bacField[*string](in, "NextToken") != nil {
			cancel()
			return nil, context.Canceled
		}
		return list(in)
	}

	_, _, err := bkScan(ctx, st, handlers)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v; want context.Canceled", err)
	}
	stops := 0
	for _, m := range bacTopMembers(err) {
		if errors.Is(m, context.Canceled) {
			stops++
		}
	}
	if stops != 1 {
		t.Errorf("members carrying the stop = %d; want 1 (err %v)", stops, err)
	}
	for _, op := range []string{"ListReportPlans", "ListRecoveryPointsByBackupVault"} {
		if n := calls.count(op); n != 0 {
			t.Errorf("%s called %d times after ctx ended", op, n)
		}
	}
}

// An NXDOMAIN after rows were stored is a failure of this scan, not a region
// gap: the counts are kept. With nothing stored it stays region-unavailable.
func TestScanBackup_NXDOMAIN(t *testing.T) {
	nx := &net.DNSError{Err: "no such host", Name: "backup.us-east-1.amazonaws.com", IsNotFound: true}
	t.Run("later phase after rows were stored", func(t *testing.T) {
		handlers := bkHandlers(newBKCalls())
		handlers["ListTieringConfigurations"] = func(any) (any, error) { return nil, nx }
		total, _, err := bkScan(context.Background(), newTestStore(t), handlers)
		assertGlueOutcome(t, err, outcomeError)
		if total != bkTotal-2 {
			t.Errorf("total = %d; want the %d other rows counted", total, bkTotal-2)
		}
	})
	t.Run("tag read after the listing", func(t *testing.T) {
		calls := newBKCalls()
		calls.errs[bkVaultARN("vault-bare")] = nx
		_, _, err := bkScan(context.Background(), newTestStore(t), bkHandlers(calls))
		assertGlueOutcome(t, err, outcomeError)
	})
	t.Run("nothing stored", func(t *testing.T) {
		st := newTestStore(t)
		warns := countWarnings(st)
		acct := newTestAccount(testAccountID)
		acct.cfg = opStubConfig(nil, nx)
		_, _, err := scanBackup(context.Background(), acct, testRegion, st, testScanID)
		assertGlueOutcome(t, err, outcomeUnavailable)
		if *warns != 0 {
			t.Errorf("warnings = %d; want 0", *warns)
		}
	})
}

// The client is pinned to the scanned region, not the config's default.
func TestScanBackup_CallsGoToScannedRegion(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	var regions parentCalls
	cfg := opStubConfig(bkHandlers(newBKCalls()), bkDenied)
	cfg.Region = "ap-southeast-2"
	acct.cfg = recordCallRegions(cfg, &regions)

	if _, _, err := scanBackup(context.Background(), acct, "eu-west-1", st, testScanID); err != nil {
		t.Fatalf("scanBackup: %v", err)
	}
	if got := regions.distinct(); !slices.Equal(got, []string{"eu-west-1"}) {
		t.Errorf("calls went to regions %v, want [eu-west-1]", got)
	}
}

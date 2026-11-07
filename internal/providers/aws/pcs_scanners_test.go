package aws

import (
	"cmp"
	"context"
	"errors"
	"net"
	"slices"
	"strings"
	"testing"
	"time"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/pcs"
	pcstypes "github.com/aws/aws-sdk-go-v2/service/pcs/types"
	"github.com/icearp/disco-cli/store"
)

// The fixture: ListClusters serves pcs_t on its first page and pcs_b on its
// second. Each cluster pcs_<n> holds one compute node group cng_<n> and one
// queue q_<n>. ListTagsForResource answers with tags for every ARN ending in
// "_t".

var pcsCreated = time.Date(2026, 9, 4, 9, 30, 0, 0, time.UTC)

// pcsModified differs from pcsCreated so a row that took its CreatedAt from
// ModifiedAt is caught.
var pcsModified = pcsCreated.Add(time.Hour)

func pcsTestARN(res string) string {
	return arnFor("aws", "pcs", testRegion, testAccountID, res)
}

// pcsSuffix is the suffix every element of cluster cid carries.
func pcsSuffix(cid string) string { return strings.TrimPrefix(cid, "pcs_") }

func pcsCluster(n string) pcstypes.ClusterSummary {
	return pcstypes.ClusterSummary{
		Arn: sdkaws.String(pcsTestARN("cluster/pcs_" + n)), Id: sdkaws.String("pcs_" + n), Name: sdkaws.String("cluster-" + n),
		Status: pcstypes.ClusterStatusActive, CreatedAt: &pcsCreated, ModifiedAt: &pcsModified,
	}
}

func pcsNodeGroup(cid string) pcstypes.ComputeNodeGroupSummary {
	id := "cng_" + pcsSuffix(cid)
	return pcstypes.ComputeNodeGroupSummary{
		Arn: sdkaws.String(pcsTestARN("cluster/" + cid + "/computenodegroup/" + id)), ClusterId: &cid, Id: &id,
		Name: sdkaws.String("nodes-" + pcsSuffix(cid)), Status: pcstypes.ComputeNodeGroupStatusActive,
		CreatedAt: &pcsCreated, ModifiedAt: &pcsModified,
	}
}

func pcsQueue(cid string) pcstypes.QueueSummary {
	id := "q_" + pcsSuffix(cid)
	return pcstypes.QueueSummary{
		Arn: sdkaws.String(pcsTestARN("cluster/" + cid + "/queue/" + id)), ClusterId: &cid, Id: &id,
		Name: sdkaws.String("queue-" + pcsSuffix(cid)), Status: pcstypes.QueueStatusActive,
		CreatedAt: &pcsCreated, ModifiedAt: &pcsModified,
	}
}

// pcsWant is a row the fixture stores: its type, ARN, Name and the element
// its attributes are.
type pcsWant struct {
	typ, arn, name string
	element        any
}

// pcsRows is every row the fixture stores.
func pcsRows() []pcsWant {
	var rows []pcsWant
	for _, n := range []string{"t", "b"} {
		c := pcsCluster(n)
		g, q := pcsNodeGroup(sv(c.Id)), pcsQueue(sv(c.Id))
		rows = append(rows,
			pcsWant{TypePCSCluster, sv(c.Arn), sv(c.Name), c},
			pcsWant{TypePCSComputeNodeGroup, sv(g.Arn), sv(g.Name), g},
			pcsWant{TypePCSQueue, sv(q.Arn), sv(q.Name), q},
		)
	}
	return rows
}

func newPCSCalls() *crtCalls {
	c := &crtCalls{newBACCalls()}
	for _, r := range pcsRows() {
		if strings.HasSuffix(r.arn, "_t") {
			c.tags[r.arn] = map[string]string{"type": r.typ}
		}
	}
	return c
}

// pcsListTags answers ListTagsForResource from c's tags and errs, keyed by
// ARN, recording each ARN sent.
func pcsListTags(c *crtCalls) func(any) (any, error) {
	return func(in any) (any, error) {
		c.called("ListTagsForResource", in)
		arn := sv(in.(*pcs.ListTagsForResourceInput).ResourceArn)
		c.mu.Lock()
		c.tagARNs = append(c.tagARNs, arn)
		err, tags := c.errs[arn], c.tags[arn]
		c.mu.Unlock()
		if err != nil {
			return nil, err
		}
		return &pcs.ListTagsForResourceOutput{Tags: tags}, nil
	}
}

// pcsClusterOf is the cluster a child listing was sent.
func pcsClusterOf(in any) string { return sv(bacField[*string](in, "ClusterIdentifier")) }

// pcsChildOps is every per-cluster listing, in call order.
var pcsChildOps = []string{"ListComputeNodeGroups", "ListQueues"}

// pcsChildOut is op's one page for cluster cid.
func pcsChildOut(op, cid string, next *string) any {
	if op == "ListComputeNodeGroups" {
		return &pcs.ListComputeNodeGroupsOutput{ComputeNodeGroups: []pcstypes.ComputeNodeGroupSummary{pcsNodeGroup(cid)}, NextToken: next}
	}
	return &pcs.ListQueuesOutput{Queues: []pcstypes.QueueSummary{pcsQueue(cid)}, NextToken: next}
}

// pcsEmptyOut is op's empty page.
func pcsEmptyOut(op string) any {
	if op == "ListComputeNodeGroups" {
		return &pcs.ListComputeNodeGroupsOutput{}
	}
	return &pcs.ListQueuesOutput{}
}

// pcsHandlers answers every op the scan calls with the fixture (pcsRows).
func pcsHandlers(c *crtCalls) map[string]func(any) (any, error) {
	h := map[string]func(any) (any, error){
		"ListTagsForResource": pcsListTags(c),
		"ListClusters": c.twoPages("ListClusters", func(n string, next *string) any {
			return &pcs.ListClustersOutput{Clusters: []pcstypes.ClusterSummary{pcsCluster(n)}, NextToken: next}
		}),
	}
	for _, op := range pcsChildOps {
		h[op] = c.answer(op, func(in any) any { return pcsChildOut(op, pcsClusterOf(in), nil) })
	}
	return h
}

// pcsScan runs the scan's real entry point against handlers; every op they do
// not answer is denied.
func pcsScan(ctx context.Context, t *testing.T, st *store.Store, handlers map[string]func(any) (any, error)) (int, int, error) {
	t.Helper()
	acct := newTestAccount(testAccountID)
	acct.cfg = opStubConfig(handlers, crDenied)
	return scanPCS(ctx, acct, testRegion, st, testScanID)
}

func pcsRowOf(t *testing.T, st *store.Store, typ, arn string) (store.Resource, bool) {
	t.Helper()
	r, ok := rowsByNativeID(t, st, typ)[arn]
	return r, ok
}

// Every type stores the tags ListTagsForResource returns for its ARN, nil for
// a resource without tags, and every stored row's ARN is sent to the tag op.
// Both cluster pages are read, each child listing is sent each cluster's Id,
// every listing asks for the model's cap of 100, and the scan reports no
// error or warning. Each row keeps the base scan's Name, Region, Status and
// verbatim element, and has the element's CreatedAt.
func TestScanPCS_StoresTagsOfEveryType(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	warns := countWarnings(st)
	calls := newPCSCalls()
	h := pcsHandlers(calls)
	sentClusters := map[string][]string{}
	for _, op := range pcsChildOps {
		list := h[op]
		h[op] = func(in any) (any, error) {
			calls.mu.Lock()
			sentClusters[op] = append(sentClusters[op], pcsClusterOf(in))
			calls.mu.Unlock()
			return list(in)
		}
	}

	total, inserted, err := pcsScan(context.Background(), t, st, h)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	rows := pcsRows()
	if total != 6 || inserted != 6 || len(rows) != 6 {
		t.Errorf("total, inserted = %d, %d; want 6 (fixture rows %d)", total, inserted, len(rows))
	}
	if *warns != 0 {
		t.Errorf("warnings = %d; want 0", *warns)
	}
	var wantTagARNs []string
	for _, want := range rows {
		r, ok := pcsRowOf(t, st, want.typ, want.arn)
		if !ok {
			t.Errorf("%s %s not stored", want.typ, want.arn)
			continue
		}
		wantTagARNs = append(wantTagARNs, want.arn)
		if strings.HasSuffix(want.arn, "_t") {
			assertTagsMap(t, r.TagsJSON, map[string]string{"type": want.typ})
		} else if r.TagsJSON != nil {
			t.Errorf("%s %s TagsJSON = %s; want nil", want.typ, want.arn, *r.TagsJSON)
		}
		if sv(r.Name) != want.name || sv(r.Status) != "ACTIVE" || sv(r.CreatedAt) != "2026-09-04T09:30:00Z" {
			t.Errorf("%s %s Name, Status, CreatedAt = %v, %v, %v; want %q, ACTIVE, 2026-09-04T09:30:00Z", want.typ, want.arn, r.Name, r.Status, r.CreatedAt, want.name)
		}
		if sv(r.Region) != testRegion || r.AttributesJSON != mustJSON(want.element) {
			t.Errorf("%s %s Region, attributes = %v, %s; want %s, %s", want.typ, want.arn, r.Region, r.AttributesJSON, testRegion, mustJSON(want.element))
		}
	}
	got := slices.Clone(calls.tagARNs)
	slices.Sort(got)
	slices.Sort(wantTagARNs)
	if !slices.Equal(got, wantTagARNs) {
		t.Errorf("ListTagsForResource ARNs = %v; want %v", got, wantTagARNs)
	}
	for _, op := range pcsChildOps {
		sent := sentClusters[op]
		slices.Sort(sent)
		if want := []string{"pcs_b", "pcs_t"}; !slices.Equal(sent, want) {
			t.Errorf("%s clusters = %v; want %v", op, sent, want)
		}
	}
	for op, want := range map[string]int{"ListClusters": 2, "ListComputeNodeGroups": 2, "ListQueues": 2} {
		caps := calls.maxResults[op]
		if len(caps) != want || slices.ContainsFunc(caps, func(n int32) bool { return n != 100 }) {
			t.Errorf("%s MaxResults = %v; want %d calls, each 100", op, caps, want)
		}
	}
}

// An aws-us-gov account scanned in us-gov-west-1 sends the gov ARNs its
// listings return to the tag op unchanged and stores their tags. Its
// elements carry no Status, which is stored unset rather than "".
func TestScanPCS_GovCloudARNsReachTagOp(t *testing.T) {
	t.Parallel()
	const govRegion = "us-gov-west-1"
	clusterARN := arnFor("aws-us-gov", "pcs", govRegion, testAccountID, "cluster/pcs_gov")
	queueARN := clusterARN + "/queue/q_gov"
	st := newTestStore(t)
	calls := newPCSCalls()
	calls.tags[clusterARN] = map[string]string{"env": "gov"}
	calls.tags[queueARN] = map[string]string{"env": "gov-queue"}
	h := pcsHandlers(calls)
	h["ListClusters"] = calls.answer("ListClusters", func(any) any {
		return &pcs.ListClustersOutput{Clusters: []pcstypes.ClusterSummary{{Arn: &clusterARN, Id: sdkaws.String("pcs_gov"), Name: sdkaws.String("gov")}}}
	})
	h["ListComputeNodeGroups"] = calls.answer("ListComputeNodeGroups", func(any) any { return pcsEmptyOut("ListComputeNodeGroups") })
	h["ListQueues"] = calls.answer("ListQueues", func(any) any {
		return &pcs.ListQueuesOutput{Queues: []pcstypes.QueueSummary{{Arn: &queueARN, Id: sdkaws.String("q_gov"), Name: sdkaws.String("gov queue")}}}
	})
	acct := partitionAccount("aws-us-gov")
	acct.cfg = opStubConfig(h, crDenied)

	total, _, err := scanPCS(context.Background(), acct, govRegion, st, testScanID)
	if err != nil || total != 2 {
		t.Fatalf("scan = %d, %v; want 2, nil", total, err)
	}
	got := slices.Clone(calls.tagARNs)
	slices.Sort(got)
	if want := []string{clusterARN, queueARN}; !slices.Equal(got, want) {
		t.Errorf("ListTagsForResource ARNs = %v; want %v", got, want)
	}
	for _, c := range []struct{ typ, arn, env string }{
		{TypePCSCluster, clusterARN, "gov"},
		{TypePCSQueue, queueARN, "gov-queue"},
	} {
		r, ok := pcsRowOf(t, st, c.typ, c.arn)
		if !ok {
			t.Fatalf("%s %s not stored", c.typ, c.arn)
		}
		assertTagsMap(t, r.TagsJSON, map[string]string{"env": c.env})
		if sv(r.Region) != govRegion || r.Status != nil {
			t.Errorf("%s Region, Status = %v, %v; want %s, nil", c.arn, r.Region, r.Status, govRegion)
		}
	}
}

// No cluster stores nothing, makes no child listing, reads no tags and
// reports nothing.
func TestScanPCS_NoClusters(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	warns := countWarnings(st)
	calls := newPCSCalls()
	h := pcsHandlers(calls)
	h["ListClusters"] = calls.answer("ListClusters", func(any) any { return &pcs.ListClustersOutput{} })

	total, inserted, err := pcsScan(context.Background(), t, st, h)
	if err != nil || total != 0 || inserted != 0 {
		t.Fatalf("scan = %d, %d, %v; want 0, 0, nil", total, inserted, err)
	}
	if *warns != 0 || len(calls.tagARNs) != 0 {
		t.Errorf("warnings, tag reads = %d, %d; want 0, 0", *warns, len(calls.tagARNs))
	}
	for _, op := range pcsChildOps {
		if n := calls.count(op); n != 0 {
			t.Errorf("%s calls = %d; want 0", op, n)
		}
	}
}

// A cluster whose child listings are empty stores only itself, reads only
// its tags and reports nothing.
func TestScanPCS_EmptyCluster(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	warns := countWarnings(st)
	calls := newPCSCalls()
	h := pcsHandlers(calls)
	h["ListClusters"] = calls.answer("ListClusters", func(any) any {
		return &pcs.ListClustersOutput{Clusters: []pcstypes.ClusterSummary{pcsCluster("t")}}
	})
	for _, op := range pcsChildOps {
		h[op] = calls.answer(op, func(any) any { return pcsEmptyOut(op) })
	}

	total, _, err := pcsScan(context.Background(), t, st, h)
	if err != nil || total != 1 {
		t.Fatalf("scan = %d, %v; want 1, nil", total, err)
	}
	for _, op := range pcsChildOps {
		if n := calls.count(op); n != 1 {
			t.Errorf("%s calls = %d; want 1, for pcs_t", op, n)
		}
	}
	if want := []string{pcsTestARN("cluster/pcs_t")}; *warns != 0 || !slices.Equal(calls.tagARNs, want) {
		t.Errorf("warnings, tag reads = %d, %v; want 0, %v", *warns, calls.tagARNs, want)
	}
}

// A rescan reports every stored row in its total, not only the newly inserted
// ones. The dispatcher reports total for the service, and a first scan into an
// empty store has total == inserted, so only a rescan catches the two swapped.
func TestScanPCS_RescanKeepsTotal(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	calls := newPCSCalls()
	if _, _, err := pcsScan(context.Background(), t, st, pcsHandlers(calls)); err != nil {
		t.Fatalf("first scan: %v", err)
	}
	total, _, err := pcsScan(context.Background(), t, st, pcsHandlers(calls))
	if err != nil {
		t.Fatalf("rescan: %v", err)
	}
	if total != 6 {
		t.Errorf("rescan total = %d; want 6", total)
	}
}

// A failed tag read keeps the row without tags unless the resource is gone:
// a denial warns once under pcs:ListTagsForResource, a throttle after retries
// is a transient error, any other code a hard one; a not-found drops the
// row. A cluster dropped as not found is no parent: its children are not
// listed.
func TestScanPCS_TagReadFailures(t *testing.T) {
	t.Parallel()
	for _, target := range []struct {
		typ, arn string
		goneRows int
	}{
		{TypePCSCluster, pcsTestARN("cluster/pcs_t"), 3},
		{TypePCSQueue, pcsTestARN("cluster/pcs_b/queue/q_b"), 1},
	} {
		t.Run(target.typ, func(t *testing.T) {
			for _, c := range []struct {
				name      string
				err       error
				kept      bool
				outcome   serviceOutcome
				wantWarns int
			}{
				{"denied", crDenied, true, 0, 1},
				{"not found", crNotFound, false, 0, 0},
				{"other error", crtBadInput, true, outcomeError, 0},
				{"transient", crThrottled, true, outcomeTransient, 0},
			} {
				t.Run(c.name, func(t *testing.T) {
					st := newTestStore(t)
					warnings := bacWarnings(st, pcsTagsOp)
					calls := newPCSCalls()
					calls.errs[target.arn] = c.err

					total, _, err := pcsScan(context.Background(), t, st, pcsHandlers(calls))
					if c.outcome == 0 && err != nil {
						t.Fatalf("err = %v; want nil", err)
					}
					if c.outcome != 0 {
						assertGlueOutcome(t, err, c.outcome)
					}
					if c.outcome == outcomeError {
						assertAPICode(t, err, "ValidationException")
					}
					r, ok := pcsRowOf(t, st, target.typ, target.arn)
					if ok != c.kept {
						t.Fatalf("row stored = %v; want %v", ok, c.kept)
					}
					if ok && r.TagsJSON != nil {
						t.Errorf("TagsJSON = %q; want nil", *r.TagsJSON)
					}
					want := 6
					if !c.kept {
						want -= target.goneRows
					}
					if total != want || countStoredRows(t, st) != want {
						t.Errorf("total, stored = %d, %d; want %d", total, countStoredRows(t, st), want)
					}
					if len(*warnings) != c.wantWarns {
						t.Fatalf("%s warnings = %v; want %d", pcsTagsOp, *warnings, c.wantWarns)
					}
					if c.wantWarns == 1 && (*warnings)[0].Scope != testAccountID+"/"+testRegion {
						t.Errorf("warning Scope = %q; want %s/%s", (*warnings)[0].Scope, testAccountID, testRegion)
					}
				})
			}
		})
	}
}

// Denied tag reads in several phases are one warning for the scan.
func TestScanPCS_TagDenialsWarnOnce(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	warnings := bacWarnings(st, pcsTagsOp)
	calls := newPCSCalls()
	denied := []struct{ typ, arn string }{
		{TypePCSCluster, pcsTestARN("cluster/pcs_t")},
		{TypePCSComputeNodeGroup, pcsTestARN("cluster/pcs_b/computenodegroup/cng_b")},
	}
	for _, d := range denied {
		calls.errs[d.arn] = crDenied
	}

	total, _, err := pcsScan(context.Background(), t, st, pcsHandlers(calls))
	if err != nil || total != 6 {
		t.Fatalf("scan = %d, %v; want 6, nil", total, err)
	}
	if len(*warnings) != 1 || (*warnings)[0].Scope != testAccountID+"/"+testRegion {
		t.Errorf("%s warnings = %v; want one, scoped %s/%s", pcsTagsOp, *warnings, testAccountID, testRegion)
	}
	for _, d := range denied {
		if r, ok := pcsRowOf(t, st, d.typ, d.arn); !ok || r.TagsJSON != nil {
			t.Errorf("%s stored %v, TagsJSON %v; want it kept without tags", d.arn, ok, r.TagsJSON)
		}
	}
}

// A throttled tag read beside a hard failure of the same op is demoted to
// one warning, so the hard error decides the outcome.
func TestScanPCS_TagThrottleDemotedBesideHardError(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	warns := countOpWarnings(st, pcsTagsOp)
	calls := newPCSCalls()
	calls.errs[pcsTestARN("cluster/pcs_t")] = crThrottled
	calls.errs[pcsTestARN("cluster/pcs_b/queue/q_b")] = crtBadInput

	_, _, err := pcsScan(context.Background(), t, st, pcsHandlers(calls))
	assertGlueOutcome(t, err, outcomeError)
	assertAPICode(t, err, "ValidationException")
	if *warns != 1 {
		t.Errorf("%s warnings = %d; want 1", pcsTagsOp, *warns)
	}
}

// A denied listing warns once under its own op, for both clusters of a child
// listing, and costs no other phase its rows; a denied cluster listing
// leaves the child listings no cluster to read.
func TestScanPCS_DeniedListingWarnsUnderItsOp(t *testing.T) {
	t.Parallel()
	for _, op := range append([]string{"ListClusters"}, pcsChildOps...) {
		t.Run(op, func(t *testing.T) {
			st := newTestStore(t)
			warns := countWarnings(st)
			opWarns := countOpWarnings(st, "pcs:"+op)
			calls := newPCSCalls()
			h := pcsHandlers(calls)
			h[op] = func(any) (any, error) { return nil, crDenied }

			total, _, err := pcsScan(context.Background(), t, st, h)
			if err != nil {
				t.Fatalf("err = %v; want nil", err)
			}
			if *warns != 1 || *opWarns != 1 {
				t.Errorf("warnings = %d (pcs:%s: %d); want 1 under the op", *warns, op, *opWarns)
			}
			want := 6 - 2
			if op == "ListClusters" {
				want = 0
			}
			if stored := countStoredRows(t, st); total != want || stored != want {
				t.Errorf("total = %d, stored rows = %d; want %d", total, stored, want)
			}
			if op == "ListClusters" && calls.count("ListQueues") != 0 {
				t.Errorf("ListQueues called %d times with no cluster read", calls.count("ListQueues"))
			}
		})
	}
}

// A failed write of any type stops the scan before any later listing and
// sends no warning about the rows it lost, their denied tag reads included.
func TestScanPCS_FailedWriteStopsWithoutWarning(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		typ, deniedARN string
		later          []string
	}{
		{TypePCSCluster, pcsTestARN("cluster/pcs_t"), pcsChildOps},
		{TypePCSComputeNodeGroup, pcsTestARN("cluster/pcs_t/computenodegroup/cng_t"), pcsChildOps[1:]},
		{TypePCSQueue, pcsTestARN("cluster/pcs_t/queue/q_t"), nil},
	} {
		t.Run(c.typ, func(t *testing.T) {
			st := newTestStore(t)
			warns := countWarnings(st)
			refuseStoreWrites(t, st, c.typ)
			calls := newPCSCalls()
			calls.errs[c.deniedARN] = crDenied

			_, _, err := pcsScan(context.Background(), t, st, pcsHandlers(calls))
			if !errors.Is(err, store.ErrStoreWrite) {
				t.Fatalf("err = %v; want store.ErrStoreWrite", err)
			}
			if *warns != 0 {
				t.Errorf("warnings = %d; want 0", *warns)
			}
			for _, op := range c.later {
				if n := calls.count(op); n != 0 {
					t.Errorf("%s called %d times after the failed write", op, n)
				}
			}
		})
	}
}

// A listing's failed page keeps the pages read before it, one cluster's
// failed listing costs the other cluster none of its rows, and no listing's
// failure stops the phases after it.
func TestScanPCS_ListFailureKeepsPagesAndRunsEveryPhase(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	calls := newPCSCalls()
	h := pcsHandlers(calls)
	h["ListClusters"] = calls.answer("ListClusters", func(any) any {
		return &pcs.ListClustersOutput{Clusters: []pcstypes.ClusterSummary{pcsCluster("t"), pcsCluster("b")}}
	})
	nodeGroups := h["ListComputeNodeGroups"]
	h["ListComputeNodeGroups"] = func(in any) (any, error) {
		if pcsClusterOf(in) == "pcs_b" {
			return nil, crtBadInput
		}
		return nodeGroups(in)
	}
	h["ListQueues"] = func(in any) (any, error) {
		if bacField[*string](in, "NextToken") != nil {
			return nil, apiErr("ConflictException", "page 2")
		}
		return pcsChildOut("ListQueues", pcsClusterOf(in), sdkaws.String("p2")), nil
	}

	total, _, err := pcsScan(context.Background(), t, st, h)
	for _, code := range []string{"ValidationException", "ConflictException"} {
		if !joinCarriesAPIErrorCode(err, code) {
			t.Errorf("err = %v; want it to carry %s", err, code)
		}
	}
	// 6 rows less cng_b.
	if stored := countStoredRows(t, st); total != 5 || stored != 5 {
		t.Errorf("total, stored = %d, %d; want 5", total, stored)
	}
	for _, want := range []struct{ typ, arn string }{
		{TypePCSComputeNodeGroup, pcsTestARN("cluster/pcs_t/computenodegroup/cng_t")},
		{TypePCSQueue, pcsTestARN("cluster/pcs_t/queue/q_t")},
		{TypePCSQueue, pcsTestARN("cluster/pcs_b/queue/q_b")},
	} {
		if _, ok := pcsRowOf(t, st, want.typ, want.arn); !ok {
			t.Errorf("%s %s missing", want.typ, want.arn)
		}
	}
}

// ListComputeNodeGroups and ListQueues model ResourceNotFoundException for a
// cluster deleted after it was listed: on a cluster's first page it skips
// the cluster silently; after a page it keeps that page and warns once under
// the op. Neither is an error, and the other cluster loses nothing.
func TestScanPCS_ClusterGoneSkipsItsChildren(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	warns := countWarnings(st)
	queueWarns := countOpWarnings(st, "pcs:ListQueues")
	calls := newPCSCalls()
	h := pcsHandlers(calls)
	nodeGroups := h["ListComputeNodeGroups"]
	h["ListComputeNodeGroups"] = func(in any) (any, error) {
		if pcsClusterOf(in) == "pcs_t" {
			return nil, crNotFound
		}
		return nodeGroups(in)
	}
	h["ListQueues"] = func(in any) (any, error) {
		if pcsClusterOf(in) == "pcs_b" && bacField[*string](in, "NextToken") != nil {
			return nil, crNotFound
		}
		next := sdkaws.String("p2")
		if pcsClusterOf(in) == "pcs_t" {
			next = nil
		}
		return pcsChildOut("ListQueues", pcsClusterOf(in), next), nil
	}

	total, _, err := pcsScan(context.Background(), t, st, h)
	if err != nil {
		t.Fatalf("err = %v; want nil", err)
	}
	// 6 rows less cng_t.
	if stored := countStoredRows(t, st); total != 5 || stored != 5 {
		t.Errorf("total, stored = %d, %d; want 5", total, stored)
	}
	if *warns != 1 || *queueWarns != 1 {
		t.Errorf("warnings = %d (pcs:ListQueues: %d); want 1 under pcs:ListQueues", *warns, *queueWarns)
	}
	for _, want := range []struct{ typ, arn string }{
		{TypePCSComputeNodeGroup, pcsTestARN("cluster/pcs_b/computenodegroup/cng_b")},
		{TypePCSQueue, pcsTestARN("cluster/pcs_b/queue/q_b")},
	} {
		if _, ok := pcsRowOf(t, st, want.typ, want.arn); !ok {
			t.Errorf("%s %s missing", want.typ, want.arn)
		}
	}
}

// A cluster listing whose second page fails keeps the first page's cluster,
// which is still read as a parent, and returns the error.
func TestScanPCS_ClusterPageFailureKeepsPage(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	calls := newPCSCalls()
	h := pcsHandlers(calls)
	h["ListClusters"] = failOnCall(2, apiErr("ConflictException", "page 2"), h["ListClusters"])

	total, _, err := pcsScan(context.Background(), t, st, h)
	assertAPICode(t, err, "ConflictException")
	if total != 3 {
		t.Errorf("total = %d; want 3, pcs_t and its children", total)
	}
	if _, ok := pcsRowOf(t, st, TypePCSQueue, pcsTestARN("cluster/pcs_t/queue/q_t")); !ok {
		t.Error("queue of pcs_t, from the page read, missing")
	}
}

// Elements listed without an ARN are not stored or read and are warned about
// once per listing, under a key that is never empty: a child is named by its
// cluster and its own name, the first cluster in listing order (pcs_t) as
// each child listing returns the element for both. A cluster without an Id
// is stored, warned about, and its children are not listed.
func TestScanPCS_UnstorableItemsWarn(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name, op, key string
		out           any
		stored        int
	}{
		{"cluster without ARN", "ListClusters", "first cluster-x", &pcs.ListClustersOutput{Clusters: []pcstypes.ClusterSummary{{Id: sdkaws.String("pcs_x"), Name: sdkaws.String("cluster-x")}, {}}}, 0},
		{"cluster without id", "ListClusters", "first " + pcsTestARN("cluster/pcs_x"), &pcs.ListClustersOutput{Clusters: []pcstypes.ClusterSummary{{Arn: sdkaws.String(pcsTestARN("cluster/pcs_x")), Name: sdkaws.String("x")}}}, 1},
		{"compute node group without ARN", "ListComputeNodeGroups", "first pcs_t/nodes-x", &pcs.ListComputeNodeGroupsOutput{ComputeNodeGroups: []pcstypes.ComputeNodeGroupSummary{{Name: sdkaws.String("nodes-x")}}}, 4},
		{"queue with an id but no ARN or name", "ListQueues", "first pcs_t/(an element with no identifier)", &pcs.ListQueuesOutput{Queues: []pcstypes.QueueSummary{{Id: sdkaws.String("q_x")}}}, 4},
	} {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := countWarnings(st)
			warnings := bacWarnings(st, "pcs:"+c.op)
			calls := newPCSCalls()
			h := pcsHandlers(calls)
			h[c.op] = func(any) (any, error) { return c.out, nil }

			total, _, err := pcsScan(context.Background(), t, st, h)
			if err != nil {
				t.Fatalf("err = %v; want nil", err)
			}
			if *warns != 1 || len(*warnings) != 1 || !strings.HasSuffix((*warnings)[0].Message, c.key) {
				t.Errorf("warnings = %d, under the op %v; want one ending %q", *warns, *warnings, c.key)
			}
			if total != c.stored {
				t.Errorf("total = %d; want %d", total, c.stored)
			}
			if c.op == "ListClusters" && calls.count("ListQueues") != 0 {
				t.Errorf("ListQueues called %d times with no cluster to read", calls.count("ListQueues"))
			}
		})
	}
}

// A repeated token ends the listing as an error, keeping its pages.
func TestScanPCS_RepeatedTokenKeepsPages(t *testing.T) {
	t.Parallel()
	for _, c := range []struct{ op, typ, arn string }{
		{"ListClusters", TypePCSCluster, pcsTestARN("cluster/pcs_t")},
		{"ListComputeNodeGroups", TypePCSComputeNodeGroup, pcsTestARN("cluster/pcs_t/computenodegroup/cng_t")},
		{"ListQueues", TypePCSQueue, pcsTestARN("cluster/pcs_t/queue/q_t")},
	} {
		t.Run(c.op, func(t *testing.T) {
			st := newTestStore(t)
			h := pcsHandlers(newPCSCalls())
			if c.op == "ListClusters" {
				h[c.op] = func(any) (any, error) {
					return &pcs.ListClustersOutput{Clusters: []pcstypes.ClusterSummary{pcsCluster("t")}, NextToken: sdkaws.String("same")}, nil
				}
			} else {
				h[c.op] = func(in any) (any, error) { return pcsChildOut(c.op, pcsClusterOf(in), sdkaws.String("same")), nil }
			}

			_, _, err := pcsScan(context.Background(), t, st, h)
			if !errors.Is(err, errRepeatedNextToken) {
				t.Fatalf("err = %v; want errRepeatedNextToken", err)
			}
			if _, ok := pcsRowOf(t, st, c.typ, c.arn); !ok {
				t.Errorf("%s from the pages read missing", c.arn)
			}
		})
	}
}

// Each child listing sends a cluster's second request the token its first
// page returned and stores the second page's element.
func TestScanPCS_ChildListingsReadEveryPage(t *testing.T) {
	t.Parallel()
	for _, op := range pcsChildOps {
		t.Run(op, func(t *testing.T) {
			st := newTestStore(t)
			calls := newPCSCalls()
			h := pcsHandlers(calls)
			h["ListClusters"] = calls.answer("ListClusters", func(any) any {
				return &pcs.ListClustersOutput{Clusters: []pcstypes.ClusterSummary{pcsCluster("t")}}
			})
			page2 := pcsSecondPage(op)
			var tokens []string
			h[op] = calls.answer(op, func(in any) any {
				token := bacField[*string](in, "NextToken")
				calls.mu.Lock()
				tokens = append(tokens, cmp.Or(sv(token), "(none)"))
				calls.mu.Unlock()
				if token == nil {
					return pcsChildOut(op, "pcs_t", sdkaws.String("p2"))
				}
				return page2.out
			})

			if _, _, err := pcsScan(context.Background(), t, st, h); err != nil {
				t.Fatalf("scan: %v", err)
			}
			if want := []string{"(none)", "p2"}; !slices.Equal(tokens, want) {
				t.Errorf("%s NextTokens = %v; want %v", op, tokens, want)
			}
			if _, ok := pcsRowOf(t, st, page2.typ, page2.arn); !ok {
				t.Errorf("%s %s from page 2 missing", page2.typ, page2.arn)
			}
		})
	}
}

// pcsPage is a listing's page out holding one element, of type typ and ARN
// arn.
type pcsPage struct {
	typ, arn string
	out      any
}

// pcsSecondPage is op's second page for cluster pcs_t, an element other than
// the first page's.
func pcsSecondPage(op string) (page pcsPage) {
	if op == "ListComputeNodeGroups" {
		g := pcsNodeGroup("pcs_t")
		g.Arn, g.Id = sdkaws.String(pcsTestARN("cluster/pcs_t/computenodegroup/cng_p2")), sdkaws.String("cng_p2")
		page.typ, page.arn, page.out = TypePCSComputeNodeGroup, sv(g.Arn), &pcs.ListComputeNodeGroupsOutput{ComputeNodeGroups: []pcstypes.ComputeNodeGroupSummary{g}}
		return page
	}
	q := pcsQueue("pcs_t")
	q.Arn, q.Id = sdkaws.String(pcsTestARN("cluster/pcs_t/queue/q_p2")), sdkaws.String("q_p2")
	page.typ, page.arn, page.out = TypePCSQueue, sv(q.Arn), &pcs.ListQueuesOutput{Queues: []pcstypes.QueueSummary{q}}
	return page
}

// Once ctx ends no further phase starts, and the stop is reported once.
func TestScanPCS_ContextEndStopsOnce(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	calls := newPCSCalls()
	h := pcsHandlers(calls)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	h["ListComputeNodeGroups"] = func(any) (any, error) {
		cancel()
		return nil, context.Canceled
	}

	_, _, err := pcsScan(ctx, t, st, h)
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
	if n := calls.count("ListQueues"); n != 0 {
		t.Errorf("ListQueues called %d times after ctx ended", n)
	}
}

// A region without PCS (NXDOMAIN on the cluster listing, nothing stored) is
// unavailable and makes no child listing. An NXDOMAIN after rows were stored
// is a failure of this scan: the counts are kept.
func TestScanPCS_Unreachable(t *testing.T) {
	t.Parallel()
	nx := &net.DNSError{Err: "no such host", Name: "pcs.us-east-1.amazonaws.com", IsNotFound: true}
	t.Run("endpoint absent", func(t *testing.T) {
		st := newTestStore(t)
		calls := newPCSCalls()
		h := pcsHandlers(calls)
		h["ListClusters"] = func(any) (any, error) { return nil, nx }
		total, _, err := pcsScan(context.Background(), t, st, h)
		assertGlueOutcome(t, err, outcomeUnavailable)
		if total != 0 || calls.count("ListComputeNodeGroups") != 0 {
			t.Errorf("total, ListComputeNodeGroups calls = %d, %d; want 0, 0", total, calls.count("ListComputeNodeGroups"))
		}
	})
	t.Run("tag read after rows were listed", func(t *testing.T) {
		st := newTestStore(t)
		calls := newPCSCalls()
		calls.errs[pcsTestARN("cluster/pcs_t/queue/q_t")] = nx
		total, _, err := pcsScan(context.Background(), t, st, pcsHandlers(calls))
		assertGlueOutcome(t, err, outcomeError)
		if total != 6 {
			t.Errorf("total = %d; want 6", total)
		}
	})
}

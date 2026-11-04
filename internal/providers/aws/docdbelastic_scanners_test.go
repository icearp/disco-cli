package aws

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"slices"
	"sync"
	"testing"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/docdbelastic"
	debtypes "github.com/aws/aws-sdk-go-v2/service/docdbelastic/types"
	"github.com/icearp/disco-cli/store"
)

var (
	debDenied     = apiErr("AccessDeniedException", "User: arn:aws:iam::123456789012:user/t is not authorized to perform: docdb-elastic:ListTagsForResource")
	debValidation = apiErr("ValidationException", "bad")
	debGone       = apiErr("ResourceNotFoundException", "not found")
	debThrottle   = apiErr("ThrottlingException", "slow down")
	debNXDOMAIN   = &net.DNSError{Err: "no such host", Name: "docdb-elastic.us-east-1.amazonaws.com", IsNotFound: true}
)

func debARN(kind, id string) string {
	return "arn:aws:docdb-elastic:us-east-1:" + testAccountID + ":" + kind + "/" + id
}

var (
	debCluster1  = debARN("cluster", "c1")
	debCluster2  = debARN("cluster", "c2")
	debSnapshot1 = debARN("cluster-snapshot", "s1")
	debSnapshot2 = debARN("cluster-snapshot", "s2")
)

// debTotal is the default scan's rows: two clusters and two snapshots.
const debTotal = 4

// debStub answers every docdb-elastic op of the default scan: clusters c1
// (page one) and c2 (page two), snapshots s1 (page one) and s2 (page two),
// a Get body for each, and tags on c1 and s1. getErrs and tagErrs, keyed by
// ARN and fixed before the scan, replace a Get or a tag read with an error.
// Every input is recorded.
type debStub struct {
	tags    map[string]map[string]string
	getErrs map[string]error
	tagErrs map[string]error

	mu     sync.Mutex
	inputs map[string][]any
}

func newDebStub() *debStub {
	return &debStub{
		tags: map[string]map[string]string{
			debCluster1:  {"env": "prod"},
			debSnapshot1: {"backup": "daily"},
		},
		getErrs: map[string]error{},
		tagErrs: map[string]error{},
		inputs:  map[string][]any{},
	}
}

func (s *debStub) debRecord(op string, in any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.inputs[op] = append(s.inputs[op], in)
}

func (s *debStub) debCalls(op string) []any {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.inputs[op])
}

// debSentARNs is the sorted ARNs every call of op was sent, read from the
// input field named field.
func (s *debStub) debSentARNs(op, field string) []string {
	var arns []string
	for _, in := range s.debCalls(op) {
		arns = append(arns, sv(bacField[*string](in, field)))
	}
	slices.Sort(arns)
	return arns
}

func (s *debStub) debHandlers() map[string]func(any) (any, error) {
	return map[string]func(any) (any, error){
		"ListClusters": func(in any) (any, error) {
			s.debRecord("ListClusters", in)
			if in.(*docdbelastic.ListClustersInput).NextToken == nil {
				return &docdbelastic.ListClustersOutput{Clusters: []debtypes.ClusterInList{
					{ClusterArn: sdkaws.String(debCluster1), ClusterName: sdkaws.String("c1"), Status: debtypes.StatusActive},
				}, NextToken: sdkaws.String("p2")}, nil
			}
			return &docdbelastic.ListClustersOutput{Clusters: []debtypes.ClusterInList{
				{ClusterArn: sdkaws.String(debCluster2), ClusterName: sdkaws.String("c2"), Status: debtypes.StatusCreating},
			}}, nil
		},
		"ListClusterSnapshots": func(in any) (any, error) {
			s.debRecord("ListClusterSnapshots", in)
			if in.(*docdbelastic.ListClusterSnapshotsInput).NextToken == nil {
				return &docdbelastic.ListClusterSnapshotsOutput{Snapshots: []debtypes.ClusterSnapshotInList{
					{SnapshotArn: sdkaws.String(debSnapshot1), SnapshotName: sdkaws.String("s1"), ClusterArn: sdkaws.String(debCluster1), Status: debtypes.StatusActive, SnapshotCreationTime: sdkaws.String("2026-01-01T00:00:00Z")},
				}, NextToken: sdkaws.String("p2")}, nil
			}
			return &docdbelastic.ListClusterSnapshotsOutput{Snapshots: []debtypes.ClusterSnapshotInList{
				{SnapshotArn: sdkaws.String(debSnapshot2), SnapshotName: sdkaws.String("s2"), ClusterArn: sdkaws.String(debCluster2), Status: debtypes.StatusCreating, SnapshotCreationTime: sdkaws.String("2026-02-01T00:00:00Z")},
			}}, nil
		},
		"GetCluster": func(in any) (any, error) {
			s.debRecord("GetCluster", in)
			arn := sv(in.(*docdbelastic.GetClusterInput).ClusterArn)
			if err := s.getErrs[arn]; err != nil {
				return nil, err
			}
			return &docdbelastic.GetClusterOutput{Cluster: &debtypes.Cluster{
				ClusterArn: sdkaws.String(arn), KmsKeyId: sdkaws.String("key-" + arn), SubnetIds: []string{"subnet-1"},
			}}, nil
		},
		"GetClusterSnapshot": func(in any) (any, error) {
			s.debRecord("GetClusterSnapshot", in)
			arn := sv(in.(*docdbelastic.GetClusterSnapshotInput).SnapshotArn)
			if err := s.getErrs[arn]; err != nil {
				return nil, err
			}
			return &docdbelastic.GetClusterSnapshotOutput{Snapshot: &debtypes.ClusterSnapshot{
				SnapshotArn: sdkaws.String(arn), KmsKeyId: sdkaws.String("key-" + arn), SnapshotType: debtypes.SnapshotTypeAutomated,
			}}, nil
		},
		"ListTagsForResource": func(in any) (any, error) {
			s.debRecord("ListTagsForResource", in)
			arn := sv(in.(*docdbelastic.ListTagsForResourceInput).ResourceArn)
			if err := s.tagErrs[arn]; err != nil {
				return nil, err
			}
			return &docdbelastic.ListTagsForResourceOutput{Tags: s.tags[arn]}, nil
		},
	}
}

func debScan(ctx context.Context, st *store.Store, handlers map[string]func(any) (any, error)) (int, int, error) {
	acct := newTestAccount(testAccountID)
	acct.cfg = opStubConfig(handlers, debDenied)
	return scanDocDBElastic(ctx, acct, testRegion, st, testScanID)
}

// debRows is every stored docdb-elastic row keyed by NativeID.
func debRows(t *testing.T, st *store.Store) map[string]store.Resource {
	t.Helper()
	rows := rowsByNativeID(t, st, TypeDocDBElasticCluster)
	for id, r := range rowsByNativeID(t, st, TypeDocDBElasticClusterSnapshot) {
		rows[id] = r
	}
	return rows
}

func debAttrs(t *testing.T, r store.Resource) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(r.AttributesJSON), &m); err != nil {
		t.Fatalf("%s attributes: %v", r.NativeID, err)
	}
	return m
}

func debAllARNs() []string {
	arns := []string{debCluster1, debCluster2, debSnapshot1, debSnapshot2}
	slices.Sort(arns)
	return arns
}

func TestScanDocDBElastic_StoresTagsOfEachType(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	stub := newDebStub()

	total, inserted, err := debScan(context.Background(), st, stub.debHandlers())
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != debTotal || inserted != debTotal {
		t.Errorf("total, inserted = %d, %d; want %d, %d", total, inserted, debTotal, debTotal)
	}
	rows := debRows(t, st)
	for _, w := range []struct {
		arn, typ, name, status, bodyKey string
		bodyVal                         any
		tags                            map[string]string
	}{
		{debCluster1, TypeDocDBElasticCluster, "c1", "ACTIVE", "SubnetIds", []any{"subnet-1"}, map[string]string{"env": "prod"}},
		{debCluster2, TypeDocDBElasticCluster, "c2", "CREATING", "SubnetIds", []any{"subnet-1"}, nil},
		{debSnapshot1, TypeDocDBElasticClusterSnapshot, "s1", "ACTIVE", "SnapshotType", "AUTOMATED", map[string]string{"backup": "daily"}},
		{debSnapshot2, TypeDocDBElasticClusterSnapshot, "s2", "CREATING", "SnapshotType", "AUTOMATED", nil},
	} {
		r, ok := rows[w.arn]
		if !ok {
			t.Errorf("%s not stored", w.arn)
			continue
		}
		if r.Type != w.typ || sv(r.Name) != w.name || sv(r.Status) != w.status || sv(r.Region) != testRegion || sv(r.AccountName) != "Test Account" {
			t.Errorf("%s: type %q name %q status %q region %q account name %q; want %q %q %q %q %q",
				w.arn, r.Type, sv(r.Name), sv(r.Status), sv(r.Region), sv(r.AccountName), w.typ, w.name, w.status, testRegion, "Test Account")
		}
		if r.AccountID != testAccountID || r.ManagedByProvider {
			t.Errorf("%s: account %q managed %v; want %q false", w.arn, r.AccountID, r.ManagedByProvider, testAccountID)
		}
		attrs := debAttrs(t, r)
		if got := attrs["KmsKeyId"]; got != "key-"+w.arn {
			t.Errorf("%s attributes KmsKeyId = %v; want the Get body's", w.arn, got)
		}
		gotJSON, _ := json.Marshal(attrs[w.bodyKey])
		wantJSON, _ := json.Marshal(w.bodyVal)
		if string(gotJSON) != string(wantJSON) {
			t.Errorf("%s attributes %s = %s; want %s", w.arn, w.bodyKey, gotJSON, wantJSON)
		}
		if w.tags == nil {
			if r.TagsJSON != nil {
				t.Errorf("%s TagsJSON = %s; want nil", w.arn, *r.TagsJSON)
			}
			continue
		}
		assertTagsMap(t, r.TagsJSON, w.tags)
	}
	if len(rows) != debTotal {
		t.Errorf("stored %d rows; want %d", len(rows), debTotal)
	}
	if got := stub.debSentARNs("ListTagsForResource", "ResourceArn"); !slices.Equal(got, debAllARNs()) {
		t.Errorf("ListTagsForResource ARNs = %v; want %v", got, debAllARNs())
	}
	if got, want := stub.debSentARNs("GetCluster", "ClusterArn"), []string{debCluster1, debCluster2}; !slices.Equal(got, want) {
		t.Errorf("GetCluster ARNs = %v; want %v", got, want)
	}
	if got, want := stub.debSentARNs("GetClusterSnapshot", "SnapshotArn"), []string{debSnapshot1, debSnapshot2}; !slices.Equal(got, want) {
		t.Errorf("GetClusterSnapshot ARNs = %v; want %v", got, want)
	}
	for _, op := range []string{"ListClusters", "ListClusterSnapshots"} {
		calls := stub.debCalls(op)
		if len(calls) != 2 {
			t.Fatalf("%s called %d times; want 2", op, len(calls))
		}
		for i, wantToken := range []string{"", "p2"} {
			if got := sv(bacField[*string](calls[i], "NextToken")); got != wantToken {
				t.Errorf("%s call %d NextToken = %q; want %q", op, i+1, got, wantToken)
			}
			if got := bacField[*int32](calls[i], "MaxResults"); got == nil || *got != 100 {
				t.Errorf("%s call %d MaxResults = %v; want 100", op, i+1, got)
			}
		}
	}
	for i, in := range stub.debCalls("ListClusterSnapshots") {
		if got := in.(*docdbelastic.ListClusterSnapshotsInput).SnapshotType; got != nil {
			t.Errorf("ListClusterSnapshots call %d SnapshotType = %q; want none", i+1, *got)
		}
	}
}

func TestScanDocDBElastic_RescanInsertsNone(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	if _, _, err := debScan(context.Background(), st, newDebStub().debHandlers()); err != nil {
		t.Fatalf("first scan: %v", err)
	}
	total, inserted, err := debScan(context.Background(), st, newDebStub().debHandlers())
	if err != nil {
		t.Fatalf("rescan: %v", err)
	}
	if total != debTotal || inserted != 0 {
		t.Errorf("rescan total, inserted = %d, %d; want %d, 0", total, inserted, debTotal)
	}
}

func TestScanDocDBElastic_NoResources(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	stub := newDebStub()
	handlers := stub.debHandlers()
	handlers["ListClusters"] = func(any) (any, error) { return &docdbelastic.ListClustersOutput{}, nil }
	handlers["ListClusterSnapshots"] = func(any) (any, error) { return &docdbelastic.ListClusterSnapshotsOutput{}, nil }

	total, inserted, err := debScan(context.Background(), st, handlers)
	if err != nil || total != 0 || inserted != 0 {
		t.Fatalf("scan = %d, %d, %v; want 0, 0, nil", total, inserted, err)
	}
	if rows := debRows(t, st); len(rows) != 0 {
		t.Errorf("stored %d rows; want none", len(rows))
	}
	for _, op := range []string{"GetCluster", "GetClusterSnapshot", "ListTagsForResource"} {
		if n := len(stub.debCalls(op)); n != 0 {
			t.Errorf("%s called %d times; want 0", op, n)
		}
	}
}

// Each tag-read outcome, for a cluster and a snapshot: a not-found drops the
// row; a denial keeps it untagged with one warning; a throttle keeps it and
// leaves the scan partial; any other error keeps it and is returned.
func TestScanDocDBElastic_TagReadFailures(t *testing.T) {
	t.Parallel()
	for _, arn := range []string{debCluster1, debSnapshot1} {
		for _, c := range []struct {
			name    string
			err     error
			kept    bool
			outcome serviceOutcome
			code    string
			warns   int
		}{
			{"not found", debGone, false, 0, "", 0},
			{"denied", debDenied, true, 0, "", 1},
			{"throttled", debThrottle, true, outcomeTransient, "ThrottlingException", 0},
			{"other", debValidation, true, outcomeError, "ValidationException", 0},
		} {
			t.Run(arn+"/"+c.name, func(t *testing.T) {
				t.Parallel()
				st := newTestStore(t)
				warns := rhWarnRecorder(st)
				stub := newDebStub()
				stub.tagErrs[arn] = c.err

				total, _, err := debScan(context.Background(), st, stub.debHandlers())
				if c.code == "" {
					if err != nil {
						t.Fatalf("scan: %v", err)
					}
				} else {
					assertGlueOutcome(t, err, c.outcome)
					assertAPICode(t, err, c.code)
				}
				r, stored := debRows(t, st)[arn]
				if stored != c.kept {
					t.Fatalf("%s stored = %v; want %v", arn, stored, c.kept)
				}
				wantTotal := debTotal
				if !c.kept {
					wantTotal--
				}
				if total != wantTotal {
					t.Errorf("total = %d; want %d", total, wantTotal)
				}
				if stored && r.TagsJSON != nil {
					t.Errorf("%s TagsJSON = %s; want nil", arn, *r.TagsJSON)
				}
				var got []store.ScanWarning
				for _, w := range warns() {
					if w.Service == docDBElasticTagsOp {
						got = append(got, w)
					}
				}
				if len(got) != c.warns {
					t.Fatalf("%s warnings = %v; want %d", docDBElasticTagsOp, got, c.warns)
				}
				if c.warns == 1 && got[0].Scope != testAccountID+"/"+testRegion {
					t.Errorf("warning scope = %q; want %q", got[0].Scope, testAccountID+"/"+testRegion)
				}
			})
		}
	}
}

func TestScanDocDBElastic_TagDenialsWarnOncePerScan(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	warns := countOpWarnings(st, docDBElasticTagsOp)
	stub := newDebStub()
	stub.tagErrs[debCluster2] = debDenied
	stub.tagErrs[debSnapshot1] = debDenied

	if _, _, err := debScan(context.Background(), st, stub.debHandlers()); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if *warns != 1 {
		t.Errorf("%s warnings = %d; want 1 for both phases", docDBElasticTagsOp, *warns)
	}
}

// A throttled tag read beside a hard tag-read error is demoted to a warning,
// so the scan reports the hard error rather than reading as transient.
func TestScanDocDBElastic_TagThrottleDemotedBesideHardError(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	warns := countOpWarnings(st, docDBElasticTagsOp)
	stub := newDebStub()
	stub.tagErrs[debCluster1] = debThrottle
	stub.tagErrs[debSnapshot2] = debValidation

	_, _, err := debScan(context.Background(), st, stub.debHandlers())
	assertGlueOutcome(t, err, outcomeError)
	assertAPICode(t, err, "ValidationException")
	if *warns != 1 {
		t.Errorf("%s warnings = %d; want 1 (the demoted throttle)", docDBElasticTagsOp, *warns)
	}
}

// Each Get outcome, for a cluster and a snapshot: a not-found drops the row
// unread; any other failure keeps the list element as the row, its tags
// still read: a denial warns once, a throttle leaves the scan partial, any
// other error and a body-less response are returned.
func TestScanDocDBElastic_GetFailures(t *testing.T) {
	t.Parallel()
	for _, k := range []struct {
		arn, getOp, label, elementKey string
		noBody                        func(map[string]func(any) (any, error))
		noBodyErr                     error
	}{
		{debCluster1, "GetCluster", docDBElasticGetClusterOp, "ClusterName", func(h map[string]func(any) (any, error)) {
			h["GetCluster"] = func(any) (any, error) { return &docdbelastic.GetClusterOutput{}, nil }
		}, errDocDBElasticNoCluster},
		{debSnapshot1, "GetClusterSnapshot", docDBElasticGetSnapshotOp, "SnapshotCreationTime", func(h map[string]func(any) (any, error)) {
			h["GetClusterSnapshot"] = func(any) (any, error) { return &docdbelastic.GetClusterSnapshotOutput{}, nil }
		}, errDocDBElasticNoSnapshot},
	} {
		for _, c := range []struct {
			name     string
			err      error
			noBody   bool
			kept     bool
			outcome  serviceOutcome
			code     string
			sentinel error
			warns    int
		}{
			{name: "not found", err: debGone},
			{name: "denied", err: debDenied, kept: true, warns: 1},
			{name: "throttled", err: debThrottle, kept: true, outcome: outcomeTransient, code: "ThrottlingException"},
			{name: "other", err: debValidation, kept: true, outcome: outcomeError, code: "ValidationException"},
			{name: "no body", noBody: true, kept: true, outcome: outcomeError, sentinel: k.noBodyErr},
		} {
			t.Run(k.getOp+"/"+c.name, func(t *testing.T) {
				t.Parallel()
				st := newTestStore(t)
				warns := countOpWarnings(st, k.label)
				stub := newDebStub()
				handlers := stub.debHandlers()
				if c.noBody {
					k.noBody(handlers)
				} else {
					stub.getErrs[k.arn] = c.err
				}

				_, _, err := debScan(context.Background(), st, handlers)
				switch {
				case c.sentinel != nil:
					assertGlueOutcome(t, err, c.outcome)
					if !errors.Is(err, c.sentinel) {
						t.Errorf("err = %v; want %v", err, c.sentinel)
					}
				case c.code != "":
					assertGlueOutcome(t, err, c.outcome)
					assertAPICode(t, err, c.code)
				case err != nil:
					t.Fatalf("scan: %v", err)
				}
				r, stored := debRows(t, st)[k.arn]
				if stored != c.kept {
					t.Fatalf("%s stored = %v; want %v", k.arn, stored, c.kept)
				}
				tagged := slices.Contains(stub.debSentARNs("ListTagsForResource", "ResourceArn"), k.arn)
				if tagged != c.kept {
					t.Errorf("%s tag read = %v; want %v", k.arn, tagged, c.kept)
				}
				if *warns != c.warns {
					t.Errorf("%s warnings = %d; want %d", k.label, *warns, c.warns)
				}
				if !stored {
					return
				}
				attrs := debAttrs(t, r)
				if _, ok := attrs["KmsKeyId"]; ok {
					t.Errorf("%s attributes carry the Get body's KmsKeyId; want the list element", k.arn)
				}
				if _, ok := attrs[k.elementKey]; !ok {
					t.Errorf("%s attributes lack the list element's %s", k.arn, k.elementKey)
				}
				assertTagsMap(t, r.TagsJSON, stub.tags[k.arn])
			})
		}
	}
}

// A listing's failed later page keeps its first page, and its error does not
// stop the other phase.
func TestScanDocDBElastic_ListPageTwoFailureKeepsPagesAndOtherPhase(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		op         string
		kept, lost string
	}{
		{"ListClusters", debCluster1, debCluster2},
		{"ListClusterSnapshots", debSnapshot1, debSnapshot2},
	} {
		t.Run(c.op, func(t *testing.T) {
			t.Parallel()
			st := newTestStore(t)
			stub := newDebStub()
			handlers := stub.debHandlers()
			list := handlers[c.op]
			handlers[c.op] = func(in any) (any, error) {
				if bacField[*string](in, "NextToken") != nil {
					return nil, debValidation
				}
				return list(in)
			}

			total, _, err := debScan(context.Background(), st, handlers)
			assertGlueOutcome(t, err, outcomeError)
			assertAPICode(t, err, "ValidationException")
			if total != debTotal-1 {
				t.Errorf("total = %d; want %d", total, debTotal-1)
			}
			rows := debRows(t, st)
			if _, ok := rows[c.kept]; !ok {
				t.Errorf("%s (page one) not stored", c.kept)
			}
			if _, ok := rows[c.lost]; ok {
				t.Errorf("%s (failed page) stored", c.lost)
			}
			if len(rows) != debTotal-1 {
				t.Errorf("stored %d rows; want %d", len(rows), debTotal-1)
			}
		})
	}
}

// A repeated NextToken ends the listing with errRepeatedNextToken, keeping
// the pages read, and the other phase still runs.
func TestScanDocDBElastic_RepeatedTokenKeepsPages(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	stub := newDebStub()
	handlers := stub.debHandlers()
	list := handlers["ListClusters"]
	handlers["ListClusters"] = func(in any) (any, error) {
		out, err := list(in)
		out.(*docdbelastic.ListClustersOutput).NextToken = sdkaws.String("p2")
		return out, err
	}

	total, _, err := debScan(context.Background(), st, handlers)
	if !errors.Is(err, errRepeatedNextToken) {
		t.Fatalf("err = %v; want errRepeatedNextToken", err)
	}
	if total != debTotal {
		t.Errorf("total = %d; want %d", total, debTotal)
	}
	if n := len(stub.debCalls("ListClusters")); n != 2 {
		t.Errorf("ListClusters called %d times; want 2", n)
	}
}

// An element without an ARN is not stored, has nothing read, and is warned
// about under its listing; the other elements are stored.
func TestScanDocDBElastic_ElementWithoutARN(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	clusterWarns := countOpWarnings(st, docDBElasticListClustersOp)
	snapshotWarns := countOpWarnings(st, docDBElasticListSnapshotsOp)
	stub := newDebStub()
	handlers := stub.debHandlers()
	listClusters, listSnapshots := handlers["ListClusters"], handlers["ListClusterSnapshots"]
	handlers["ListClusters"] = func(in any) (any, error) {
		out, err := listClusters(in)
		o := out.(*docdbelastic.ListClustersOutput)
		o.Clusters = append(o.Clusters, debtypes.ClusterInList{ClusterName: sdkaws.String("orphan")})
		return o, err
	}
	handlers["ListClusterSnapshots"] = func(in any) (any, error) {
		out, err := listSnapshots(in)
		o := out.(*docdbelastic.ListClusterSnapshotsOutput)
		o.Snapshots = append(o.Snapshots, debtypes.ClusterSnapshotInList{SnapshotName: sdkaws.String("orphan")})
		return o, err
	}

	total, _, err := debScan(context.Background(), st, handlers)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != debTotal {
		t.Errorf("total = %d; want %d", total, debTotal)
	}
	if *clusterWarns != 1 || *snapshotWarns != 1 {
		t.Errorf("warnings = %d clusters, %d snapshots; want 1 each", *clusterWarns, *snapshotWarns)
	}
	if got := stub.debSentARNs("ListTagsForResource", "ResourceArn"); !slices.Equal(got, debAllARNs()) {
		t.Errorf("ListTagsForResource ARNs = %v; want %v", got, debAllARNs())
	}
	if n := len(stub.debCalls("GetCluster")) + len(stub.debCalls("GetClusterSnapshot")); n != debTotal {
		t.Errorf("Get calls = %d; want %d (none for the ARN-less elements)", n, debTotal)
	}
	if rows := debRows(t, st); len(rows) != debTotal {
		t.Errorf("stored %d rows; want %d", len(rows), debTotal)
	}
}

// A denied listing warns under its op and the other phase still runs.
func TestScanDocDBElastic_ListDenied(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	warns := countOpWarnings(st, docDBElasticListClustersOp)
	handlers := newDebStub().debHandlers()
	delete(handlers, "ListClusters")

	total, _, err := debScan(context.Background(), st, handlers)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != 2 {
		t.Errorf("total = %d; want the 2 snapshots", total)
	}
	if *warns != 1 {
		t.Errorf("%s warnings = %d; want 1", docDBElasticListClustersOp, *warns)
	}
}

// A failed write stops the scan without a warning about the rows it lost (a
// denied tag read among them), and the snapshots are not listed.
func TestScanDocDBElastic_FailedWriteStopsScan(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	warns := rhWarnRecorder(st)
	refuseStoreWrites(t, st, TypeDocDBElasticCluster)
	stub := newDebStub()
	stub.tagErrs[debCluster1] = debDenied

	_, _, err := debScan(context.Background(), st, stub.debHandlers())
	if !errors.Is(err, store.ErrStoreWrite) {
		t.Fatalf("err = %v; want store.ErrStoreWrite", err)
	}
	if w := warns(); len(w) != 0 {
		t.Errorf("warnings = %v; want none", w)
	}
	if n := len(stub.debCalls("ListClusterSnapshots")); n != 0 {
		t.Errorf("ListClusterSnapshots called %d times after the failed write", n)
	}
	if n := len(stub.debCalls("ListClusters")); n != 1 {
		t.Errorf("ListClusters called %d times; want 1 (no page after the failed write)", n)
	}
}

// Once ctx ends, the stop is one member of the error, although the phase in
// flight saw it, and the snapshots are not listed.
func TestScanDocDBElastic_ContextEndReportedOnce(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	stub := newDebStub()
	handlers := stub.debHandlers()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	list := handlers["ListClusters"]
	handlers["ListClusters"] = func(in any) (any, error) {
		if bacField[*string](in, "NextToken") != nil {
			cancel()
			return nil, context.Canceled
		}
		return list(in)
	}

	_, _, err := debScan(ctx, st, handlers)
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
	if n := len(stub.debCalls("ListClusterSnapshots")); n != 0 {
		t.Errorf("ListClusterSnapshots called %d times after ctx ended", n)
	}
	if _, ok := debRows(t, st)[debCluster1]; !ok {
		t.Errorf("%s (page one) not stored", debCluster1)
	}
}

// An NXDOMAIN after rows were stored is a failure of this scan, not a region
// gap: the counts are kept. With nothing stored it stays region-unavailable.
func TestScanDocDBElastic_NXDOMAIN(t *testing.T) {
	t.Parallel()
	t.Run("snapshots after clusters stored", func(t *testing.T) {
		t.Parallel()
		st := newTestStore(t)
		handlers := newDebStub().debHandlers()
		handlers["ListClusterSnapshots"] = func(any) (any, error) { return nil, debNXDOMAIN }
		total, _, err := debScan(context.Background(), st, handlers)
		assertGlueOutcome(t, err, outcomeError)
		if total != 2 {
			t.Errorf("total = %d; want the 2 clusters counted", total)
		}
	})
	t.Run("tag read after the listing", func(t *testing.T) {
		t.Parallel()
		st := newTestStore(t)
		stub := newDebStub()
		stub.tagErrs[debSnapshot2] = debNXDOMAIN
		_, _, err := debScan(context.Background(), st, stub.debHandlers())
		assertGlueOutcome(t, err, outcomeError)
	})
	t.Run("nothing stored", func(t *testing.T) {
		t.Parallel()
		st := newTestStore(t)
		warns := rhWarnRecorder(st)
		acct := newTestAccount(testAccountID)
		acct.cfg = opStubConfig(nil, debNXDOMAIN)
		_, _, err := scanDocDBElastic(context.Background(), acct, testRegion, st, testScanID)
		assertGlueOutcome(t, err, outcomeUnavailable)
		if w := warns(); len(w) != 0 {
			t.Errorf("warnings = %v; want none", w)
		}
	})
}

// A cn or gov listing's ARN reaches GetCluster and ListTagsForResource
// unchanged.
func TestScanDocDBElastic_PartitionARNsReachTagOp(t *testing.T) {
	t.Parallel()
	for _, p := range []string{"aws-us-gov", "aws-cn"} {
		t.Run(p, func(t *testing.T) {
			t.Parallel()
			st := newTestStore(t)
			acct := partitionAccount(p)
			region := acct.Regions[0]
			arn := "arn:" + p + ":docdb-elastic:" + region + ":" + testAccountID + ":cluster/c-" + p
			snapARN := "arn:" + p + ":docdb-elastic:" + region + ":" + testAccountID + ":cluster-snapshot/s-" + p
			stub := newDebStub()
			stub.tags[arn] = map[string]string{"env": "prod"}
			stub.tags[snapARN] = map[string]string{"backup": "daily"}
			handlers := stub.debHandlers()
			handlers["ListClusters"] = func(in any) (any, error) {
				stub.debRecord("ListClusters", in)
				return &docdbelastic.ListClustersOutput{Clusters: []debtypes.ClusterInList{{ClusterArn: sdkaws.String(arn), ClusterName: sdkaws.String("c"), Status: debtypes.StatusActive}}}, nil
			}
			handlers["ListClusterSnapshots"] = func(in any) (any, error) {
				stub.debRecord("ListClusterSnapshots", in)
				return &docdbelastic.ListClusterSnapshotsOutput{Snapshots: []debtypes.ClusterSnapshotInList{{SnapshotArn: sdkaws.String(snapARN), SnapshotName: sdkaws.String("s"), ClusterArn: sdkaws.String(arn), Status: debtypes.StatusActive}}}, nil
			}
			acct.cfg = opStubConfig(handlers, debDenied)

			if _, _, err := scanDocDBElastic(context.Background(), acct, region, st, testScanID); err != nil {
				t.Fatalf("scan: %v", err)
			}
			wantTagARNs := []string{arn, snapARN}
			slices.Sort(wantTagARNs)
			if got := stub.debSentARNs("ListTagsForResource", "ResourceArn"); !slices.Equal(got, wantTagARNs) {
				t.Errorf("ListTagsForResource ARNs = %v; want %v", got, wantTagARNs)
			}
			if got := stub.debSentARNs("GetCluster", "ClusterArn"); !slices.Equal(got, []string{arn}) {
				t.Errorf("GetCluster ARNs = %v; want [%s]", got, arn)
			}
			if got := stub.debSentARNs("GetClusterSnapshot", "SnapshotArn"); !slices.Equal(got, []string{snapARN}) {
				t.Errorf("GetClusterSnapshot ARNs = %v; want [%s]", got, snapARN)
			}
			rows := debRows(t, st)
			for id, wantTags := range map[string]map[string]string{arn: {"env": "prod"}, snapARN: {"backup": "daily"}} {
				r, ok := rows[id]
				if !ok {
					t.Errorf("%s not stored", id)
					continue
				}
				if sv(r.Region) != region {
					t.Errorf("%s Region = %q; want %q", id, sv(r.Region), region)
				}
				assertTagsMap(t, r.TagsJSON, wantTags)
			}
			if len(rows) != 2 {
				t.Errorf("stored %d rows; want 2", len(rows))
			}
		})
	}
}

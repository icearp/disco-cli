package aws

import (
	"context"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/dsql"
	dsqltypes "github.com/aws/aws-sdk-go-v2/service/dsql/types"
	"github.com/icearp/disco-cli/store"
)

const (
	dsqlC1ARN = "arn:aws:dsql:us-east-1:123456789012:cluster/c1"
	dsqlC2ARN = "arn:aws:dsql:us-east-1:123456789012:cluster/c2"
)

var errDSQLUnexpectedOp = errors.New("unexpected op")

// dsqlStub answers ListClusters with clusters (one page), GetCluster by
// getCluster, and ListStreams with one stream per cluster, recording which
// clusters ListStreams was called for.
type dsqlStub struct {
	clusters   []dsqltypes.ClusterSummary
	getCluster func(id string) (any, error)
	listStream func(id string) (any, error)

	mu      sync.Mutex
	streams []string
}

func (s *dsqlStub) handlers(t *testing.T) map[string]func(in any) (any, error) {
	return map[string]func(in any) (any, error){
		"ListClusters": func(in any) (any, error) {
			req := in.(*dsql.ListClustersInput)
			if req.NextToken != nil || req.MaxResults == nil || *req.MaxResults != 100 {
				t.Errorf("ListClusters input = token %v, MaxResults %v; want no token, 100", req.NextToken, req.MaxResults)
			}
			return &dsql.ListClustersOutput{Clusters: s.clusters}, nil
		},
		"GetCluster": func(in any) (any, error) {
			return s.getCluster(sv(in.(*dsql.GetClusterInput).Identifier))
		},
		"ListStreams": func(in any) (any, error) {
			id := sv(in.(*dsql.ListStreamsInput).ClusterIdentifier)
			s.mu.Lock()
			s.streams = append(s.streams, id)
			s.mu.Unlock()
			if s.listStream != nil {
				return s.listStream(id)
			}
			return dsqlStreamPage(id), nil
		},
	}
}

func dsqlStreamPage(clusterID string) *dsql.ListStreamsOutput {
	return &dsql.ListStreamsOutput{Streams: []dsqltypes.StreamSummary{{
		Arn: ptrStr("arn:aws:dsql:us-east-1:123456789012:cluster/" + clusterID + "/stream/s1"), ClusterIdentifier: ptrStr(clusterID),
		StreamIdentifier: ptrStr("s1"), Status: dsqltypes.StreamStatusActive,
	}}}
}

func dsqlTwoClusters() []dsqltypes.ClusterSummary {
	return []dsqltypes.ClusterSummary{
		{Arn: ptrStr(dsqlC1ARN), Identifier: ptrStr("c1")},
		{Arn: ptrStr(dsqlC2ARN), Identifier: ptrStr("c2")},
	}
}

func runDSQL(t *testing.T, st *store.Store, s *dsqlStub) (int, int, error) {
	t.Helper()
	acct := newTestAccount(testAccountID)
	acct.cfg = opStubConfig(s.handlers(t), errDSQLUnexpectedOp)
	return scanDSQL(context.Background(), acct, testRegion, st, testScanID)
}

// TestScanDSQL_ClusterRowFromGetCluster pins a cluster row built from its
// GetCluster body, tags included, beside an untagged cluster, and that each
// stored cluster's streams are listed.
func TestScanDSQL_ClusterRowFromGetCluster(t *testing.T) {
	st := newTestStore(t)
	s := &dsqlStub{clusters: dsqlTwoClusters(), getCluster: func(id string) (any, error) {
		out := &dsql.GetClusterOutput{
			Arn: ptrStr("arn:aws:dsql:us-east-1:123456789012:cluster/" + id), Identifier: ptrStr(id),
			Status: dsqltypes.ClusterStatusActive,
		}
		if id == "c1" {
			out.Tags = map[string]string{"team": "db"}
		}
		return out, nil
	}}
	total, inserted, err := runDSQL(t, st, s)
	if err != nil {
		t.Fatalf("scanDSQL: %v", err)
	}
	if total != 4 || inserted != 4 {
		t.Errorf("total, inserted = %d, %d; want 4, 4 (2 clusters, 2 streams)", total, inserted)
	}
	rows := rowsByNativeID(t, st, TypeDSQLCluster)
	c1 := rows[dsqlC1ARN]
	if sv(c1.Name) != "c1" || sv(c1.Status) != "ACTIVE" {
		t.Errorf("c1 Name, Status = %q, %q; want c1, ACTIVE", sv(c1.Name), sv(c1.Status))
	}
	assertJSONEqual(t, "c1 attributes", c1.AttributesJSON, `{"Arn":"`+dsqlC1ARN+`","CreationTime":null,
		"DeletionProtectionEnabled":null,"Identifier":"c1","Status":"ACTIVE","EncryptionDetails":null,
		"Endpoint":null,"MultiRegionProperties":null,"Tags":{"team":"db"}}`)
	assertTagsMap(t, c1.TagsJSON, map[string]string{"team": "db"})
	if got := rows[dsqlC2ARN].TagsJSON; got != nil {
		t.Errorf("untagged c2 TagsJSON = %q, want nil", *got)
	}
	if got := strings.Join(s.streams, ","); got != "c1,c2" {
		t.Errorf("ListStreams called for %q, want c1,c2", got)
	}
}

// TestScanDSQL_GetClusterFails pins what a failed GetCluster does to c2,
// beside c1 read normally: a denial keeps c2 as its untagged ListClusters
// element with one warning; a transient or any other error keeps it and is
// returned; ResourceNotFoundException drops it silently. Streams are
// listed for every stored cluster.
func TestScanDSQL_GetClusterFails(t *testing.T) {
	cases := []struct {
		name         string
		err          error
		wantErr      func(error) bool // nil: the scan returns no error
		wantKept     bool
		wantGetWarns int
		wantNotFound int
	}{
		{"denied", apiErr("AccessDeniedException", "not authorized to perform: dsql:GetCluster"), nil, true, 1, 0},
		{"transient", io.EOF, func(err error) bool { return errors.Is(err, io.EOF) }, true, 0, 0},
		{"other error", apiErr("ValidationException", "bad"), func(err error) bool { return isAPIErrorCode(err, "ValidationException") }, true, 0, 0},
		{"not found", apiErr("ResourceNotFoundException", "gone"), nil, false, 0, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			getWarns := countOpWarnings(st, "dsql:GetCluster")
			listWarns := countOpWarnings(st, "dsql:ListClusters")
			s := &dsqlStub{clusters: dsqlTwoClusters(), getCluster: func(id string) (any, error) {
				if id == "c2" {
					return nil, c.err
				}
				return &dsql.GetClusterOutput{Arn: ptrStr(dsqlC1ARN), Identifier: ptrStr("c1"), Tags: map[string]string{"team": "db"}}, nil
			}}
			total, inserted, err := runDSQL(t, st, s)
			switch {
			case c.wantErr == nil && err != nil:
				t.Fatalf("err = %v, want nil", err)
			case c.wantErr != nil && !c.wantErr(err):
				t.Fatalf("err = %v, want the GetCluster error", err)
			}
			wantRows, wantStreams := 1, "c1"
			if c.wantKept {
				wantRows, wantStreams = 2, "c1,c2"
			}
			if total != 2*wantRows || inserted != 2*wantRows {
				t.Errorf("total, inserted = %d, %d; want %d, %d", total, inserted, 2*wantRows, 2*wantRows)
			}
			if *getWarns != c.wantGetWarns || *listWarns != c.wantNotFound {
				t.Errorf("GetCluster, ListClusters warnings = %d, %d; want %d, %d", *getWarns, *listWarns, c.wantGetWarns, c.wantNotFound)
			}
			rows := rowsByNativeID(t, st, TypeDSQLCluster)
			assertTagsMap(t, rows[dsqlC1ARN].TagsJSON, map[string]string{"team": "db"})
			row, kept := rows[dsqlC2ARN]
			if kept != c.wantKept {
				t.Fatalf("c2 stored = %v, want %v", kept, c.wantKept)
			}
			if kept {
				assertJSONEqual(t, "c2 attributes", row.AttributesJSON, `{"Arn":"`+dsqlC2ARN+`","Identifier":"c2"}`)
				if row.TagsJSON != nil || row.Status != nil || sv(row.Name) != "c2" {
					t.Errorf("c2 TagsJSON, Status, Name = %v, %v, %q; want both nil and c2", row.TagsJSON, row.Status, sv(row.Name))
				}
			}
			if got := strings.Join(s.streams, ","); got != wantStreams {
				t.Errorf("ListStreams called for %q, want %q", got, wantStreams)
			}
		})
	}
}

// A listed cluster with no Arn is not stored, warned about under its
// Identifier, and its streams are not listed.
func TestScanDSQL_EmptyArnWarns(t *testing.T) {
	st := newTestStore(t)
	var msgs []string
	st.OnWarn = func(w store.ScanWarning) {
		if w.Service == "dsql:ListClusters" {
			msgs = append(msgs, w.Message)
		}
	}
	s := &dsqlStub{
		clusters: []dsqltypes.ClusterSummary{{Identifier: ptrStr("noarn")}, {Arn: ptrStr(dsqlC1ARN), Identifier: ptrStr("c1")}},
		getCluster: func(id string) (any, error) {
			return &dsql.GetClusterOutput{Arn: ptrStr(dsqlC1ARN), Identifier: ptrStr(id)}, nil
		},
	}
	total, inserted, err := runDSQL(t, st, s)
	if err != nil {
		t.Fatalf("scanDSQL: %v", err)
	}
	if total != 2 || inserted != 2 {
		t.Errorf("total, inserted = %d, %d; want 2, 2", total, inserted)
	}
	if len(msgs) != 1 || !strings.Contains(msgs[0], "noarn") {
		t.Errorf("ListClusters warnings = %q, want one naming noarn", msgs)
	}
	if got := strings.Join(s.streams, ","); got != "c1" {
		t.Errorf("ListStreams called for %q, want c1", got)
	}
}

// TestScanDSQL_LaterListPageFails pins that clusters read before a failed
// ListClusters page are stored and their streams listed: a denial warns once
// and is not returned, any other error is returned.
func TestScanDSQL_LaterListPageFails(t *testing.T) {
	cases := []struct {
		name      string
		err       error
		wantErr   bool
		wantWarns int
	}{
		{"denied", apiErr("AccessDeniedException", "not authorized to perform: dsql:ListClusters"), false, 1},
		{"other error", apiErr("ValidationException", "bad token"), true, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := countOpWarnings(st, "dsql:ListClusters")
			s := &dsqlStub{getCluster: func(id string) (any, error) {
				return &dsql.GetClusterOutput{Arn: ptrStr(dsqlC1ARN), Identifier: ptrStr(id)}, nil
			}}
			h := s.handlers(t)
			h["ListClusters"] = func(in any) (any, error) {
				if sv(in.(*dsql.ListClustersInput).NextToken) == "p2" {
					return nil, c.err
				}
				return &dsql.ListClustersOutput{Clusters: dsqlTwoClusters()[:1], NextToken: ptrStr("p2")}, nil
			}
			acct := newTestAccount(testAccountID)
			acct.cfg = opStubConfig(h, errDSQLUnexpectedOp)
			total, inserted, err := scanDSQL(context.Background(), acct, testRegion, st, testScanID)
			if c.wantErr != isAPIErrorCode(err, "ValidationException") || (!c.wantErr && err != nil) {
				t.Fatalf("err = %v, want error %v", err, c.wantErr)
			}
			if total != 2 || inserted != 2 {
				t.Errorf("total, inserted = %d, %d; want 2, 2", total, inserted)
			}
			if *warns != c.wantWarns {
				t.Errorf("ListClusters warnings = %d, want %d", *warns, c.wantWarns)
			}
			if _, ok := rowsByNativeID(t, st, TypeDSQLCluster)[dsqlC1ARN]; !ok {
				t.Error("c1, read before the failed page, not stored")
			}
			if got := strings.Join(s.streams, ","); got != "c1" {
				t.Errorf("ListStreams called for %q, want c1", got)
			}
		})
	}
}

// One cluster's failed ListStreams does not stop the next cluster's, and its
// error is returned with every stored row counted.
func TestScanDSQL_StreamFailureDoesNotStopOthers(t *testing.T) {
	st := newTestStore(t)
	s := &dsqlStub{
		clusters: dsqlTwoClusters(),
		getCluster: func(id string) (any, error) {
			return &dsql.GetClusterOutput{Arn: ptrStr("arn:aws:dsql:us-east-1:123456789012:cluster/" + id), Identifier: ptrStr(id)}, nil
		},
		listStream: func(id string) (any, error) {
			if id == "c1" {
				return nil, apiErr("ValidationException", "bad cluster")
			}
			return dsqlStreamPage(id), nil
		},
	}
	total, inserted, err := runDSQL(t, st, s)
	if !isAPIErrorCode(err, "ValidationException") {
		t.Fatalf("err = %v, want the ListStreams ValidationException", err)
	}
	if total != 3 || inserted != 3 {
		t.Errorf("total, inserted = %d, %d; want 3, 3", total, inserted)
	}
	if n := len(rowsByNativeID(t, st, TypeDSQLStream)); n != 1 {
		t.Errorf("stream rows = %d, want 1 (c2's)", n)
	}
}

// A failed cluster write stops the scan before any stream is listed, and the
// GetCluster denial is not warned about: its row was never stored.
func TestScanDSQL_ClusterWriteFailureStops(t *testing.T) {
	st := newTestStore(t)
	refuseStoreWrites(t, st, TypeDSQLCluster)
	getWarns := countOpWarnings(st, "dsql:GetCluster")
	s := &dsqlStub{clusters: dsqlTwoClusters(), getCluster: func(string) (any, error) {
		return nil, apiErr("AccessDeniedException", "not authorized to perform: dsql:GetCluster")
	}}
	_, _, err := runDSQL(t, st, s)
	if !errors.Is(err, store.ErrStoreWrite) {
		t.Fatalf("err = %v, want ErrStoreWrite", err)
	}
	if len(s.streams) != 0 || *getWarns != 0 {
		t.Errorf("ListStreams calls, GetCluster warnings = %v, %d; want none, 0", s.streams, *getWarns)
	}
}

// A failed stream write stops the scan: no later cluster's streams are listed.
func TestScanDSQL_StreamWriteFailureStops(t *testing.T) {
	st := newTestStore(t)
	refuseStoreWrites(t, st, TypeDSQLStream)
	s := &dsqlStub{clusters: dsqlTwoClusters(), getCluster: func(id string) (any, error) {
		return &dsql.GetClusterOutput{Arn: ptrStr("arn:aws:dsql:us-east-1:123456789012:cluster/" + id), Identifier: ptrStr(id)}, nil
	}}
	total, inserted, err := runDSQL(t, st, s)
	if !errors.Is(err, store.ErrStoreWrite) {
		t.Fatalf("err = %v, want ErrStoreWrite", err)
	}
	if total != 2 || inserted != 2 {
		t.Errorf("total, inserted = %d, %d; want 2, 2 (the clusters)", total, inserted)
	}
	if got := strings.Join(s.streams, ","); got != "c1" {
		t.Errorf("ListStreams called for %q, want c1 only", got)
	}
}

// When ctx ends after ListClusters, no GetCluster is called: every listed
// cluster is stored as its ListClusters element, no stream is listed, and the
// stop is returned with the rows counted.
func TestScanDSQL_ContextEndsAfterListing(t *testing.T) {
	st := newTestStore(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s := &dsqlStub{getCluster: func(string) (any, error) {
		t.Error("GetCluster called after ctx ended")
		return nil, errDSQLUnexpectedOp
	}}
	h := s.handlers(t)
	h["ListClusters"] = func(any) (any, error) {
		cancel()
		return &dsql.ListClustersOutput{Clusters: dsqlTwoClusters()}, nil
	}
	acct := newTestAccount(testAccountID)
	acct.cfg = opStubConfig(h, errDSQLUnexpectedOp)
	total, inserted, err := scanDSQL(ctx, acct, testRegion, st, testScanID)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if total != 2 || inserted != 2 {
		t.Errorf("total, inserted = %d, %d; want 2, 2", total, inserted)
	}
	rows := rowsByNativeID(t, st, TypeDSQLCluster)
	assertJSONEqual(t, "c2 attributes", rows[dsqlC2ARN].AttributesJSON, `{"Arn":"`+dsqlC2ARN+`","Identifier":"c2"}`)
	if len(rows) != 2 || len(s.streams) != 0 {
		t.Errorf("cluster rows, ListStreams calls = %d, %v; want 2, none", len(rows), s.streams)
	}
}

// scanDSQLClusters itself returns the stop when ctx ends before GetCluster,
// rather than relying on the stream phase to report it.
func TestScanDSQLClusters_ContextEndsAfterListing(t *testing.T) {
	st := newTestStore(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s := &dsqlStub{getCluster: func(string) (any, error) { return nil, errDSQLUnexpectedOp }}
	h := s.handlers(t)
	h["ListClusters"] = func(any) (any, error) {
		cancel()
		return &dsql.ListClustersOutput{Clusters: dsqlTwoClusters()}, nil
	}
	acct := newTestAccount(testAccountID)
	acct.cfg = opStubConfig(h, errDSQLUnexpectedOp)
	client := dsql.NewFromConfig(acct.cfg)
	total, inserted, clusters, err := scanDSQLClusters(ctx, client, acct, testRegion, st, testScanID)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if total != 2 || inserted != 2 || len(clusters) != 2 {
		t.Errorf("total, inserted, clusters = %d, %d, %d; want 2, 2, 2", total, inserted, len(clusters))
	}
}

// When ctx ends on the first of two ListClusters pages, page 2 is not
// requested: its clusters are not stored and the stop is returned.
func TestScanDSQLClusters_ContextEndsOnFirstOfTwoPages(t *testing.T) {
	st := newTestStore(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s := &dsqlStub{getCluster: func(string) (any, error) { return nil, errDSQLUnexpectedOp }}
	h := s.handlers(t)
	h["ListClusters"] = func(in any) (any, error) {
		if sv(in.(*dsql.ListClustersInput).NextToken) == "p2" {
			return &dsql.ListClustersOutput{Clusters: dsqlTwoClusters()[1:]}, nil
		}
		cancel()
		return &dsql.ListClustersOutput{Clusters: dsqlTwoClusters()[:1], NextToken: ptrStr("p2")}, nil
	}
	acct := newTestAccount(testAccountID)
	acct.cfg = opStubConfig(h, errDSQLUnexpectedOp)
	total, inserted, _, err := scanDSQLClusters(ctx, dsql.NewFromConfig(acct.cfg), acct, testRegion, st, testScanID)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if total != 1 || inserted != 1 {
		t.Errorf("total, inserted = %d, %d; want 1, 1", total, inserted)
	}
	rows := rowsByNativeID(t, st, TypeDSQLCluster)
	if _, ok := rows[dsqlC2ARN]; ok || len(rows) != 1 {
		t.Errorf("cluster rows = %d (c2 stored %v); want only c1", len(rows), ok)
	}
}

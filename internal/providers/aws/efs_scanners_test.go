package aws

import (
	"context"
	"errors"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/efs"
	efstypes "github.com/aws/aws-sdk-go-v2/service/efs/types"
	"github.com/icearp/disco-cli/store"
)

var (
	efsTestFSA   = "arn:aws:elasticfilesystem:us-east-1:123456789012:file-system/fs-aaaa"
	efsTestFSC   = "arn:aws:elasticfilesystem:us-east-1:123456789012:file-system/fs-cccc"
	efsTestFSDst = "arn:aws:elasticfilesystem:us-west-2:123456789012:file-system/fs-dddd"
	efsTestAP    = "arn:aws:elasticfilesystem:us-east-1:123456789012:access-point/fsap-1"
	efsTestRepA  = efsTestFSA + "/replication-configuration"
	efsTestRepC  = efsTestFSC + "/replication-configuration"

	efsDenied   = apiErr("AccessDeniedException", "User: arn:aws:iam::123456789012:role/r is not authorized to perform: elasticfilesystem:Describe")
	efsThrottle = apiErr("ThrottlingException", "slow down")
	efsBadReq   = apiErr("BadRequest", "bad request")
	efsNoRepl   = apiErr("ReplicationNotFound", "No replication configuration found")
	efsCreated  = time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
)

// efsReplication builds a configuration whose source file system is srcARN in
// srcRegion, replicating to fs-dddd in us-west-2.
func efsReplication(srcARN, srcRegion string) efstypes.ReplicationConfigurationDescription {
	id := srcARN[strings.LastIndex(srcARN, "/")+1:]
	return efstypes.ReplicationConfigurationDescription{
		SourceFileSystemArn: sdkaws.String(srcARN), SourceFileSystemId: sdkaws.String(id),
		SourceFileSystemRegion: sdkaws.String(srcRegion), OriginalSourceFileSystemArn: sdkaws.String(srcARN),
		CreationTime: &efsCreated,
		Destinations: []efstypes.Destination{{
			FileSystemId: sdkaws.String("fs-dddd"), Region: sdkaws.String("us-west-2"),
			Status: efstypes.ReplicationStatusEnabled, LastReplicatedTimestamp: &efsCreated,
		}},
	}
}

// efsReplicationPages answers page one with fs-aaaa's configuration and the
// destination-side view of a us-west-2 source, and page two with fs-cccc's
// configuration, one whose source is another account's, and one without a
// source ARN. It records each call's MaxResults.
func efsReplicationPages(limits *[]int32) func(in any) (any, error) {
	var mu sync.Mutex
	page2 := "page-2"
	return func(in any) (any, error) {
		req := in.(*efs.DescribeReplicationConfigurationsInput)
		mu.Lock()
		*limits = append(*limits, sdkaws.ToInt32(req.MaxResults))
		mu.Unlock()
		if sv(req.NextToken) == "" {
			return &efs.DescribeReplicationConfigurationsOutput{
				Replications: []efstypes.ReplicationConfigurationDescription{
					efsReplication(efsTestFSA, "us-east-1"),
					efsReplication(efsTestFSDst, "us-west-2"),
				},
				NextToken: &page2,
			}, nil
		}
		return &efs.DescribeReplicationConfigurationsOutput{
			Replications: []efstypes.ReplicationConfigurationDescription{
				efsReplication(efsTestFSC, "us-east-1"),
				efsReplication("arn:aws:elasticfilesystem:us-east-1:999999999999:file-system/fs-xacct", "us-east-1"),
				{SourceFileSystemRegion: sdkaws.String("us-east-1")},
			},
		}, nil
	}
}

// efsHandlers answers a region holding file system fs-aaaa (one mount
// target), one access point, and the replication pages above.
func efsHandlers(limits *[]int32) map[string]func(in any) (any, error) {
	return map[string]func(in any) (any, error){
		"DescribeFileSystems": func(any) (any, error) {
			return &efs.DescribeFileSystemsOutput{FileSystems: []efstypes.FileSystemDescription{{
				FileSystemArn: &efsTestFSA, FileSystemId: sdkaws.String("fs-aaaa"), LifeCycleState: efstypes.LifeCycleStateAvailable,
			}}}, nil
		},
		"DescribeMountTargets": func(any) (any, error) {
			return &efs.DescribeMountTargetsOutput{MountTargets: []efstypes.MountTargetDescription{{
				MountTargetId: sdkaws.String("fsmt-1"), FileSystemId: sdkaws.String("fs-aaaa"), LifeCycleState: efstypes.LifeCycleStateAvailable,
			}}}, nil
		},
		"DescribeAccessPoints": func(any) (any, error) {
			return &efs.DescribeAccessPointsOutput{AccessPoints: []efstypes.AccessPointDescription{{
				AccessPointArn: &efsTestAP, FileSystemId: sdkaws.String("fs-aaaa"), LifeCycleState: efstypes.LifeCycleStateAvailable,
			}}}, nil
		},
		"DescribeReplicationConfigurations": efsReplicationPages(limits),
	}
}

func scanEFSTopLevel(t *testing.T, st *store.Store, handlers map[string]func(in any) (any, error)) (int, error) {
	t.Helper()
	acct := newTestAccount(testAccountID)
	acct.cfg = opStubConfig(handlers, efsDenied)
	total, _, err := scanEFS(context.Background(), acct, testRegion, st, testScanID)
	return total, err
}

func TestScanEFS_ReplicationConfigurationRows(t *testing.T) {
	st := newTestStore(t)
	var limits []int32
	total, err := scanEFSTopLevel(t, st, efsHandlers(&limits))
	if err != nil {
		t.Fatalf("scanEFS: %v", err)
	}
	// file system + mount target + access point + two replication configurations
	if total != 5 {
		t.Errorf("total = %d, want 5", total)
	}
	if len(limits) != 2 || limits[0] != 100 || limits[1] != 100 {
		t.Errorf("MaxResults per page = %v, want [100 100]", limits)
	}
	rows := rowsByNativeID(t, st, TypeEFSReplicationConfiguration)
	if len(rows) != 2 {
		t.Fatalf("replication rows = %d, want 2 (%s, %s)", len(rows), efsTestRepA, efsTestRepC)
	}
	r, ok := rows[efsTestRepA]
	if !ok {
		t.Fatalf("missing %s", efsTestRepA)
	}
	if r.Type != TypeEFSReplicationConfiguration || sv(r.Region) != "us-east-1" || sv(r.Name) != "fs-aaaa" || sv(r.Status) != "ENABLED" {
		t.Errorf("row = type %s region %q name %q status %q, want %s us-east-1 fs-aaaa ENABLED",
			r.Type, sv(r.Region), sv(r.Name), sv(r.Status), TypeEFSReplicationConfiguration)
	}
	if sv(r.CreatedAt) != "2026-01-02T03:04:05Z" {
		t.Errorf("CreatedAt = %q, want 2026-01-02T03:04:05Z", sv(r.CreatedAt))
	}
	if !strings.Contains(r.AttributesJSON, `"OriginalSourceFileSystemArn":"`+efsTestFSA+`"`) {
		t.Errorf("attributes lack OriginalSourceFileSystemArn: %s", r.AttributesJSON)
	}
	if _, ok := rows[efsTestRepC]; !ok {
		t.Errorf("missing %s", efsTestRepC)
	}
	assertContainsNative(t, st, efsTestFSA, efsTestRepA)
	// fs-cccc was not listed this scan, so its configuration is stored unlinked.
	rels, err := st.RelationshipsFrom(store.ResourceID("aws", testAccountID, efsTestFSC), store.RelContains)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 0 {
		t.Errorf("unlisted file system has contains edges %v, want none", rels)
	}
	assertContainsEdgesDeclared(t, st)
}

func TestScanEFSReplicationConfigurations_Errors(t *testing.T) {
	cases := []struct {
		name     string
		empty    bool
		page1    error
		page2    error
		wantCode string
		wantRows int
		wantWarn int
	}{
		{"empty", true, nil, nil, "", 0, 0},
		{"none on first page", false, efsNoRepl, nil, "", 0, 0},
		{"denied on first page", false, efsDenied, nil, "", 0, 1},
		{"denied on second page keeps first", false, nil, efsDenied, "", 1, 1},
		{"not found on second page is returned", false, nil, efsNoRepl, "ReplicationNotFound", 1, 0},
		{"other error on first page", false, efsBadReq, nil, "BadRequest", 0, 0},
		{"other error on second page keeps first", false, nil, efsBadReq, "BadRequest", 1, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := countOpWarnings(st, efsReplicationConfigurationsOp)
			var limits []int32
			pages := efsReplicationPages(&limits)
			h := map[string]func(in any) (any, error){
				"DescribeReplicationConfigurations": func(in any) (any, error) {
					if sv(in.(*efs.DescribeReplicationConfigurationsInput).NextToken) == "" {
						if tc.page1 != nil {
							return nil, tc.page1
						}
						if tc.empty {
							return &efs.DescribeReplicationConfigurationsOutput{}, nil
						}
						return pages(in)
					}
					if tc.page2 != nil {
						return nil, tc.page2
					}
					return pages(in)
				},
			}
			client := efs.NewFromConfig(opStubConfig(h, efsDenied))
			acct := newTestAccount(testAccountID)
			_, _, err := scanEFSReplicationConfigurations(context.Background(), client, acct, testRegion, st, testScanID, nil)
			if tc.wantCode == "" && err != nil {
				t.Fatalf("err = %v, want nil", err)
			}
			if tc.wantCode != "" {
				assertAPICode(t, err, tc.wantCode)
			}
			if rows := rowsByNativeID(t, st, TypeEFSReplicationConfiguration); len(rows) != tc.wantRows {
				t.Errorf("rows = %d, want %d", len(rows), tc.wantRows)
			}
			if *warns != tc.wantWarn {
				t.Errorf("warnings = %d, want %d", *warns, tc.wantWarn)
			}
		})
	}
}

// The replication phase runs after every older phase, so its hard error
// leaves their rows stored, and an older phase's hard error still lets it run.
func TestScanEFS_PhaseOrderAndIndependence(t *testing.T) {
	t.Run("replication error after older phases", func(t *testing.T) {
		st := newTestStore(t)
		var limits []int32
		h := efsHandlers(&limits)
		h["DescribeReplicationConfigurations"] = func(any) (any, error) { return nil, efsBadReq }
		total, err := scanEFSTopLevel(t, st, h)
		assertAPICode(t, err, "BadRequest")
		if total != 3 {
			t.Errorf("total = %d, want 3 older-phase rows", total)
		}
		for _, typ := range []string{TypeEFSFileSystem, TypeEFSMountTarget, TypeEFSAccessPoint} {
			if rows := rowsByNativeID(t, st, typ); len(rows) != 1 {
				t.Errorf("%s rows = %d, want 1", typ, len(rows))
			}
		}
	})
	t.Run("older phase error still runs replication", func(t *testing.T) {
		st := newTestStore(t)
		var limits []int32
		h := efsHandlers(&limits)
		h["DescribeAccessPoints"] = func(any) (any, error) { return nil, efsBadReq }
		total, err := scanEFSTopLevel(t, st, h)
		assertAPICode(t, err, "BadRequest")
		if total != 4 {
			t.Errorf("total = %d, want 4 (file system, mount target, two configurations)", total)
		}
		if rows := rowsByNativeID(t, st, TypeEFSReplicationConfiguration); len(rows) != 2 {
			t.Errorf("replication rows = %d, want 2", len(rows))
		}
		assertContainsNative(t, st, efsTestFSA, efsTestRepA)
	})
}

// A transient older-phase error is demoted to a warning so that the
// replication phase's hard error still reaches the dispatcher as a hard error.
func TestScanEFS_OlderTransientDoesNotMaskReplicationError(t *testing.T) {
	st := newTestStore(t)
	warns := countOpWarnings(st, "elasticfilesystem")
	var limits []int32
	h := efsHandlers(&limits)
	h["DescribeFileSystems"] = func(any) (any, error) { return nil, efsThrottle }
	h["DescribeReplicationConfigurations"] = func(any) (any, error) { return nil, efsBadReq }
	_, err := scanEFSTopLevel(t, st, h)
	assertAPICode(t, err, "BadRequest")
	if got := classifyServiceError(err); got != outcomeError {
		t.Errorf("classifyServiceError = %v, want outcomeError", got)
	}
	if *warns != 1 {
		t.Errorf("warnings = %d, want 1 for the demoted transient", *warns)
	}
}

// A replication network error is demoted to a warning so that a file system
// phase's hard error and the rows stored before it still reach the dispatcher.
func TestScanEFS_ReplicationTransientDoesNotMaskOlderError(t *testing.T) {
	st := newTestStore(t)
	warns := countOpWarnings(st, efsReplicationConfigurationsOp)
	var limits []int32
	h := efsHandlers(&limits)
	h["DescribeAccessPoints"] = func(any) (any, error) { return nil, efsBadReq }
	h["DescribeReplicationConfigurations"] = func(any) (any, error) {
		return nil, &net.OpError{Op: "read", Net: "tcp", Err: errors.New("connection reset")}
	}
	total, err := scanEFSTopLevel(t, st, h)
	assertAPICode(t, err, "BadRequest")
	if got := classifyServiceError(err); got != outcomeError {
		t.Errorf("classifyServiceError = %v, want outcomeError", got)
	}
	if total != 2 {
		t.Errorf("total = %d, want 2 (file system, mount target)", total)
	}
	if *warns != 1 {
		t.Errorf("warnings = %d, want 1 for the demoted transient", *warns)
	}
}

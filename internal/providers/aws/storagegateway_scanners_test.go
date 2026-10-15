package aws

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/storagegateway"
	sgwtypes "github.com/aws/aws-sdk-go-v2/service/storagegateway/types"
	"github.com/aws/smithy-go"
	"github.com/icearp/disco-cli/store"
)

var (
	sgwDenied = &smithy.GenericAPIError{Code: "AccessDeniedException", Message: "User: arn:aws:iam::123456789012:user/t is not authorized to perform: storagegateway:Describe"}
	// sgwGatewayDown is refused for the gateway's state; sgwBadParams for the
	// request itself. Storage Gateway sends both as InvalidGatewayRequestException.
	sgwGatewayDown = &sgwtypes.InvalidGatewayRequestException{
		Message: ptrStr("The specified gateway is not connected."),
		Error_:  &sgwtypes.StorageGatewayError{ErrorCode: sgwtypes.ErrorCodeGatewayNotConnected},
	}
	sgwBadParams = &sgwtypes.InvalidGatewayRequestException{
		Message: ptrStr("One or more parameters are invalid."),
		Error_:  &sgwtypes.StorageGatewayError{ErrorCode: sgwtypes.ErrorCodeInvalidParameters},
	}
	sgwBadInput = &smithy.GenericAPIError{Code: "ValidationException", Message: "bad"}
	sgwThrottle = &smithy.GenericAPIError{Code: "ThrottlingException", Message: "rate exceeded"}
)

func sgwTestClient(handlers map[string]func(in any) (any, error)) *storagegateway.Client {
	return storagegateway.NewFromConfig(opStubConfig(handlers, sgwDenied))
}

func sgwTestGatewayARN(id string) string {
	return sgwGatewayARN(testRegion, testAccountID, id)
}

func sgwShareARN(id string) string {
	return fmt.Sprintf("arn:aws:storagegateway:%s:%s:share/%s", testRegion, testAccountID, id)
}

func sgwTapeARN(barcode string) string {
	return fmt.Sprintf("arn:aws:storagegateway:%s:%s:tape/%s", testRegion, testAccountID, barcode)
}

func sgwShare(id, gw string, typ sgwtypes.FileShareType) sgwtypes.FileShareInfo {
	return sgwtypes.FileShareInfo{
		FileShareARN: ptrStr(sgwShareARN(id)), FileShareId: ptrStr(id), FileShareStatus: ptrStr("AVAILABLE"),
		FileShareType: typ, GatewayARN: ptrStr(sgwTestGatewayARN(gw)),
	}
}

// sgwListShares answers ListFileShares with one page per argument.
func sgwListShares(pages ...[]sgwtypes.FileShareInfo) func(in any) (any, error) {
	return func(in any) (any, error) {
		k := 0
		if m := sv(in.(*storagegateway.ListFileSharesInput).Marker); m != "" {
			_, _ = fmt.Sscanf(m, "p%d", &k)
		}
		out := &storagegateway.ListFileSharesOutput{FileShareInfoList: pages[k]}
		if k+1 < len(pages) {
			out.NextMarker = ptrStr(fmt.Sprintf("p%d", k+1))
		}
		return out, nil
	}
}

// sgwDescribeNFS describes every requested NFS share, recording each call's ARNs.
func sgwDescribeNFS(calls *sgwBatches) func(in any) (any, error) {
	return func(in any) (any, error) {
		arns := in.(*storagegateway.DescribeNFSFileSharesInput).FileShareARNList
		calls.add(arns)
		out := &storagegateway.DescribeNFSFileSharesOutput{}
		for _, a := range arns {
			out.NFSFileShareInfoList = append(out.NFSFileShareInfoList, sgwtypes.NFSFileShareInfo{
				FileShareARN: ptrStr(a), Path: ptrStr("/nfs"), LocationARN: ptrStr("arn:aws:s3:::bucket"),
			})
		}
		return out, nil
	}
}

func sgwDescribeSMB(calls *sgwBatches) func(in any) (any, error) {
	return func(in any) (any, error) {
		arns := in.(*storagegateway.DescribeSMBFileSharesInput).FileShareARNList
		calls.add(arns)
		out := &storagegateway.DescribeSMBFileSharesOutput{}
		for _, a := range arns {
			out.SMBFileShareInfoList = append(out.SMBFileShareInfoList, sgwtypes.SMBFileShareInfo{
				FileShareARN: ptrStr(a), Path: ptrStr("/smb"), Authentication: ptrStr("ActiveDirectory"),
			})
		}
		return out, nil
	}
}

// sgwBatches records the ARN list of each batch describe call; calls run on
// fan-out goroutines.
type sgwBatches struct {
	mu    sync.Mutex
	calls [][]string
}

func (b *sgwBatches) add(arns []string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.calls = append(b.calls, slices.Clone(arns))
}

// sorted returns the calls ordered by their first ARN.
func (b *sgwBatches) sorted() [][]string {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := slices.Clone(b.calls)
	slices.SortFunc(out, func(x, y []string) int { return strings.Compare(x[0], y[0]) })
	return out
}

func scanSGWSharesForTest(t *testing.T, st *store.Store, handlers map[string]func(any) (any, error)) (int, error) {
	t.Helper()
	total, _, err := scanSGWFileShares(context.Background(), sgwTestClient(handlers), newTestAccount(testAccountID), testRegion, st, testScanID)
	return total, err
}

// sgwAttrKeys decodes a row's attributes into their top-level keys.
func sgwAttrKeys(t *testing.T, r store.Resource) map[string]json.RawMessage {
	t.Helper()
	var m map[string]json.RawMessage
	if err := json.Unmarshal([]byte(r.AttributesJSON), &m); err != nil {
		t.Fatalf("decode attrs of %s: %v", r.NativeID, err)
	}
	return m
}

func TestScanSGWFileShares_DescribedDetailReplacesSummaryInOneRowPerShare(t *testing.T) {
	st := newTestStore(t)
	var nfsCalls, smbCalls sgwBatches
	// gw1 has 11 NFS shares (two batches: the cap is 10) and an SMB share;
	// gw2's NFS share must get a batch of its own.
	var page1 []sgwtypes.FileShareInfo
	for k := range 11 {
		page1 = append(page1, sgwShare(fmt.Sprintf("n%02d", k), "gw1", sgwtypes.FileShareTypeNfs))
	}
	page2 := []sgwtypes.FileShareInfo{
		sgwShare("s1", "gw1", sgwtypes.FileShareTypeSmb),
		sgwShare("z1", "gw2", sgwtypes.FileShareTypeNfs),
	}
	total, err := scanSGWSharesForTest(t, st, map[string]func(any) (any, error){
		"ListFileShares":        sgwListShares(page1, page2),
		"DescribeNFSFileShares": sgwDescribeNFS(&nfsCalls),
		"DescribeSMBFileShares": sgwDescribeSMB(&smbCalls),
	})
	if err != nil || total != 13 {
		t.Fatalf("total, err = %d, %v; want 13, nil", total, err)
	}
	var wantGW1 []string
	for k := range 10 {
		wantGW1 = append(wantGW1, sgwShareARN(fmt.Sprintf("n%02d", k)))
	}
	wantNFS := [][]string{wantGW1, {sgwShareARN("n10")}, {sgwShareARN("z1")}}
	if got := nfsCalls.sorted(); !slices.EqualFunc(got, wantNFS, slices.Equal[[]string]) {
		t.Errorf("DescribeNFSFileShares batches = %v, want %v", got, wantNFS)
	}
	if got := smbCalls.sorted(); !slices.EqualFunc(got, [][]string{{sgwShareARN("s1")}}, slices.Equal[[]string]) {
		t.Errorf("DescribeSMBFileShares batches = %v, want [[s1]]", got)
	}
	rows := rowsByNativeID(t, st, TypeStorageGatewayShare)
	if len(rows) != 13 {
		t.Fatalf("rows = %d, want 13", len(rows))
	}
	nfs := rows[sgwShareARN("z1")]
	if nfs.Type != TypeStorageGatewayShare || sv(nfs.Region) != testRegion || sv(nfs.Name) != "z1" || sv(nfs.Status) != "AVAILABLE" {
		t.Errorf("nfs row = type %q region %q name %q status %q", nfs.Type, sv(nfs.Region), sv(nfs.Name), sv(nfs.Status))
	}
	if k := sgwAttrKeys(t, nfs); string(k["Path"]) != `"/nfs"` || k["FileShareType"] != nil {
		t.Errorf("nfs attrs = %s; want the DescribeNFSFileShares element", nfs.AttributesJSON)
	}
	if k := sgwAttrKeys(t, rows[sgwShareARN("s1")]); string(k["Authentication"]) != `"ActiveDirectory"` {
		t.Errorf("smb attrs = %s; want the DescribeSMBFileShares element", rows[sgwShareARN("s1")].AttributesJSON)
	}
}

func TestScanSGWFileShares_NoSharesDescribesNothing(t *testing.T) {
	st := newTestStore(t)
	called := false
	describe := func(any) (any, error) { called = true; return nil, sgwBadInput }
	total, err := scanSGWSharesForTest(t, st, map[string]func(any) (any, error){
		"ListFileShares":        sgwListShares(nil),
		"DescribeNFSFileShares": describe,
		"DescribeSMBFileShares": describe,
	})
	if err != nil || total != 0 || called {
		t.Fatalf("total, err, described = %d, %v, %v; want 0, nil, false", total, err, called)
	}
}

func TestScanSGWFileShares_UndescribedSharesKeepSummary(t *testing.T) {
	for _, tc := range []struct {
		name     string
		err      error
		wantErr  bool
		warnings int
	}{
		{name: "denied", err: sgwDenied, warnings: 1},
		{name: "gateway not connected", err: sgwGatewayDown, warnings: 1},
		{name: "throttled", err: sgwThrottle, warnings: 1},
		{name: "invalid parameters", err: sgwBadParams, wantErr: true},
		{name: "other error", err: sgwBadInput, wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			warnings := countOpWarnings(st, "storagegateway:DescribeNFSFileShares")
			var smbCalls sgwBatches
			total, err := scanSGWSharesForTest(t, st, map[string]func(any) (any, error){
				"ListFileShares": sgwListShares([]sgwtypes.FileShareInfo{
					sgwShare("a", "gw1", sgwtypes.FileShareTypeNfs),
					sgwShare("b", "gw2", sgwtypes.FileShareTypeNfs),
					sgwShare("c", "gw1", sgwtypes.FileShareTypeSmb),
				}),
				"DescribeNFSFileShares": func(any) (any, error) { return nil, tc.err },
				"DescribeSMBFileShares": sgwDescribeSMB(&smbCalls),
			})
			if tc.wantErr != (err != nil) || (tc.wantErr && !errors.Is(err, tc.err)) {
				t.Fatalf("err = %v, want error %v", err, tc.wantErr)
			}
			if total != 3 {
				t.Errorf("total = %d, want 3: every listed share is stored", total)
			}
			if *warnings != tc.warnings {
				t.Errorf("warnings = %d, want %d for two failed batches", *warnings, tc.warnings)
			}
			rows := rowsByNativeID(t, st, TypeStorageGatewayShare)
			if len(rows) != 3 {
				t.Fatalf("rows = %d, want 3", len(rows))
			}
			for _, id := range []string{"a", "b"} {
				if k := sgwAttrKeys(t, rows[sgwShareARN(id)]); string(k["FileShareType"]) != `"NFS"` {
					t.Errorf("share %s attrs = %s; want the ListFileShares summary", id, rows[sgwShareARN(id)].AttributesJSON)
				}
			}
			if k := sgwAttrKeys(t, rows[sgwShareARN("c")]); string(k["Authentication"]) != `"ActiveDirectory"` {
				t.Errorf("smb attrs = %s; want it described despite the NFS failure", rows[sgwShareARN("c")].AttributesJSON)
			}
		})
	}
}

func sgwTape(barcode, gw string) sgwtypes.TapeInfo {
	tp := sgwtypes.TapeInfo{TapeARN: ptrStr(sgwTapeARN(barcode)), TapeBarcode: ptrStr(barcode), TapeStatus: ptrStr("AVAILABLE")}
	if gw != "" {
		tp.GatewayARN = ptrStr(sgwTestGatewayARN(gw))
	} else {
		tp.TapeStatus = ptrStr("ARCHIVED")
	}
	return tp
}

func sgwArchive(barcode string) sgwtypes.TapeArchive {
	created := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	return sgwtypes.TapeArchive{
		TapeARN: ptrStr(sgwTapeARN(barcode)), TapeBarcode: ptrStr(barcode), TapeStatus: ptrStr("ARCHIVED"),
		TapeCreatedDate: &created, KMSKey: ptrStr("arn:aws:kms:us-east-1:123456789012:key/k"),
	}
}

func sgwListTapes(pages ...[]sgwtypes.TapeInfo) func(in any) (any, error) {
	return func(in any) (any, error) {
		k := 0
		if m := sv(in.(*storagegateway.ListTapesInput).Marker); m != "" {
			_, _ = fmt.Sscanf(m, "p%d", &k)
		}
		out := &storagegateway.ListTapesOutput{TapeInfos: pages[k]}
		if k+1 < len(pages) {
			out.Marker = ptrStr(fmt.Sprintf("p%d", k+1))
		}
		return out, nil
	}
}

func sgwDescribeArchives(pages ...[]sgwtypes.TapeArchive) func(in any) (any, error) {
	return func(in any) (any, error) {
		k := 0
		if m := sv(in.(*storagegateway.DescribeTapeArchivesInput).Marker); m != "" {
			_, _ = fmt.Sscanf(m, "p%d", &k)
		}
		out := &storagegateway.DescribeTapeArchivesOutput{TapeArchives: pages[k]}
		if k+1 < len(pages) {
			out.Marker = ptrStr(fmt.Sprintf("p%d", k+1))
		}
		return out, nil
	}
}

func scanSGWTapesForTest(t *testing.T, st *store.Store, handlers map[string]func(any) (any, error)) (int, error) {
	t.Helper()
	total, _, err := scanSGWTapes(context.Background(), sgwTestClient(handlers), newTestAccount(testAccountID), testRegion, st, testScanID)
	return total, err
}

func TestScanSGWTapes_ShelvedTapeTakesArchiveDetail(t *testing.T) {
	st := newTestStore(t)
	total, err := scanSGWTapesForTest(t, st, map[string]func(any) (any, error){
		"ListTapes": sgwListTapes([]sgwtypes.TapeInfo{sgwTape("LIB1", "gw1"), sgwTape("VTS1", "")}, []sgwtypes.TapeInfo{sgwTape("VTS2", "")}),
		// LIB1 is archived too, yet sits in a gateway's library: it keeps the
		// summary that names its gateway. VTS2 has no archive element.
		"DescribeTapeArchives": sgwDescribeArchives([]sgwtypes.TapeArchive{sgwArchive("LIB1")}, []sgwtypes.TapeArchive{sgwArchive("VTS1")}),
	})
	if err != nil || total != 3 {
		t.Fatalf("total, err = %d, %v; want 3, nil", total, err)
	}
	rows := rowsByNativeID(t, st, TypeStorageGatewayTape)
	if len(rows) != 3 {
		t.Fatalf("rows = %d, want 3", len(rows))
	}
	vts := rows[sgwTapeARN("VTS1")]
	if vts.Type != TypeStorageGatewayTape || sv(vts.Region) != testRegion || sv(vts.Name) != "VTS1" || sv(vts.Status) != "ARCHIVED" {
		t.Errorf("shelved row = type %q region %q name %q status %q", vts.Type, sv(vts.Region), sv(vts.Name), sv(vts.Status))
	}
	if k := sgwAttrKeys(t, vts); string(k["TapeCreatedDate"]) != `"2026-01-02T03:04:05Z"` || k["KMSKey"] == nil {
		t.Errorf("shelved attrs = %s; want the DescribeTapeArchives element from page 2", vts.AttributesJSON)
	}
	if k := sgwAttrKeys(t, rows[sgwTapeARN("LIB1")]); string(k["GatewayARN"]) != fmt.Sprintf("%q", sgwTestGatewayARN("gw1")) {
		t.Errorf("library attrs = %s; want the ListTapes summary", rows[sgwTapeARN("LIB1")].AttributesJSON)
	}
	if k := sgwAttrKeys(t, rows[sgwTapeARN("VTS2")]); k["KMSKey"] != nil || string(k["TapeStatus"]) != `"ARCHIVED"` {
		t.Errorf("unarchived attrs = %s; want the ListTapes summary", rows[sgwTapeARN("VTS2")].AttributesJSON)
	}
}

func TestScanSGWTapes_NoShelvedTapeSkipsArchives(t *testing.T) {
	st := newTestStore(t)
	called := false
	total, err := scanSGWTapesForTest(t, st, map[string]func(any) (any, error){
		"ListTapes":            sgwListTapes([]sgwtypes.TapeInfo{sgwTape("LIB1", "gw1")}),
		"DescribeTapeArchives": func(any) (any, error) { called = true; return nil, sgwBadInput },
	})
	if err != nil || total != 1 || called {
		t.Fatalf("total, err, described = %d, %v, %v; want 1, nil, false", total, err, called)
	}
}

func TestScanSGWTapes_ArchiveFailureKeepsSummaries(t *testing.T) {
	for _, tc := range []struct {
		name     string
		err      error
		wantErr  bool
		warnings int
	}{
		{name: "denied", err: sgwDenied, warnings: 1},
		{name: "gateway not connected", err: sgwGatewayDown, warnings: 1},
		{name: "throttled", err: sgwThrottle, warnings: 1},
		{name: "invalid parameters", err: sgwBadParams, wantErr: true},
		{name: "other error", err: sgwBadInput, wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			warnings := countOpWarnings(st, "storagegateway:DescribeTapeArchives")
			total, err := scanSGWTapesForTest(t, st, map[string]func(any) (any, error){
				"ListTapes":            sgwListTapes([]sgwtypes.TapeInfo{sgwTape("VTS1", "")}),
				"DescribeTapeArchives": func(any) (any, error) { return nil, tc.err },
			})
			if tc.wantErr != (err != nil) || (tc.wantErr && !errors.Is(err, tc.err)) {
				t.Fatalf("err = %v, want error %v", err, tc.wantErr)
			}
			if *warnings != tc.warnings {
				t.Errorf("warnings = %d, want %d", *warnings, tc.warnings)
			}
			rows := rowsByNativeID(t, st, TypeStorageGatewayTape)
			if total != 1 || len(rows) != 1 {
				t.Fatalf("total, rows = %d, %d; want 1, 1", total, len(rows))
			}
			if k := sgwAttrKeys(t, rows[sgwTapeARN("VTS1")]); k["TapeCreatedDate"] != nil {
				t.Errorf("attrs = %s; want the ListTapes summary", rows[sgwTapeARN("VTS1")].AttributesJSON)
			}
		})
	}
}

func sgwGateway(id, typ string) sgwtypes.GatewayInfo {
	return sgwtypes.GatewayInfo{GatewayARN: ptrStr(sgwTestGatewayARN(id)), GatewayName: ptrStr(id), GatewayType: ptrStr(typ)}
}

func sgwPolicy(gw string) sgwtypes.AutomaticTapeCreationPolicyInfo {
	return sgwtypes.AutomaticTapeCreationPolicyInfo{
		GatewayARN: ptrStr(sgwTestGatewayARN(gw)),
		AutomaticTapeCreationRules: []sgwtypes.AutomaticTapeCreationRule{{
			TapeBarcodePrefix: ptrStr("ABC"), PoolId: ptrStr("GLACIER"), TapeSizeInBytes: sdkaws.Int64(107374182400), MinimumNumTapes: sdkaws.Int32(5),
		}},
	}
}

func scanSGWPoliciesForTest(t *testing.T, st *store.Store, gateways []sgwtypes.GatewayInfo, list func(any) (any, error)) (int, error) {
	t.Helper()
	client := sgwTestClient(map[string]func(any) (any, error){"ListAutomaticTapeCreationPolicies": list})
	total, _, err := scanSGWAutomaticTapeCreationPolicies(context.Background(), client, gateways, newTestAccount(testAccountID), testRegion, st, testScanID)
	return total, err
}

func TestScanSGWAutomaticTapeCreationPolicies_StoresEachUnderItsGateway(t *testing.T) {
	st := newTestStore(t)
	// "late" is stored from an earlier scan but not listed by this one.
	seedParentRows(t, st, TypeStorageGatewayGateway, sgwTestGatewayARN("tape"), sgwTestGatewayARN("file"), sgwTestGatewayARN("late"))
	calls := 0
	total, err := scanSGWPoliciesForTest(t, st,
		[]sgwtypes.GatewayInfo{sgwGateway("tape", "VTL"), sgwGateway("file", "FILE_S3")},
		func(in any) (any, error) {
			calls++
			if in.(*storagegateway.ListAutomaticTapeCreationPoliciesInput).GatewayARN != nil {
				return nil, sgwBadInput // one region-wide call, not per gateway
			}
			return &storagegateway.ListAutomaticTapeCreationPoliciesOutput{AutomaticTapeCreationPolicyInfos: []sgwtypes.AutomaticTapeCreationPolicyInfo{
				sgwPolicy("tape"), sgwPolicy("late"), {},
			}}, nil
		})
	if err != nil || total != 2 || calls != 1 {
		t.Fatalf("total, err, calls = %d, %v, %d; want 2, nil, 1", total, err, calls)
	}
	rows := rowsByNativeID(t, st, TypeStorageGatewayAutomaticTapeCreationPolicy)
	if len(rows) != 2 {
		t.Fatalf("rows = %d, want 2 (the policy without a GatewayARN is dropped)", len(rows))
	}
	id := sgwTestGatewayARN("tape") + "/automatic-tape-creation-policy"
	got, ok := rows[id]
	if !ok {
		t.Fatalf("no row %s; have %v", id, rows)
	}
	if got.Type != TypeStorageGatewayAutomaticTapeCreationPolicy || sv(got.Region) != testRegion {
		t.Errorf("row = type %q region %q", got.Type, sv(got.Region))
	}
	if k := sgwAttrKeys(t, got); k["AutomaticTapeCreationRules"] == nil {
		t.Errorf("attrs = %s; want the policy element", got.AttributesJSON)
	}
	assertContainsNative(t, st, sgwTestGatewayARN("tape"), id)
	// A policy whose gateway was not listed this scan is stored unlinked.
	lateID := store.ResourceID("aws", testAccountID, sgwTestGatewayARN("late")+"/automatic-tape-creation-policy")
	parents, err := st.RelationshipsTo(lateID, store.RelContains)
	if err != nil {
		t.Fatalf("RelationshipsTo: %v", err)
	}
	if len(parents) != 0 {
		t.Errorf("unlisted gateway's policy has parents %v, want none", parents)
	}
}

func TestScanSGWAutomaticTapeCreationPolicies_NoTapeGatewayNoCall(t *testing.T) {
	for _, gateways := range [][]sgwtypes.GatewayInfo{nil, {sgwGateway("file", "FILE_S3"), sgwGateway("vol", "CACHED")}} {
		st := newTestStore(t)
		called := false
		total, err := scanSGWPoliciesForTest(t, st, gateways, func(any) (any, error) { called = true; return nil, sgwBadInput })
		if err != nil || total != 0 || called {
			t.Errorf("gateways %d: total, err, called = %d, %v, %v; want 0, nil, false", len(gateways), total, err, called)
		}
	}
}

func TestScanSGWAutomaticTapeCreationPolicies_Errors(t *testing.T) {
	for _, tc := range []struct {
		name     string
		err      error
		wantErr  bool
		warnings int
	}{
		{name: "denied", err: sgwDenied, warnings: 1},
		{name: "gateway not connected", err: sgwGatewayDown, warnings: 1},
		{name: "invalid parameters", err: sgwBadParams, wantErr: true},
		{name: "other error", err: sgwBadInput, wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			warnings := countOpWarnings(st, "storagegateway:ListAutomaticTapeCreationPolicies")
			total, err := scanSGWPoliciesForTest(t, st, []sgwtypes.GatewayInfo{sgwGateway("tape", "VTL")}, func(any) (any, error) { return nil, tc.err })
			if tc.wantErr != (err != nil) || (tc.wantErr && !errors.Is(err, tc.err)) {
				t.Fatalf("err = %v, want error %v", err, tc.wantErr)
			}
			if total != 0 || *warnings != tc.warnings {
				t.Errorf("total, warnings = %d, %d; want 0, %d", total, *warnings, tc.warnings)
			}
		})
	}
}

// sgwScanHandlers answers every op of a full scan with one row each, on one
// tape gateway.
func sgwScanHandlers() map[string]func(any) (any, error) {
	var nfs, smb sgwBatches
	return map[string]func(any) (any, error){
		"ListGateways": func(any) (any, error) {
			return &storagegateway.ListGatewaysOutput{Gateways: []sgwtypes.GatewayInfo{sgwGateway("gw1", "VTL")}}, nil
		},
		"ListVolumes": func(any) (any, error) {
			return &storagegateway.ListVolumesOutput{VolumeInfos: []sgwtypes.VolumeInfo{{VolumeARN: ptrStr(sgwTestGatewayARN("gw1") + "/volume/vol-1")}}}, nil
		},
		"ListFileShares":        sgwListShares([]sgwtypes.FileShareInfo{sgwShare("a", "gw1", sgwtypes.FileShareTypeNfs)}),
		"DescribeNFSFileShares": sgwDescribeNFS(&nfs),
		"DescribeSMBFileShares": sgwDescribeSMB(&smb),
		"ListTapes":             sgwListTapes([]sgwtypes.TapeInfo{sgwTape("VTS1", "")}),
		"DescribeTapeArchives":  sgwDescribeArchives([]sgwtypes.TapeArchive{sgwArchive("VTS1")}),
		"ListTapePools": func(any) (any, error) {
			return &storagegateway.ListTapePoolsOutput{PoolInfos: []sgwtypes.PoolInfo{{PoolARN: ptrStr("arn:aws:storagegateway:us-east-1:123456789012:tapepool/pool-1")}}}, nil
		},
		"ListFileSystemAssociations": func(any) (any, error) { return &storagegateway.ListFileSystemAssociationsOutput{}, nil },
		"ListCacheReports":           func(any) (any, error) { return &storagegateway.ListCacheReportsOutput{}, nil },
		"DescribeVTLDevices": func(any) (any, error) {
			return &storagegateway.DescribeVTLDevicesOutput{VTLDevices: []sgwtypes.VTLDevice{{VTLDeviceARN: ptrStr(sgwTestGatewayARN("gw1") + "/device/d1")}}}, nil
		},
		"ListAutomaticTapeCreationPolicies": func(any) (any, error) {
			return &storagegateway.ListAutomaticTapeCreationPoliciesOutput{AutomaticTapeCreationPolicyInfos: []sgwtypes.AutomaticTapeCreationPolicyInfo{sgwPolicy("gw1")}}, nil
		},
	}
}

func scanSGWForTest(t *testing.T, st *store.Store, handlers map[string]func(any) (any, error)) (int, error) {
	t.Helper()
	total, _, err := scanStorageGatewayWith(context.Background(), sgwTestClient(handlers), newTestAccount(testAccountID), testRegion, st, testScanID)
	return total, err
}

func TestScanStorageGateway_StoresNewRowsAlongsideExisting(t *testing.T) {
	st := newTestStore(t)
	total, err := scanSGWForTest(t, st, sgwScanHandlers())
	if err != nil || total != 7 {
		t.Fatalf("total, err = %d, %v; want 7 (gateway, volume, share, tape, pool, device, policy), nil", total, err)
	}
	if k := sgwAttrKeys(t, rowsByNativeID(t, st, TypeStorageGatewayShare)[sgwShareARN("a")]); k["Path"] == nil {
		t.Error("share row is not the DescribeNFSFileShares element")
	}
	if k := sgwAttrKeys(t, rowsByNativeID(t, st, TypeStorageGatewayTape)[sgwTapeARN("VTS1")]); k["TapeCreatedDate"] == nil {
		t.Error("shelved tape row is not the DescribeTapeArchives element")
	}
	assertContainsNative(t, st, sgwTestGatewayARN("gw1"), sgwTestGatewayARN("gw1")+"/automatic-tape-creation-policy")
}

// A phase's hard error neither stops the phases after it nor loses their rows,
// and a transient one is a warning, not the service's error.
func TestScanStorageGateway_PhaseErrorsDoNotStopOtherPhases(t *testing.T) {
	for _, op := range []string{"ListVolumes", "ListTapes", "ListAutomaticTapeCreationPolicies"} {
		t.Run(op, func(t *testing.T) {
			st := newTestStore(t)
			handlers := sgwScanHandlers()
			handlers[op] = func(any) (any, error) { return nil, sgwBadInput }
			total, err := scanSGWForTest(t, st, handlers)
			if !isAPIErrorCode(err, "ValidationException") {
				t.Fatalf("err = %v, want the %s error", err, op)
			}
			if total != 6 {
				t.Errorf("total = %d, want 6: every other phase stored its row", total)
			}
			if len(rowsByNativeID(t, st, TypeStorageGatewayDevice)) != 1 {
				t.Error("VTL device phase did not run")
			}
		})
	}
	t.Run("transient", func(t *testing.T) {
		st := newTestStore(t)
		warnings := countOpWarnings(st, "storagegateway:ListVolumes")
		handlers := sgwScanHandlers()
		handlers["ListVolumes"] = func(any) (any, error) { return nil, sgwThrottle }
		handlers["ListAutomaticTapeCreationPolicies"] = func(any) (any, error) { return nil, sgwBadInput }
		total, err := scanSGWForTest(t, st, handlers)
		if !isAPIErrorCode(err, "ValidationException") || isAPIErrorCode(err, "ThrottlingException") {
			t.Fatalf("err = %v, want only the policy phase's error", err)
		}
		if total != 5 || *warnings != 1 {
			t.Errorf("total, warnings = %d, %d; want 5, 1", total, *warnings)
		}
	})
}

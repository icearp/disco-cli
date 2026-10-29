package aws

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net"
	"reflect"
	"slices"
	"strconv"
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

// sgwNoTags answers ListTagsForResource with no tags.
func sgwNoTags(any) (any, error) { return &storagegateway.ListTagsForResourceOutput{}, nil }

// sgwRunPhase runs one phase with its rows' tags read through sgwTags and
// returns its total and joined errors. Handlers without ListTagsForResource
// answer every tag read with no tags.
func sgwRunPhase(t *testing.T, st *store.Store, handlers map[string]func(any) (any, error), run func(storageGatewayAPI, rowTagReader, *phaseTotals) bool) (int, error) {
	t.Helper()
	if _, ok := handlers["ListTagsForResource"]; !ok {
		handlers["ListTagsForResource"] = sgwNoTags
	}
	client := sgwTestClient(handlers)
	var p phaseTotals
	run(client, sgwTags(client), &p)
	total, _, err := p.result(st, newTestAccount(testAccountID), testRegion)
	return total, err
}

func scanSGWSharesForTest(t *testing.T, st *store.Store, handlers map[string]func(any) (any, error)) (int, error) {
	t.Helper()
	return sgwRunPhase(t, st, handlers, func(client storageGatewayAPI, tags rowTagReader, p *phaseTotals) bool {
		return scanSGWFileShares(context.Background(), client, newTestAccount(testAccountID), testRegion, st, testScanID, tags, p)
	})
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
	return sgwRunPhase(t, st, handlers, func(client storageGatewayAPI, tags rowTagReader, p *phaseTotals) bool {
		return scanSGWTapes(context.Background(), client, newTestAccount(testAccountID), testRegion, st, testScanID, tags, p)
	})
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
		"ListTagsForResource":        sgwNoTags,
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

// sgwTestARN is a Storage Gateway ARN of the test account and region.
func sgwTestARN(resource string) string {
	return "arn:aws:storagegateway:" + testRegion + ":" + testAccountID + ":" + resource
}

// sgwKind is one listing whose rows have their tags read with
// ListTagsForResource: the List op, the type it stores, the ARN field of its
// element, the Status its rows carry ("" for none), and page, which builds
// the op's output holding the element of suffix, with next as its marker.
type sgwKind struct {
	op, typ, arnField, status string
	arn                       func(suffix string) string
	name                      func(suffix string) string
	page                      func(suffix string, next *string) any
}

func sgwKinds() []sgwKind {
	gw := func(s string) string { return sgwTestARN("gateway/sgw-" + s) }
	vol := func(s string) string { return gw("tagged") + "/volume/vol-" + s }
	tape := func(s string) string { return sgwTestARN("tape/TAPE" + strings.ToUpper(s)) }
	pool := func(s string) string { return sgwTestARN("tapepool/pool-" + s) }
	fsa := func(s string) string { return sgwTestARN("fs-association/fsa-" + s) }
	return []sgwKind{
		{"ListGateways", TypeStorageGatewayGateway, "GatewayARN", "ACTIVE", gw, func(s string) string { return "gw-" + s }, func(s string, next *string) any {
			return &storagegateway.ListGatewaysOutput{Marker: next, Gateways: []sgwtypes.GatewayInfo{{
				GatewayARN: ptrStr(gw(s)), GatewayName: ptrStr("gw-" + s), GatewayOperationalState: ptrStr("ACTIVE"), GatewayType: ptrStr("VTL"),
			}}}
		}},
		{"ListVolumes", TypeStorageGatewayVolume, "VolumeARN", "", vol, func(s string) string { return "vol-" + s }, func(s string, next *string) any {
			return &storagegateway.ListVolumesOutput{Marker: next, VolumeInfos: []sgwtypes.VolumeInfo{{
				VolumeARN: ptrStr(vol(s)), VolumeId: ptrStr("vol-" + s), GatewayARN: ptrStr(gw("tagged")),
			}}}
		}},
		{"ListTapes", TypeStorageGatewayTape, "TapeARN", "AVAILABLE", tape, func(s string) string { return "TAPE" + strings.ToUpper(s) }, func(s string, next *string) any {
			return &storagegateway.ListTapesOutput{Marker: next, TapeInfos: []sgwtypes.TapeInfo{{
				TapeARN: ptrStr(tape(s)), TapeBarcode: ptrStr("TAPE" + strings.ToUpper(s)), TapeStatus: ptrStr("AVAILABLE"), GatewayARN: ptrStr(gw("tagged")),
			}}}
		}},
		{"ListTapePools", TypeStorageGatewayTapePool, "PoolARN", "ACTIVE", pool, func(s string) string { return "pool-" + s }, func(s string, next *string) any {
			return &storagegateway.ListTapePoolsOutput{Marker: next, PoolInfos: []sgwtypes.PoolInfo{{
				PoolARN: ptrStr(pool(s)), PoolName: ptrStr("pool-" + s), PoolStatus: sgwtypes.PoolStatusActive,
			}}}
		}},
		{"ListFileSystemAssociations", TypeStorageGatewayFsAssociation, "FileSystemAssociationARN", "AVAILABLE", fsa, func(s string) string { return "fsa-" + s }, func(s string, next *string) any {
			return &storagegateway.ListFileSystemAssociationsOutput{NextMarker: next, FileSystemAssociationSummaryList: []sgwtypes.FileSystemAssociationSummary{{
				FileSystemAssociationARN: ptrStr(fsa(s)), FileSystemAssociationId: ptrStr("fsa-" + s), FileSystemAssociationStatus: ptrStr("AVAILABLE"), GatewayARN: ptrStr(gw("tagged")),
			}}}
		}},
	}
}

func sgwKindOf(t *testing.T, op string) sgwKind {
	t.Helper()
	for _, k := range sgwKinds() {
		if k.op == op {
			return k
		}
	}
	t.Fatalf("%s is no tag-read listing", op)
	return sgwKind{}
}

// The file shares and cache reports of the scan fixture: two of each, one per
// page. The NFS share's describe shows its tags; the SMB share has none. The
// tagged cache report's element carries its tags.
var (
	sgwTestShareTagged  = sgwTestARN("share/share-tagged")
	sgwTestShareBare    = sgwTestARN("share/share-bare")
	sgwTestReportTagged = sgwTestShareTagged + "/cache-report/report-tagged"
	sgwTestReportBare   = sgwTestShareTagged + "/cache-report/report-bare"
	sgwTestDevice       = sgwTestARN("gateway/sgw-tagged/device/AMZN_SGW-1_TAPEDRIVE_00001")
)

const (
	// sgwTestTotal is the fixture's rows: two of each of the five tag-read
	// listings, two file shares, two cache reports, one VTL device (of the
	// tagged gateway) and one tape creation policy.
	sgwTestTotal = 2*5 + 2 + 2 + 1 + 1
	// sgwTestBadParams is the request-defect code the tag op answers with.
	sgwTestBadParams = "InvalidGatewayRequestException"
)

// sgwCalls records the calls of a scan and answers ListTagsForResource with
// tags, or errs, by ARN.
type sgwCalls struct {
	mu      sync.Mutex
	ops     map[string]int
	inputs  map[string][]any
	tagARNs []string
	tags    map[string]map[string]string
	errs    map[string]error
}

// newSGWCalls tags the "tagged" row of every tag-read kind with its type.
func newSGWCalls() *sgwCalls {
	c := &sgwCalls{ops: map[string]int{}, inputs: map[string][]any{}, tags: map[string]map[string]string{}, errs: map[string]error{}}
	for _, k := range sgwKinds() {
		c.tags[k.arn("tagged")] = map[string]string{"type": k.typ}
	}
	return c
}

func (c *sgwCalls) record(op string, in any) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.ops[op]++
	c.inputs[op] = append(c.inputs[op], in)
}

func (c *sgwCalls) count(op string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.ops[op]
}

func (c *sgwCalls) sortedTagARNs() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return slices.Sorted(slices.Values(c.tagARNs))
}

func (c *sgwCalls) listTags(in any) (any, error) {
	arn := sv(in.(*storagegateway.ListTagsForResourceInput).ResourceARN)
	c.record("ListTagsForResource", in)
	c.mu.Lock()
	c.tagARNs = append(c.tagARNs, arn)
	err, tags := c.errs[arn], c.tags[arn]
	c.mu.Unlock()
	if err != nil {
		return nil, err
	}
	out := &storagegateway.ListTagsForResourceOutput{ResourceARN: &arn}
	for _, k := range slices.Sorted(maps.Keys(tags)) {
		out.Tags = append(out.Tags, sgwtypes.Tag{Key: ptrStr(k), Value: ptrStr(tags[k])})
	}
	return out, nil
}

// twoPages answers op with the "tagged" element on the first page and the
// "bare" one on the second.
func (c *sgwCalls) twoPages(op string, page func(suffix string, next *string) any) func(any) (any, error) {
	return func(in any) (any, error) {
		c.record(op, in)
		if bacField[*string](in, "Marker") != nil {
			return page("bare", nil), nil
		}
		return page("tagged", ptrStr("p2")), nil
	}
}

// sgwHandlers answers every op the scan calls with the fixture.
func sgwHandlers(c *sgwCalls) map[string]func(in any) (any, error) {
	h := map[string]func(in any) (any, error){
		"ListTagsForResource": c.listTags,
		"ListFileShares": c.twoPages("ListFileShares", func(s string, next *string) any {
			typ := sgwtypes.FileShareTypeNfs
			if s == "bare" {
				typ = sgwtypes.FileShareTypeSmb
			}
			return &storagegateway.ListFileSharesOutput{NextMarker: next, FileShareInfoList: []sgwtypes.FileShareInfo{{
				FileShareARN: ptrStr(sgwTestARN("share/share-" + s)), FileShareId: ptrStr("share-" + s), FileShareStatus: ptrStr("AVAILABLE"),
				FileShareType: typ, GatewayARN: ptrStr(sgwTestARN("gateway/sgw-tagged")),
			}}}
		}),
		"DescribeNFSFileShares": func(in any) (any, error) {
			c.record("DescribeNFSFileShares", in)
			out := &storagegateway.DescribeNFSFileSharesOutput{}
			for _, a := range in.(*storagegateway.DescribeNFSFileSharesInput).FileShareARNList {
				out.NFSFileShareInfoList = append(out.NFSFileShareInfoList, sgwtypes.NFSFileShareInfo{
					FileShareARN: ptrStr(a), Path: ptrStr("/nfs"), Tags: []sgwtypes.Tag{{Key: ptrStr("type"), Value: ptrStr(TypeStorageGatewayShare)}},
				})
			}
			return out, nil
		},
		"DescribeSMBFileShares": func(in any) (any, error) {
			c.record("DescribeSMBFileShares", in)
			out := &storagegateway.DescribeSMBFileSharesOutput{}
			for _, a := range in.(*storagegateway.DescribeSMBFileSharesInput).FileShareARNList {
				out.SMBFileShareInfoList = append(out.SMBFileShareInfoList, sgwtypes.SMBFileShareInfo{FileShareARN: ptrStr(a), Path: ptrStr("/smb")})
			}
			return out, nil
		},
		"ListCacheReports": c.twoPages("ListCacheReports", func(s string, next *string) any {
			cr := sgwtypes.CacheReportInfo{
				CacheReportARN: ptrStr(sgwTestShareTagged + "/cache-report/report-" + s), ReportName: ptrStr("report-" + s),
				CacheReportStatus: sgwtypes.CacheReportStatusCompleted,
			}
			if s == "tagged" {
				cr.Tags = []sgwtypes.Tag{{Key: ptrStr("type"), Value: ptrStr(TypeStorageGatewayCacheReport)}}
			}
			return &storagegateway.ListCacheReportsOutput{Marker: next, CacheReportList: []sgwtypes.CacheReportInfo{cr}}
		}),
		"DescribeVTLDevices": func(in any) (any, error) {
			c.record("DescribeVTLDevices", in)
			out := &storagegateway.DescribeVTLDevicesOutput{}
			if sv(in.(*storagegateway.DescribeVTLDevicesInput).GatewayARN) == sgwTestARN("gateway/sgw-tagged") {
				out.VTLDevices = []sgwtypes.VTLDevice{{VTLDeviceARN: ptrStr(sgwTestDevice), VTLDeviceType: ptrStr("Medium changer")}}
			}
			return out, nil
		},
		"ListAutomaticTapeCreationPolicies": func(in any) (any, error) {
			c.record("ListAutomaticTapeCreationPolicies", in)
			return &storagegateway.ListAutomaticTapeCreationPoliciesOutput{AutomaticTapeCreationPolicyInfos: []sgwtypes.AutomaticTapeCreationPolicyInfo{{
				GatewayARN: ptrStr(sgwTestARN("gateway/sgw-tagged")),
			}}}, nil
		},
	}
	for _, k := range sgwKinds() {
		h[k.op] = c.twoPages(k.op, k.page)
	}
	return h
}

// sgwScan runs the scan's real entry point against handlers; every op they
// do not answer is denied.
func sgwScan(ctx context.Context, st *store.Store, handlers map[string]func(in any) (any, error)) (int, int, error) {
	acct := newTestAccount(testAccountID)
	acct.cfg = opStubConfig(handlers, sgwDenied)
	return scanStorageGateway(ctx, acct, testRegion, st, testScanID)
}

// sgwTagReadARNs is the ARN of every row whose tags the fixture's scan reads
// with ListTagsForResource: every tag-read kind's two rows, and no file share
// (their describes show all their tags) or cache report (its element carries
// them).
func sgwTagReadARNs() []string {
	var arns []string
	for _, k := range sgwKinds() {
		arns = append(arns, k.arn("tagged"), k.arn("bare"))
	}
	slices.Sort(arns)
	return arns
}

// Every tag-read listing stores both its pages, each row under its listed
// ARN, with its name, status and listed element, and the tags
// ListTagsForResource returns for that ARN, nil for none. Exactly those ARNs
// are sent to the tag op, and no op is sent a Limit. A file share's tags come
// from its describe, a cache report's from its element; the VTL device and
// the tape creation policy are stored untagged. No error, no warning.
func TestScanStorageGateway_StoresTagsOfEveryType(t *testing.T) {
	st := newTestStore(t)
	warns := rhWarnRecorder(st)
	calls := newSGWCalls()

	total, inserted, err := sgwScan(context.Background(), st, sgwHandlers(calls))
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != sgwTestTotal || inserted != sgwTestTotal {
		t.Errorf("total, inserted = %d, %d; want %d, %d", total, inserted, sgwTestTotal, sgwTestTotal)
	}
	if w := warns(); len(w) != 0 {
		t.Errorf("warnings = %v; want none", w)
	}
	for _, k := range sgwKinds() {
		rows := rowsByNativeID(t, st, k.typ)
		if len(rows) != 2 {
			t.Errorf("%s rows = %v; want 2", k.typ, nativeIDs(rows))
		}
		for _, suffix := range []string{"tagged", "bare"} {
			arn := k.arn(suffix)
			r, ok := rows[arn]
			if !ok {
				t.Errorf("%s %s missing", k.typ, arn)
				continue
			}
			wantStatus := ptrStr(k.status)
			if k.status == "" {
				wantStatus = nil
			}
			if sv(r.Name) != k.name(suffix) || sv(r.Region) != testRegion || !reflect.DeepEqual(r.Status, wantStatus) || r.CreatedAt != nil || r.ManagedByProvider {
				t.Errorf("%s Name, Region, Status, CreatedAt, Managed = %q, %q, %v, %v, %v; want %q, %q, %q, nil, false",
					arn, sv(r.Name), sv(r.Region), r.Status, r.CreatedAt, r.ManagedByProvider, k.name(suffix), testRegion, k.status)
			}
			if got := sgwAttrKeys(t, r)[k.arnField]; string(got) != strconv.Quote(arn) {
				t.Errorf("%s attrs = %s; want the listed element", arn, r.AttributesJSON)
			}
			if suffix == "tagged" {
				assertTagsMap(t, r.TagsJSON, map[string]string{"type": k.typ})
			} else if r.TagsJSON != nil {
				t.Errorf("%s TagsJSON = %q; want nil", arn, *r.TagsJSON)
			}
		}
	}
	shares := rowsByNativeID(t, st, TypeStorageGatewayShare)
	if s, ok := shares[sgwTestShareTagged]; !ok || sv(s.Name) != "share-tagged" || sv(s.Status) != "AVAILABLE" || s.CreatedAt != nil || string(sgwAttrKeys(t, s)["Path"]) != `"/nfs"` {
		t.Errorf("NFS share stored %v: %+v; want share-tagged, AVAILABLE, no CreatedAt, the describe element", ok, s)
	} else {
		assertTagsMap(t, s.TagsJSON, map[string]string{"type": TypeStorageGatewayShare})
	}
	if s, ok := shares[sgwTestShareBare]; !ok || s.TagsJSON != nil || s.CreatedAt != nil || string(sgwAttrKeys(t, s)["Path"]) != `"/smb"` {
		t.Errorf("SMB share stored %v: %+v; want untagged, no CreatedAt, the describe element", ok, s)
	}
	reports := rowsByNativeID(t, st, TypeStorageGatewayCacheReport)
	for arn, name := range map[string]string{sgwTestReportTagged: "report-tagged", sgwTestReportBare: "report-bare"} {
		r, ok := reports[arn]
		if !ok || sv(r.Name) != name || sv(r.Status) != "COMPLETED" || sv(r.Region) != testRegion || r.CreatedAt != nil {
			t.Errorf("cache report %s stored %v: %+v; want %s, COMPLETED, no CreatedAt", arn, ok, r, name)
			continue
		}
		if got := sgwAttrKeys(t, r)["CacheReportARN"]; string(got) != strconv.Quote(arn) {
			t.Errorf("cache report %s attrs = %s; want the listed element", arn, r.AttributesJSON)
		}
	}
	assertTagsMap(t, reports[sgwTestReportTagged].TagsJSON, map[string]string{"type": TypeStorageGatewayCacheReport})
	if tg := reports[sgwTestReportBare].TagsJSON; tg != nil {
		t.Errorf("bare cache report TagsJSON = %q; want nil", *tg)
	}
	for typ, arn := range map[string]string{
		TypeStorageGatewayDevice:                      sgwTestDevice,
		TypeStorageGatewayAutomaticTapeCreationPolicy: sgwTestARN("gateway/sgw-tagged/automatic-tape-creation-policy"),
	} {
		if r, ok := rowsByNativeID(t, st, typ)[arn]; !ok || r.TagsJSON != nil {
			t.Errorf("%s stored %v; want stored untagged", arn, ok)
		}
	}
	if got := calls.sortedTagARNs(); !slices.Equal(got, sgwTagReadARNs()) {
		t.Errorf("ListTagsForResource ARNs = %v\nwant %v", got, sgwTagReadARNs())
	}
	for op, ins := range calls.inputs {
		for _, in := range ins {
			if n := bacField[*int32](in, "Limit"); n != nil {
				t.Errorf("%s Limit = %d; want none sent", op, *n)
			}
		}
	}
	// Each tagged gateway lists one page of devices: both gateways were stored.
	if n := calls.count("DescribeVTLDevices"); n != 2 {
		t.Errorf("DescribeVTLDevices calls = %d; want 2", n)
	}
}

// A rescan stores the same rows without inserting any.
func TestScanStorageGateway_RescanInsertsNone(t *testing.T) {
	st := newTestStore(t)
	if _, _, err := sgwScan(context.Background(), st, sgwHandlers(newSGWCalls())); err != nil {
		t.Fatalf("first scan: %v", err)
	}
	total, inserted, err := sgwScan(context.Background(), st, sgwHandlers(newSGWCalls()))
	if err != nil {
		t.Fatalf("rescan: %v", err)
	}
	if total != sgwTestTotal || inserted != 0 {
		t.Errorf("rescan total, inserted = %d, %d; want %d, 0", total, inserted, sgwTestTotal)
	}
}

// With nothing listed the scan stores nothing, reads no tags and warns
// nothing.
func TestScanStorageGateway_EmptyListings(t *testing.T) {
	st := newTestStore(t)
	warns := rhWarnRecorder(st)
	calls := newSGWCalls()
	handlers := sgwHandlers(calls)
	empty := map[string]any{
		"ListGateways": &storagegateway.ListGatewaysOutput{}, "ListVolumes": &storagegateway.ListVolumesOutput{},
		"ListFileShares": &storagegateway.ListFileSharesOutput{}, "ListTapes": &storagegateway.ListTapesOutput{},
		"ListTapePools": &storagegateway.ListTapePoolsOutput{}, "ListFileSystemAssociations": &storagegateway.ListFileSystemAssociationsOutput{},
		"ListCacheReports": &storagegateway.ListCacheReportsOutput{},
	}
	for op, out := range empty {
		handlers[op] = func(any) (any, error) { return out, nil }
	}
	total, _, err := sgwScan(context.Background(), st, handlers)
	if err != nil || total != 0 {
		t.Fatalf("total, err = %d, %v; want 0, nil", total, err)
	}
	if n := calls.count("ListTagsForResource"); n != 0 {
		t.Errorf("ListTagsForResource calls = %d; want 0", n)
	}
	if w := warns(); len(w) != 0 {
		t.Errorf("warnings = %v; want none", w)
	}
}

// A failed tag read keeps its row untagged: a denial warns once under the
// tag op; a refusal for the gateway's state (GatewayNotConnected, or
// GatewayNotFound for a gateway deleted since its listing) warns once under
// the tag op with no error; a throttle is transient; a request defect is an
// error. The op models no not-found code, so no row is dropped. The NFS share
// is read because its describe shows sgwDescribeTagsShown tags, and a failed
// read stores it without the describe's.
func TestScanStorageGateway_TagReadFailures(t *testing.T) {
	gatewayGone := &sgwtypes.InvalidGatewayRequestException{
		Message: ptrStr("The specified gateway was not found."),
		Error_:  &sgwtypes.StorageGatewayError{ErrorCode: sgwtypes.ErrorCodeGatewayNotFound},
	}
	type target struct{ typ, arn string }
	targets := []target{{TypeStorageGatewayShare, sgwTestShareTagged}}
	for _, k := range sgwKinds() {
		targets = append(targets, target{k.typ, k.arn("tagged")})
	}
	for _, tg := range targets {
		arn := tg.arn
		for _, c := range []struct {
			name    string
			err     error
			outcome serviceOutcome // -1: no error
			code    string
			warns   int
		}{
			{"denied", sgwDenied, -1, "", 1},
			{"gateway not connected", sgwGatewayDown, -1, "", 1},
			{"gateway not found", gatewayGone, -1, "", 1},
			{"transient", sgwThrottle, outcomeTransient, "ThrottlingException", 0},
			{"invalid parameters", sgwBadParams, outcomeError, sgwTestBadParams, 0},
		} {
			t.Run(tg.typ+"/"+c.name, func(t *testing.T) {
				st := newTestStore(t)
				warns := rhWarnRecorder(st)
				calls := newSGWCalls()
				calls.errs[arn] = c.err
				handlers := sgwHandlers(calls)
				handlers["DescribeNFSFileShares"] = sgwDescribeWithTags(sgwtypes.FileShareTypeNfs, sgwDescribeTagsShown)

				total, _, err := sgwScan(context.Background(), st, handlers)
				if c.outcome < 0 {
					if err != nil {
						t.Fatalf("err = %v; want nil", err)
					}
				} else {
					assertGlueOutcome(t, err, c.outcome)
					assertAPICode(t, err, c.code)
				}
				if total != sgwTestTotal {
					t.Errorf("total = %d; want %d", total, sgwTestTotal)
				}
				r, ok := rowsByNativeID(t, st, tg.typ)[arn]
				if !ok || r.TagsJSON != nil {
					t.Fatalf("row stored %v, TagsJSON %v; want stored untagged", ok, r.TagsJSON)
				}
				w := warns()
				if len(w) != c.warns {
					t.Fatalf("warnings = %v; want %d", w, c.warns)
				}
				for _, x := range w {
					if x.Service != sgwTagsOp || x.Scope != testAccountID+"/"+testRegion || !strings.Contains(x.Message, arn) {
						t.Errorf("warning %+v; want one under %s in %s/%s naming %s", x, sgwTagsOp, testAccountID, testRegion, arn)
					}
				}
			})
		}
	}
}

// Denied tag reads in two listings send one warning for the whole scan; the
// rows are kept untagged.
func TestScanStorageGateway_TagDenialsWarnOncePerScan(t *testing.T) {
	st := newTestStore(t)
	warnings := bacWarnings(st, sgwTagsOp)
	calls := newSGWCalls()
	for _, op := range []string{"ListVolumes", "ListTapePools"} {
		k := sgwKindOf(t, op)
		calls.errs[k.arn("tagged")] = sgwDenied
		calls.errs[k.arn("bare")] = sgwDenied
	}

	total, _, err := sgwScan(context.Background(), st, sgwHandlers(calls))
	if err != nil || total != sgwTestTotal {
		t.Fatalf("total, err = %d, %v; want %d, nil", total, err, sgwTestTotal)
	}
	if len(*warnings) != 1 || (*warnings)[0].Scope != testAccountID+"/"+testRegion {
		t.Fatalf("%s warnings = %v; want 1 in %s/%s", sgwTagsOp, *warnings, testAccountID, testRegion)
	}
	for _, op := range []string{"ListVolumes", "ListTapePools"} {
		k := sgwKindOf(t, op)
		for _, arn := range []string{k.arn("tagged"), k.arn("bare")} {
			if r, ok := rowsByNativeID(t, st, k.typ)[arn]; !ok || r.TagsJSON != nil {
				t.Errorf("%s stored %v; want stored untagged", arn, ok)
			}
		}
	}
}

// Tag reads refused for their gateway's state in two listings warn once for
// the scan, counting all three and naming the smallest ARN of the first
// listing, whichever of its reads finished first.
func TestScanStorageGateway_GatewayRefusalsWarnOnce(t *testing.T) {
	st := newTestStore(t)
	warnings := bacWarnings(st, sgwTagsOp)
	calls := newSGWCalls()
	vol := sgwKindOf(t, "ListVolumes")
	calls.errs[vol.arn("tagged")] = sgwGatewayDown
	calls.errs[vol.arn("bare")] = sgwGatewayDown
	calls.errs[sgwKindOf(t, "ListFileSystemAssociations").arn("tagged")] = sgwGatewayDown

	if _, _, err := sgwScan(context.Background(), st, sgwHandlers(calls)); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if len(*warnings) != 1 || !strings.HasPrefix((*warnings)[0].Message, "3 ") || !strings.Contains((*warnings)[0].Message, vol.arn("bare")) {
		t.Fatalf("%s warnings = %v; want 1 counting 3, naming %s", sgwTagsOp, *warnings, vol.arn("bare"))
	}
}

// A failed write warns about the refusals of the phases written before it,
// not about the refusal or denial of its own phase: their rows may be ones
// the write lost. Both tape pools are on the one page whose write fails.
func TestScanStorageGateway_FailedWriteWarnsOnlyWrittenPhases(t *testing.T) {
	st := newTestStore(t)
	warns := rhWarnRecorder(st)
	refuseStoreWrites(t, st, TypeStorageGatewayTapePool)
	calls := newSGWCalls()
	vol := sgwKindOf(t, "ListVolumes").arn("bare")
	pool := sgwKindOf(t, "ListTapePools")
	calls.errs[vol] = sgwGatewayDown
	calls.errs[pool.arn("tagged")] = sgwDenied
	calls.errs[pool.arn("bare")] = sgwGatewayDown
	handlers := sgwHandlers(calls)
	handlers["ListTapePools"] = func(any) (any, error) {
		return &storagegateway.ListTapePoolsOutput{PoolInfos: []sgwtypes.PoolInfo{
			{PoolARN: ptrStr(pool.arn("tagged")), PoolName: ptrStr("pool-tagged")},
			{PoolARN: ptrStr(pool.arn("bare")), PoolName: ptrStr("pool-bare")},
		}}, nil
	}

	_, _, err := sgwScan(context.Background(), st, handlers)
	if got := calls.sortedTagARNs(); !slices.Contains(got, pool.arn("tagged")) || !slices.Contains(got, pool.arn("bare")) {
		t.Fatalf("ListTagsForResource ARNs = %v; want both tape pools read", got)
	}
	if !errors.Is(err, store.ErrStoreWrite) {
		t.Fatalf("err = %v; want store.ErrStoreWrite", err)
	}
	w := warns()
	if len(w) != 1 || w[0].Service != sgwTagsOp || w[0].Scope != testAccountID+"/"+testRegion ||
		!strings.HasPrefix(w[0].Message, "1 ") || !strings.Contains(w[0].Message, vol) {
		t.Fatalf("warnings = %v; want 1 under %s counting 1, naming %s", w, sgwTagsOp, vol)
	}
	if r, ok := rowsByNativeID(t, st, TypeStorageGatewayVolume)[vol]; !ok || r.TagsJSON != nil {
		t.Errorf("%s stored %v; want stored untagged", vol, ok)
	}
}

// A throttled tag read beside a hard failure of the same op is demoted to one
// warning, so the hard error decides the outcome.
func TestScanStorageGateway_TagThrottleDemotedBesideHardError(t *testing.T) {
	st := newTestStore(t)
	warns := countOpWarnings(st, sgwTagsOp)
	calls := newSGWCalls()
	calls.errs[sgwKindOf(t, "ListGateways").arn("tagged")] = sgwThrottle
	calls.errs[sgwKindOf(t, "ListTapes").arn("bare")] = sgwBadParams

	_, _, err := sgwScan(context.Background(), st, sgwHandlers(calls))
	assertGlueOutcome(t, err, outcomeError)
	assertAPICode(t, err, sgwTestBadParams)
	if *warns != 1 {
		t.Errorf("%s warnings = %d; want 1", sgwTagsOp, *warns)
	}
}

// A failed write stops the scan: no later phase runs; the phases before it
// keep their rows.
func TestScanStorageGateway_FailedWriteStopsScan(t *testing.T) {
	st := newTestStore(t)
	refuseStoreWrites(t, st, TypeStorageGatewayTape)
	calls := newSGWCalls()

	total, _, err := sgwScan(context.Background(), st, sgwHandlers(calls))
	if !errors.Is(err, store.ErrStoreWrite) {
		t.Fatalf("err = %v; want store.ErrStoreWrite", err)
	}
	if n := calls.count("ListTapes"); n != 1 {
		t.Errorf("ListTapes called %d times; want 1, none after the failed write", n)
	}
	for _, op := range []string{"ListTapePools", "ListFileSystemAssociations", "ListCacheReports", "DescribeVTLDevices", "ListAutomaticTapeCreationPolicies"} {
		if n := calls.count(op); n != 0 {
			t.Errorf("%s called %d times after the failed write; want 0", op, n)
		}
	}
	// Gateways, volumes and file shares (2 each) ran before it.
	if total != 3*2 {
		t.Errorf("total = %d; want %d", total, 3*2)
	}
}

// A listing's failed page keeps the page read before it, and its error does
// not stop the later listings. A failed gateway listing hands the gateway
// phases only the gateways it stored.
func TestScanStorageGateway_ListFailureKeepsPagesAndLaterPhases(t *testing.T) {
	for _, k := range sgwKinds() {
		t.Run(k.op, func(t *testing.T) {
			st := newTestStore(t)
			calls := newSGWCalls()
			handlers := sgwHandlers(calls)
			handlers[k.op] = failOnCall(2, sgwBadParams, handlers[k.op])

			total, _, err := sgwScan(context.Background(), st, handlers)
			assertGlueOutcome(t, err, outcomeError)
			assertAPICode(t, err, sgwTestBadParams)
			if total != sgwTestTotal-1 {
				t.Errorf("total = %d; want %d", total, sgwTestTotal-1)
			}
			if r, ok := rowsByNativeID(t, st, k.typ)[k.arn("tagged")]; !ok {
				t.Error("row from the page before the failure missing")
			} else {
				assertTagsMap(t, r.TagsJSON, map[string]string{"type": k.typ})
			}
			if n := len(rowsByNativeID(t, st, TypeStorageGatewayAutomaticTapeCreationPolicy)); n != 1 {
				t.Errorf("tape creation policies (the last phase) = %d; want 1", n)
			}
			wantDevices := 2
			if k.op == "ListGateways" {
				wantDevices = 1
			}
			if n := calls.count("DescribeVTLDevices"); n != wantDevices {
				t.Errorf("DescribeVTLDevices calls = %d; want %d, one per stored gateway", n, wantDevices)
			}
		})
	}
	t.Run("ListGateways first page", func(t *testing.T) {
		st := newTestStore(t)
		calls := newSGWCalls()
		handlers := sgwHandlers(calls)
		handlers["ListGateways"] = func(any) (any, error) { return nil, sgwBadParams }

		total, _, err := sgwScan(context.Background(), st, handlers)
		assertAPICode(t, err, sgwTestBadParams)
		// Only the device and policy phases, which read per gateway, lose
		// their rows.
		if want := sgwTestTotal - 2 - 2; total != want {
			t.Errorf("total = %d; want %d", total, want)
		}
		if n := calls.count("DescribeVTLDevices") + calls.count("ListAutomaticTapeCreationPolicies"); n != 0 {
			t.Errorf("gateway phases called %d times; want 0 with no gateway stored", n)
		}
	})
}

// A repeated marker ends a listing as an error, keeping its pages.
func TestScanStorageGateway_RepeatedMarkerKeepsPages(t *testing.T) {
	for op, field := range map[string]string{
		"ListTapes": "Marker", "ListFileSystemAssociations": "NextMarker", "ListFileShares": "NextMarker", "ListCacheReports": "Marker",
	} {
		t.Run(op, func(t *testing.T) {
			st := newTestStore(t)
			calls := newSGWCalls()
			handlers := sgwHandlers(calls)
			next := handlers[op]
			handlers[op] = func(in any) (any, error) {
				out, err := next(in)
				reflect.ValueOf(out).Elem().FieldByName(field).Set(reflect.ValueOf(ptrStr("p2")))
				return out, err
			}
			total, _, err := sgwScan(context.Background(), st, handlers)
			if !errors.Is(err, errRepeatedNextToken) {
				t.Fatalf("err = %v; want errRepeatedNextToken", err)
			}
			if total != sgwTestTotal {
				t.Errorf("total = %d; want %d", total, sgwTestTotal)
			}
			if n := calls.count(op); n != 2 {
				t.Errorf("%s calls = %d; want 2", op, n)
			}
		})
	}
}

// Every page of a resource's tags is read; a repeated marker keeps the row
// untagged and is an error.
func TestScanStorageGateway_TagPages(t *testing.T) {
	arn := sgwKindOf(t, "ListVolumes").arn("bare")
	pageOf := func(next *string, k, v string) *storagegateway.ListTagsForResourceOutput {
		return &storagegateway.ListTagsForResourceOutput{Marker: next, Tags: []sgwtypes.Tag{{Key: ptrStr(k), Value: ptrStr(v)}}}
	}
	for _, c := range []struct {
		name     string
		second   *string
		wantTags map[string]string
	}{
		{"every page read", nil, map[string]string{"a": "1", "b": "2"}},
		{"repeated marker", ptrStr("t2"), nil},
	} {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			calls := newSGWCalls()
			handlers := sgwHandlers(calls)
			handlers["ListTagsForResource"] = func(in any) (any, error) {
				req := in.(*storagegateway.ListTagsForResourceInput)
				if sv(req.ResourceARN) != arn {
					return calls.listTags(in)
				}
				if req.Marker == nil {
					return pageOf(ptrStr("t2"), "a", "1"), nil
				}
				if sv(req.Marker) != "t2" {
					return nil, sgwBadParams
				}
				return pageOf(c.second, "b", "2"), nil
			}
			total, _, err := sgwScan(context.Background(), st, handlers)
			if total != sgwTestTotal {
				t.Errorf("total = %d; want %d", total, sgwTestTotal)
			}
			r := rowsByNativeID(t, st, TypeStorageGatewayVolume)[arn]
			if c.wantTags == nil {
				if !errors.Is(err, errRepeatedNextToken) {
					t.Fatalf("err = %v; want errRepeatedNextToken", err)
				}
				if r.TagsJSON != nil {
					t.Errorf("TagsJSON = %q; want nil", *r.TagsJSON)
				}
				return
			}
			if err != nil {
				t.Fatalf("scan: %v", err)
			}
			assertTagsMap(t, r.TagsJSON, c.wantTags)
		})
	}
}

// An element without an ARN is not stored, is not sent to the tag op, and is
// warned about once under its listing, named by its id or name, or by the
// fallback text when it has none.
func TestScanStorageGateway_ElementWithoutARNWarns(t *testing.T) {
	for _, c := range []struct {
		name, op, key string
		answer        func(in any) (any, error)
	}{
		{"volume", "ListVolumes", "vol-x", func(any) (any, error) {
			return &storagegateway.ListVolumesOutput{VolumeInfos: []sgwtypes.VolumeInfo{{VolumeId: ptrStr("vol-x")}}}, nil
		}},
		{"gateway without a name", "ListGateways", noIdentifier, func(any) (any, error) {
			return &storagegateway.ListGatewaysOutput{Gateways: []sgwtypes.GatewayInfo{{}}}, nil
		}},
		{"file share", "ListFileShares", "share-x", func(any) (any, error) {
			return &storagegateway.ListFileSharesOutput{FileShareInfoList: []sgwtypes.FileShareInfo{{FileShareId: ptrStr("share-x"), FileShareType: sgwtypes.FileShareTypeNfs}}}, nil
		}},
		{"cache report", "ListCacheReports", "report-x", func(any) (any, error) {
			return &storagegateway.ListCacheReportsOutput{CacheReportList: []sgwtypes.CacheReportInfo{{ReportName: ptrStr("report-x")}}}, nil
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := rhWarnRecorder(st)
			calls := newSGWCalls()
			handlers := sgwHandlers(calls)
			handlers[c.op] = c.answer

			total, _, err := sgwScan(context.Background(), st, handlers)
			if err != nil {
				t.Fatalf("scan: %v", err)
			}
			// The listing's two rows are gone; a gateway listing also takes
			// the device and policy of the tagged gateway with it.
			lost := 2
			if c.op == "ListGateways" {
				lost = 4
			}
			if want := sgwTestTotal - lost; total != want {
				t.Errorf("total = %d; want %d", total, want)
			}
			w := warns()
			if len(w) != 1 {
				t.Fatalf("warnings = %v; want 1", w)
			}
			if w[0].Service != "storagegateway:"+c.op || !strings.Contains(w[0].Message, c.key) || w[0].Scope != testAccountID+"/"+testRegion {
				t.Errorf("warning %+v; want one under storagegateway:%s naming %q in %s/%s", w[0], c.op, c.key, testAccountID, testRegion)
			}
			if slices.Contains(calls.sortedTagARNs(), "") {
				t.Error("an empty ARN was sent to ListTagsForResource")
			}
			if n := calls.count("DescribeNFSFileShares"); c.op == "ListFileShares" && n != 0 {
				t.Errorf("DescribeNFSFileShares calls = %d; want 0 for a share without an ARN", n)
			}
		})
	}
}

// Rows of the aws-us-gov and aws-cn partitions reach the tag op under their
// listed ARNs unchanged.
func TestScanStorageGateway_TagReadsKeepPartitionARNs(t *testing.T) {
	for _, partition := range []string{"aws-us-gov", "aws-cn"} {
		t.Run(partition, func(t *testing.T) {
			st := newTestStore(t)
			calls := newSGWCalls()
			handlers := sgwHandlers(calls)
			acct := partitionAccount(partition)
			region := acct.Regions[0]
			arn := "arn:" + partition + ":storagegateway:" + region + ":" + testAccountID + ":tape/TAPEP"
			calls.tags[arn] = map[string]string{"k": "v"}
			handlers["ListTapes"] = func(any) (any, error) {
				return &storagegateway.ListTapesOutput{TapeInfos: []sgwtypes.TapeInfo{{TapeARN: &arn, TapeBarcode: ptrStr("TAPEP"), GatewayARN: ptrStr("g")}}}, nil
			}
			acct.cfg = opStubConfig(handlers, sgwDenied)
			if _, _, err := scanStorageGateway(context.Background(), acct, region, st, testScanID); err != nil {
				t.Fatalf("scan: %v", err)
			}
			if !slices.Contains(calls.sortedTagARNs(), arn) {
				t.Errorf("ListTagsForResource ARNs %v lack %s", calls.sortedTagARNs(), arn)
			}
			assertTagsMap(t, rowsByNativeID(t, st, TypeStorageGatewayTape)[arn].TagsJSON, map[string]string{"k": "v"})
		})
	}
}

// Once ctx ends no further phase starts, and the stop is one member of the
// error, although the phase in flight saw it in its listing.
func TestScanStorageGateway_ContextEndReportedOnce(t *testing.T) {
	st := newTestStore(t)
	calls := newSGWCalls()
	handlers := sgwHandlers(calls)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	list := handlers["ListTapes"]
	handlers["ListTapes"] = func(in any) (any, error) {
		if bacField[*string](in, "Marker") != nil {
			cancel()
			return nil, context.Canceled
		}
		return list(in)
	}

	_, _, err := sgwScan(ctx, st, handlers)
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
	if n := calls.count("ListTapePools"); n != 0 {
		t.Errorf("ListTapePools called %d times after the stop; want 0", n)
	}
}

// An NXDOMAIN after rows were stored is a failure of this scan, not a region
// gap: the counts are kept. With nothing stored it stays region-unavailable.
func TestScanStorageGateway_NXDOMAIN(t *testing.T) {
	nx := &net.DNSError{Err: "no such host", Name: "storagegateway.us-east-1.amazonaws.com", IsNotFound: true}
	t.Run("a listing after others stored rows", func(t *testing.T) {
		st := newTestStore(t)
		handlers := sgwHandlers(newSGWCalls())
		handlers["ListTapePools"] = func(any) (any, error) { return nil, nx }
		total, _, err := sgwScan(context.Background(), st, handlers)
		assertGlueOutcome(t, err, outcomeError)
		if total != sgwTestTotal-2 {
			t.Errorf("total = %d; want the %d other rows counted", total, sgwTestTotal-2)
		}
	})
	t.Run("file share describe after the listing", func(t *testing.T) {
		st := newTestStore(t)
		warns := countOpWarnings(st, "storagegateway:DescribeNFSFileShares")
		handlers := sgwHandlers(newSGWCalls())
		handlers["DescribeNFSFileShares"] = func(any) (any, error) { return nil, nx }
		total, _, err := sgwScan(context.Background(), st, handlers)
		assertGlueOutcome(t, err, outcomeError)
		if total != sgwTestTotal || *warns != 0 {
			t.Errorf("total, describe warnings = %d, %d; want %d, 0", total, *warns, sgwTestTotal)
		}
	})
	t.Run("tag read after the listing", func(t *testing.T) {
		st := newTestStore(t)
		calls := newSGWCalls()
		calls.errs[sgwKindOf(t, "ListGateways").arn("bare")] = nx
		_, _, err := sgwScan(context.Background(), st, sgwHandlers(calls))
		assertGlueOutcome(t, err, outcomeError)
	})
	t.Run("nothing stored", func(t *testing.T) {
		st := newTestStore(t)
		warns := rhWarnRecorder(st)
		acct := newTestAccount(testAccountID)
		acct.cfg = opStubConfig(nil, nx)
		_, _, err := scanStorageGateway(context.Background(), acct, testRegion, st, testScanID)
		assertGlueOutcome(t, err, outcomeUnavailable)
		if w := warns(); len(w) != 0 {
			t.Errorf("warnings = %v; want none", w)
		}
	})
}

// sgwDescribeWithTags answers the describe of typ with each share showing n
// tags, "dNN": "describe".
func sgwDescribeWithTags(typ sgwtypes.FileShareType, n int) func(any) (any, error) {
	var tags []sgwtypes.Tag
	for i := range n {
		tags = append(tags, sgwtypes.Tag{Key: ptrStr(fmt.Sprintf("d%02d", i)), Value: ptrStr("describe")})
	}
	return func(in any) (any, error) {
		if typ == sgwtypes.FileShareTypeSmb {
			out := &storagegateway.DescribeSMBFileSharesOutput{}
			for _, a := range in.(*storagegateway.DescribeSMBFileSharesInput).FileShareARNList {
				out.SMBFileShareInfoList = append(out.SMBFileShareInfoList, sgwtypes.SMBFileShareInfo{FileShareARN: ptrStr(a), Tags: tags})
			}
			return out, nil
		}
		out := &storagegateway.DescribeNFSFileSharesOutput{}
		for _, a := range in.(*storagegateway.DescribeNFSFileSharesInput).FileShareARNList {
			out.NFSFileShareInfoList = append(out.NFSFileShareInfoList, sgwtypes.NFSFileShareInfo{FileShareARN: ptrStr(a), Tags: tags})
		}
		return out, nil
	}
}

// A file share's tags come from its describe, NFS or SMB, only when it shows
// fewer than sgwDescribeTagsShown, which it may have cut short; a share
// showing that many, or whose describe failed, has them read with
// ListTagsForResource.
func TestScanSGWFileShares_TagSource(t *testing.T) {
	read := map[string]string{"source": "ListTagsForResource"}
	described := func(n int) map[string]string {
		m := map[string]string{}
		for i := range n {
			m[fmt.Sprintf("d%02d", i)] = "describe"
		}
		return m
	}
	for typ, op := range map[sgwtypes.FileShareType]string{sgwtypes.FileShareTypeNfs: "DescribeNFSFileShares", sgwtypes.FileShareTypeSmb: "DescribeSMBFileShares"} {
		for _, c := range []struct {
			name     string
			describe func(any) (any, error)
			wantRead bool
			wantTags map[string]string
		}{
			{"no tags shown", sgwDescribeWithTags(typ, 0), false, nil},
			{"fewer than the cut shown", sgwDescribeWithTags(typ, sgwDescribeTagsShown-1), false, described(sgwDescribeTagsShown - 1)},
			{"as many as the cut shown", sgwDescribeWithTags(typ, sgwDescribeTagsShown), true, read},
			{"describe denied", func(any) (any, error) { return nil, sgwDenied }, true, read},
		} {
			t.Run(string(typ)+"/"+c.name, func(t *testing.T) {
				st := newTestStore(t)
				calls := newSGWCalls()
				calls.tags[sgwShareARN("a")] = read
				total, err := scanSGWSharesForTest(t, st, map[string]func(any) (any, error){
					"ListFileShares":      sgwListShares([]sgwtypes.FileShareInfo{sgwShare("a", "gw1", typ)}),
					op:                    c.describe,
					"ListTagsForResource": calls.listTags,
				})
				if err != nil || total != 1 {
					t.Fatalf("total, err = %d, %v; want 1, nil", total, err)
				}
				if got := calls.sortedTagARNs(); (len(got) == 1) != c.wantRead || (c.wantRead && got[0] != sgwShareARN("a")) {
					t.Errorf("ListTagsForResource ARNs = %v; want read %v", got, c.wantRead)
				}
				r := rowsByNativeID(t, st, TypeStorageGatewayShare)[sgwShareARN("a")]
				if c.wantTags == nil {
					if r.TagsJSON != nil {
						t.Errorf("TagsJSON = %q; want nil", *r.TagsJSON)
					}
					return
				}
				assertTagsMap(t, r.TagsJSON, c.wantTags)
			})
		}
	}
}

// After a failed write the describes warn nothing: the warning would name
// shares the write lost. Their hard errors are still returned.
func TestScanSGWFileShares_FailedWriteWarnsNothing(t *testing.T) {
	st := newTestStore(t)
	warns := rhWarnRecorder(st)
	refuseStoreWrites(t, st, TypeStorageGatewayShare)
	_, err := scanSGWSharesForTest(t, st, map[string]func(any) (any, error){
		"ListFileShares": sgwListShares([]sgwtypes.FileShareInfo{
			sgwShare("a", "gw1", sgwtypes.FileShareTypeNfs), sgwShare("b", "gw2", sgwtypes.FileShareTypeSmb),
		}),
		"DescribeNFSFileShares": func(any) (any, error) { return nil, sgwDenied },
		"DescribeSMBFileShares": func(any) (any, error) { return nil, sgwBadParams },
	})
	if !errors.Is(err, store.ErrStoreWrite) || !errors.Is(err, sgwBadParams) {
		t.Fatalf("err = %v; want store.ErrStoreWrite and the SMB describe error", err)
	}
	if w := warns(); len(w) != 0 {
		t.Errorf("warnings = %v; want none", w)
	}
}

// A listing whose later page fails keeps the shares of the page before it,
// described and stored.
func TestScanSGWFileShares_LaterPageFailureKeepsShares(t *testing.T) {
	st := newTestStore(t)
	var nfs sgwBatches
	total, err := scanSGWSharesForTest(t, st, map[string]func(any) (any, error){
		"ListFileShares": failOnCall(2, sgwBadParams, sgwListShares(
			[]sgwtypes.FileShareInfo{sgwShare("a", "gw1", sgwtypes.FileShareTypeNfs)},
			[]sgwtypes.FileShareInfo{sgwShare("b", "gw1", sgwtypes.FileShareTypeNfs)},
		)),
		"DescribeNFSFileShares": sgwDescribeNFS(&nfs),
	})
	if !errors.Is(err, sgwBadParams) || total != 1 {
		t.Fatalf("total, err = %d, %v; want 1 and the page error", total, err)
	}
	if k := sgwAttrKeys(t, rowsByNativeID(t, st, TypeStorageGatewayShare)[sgwShareARN("a")]); string(k["Path"]) != `"/nfs"` {
		t.Error("share from the page before the failure is not its describe element")
	}
}

// A shelved tape keeps its archive attributes and has its tags read. After a
// failed write the archive read's error, a denial or a throttle, warns
// nothing and is returned with the write's.
func TestScanSGWTapes_ShelvedTapeTagsAndFailedWrite(t *testing.T) {
	t.Run("tags", func(t *testing.T) {
		st := newTestStore(t)
		calls := newSGWCalls()
		calls.tags[sgwTapeARN("VTS1")] = map[string]string{"k": "v"}
		total, err := scanSGWTapesForTest(t, st, map[string]func(any) (any, error){
			"ListTapes":            sgwListTapes([]sgwtypes.TapeInfo{sgwTape("VTS1", "")}),
			"DescribeTapeArchives": sgwDescribeArchives([]sgwtypes.TapeArchive{sgwArchive("VTS1")}),
			"ListTagsForResource":  calls.listTags,
		})
		if err != nil || total != 1 {
			t.Fatalf("total, err = %d, %v; want 1, nil", total, err)
		}
		r := rowsByNativeID(t, st, TypeStorageGatewayTape)[sgwTapeARN("VTS1")]
		if sgwAttrKeys(t, r)["KMSKey"] == nil {
			t.Errorf("attrs = %s; want the archive element", r.AttributesJSON)
		}
		assertTagsMap(t, r.TagsJSON, map[string]string{"k": "v"})
	})
	for name, archiveErr := range map[string]error{"denied": sgwDenied, "throttled": sgwThrottle} {
		t.Run("failed write/"+name, func(t *testing.T) {
			st := newTestStore(t)
			warns := rhWarnRecorder(st)
			refuseStoreWrites(t, st, TypeStorageGatewayTape)
			_, err := scanSGWTapesForTest(t, st, map[string]func(any) (any, error){
				"ListTapes":            sgwListTapes([]sgwtypes.TapeInfo{sgwTape("VTS1", "")}),
				"DescribeTapeArchives": func(any) (any, error) { return nil, archiveErr },
			})
			if !errors.Is(err, store.ErrStoreWrite) || !errors.Is(err, archiveErr) {
				t.Fatalf("err = %v; want store.ErrStoreWrite and the archive error", err)
			}
			if w := warns(); len(w) != 0 {
				t.Errorf("warnings = %v; want none", w)
			}
		})
	}
}

// The archives are read once, on the first page with a shelved tape, and a
// repeated archive marker is an error that keeps the tapes.
func TestScanSGWTapes_ArchivesReadOnce(t *testing.T) {
	t.Run("once", func(t *testing.T) {
		st := newTestStore(t)
		calls := 0
		archives := sgwDescribeArchives([]sgwtypes.TapeArchive{sgwArchive("VTS1")}, []sgwtypes.TapeArchive{sgwArchive("VTS2")})
		total, err := scanSGWTapesForTest(t, st, map[string]func(any) (any, error){
			"ListTapes": sgwListTapes([]sgwtypes.TapeInfo{sgwTape("LIB1", "gw1")}, []sgwtypes.TapeInfo{sgwTape("VTS1", "")}, []sgwtypes.TapeInfo{sgwTape("VTS2", "")}),
			"DescribeTapeArchives": func(in any) (any, error) {
				calls++
				return archives(in)
			},
		})
		if err != nil || total != 3 || calls != 2 {
			t.Fatalf("total, err, archive pages = %d, %v, %d; want 3, nil, 2", total, err, calls)
		}
		if k := sgwAttrKeys(t, rowsByNativeID(t, st, TypeStorageGatewayTape)[sgwTapeARN("VTS2")]); k["KMSKey"] == nil {
			t.Error("VTS2 is not its archive element")
		}
	})
	t.Run("repeated marker", func(t *testing.T) {
		st := newTestStore(t)
		total, err := scanSGWTapesForTest(t, st, map[string]func(any) (any, error){
			"ListTapes": sgwListTapes([]sgwtypes.TapeInfo{sgwTape("VTS1", "")}),
			"DescribeTapeArchives": func(any) (any, error) {
				return &storagegateway.DescribeTapeArchivesOutput{Marker: ptrStr("same"), TapeArchives: []sgwtypes.TapeArchive{sgwArchive("VTS1")}}, nil
			},
		})
		if !errors.Is(err, errRepeatedNextToken) || total != 1 {
			t.Fatalf("total, err = %d, %v; want 1, errRepeatedNextToken", total, err)
		}
	})
}

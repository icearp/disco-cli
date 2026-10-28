package aws

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"testing"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/transfer"
	transfertypes "github.com/aws/aws-sdk-go-v2/service/transfer/types"
	"github.com/icearp/disco-cli/store"
)

var transferDenied = apiErr("AccessDeniedException", "User: arn:aws:iam::123:user/x is not authorized to perform: transfer:List")

func transferTestServerARN(id string) string {
	return "arn:aws:transfer:" + testRegion + ":" + testAccountID + ":server/" + id
}

func transferTestServers(ids ...string) []childParent {
	out := make([]childParent, len(ids))
	for i, id := range ids {
		out[i] = childParent{id: id, arn: transferTestServerARN(id)}
	}
	return out
}

// seedTransferServers stores the server rows the access closure pairs point at.
func seedTransferServers(t *testing.T, st *store.Store, servers []childParent) {
	t.Helper()
	for _, s := range servers {
		upsertTestResource(t, st, "aws", testAccountID, TypeTransferServer, s.arn, testRegion, fmt.Sprintf(`{"ServerId":%q}`, s.id))
	}
}

func transferDescribedAccess(externalID string) *transfertypes.DescribedAccess {
	return &transfertypes.DescribedAccess{
		ExternalId:        sdkaws.String(externalID),
		Role:              sdkaws.String("arn:aws:iam::" + testAccountID + ":role/tx-" + externalID),
		Policy:            sdkaws.String(`{"Version":"2012-10-17"}`),
		HomeDirectoryType: transfertypes.HomeDirectoryTypeLogical,
		HomeDirectoryMappings: []transfertypes.HomeDirectoryMapEntry{
			{Entry: sdkaws.String("/"), Target: sdkaws.String("/bucket/home")},
		},
		PosixProfile: &transfertypes.PosixProfile{Uid: sdkaws.Int64(1001), Gid: sdkaws.Int64(2001)},
	}
}

// transferAccessHandlers answers ListAccesses from pages of ExternalIds keyed by
// server id (the first page carries NextToken "p2" when there is a second) and
// DescribeAccess from transferDescribedAccess. listErrs fails a server's list
// call; descErrs fails the describe of a "server/externalId" key. A list call
// whose MaxResults is not the model cap fails, so the page size is pinned.
func transferAccessHandlers(calls *parentCalls, pages map[string][][]string, listErrs, descErrs map[string]error) map[string]func(any) (any, error) {
	return map[string]func(any) (any, error){
		"ListAccesses": func(in any) (any, error) {
			req := in.(*transfer.ListAccessesInput)
			if sdkaws.ToInt32(req.MaxResults) != 1000 {
				return nil, fmt.Errorf("MaxResults = %v; want 1000", req.MaxResults)
			}
			id := sdkaws.ToString(req.ServerId)
			calls.add(id)
			if err := listErrs[id]; err != nil {
				return nil, err
			}
			page := 0
			if sdkaws.ToString(req.NextToken) == "p2" {
				page = 1
			}
			out := &transfer.ListAccessesOutput{ServerId: req.ServerId}
			if page < len(pages[id]) {
				for _, ext := range pages[id][page] {
					out.Accesses = append(out.Accesses, transfertypes.ListedAccess{ExternalId: sdkaws.String(ext)})
				}
			}
			if page == 0 && len(pages[id]) > 1 {
				out.NextToken = sdkaws.String("p2")
			}
			return out, nil
		},
		"DescribeAccess": func(in any) (any, error) {
			req := in.(*transfer.DescribeAccessInput)
			key := sdkaws.ToString(req.ServerId) + "/" + sdkaws.ToString(req.ExternalId)
			if err := descErrs[key]; err != nil {
				return nil, err
			}
			return &transfer.DescribeAccessOutput{ServerId: req.ServerId, Access: transferDescribedAccess(sdkaws.ToString(req.ExternalId))}, nil
		},
	}
}

func runTransferAccesses(t *testing.T, st *store.Store, handlers map[string]func(any) (any, error), servers []childParent) (int, error) {
	t.Helper()
	seedTransferServers(t, st, servers)
	client := transfer.NewFromConfig(opStubConfig(handlers, transferDenied))
	total, _, err := scanTransferAccesses(context.Background(), client, newTestAccount(testAccountID), testRegion, st, testScanID, servers)
	return total, err
}

func TestScanTransferAccesses_Fields(t *testing.T) {
	st := newTestStore(t)
	var calls parentCalls
	handlers := transferAccessHandlers(&calls, map[string][][]string{
		"s-1": {{"S-1-5-21-1", ""}, {"S-1-5-21-2"}},
		"s-2": {{"S-1-5-21-3"}},
	}, nil, nil)

	total, err := runTransferAccesses(t, st, handlers, transferTestServers("s-1", "s-2"))
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != 3 {
		t.Errorf("total = %d; want 3", total)
	}
	if got, want := calls.distinct(), []string{"s-1", "s-2"}; !reflect.DeepEqual(got, want) {
		t.Errorf("servers queried = %v; want %v", got, want)
	}
	rows := rowsByNativeID(t, st, TypeTransferAccess)
	if len(rows) != 3 {
		t.Fatalf("stored %d accesses; want 3: %v", len(rows), rows)
	}
	for _, c := range []struct{ server, ext string }{{"s-1", "S-1-5-21-1"}, {"s-1", "S-1-5-21-2"}, {"s-2", "S-1-5-21-3"}} {
		nativeID := transferTestServerARN(c.server) + "/access/" + c.ext
		r, ok := rows[nativeID]
		if !ok {
			t.Errorf("missing access %s", nativeID)
			continue
		}
		if r.Type != TypeTransferAccess || sv(r.Region) != testRegion || sv(r.Name) != c.ext {
			t.Errorf("%s: type %q region %q name %q", nativeID, r.Type, sv(r.Region), sv(r.Name))
		}
		assertContainsNative(t, st, transferTestServerARN(c.server), nativeID)
	}
	var attrs transfertypes.DescribedAccess
	r := rows[transferTestServerARN("s-1")+"/access/S-1-5-21-2"]
	if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
		t.Fatalf("decode attrs: %v", err)
	}
	if !reflect.DeepEqual(&attrs, transferDescribedAccess("S-1-5-21-2")) {
		t.Errorf("attrs = %s; want the DescribeAccess body", r.AttributesJSON)
	}
}

func TestScanTransferAccesses_Empty(t *testing.T) {
	for _, tc := range []struct {
		name    string
		servers []childParent
	}{
		{"no servers", nil},
		{"server without accesses", transferTestServers("s-1")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			warnings := countOpWarnings(st, transferListAccessesOp)
			var calls parentCalls
			total, err := runTransferAccesses(t, st, transferAccessHandlers(&calls, nil, nil, nil), tc.servers)
			if err != nil || total != 0 {
				t.Fatalf("total %d err %v; want 0, nil", total, err)
			}
			if n := len(rowsByNativeID(t, st, TypeTransferAccess)); n != 0 {
				t.Errorf("stored %d accesses; want 0", n)
			}
			if *warnings != 0 {
				t.Errorf("warnings = %d; want 0", *warnings)
			}
		})
	}
}

// TestScanTransferAccesses_PerServerErrors pins the per-server error policy:
// each case fails s-1 and expects s-2's access stored, with the given number
// of warnings for the failing op, or the phase error.
func TestScanTransferAccesses_PerServerErrors(t *testing.T) {
	conflict := apiErr("ConflictException", "conflict")
	cases := []struct {
		name     string
		listErrs map[string]error
		descErrs map[string]error
		warnOp   string
		warns    int
		wantErr  string
	}{
		{name: "server gone", listErrs: map[string]error{"s-1": apiErr("ResourceNotFoundException", "gone")}, warnOp: transferListAccessesOp},
		{name: "access gone", descErrs: map[string]error{"s-1/S-1-5-21-1": apiErr("ResourceNotFoundException", "gone")}, warnOp: transferDescribeAccessOp},
		{name: "list denied", listErrs: map[string]error{"s-1": transferDenied, "s-3": transferDenied}, warnOp: transferListAccessesOp, warns: 1},
		{
			name: "describe denied", warnOp: transferDescribeAccessOp, warns: 1,
			descErrs: map[string]error{"s-1/S-1-5-21-1": transferDenied, "s-3/S-1-5-21-4": transferDenied},
		},
		{name: "server not ready", listErrs: map[string]error{"s-1": apiErr("InvalidRequestException", "server is OFFLINE")}, warnOp: transferListAccessesOp, warns: 1},
		{name: "list throttled", listErrs: map[string]error{"s-1": apiErr("ThrottlingException", "slow down")}, warnOp: transferListAccessesOp, warns: 1},
		// A describe error that skips the server is counted by the ListAccesses fan-out.
		{name: "describe throttled", descErrs: map[string]error{"s-1/S-1-5-21-1": apiErr("ThrottlingException", "slow down")}, warnOp: transferListAccessesOp, warns: 1},
		{name: "describe not ready", descErrs: map[string]error{"s-1/S-1-5-21-1": apiErr("InvalidRequestException", "server is OFFLINE")}, warnOp: transferListAccessesOp, warns: 1},
		{name: "list other error", listErrs: map[string]error{"s-1": conflict}, warnOp: transferListAccessesOp, wantErr: "ConflictException"},
		{name: "describe other error", descErrs: map[string]error{"s-1/S-1-5-21-1": conflict}, warnOp: transferDescribeAccessOp, wantErr: "ConflictException"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			warnings := countOpWarnings(st, tc.warnOp)
			var calls parentCalls
			handlers := transferAccessHandlers(&calls, map[string][][]string{
				"s-1": {{"S-1-5-21-1"}},
				"s-2": {{"S-1-5-21-3"}},
				"s-3": {{"S-1-5-21-4"}},
			}, tc.listErrs, tc.descErrs)
			_, err := runTransferAccesses(t, st, handlers, transferTestServers("s-1", "s-2", "s-3"))
			if tc.wantErr != "" {
				if !isAPIErrorCode(err, tc.wantErr) {
					t.Fatalf("err = %v; want %s", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("scan: %v", err)
			}
			rows := rowsByNativeID(t, st, TypeTransferAccess)
			if _, ok := rows[transferTestServerARN("s-2")+"/access/S-1-5-21-3"]; !ok {
				t.Errorf("sibling server's access not stored: %v", rows)
			}
			if _, ok := rows[transferTestServerARN("s-1")+"/access/S-1-5-21-1"]; ok {
				t.Errorf("failed access stored")
			}
			if *warnings != tc.warns {
				t.Errorf("%s warnings = %d; want %d", tc.warnOp, *warnings, tc.warns)
			}
		})
	}
}

// TestScanTransferAccesses_DescribeErrorKeepsEarlierAccesses pins that a
// describe error skipping a server keeps the accesses described before it.
func TestScanTransferAccesses_DescribeErrorKeepsEarlierAccesses(t *testing.T) {
	st := newTestStore(t)
	warnings := countOpWarnings(st, transferListAccessesOp)
	var calls parentCalls
	handlers := transferAccessHandlers(&calls, map[string][][]string{
		"s-1": {{"S-1-5-21-1", "S-1-5-21-2"}},
	}, nil, map[string]error{"s-1/S-1-5-21-2": apiErr("ThrottlingException", "slow down")})

	total, err := runTransferAccesses(t, st, handlers, transferTestServers("s-1"))
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	kept := transferTestServerARN("s-1") + "/access/S-1-5-21-1"
	rows := rowsByNativeID(t, st, TypeTransferAccess)
	if _, ok := rows[kept]; !ok || len(rows) != 1 || total != 1 {
		t.Fatalf("total %d rows %v; want only %s", total, rows, kept)
	}
	assertContainsNative(t, st, transferTestServerARN("s-1"), kept)
	if *warnings != 1 {
		t.Errorf("warnings = %d; want 1", *warnings)
	}
}

func TestTransferDirectoryServers_DirectoryOrUnknown(t *testing.T) {
	dir := transferServer{childParent{id: "s-1", arn: transferTestServerARN("s-1")}, transfertypes.IdentityProviderTypeAwsDirectoryService}
	unknown := transferServer{childParent{id: "s-4", arn: transferTestServerARN("s-4")}, ""}
	servers := []transferServer{
		{childParent{id: "s-0", arn: transferTestServerARN("s-0")}, transfertypes.IdentityProviderTypeServiceManaged},
		dir,
		{childParent{id: "s-2", arn: transferTestServerARN("s-2")}, transfertypes.IdentityProviderTypeAwsLambda},
		{childParent{id: "s-3", arn: transferTestServerARN("s-3")}, transfertypes.IdentityProviderTypeApiGateway},
		unknown,
	}
	if got, want := transferDirectoryServers(servers), []childParent{dir.childParent, unknown.childParent}; !reflect.DeepEqual(got, want) {
		t.Errorf("directory servers = %v; want %v", got, want)
	}
}

package aws

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"sync/atomic"
	"testing"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/directconnect"
	directconnecttypes "github.com/aws/aws-sdk-go-v2/service/directconnect/types"
	"github.com/icearp/disco-cli/store"
)

const (
	dxTestGroupA = "arn:aws:directconnect::123456789012:dx-resiliency-group/rg-a"
	dxTestGroupB = "arn:aws:directconnect::123456789012:dx-resiliency-group/rg-b"
	dxTestConn1  = "arn:aws:directconnect:us-east-1:123456789012:dxcon/dxcon-1"
	dxTestConn2  = "arn:aws:directconnect:us-east-1:123456789012:dxcon/dxcon-2"
)

var (
	dcDenied    = apiErr("AccessDeniedException", "User: arn:aws:iam::123456789012:role/r is not authorized to perform: directconnect:X")
	dcClientErr = apiErr("DirectConnectClientException", "bad request")
	dcThrottled = apiErr("ThrottlingException", "Rate exceeded")
)

func newDXTestClient(handlers map[string]func(in any) (any, error)) *directconnect.Client {
	return directconnect.NewFromConfig(opStubConfig(handlers, dcDenied))
}

// dxPaged answers a paginated DX op: the first call returns first with
// NextToken "p2", a call carrying "p2" returns second. A call whose
// MaxResults is not the model cap fails, so the page size is pinned.
func dxPaged[T any](maxResults func(T) *int32, token func(T) *string, first, second any) func(any) (any, error) {
	return func(in any) (any, error) {
		p := in.(T)
		if sdkaws.ToInt32(maxResults(p)) != dxPageLimit {
			return nil, apiErr("DirectConnectClientException", "MaxResults must be at most 100")
		}
		if sdkaws.ToString(token(p)) == "p2" {
			return second, nil
		}
		return first, nil
	}
}

func dxInterconnectPages(first, second []directconnecttypes.Interconnect) func(any) (any, error) {
	return dxPaged(
		func(p *directconnect.DescribeInterconnectsInput) *int32 { return p.MaxResults },
		func(p *directconnect.DescribeInterconnectsInput) *string { return p.NextToken },
		&directconnect.DescribeInterconnectsOutput{Interconnects: first, NextToken: sdkaws.String("p2")},
		&directconnect.DescribeInterconnectsOutput{Interconnects: second})
}

func TestScanDXInterconnects_TwoPagesStored(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	client := newDXTestClient(map[string]func(any) (any, error){
		"DescribeInterconnects": dxInterconnectPages(
			[]directconnecttypes.Interconnect{
				{InterconnectId: sdkaws.String("dxcon-ic1"), InterconnectName: sdkaws.String("ic one"), InterconnectState: directconnecttypes.InterconnectStateAvailable, LagId: sdkaws.String("dxlag-1")},
				{InterconnectName: sdkaws.String("no id")},
			},
			[]directconnecttypes.Interconnect{
				{InterconnectId: sdkaws.String("dxcon-ic2"), InterconnectName: sdkaws.String("ic two"), InterconnectState: directconnecttypes.InterconnectStateDown},
			}),
	})
	total, _, err := scanDXInterconnects(context.Background(), client, acct, testRegion, st, testScanID)
	if err != nil {
		t.Fatalf("scanDXInterconnects: %v", err)
	}
	if total != 2 {
		t.Errorf("total = %d; want 2", total)
	}
	rows := rowsByNativeID(t, st, TypeDirectConnectInterconnect)
	if len(rows) != 2 {
		t.Fatalf("rows = %d; want 2", len(rows))
	}
	r1, ok := rows["arn:aws:directconnect:us-east-1:123456789012:dxcon/dxcon-ic1"]
	if !ok {
		t.Fatalf("interconnect dxcon-ic1 not stored under its connection ARN; got %v", rows)
	}
	if sv(r1.Name) != "ic one" || sv(r1.Status) != "available" || sv(r1.Region) != testRegion {
		t.Errorf("row = name %q status %q region %q; want ic one/available/%s", sv(r1.Name), sv(r1.Status), sv(r1.Region), testRegion)
	}
	var attrs struct {
		LagID string `json:"LagId"`
	}
	if err := json.Unmarshal([]byte(r1.AttributesJSON), &attrs); err != nil || attrs.LagID != "dxlag-1" {
		t.Errorf("attrs LagId = %q (err %v); want dxlag-1", attrs.LagID, err)
	}
	if r2 := rows["arn:aws:directconnect:us-east-1:123456789012:dxcon/dxcon-ic2"]; sv(r2.Status) != "down" {
		t.Errorf("dxcon-ic2 status = %q; want down", sv(r2.Status))
	}
}

func TestScanDXInterconnects_Empty(t *testing.T) {
	st := newTestStore(t)
	client := newDXTestClient(map[string]func(any) (any, error){
		"DescribeInterconnects": func(any) (any, error) { return &directconnect.DescribeInterconnectsOutput{}, nil },
	})
	total, _, err := scanDXInterconnects(context.Background(), client, newTestAccount(testAccountID), testRegion, st, testScanID)
	if err != nil || total != 0 {
		t.Fatalf("scanDXInterconnects = %d, %v; want 0, nil", total, err)
	}
}

// A repeated token fails the listing; the interconnect already listed is stored.
func TestScanDXInterconnects_RepeatedTokenFails(t *testing.T) {
	st := newTestStore(t)
	calls := 0
	client := newDXTestClient(map[string]func(any) (any, error){
		"DescribeInterconnects": func(any) (any, error) {
			calls++
			if calls > 2 {
				return nil, errors.New("called again after a repeated token")
			}
			return &directconnect.DescribeInterconnectsOutput{
				Interconnects: []directconnecttypes.Interconnect{{InterconnectId: sdkaws.String("dxcon-ic1")}},
				NextToken:     sdkaws.String("same"),
			}, nil
		},
	})
	_, _, err := scanDXInterconnects(context.Background(), client, newTestAccount(testAccountID), testRegion, st, testScanID)
	if !errors.Is(err, errRepeatedNextToken) {
		t.Fatalf("err = %v, want errRepeatedNextToken", err)
	}
	if got := len(rowsByNativeID(t, st, TypeDirectConnectInterconnect)); got != 1 {
		t.Errorf("stored = %d; want the listed interconnect stored", got)
	}
	if calls != 2 {
		t.Errorf("%d calls; want 2 (the first page, then the repeated token)", calls)
	}
}

func TestScanDXInterconnects_Errors(t *testing.T) {
	for _, tc := range []struct {
		name      string
		secondErr error // the second page's error; the first page lists one interconnect
		firstErr  error
		wantErr   bool
		wantRows  int
		wantWarns int
	}{
		{name: "denied", firstErr: dcDenied, wantWarns: 1},
		{name: "denied after a page keeps its rows", secondErr: dcDenied, wantRows: 1, wantWarns: 1},
		{name: "client error", firstErr: dcClientErr, wantErr: true},
		{name: "client error after a page", secondErr: dcClientErr, wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := countOpWarnings(st, dxDescribeInterconnectsOp)
			client := newDXTestClient(map[string]func(any) (any, error){
				"DescribeInterconnects": func(in any) (any, error) {
					if in.(*directconnect.DescribeInterconnectsInput).NextToken != nil {
						return nil, tc.secondErr
					}
					if tc.firstErr != nil {
						return nil, tc.firstErr
					}
					return &directconnect.DescribeInterconnectsOutput{
						Interconnects: []directconnecttypes.Interconnect{{InterconnectId: sdkaws.String("dxcon-ic1")}},
						NextToken:     sdkaws.String("p2"),
					}, nil
				},
			})
			_, _, err := scanDXInterconnects(context.Background(), client, newTestAccount(testAccountID), testRegion, st, testScanID)
			if tc.wantErr != (err != nil) {
				t.Fatalf("err = %v; want error %v", err, tc.wantErr)
			}
			if tc.wantErr && !isAPIErrorCode(err, "DirectConnectClientException") {
				t.Errorf("err = %v; want the DirectConnectClientException", err)
			}
			if n := len(rowsByNativeID(t, st, TypeDirectConnectInterconnect)); n != tc.wantRows {
				t.Errorf("rows = %d; want %d", n, tc.wantRows)
			}
			if *warns != tc.wantWarns {
				t.Errorf("warnings = %d; want %d", *warns, tc.wantWarns)
			}
		})
	}
}

// dxGroupHandlers answers the resiliency-group ops: groups rg-a, rg-b and a
// deleted rg-gone listed over two pages (plus an id-less summary), each Get
// returning the group's detail, rg-a's associations over two pages in
// reverse ARN order and none for rg-b. getErr / assocErr, when set, answer
// the Get / associations call for that group id instead. Every Get is
// recorded in gets.
type dxGroupHandlers struct {
	getErr   map[string]error
	assocErr map[string]error
	gets     parentCalls
}

func (h *dxGroupHandlers) handlers() map[string]func(any) (any, error) {
	return map[string]func(any) (any, error){
		"ListResiliencyGroups": dxPaged(
			func(p *directconnect.ListResiliencyGroupsInput) *int32 { return p.MaxResults },
			func(p *directconnect.ListResiliencyGroupsInput) *string { return p.NextToken },
			&directconnect.ListResiliencyGroupsOutput{NextToken: sdkaws.String("p2"), Items: []directconnecttypes.ResiliencyGroupSummary{
				{ResiliencyGroupId: sdkaws.String("rg-a"), State: directconnecttypes.ResiliencyGroupStateAvailable},
				{ResiliencyGroupName: sdkaws.String("no id")},
				{ResiliencyGroupId: sdkaws.String("rg-gone"), State: directconnecttypes.ResiliencyGroupStateDeleted},
			}},
			&directconnect.ListResiliencyGroupsOutput{Items: []directconnecttypes.ResiliencyGroupSummary{
				{ResiliencyGroupId: sdkaws.String("rg-b"), State: directconnecttypes.ResiliencyGroupStateDeleting},
			}}),
		"GetResiliencyGroup": func(in any) (any, error) {
			id := sdkaws.ToString(in.(*directconnect.GetResiliencyGroupInput).ResiliencyGroupId)
			h.gets.add(id)
			err := h.getErr[id]
			if err != nil {
				return nil, err
			}
			g := &directconnecttypes.ResiliencyGroup{
				ResiliencyGroupId:   sdkaws.String(id),
				ResiliencyGroupArn:  sdkaws.String("arn:aws:directconnect::123456789012:dx-resiliency-group/" + id),
				ResiliencyGroupName: sdkaws.String("group " + id),
				State:               directconnecttypes.ResiliencyGroupStateAvailable,
				Tags:                []directconnecttypes.Tag{{Key: sdkaws.String("env"), Value: sdkaws.String("prod")}},
			}
			if id == "rg-b" {
				g.State = directconnecttypes.ResiliencyGroupStateDeleting
			}
			return &directconnect.GetResiliencyGroupOutput{ResiliencyGroup: g}, nil
		},
		"ListResiliencyGroupAssociations": func(in any) (any, error) {
			p := in.(*directconnect.ListResiliencyGroupAssociationsInput)
			id := sdkaws.ToString(p.ResiliencyGroupId)
			err := h.assocErr[id]
			if err != nil {
				return nil, err
			}
			if id != "rg-a" {
				return &directconnect.ListResiliencyGroupAssociationsOutput{}, nil
			}
			return dxPaged(
				func(p *directconnect.ListResiliencyGroupAssociationsInput) *int32 { return p.MaxResults },
				func(p *directconnect.ListResiliencyGroupAssociationsInput) *string { return p.NextToken },
				&directconnect.ListResiliencyGroupAssociationsOutput{NextToken: sdkaws.String("p2"), Items: []directconnecttypes.ResiliencyGroupAssociation{
					{ResiliencyGroupId: sdkaws.String(id), ConnectionArn: sdkaws.String(dxTestConn2), State: directconnecttypes.ResiliencyGroupAssociationStateAssociated},
				}},
				&directconnect.ListResiliencyGroupAssociationsOutput{Items: []directconnecttypes.ResiliencyGroupAssociation{
					{ResiliencyGroupId: sdkaws.String(id), ConnectionArn: sdkaws.String(dxTestConn1), State: directconnecttypes.ResiliencyGroupAssociationStateAssociating},
				}})(p)
		},
	}
}

func dxGroupAssociationARNs(t *testing.T, r store.Resource) []string {
	t.Helper()
	var attrs dxResiliencyGroupAttrs
	if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
		t.Fatalf("decode %s: %v", r.NativeID, err)
	}
	if attrs.Associations == nil {
		return nil
	}
	arns := []string{}
	for _, a := range attrs.Associations {
		arns = append(arns, sv(a.ConnectionArn))
	}
	return arns
}

func TestScanDXResiliencyGroups_StoresGroupsWithAssociations(t *testing.T) {
	st := newTestStore(t)
	h := &dxGroupHandlers{}
	client := newDXTestClient(h.handlers())
	total, _, err := scanDXResiliencyGroups(context.Background(), client, newTestAccount(testAccountID), testRegion, st, testScanID)
	if err != nil {
		t.Fatalf("scanDXResiliencyGroups: %v", err)
	}
	if total != 2 {
		t.Errorf("total = %d; want 2", total)
	}
	if got := h.gets.distinct(); !slices.Equal(got, []string{"rg-a", "rg-b"}) {
		t.Errorf("GetResiliencyGroup calls = %v; want [rg-a rg-b] (deleted and id-less groups skipped)", got)
	}
	rows := rowsByNativeID(t, st, TypeDirectConnectResiliencyGroup)
	if len(rows) != 2 {
		t.Fatalf("rows = %d; want 2", len(rows))
	}
	a, ok := rows[dxTestGroupA]
	if !ok {
		t.Fatalf("group rg-a not stored under its ARN; got %v", rows)
	}
	if sv(a.Name) != "group rg-a" || sv(a.Status) != "available" || sv(a.Region) != testRegion {
		t.Errorf("rg-a = name %q status %q region %q; want group rg-a/available/%s", sv(a.Name), sv(a.Status), sv(a.Region), testRegion)
	}
	var attrs struct {
		Tags []struct{ Key, Value string }
	}
	if err := json.Unmarshal([]byte(a.AttributesJSON), &attrs); err != nil || len(attrs.Tags) != 1 || attrs.Tags[0].Key != "env" {
		t.Errorf("rg-a Tags = %+v (err %v); want the Get detail's env tag", attrs.Tags, err)
	}
	if got := dxGroupAssociationARNs(t, a); !slices.Equal(got, []string{dxTestConn1, dxTestConn2}) {
		t.Errorf("rg-a associations = %v; want both pages sorted [%s %s]", got, dxTestConn1, dxTestConn2)
	}
	b := rows[dxTestGroupB]
	if sv(b.Status) != "deleting" {
		t.Errorf("rg-b status = %q; want deleting", sv(b.Status))
	}
	if got := dxGroupAssociationARNs(t, b); got == nil || len(got) != 0 {
		t.Errorf("rg-b associations = %#v; want [] (none, not denied)", got)
	}
}

func TestScanDXResiliencyGroups_Empty(t *testing.T) {
	st := newTestStore(t)
	client := newDXTestClient(map[string]func(any) (any, error){
		"ListResiliencyGroups": func(any) (any, error) { return &directconnect.ListResiliencyGroupsOutput{}, nil },
	})
	total, _, err := scanDXResiliencyGroups(context.Background(), client, newTestAccount(testAccountID), testRegion, st, testScanID)
	if err != nil || total != 0 {
		t.Fatalf("scanDXResiliencyGroups = %d, %v; want 0, nil", total, err)
	}
}

func TestScanDXResiliencyGroups_ListErrors(t *testing.T) {
	for _, tc := range []struct {
		name      string
		err       error
		wantErr   bool
		wantWarns int
	}{
		{name: "denied", err: dcDenied, wantWarns: 1},
		{name: "client error", err: dcClientErr, wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := countOpWarnings(st, dxListResiliencyGroupsOp)
			client := newDXTestClient(map[string]func(any) (any, error){
				"ListResiliencyGroups": func(any) (any, error) { return nil, tc.err },
			})
			_, _, err := scanDXResiliencyGroups(context.Background(), client, newTestAccount(testAccountID), testRegion, st, testScanID)
			if tc.wantErr != (err != nil) {
				t.Fatalf("err = %v; want error %v", err, tc.wantErr)
			}
			if tc.wantErr && !isAPIErrorCode(err, "DirectConnectClientException") {
				t.Errorf("err = %v; want the DirectConnectClientException", err)
			}
			if *warns != tc.wantWarns {
				t.Errorf("warnings = %d; want %d", *warns, tc.wantWarns)
			}
		})
	}
}

func TestScanDXResiliencyGroups_PerGroupErrors(t *testing.T) {
	both := func(err error) map[string]error { return map[string]error{"rg-a": err, "rg-b": err} }
	for _, tc := range []struct {
		name     string
		getErr   map[string]error
		assocErr map[string]error
		wantErr  bool
		// wantAssocs maps each stored group to its association ARNs (nil = null).
		wantAssocs map[string][]string
		warnOp     string
		wantWarns  int
	}{
		{
			name: "get denied warns once", getErr: both(dcDenied),
			wantAssocs: map[string][]string{}, warnOp: dxGetResiliencyGroupOp, wantWarns: 1,
		},
		{
			name: "associations denied keeps groups with null associations", assocErr: both(dcDenied),
			wantAssocs: map[string][]string{dxTestGroupA: nil, dxTestGroupB: nil}, warnOp: dxListResiliencyGroupAssociationsOp, wantWarns: 1,
		},
		{
			name: "get throttled skips that group with one warning", getErr: map[string]error{"rg-a": dcThrottled},
			wantAssocs: map[string][]string{dxTestGroupB: {}}, warnOp: dxGetResiliencyGroupOp, wantWarns: 1,
		},
		{
			name: "associations throttled skips that group with one warning", assocErr: map[string]error{"rg-a": dcThrottled},
			wantAssocs: map[string][]string{dxTestGroupB: {}}, warnOp: dxListResiliencyGroupAssociationsOp, wantWarns: 1,
		},
		{name: "get client error fails the phase", getErr: map[string]error{"rg-a": dcClientErr}, wantErr: true},
		{name: "associations client error fails the phase", assocErr: map[string]error{"rg-a": dcClientErr}, wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := countOpWarnings(st, tc.warnOp)
			h := &dxGroupHandlers{getErr: tc.getErr, assocErr: tc.assocErr}
			_, _, err := scanDXResiliencyGroups(context.Background(), newDXTestClient(h.handlers()), newTestAccount(testAccountID), testRegion, st, testScanID)
			if tc.wantErr {
				if !isAPIErrorCode(err, "DirectConnectClientException") {
					t.Fatalf("err = %v; want the DirectConnectClientException", err)
				}
				if n := len(rowsByNativeID(t, st, TypeDirectConnectResiliencyGroup)); n != 0 {
					t.Errorf("rows = %d; want 0 (a failed phase stores none)", n)
				}
				return
			}
			if err != nil {
				t.Fatalf("scanDXResiliencyGroups: %v", err)
			}
			rows := rowsByNativeID(t, st, TypeDirectConnectResiliencyGroup)
			if len(rows) != len(tc.wantAssocs) {
				t.Fatalf("rows = %d; want %d", len(rows), len(tc.wantAssocs))
			}
			for arn, want := range tc.wantAssocs {
				r, ok := rows[arn]
				if !ok {
					t.Fatalf("group %s not stored", arn)
				}
				got := dxGroupAssociationARNs(t, r)
				if (got == nil) != (want == nil) || !slices.Equal(got, want) {
					t.Errorf("%s associations = %#v; want %#v", arn, got, want)
				}
			}
			if *warns != tc.wantWarns {
				t.Errorf("%s warnings = %d; want %d", tc.warnOp, *warns, tc.wantWarns)
			}
		})
	}
}

func TestScanDXResiliencyGroups_DetailWithoutARNSkipped(t *testing.T) {
	st := newTestStore(t)
	h := &dxGroupHandlers{}
	handlers := h.handlers()
	handlers["GetResiliencyGroup"] = func(in any) (any, error) {
		if sdkaws.ToString(in.(*directconnect.GetResiliencyGroupInput).ResiliencyGroupId) == "rg-a" {
			return &directconnect.GetResiliencyGroupOutput{}, nil
		}
		return &directconnect.GetResiliencyGroupOutput{ResiliencyGroup: &directconnecttypes.ResiliencyGroup{ResiliencyGroupId: sdkaws.String("rg-b")}}, nil
	}
	total, _, err := scanDXResiliencyGroups(context.Background(), newDXTestClient(handlers), newTestAccount(testAccountID), testRegion, st, testScanID)
	if err != nil || total != 0 {
		t.Fatalf("scanDXResiliencyGroups = %d, %v; want 0, nil", total, err)
	}
}

// dxAllHandlers answers every DX op: one connection, no LAGs, VIFs or
// gateways, one interconnect and the dxGroupHandlers groups. override
// replaces individual ops.
func dxAllHandlers(override map[string]func(any) (any, error)) map[string]func(any) (any, error) {
	h := (&dxGroupHandlers{}).handlers()
	h["DescribeConnections"] = func(any) (any, error) {
		return &directconnect.DescribeConnectionsOutput{Connections: []directconnecttypes.Connection{{ConnectionId: sdkaws.String("dxcon-1")}}}, nil
	}
	h["DescribeLags"] = func(any) (any, error) { return &directconnect.DescribeLagsOutput{}, nil }
	h["DescribeVirtualInterfaces"] = func(any) (any, error) { return &directconnect.DescribeVirtualInterfacesOutput{}, nil }
	h["DescribeDirectConnectGateways"] = func(any) (any, error) { return &directconnect.DescribeDirectConnectGatewaysOutput{}, nil }
	h["DescribeInterconnects"] = func(any) (any, error) {
		return &directconnect.DescribeInterconnectsOutput{Interconnects: []directconnecttypes.Interconnect{{InterconnectId: sdkaws.String("dxcon-ic1")}}}, nil
	}
	for op, fn := range override {
		h[op] = fn
	}
	return h
}

func TestScanDirectConnect_RunsNewPhasesWithOlderPhases(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	acct.cfg = opStubConfig(dxAllHandlers(nil), dcDenied)
	total, _, err := scanDirectConnect(context.Background(), acct, testRegion, st, testScanID)
	if err != nil {
		t.Fatalf("scanDirectConnect: %v", err)
	}
	if total != 4 {
		t.Errorf("total = %d; want 4 (connection, interconnect, two groups)", total)
	}
	for typ, want := range map[string]int{
		TypeDirectConnectConnection: 1, TypeDirectConnectInterconnect: 1, TypeDirectConnectResiliencyGroup: 2,
	} {
		if n := len(rowsByNativeID(t, st, typ)); n != want {
			t.Errorf("%s rows = %d; want %d", typ, n, want)
		}
	}
}

func TestScanDirectConnectAll_PhaseErrorsAreIndependent(t *testing.T) {
	fail := func(any) (any, error) { return nil, dcClientErr }
	for _, tc := range []struct {
		name      string
		override  map[string]func(any) (any, error)
		wantTypes map[string]int
	}{
		{
			name:      "older phase error keeps the new phases",
			override:  map[string]func(any) (any, error){"DescribeConnections": fail},
			wantTypes: map[string]int{TypeDirectConnectConnection: 0, TypeDirectConnectInterconnect: 1, TypeDirectConnectResiliencyGroup: 2},
		},
		{
			name:      "interconnect error keeps the other phases",
			override:  map[string]func(any) (any, error){"DescribeInterconnects": fail},
			wantTypes: map[string]int{TypeDirectConnectConnection: 1, TypeDirectConnectInterconnect: 0, TypeDirectConnectResiliencyGroup: 2},
		},
		{
			name:      "resiliency group error keeps the other phases",
			override:  map[string]func(any) (any, error){"ListResiliencyGroups": fail},
			wantTypes: map[string]int{TypeDirectConnectConnection: 1, TypeDirectConnectInterconnect: 1, TypeDirectConnectResiliencyGroup: 0},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			client := newDXTestClient(dxAllHandlers(tc.override))
			_, _, err := scanDirectConnectAll(context.Background(), client, newTestAccount(testAccountID), testRegion, st, testScanID)
			if !isAPIErrorCode(err, "DirectConnectClientException") {
				t.Errorf("err = %v; want the failed phase's DirectConnectClientException", err)
			}
			for typ, want := range tc.wantTypes {
				if n := len(rowsByNativeID(t, st, typ)); n != want {
					t.Errorf("%s rows = %d; want %d", typ, n, want)
				}
			}
		})
	}
}

// A transient error in one phase is demoted to a warning so that another
// phase's hard error still reaches the dispatcher as a hard error.
func TestScanDirectConnectAll_TransientDemoted(t *testing.T) {
	for _, tc := range []struct {
		name               string
		throttledOp, label string
		failedOp           string
		wantTotal          int
	}{
		{name: "older phases", throttledOp: "DescribeConnections", label: "directconnect", failedOp: "ListResiliencyGroups", wantTotal: 1},
		{name: "interconnects", throttledOp: "DescribeInterconnects", label: dxDescribeInterconnectsOp, failedOp: "DescribeConnections", wantTotal: 2},
		{name: "resiliency groups", throttledOp: "ListResiliencyGroups", label: dxListResiliencyGroupsOp, failedOp: "DescribeInterconnects", wantTotal: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := countOpWarnings(st, tc.label)
			client := newDXTestClient(dxAllHandlers(map[string]func(any) (any, error){
				tc.throttledOp: func(any) (any, error) { return nil, dcThrottled },
				tc.failedOp:    func(any) (any, error) { return nil, dcClientErr },
			}))
			total, _, err := scanDirectConnectAll(context.Background(), client, newTestAccount(testAccountID), testRegion, st, testScanID)
			if got := classifyServiceError(err); got != outcomeError || !isAPIErrorCode(err, "DirectConnectClientException") {
				t.Errorf("err = %v (outcome %v); want only the hard DirectConnectClientException (outcomeError)", err, got)
			}
			if *warns != 1 {
				t.Errorf("%s transient warnings = %d; want 1", tc.label, *warns)
			}
			if total != tc.wantTotal {
				t.Errorf("total = %d; want %d (the rows of the phases that neither failed nor throttled)", total, tc.wantTotal)
			}
		})
	}
}

// Two associations of one connection differ only in state; their stored order
// must not depend on the order the API returned them in.
func TestScanDXResiliencyGroups_AssociationOrderStable(t *testing.T) {
	assoc := func(state directconnecttypes.ResiliencyGroupAssociationState) directconnecttypes.ResiliencyGroupAssociation {
		return directconnecttypes.ResiliencyGroupAssociation{ConnectionArn: sdkaws.String(dxTestConn1), State: state}
	}
	stored := func(items ...directconnecttypes.ResiliencyGroupAssociation) string {
		st := newTestStore(t)
		handlers := (&dxGroupHandlers{}).handlers()
		handlers["ListResiliencyGroupAssociations"] = func(any) (any, error) {
			return &directconnect.ListResiliencyGroupAssociationsOutput{Items: items}, nil
		}
		if _, _, err := scanDXResiliencyGroups(context.Background(), newDXTestClient(handlers), newTestAccount(testAccountID), testRegion, st, testScanID); err != nil {
			t.Fatalf("scanDXResiliencyGroups: %v", err)
		}
		return rowsByNativeID(t, st, TypeDirectConnectResiliencyGroup)[dxTestGroupA].AttributesJSON
	}
	disassociating := assoc(directconnecttypes.ResiliencyGroupAssociationStateDisassociating)
	associating := assoc(directconnecttypes.ResiliencyGroupAssociationStateAssociating)
	if a, b := stored(disassociating, associating), stored(associating, disassociating); a != b {
		t.Errorf("attributes depend on API order:\n%s\n%s", a, b)
	}
}

// dxRepeatToken answers every call with page and the token "same", as a broken
// service would, counting the calls; a fourth call fails so a loop that does
// not stop ends in an assertion rather than a hang.
func dxRepeatToken(calls *atomic.Int32, page func() any) func(any) (any, error) {
	return func(any) (any, error) {
		if calls.Add(1) > 3 {
			return nil, errors.New("called again after a repeated token")
		}
		return page(), nil
	}
}

// A repeated ListResiliencyGroups token fails the listing; the group already
// listed is still read and stored.
func TestScanDXResiliencyGroups_RepeatedListTokenFails(t *testing.T) {
	st := newTestStore(t)
	h := &dxGroupHandlers{}
	handlers := h.handlers()
	var calls atomic.Int32
	handlers["ListResiliencyGroups"] = dxRepeatToken(&calls, func() any {
		return &directconnect.ListResiliencyGroupsOutput{NextToken: sdkaws.String("same"), Items: []directconnecttypes.ResiliencyGroupSummary{
			{ResiliencyGroupId: sdkaws.String("rg-a"), State: directconnecttypes.ResiliencyGroupStateAvailable},
		}}
	})
	_, _, err := scanDXResiliencyGroups(context.Background(), newDXTestClient(handlers), newTestAccount(testAccountID), testRegion, st, testScanID)
	if !errors.Is(err, errRepeatedNextToken) {
		t.Fatalf("err = %v, want errRepeatedNextToken", err)
	}
	if got := calls.Load(); got != 2 {
		t.Errorf("ListResiliencyGroups calls = %d; want 2 (stop when the token repeats)", got)
	}
	if _, ok := rowsByNativeID(t, st, TypeDirectConnectResiliencyGroup)[dxTestGroupA]; !ok {
		t.Errorf("group rg-a not stored")
	}
}

// A repeated ListResiliencyGroupAssociations token fails the scan, but every
// group is still stored. The group whose listing repeated keeps Associations
// null (not read): stored with part of them, it would claim the rest absent.
func TestScanDXResiliencyGroups_RepeatedAssociationTokenFails(t *testing.T) {
	st := newTestStore(t)
	h := &dxGroupHandlers{}
	handlers := h.handlers()
	listAssocs := handlers["ListResiliencyGroupAssociations"]
	var calls atomic.Int32
	handlers["ListResiliencyGroupAssociations"] = func(in any) (any, error) {
		if sdkaws.ToString(in.(*directconnect.ListResiliencyGroupAssociationsInput).ResiliencyGroupId) != "rg-a" {
			return listAssocs(in)
		}
		if calls.Add(1) > 3 {
			return nil, errors.New("called again after a repeated token")
		}
		return &directconnect.ListResiliencyGroupAssociationsOutput{NextToken: sdkaws.String("same"), Items: []directconnecttypes.ResiliencyGroupAssociation{
			{ResiliencyGroupId: sdkaws.String("rg-a"), ConnectionArn: sdkaws.String(dxTestConn1), State: directconnecttypes.ResiliencyGroupAssociationStateAssociated},
		}}, nil
	}
	_, _, err := scanDXResiliencyGroups(context.Background(), newDXTestClient(handlers), newTestAccount(testAccountID), testRegion, st, testScanID)
	if !errors.Is(err, errRepeatedNextToken) {
		t.Fatalf("err = %v, want errRepeatedNextToken", err)
	}
	if got := calls.Load(); got != 2 {
		t.Errorf("rg-a association calls = %d; want 2 (stop when the token repeats)", got)
	}
	rows := rowsByNativeID(t, st, TypeDirectConnectResiliencyGroup)
	for id, wantNull := range map[string]bool{dxTestGroupA: true, dxTestGroupB: false} {
		r, ok := rows[id]
		if !ok {
			t.Errorf("group %s not stored", id)
			continue
		}
		var attrs struct{ Associations json.RawMessage }
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			t.Fatalf("decode %s: %v", id, err)
		}
		if gotNull := string(attrs.Associations) == "null"; gotNull != wantNull {
			t.Errorf("group %s Associations = %s; want null %v", id, attrs.Associations, wantNull)
		}
	}
}

package aws

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"reflect"
	"slices"
	"strings"
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
				{InterconnectId: sdkaws.String("dxcon-ic1"), InterconnectName: sdkaws.String("ic one"), InterconnectState: directconnecttypes.InterconnectStateAvailable, LagId: sdkaws.String("dxlag-1"), Tags: dxTags("env", "prod")},
				{InterconnectName: sdkaws.String("no id")},
			},
			[]directconnecttypes.Interconnect{
				{InterconnectId: sdkaws.String("dxcon-ic2"), InterconnectState: directconnecttypes.InterconnectStateDown},
			}),
	})
	warns := countOpWarnings(st, dxDescribeInterconnectsOp)
	total, _, err := scanDXInterconnects(context.Background(), client, acct, testRegion, st, testScanID)
	if err != nil {
		t.Fatalf("scanDXInterconnects: %v", err)
	}
	if *warns != 1 {
		t.Errorf("warnings = %d; want 1 for the interconnect without an InterconnectId", *warns)
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
	assertTagsMap(t, r1.TagsJSON, map[string]string{"env": "prod"})
	r2 := rows["arn:aws:directconnect:us-east-1:123456789012:dxcon/dxcon-ic2"]
	if sv(r2.Status) != "down" || sv(r2.Name) != "dxcon-ic2" {
		t.Errorf("dxcon-ic2 status %q name %q; want down and its id (no name listed)", sv(r2.Status), sv(r2.Name))
	}
	if r2.TagsJSON != nil {
		t.Errorf("dxcon-ic2 tags = %s; want none", *r2.TagsJSON)
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
		{name: "client error after a page keeps its rows", secondErr: dcClientErr, wantErr: true, wantRows: 1},
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
	// summaryNoARN lists the groups whose summary lacks ResiliencyGroupArn.
	summaryNoARN map[string]bool
	gets         parentCalls
	assocCalls   parentCalls
}

// summary is the list summary of group id: the detail's ARN, name and
// owner, with state.
func (h *dxGroupHandlers) summary(id string, state directconnecttypes.ResiliencyGroupState) directconnecttypes.ResiliencyGroupSummary {
	s := directconnecttypes.ResiliencyGroupSummary{
		ResiliencyGroupId: sdkaws.String(id), ResiliencyGroupName: sdkaws.String("group " + id),
		ResiliencyGroupArn: sdkaws.String("arn:aws:directconnect::123456789012:dx-resiliency-group/" + id),
		OwnerAccount:       sdkaws.String(testAccountID), State: state, ResiliencyGroupType: directconnecttypes.ResiliencyGroupTypeManaged,
	}
	if h.summaryNoARN[id] {
		s.ResiliencyGroupArn = nil
	}
	return s
}

func (h *dxGroupHandlers) handlers() map[string]func(any) (any, error) {
	return map[string]func(any) (any, error){
		"ListResiliencyGroups": dxPaged(
			func(p *directconnect.ListResiliencyGroupsInput) *int32 { return p.MaxResults },
			func(p *directconnect.ListResiliencyGroupsInput) *string { return p.NextToken },
			&directconnect.ListResiliencyGroupsOutput{NextToken: sdkaws.String("p2"), Items: []directconnecttypes.ResiliencyGroupSummary{
				h.summary("rg-a", directconnecttypes.ResiliencyGroupStateAvailable),
				{ResiliencyGroupName: sdkaws.String("no id")},
				h.summary("rg-gone", directconnecttypes.ResiliencyGroupStateDeleted),
			}},
			&directconnect.ListResiliencyGroupsOutput{Items: []directconnecttypes.ResiliencyGroupSummary{
				h.summary("rg-b", directconnecttypes.ResiliencyGroupStateDeleting),
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
				g.Tags = nil
			}
			return &directconnect.GetResiliencyGroupOutput{ResiliencyGroup: g}, nil
		},
		"ListResiliencyGroupAssociations": func(in any) (any, error) {
			p := in.(*directconnect.ListResiliencyGroupAssociationsInput)
			id := sdkaws.ToString(p.ResiliencyGroupId)
			h.assocCalls.add(id)
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
	assertTagsMap(t, a.TagsJSON, map[string]string{"env": "prod"})
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
	if b.TagsJSON != nil {
		t.Errorf("rg-b tags = %s; want none", *b.TagsJSON)
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

// One group's failed read costs no other group, and no group is lost: a
// group whose Get fails is stored from its summary (no tags, Associations
// null), one whose associations could not be read with Associations null.
// Either error is returned unless it was a denial, which warns once.
func TestScanDXResiliencyGroups_PerGroupErrors(t *testing.T) {
	both := func(err error) map[string]error { return map[string]error{"rg-a": err, "rg-b": err} }
	for _, tc := range []struct {
		name     string
		getErr   map[string]error
		assocErr map[string]error
		wantCode string // the error code returned; "" for none
		// wantAssocs maps each stored group to its association ARNs (nil = null).
		wantAssocs map[string][]string
		// fromSummary lists the groups stored from their summary.
		fromSummary []string
		warnOp      string
		wantWarns   int
	}{
		{
			name: "get denied stores the summaries and warns once", getErr: both(dcDenied),
			wantAssocs: map[string][]string{dxTestGroupA: nil, dxTestGroupB: nil}, fromSummary: []string{dxTestGroupA, dxTestGroupB},
			warnOp: dxGetResiliencyGroupOp, wantWarns: 1,
		},
		{
			name: "associations denied keeps groups with null associations", assocErr: both(dcDenied),
			wantAssocs: map[string][]string{dxTestGroupA: nil, dxTestGroupB: nil}, warnOp: dxListResiliencyGroupAssociationsOp, wantWarns: 1,
		},
		{
			name: "get throttled is returned and stores the summary", getErr: map[string]error{"rg-a": dcThrottled},
			wantCode: "ThrottlingException", wantAssocs: map[string][]string{dxTestGroupA: nil, dxTestGroupB: {}}, fromSummary: []string{dxTestGroupA},
			warnOp: dxGetResiliencyGroupOp,
		},
		{
			name: "associations throttled is returned and keeps the group with null associations", assocErr: map[string]error{"rg-a": dcThrottled},
			wantCode: "ThrottlingException", wantAssocs: map[string][]string{dxTestGroupA: nil, dxTestGroupB: {}}, warnOp: dxListResiliencyGroupAssociationsOp,
		},
		{
			name: "get client error is returned and stores the summary", getErr: map[string]error{"rg-a": dcClientErr},
			wantCode: "DirectConnectClientException", wantAssocs: map[string][]string{dxTestGroupA: nil, dxTestGroupB: {}}, fromSummary: []string{dxTestGroupA},
			warnOp: dxGetResiliencyGroupOp,
		},
		{
			name: "associations client error is returned and keeps the group with null associations", assocErr: map[string]error{"rg-a": dcClientErr},
			wantCode: "DirectConnectClientException", wantAssocs: map[string][]string{dxTestGroupA: nil, dxTestGroupB: {}}, warnOp: dxListResiliencyGroupAssociationsOp,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := countOpWarnings(st, tc.warnOp)
			h := &dxGroupHandlers{getErr: tc.getErr, assocErr: tc.assocErr}
			total, _, err := scanDXResiliencyGroups(context.Background(), newDXTestClient(h.handlers()), newTestAccount(testAccountID), testRegion, st, testScanID)
			if tc.wantCode == "" && err != nil {
				t.Fatalf("scanDXResiliencyGroups: %v", err)
			}
			if tc.wantCode != "" && !isAPIErrorCode(err, tc.wantCode) {
				t.Fatalf("err = %v; want the %s", err, tc.wantCode)
			}
			if total != len(tc.wantAssocs) {
				t.Errorf("total = %d; want %d", total, len(tc.wantAssocs))
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
				// rg-a's detail carries the env tag; a summary carries none.
				if arn == dxTestGroupA && slices.Contains(tc.fromSummary, arn) != (r.TagsJSON == nil) {
					t.Errorf("%s tags = %v; want none only when stored from the summary", arn, r.TagsJSON)
				}
				if !strings.HasPrefix(sv(r.Name), "group rg-") || sv(r.Status) == "" {
					t.Errorf("%s name %q status %q; want the group's name and state", arn, sv(r.Name), sv(r.Status))
				}
			}
			if *warns != tc.wantWarns {
				t.Errorf("%s warnings = %d; want %d", tc.warnOp, *warns, tc.wantWarns)
			}
		})
	}
}

// A Get detail without a ResiliencyGroupArn has no key to store it under, so
// it is warned about once, and the other group is stored.
func TestScanDXResiliencyGroups_DetailWithoutARN(t *testing.T) {
	for _, tc := range []struct {
		name         string
		summaryNoARN bool
		wantStored   bool
		wantWarns    int
	}{
		{name: "keyed by the summary ARN", wantStored: true},
		{name: "warned when the summary lacks it too", summaryNoARN: true, wantWarns: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := countOpWarnings(st, dxGetResiliencyGroupOp)
			h := &dxGroupHandlers{summaryNoARN: map[string]bool{"rg-a": tc.summaryNoARN}}
			handlers := h.handlers()
			get := handlers["GetResiliencyGroup"]
			handlers["GetResiliencyGroup"] = func(in any) (any, error) {
				if sdkaws.ToString(in.(*directconnect.GetResiliencyGroupInput).ResiliencyGroupId) == "rg-a" {
					return &directconnect.GetResiliencyGroupOutput{ResiliencyGroup: &directconnecttypes.ResiliencyGroup{
						ResiliencyGroupId: sdkaws.String("rg-a"), Tags: dxTags("env", "prod"),
					}}, nil
				}
				return get(in)
			}
			if _, _, err := scanDXResiliencyGroups(context.Background(), newDXTestClient(handlers), newTestAccount(testAccountID), testRegion, st, testScanID); err != nil {
				t.Fatalf("scanDXResiliencyGroups: %v", err)
			}
			rows := rowsByNativeID(t, st, TypeDirectConnectResiliencyGroup)
			if _, ok := rows[dxTestGroupB]; !ok {
				t.Errorf("group rg-b not stored")
			}
			a, ok := rows[dxTestGroupA]
			if ok != tc.wantStored {
				t.Fatalf("rg-a stored = %v; want %v", ok, tc.wantStored)
			}
			if ok {
				assertTagsMap(t, a.TagsJSON, map[string]string{"env": "prod"})
			}
			if *warns != tc.wantWarns {
				t.Errorf("warnings = %d; want %d", *warns, tc.wantWarns)
			}
			// A group that is not stored has no row to hold its associations.
			if read := slices.Contains(h.assocCalls.distinct(), "rg-a"); read != tc.wantStored {
				t.Errorf("rg-a associations read = %v; want %v", read, tc.wantStored)
			}
		})
	}
}

// A Get that answers without a group, for a group whose summary has no ARN
// either, leaves nothing to store: one not-stored warning, and the empty
// answer is returned.
func TestScanDXResiliencyGroups_EmptyGetWithoutARN(t *testing.T) {
	st := newTestStore(t)
	warns := countOpWarnings(st, dxGetResiliencyGroupOp)
	h := &dxGroupHandlers{summaryNoARN: map[string]bool{"rg-a": true}}
	handlers := h.handlers()
	get := handlers["GetResiliencyGroup"]
	handlers["GetResiliencyGroup"] = func(in any) (any, error) {
		if sdkaws.ToString(in.(*directconnect.GetResiliencyGroupInput).ResiliencyGroupId) == "rg-a" {
			return &directconnect.GetResiliencyGroupOutput{}, nil
		}
		return get(in)
	}
	_, _, err := scanDXResiliencyGroups(context.Background(), newDXTestClient(handlers), newTestAccount(testAccountID), testRegion, st, testScanID)
	if !errors.Is(err, errDXNoGroupBody) {
		t.Errorf("err = %v; want errDXNoGroupBody", err)
	}
	if rows := rowsByNativeID(t, st, TypeDirectConnectResiliencyGroup); len(rows) != 1 {
		t.Errorf("rows = %v; want only rg-b", rows)
	}
	if *warns != 1 {
		t.Errorf("%s warnings = %d; want 1 (not stored)", dxGetResiliencyGroupOp, *warns)
	}
}

// A VIF whose type maps to no disco type is not stored and is warned about
// once under the VIF op, scoped to the account and region.
func TestScanDXVirtualInterfaces_UnmappedTypeWarned(t *testing.T) {
	st := newTestStore(t)
	var scopes []string
	st.OnWarn = func(w store.ScanWarning) {
		if w.Service == dxDescribeVirtualInterfacesOp {
			scopes = append(scopes, w.Scope)
		}
	}
	client := newDXTestClient(map[string]func(any) (any, error){
		"DescribeVirtualInterfaces": func(any) (any, error) {
			return &directconnect.DescribeVirtualInterfacesOutput{VirtualInterfaces: []directconnecttypes.VirtualInterface{
				{VirtualInterfaceId: sdkaws.String("dxvif-x"), VirtualInterfaceType: sdkaws.String("unknown")},
			}}, nil
		},
	})
	total, _, err := scanDXVirtualInterfaces(context.Background(), client, newTestAccount(testAccountID), testRegion, st, testScanID)
	if err != nil || total != 0 {
		t.Fatalf("scanDXVirtualInterfaces = %d, %v; want 0, nil", total, err)
	}
	if want := testAccountID + "/" + testRegion; !slices.Equal(scopes, []string{want}) {
		t.Errorf("warning scopes = %v; want one [%s]", scopes, want)
	}
}

// A group stored from its summary after a failed Get keeps the NativeID of the
// row its detail stores, so the two scans write one row per group.
func TestScanDXResiliencyGroups_SummaryRowSharesDetailNativeID(t *testing.T) {
	st := newTestStore(t)
	for _, h := range []*dxGroupHandlers{{}, {getErr: map[string]error{"rg-a": dcDenied, "rg-b": dcDenied}}} {
		if _, _, err := scanDXResiliencyGroups(context.Background(), newDXTestClient(h.handlers()), newTestAccount(testAccountID), testRegion, st, testScanID); err != nil {
			t.Fatalf("scanDXResiliencyGroups: %v", err)
		}
	}
	rows := rowsByNativeID(t, st, TypeDirectConnectResiliencyGroup)
	if len(rows) != 2 {
		t.Errorf("rows = %v; want one per group (rg-a, rg-b)", rows)
	}
}

// Groups not read because ctx ended are still stored, from their summary, and
// the stop is returned.
func TestScanDXResiliencyGroups_StoppedGroupsStoredFromSummary(t *testing.T) {
	st := newTestStore(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	h := &dxGroupHandlers{}
	handlers := h.handlers()
	list := handlers["ListResiliencyGroups"]
	handlers["ListResiliencyGroups"] = func(in any) (any, error) {
		out, err := list(in)
		if in.(*directconnect.ListResiliencyGroupsInput).NextToken != nil {
			cancel()
		}
		return out, err
	}
	_, _, err := scanDXResiliencyGroups(ctx, newDXTestClient(handlers), newTestAccount(testAccountID), testRegion, st, testScanID)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v; want context.Canceled", err)
	}
	if got := h.gets.distinct(); len(got) != 0 {
		t.Errorf("GetResiliencyGroup calls = %v; want none after ctx ended", got)
	}
	rows := rowsByNativeID(t, st, TypeDirectConnectResiliencyGroup)
	for _, arn := range []string{dxTestGroupA, dxTestGroupB} {
		r, ok := rows[arn]
		if !ok {
			t.Errorf("group %s not stored", arn)
			continue
		}
		if assocs := dxGroupAssociationARNs(t, r); r.TagsJSON != nil || assocs != nil || sv(r.Name) != "group "+arn[len(arn)-4:] {
			t.Errorf("%s = name %q tags %v associations %v; want the summary row (its name, no tags, null)", arn, sv(r.Name), r.TagsJSON, assocs)
		}
	}
	// Every field the summary lists is stored, under the detail's keys.
	var got map[string]any
	if err := json.Unmarshal([]byte(rows[dxTestGroupA].AttributesJSON), &got); err != nil {
		t.Fatal(err)
	}
	want := map[string]any{
		"OwnerAccount": testAccountID, "ResiliencyGroupArn": dxTestGroupA, "ResiliencyGroupId": "rg-a",
		"ResiliencyGroupName": "group rg-a", "ResiliencyGroupType": "Managed", "State": "available",
		"Tags": nil, "Associations": nil,
	}
	if !maps.Equal(got, want) {
		t.Errorf("rg-a attributes = %v; want %v", got, want)
	}
}

// A denied group with no ARN in its summary is not stored, so it is warned
// about once as such, not also as stored from its summary.
func TestScanDXResiliencyGroups_DeniedWithoutARNWarnedOnce(t *testing.T) {
	st := newTestStore(t)
	warns := countOpWarnings(st, dxGetResiliencyGroupOp)
	h := &dxGroupHandlers{getErr: map[string]error{"rg-a": dcDenied}, summaryNoARN: map[string]bool{"rg-a": true}}
	if _, _, err := scanDXResiliencyGroups(context.Background(), newDXTestClient(h.handlers()), newTestAccount(testAccountID), testRegion, st, testScanID); err != nil {
		t.Fatalf("scanDXResiliencyGroups: %v", err)
	}
	if rows := rowsByNativeID(t, st, TypeDirectConnectResiliencyGroup); len(rows) != 1 {
		t.Errorf("rows = %v; want only rg-b", rows)
	}
	if *warns != 1 {
		t.Errorf("%s warnings = %d; want 1 (no ARN), no denial warning", dxGetResiliencyGroupOp, *warns)
	}
}

// A Get that answers without a group stores the summary and returns
// errDXNoGroupBody.
func TestScanDXResiliencyGroups_EmptyGetStoresSummary(t *testing.T) {
	st := newTestStore(t)
	h := &dxGroupHandlers{}
	handlers := h.handlers()
	get := handlers["GetResiliencyGroup"]
	handlers["GetResiliencyGroup"] = func(in any) (any, error) {
		if sdkaws.ToString(in.(*directconnect.GetResiliencyGroupInput).ResiliencyGroupId) == "rg-a" {
			return &directconnect.GetResiliencyGroupOutput{}, nil
		}
		return get(in)
	}
	_, _, err := scanDXResiliencyGroups(context.Background(), newDXTestClient(handlers), newTestAccount(testAccountID), testRegion, st, testScanID)
	if !errors.Is(err, errDXNoGroupBody) {
		t.Fatalf("err = %v; want errDXNoGroupBody", err)
	}
	a, ok := rowsByNativeID(t, st, TypeDirectConnectResiliencyGroup)[dxTestGroupA]
	if !ok {
		t.Fatalf("group rg-a not stored")
	}
	if a.TagsJSON != nil || sv(a.Name) != "group rg-a" || dxGroupAssociationARNs(t, a) != nil {
		t.Errorf("rg-a = name %q tags %v; want the summary row (its name, no tags, null associations)", sv(a.Name), a.TagsJSON)
	}
}

// The denial warning names the account and the region.
func TestScanDXResiliencyGroups_DenialWarningScope(t *testing.T) {
	st := newTestStore(t)
	var scopes []string
	st.OnWarn = func(w store.ScanWarning) {
		if w.Service == dxGetResiliencyGroupOp {
			scopes = append(scopes, w.Scope)
		}
	}
	h := &dxGroupHandlers{getErr: map[string]error{"rg-a": dcDenied}}
	if _, _, err := scanDXResiliencyGroups(context.Background(), newDXTestClient(h.handlers()), newTestAccount(testAccountID), testRegion, st, testScanID); err != nil {
		t.Fatalf("scanDXResiliencyGroups: %v", err)
	}
	if want := testAccountID + "/" + testRegion; !slices.Equal(scopes, []string{want}) {
		t.Errorf("denial warning scopes = %v; want [%s]", scopes, want)
	}
}

// A failed ListResiliencyGroups page keeps the groups listed before it.
func TestScanDXResiliencyGroups_ListErrorAfterPageKeepsGroups(t *testing.T) {
	for _, tc := range []struct {
		name      string
		err       error
		wantErr   bool
		wantWarns int
	}{
		{name: "client error", err: dcClientErr, wantErr: true},
		{name: "denied", err: dcDenied, wantWarns: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := countOpWarnings(st, dxListResiliencyGroupsOp)
			handlers := (&dxGroupHandlers{}).handlers()
			list := handlers["ListResiliencyGroups"]
			handlers["ListResiliencyGroups"] = func(in any) (any, error) {
				if in.(*directconnect.ListResiliencyGroupsInput).NextToken != nil {
					return nil, tc.err
				}
				return list(in)
			}
			_, _, err := scanDXResiliencyGroups(context.Background(), newDXTestClient(handlers), newTestAccount(testAccountID), testRegion, st, testScanID)
			if tc.wantErr != isAPIErrorCode(err, "DirectConnectClientException") || (!tc.wantErr && err != nil) {
				t.Fatalf("err = %v; want the DirectConnectClientException %v", err, tc.wantErr)
			}
			rows := rowsByNativeID(t, st, TypeDirectConnectResiliencyGroup)
			if _, ok := rows[dxTestGroupA]; !ok || len(rows) != 1 {
				t.Errorf("rows = %v; want only rg-a, listed before the failed page", rows)
			}
			// One warning for the id-less summary on the first page, plus the denial.
			if *warns != 1+tc.wantWarns {
				t.Errorf("warnings = %d; want %d", *warns, 1+tc.wantWarns)
			}
		})
	}
}

// dxAllHandlers answers every DX op: one connection, LAG, private VIF,
// gateway (with one association) and interconnect, and the dxGroupHandlers
// groups. override replaces individual ops.
func dxAllHandlers(override map[string]func(any) (any, error)) map[string]func(any) (any, error) {
	h := (&dxGroupHandlers{}).handlers()
	h["DescribeConnections"] = func(any) (any, error) {
		return &directconnect.DescribeConnectionsOutput{Connections: []directconnecttypes.Connection{{ConnectionId: sdkaws.String("dxcon-1")}}}, nil
	}
	h["DescribeLags"] = func(any) (any, error) {
		return &directconnect.DescribeLagsOutput{Lags: []directconnecttypes.Lag{{LagId: sdkaws.String("dxlag-1")}}}, nil
	}
	h["DescribeVirtualInterfaces"] = func(any) (any, error) {
		return &directconnect.DescribeVirtualInterfacesOutput{VirtualInterfaces: []directconnecttypes.VirtualInterface{
			{VirtualInterfaceId: sdkaws.String("dxvif-1"), VirtualInterfaceType: sdkaws.String("private")},
		}}, nil
	}
	h["DescribeDirectConnectGateways"] = func(any) (any, error) {
		return &directconnect.DescribeDirectConnectGatewaysOutput{DirectConnectGateways: []directconnecttypes.DirectConnectGateway{
			{DirectConnectGatewayId: sdkaws.String("g-1")},
		}}, nil
	}
	h["DescribeDirectConnectGatewayAssociations"] = func(in any) (any, error) {
		gw := sdkaws.ToString(in.(*directconnect.DescribeDirectConnectGatewayAssociationsInput).DirectConnectGatewayId)
		return &directconnect.DescribeDirectConnectGatewayAssociationsOutput{DirectConnectGatewayAssociations: []directconnecttypes.DirectConnectGatewayAssociation{
			{AssociationId: sdkaws.String("a-" + gw), DirectConnectGatewayId: sdkaws.String(gw)},
		}}, nil
	}
	h["DescribeInterconnects"] = func(any) (any, error) {
		return &directconnect.DescribeInterconnectsOutput{Interconnects: []directconnecttypes.Interconnect{{InterconnectId: sdkaws.String("dxcon-ic1")}}}, nil
	}
	for op, fn := range override {
		h[op] = fn
	}
	return h
}

// dxAllCounts is the rows dxAllHandlers stores per type.
func dxAllCounts() map[string]int {
	return map[string]int{
		TypeDirectConnectConnection: 1, TypeDirectConnectLag: 1, TypeDirectConnectPrivateVirtualInterface: 1,
		TypeDirectConnectDirectConnectGateway: 1, TypeDirectConnectDirectConnectGatewayAssociation: 1,
		TypeDirectConnectInterconnect: 1, TypeDirectConnectResiliencyGroup: 2,
	}
}

func TestScanDirectConnect_RunsEveryPhase(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	acct.cfg = opStubConfig(dxAllHandlers(nil), dcDenied)
	total, _, err := scanDirectConnect(context.Background(), acct, testRegion, st, testScanID)
	if err != nil {
		t.Fatalf("scanDirectConnect: %v", err)
	}
	if total != 8 {
		t.Errorf("total = %d; want 8", total)
	}
	for typ, want := range dxAllCounts() {
		if n := len(rowsByNativeID(t, st, typ)); n != want {
			t.Errorf("%s rows = %d; want %d", typ, n, want)
		}
	}
}

// Every phase runs whichever phase fails, and the failure is returned. A
// failed gateway listing leaves the association phase only the gateways
// listed before it.
func TestScanDirectConnectAll_PhaseErrorsAreIndependent(t *testing.T) {
	fail := func(any) (any, error) { return nil, dcClientErr }
	for _, tc := range []struct {
		sdkOp string
		fn    func(any) (any, error)
		lost  []string // the types the failure leaves with no rows
	}{
		{sdkOp: "DescribeConnections", fn: fail, lost: []string{TypeDirectConnectConnection}},
		{sdkOp: "DescribeLags", fn: fail, lost: []string{TypeDirectConnectLag}},
		{sdkOp: "DescribeVirtualInterfaces", fn: fail, lost: []string{TypeDirectConnectPrivateVirtualInterface}},
		{sdkOp: "DescribeDirectConnectGateways", fn: fail, lost: []string{TypeDirectConnectDirectConnectGateway, TypeDirectConnectDirectConnectGatewayAssociation}},
		{sdkOp: "DescribeDirectConnectGateways", fn: func(in any) (any, error) {
			if in.(*directconnect.DescribeDirectConnectGatewaysInput).NextToken != nil {
				return nil, dcClientErr
			}
			return &directconnect.DescribeDirectConnectGatewaysOutput{NextToken: sdkaws.String("p2"), DirectConnectGateways: []directconnecttypes.DirectConnectGateway{
				{DirectConnectGatewayId: sdkaws.String("g-1")},
			}}, nil
		}},
		{sdkOp: "DescribeDirectConnectGatewayAssociations", fn: fail, lost: []string{TypeDirectConnectDirectConnectGatewayAssociation}},
		{sdkOp: "DescribeInterconnects", fn: fail, lost: []string{TypeDirectConnectInterconnect}},
		{sdkOp: "ListResiliencyGroups", fn: fail, lost: []string{TypeDirectConnectResiliencyGroup}},
	} {
		t.Run(tc.sdkOp+" lost "+strings.Join(tc.lost, ","), func(t *testing.T) {
			st := newTestStore(t)
			client := newDXTestClient(dxAllHandlers(map[string]func(any) (any, error){tc.sdkOp: tc.fn}))
			_, _, err := scanDirectConnectAll(context.Background(), client, newTestAccount(testAccountID), testRegion, st, testScanID)
			if !isAPIErrorCode(err, "DirectConnectClientException") {
				t.Errorf("err = %v; want the failed phase's DirectConnectClientException", err)
			}
			want := dxAllCounts()
			for _, typ := range tc.lost {
				want[typ] = 0
			}
			for typ, n := range want {
				if got := len(rowsByNativeID(t, st, typ)); got != n {
					t.Errorf("%s rows = %d; want %d", typ, got, n)
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
		{name: "connections", throttledOp: "DescribeConnections", label: dxDescribeConnectionsOp, failedOp: "ListResiliencyGroups", wantTotal: 5},
		{name: "interconnects", throttledOp: "DescribeInterconnects", label: dxDescribeInterconnectsOp, failedOp: "DescribeConnections", wantTotal: 6},
		{name: "resiliency groups", throttledOp: "ListResiliencyGroups", label: dxListResiliencyGroupsOp, failedOp: "DescribeInterconnects", wantTotal: 5},
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

// A throttled phase alone is returned, not demoted, so the scan is marked
// partial rather than complete.
func TestScanDirectConnectAll_TransientAloneReturned(t *testing.T) {
	st := newTestStore(t)
	client := newDXTestClient(dxAllHandlers(map[string]func(any) (any, error){
		"DescribeLags": func(any) (any, error) { return nil, dcThrottled },
	}))
	total, _, err := scanDirectConnectAll(context.Background(), client, newTestAccount(testAccountID), testRegion, st, testScanID)
	if got := classifyServiceError(err); got != outcomeTransient {
		t.Errorf("err = %v (outcome %v); want outcomeTransient", err, got)
	}
	if total != 7 {
		t.Errorf("total = %d; want 7 (every row but the LAG)", total)
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

// dxTags builds Direct Connect tags from key, value pairs.
func dxTags(kv ...string) []directconnecttypes.Tag {
	var tags []directconnecttypes.Tag
	for i := 0; i < len(kv); i += 2 {
		tags = append(tags, directconnecttypes.Tag{Key: sdkaws.String(kv[i]), Value: sdkaws.String(kv[i+1])})
	}
	return tags
}

// dxWantRow is a stored row a listing test expects: its type, Name, Status,
// one attribute and its tags (nil for none).
type dxWantRow struct {
	typ, name, status string
	attrKey, attrVal  string
	tags              map[string]string
}

func assertDXRows(t *testing.T, st *store.Store, want map[string]dxWantRow) {
	t.Helper()
	got := map[string]store.Resource{}
	for _, w := range want {
		maps.Copy(got, rowsByNativeID(t, st, w.typ))
	}
	if len(got) != len(want) {
		t.Errorf("stored %d rows; want %d", len(got), len(want))
	}
	for nativeID, w := range want {
		r, ok := got[nativeID]
		if !ok {
			t.Errorf("no row with NativeID %s", nativeID)
			continue
		}
		if r.Type != w.typ || sv(r.Name) != w.name || sv(r.Status) != w.status || sv(r.Region) != testRegion {
			t.Errorf("%s = type %s name %q status %q region %q; want %s/%q/%q/%s", nativeID, r.Type, sv(r.Name), sv(r.Status), sv(r.Region), w.typ, w.name, w.status, testRegion)
		}
		var attrs map[string]any
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil || attrs[w.attrKey] != w.attrVal {
			t.Errorf("%s attrs %s = %v (err %v); want %q", nativeID, w.attrKey, attrs[w.attrKey], err, w.attrVal)
		}
		if w.tags == nil {
			if r.TagsJSON != nil {
				t.Errorf("%s tags = %s; want none", nativeID, *r.TagsJSON)
			}
			continue
		}
		assertTagsMap(t, r.TagsJSON, w.tags)
	}
}

// dxListCase is one region-wide Direct Connect listing: its two pages (the
// first with an element that cannot be stored), the rows they store and the
// not-stored warnings.
type dxListCase struct {
	op, sdkOp string
	pages     func(any) (any, error)
	page1     func() any // a first page holding one element and NextToken "p2"
	scan      func(context.Context, dxAPI, *account, string, *store.Store, string) (int, int, error)
	want      map[string]dxWantRow
	wantWarns int
}

func dxListCases() []dxListCase {
	return []dxListCase{
		{
			op: dxDescribeConnectionsOp, sdkOp: "DescribeConnections",
			pages: dxPaged(
				func(p *directconnect.DescribeConnectionsInput) *int32 { return p.MaxResults },
				func(p *directconnect.DescribeConnectionsInput) *string { return p.NextToken },
				&directconnect.DescribeConnectionsOutput{NextToken: sdkaws.String("p2"), Connections: []directconnecttypes.Connection{
					{ConnectionId: sdkaws.String("dxcon-1"), ConnectionName: sdkaws.String("conn one"), ConnectionState: directconnecttypes.ConnectionStateAvailable, Location: sdkaws.String("EqDC2"), Tags: dxTags("env", "prod")},
					{ConnectionName: sdkaws.String("no id")},
				}},
				&directconnect.DescribeConnectionsOutput{Connections: []directconnecttypes.Connection{
					{ConnectionId: sdkaws.String("dxcon-2"), ConnectionState: directconnecttypes.ConnectionStateDown, Location: sdkaws.String("EqSE2")},
				}}),
			page1: func() any {
				return &directconnect.DescribeConnectionsOutput{NextToken: sdkaws.String("p2"), Connections: []directconnecttypes.Connection{{ConnectionId: sdkaws.String("dxcon-1")}}}
			},
			scan: scanDXConnections,
			want: map[string]dxWantRow{
				"arn:aws:directconnect:us-east-1:123456789012:dxcon/dxcon-1": {TypeDirectConnectConnection, "conn one", "available", "Location", "EqDC2", map[string]string{"env": "prod"}},
				"arn:aws:directconnect:us-east-1:123456789012:dxcon/dxcon-2": {TypeDirectConnectConnection, "dxcon-2", "down", "Location", "EqSE2", nil},
			},
			wantWarns: 1,
		},
		{
			op: dxDescribeLagsOp, sdkOp: "DescribeLags",
			pages: dxPaged(
				func(p *directconnect.DescribeLagsInput) *int32 { return p.MaxResults },
				func(p *directconnect.DescribeLagsInput) *string { return p.NextToken },
				&directconnect.DescribeLagsOutput{NextToken: sdkaws.String("p2"), Lags: []directconnecttypes.Lag{
					{LagId: sdkaws.String("dxlag-1"), LagName: sdkaws.String("lag one"), LagState: directconnecttypes.LagStateAvailable, Location: sdkaws.String("EqDC2"), Tags: dxTags("team", "net")},
					{LagName: sdkaws.String("no id")},
				}},
				&directconnect.DescribeLagsOutput{Lags: []directconnecttypes.Lag{
					{LagId: sdkaws.String("dxlag-2"), LagState: directconnecttypes.LagStatePending, Location: sdkaws.String("EqSE2")},
				}}),
			page1: func() any {
				return &directconnect.DescribeLagsOutput{NextToken: sdkaws.String("p2"), Lags: []directconnecttypes.Lag{{LagId: sdkaws.String("dxlag-1")}}}
			},
			scan: scanDXLags,
			want: map[string]dxWantRow{
				"arn:aws:directconnect:us-east-1:123456789012:dxlag/dxlag-1": {TypeDirectConnectLag, "lag one", "available", "Location", "EqDC2", map[string]string{"team": "net"}},
				"arn:aws:directconnect:us-east-1:123456789012:dxlag/dxlag-2": {TypeDirectConnectLag, "dxlag-2", "pending", "Location", "EqSE2", nil},
			},
			wantWarns: 1,
		},
		{
			op: dxDescribeVirtualInterfacesOp, sdkOp: "DescribeVirtualInterfaces",
			pages: dxPaged(
				func(p *directconnect.DescribeVirtualInterfacesInput) *int32 { return p.MaxResults },
				func(p *directconnect.DescribeVirtualInterfacesInput) *string { return p.NextToken },
				&directconnect.DescribeVirtualInterfacesOutput{NextToken: sdkaws.String("p2"), VirtualInterfaces: []directconnecttypes.VirtualInterface{
					{VirtualInterfaceId: sdkaws.String("dxvif-1"), VirtualInterfaceName: sdkaws.String("vif one"), VirtualInterfaceType: sdkaws.String("private"), VirtualInterfaceState: directconnecttypes.VirtualInterfaceStateAvailable, ConnectionId: sdkaws.String("dxcon-1"), Tags: dxTags("env", "prod")},
					{VirtualInterfaceName: sdkaws.String("no id"), VirtualInterfaceType: sdkaws.String("private")},
					{VirtualInterfaceId: sdkaws.String("dxvif-x"), VirtualInterfaceType: sdkaws.String("unknown")},
				}},
				&directconnect.DescribeVirtualInterfacesOutput{VirtualInterfaces: []directconnecttypes.VirtualInterface{
					{VirtualInterfaceId: sdkaws.String("dxvif-2"), VirtualInterfaceType: sdkaws.String("public"), VirtualInterfaceState: directconnecttypes.VirtualInterfaceStateDown, ConnectionId: sdkaws.String("dxcon-2")},
					{VirtualInterfaceId: sdkaws.String("dxvif-3"), VirtualInterfaceType: sdkaws.String("transit"), VirtualInterfaceState: directconnecttypes.VirtualInterfaceStateAvailable, ConnectionId: sdkaws.String("dxlag-1"), Tags: dxTags("k", "v")},
				}}),
			page1: func() any {
				return &directconnect.DescribeVirtualInterfacesOutput{NextToken: sdkaws.String("p2"), VirtualInterfaces: []directconnecttypes.VirtualInterface{
					{VirtualInterfaceId: sdkaws.String("dxvif-1"), VirtualInterfaceType: sdkaws.String("private")},
				}}
			},
			scan: scanDXVirtualInterfaces,
			want: map[string]dxWantRow{
				"arn:aws:directconnect:us-east-1:123456789012:dxvif/dxvif-1": {TypeDirectConnectPrivateVirtualInterface, "vif one", "available", "ConnectionId", "dxcon-1", map[string]string{"env": "prod"}},
				"arn:aws:directconnect:us-east-1:123456789012:dxvif/dxvif-2": {TypeDirectConnectPublicVirtualInterface, "dxvif-2", "down", "ConnectionId", "dxcon-2", nil},
				"arn:aws:directconnect:us-east-1:123456789012:dxvif/dxvif-3": {TypeDirectConnectTransitVirtualInterface, "dxvif-3", "available", "ConnectionId", "dxlag-1", map[string]string{"k": "v"}},
			},
			wantWarns: 2, // one for the id-less VIF, one for the unmapped type
		},
		{
			op: dxDescribeGatewaysOp, sdkOp: "DescribeDirectConnectGateways",
			pages: dxPaged(
				func(p *directconnect.DescribeDirectConnectGatewaysInput) *int32 { return p.MaxResults },
				func(p *directconnect.DescribeDirectConnectGatewaysInput) *string { return p.NextToken },
				&directconnect.DescribeDirectConnectGatewaysOutput{NextToken: sdkaws.String("p2"), DirectConnectGateways: []directconnecttypes.DirectConnectGateway{
					{DirectConnectGatewayId: sdkaws.String("g-1"), DirectConnectGatewayName: sdkaws.String("gw one"), DirectConnectGatewayState: directconnecttypes.DirectConnectGatewayStateAvailable, OwnerAccount: sdkaws.String(testAccountID), Tags: dxTags("env", "prod")},
					{DirectConnectGatewayName: sdkaws.String("no id")},
				}},
				&directconnect.DescribeDirectConnectGatewaysOutput{DirectConnectGateways: []directconnecttypes.DirectConnectGateway{
					{DirectConnectGatewayId: sdkaws.String("g-2"), DirectConnectGatewayState: directconnecttypes.DirectConnectGatewayStatePending, OwnerAccount: sdkaws.String(testAccountID)},
				}}),
			page1: func() any {
				return &directconnect.DescribeDirectConnectGatewaysOutput{NextToken: sdkaws.String("p2"), DirectConnectGateways: []directconnecttypes.DirectConnectGateway{{DirectConnectGatewayId: sdkaws.String("g-1")}}}
			},
			scan: func(ctx context.Context, client dxAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
				_, t, i, err := scanDXGateways(ctx, client, acct, region, st, scanID)
				return t, i, err
			},
			want: map[string]dxWantRow{
				"arn:aws:directconnect::123456789012:dx-gateway/g-1": {TypeDirectConnectDirectConnectGateway, "gw one", "available", "OwnerAccount", testAccountID, map[string]string{"env": "prod"}},
				"arn:aws:directconnect::123456789012:dx-gateway/g-2": {TypeDirectConnectDirectConnectGateway, "g-2", "pending", "OwnerAccount", testAccountID, nil},
			},
			wantWarns: 1,
		},
	}
}

func TestScanDXListings_TwoPagesStoredWithTags(t *testing.T) {
	for _, tc := range dxListCases() {
		t.Run(tc.op, func(t *testing.T) {
			st := newTestStore(t)
			warns := countOpWarnings(st, tc.op)
			client := newDXTestClient(map[string]func(any) (any, error){tc.sdkOp: tc.pages})
			total, inserted, err := tc.scan(context.Background(), client, newTestAccount(testAccountID), testRegion, st, testScanID)
			if err != nil {
				t.Fatalf("scan: %v", err)
			}
			if total != len(tc.want) || inserted != len(tc.want) {
				t.Errorf("total, inserted = %d, %d; want %d, %d", total, inserted, len(tc.want), len(tc.want))
			}
			assertDXRows(t, st, tc.want)
			if *warns != tc.wantWarns {
				t.Errorf("not-stored warnings = %d; want %d", *warns, tc.wantWarns)
			}
		})
	}
}

// A denied first page warns once; a failed later page keeps the rows read
// before it and returns the error.
func TestScanDXListings_PageErrors(t *testing.T) {
	for _, tc := range dxListCases() {
		for _, ec := range []struct {
			name                string
			firstErr, secondErr error
			wantErr             bool
			wantRows, wantWarns int
		}{
			{name: "denied", firstErr: dcDenied, wantWarns: 1},
			{name: "denied after a page keeps its rows", secondErr: dcDenied, wantRows: 1, wantWarns: 1},
			{name: "client error", firstErr: dcClientErr, wantErr: true},
			{name: "client error after a page keeps its rows", secondErr: dcClientErr, wantErr: true, wantRows: 1},
		} {
			t.Run(tc.op+"/"+ec.name, func(t *testing.T) {
				st := newTestStore(t)
				warns := countOpWarnings(st, tc.op)
				client := newDXTestClient(map[string]func(any) (any, error){
					tc.sdkOp: func(in any) (any, error) {
						// Every DX list input is a struct pointer with a NextToken *string.
						if reflect.ValueOf(in).Elem().FieldByName("NextToken").IsNil() {
							if ec.firstErr != nil {
								return nil, ec.firstErr
							}
							return tc.page1(), nil
						}
						return nil, ec.secondErr
					},
				})
				total, _, err := tc.scan(context.Background(), client, newTestAccount(testAccountID), testRegion, st, testScanID)
				if ec.wantErr != isAPIErrorCode(err, "DirectConnectClientException") || (!ec.wantErr && err != nil) {
					t.Fatalf("err = %v; want the DirectConnectClientException %v", err, ec.wantErr)
				}
				if total != ec.wantRows {
					t.Errorf("total = %d; want %d", total, ec.wantRows)
				}
				if *warns != ec.wantWarns {
					t.Errorf("warnings = %d; want %d", *warns, ec.wantWarns)
				}
			})
		}
	}
}

// Gateways are global: a scan of any region but us-east-1 neither lists nor
// stores them.
func TestScanDXGateways_OnlyInUSEast1(t *testing.T) {
	st := newTestStore(t)
	client := newDXTestClient(map[string]func(any) (any, error){
		"DescribeDirectConnectGateways": func(any) (any, error) { return nil, errors.New("listed outside us-east-1") },
	})
	ids, total, _, err := scanDXGateways(context.Background(), client, newTestAccount(testAccountID), "us-west-2", st, testScanID)
	if err != nil || total != 0 || ids != nil {
		t.Errorf("scanDXGateways(us-west-2) = %v, %d, %v; want nil, 0, nil", ids, total, err)
	}
}

// The gateway phase hands on the ids of the gateways it stored, those read
// before a failed page included.
func TestScanDXGateways_ReturnsStoredIDs(t *testing.T) {
	st := newTestStore(t)
	client := newDXTestClient(map[string]func(any) (any, error){
		"DescribeDirectConnectGateways": func(in any) (any, error) {
			if in.(*directconnect.DescribeDirectConnectGatewaysInput).NextToken != nil {
				return nil, dcClientErr
			}
			return &directconnect.DescribeDirectConnectGatewaysOutput{NextToken: sdkaws.String("p2"), DirectConnectGateways: []directconnecttypes.DirectConnectGateway{
				{DirectConnectGatewayId: sdkaws.String("g-1")}, {DirectConnectGatewayName: sdkaws.String("no id")},
			}}, nil
		},
	})
	ids, _, _, err := scanDXGateways(context.Background(), client, newTestAccount(testAccountID), testRegion, st, testScanID)
	if !isAPIErrorCode(err, "DirectConnectClientException") {
		t.Errorf("err = %v; want the DirectConnectClientException", err)
	}
	if !slices.Equal(ids, []string{"g-1"}) {
		t.Errorf("ids = %v; want [g-1]", ids)
	}
}

// dxAssociationPages answers DescribeDirectConnectGatewayAssociations: g-1
// has two pages (the first with an id-less association), g-2 one; a call
// without the model's page cap fails, so it is pinned with the gateway id.
func dxAssociationPages(fail map[string]error) func(any) (any, error) {
	return func(in any) (any, error) {
		p := in.(*directconnect.DescribeDirectConnectGatewayAssociationsInput)
		gw := sdkaws.ToString(p.DirectConnectGatewayId)
		if err := fail[gw]; err != nil {
			return nil, err
		}
		if sdkaws.ToInt32(p.MaxResults) != dxPageLimit {
			return nil, apiErr("DirectConnectClientException", "MaxResults must be at most 100")
		}
		assoc := func(id string, state directconnecttypes.DirectConnectGatewayAssociationState) directconnecttypes.DirectConnectGatewayAssociation {
			return directconnecttypes.DirectConnectGatewayAssociation{
				AssociationId: sdkaws.String(id), DirectConnectGatewayId: sdkaws.String(gw), AssociationState: state,
				AssociatedGateway: &directconnecttypes.AssociatedGateway{Id: sdkaws.String("tgw-" + id)},
			}
		}
		switch {
		case gw == "g-1" && p.NextToken == nil:
			return &directconnect.DescribeDirectConnectGatewayAssociationsOutput{NextToken: sdkaws.String("p2"), DirectConnectGatewayAssociations: []directconnecttypes.DirectConnectGatewayAssociation{
				assoc("a-1", directconnecttypes.DirectConnectGatewayAssociationStateAssociated), {DirectConnectGatewayId: sdkaws.String(gw)},
			}}, nil
		case gw == "g-1" && sdkaws.ToString(p.NextToken) == "p2":
			return &directconnect.DescribeDirectConnectGatewayAssociationsOutput{DirectConnectGatewayAssociations: []directconnecttypes.DirectConnectGatewayAssociation{
				assoc("a-2", directconnecttypes.DirectConnectGatewayAssociationStateAssociating),
			}}, nil
		case gw == "g-2" && p.NextToken == nil:
			return &directconnect.DescribeDirectConnectGatewayAssociationsOutput{DirectConnectGatewayAssociations: []directconnecttypes.DirectConnectGatewayAssociation{
				assoc("a-3", directconnecttypes.DirectConnectGatewayAssociationStateDisassociated),
			}}, nil
		}
		return nil, fmt.Errorf("unexpected call for %s token %q", gw, sdkaws.ToString(p.NextToken))
	}
}

func TestScanDXGatewayAssociations_StoredPerGateway(t *testing.T) {
	st := newTestStore(t)
	warns := countOpWarnings(st, dxDescribeGatewayAssociationsOp)
	client := newDXTestClient(map[string]func(any) (any, error){"DescribeDirectConnectGatewayAssociations": dxAssociationPages(nil)})
	total, _, err := scanDXGatewayAssociations(context.Background(), client, newTestAccount(testAccountID), testRegion, st, testScanID, []string{"g-1", "g-2"})
	if err != nil {
		t.Fatalf("scanDXGatewayAssociations: %v", err)
	}
	if total != 3 {
		t.Errorf("total = %d; want 3", total)
	}
	assertDXRows(t, st, map[string]dxWantRow{
		"arn:aws:directconnect::123456789012:dx-gateway/g-1/association/a-1": {TypeDirectConnectDirectConnectGatewayAssociation, "a-1", "associated", "DirectConnectGatewayId", "g-1", nil},
		"arn:aws:directconnect::123456789012:dx-gateway/g-1/association/a-2": {TypeDirectConnectDirectConnectGatewayAssociation, "a-2", "associating", "DirectConnectGatewayId", "g-1", nil},
		"arn:aws:directconnect::123456789012:dx-gateway/g-2/association/a-3": {TypeDirectConnectDirectConnectGatewayAssociation, "a-3", "disassociated", "DirectConnectGatewayId", "g-2", nil},
	})
	if *warns != 1 {
		t.Errorf("warnings = %d; want 1 for the association without an AssociationId", *warns)
	}
}

// One gateway's failure costs the other gateways nothing: a denial warns once,
// any other error is returned after their associations are stored.
func TestScanDXGatewayAssociations_PerGatewayErrors(t *testing.T) {
	for _, tc := range []struct {
		name      string
		err       error
		wantErr   bool
		wantWarns int
	}{
		{name: "denied", err: dcDenied, wantWarns: 2},
		{name: "client error", err: dcClientErr, wantErr: true, wantWarns: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := countOpWarnings(st, dxDescribeGatewayAssociationsOp)
			client := newDXTestClient(map[string]func(any) (any, error){
				"DescribeDirectConnectGatewayAssociations": dxAssociationPages(map[string]error{"g-0": tc.err, "g-9": tc.err}),
			})
			total, _, err := scanDXGatewayAssociations(context.Background(), client, newTestAccount(testAccountID), testRegion, st, testScanID, []string{"g-0", "g-1", "g-9", "g-2"})
			if tc.wantErr != isAPIErrorCode(err, "DirectConnectClientException") || (!tc.wantErr && err != nil) {
				t.Fatalf("err = %v; want the DirectConnectClientException %v", err, tc.wantErr)
			}
			if total != 3 {
				t.Errorf("total = %d; want 3 (every association of g-1 and g-2)", total)
			}
			// The id-less association warns once; a denial adds one warning for both gateways.
			if *warns != tc.wantWarns {
				t.Errorf("warnings = %d; want %d", *warns, tc.wantWarns)
			}
		})
	}
}

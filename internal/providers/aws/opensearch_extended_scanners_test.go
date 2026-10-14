package aws

import (
	"context"
	"maps"
	"slices"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/opensearch"
	opensearchtypes "github.com/aws/aws-sdk-go-v2/service/opensearch/types"
	"github.com/icearp/disco-cli/store"
)

const (
	osApp1ARN = "arn:aws:opensearch:us-east-1:123456789012:application/a1"
	osApp2ARN = "arn:aws:opensearch:us-east-1:123456789012:application/a2"
	osApp3ARN = "arn:aws:opensearch:us-east-1:123456789012:application/a3"
)

var osApps = []childParent{{id: "a1", arn: osApp1ARN}, {id: "a2", arn: osApp2ARN}, {id: "a3", arn: osApp3ARN}}

// osAttachmentHandler serves a1 two pages (att1; then att2 and an element
// with no id) and a3 one (att3). fail maps an app id, or "a1/p2" for a1's
// second page, to the error that call returns; every call's app id is recorded.
func osAttachmentHandler(apps *parentCalls, fail map[string]error) func(in any) (any, error) {
	return func(in any) (any, error) {
		req := in.(*opensearch.ListDataSourceAttachmentsInput)
		id := sv(req.Id)
		apps.add(id)
		key := id
		if req.NextToken != nil {
			key += "/p2"
		}
		if err := fail[key]; err != nil {
			return nil, err
		}
		switch {
		case id == "a1" && req.NextToken == nil:
			return &opensearch.ListDataSourceAttachmentsOutput{NextToken: ptrStr("t2"), Attachments: []opensearchtypes.DataSourceAttachmentSummary{
				{AttachmentId: ptrStr("att1"), DataSourceArn: ptrStr(osDomainARN), Status: opensearchtypes.DataSourceAttachmentStatusAttached},
			}}, nil
		case id == "a1":
			return &opensearch.ListDataSourceAttachmentsOutput{Attachments: []opensearchtypes.DataSourceAttachmentSummary{
				{AttachmentId: ptrStr("att2")}, {DataSourceArn: ptrStr(osDomainARN)},
			}}, nil
		case id == "a3":
			return &opensearch.ListDataSourceAttachmentsOutput{Attachments: []opensearchtypes.DataSourceAttachmentSummary{
				{AttachmentId: ptrStr("att3"), Status: opensearchtypes.DataSourceAttachmentStatusFailed},
			}}, nil
		}
		return &opensearch.ListDataSourceAttachmentsOutput{}, nil
	}
}

func scanOSAttachments(t *testing.T, st *store.Store, handler func(in any) (any, error)) (int, error) {
	t.Helper()
	seedParentRows(t, st, TypeOpenSearchApplication, osApp1ARN, osApp2ARN, osApp3ARN)
	client := osClient(map[string]func(any) (any, error){"ListDataSourceAttachments": handler})
	total, _, err := scanOpenSearchDataSourceAttachments(context.Background(), client, newTestAccount(testAccountID), testRegion, st, testScanID, osApps)
	return total, err
}

func TestScanOpenSearchDataSourceAttachments_StoresEveryAppsAttachments(t *testing.T) {
	st := newTestStore(t)
	var apps parentCalls
	total, err := scanOSAttachments(t, st, osAttachmentHandler(&apps, nil))
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if got := apps.distinct(); len(apps.ids) != 4 || !slices.Equal(got, []string{"a1", "a2", "a3"}) {
		t.Errorf("apps queried = %v; want a1 (two pages), a2, a3", apps.ids)
	}
	rows := rowsByNativeID(t, st, TypeOpenSearchDataSourceAttachment)
	want := []string{
		osApp1ARN + "/data-source-attachment/att1",
		osApp1ARN + "/data-source-attachment/att2",
		osApp3ARN + "/data-source-attachment/att3",
	}
	if ids := slices.Sorted(maps.Keys(rows)); total != 3 || !slices.Equal(ids, want) {
		t.Fatalf("total = %d, rows = %v; want 3, %v", total, ids, want)
	}
	att1 := rows[want[0]]
	if sv(att1.Name) != "att1" || sv(att1.Status) != "ATTACHED" || sv(att1.Region) != testRegion {
		t.Errorf("att1 = name %q status %q region %q", sv(att1.Name), sv(att1.Status), sv(att1.Region))
	}
	if att2 := rows[want[1]]; att2.Status != nil {
		t.Errorf("att2 status = %q; want unset", sv(att2.Status))
	}
	assertContainsNative(t, st, osApp1ARN, want[0])
	assertContainsNative(t, st, osApp1ARN, want[1])
	assertContainsNative(t, st, osApp3ARN, want[2])
	assertContainsEdgesDeclared(t, st)
}

// A vanished app (any page) or a region that does not serve the op (an app's
// first call only) is skipped silently;
// a denial warns once and keeps the other apps' rows; anything else fails.
func TestScanOpenSearchDataSourceAttachments_Errors(t *testing.T) {
	for _, tc := range []struct {
		name      string
		fail      map[string]error
		wantCode  string
		wantTotal int
		wantWarns int
	}{
		{name: "app gone", fail: map[string]error{"a1": osGone}, wantTotal: 1},
		{name: "app gone on page two", fail: map[string]error{"a1/p2": osGone}, wantTotal: 1},
		{name: "op not deployed", fail: map[string]error{"a1": osUnknown, "a3": osUnknown}},
		{name: "not deployed on page two", fail: map[string]error{"a1/p2": osUnknown}, wantCode: "UnknownOperationException"},
		{name: "denied on two apps", fail: map[string]error{"a1": osDenied, "a2": osDenied}, wantTotal: 1, wantWarns: 1},
		{name: "other", fail: map[string]error{"a3": osBadInput}, wantCode: "ValidationException"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := countOpWarnings(st, "opensearch:ListDataSourceAttachments")
			var apps parentCalls
			total, err := scanOSAttachments(t, st, osAttachmentHandler(&apps, tc.fail))
			if tc.wantCode == "" && err != nil || tc.wantCode != "" && !isAPIErrorCode(err, tc.wantCode) {
				t.Errorf("err = %v; want code %q", err, tc.wantCode)
			}
			if got := len(rowsByNativeID(t, st, TypeOpenSearchDataSourceAttachment)); total != tc.wantTotal || got != tc.wantTotal {
				t.Errorf("total = %d, stored = %d; want %d", total, got, tc.wantTotal)
			}
			if *warns != tc.wantWarns {
				t.Errorf("warnings = %d; want %d", *warns, tc.wantWarns)
			}
		})
	}
}

func TestScanOpenSearchDataSourceAttachments_NoApps(t *testing.T) {
	st := newTestStore(t)
	client := osClient(map[string]func(any) (any, error){})
	total, _, err := scanOpenSearchDataSourceAttachments(context.Background(), client, newTestAccount(testAccountID), testRegion, st, testScanID, nil)
	if err != nil || total != 0 {
		t.Errorf("total = %d, err = %v; want 0, nil", total, err)
	}
}

func TestScanOpenSearchApplications_ReturnsAttachmentParents(t *testing.T) {
	st := newTestStore(t)
	client := osClient(map[string]func(any) (any, error){
		"ListApplications": func(any) (any, error) {
			return &opensearch.ListApplicationsOutput{ApplicationSummaries: []opensearchtypes.ApplicationSummary{
				{Id: ptrStr("a1"), Arn: ptrStr(osApp1ARN), Name: ptrStr("ui")},
				{Arn: ptrStr(osApp2ARN)},
				{Id: ptrStr("a3")},
			}}, nil
		},
	})
	parents, total, _, err := scanOpenSearchApplications(context.Background(), client, newTestAccount(testAccountID), testRegion, st, testScanID)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if want := []childParent{{id: "a1", arn: osApp1ARN}}; total != 2 || !slices.Equal(parents, want) {
		t.Errorf("total = %d, parents = %v; want 2 rows, %v (a parent needs both id and ARN)", total, parents, want)
	}
}

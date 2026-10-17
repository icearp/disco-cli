package aws

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/qapps"
	qappstypes "github.com/aws/aws-sdk-go-v2/service/qapps/types"
	"github.com/aws/aws-sdk-go-v2/service/qbusiness"
	qbtypes "github.com/aws/aws-sdk-go-v2/service/qbusiness/types"
	"github.com/icearp/disco-cli/store"
)

var qappsDenied = apiErr("AccessDeniedException", "User: arn:aws:iam::123:user/x is not authorized to perform: qapps:List")

func qappsTestInstanceARN(id string) string {
	return "arn:aws:qapps:" + testRegion + ":" + testAccountID + ":application/" + id
}

func newQAppsTestClient(handlers map[string]func(in any) (any, error)) *qapps.Client {
	return qapps.NewFromConfig(opStubConfig(handlers, qappsDenied))
}

func qappsCategory(id, title string, appCount int32) qappstypes.Category {
	return qappstypes.Category{Id: sdkaws.String(id), Title: sdkaws.String(title), AppCount: sdkaws.Int32(appCount), Color: sdkaws.String("#FF6600")}
}

func qappsLibraryItem(id string) qappstypes.LibraryItemMember {
	return qappstypes.LibraryItemMember{
		LibraryItemId: sdkaws.String(id), AppId: sdkaws.String("app-" + id), AppVersion: sdkaws.Int32(3),
		Status: sdkaws.String("PUBLISHED"), CreatedAt: sdkaws.Time(time.Date(2024, 5, 21, 23, 17, 27, 0, time.UTC)),
		CreatedBy: sdkaws.String("user-1"), RatingCount: sdkaws.Int32(4), UserCount: sdkaws.Int32(5),
		Categories: []qappstypes.Category{{Id: sdkaws.String("cat-1"), Title: sdkaws.String("HR")}},
	}
}

func qappsQApp(instanceID, appID, title string) qappstypes.UserAppItem {
	arn := qappsTestInstanceARN(instanceID) + "/qapp/" + appID
	return qappstypes.UserAppItem{AppArn: &arn, AppId: sdkaws.String(appID), Title: sdkaws.String(title)}
}

// qappsQAppsHandler answers ListQApps from pages keyed by instance id, as
// qappsLibraryHandler does ListLibraryItems.
func qappsQAppsHandler(calls *parentCalls, pages map[string][][]qappstypes.UserAppItem, errs map[string]error) func(any) (any, error) {
	return func(in any) (any, error) {
		req := in.(*qapps.ListQAppsInput)
		if sdkaws.ToInt32(req.Limit) != qappsPageLimit {
			return nil, fmt.Errorf("Limit = %v; want %d", req.Limit, qappsPageLimit)
		}
		id := sdkaws.ToString(req.InstanceId)
		calls.add(id)
		page := 0
		if sdkaws.ToString(req.NextToken) == "p2" {
			page = 1
			if err := errs[id+"/p2"]; err != nil {
				return nil, err
			}
		} else if err := errs[id]; err != nil {
			return nil, err
		}
		out := &qapps.ListQAppsOutput{}
		if page < len(pages[id]) {
			out.Apps = pages[id][page]
		}
		if page == 0 && len(pages[id]) > 1 {
			out.NextToken = sdkaws.String("p2")
		}
		return out, nil
	}
}

// qappsCategoriesHandler answers ListCategories from categories keyed by
// instance id; errs fails an instance's call.
func qappsCategoriesHandler(calls *parentCalls, cats map[string][]qappstypes.Category, errs map[string]error) func(any) (any, error) {
	return func(in any) (any, error) {
		id := sdkaws.ToString(in.(*qapps.ListCategoriesInput).InstanceId)
		calls.add(id)
		if err := errs[id]; err != nil {
			return nil, err
		}
		return &qapps.ListCategoriesOutput{Categories: cats[id]}, nil
	}
}

// qappsLibraryHandler answers ListLibraryItems from pages keyed by instance
// id: the first page carries NextToken "p2" when there is a second. errs
// fails an instance's call; a "/p2" key fails only its second page. A call
// whose Limit is not the model cap fails, so the page size is pinned.
func qappsLibraryHandler(calls *parentCalls, pages map[string][][]qappstypes.LibraryItemMember, errs map[string]error) func(any) (any, error) {
	return func(in any) (any, error) {
		req := in.(*qapps.ListLibraryItemsInput)
		if sdkaws.ToInt32(req.Limit) != qappsPageLimit {
			return nil, fmt.Errorf("Limit = %v; want %d", req.Limit, qappsPageLimit)
		}
		id := sdkaws.ToString(req.InstanceId)
		calls.add(id)
		page := 0
		if sdkaws.ToString(req.NextToken) == "p2" {
			page = 1
			if err := errs[id+"/p2"]; err != nil {
				return nil, err
			}
		} else if err := errs[id]; err != nil {
			return nil, err
		}
		out := &qapps.ListLibraryItemsOutput{}
		if page < len(pages[id]) {
			out.LibraryItems = pages[id][page]
		}
		if page == 0 && len(pages[id]) > 1 {
			out.NextToken = sdkaws.String("p2")
		}
		return out, nil
	}
}

func TestScanQAppsCategories_Fields(t *testing.T) {
	st := newTestStore(t)
	var calls parentCalls
	client := newQAppsTestClient(map[string]func(any) (any, error){
		"ListCategories": qappsCategoriesHandler(&calls, map[string][]qappstypes.Category{
			"inst-1": {qappsCategory("cat-1", "HR", 10), {Title: sdkaws.String("no id")}},
			"inst-2": {qappsCategory("cat-2", "IT", 0)},
		}, nil),
	})
	instances := qappsInstances(testRegion, testAccountID, []string{"inst-1", "inst-2"})

	total, _, err := scanQAppsCategories(context.Background(), client, newTestAccount(testAccountID), testRegion, st, testScanID, instances)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != 2 {
		t.Errorf("total = %d; want 2 (the id-less category skipped)", total)
	}
	if got := calls.distinct(); !reflect.DeepEqual(got, []string{"inst-1", "inst-2"}) {
		t.Errorf("instances queried = %v; want [inst-1 inst-2]", got)
	}
	rows := rowsByNativeID(t, st, TypeQAppsCategory)
	want := qappsTestInstanceARN("inst-1") + "/category/cat-1"
	r, ok := rows[want]
	if !ok || len(rows) != 2 {
		t.Fatalf("category rows = %v; want 2 including %s", rows, want)
	}
	if _, ok := rows[qappsTestInstanceARN("inst-2")+"/category/cat-2"]; !ok {
		t.Errorf("inst-2's category missing from %v", rows)
	}
	if r.Type != TypeQAppsCategory || sdkaws.ToString(r.Name) != "HR" || sdkaws.ToString(r.Region) != testRegion {
		t.Errorf("row = type %q name %q region %q; want %q HR %q", r.Type, sdkaws.ToString(r.Name), sdkaws.ToString(r.Region), TypeQAppsCategory, testRegion)
	}
	if !strings.Contains(r.AttributesJSON, `"AppCount":10`) {
		t.Errorf("attributes = %s; want AppCount 10", r.AttributesJSON)
	}
}

func TestScanQAppsCategories_Empty(t *testing.T) {
	st := newTestStore(t)
	var calls parentCalls
	client := newQAppsTestClient(map[string]func(any) (any, error){
		"ListCategories": qappsCategoriesHandler(&calls, nil, nil),
	})
	total, _, err := scanQAppsCategories(context.Background(), client, newTestAccount(testAccountID), testRegion, st, testScanID,
		qappsInstances(testRegion, testAccountID, []string{"inst-1"}))
	if err != nil || total != 0 {
		t.Errorf("total, err = %d, %v; want 0, nil", total, err)
	}
	if n := len(rowsByNativeID(t, st, TypeQAppsCategory)); n != 0 {
		t.Errorf("category rows = %d; want 0", n)
	}
}

func TestScanQAppsLibraryItems_PagesAndFields(t *testing.T) {
	st := newTestStore(t)
	var calls parentCalls
	client := newQAppsTestClient(map[string]func(any) (any, error){
		"ListLibraryItems": qappsLibraryHandler(&calls, map[string][][]qappstypes.LibraryItemMember{
			"inst-1": {{qappsLibraryItem("li-1"), {AppId: sdkaws.String("no id")}}, {ratedLibraryItem("li-2")}},
			"inst-2": {{qappsLibraryItem("li-3")}},
		}, nil),
	})
	instances := qappsInstances(testRegion, testAccountID, []string{"inst-1", "inst-2"})

	total, _, err := scanQAppsLibraryItems(context.Background(), client, newTestAccount(testAccountID), testRegion, st, testScanID, instances)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != 3 {
		t.Errorf("total = %d; want 3 (both pages of inst-1, the id-less item skipped, plus inst-2)", total)
	}
	if got := calls.distinct(); !reflect.DeepEqual(got, []string{"inst-1", "inst-2"}) {
		t.Errorf("instances queried = %v; want [inst-1 inst-2]", got)
	}
	rows := rowsByNativeID(t, st, TypeQAppsLibraryItem)
	if len(rows) != 3 {
		t.Fatalf("library item rows = %v; want 3", rows)
	}
	for _, nid := range []string{
		qappsTestInstanceARN("inst-1") + "/library-item/li-1",
		qappsTestInstanceARN("inst-1") + "/library-item/li-2",
		qappsTestInstanceARN("inst-2") + "/library-item/li-3",
	} {
		if _, ok := rows[nid]; !ok {
			t.Errorf("row %s missing", nid)
		}
	}
	r := rows[qappsTestInstanceARN("inst-1")+"/library-item/li-2"]
	if r.Type != TypeQAppsLibraryItem || sdkaws.ToString(r.Name) != "li-2" || sdkaws.ToString(r.Region) != testRegion ||
		sdkaws.ToString(r.Status) != "PUBLISHED" || sdkaws.ToString(r.CreatedAt) != "2024-05-21T23:17:27Z" {
		t.Errorf("row = type %q name %q region %q status %q created %q", r.Type, sdkaws.ToString(r.Name),
			sdkaws.ToString(r.Region), sdkaws.ToString(r.Status), sdkaws.ToString(r.CreatedAt))
	}
	var attrs map[string]any
	if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
		t.Fatalf("decode attributes: %v", err)
	}
	if attrs["RatingCount"] != float64(4) {
		t.Errorf("attributes = %s; want RatingCount 4", r.AttributesJSON)
	}
	if attrs["IsRatedByUser"] != nil {
		t.Errorf("attributes = %s; want IsRatedByUser cleared (caller-relative)", r.AttributesJSON)
	}
}

// ratedLibraryItem is a library item the caller has rated.
func ratedLibraryItem(id string) qappstypes.LibraryItemMember {
	li := qappsLibraryItem(id)
	li.IsRatedByUser = sdkaws.Bool(true)
	return li
}

func TestScanQAppsQApps_PagesAndFields(t *testing.T) {
	st := newTestStore(t)
	var calls parentCalls
	client := newQAppsTestClient(map[string]func(any) (any, error){
		"ListQApps": qappsQAppsHandler(&calls, map[string][][]qappstypes.UserAppItem{
			"inst-1": {{qappsQApp("inst-1", "app-1", "Mailer"), {AppId: sdkaws.String("no arn")}}, {qappsQApp("inst-1", "app-2", "")}},
			"inst-2": {{qappsQApp("inst-2", "app-3", "Notes")}},
		}, nil),
	})
	instances := qappsInstances(testRegion, testAccountID, []string{"inst-1", "inst-2"})

	total, _, err := scanQAppsQApps(context.Background(), client, newTestAccount(testAccountID), testRegion, st, testScanID, instances)
	if err != nil || total != 3 {
		t.Fatalf("total, err = %d, %v; want 3 (the ARN-less app skipped), nil", total, err)
	}
	if got := calls.distinct(); !reflect.DeepEqual(got, []string{"inst-1", "inst-2"}) {
		t.Errorf("instances queried = %v; want [inst-1 inst-2]", got)
	}
	rows := rowsByNativeID(t, st, TypeQAppsQApp)
	r, ok := rows[qappsTestInstanceARN("inst-1")+"/qapp/app-1"]
	if !ok || len(rows) != 3 {
		t.Fatalf("Q App rows = %v; want 3 including inst-1's app-1", rows)
	}
	if r.Type != TypeQAppsQApp || sdkaws.ToString(r.Name) != "Mailer" || sdkaws.ToString(r.Region) != testRegion {
		t.Errorf("row = type %q name %q region %q", r.Type, sdkaws.ToString(r.Name), sdkaws.ToString(r.Region))
	}
	if name := sdkaws.ToString(rows[qappsTestInstanceARN("inst-1")+"/qapp/app-2"].Name); name != "app-2" {
		t.Errorf("untitled app name = %q; want its AppId app-2", name)
	}
}

// A hard error on an instance's second page fails the phase but keeps the
// first page's rows.
func TestScanQAppsQApps_HardErrorKeepsEarlierPages(t *testing.T) {
	st := newTestStore(t)
	var calls parentCalls
	client := newQAppsTestClient(map[string]func(any) (any, error){
		"ListQApps": qappsQAppsHandler(&calls, map[string][][]qappstypes.UserAppItem{
			"inst-1": {{qappsQApp("inst-1", "app-1", "Mailer")}, {qappsQApp("inst-1", "app-2", "Notes")}},
		}, map[string]error{"inst-1/p2": apiErr("ConflictException", "bad")}),
	})
	total, _, err := scanQAppsQApps(context.Background(), client, newTestAccount(testAccountID), testRegion, st, testScanID,
		qappsInstances(testRegion, testAccountID, []string{"inst-1"}))
	if !isAPIErrorCode(err, "ConflictException") {
		t.Errorf("err = %v; want the ConflictException", err)
	}
	if total != 1 {
		t.Errorf("total = %d; want 1", total)
	}
	if _, ok := rowsByNativeID(t, st, TypeQAppsQApp)[qappsTestInstanceARN("inst-1")+"/qapp/app-1"]; !ok {
		t.Errorf("first page's row missing")
	}
}

func TestScanQAppsLibraryItems_Empty(t *testing.T) {
	st := newTestStore(t)
	var calls parentCalls
	client := newQAppsTestClient(map[string]func(any) (any, error){
		"ListLibraryItems": qappsLibraryHandler(&calls, nil, nil),
	})
	total, _, err := scanQAppsLibraryItems(context.Background(), client, newTestAccount(testAccountID), testRegion, st, testScanID,
		qappsInstances(testRegion, testAccountID, []string{"inst-1"}))
	if err != nil || total != 0 {
		t.Errorf("total, err = %d, %v; want 0, nil", total, err)
	}
	if n := len(rowsByNativeID(t, st, TypeQAppsLibraryItem)); n != 0 {
		t.Errorf("library item rows = %d; want 0", n)
	}
}

// qappsChildPhase is one per-instance child phase, driven through a client
// whose op answers each instance with one row or the error errs gives it.
type qappsChildPhase struct {
	name, op string
	typ      string
	handlers func(calls *parentCalls, errs map[string]error) map[string]func(any) (any, error)
	scan     func(context.Context, qappsAPI, *account, string, *store.Store, string, []childParent) (int, int, error)
}

var qappsChildPhases = []qappsChildPhase{
	{
		name: "q apps", op: qappsListQAppsOp, typ: TypeQAppsQApp,
		handlers: func(calls *parentCalls, errs map[string]error) map[string]func(any) (any, error) {
			return map[string]func(any) (any, error){"ListQApps": qappsQAppsHandler(calls, map[string][][]qappstypes.UserAppItem{
				"inst-1": {{qappsQApp("inst-1", "app-1", "A")}}, "inst-2": {{qappsQApp("inst-2", "app-2", "B")}}, "inst-3": {{qappsQApp("inst-3", "app-3", "C")}},
			}, errs)}
		},
		scan: scanQAppsQApps,
	},
	{
		name: "categories", op: qappsListCategoriesOp, typ: TypeQAppsCategory,
		handlers: func(calls *parentCalls, errs map[string]error) map[string]func(any) (any, error) {
			return map[string]func(any) (any, error){"ListCategories": qappsCategoriesHandler(calls, map[string][]qappstypes.Category{
				"inst-1": {qappsCategory("cat-1", "HR", 1)}, "inst-2": {qappsCategory("cat-2", "IT", 1)}, "inst-3": {qappsCategory("cat-3", "Ops", 1)},
			}, errs)}
		},
		scan: scanQAppsCategories,
	},
	{
		name: "library items", op: qappsListLibraryItemsOp, typ: TypeQAppsLibraryItem,
		handlers: func(calls *parentCalls, errs map[string]error) map[string]func(any) (any, error) {
			return map[string]func(any) (any, error){"ListLibraryItems": qappsLibraryHandler(calls, map[string][][]qappstypes.LibraryItemMember{
				"inst-1": {{qappsLibraryItem("li-1")}}, "inst-2": {{qappsLibraryItem("li-2")}}, "inst-3": {{qappsLibraryItem("li-3")}},
			}, errs)}
		},
		scan: scanQAppsLibraryItems,
	},
}

// Each instance's error is handled on its own: inst-1 always succeeds, inst-2
// and inst-3 fail with the case's error. A skip keeps inst-1's row and warns
// as many times as the case says (once for both failing instances); any other
// error fails the phase.
func TestScanQAppsChildPhases_PerInstanceErrors(t *testing.T) {
	cases := []struct {
		name      string
		err       error
		wantWarns int
		wantErr   bool
	}{
		{"gone is silent", apiErr("ResourceNotFoundException", "gone"), 0, false},
		{"denial warns once", qappsDenied, 1, false},
		{"unauthorized warns once", apiErr("UnauthorizedException", "not authenticated"), 1, false},
		{"validation warns once", apiErr("ValidationException", "not active"), 1, false},
		{"transient warns once", apiErr("ThrottlingException", "Rate exceeded"), 1, false},
		{"other fails", apiErr("ConflictException", "bad"), 0, true},
	}
	for _, ph := range qappsChildPhases {
		for _, tc := range cases {
			t.Run(ph.name+"/"+tc.name, func(t *testing.T) {
				st := newTestStore(t)
				warns := countOpWarnings(st, ph.op)
				var calls parentCalls
				client := newQAppsTestClient(ph.handlers(&calls, map[string]error{"inst-2": tc.err, "inst-3": tc.err}))
				instances := qappsInstances(testRegion, testAccountID, []string{"inst-1", "inst-2", "inst-3"})

				_, _, err := ph.scan(context.Background(), client, newTestAccount(testAccountID), testRegion, st, testScanID, instances)
				if tc.wantErr {
					if !isAPIErrorCode(err, "ConflictException") {
						t.Errorf("err = %v; want the ConflictException", err)
					}
					return
				}
				if err != nil {
					t.Fatalf("err = %v; want nil", err)
				}
				if *warns != tc.wantWarns {
					t.Errorf("warnings = %d; want %d", *warns, tc.wantWarns)
				}
				if n := len(rowsByNativeID(t, st, ph.typ)); n != 1 {
					t.Errorf("rows = %d; want 1 (inst-1's)", n)
				}
			})
		}
	}
}

// A skip on a second page keeps the first page's rows.
func TestScanQAppsLibraryItems_SkipKeepsEarlierPages(t *testing.T) {
	st := newTestStore(t)
	var calls parentCalls
	client := newQAppsTestClient(map[string]func(any) (any, error){
		"ListLibraryItems": qappsLibraryHandler(&calls, map[string][][]qappstypes.LibraryItemMember{
			"inst-1": {{qappsLibraryItem("li-1")}, {qappsLibraryItem("li-2")}},
		}, map[string]error{"inst-1/p2": apiErr("ResourceNotFoundException", "gone")}),
	})
	total, _, err := scanQAppsLibraryItems(context.Background(), client, newTestAccount(testAccountID), testRegion, st, testScanID,
		qappsInstances(testRegion, testAccountID, []string{"inst-1"}))
	if err != nil || total != 1 {
		t.Fatalf("total, err = %d, %v; want 1, nil", total, err)
	}
	if _, ok := rowsByNativeID(t, st, TypeQAppsLibraryItem)[qappsTestInstanceARN("inst-1")+"/library-item/li-1"]; !ok {
		t.Errorf("first page's row missing")
	}
}

// A NextToken that repeats the request's token ends the listing.
func TestScanQAppsPaginatedPhases_RepeatedTokenStops(t *testing.T) {
	cases := []struct {
		op   string
		page func() any
		scan func(context.Context, qappsAPI, *account, string, *store.Store, string, []childParent) (int, int, error)
	}{
		{"ListQApps", func() any {
			return &qapps.ListQAppsOutput{Apps: []qappstypes.UserAppItem{qappsQApp("inst-1", "app-1", "A")}, NextToken: sdkaws.String("same")}
		}, scanQAppsQApps},
		{"ListLibraryItems", func() any {
			return &qapps.ListLibraryItemsOutput{LibraryItems: []qappstypes.LibraryItemMember{qappsLibraryItem("li-1")}, NextToken: sdkaws.String("same")}
		}, scanQAppsLibraryItems},
	}
	for _, tc := range cases {
		t.Run(tc.op, func(t *testing.T) {
			st := newTestStore(t)
			var n atomic.Int32
			client := newQAppsTestClient(map[string]func(any) (any, error){
				tc.op: func(any) (any, error) {
					if n.Add(1) > 3 {
						return nil, errors.New("paginator did not stop on a repeated token")
					}
					return tc.page(), nil
				},
			})
			_, _, err := tc.scan(context.Background(), client, newTestAccount(testAccountID), testRegion, st, testScanID,
				qappsInstances(testRegion, testAccountID, []string{"inst-1"}))
			if err != nil {
				t.Errorf("err = %v; want nil", err)
			}
			if got := n.Load(); got != 2 {
				t.Errorf("calls = %d; want 2", got)
			}
		})
	}
}

// qappsTopLevelHandlers answers every op scanQApps calls for two
// applications, each with one Q App, one category and one library item.
func qappsTopLevelHandlers(calls *parentCalls) map[string]func(any) (any, error) {
	return map[string]func(any) (any, error){
		"ListApplications": func(any) (any, error) {
			return &qbusiness.ListApplicationsOutput{Applications: []qbtypes.Application{
				{ApplicationId: sdkaws.String("inst-1")}, {ApplicationId: sdkaws.String("inst-2")},
			}}, nil
		},
		"ListQApps": qappsQAppsHandler(calls, map[string][][]qappstypes.UserAppItem{
			"inst-1": {{qappsQApp("inst-1", "app-1", "A")}}, "inst-2": {{qappsQApp("inst-2", "app-2", "B")}},
		}, nil),
		"ListCategories": qappsCategoriesHandler(calls, map[string][]qappstypes.Category{
			"inst-1": {qappsCategory("cat-1", "HR", 1)}, "inst-2": {qappsCategory("cat-2", "IT", 1)},
		}, nil),
		"ListLibraryItems": qappsLibraryHandler(calls, map[string][][]qappstypes.LibraryItemMember{
			"inst-1": {{qappsLibraryItem("li-1")}}, "inst-2": {{qappsLibraryItem("li-2")}},
		}, nil),
	}
}

func TestScanQApps_ChildrenOfListedApplications(t *testing.T) {
	st := newTestStore(t)
	var calls parentCalls
	acct := &account{ID: testAccountID, Name: "Test Account", cfg: opStubConfig(qappsTopLevelHandlers(&calls), qappsDenied)}

	total, _, err := scanQApps(context.Background(), acct, testRegion, st, testScanID)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != 6 {
		t.Errorf("total = %d; want 6 (2 Q Apps + 2 categories + 2 library items)", total)
	}
	if got := calls.distinct(); !reflect.DeepEqual(got, []string{"inst-1", "inst-2"}) {
		t.Errorf("instances queried = %v; want [inst-1 inst-2]", got)
	}
	for _, id := range []string{"inst-1", "inst-2"} {
		if _, ok := rowsByNativeID(t, st, TypeQAppsCategory)[qappsTestInstanceARN(id)+"/category/cat-"+id[len("inst-"):]]; !ok {
			t.Errorf("%s's category missing", id)
		}
		if _, ok := rowsByNativeID(t, st, TypeQAppsLibraryItem)[qappsTestInstanceARN(id)+"/library-item/li-"+id[len("inst-"):]]; !ok {
			t.Errorf("%s's library item missing", id)
		}
	}
}

// A hard error in any phase leaves the other phases' rows and is returned.
func TestScanQApps_PhaseErrorsAreIndependent(t *testing.T) {
	for _, op := range []string{"ListQApps", "ListCategories", "ListLibraryItems"} {
		t.Run(op, func(t *testing.T) {
			st := newTestStore(t)
			var calls parentCalls
			h := qappsTopLevelHandlers(&calls)
			h[op] = func(any) (any, error) { return nil, apiErr("ConflictException", "bad") }
			acct := &account{ID: testAccountID, Name: "Test Account", cfg: opStubConfig(h, qappsDenied)}

			total, _, err := scanQApps(context.Background(), acct, testRegion, st, testScanID)
			if !isAPIErrorCode(err, "ConflictException") {
				t.Errorf("err = %v; want the %s ConflictException", err, op)
			}
			if total != 4 {
				t.Errorf("total = %d; want 4 (the two other phases' rows)", total)
			}
			for _, typ := range []string{TypeQAppsQApp, TypeQAppsCategory, TypeQAppsLibraryItem} {
				want := 2
				if (op == "ListQApps" && typ == TypeQAppsQApp) || (op == "ListCategories" && typ == TypeQAppsCategory) ||
					(op == "ListLibraryItems" && typ == TypeQAppsLibraryItem) {
					want = 0
				}
				if n := len(rowsByNativeID(t, st, typ)); n != want {
					t.Errorf("%s rows = %d; want %d", typ, n, want)
				}
			}
		})
	}
}

// A Q Apps instance still throttled after the SDK's retries is skipped with
// one warning, so a later phase's hard error still reaches the dispatcher as
// a hard error.
func TestScanQApps_TransientDoesNotMaskLaterError(t *testing.T) {
	st := newTestStore(t)
	warns := countOpWarnings(st, qappsListQAppsOp)
	var calls parentCalls
	h := qappsTopLevelHandlers(&calls)
	h["ListQApps"] = func(any) (any, error) { return nil, apiErr("ThrottlingException", "slow down") }
	h["ListLibraryItems"] = func(any) (any, error) { return nil, apiErr("ConflictException", "bad") }
	acct := &account{ID: testAccountID, Name: "Test Account", cfg: opStubConfig(h, qappsDenied)}

	_, _, err := scanQApps(context.Background(), acct, testRegion, st, testScanID)
	if !isAPIErrorCode(err, "ConflictException") {
		t.Errorf("err = %v; want the ListLibraryItems ConflictException", err)
	}
	if got := classifyServiceError(err); got != outcomeError {
		t.Errorf("classifyServiceError = %v; want outcomeError", got)
	}
	if *warns != 1 {
		t.Errorf("warnings = %d; want 1 for the throttled instances", *warns)
	}
	if n := len(rowsByNativeID(t, st, TypeQAppsCategory)); n != 2 {
		t.Errorf("category rows = %d; want 2", n)
	}
}

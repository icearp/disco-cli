package aws

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"reflect"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/quicksight"
	qstypes "github.com/aws/aws-sdk-go-v2/service/quicksight/types"
	smithyhttp "github.com/aws/smithy-go/transport/http"
	"github.com/icearp/disco-cli/internal/util"
	"github.com/icearp/disco-cli/store"
)

// TestQSPages pins QuickSight's first-page skip: an op the region does not
// serve (an HTML 404 the SDK cannot decode, the ListAgents region gap) and
// the op's own skip codes end the listing silently on the first page only;
// a denial, a decoded ResourceNotFoundException the op does not list, and
// any code on a later page reach the caller.
func TestQSPages(t *testing.T) {
	resp404 := &smithyhttp.ResponseError{
		Response: &smithyhttp.Response{Response: &http.Response{StatusCode: 404}},
		Err:      apiErr("UnknownError", "deserialization failed"),
	}
	typed404 := &smithyhttp.ResponseError{
		Response: &smithyhttp.Response{Response: &http.Response{StatusCode: 404}},
		Err:      apiErr("ResourceNotFoundException", "directory not found"),
	}
	cases := []struct {
		name     string
		token    *string
		err      error
		wantSkip bool
	}{
		{"undecodable 404 on the first page", nil, resp404, true},
		{"listed code on the first page", nil, apiErr(qsEditionGap, "x"), true},
		{"listed code on a later page", sdkaws.String("p2"), apiErr(qsEditionGap, "x"), false},
		{"undecodable 404 on a later page", sdkaws.String("p2"), resp404, false},
		{"decoded not-found the op does not list", nil, typed404, false},
		{"denial on the first page", nil, apiErr("AccessDeniedException", "denied"), false},
		{"code the op does not list", nil, apiErr(qsFeatureOff, "x"), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			page := qsPages([]string{qsEditionGap}, func(context.Context, *string) ([]int, *string, error) {
				return []int{1}, nil, tc.err
			})
			items, _, err := page(context.Background(), tc.token)
			if tc.wantSkip != (err == nil) {
				t.Fatalf("err = %v; want skipped %v", err, tc.wantSkip)
			}
			if tc.wantSkip && items != nil {
				t.Errorf("items = %v; want none from a skipped listing", items)
			}
		})
	}
}

// fakeQSChildren serves the QuickSight child list ops from per-parent pages
// keyed by NextToken ("" is the first page); a parent listed in errs answers
// with that error instead, and an alias request not asking for 100 items
// with ValidationException.
type fakeQSChildren struct {
	quickSightAPI
	templateAliases map[string]map[string]*quicksight.ListTemplateAliasesOutput
	themeAliases    map[string]map[string]*quicksight.ListThemeAliasesOutput
	topicSchedules  map[string]*quicksight.ListTopicRefreshSchedulesOutput
	errs            map[string]error
}

func (f fakeQSChildren) ListTemplateAliases(_ context.Context, in *quicksight.ListTemplateAliasesInput, _ ...func(*quicksight.Options)) (*quicksight.ListTemplateAliasesOutput, error) {
	if sdkaws.ToInt32(in.MaxResults) != 100 {
		return nil, apiErr("ValidationException", "MaxResults is not the model's cap, 100")
	}
	if err := f.errs[*in.TemplateId]; err != nil {
		return nil, err
	}
	return f.templateAliases[*in.TemplateId][sv(in.NextToken)], nil
}

func (f fakeQSChildren) ListThemeAliases(_ context.Context, in *quicksight.ListThemeAliasesInput, _ ...func(*quicksight.Options)) (*quicksight.ListThemeAliasesOutput, error) {
	if sdkaws.ToInt32(in.MaxResults) != 100 {
		return nil, apiErr("ValidationException", "MaxResults is not the model's cap, 100")
	}
	if err := f.errs[*in.ThemeId]; err != nil {
		return nil, err
	}
	return f.themeAliases[*in.ThemeId][sv(in.NextToken)], nil
}

func (f fakeQSChildren) ListTopicRefreshSchedules(_ context.Context, in *quicksight.ListTopicRefreshSchedulesInput, _ ...func(*quicksight.Options)) (*quicksight.ListTopicRefreshSchedulesOutput, error) {
	if err := f.errs[*in.TopicId]; err != nil {
		return nil, err
	}
	return f.topicSchedules[*in.TopicId], nil
}

func qsTestARN(kind, id string) string {
	return fmt.Sprintf("arn:aws:quicksight:%s:%s:%s/%s", testRegion, testAccountID, kind, id)
}

// seedQSParents stores one parentType row per id (the child phase's parents are
// rows an earlier phase of the same scan stored) and returns them as childParents.
func seedQSParents(t *testing.T, st *store.Store, parentType, kind string, ids ...string) []childParent {
	t.Helper()
	parents := make([]childParent, len(ids))
	for i, id := range ids {
		arn := qsTestARN(kind, id)
		upsertTestResource(t, st, "aws", testAccountID, parentType, arn, testRegion, "{}")
		parents[i] = childParent{id: id, arn: arn}
	}
	return parents
}

func qsRows(t *testing.T, st *store.Store, rtype string) map[string]store.Resource {
	t.Helper()
	rows, err := st.ListResources(store.ResourceFilter{Types: []string{rtype}, Limit: util.AllResources})
	if err != nil {
		t.Fatalf("ListResources(%s): %v", rtype, err)
	}
	byNativeID := make(map[string]store.Resource, len(rows))
	for _, r := range rows {
		byNativeID[r.NativeID] = r
	}
	return byNativeID
}

// assertQSContains fails unless the parent row contains exactly the children
// whose NativeIDs are given.
func assertQSContains(t *testing.T, st *store.Store, parentARN string, childNativeIDs ...string) {
	t.Helper()
	parentID := store.ResourceID("aws", testAccountID, parentARN)
	rels, err := st.RelationshipsFrom(parentID, store.RelContains)
	if err != nil {
		t.Fatalf("RelationshipsFrom(%s): %v", parentARN, err)
	}
	if len(rels) != len(childNativeIDs) {
		t.Errorf("%s contains %d children; want %d", parentARN, len(rels), len(childNativeIDs))
	}
	for _, c := range childNativeIDs {
		assertRelationship(t, rels, parentID, store.ResourceID("aws", testAccountID, c), store.RelContains)
	}
}

func templateAlias(templateID, name string, version int64) qstypes.TemplateAlias {
	return qstypes.TemplateAlias{
		AliasName:             sdkaws.String(name),
		Arn:                   sdkaws.String(qsTestARN("template", templateID) + "/alias/" + name),
		TemplateVersionNumber: sdkaws.Int64(version),
	}
}

func TestScanQSTemplateAliases_PaginatesEveryTemplateAndWiresHierarchy(t *testing.T) {
	st := newTestStore(t)
	parents := seedQSParents(t, st, TypeQuickSightTemplate, "template", "t1", "t2")
	fake := fakeQSChildren{templateAliases: map[string]map[string]*quicksight.ListTemplateAliasesOutput{
		"t1": {
			"":   {TemplateAliasList: []qstypes.TemplateAlias{templateAlias("t1", "prod", 3)}, NextToken: sdkaws.String("p2")},
			"p2": {TemplateAliasList: []qstypes.TemplateAlias{templateAlias("t1", "$LATEST", 4)}},
		},
		"t2": {"": {TemplateAliasList: []qstypes.TemplateAlias{
			templateAlias("t2", "prod", 1),
			{AliasName: sdkaws.String("no-arn")},
		}}},
	}}

	total, _, err := scanQSTemplateAliases(context.Background(), fake, newTestAccount(testAccountID), testRegion, st, testScanID, parents)
	if err != nil {
		t.Fatalf("scanQSTemplateAliases: %v", err)
	}
	if total != 3 {
		t.Errorf("total = %d; want 3 (the ARN-less alias is skipped)", total)
	}
	rows := qsRows(t, st, TypeQuickSightTemplateAlias)
	want := map[string]struct {
		name    string
		version int64
	}{
		qsTestARN("template", "t1") + "/alias/prod":    {"prod", 3},
		qsTestARN("template", "t1") + "/alias/$LATEST": {"$LATEST", 4},
		qsTestARN("template", "t2") + "/alias/prod":    {"prod", 1},
	}
	if len(rows) != len(want) {
		t.Fatalf("stored %d template-alias rows; want %d", len(rows), len(want))
	}
	for nativeID, w := range want {
		r, ok := rows[nativeID]
		if !ok {
			t.Errorf("missing template-alias row %q", nativeID)
			continue
		}
		if sv(r.Name) != w.name || sv(r.Region) != testRegion {
			t.Errorf("row %q: name=%q region=%q; want %q %q", nativeID, sv(r.Name), sv(r.Region), w.name, testRegion)
		}
		var attrs qstypes.TemplateAlias
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil || attrs.TemplateVersionNumber == nil || *attrs.TemplateVersionNumber != w.version {
			t.Errorf("row %q: attributes TemplateVersionNumber = %v (err %v); want %d", nativeID, attrs.TemplateVersionNumber, err, w.version)
		}
	}
	assertQSContains(t, st, parents[0].arn, qsTestARN("template", "t1")+"/alias/prod", qsTestARN("template", "t1")+"/alias/$LATEST")
	assertQSContains(t, st, parents[1].arn, qsTestARN("template", "t2")+"/alias/prod")
}

// qsChildOps drives each QuickSight child phase over parents, where every
// parent not in errs lists exactly one child.
var qsChildOps = []struct {
	name, parentType, kind, childType, op string
	run                                   func(st *store.Store, parents []childParent, errs map[string]error) error
}{
	{
		"template aliases", TypeQuickSightTemplate, "template", TypeQuickSightTemplateAlias, "quicksight:ListTemplateAliases",
		func(st *store.Store, parents []childParent, errs map[string]error) error {
			fake := fakeQSChildren{templateAliases: map[string]map[string]*quicksight.ListTemplateAliasesOutput{}, errs: errs}
			for _, p := range parents {
				fake.templateAliases[p.id] = map[string]*quicksight.ListTemplateAliasesOutput{"": {TemplateAliasList: []qstypes.TemplateAlias{templateAlias(p.id, "a", 1)}}}
			}
			_, _, err := scanQSTemplateAliases(context.Background(), fake, newTestAccount(testAccountID), testRegion, st, testScanID, parents)
			return err
		},
	},
	{
		"theme aliases", TypeQuickSightTheme, "theme", TypeQuickSightThemeAlias, "quicksight:ListThemeAliases",
		func(st *store.Store, parents []childParent, errs map[string]error) error {
			fake := fakeQSChildren{themeAliases: map[string]map[string]*quicksight.ListThemeAliasesOutput{}, errs: errs}
			for _, p := range parents {
				fake.themeAliases[p.id] = map[string]*quicksight.ListThemeAliasesOutput{"": {ThemeAliasList: []qstypes.ThemeAlias{themeAlias(p.id, "a")}}}
			}
			_, _, err := scanQSThemeAliases(context.Background(), fake, newTestAccount(testAccountID), testRegion, st, testScanID, parents)
			return err
		},
	},
	{
		"topic refresh schedules", TypeQuickSightTopic, "topic", TypeQuickSightTopicRefreshSchedule, "quicksight:ListTopicRefreshSchedules",
		func(st *store.Store, parents []childParent, errs map[string]error) error {
			fake := fakeQSChildren{topicSchedules: map[string]*quicksight.ListTopicRefreshSchedulesOutput{}, errs: errs}
			for _, p := range parents {
				fake.topicSchedules[p.id] = &quicksight.ListTopicRefreshSchedulesOutput{RefreshSchedules: []qstypes.TopicRefreshScheduleSummary{topicSchedule("ds-"+p.id, "")}}
			}
			_, _, err := scanQSTopicRefreshSchedules(context.Background(), fake, newTestAccount(testAccountID), testRegion, st, testScanID, parents)
			return err
		},
	},
}

// TestScanQSChildren_ErrorHandling runs the per-parent error ladder through
// every QuickSight child phase: three parents, one answering err, the other
// two listing one child each, which are stored whatever the failing one
// answers.
func TestScanQSChildren_ErrorHandling(t *testing.T) {
	cases := []struct {
		name      string
		err       error
		wantCode  string
		wantRows  int
		wantWarns int
	}{
		{"parent deleted since listing is skipped", apiErr("ResourceNotFoundException", "gone"), "", 2, 0},
		{"access denied skips the parent with a warning", apiErr("AccessDeniedException", "User: x is not authorized to perform: quicksight:List"), "", 2, 1},
		{"edition gap propagates", apiErr("UnsupportedUserEditionException", "standard edition"), "UnsupportedUserEditionException", 2, 0},
		{"invalid parameter propagates", apiErr("InvalidParameterValueException", "x"), "InvalidParameterValueException", 2, 0},
		{"other error propagates", apiErr("InternalFailureException", "boom"), "InternalFailureException", 2, 0},
	}
	for _, op := range qsChildOps {
		for _, tc := range cases {
			t.Run(op.name+"/"+tc.name, func(t *testing.T) {
				st := newTestStore(t)
				var warned []string
				st.OnWarn = func(w store.ScanWarning) { warned = append(warned, w.Service) }
				parents := seedQSParents(t, st, op.parentType, op.kind, "bad", "ok1", "ok2")
				err := op.run(st, parents, map[string]error{"bad": tc.err})
				if tc.wantCode != "" {
					if !isAPIErrorCode(err, tc.wantCode) {
						t.Fatalf("err = %v; want the %s propagated", err, tc.wantCode)
					}
				} else if err != nil {
					t.Fatalf("err = %v; want nil", err)
				}
				if got := len(qsRows(t, st, op.childType)); got != tc.wantRows {
					t.Errorf("stored %d rows; want %d", got, tc.wantRows)
				}
				if len(warned) != tc.wantWarns {
					t.Errorf("recorded %d warnings; want %d", len(warned), tc.wantWarns)
				}
				for _, svc := range warned {
					if svc != op.op {
						t.Errorf("warning names %q; want %q", svc, op.op)
					}
				}
			})
		}
	}
}

func TestScanQSChildren_AccessDeniedWarnsOncePerOp(t *testing.T) {
	st := newTestStore(t)
	warns := countWarnings(st)
	parents := seedQSParents(t, st, TypeQuickSightTemplate, "template", "d1", "d2", "d3", "ok")
	deny := apiErr("AccessDeniedException", "User: x is not authorized to perform: quicksight:ListTemplateAliases")
	fake := fakeQSChildren{
		templateAliases: map[string]map[string]*quicksight.ListTemplateAliasesOutput{
			"ok": {"": {TemplateAliasList: []qstypes.TemplateAlias{templateAlias("ok", "a", 1)}}},
		},
		errs: map[string]error{"d1": deny, "d2": deny, "d3": deny},
	}
	if _, _, err := scanQSTemplateAliases(context.Background(), fake, newTestAccount(testAccountID), testRegion, st, testScanID, parents); err != nil {
		t.Fatalf("scanQSTemplateAliases: %v", err)
	}
	if *warns != 1 {
		t.Errorf("recorded %d warnings for three denied templates; want 1", *warns)
	}
	if _, ok := qsRows(t, st, TypeQuickSightTemplateAlias)[qsTestARN("template", "ok")+"/alias/a"]; !ok {
		t.Error("the readable template's alias was not stored")
	}
}

func themeAlias(themeID, name string) qstypes.ThemeAlias {
	return qstypes.ThemeAlias{
		AliasName:          sdkaws.String(name),
		Arn:                sdkaws.String(qsTestARN("theme", themeID) + "/alias/" + name),
		ThemeVersionNumber: sdkaws.Int64(2),
	}
}

func TestScanQSThemeAliases_PaginatesAndSkipsDeletedTheme(t *testing.T) {
	st := newTestStore(t)
	warns := countWarnings(st)
	parents := seedQSParents(t, st, TypeQuickSightTheme, "theme", "th1", "th2", "bad")
	fake := fakeQSChildren{
		themeAliases: map[string]map[string]*quicksight.ListThemeAliasesOutput{
			"th1": {
				"":   {ThemeAliasList: []qstypes.ThemeAlias{themeAlias("th1", "prod")}, NextToken: sdkaws.String("p2")},
				"p2": {ThemeAliasList: []qstypes.ThemeAlias{themeAlias("th1", "dev")}},
			},
			"th2": {"": {ThemeAliasList: []qstypes.ThemeAlias{themeAlias("th2", "prod"), {AliasName: sdkaws.String("no-arn")}}}},
		},
		errs: map[string]error{"bad": apiErr("ResourceNotFoundException", "x")},
	}

	total, _, err := scanQSThemeAliases(context.Background(), fake, newTestAccount(testAccountID), testRegion, st, testScanID, parents)
	if err != nil {
		t.Fatalf("scanQSThemeAliases: %v", err)
	}
	if total != 3 {
		t.Errorf("total = %d; want 3", total)
	}
	rows := qsRows(t, st, TypeQuickSightThemeAlias)
	for _, nativeID := range []string{
		qsTestARN("theme", "th1") + "/alias/prod",
		qsTestARN("theme", "th1") + "/alias/dev",
		qsTestARN("theme", "th2") + "/alias/prod",
	} {
		r, ok := rows[nativeID]
		if !ok {
			t.Errorf("missing theme-alias row %q", nativeID)
			continue
		}
		if r.Type != TypeQuickSightThemeAlias || sv(r.Region) != testRegion || sv(r.Name) == "" {
			t.Errorf("row %q: type=%q region=%q name=%q", nativeID, r.Type, sv(r.Region), sv(r.Name))
		}
	}
	if *warns != 1 {
		t.Errorf("recorded %d warnings; want 1, for the ARN-less alias (a deleted theme is a silent skip)", *warns)
	}
	assertQSContains(t, st, parents[0].arn, qsTestARN("theme", "th1")+"/alias/prod", qsTestARN("theme", "th1")+"/alias/dev")
}

// ListThemes also returns QuickSight's starter themes (owner account "aws"):
// they are stored as ManagedByProvider without a tag read, and only the
// other themes, another account's shared one included, are handed to the
// alias phase.
func TestScanQSThemes_StarterThemesManagedAndNotHandedOn(t *testing.T) {
	st := newTestStore(t)
	mine, starter := qsTestARN("theme", "mine"), "arn:aws:quicksight::aws:theme/CLASSIC"
	shared := "arn:aws:quicksight:" + testRegion + ":999999999999:theme/shared"
	stub := stubResponses(t, map[string][]stubCall{
		"ListThemes": {{Output: &quicksight.ListThemesOutput{ThemeSummaryList: []qstypes.ThemeSummary{
			{ThemeId: sdkaws.String("mine"), Arn: sdkaws.String(mine), Name: sdkaws.String("Mine")},
			{ThemeId: sdkaws.String("CLASSIC"), Arn: sdkaws.String(starter), Name: sdkaws.String("Classic")},
			{ThemeId: sdkaws.String("shared"), Arn: sdkaws.String(shared), Name: sdkaws.String("Shared")},
		}}}},
		// Two responses only: a tag read of the starter theme exhausts the queue.
		"ListTagsForResource": {
			{Output: &quicksight.ListTagsForResourceOutput{Tags: []qstypes.Tag{{Key: sdkaws.String("team"), Value: sdkaws.String("bi")}}}},
			{Output: &quicksight.ListTagsForResourceOutput{Tags: []qstypes.Tag{{Key: sdkaws.String("team"), Value: sdkaws.String("bi")}}}},
		},
	})
	client := quicksight.NewFromConfig(cfgWithStub(stub, testRegion))

	var p phaseTotals
	parents, stop := scanQSThemes(context.Background(), client, newTestAccount(testAccountID), testRegion, st, testScanID, &p)
	if _, _, err := p.result(st, newTestAccount(testAccountID), testRegion); err != nil || stop {
		t.Fatalf("scanQSThemes: err = %v, storeWriteFailed = %v", err, stop)
	}
	if p.total != 3 {
		t.Errorf("total = %d; want 3 (the starter theme is still stored)", p.total)
	}
	want := []childParent{{id: "mine", arn: mine}, {id: "shared", arn: shared}}
	if !slices.Equal(parents, want) {
		t.Errorf("parents = %v; want %v (starter themes have no account aliases)", parents, want)
	}
	rows := rowsByNativeID(t, st, TypeQuickSightTheme)
	if r := rows[starter]; !r.ManagedByProvider || r.TagsJSON != nil {
		t.Errorf("starter theme: ManagedByProvider = %v, TagsJSON = %v; want true, nil", r.ManagedByProvider, r.TagsJSON)
	}
	for _, arn := range []string{mine, shared} {
		if rows[arn].ManagedByProvider {
			t.Errorf("theme %s is ManagedByProvider; want a user resource (only owner aws is QuickSight's)", arn)
		}
		assertTagsMap(t, rows[arn].TagsJSON, map[string]string{"team": "bi"})
	}
}

func topicSchedule(datasetID, datasetName string) qstypes.TopicRefreshScheduleSummary {
	return qstypes.TopicRefreshScheduleSummary{
		DatasetId:       sdkaws.String(datasetID),
		DatasetName:     sdkaws.String(datasetName),
		DatasetArn:      sdkaws.String(qsTestARN("dataset", datasetID)),
		RefreshSchedule: &qstypes.TopicRefreshSchedule{IsEnabled: sdkaws.Bool(true), Timezone: sdkaws.String("UTC")},
	}
}

func TestScanQSTopicRefreshSchedules_StoresPerDatasetSchedules(t *testing.T) {
	st := newTestStore(t)
	parents := seedQSParents(t, st, TypeQuickSightTopic, "topic", "tp1", "tp2", "gone")
	fake := fakeQSChildren{
		topicSchedules: map[string]*quicksight.ListTopicRefreshSchedulesOutput{
			"tp1": {RefreshSchedules: []qstypes.TopicRefreshScheduleSummary{
				topicSchedule("ds1", "Sales"),
				topicSchedule("ds2", ""),
				{DatasetName: sdkaws.String("no-id")},
			}},
			"tp2": {},
		},
		errs: map[string]error{"gone": apiErr("ResourceNotFoundException", "x")},
	}

	total, _, err := scanQSTopicRefreshSchedules(context.Background(), fake, newTestAccount(testAccountID), testRegion, st, testScanID, parents)
	if err != nil {
		t.Fatalf("scanQSTopicRefreshSchedules: %v", err)
	}
	if total != 2 {
		t.Errorf("total = %d; want 2 (the dataset-less schedule is skipped)", total)
	}
	rows := qsRows(t, st, TypeQuickSightTopicRefreshSchedule)
	want := map[string]string{
		qsTestARN("topic", "tp1") + "/refresh-schedule/ds1": "Sales",
		qsTestARN("topic", "tp1") + "/refresh-schedule/ds2": "ds2",
	}
	if len(rows) != len(want) {
		t.Fatalf("stored %d schedule rows; want %d", len(rows), len(want))
	}
	for nativeID, name := range want {
		r, ok := rows[nativeID]
		if !ok {
			t.Errorf("missing schedule row %q", nativeID)
			continue
		}
		if sv(r.Name) != name || sv(r.Region) != testRegion {
			t.Errorf("row %q: name=%q region=%q; want %q %q", nativeID, sv(r.Name), sv(r.Region), name, testRegion)
		}
		var attrs qstypes.TopicRefreshScheduleSummary
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil || attrs.RefreshSchedule == nil || sv(attrs.RefreshSchedule.Timezone) != "UTC" {
			t.Errorf("row %q: attributes RefreshSchedule = %+v (err %v); want Timezone UTC", nativeID, attrs.RefreshSchedule, err)
		}
	}
	assertQSContains(t, st, parents[0].arn,
		qsTestARN("topic", "tp1")+"/refresh-schedule/ds1", qsTestARN("topic", "tp1")+"/refresh-schedule/ds2")
	assertQSContains(t, st, parents[1].arn)
}

func TestScanQSKeyRegistration(t *testing.T) {
	keyARN := "arn:aws:kms:us-east-1:123456789012:key/k1"
	registered := &quicksight.DescribeKeyRegistrationOutput{
		AwsAccountId:    sdkaws.String(testAccountID),
		KeyRegistration: []qstypes.RegisteredCustomerManagedKey{{KeyArn: sdkaws.String(keyARN), DefaultKey: true}},
		RequestId:       sdkaws.String("req-1"),
		Status:          200,
	}
	cases := []struct {
		name      string
		call      stubCall
		wantCode  string
		wantWarns int
		wantRow   bool
	}{
		{"registered key is stored", stubCall{Output: registered}, "", 0, true},
		{"no registered key stores nothing", stubCall{Output: &quicksight.DescribeKeyRegistrationOutput{AwsAccountId: sdkaws.String(testAccountID)}}, "", 0, false},
		{"AWS-owned Q data key alone stores nothing", stubCall{Output: &quicksight.DescribeKeyRegistrationOutput{
			QDataKey: &qstypes.QDataKey{QDataKeyType: qstypes.QDataKeyTypeAwsOwned},
		}}, "", 0, false},
		{"CMK Q data key alone is stored", stubCall{Output: &quicksight.DescribeKeyRegistrationOutput{
			QDataKey: &qstypes.QDataKey{QDataKeyType: qstypes.QDataKeyTypeCmk, QDataKeyArn: sdkaws.String(keyARN)},
		}}, "", 0, true},
		{"access denied is skipped with a warning", stubCall{Err: apiErr("AccessDeniedException", "not subscribed")}, "", 1, false},
		// The op's model lists no code for an account that cannot use it.
		{"invalid parameter propagates", stubCall{Err: apiErr("InvalidParameterValueException", "x")}, "InvalidParameterValueException", 0, false},
		{"not-found propagates", stubCall{Err: apiErr("ResourceNotFoundException", "directory not found")}, "ResourceNotFoundException", 0, false},
		{"op the region does not serve is a silent skip", stubCall{Err: apiErr("UnknownOperationException", "x")}, "", 0, false},
		{"other error propagates", stubCall{Err: apiErr("InternalFailureException", "boom")}, "InternalFailureException", 0, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := countWarnings(st)
			client := quicksight.NewFromConfig(cfgWithStub(stubResponses(t, map[string][]stubCall{"DescribeKeyRegistration": {tc.call}}), testRegion))

			_, _, err := scanQSKeyRegistration(context.Background(), client, newTestAccount(testAccountID), testRegion, st, testScanID)
			if (tc.wantCode == "") != (err == nil) || (tc.wantCode != "" && !isAPIErrorCode(err, tc.wantCode)) {
				t.Fatalf("err = %v; want code %q", err, tc.wantCode)
			}
			if *warns != tc.wantWarns {
				t.Errorf("recorded %d warnings; want %d", *warns, tc.wantWarns)
			}
			rows := qsRows(t, st, TypeQuickSightKeyRegistration)
			nativeID := "arn:aws:quicksight:" + testRegion + ":" + testAccountID + ":key-registration"
			r, ok := rows[nativeID]
			if ok != tc.wantRow || len(rows) != map[bool]int{true: 1, false: 0}[tc.wantRow] {
				t.Fatalf("rows = %v; want row %q present=%v", rows, nativeID, tc.wantRow)
			}
			if !ok {
				return
			}
			if r.ManagedByProvider || sv(r.Region) != testRegion {
				t.Errorf("managed=%v region=%q; want false %q", r.ManagedByProvider, sv(r.Region), testRegion)
			}
			var attrs map[string]json.RawMessage
			if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
				t.Fatalf("attributes: %v", err)
			}
			if _, has := attrs["RequestId"]; has {
				t.Error("attributes carry the per-call RequestId; want it left out")
			}
			want := tc.call.Output.(*quicksight.DescribeKeyRegistrationOutput)
			var body quicksight.DescribeKeyRegistrationOutput
			if err := json.Unmarshal([]byte(r.AttributesJSON), &body); err != nil {
				t.Fatalf("attributes: %v", err)
			}
			if len(body.KeyRegistration) != len(want.KeyRegistration) || (body.QDataKey == nil) != (want.QDataKey == nil) {
				t.Errorf("attributes KeyRegistration=%+v QDataKey=%+v; want %+v %+v", body.KeyRegistration, body.QDataKey, want.KeyRegistration, want.QDataKey)
			}
			for i, k := range body.KeyRegistration {
				if i < len(want.KeyRegistration) && sv(k.KeyArn) != sv(want.KeyRegistration[i].KeyArn) {
					t.Errorf("attributes KeyRegistration[%d].KeyArn = %q; want %q", i, sv(k.KeyArn), sv(want.KeyRegistration[i].KeyArn))
				}
			}
		})
	}
}

// TestScanQuickSight_ChildPhasesUseListedParents pins the wiring: the template,
// theme and topic phases hand what they listed to the child phases, which run
// after the phases that list their parents.
func TestScanQuickSight_ChildPhasesUseListedParents(t *testing.T) {
	st := newTestStore(t)
	tplARN, themeARN, topicARN := qsTestARN("template", "t1"), qsTestARN("theme", "th1"), qsTestARN("topic", "tp1")
	stub := stubResponses(t, map[string][]stubCall{
		"ListDataSets":                 {{Output: &quicksight.ListDataSetsOutput{}}},
		"ListNamespaces":               {{Output: &quicksight.ListNamespacesOutput{}}},
		"ListAnalyses":                 {{Output: &quicksight.ListAnalysesOutput{}}},
		"ListDashboards":               {{Output: &quicksight.ListDashboardsOutput{}}},
		"ListDataSources":              {{Output: &quicksight.ListDataSourcesOutput{}}},
		"ListFolders":                  {{Output: &quicksight.ListFoldersOutput{}}},
		"ListVPCConnections":           {{Output: &quicksight.ListVPCConnectionsOutput{}}},
		"ListCustomPermissions":        {{Output: &quicksight.ListCustomPermissionsOutput{}}},
		"DescribeAccountSubscription":  {{Output: &quicksight.DescribeAccountSubscriptionOutput{AccountInfo: &qstypes.AccountInfo{}}}},
		"ListActionConnectors":         {{Output: &quicksight.ListActionConnectorsOutput{}}},
		"ListAgents":                   {{Output: &quicksight.ListAgentsOutput{}}},
		"ListBrands":                   {{Output: &quicksight.ListBrandsOutput{}}},
		"ListFlows":                    {{Output: &quicksight.ListFlowsOutput{}}},
		"ListKnowledgeBases":           {{Output: &quicksight.ListKnowledgeBasesOutput{}}},
		"ListOAuthClientApplications":  {{Output: &quicksight.ListOAuthClientApplicationsOutput{}}},
		"ListSpaces":                   {{Output: &quicksight.ListSpacesOutput{}}},
		"DescribeAccountSettings":      {{Output: &quicksight.DescribeAccountSettingsOutput{}}},
		"DescribeAccountCustomization": {{Output: &quicksight.DescribeAccountCustomizationOutput{}}},
		"ListApps":                     {{Output: &quicksight.ListAppsOutput{}}},
		"ListApprovalPolicies":         {{Output: &quicksight.ListApprovalPoliciesOutput{}}},
		"ListDlpSettings":              {{Output: &quicksight.ListDlpSettingsOutput{}}},
		"ListLimitsProfiles":           {{Output: &quicksight.ListLimitsProfilesOutput{}}},
		"DescribeKeyRegistration": {{Output: &quicksight.DescribeKeyRegistrationOutput{
			KeyRegistration: []qstypes.RegisteredCustomerManagedKey{{KeyArn: sdkaws.String("arn:aws:kms:us-east-1:123456789012:key/k1")}},
		}}},
		"ListTemplates": {{Output: &quicksight.ListTemplatesOutput{TemplateSummaryList: []qstypes.TemplateSummary{
			{TemplateId: sdkaws.String("t1"), Arn: sdkaws.String(tplARN)},
		}}}},
		"ListThemes": {{Output: &quicksight.ListThemesOutput{ThemeSummaryList: []qstypes.ThemeSummary{
			{ThemeId: sdkaws.String("th1"), Arn: sdkaws.String(themeARN)},
		}}}},
		"ListTopics": {{Output: &quicksight.ListTopicsOutput{TopicsSummaries: []qstypes.TopicSummary{
			{TopicId: sdkaws.String("tp1"), Arn: sdkaws.String(topicARN)},
		}}}},
		// The template, theme and topic tag reads.
		"ListTagsForResource": {{Output: &quicksight.ListTagsForResourceOutput{}}, {Output: &quicksight.ListTagsForResourceOutput{}}, {Output: &quicksight.ListTagsForResourceOutput{}}},
		"ListTemplateAliases": {{Output: &quicksight.ListTemplateAliasesOutput{TemplateAliasList: []qstypes.TemplateAlias{templateAlias("t1", "prod", 1)}}}},
		"ListThemeAliases":    {{Output: &quicksight.ListThemeAliasesOutput{ThemeAliasList: []qstypes.ThemeAlias{themeAlias("th1", "prod")}}}},
		"ListTopicRefreshSchedules": {{Output: &quicksight.ListTopicRefreshSchedulesOutput{
			RefreshSchedules: []qstypes.TopicRefreshScheduleSummary{topicSchedule("ds1", "Sales")},
		}}},
	})
	acct := &account{ID: testAccountID, Name: "Test Account", cfg: cfgWithStub(stub, testRegion)}

	total, _, err := scanQuickSight(context.Background(), acct, testRegion, st, testScanID)
	if err != nil {
		t.Fatalf("scanQuickSight: %v", err)
	}
	if total != 7 {
		t.Errorf("total = %d; want 7 (template, theme, topic, one child of each and the key registration)", total)
	}
	if got := len(qsRows(t, st, TypeQuickSightKeyRegistration)); got != 1 {
		t.Errorf("stored %d key registrations; want 1", got)
	}
	assertQSContains(t, st, tplARN, tplARN+"/alias/prod")
	assertQSContains(t, st, themeARN, themeARN+"/alias/prod")
	assertQSContains(t, st, topicARN, topicARN+"/refresh-schedule/ds1")
}

var qsListCreated = time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)

// qsListItemARN is the ARN of the list item with id, or nil for "" (an
// ARN-less item the phase must drop).
func qsListItemARN(kind, id string) *string {
	if id == "" {
		return nil
	}
	return sdkaws.String(qsTestARN(kind, id))
}

// qsListItemName is "name-{id}", or nil for ids starting "anon" (an unnamed
// item whose row falls back to its id).
func qsListItemName(id string) *string {
	if strings.HasPrefix(id, "anon") {
		return nil
	}
	return sdkaws.String("name-" + id)
}

// qsPhaseResult runs a phase that adds its counts and errors to a
// phaseTotals and returns them joined.
func qsPhaseResult(st *store.Store, run func(p *phaseTotals) bool) (int, int, error) {
	var p phaseTotals
	run(&p)
	return p.result(st, newTestAccount(testAccountID), testRegion)
}

// qsListCase is one account-level QuickSight list phase: run scans it, input
// reads a request's paging fields, page builds a response listing ids.
// pageCap is the op's MaxResults model cap, skipCodes the first-page codes
// it skips silently, and noCreatedAt says its rows carry no creation time.
type qsListCase struct {
	name, op, kind, rtype, arnKey, wantStatus string
	pageCap                                   int32
	skipCodes                                 []string
	noCreatedAt                               bool
	run                                       func(ctx context.Context, client quickSightAPI, st *store.Store) (int, int, error)
	input                                     func(in any) (token *string, maxResults *int32)
	page                                      func(next *string, ids ...string) any
}

var qsListCases = []qsListCase{
	{
		name: "apps", op: qsListAppsOp, kind: "app", rtype: TypeQuickSightApp, arnKey: "Arn",
		pageCap: 100,
		run: func(ctx context.Context, client quickSightAPI, st *store.Store) (int, int, error) {
			return qsPhaseResult(st, func(p *phaseTotals) bool {
				return scanQSApps(ctx, client, newTestAccount(testAccountID), testRegion, st, testScanID, p)
			})
		},
		input: func(in any) (*string, *int32) { i := in.(*quicksight.ListAppsInput); return i.NextToken, i.MaxResults },
		page: func(next *string, ids ...string) any {
			out := &quicksight.ListAppsOutput{NextToken: next}
			for _, id := range ids {
				out.AppSummaryList = append(out.AppSummaryList, qstypes.AppSummary{
					AppId: sdkaws.String(id), Arn: qsListItemARN("app", id), Name: qsListItemName(id),
					CreatedTime: &qsListCreated, Visibility: qstypes.AppVisibilityPrivate,
				})
			}
			return out
		},
	},
	{
		name: "approval policies", op: qsListApprovalPoliciesOp, kind: "approval-policy", rtype: TypeQuickSightApprovalPolicy, arnKey: "PolicyArn",
		pageCap: 100, skipCodes: []string{qsNoSubscription},
		run: func(ctx context.Context, client quickSightAPI, st *store.Store) (int, int, error) {
			return scanQSApprovalPolicies(ctx, client, newTestAccount(testAccountID), testRegion, st, testScanID)
		},
		input: func(in any) (*string, *int32) {
			i := in.(*quicksight.ListApprovalPoliciesInput)
			return i.NextToken, i.MaxResults
		},
		page: func(next *string, ids ...string) any {
			out := &quicksight.ListApprovalPoliciesOutput{NextToken: next}
			for _, id := range ids {
				out.Policies = append(out.Policies, qstypes.ApprovalPolicy{
					PolicyId: sdkaws.String(id), PolicyArn: qsListItemARN("approval-policy", id), Name: qsListItemName(id),
					CreatedAt: &qsListCreated, ApprovalGroups: []string{qsTestARN("group", "default/approvers")},
				})
			}
			return out
		},
	},
	{
		name: "DLP settings", op: qsListDlpSettingsOp, kind: "dlpsetting", rtype: TypeQuickSightDLPSetting, arnKey: "Arn", wantStatus: "ACTIVE",
		pageCap: 100, skipCodes: []string{qsFeatureOff},
		run: func(ctx context.Context, client quickSightAPI, st *store.Store) (int, int, error) {
			return qsPhaseResult(st, func(p *phaseTotals) bool {
				return scanQSDLPSettings(ctx, client, newTestAccount(testAccountID), testRegion, st, testScanID, p)
			})
		},
		input: func(in any) (*string, *int32) {
			i := in.(*quicksight.ListDlpSettingsInput)
			return i.NextToken, i.MaxResults
		},
		page: func(next *string, ids ...string) any {
			out := &quicksight.ListDlpSettingsOutput{NextToken: next}
			for _, id := range ids {
				out.DlpSettingSummaries = append(out.DlpSettingSummaries, qstypes.DlpSettingSummary{
					DlpSettingId: sdkaws.String(id), Arn: qsListItemARN("dlpsetting", id), Name: qsListItemName(id),
					CreatedAt: &qsListCreated, Status: qstypes.DlpSettingStatusActive, ProviderType: qstypes.DlpProviderTypeMicrosoftPurview,
				})
			}
			return out
		},
	},
	{
		name: "limits profiles", op: qsListLimitsProfilesOp, kind: "limits-profile", rtype: TypeQuickSightLimitsProfile, arnKey: "Arn",
		pageCap: 100,
		run: func(ctx context.Context, client quickSightAPI, st *store.Store) (int, int, error) {
			return scanQSLimitsProfiles(ctx, client, newTestAccount(testAccountID), testRegion, st, testScanID)
		},
		input: func(in any) (*string, *int32) {
			i := in.(*quicksight.ListLimitsProfilesInput)
			return i.NextToken, i.MaxResults
		},
		page: func(next *string, ids ...string) any {
			out := &quicksight.ListLimitsProfilesOutput{NextToken: next}
			for _, id := range ids {
				out.Profiles = append(out.Profiles, qstypes.LimitsProfile{
					ProfileId: sdkaws.String(id), Arn: qsListItemARN("limits-profile", id), ProfileName: qsListItemName(id),
					AccountId: sdkaws.String(testAccountID), CreatedAt: &qsListCreated,
				})
			}
			return out
		},
	},
	{
		name: "agents", op: "quicksight:ListAgents", kind: "agent", rtype: TypeQuickSightAgent, arnKey: "Arn",
		noCreatedAt: true, pageCap: 100, skipCodes: []string{qsEditionGap, qsPrecondition},
		run: func(ctx context.Context, client quickSightAPI, st *store.Store) (int, int, error) {
			return scanQSAgents(ctx, client, newTestAccount(testAccountID), testRegion, st, testScanID)
		},
		input: func(in any) (*string, *int32) {
			i := in.(*quicksight.ListAgentsInput)
			return i.NextToken, i.MaxResults
		},
		page: func(next *string, ids ...string) any {
			out := &quicksight.ListAgentsOutput{NextToken: next}
			for _, id := range ids {
				out.AgentSummaries = append(out.AgentSummaries, qstypes.AgentSummary{AgentId: sdkaws.String(id), Arn: qsListItemARN("agent", id), Name: qsListItemName(id)})
			}
			return out
		},
	},
	{
		name: "knowledge bases", op: "quicksight:ListKnowledgeBases", kind: "knowledge-base", rtype: TypeQuickSightKnowledgeBase, arnKey: "KnowledgeBaseArn",
		noCreatedAt: true, pageCap: 100, skipCodes: []string{qsFeatureOff, qsPrecondition},
		run: func(ctx context.Context, client quickSightAPI, st *store.Store) (int, int, error) {
			return scanQSKnowledgeBases(ctx, client, newTestAccount(testAccountID), testRegion, st, testScanID)
		},
		input: func(in any) (*string, *int32) {
			i := in.(*quicksight.ListKnowledgeBasesInput)
			return i.NextToken, i.MaxResults
		},
		page: func(next *string, ids ...string) any {
			out := &quicksight.ListKnowledgeBasesOutput{NextToken: next}
			for _, id := range ids {
				out.KnowledgeBaseSummaries = append(out.KnowledgeBaseSummaries, qstypes.KnowledgeBaseSummary{KnowledgeBaseId: sdkaws.String(id), KnowledgeBaseArn: qsListItemARN("knowledge-base", id), Name: qsListItemName(id)})
			}
			return out
		},
	},
	{
		name: "spaces", op: "quicksight:ListSpaces", kind: "space", rtype: TypeQuickSightSpace, arnKey: "SpaceArn",
		noCreatedAt: true, pageCap: 500, skipCodes: []string{qsNoSubscription},
		run: func(ctx context.Context, client quickSightAPI, st *store.Store) (int, int, error) {
			return scanQSSpaces(ctx, client, newTestAccount(testAccountID), testRegion, st, testScanID)
		},
		input: func(in any) (*string, *int32) {
			i := in.(*quicksight.ListSpacesInput)
			return i.NextToken, i.MaxResults
		},
		page: func(next *string, ids ...string) any {
			out := &quicksight.ListSpacesOutput{NextToken: next}
			for _, id := range ids {
				out.SpaceSummaries = append(out.SpaceSummaries, qstypes.SpaceSummary{SpaceId: sdkaws.String(id), SpaceArn: qsListItemARN("space", id), Name: qsListItemName(id)})
			}
			return out
		},
	},
}

// qsListPage is one stubbed response, keyed by the request's NextToken.
type qsListPage struct {
	ids  []string
	next *string
	err  error
}

// qsListCalls records the paging fields of every request a stub answered.
type qsListCalls struct {
	tokens     []string
	maxResults []int32
}

// qsListClient answers c's op from pages; any other op, a token with no page,
// or a request beyond one more than the pages fails with
// InternalFailureException, so a scan that never stops fails instead of hanging.
func qsListClient(t *testing.T, c qsListCase, pages map[string]qsListPage) (*quicksight.Client, *qsListCalls) {
	t.Helper()
	calls := &qsListCalls{}
	h := map[string]func(any) (any, error){
		"ListTagsForResource": func(any) (any, error) { return &quicksight.ListTagsForResourceOutput{}, nil },
		strings.TrimPrefix(c.op, "quicksight:"): func(in any) (any, error) {
			token, maxResults := c.input(in)
			calls.tokens = append(calls.tokens, sv(token))
			calls.maxResults = append(calls.maxResults, sdkaws.ToInt32(maxResults))
			if len(calls.tokens) > len(pages)+1 {
				return nil, apiErr("InternalFailureException", "more requests than pages: the scan does not stop")
			}
			p, ok := pages[sv(token)]
			if !ok {
				t.Errorf("%s: request with unexpected token %q", c.op, sv(token))
				return nil, apiErr("InternalFailureException", "no such page")
			}
			if p.err != nil {
				return nil, p.err
			}
			return c.page(p.next, p.ids...), nil
		},
	}
	return quicksight.NewFromConfig(opStubConfig(h, apiErr("InternalFailureException", "unexpected op"))), calls
}

func TestScanQSAccountLists_PaginatesAndStores(t *testing.T) {
	for _, c := range qsListCases {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := countOpWarnings(st, c.op)
			client, calls := qsListClient(t, c, map[string]qsListPage{
				"":   {ids: []string{"a1", "", "anon1"}, next: sdkaws.String("p2")},
				"p2": {ids: []string{"a2"}},
			})

			total, _, err := c.run(context.Background(), client, st)
			if err != nil {
				t.Fatalf("scan: %v", err)
			}
			if total != 3 {
				t.Errorf("total = %d; want 3 (the ARN-less item is dropped)", total)
			}
			if !slices.Equal(calls.tokens, []string{"", "p2"}) || !slices.Equal(calls.maxResults, []int32{c.pageCap, c.pageCap}) {
				t.Errorf("requests tokens=%q maxResults=%v; want [\"\" p2] [%d %d]", calls.tokens, calls.maxResults, c.pageCap, c.pageCap)
			}
			if *warns != 1 {
				t.Errorf("recorded %d warnings under %s; want 1 (the ARN-less item)", *warns, c.op)
			}
			rows := qsRows(t, st, c.rtype)
			want := map[string]string{"a1": "name-a1", "anon1": "anon1", "a2": "name-a2"}
			if len(rows) != len(want) {
				t.Fatalf("stored %d rows; want %d", len(rows), len(want))
			}
			for id, name := range want {
				arn := qsTestARN(c.kind, id)
				r, ok := rows[arn]
				if !ok {
					t.Errorf("missing row %q", arn)
					continue
				}
				wantCreated := "2026-01-02T03:04:05Z"
				if c.noCreatedAt {
					wantCreated = ""
				}
				if r.Type != c.rtype || sv(r.Name) != name || sv(r.Region) != testRegion || sv(r.CreatedAt) != wantCreated || sv(r.Status) != c.wantStatus {
					t.Errorf("row %q: type=%q name=%q region=%q created=%q status=%q; want %q %q %q %q %q",
						arn, r.Type, sv(r.Name), sv(r.Region), sv(r.CreatedAt), sv(r.Status), c.rtype, name, testRegion, wantCreated, c.wantStatus)
				}
				if r.ManagedByProvider {
					t.Errorf("row %q is ManagedByProvider; want a user resource", arn)
				}
				var attrs map[string]any
				if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
					t.Fatalf("row %q attributes: %v", arn, err)
				}
				if attrs[c.arnKey] != arn {
					t.Errorf("row %q: attributes %s = %v; want the ARN", arn, c.arnKey, attrs[c.arnKey])
				}
			}
		})
	}
}

func TestScanQSAccountLists_EmptyStoresNothing(t *testing.T) {
	for _, c := range qsListCases {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			client, _ := qsListClient(t, c, map[string]qsListPage{"": {}})
			total, _, err := c.run(context.Background(), client, st)
			if err != nil || total != 0 {
				t.Errorf("scan = (%d, %v); want (0, nil)", total, err)
			}
			if n := len(qsRows(t, st, c.rtype)); n != 0 {
				t.Errorf("stored %d rows; want 0", n)
			}
		})
	}
}

func TestScanQSAccountLists_StopsOnRepeatedToken(t *testing.T) {
	for _, c := range qsListCases {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			client, calls := qsListClient(t, c, map[string]qsListPage{
				"":   {ids: []string{"a1"}, next: sdkaws.String("p2")},
				"p2": {ids: []string{"a2"}, next: sdkaws.String("p2")},
			})
			total, _, err := c.run(context.Background(), client, st)
			if !errors.Is(err, errRepeatedNextToken) {
				t.Fatalf("err = %v; want errRepeatedNextToken (the listing stopped incomplete)", err)
			}
			if len(calls.tokens) != 2 || total != 2 {
				t.Errorf("made %d requests storing %d rows; want 2 and 2 (stop when the service repeats a token, keeping the pages read)", len(calls.tokens), total)
			}
		})
	}
}

// qsTestCodes are the codes the first-page and later-page error tests send
// every QuickSight listing: each code some op lists as its skip code, a
// denial, and codes no op skips.
var qsTestCodes = []string{
	"AccessDeniedException", qsNoSubscription, qsEditionGap, qsPrecondition, qsFeatureOff,
	"ResourceUnavailableException", "InvalidParameterValueException", "InternalFailureException",
}

// Each account-level list phase skips silently only the first-page codes
// its op lists (skipCodes) and an op the region does not serve; a denial is
// warned about; every other first-page code, and every code on a later page
// but a denial, is returned with the rows read before it.
func TestScanQSAccountLists_ErrorHandling(t *testing.T) {
	resp404 := &smithyhttp.ResponseError{
		Response: &smithyhttp.Response{Response: &http.Response{StatusCode: 404}},
		Err:      apiErr("UnknownError", "deserialization failed"),
	}
	firstPage := func(err error) map[string]qsListPage { return map[string]qsListPage{"": {err: err}} }
	secondPage := func(err error) map[string]qsListPage {
		return map[string]qsListPage{"": {ids: []string{"a1"}, next: sdkaws.String("p2")}, "p2": {err: err}}
	}
	type errCase struct {
		name      string
		pages     map[string]qsListPage
		wantCode  string
		wantRows  int
		wantWarns int
	}
	for _, c := range qsListCases {
		cases := []errCase{{"first-page HTML 404 is a silent skip", firstPage(resp404), "", 0, 0}}
		for _, code := range qsTestCodes {
			switch {
			case code == "AccessDeniedException":
				cases = append(cases,
					errCase{"first-page " + code + " warns", firstPage(apiErr(code, "x")), "", 0, 1},
					errCase{"later-page " + code + " warns and keeps the rows", secondPage(apiErr(code, "x")), "", 1, 1})
				continue
			case slices.Contains(c.skipCodes, code):
				cases = append(cases, errCase{"first-page " + code + " is a silent skip", firstPage(apiErr(code, "x")), "", 0, 0})
			default:
				cases = append(cases, errCase{"first-page " + code + " propagates", firstPage(apiErr(code, "x")), code, 0, 0})
			}
			cases = append(cases, errCase{"later-page " + code + " propagates and keeps the rows", secondPage(apiErr(code, "x")), code, 1, 0})
		}
		for _, tc := range cases {
			t.Run(c.name+"/"+tc.name, func(t *testing.T) {
				st := newTestStore(t)
				warns := countOpWarnings(st, c.op)
				client, _ := qsListClient(t, c, tc.pages)

				total, _, err := c.run(context.Background(), client, st)
				if tc.wantCode == "" && err != nil {
					t.Fatalf("err = %v; want nil", err)
				}
				if tc.wantCode != "" && !isAPIErrorCode(err, tc.wantCode) {
					t.Fatalf("err = %v; want %s", err, tc.wantCode)
				}
				if n := len(qsRows(t, st, c.rtype)); n != tc.wantRows || total != tc.wantRows {
					t.Errorf("stored %d rows (total %d); want %d", n, total, tc.wantRows)
				}
				if *warns != tc.wantWarns {
					t.Errorf("recorded %d warnings for %s; want %d", *warns, c.op, tc.wantWarns)
				}
			})
		}
	}
}

// The governance lists may be account-global, so a row whose ARN names another
// region is that region's to store. ARNs that cannot be placed in a region are
// kept rather than lost.
func TestScanQSGovernanceLists_DropOtherRegionRows(t *testing.T) {
	arns := func(kind string) (match, foreign, regionless, unparseable string) {
		return qsTestARN(kind, "here"),
			fmt.Sprintf("arn:aws:quicksight:eu-west-1:%s:%s/there", testAccountID, kind),
			fmt.Sprintf("arn:aws:quicksight::%s:%s/nowhere", testAccountID, kind),
			kind + "/not-an-arn"
	}
	cases := []struct {
		name, op, kind, rtype string
		run                   func(ctx context.Context, client quickSightAPI, st *store.Store) (int, int, error)
		output                func(arns ...string) any
	}{
		{
			"approval policies", "ListApprovalPolicies", "approval-policy", TypeQuickSightApprovalPolicy,
			func(ctx context.Context, client quickSightAPI, st *store.Store) (int, int, error) {
				return scanQSApprovalPolicies(ctx, client, newTestAccount(testAccountID), testRegion, st, testScanID)
			},
			func(arns ...string) any {
				out := &quicksight.ListApprovalPoliciesOutput{}
				for _, a := range arns {
					out.Policies = append(out.Policies, qstypes.ApprovalPolicy{PolicyArn: sdkaws.String(a), PolicyId: sdkaws.String("p")})
				}
				return out
			},
		},
		{
			"limits profiles", "ListLimitsProfiles", "limits-profile", TypeQuickSightLimitsProfile,
			func(ctx context.Context, client quickSightAPI, st *store.Store) (int, int, error) {
				return scanQSLimitsProfiles(ctx, client, newTestAccount(testAccountID), testRegion, st, testScanID)
			},
			func(arns ...string) any {
				out := &quicksight.ListLimitsProfilesOutput{}
				for _, a := range arns {
					out.Profiles = append(out.Profiles, qstypes.LimitsProfile{Arn: sdkaws.String(a), ProfileId: sdkaws.String("lp-1")})
				}
				return out
			},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			match, foreign, regionless, unparseable := arns(c.kind)
			h := map[string]func(any) (any, error){
				c.op: func(any) (any, error) { return c.output(match, foreign, regionless, unparseable), nil },
			}
			client := quicksight.NewFromConfig(opStubConfig(h, apiErr("InternalFailureException", "unexpected op")))

			total, _, err := c.run(context.Background(), client, st)
			if err != nil {
				t.Fatalf("scan: %v", err)
			}
			rows := qsRows(t, st, c.rtype)
			if total != 3 || len(rows) != 3 {
				t.Errorf("total = %d, stored %d rows; want 3 and 3 (only the eu-west-1 row dropped)", total, len(rows))
			}
			for _, arn := range []string{match, regionless, unparseable} {
				if _, ok := rows[arn]; !ok {
					t.Errorf("row %q dropped; want kept", arn)
				}
			}
			if _, ok := rows[foreign]; ok {
				t.Errorf("row %q from another region stored; want dropped", foreign)
			}
		})
	}
}

// qsTopLevelHandlers answers the subscription gate for a subscribed account,
// ListDataSets with one data set, its refresh schedules with none, and each
// account-level list op with one item; every other op is left to
// qsNotServed, which the other phases skip.
func qsTopLevelHandlers() map[string]func(any) (any, error) {
	h := map[string]func(any) (any, error){
		"DescribeAccountSubscription": qsSubscribed,
		"ListTagsForResource":         func(any) (any, error) { return &quicksight.ListTagsForResourceOutput{}, nil },
		"ListDataSets": func(any) (any, error) {
			return &quicksight.ListDataSetsOutput{DataSetSummaries: []qstypes.DataSetSummary{
				{DataSetId: sdkaws.String("ds1"), Arn: sdkaws.String(qsTestARN("dataset", "ds1"))},
			}}, nil
		},
		"ListRefreshSchedules": func(any) (any, error) { return &quicksight.ListRefreshSchedulesOutput{}, nil },
	}
	for _, c := range qsListCases {
		h[strings.TrimPrefix(c.op, "quicksight:")] = func(any) (any, error) { return c.page(nil, "x1"), nil }
	}
	return h
}

var qsTopLevelDeny = apiErr("AccessDeniedException", "not subscribed")

// qsNotServed answers the ops a test leaves unhandled as ones the region
// does not serve, which the account-level phases skip silently.
var qsNotServed = apiErr("UnknownOperationException", "not served in this region")

// TestScanQuickSight_AccountListPhasesIndependentOfOlderPhases pins the wiring:
// every account-level list phase runs, after the older phases, and a hard
// error on either side costs the other side no rows.
func TestScanQuickSight_AccountListPhasesIndependentOfOlderPhases(t *testing.T) {
	cases := []struct {
		name, failOp string
	}{
		{name: "all succeed"},
		{name: "older phase fails", failOp: "ListDataSets"},
	}
	for _, c := range qsListCases {
		cases = append(cases, struct{ name, failOp string }{c.name + " fails", strings.TrimPrefix(c.op, "quicksight:")})
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			h := qsTopLevelHandlers()
			if tc.failOp != "" {
				h[tc.failOp] = func(any) (any, error) { return nil, apiErr("InternalFailureException", "boom") }
			}
			acct := &account{ID: testAccountID, Name: "Test Account", cfg: opStubConfig(h, qsNotServed)}

			total, _, err := scanQuickSight(context.Background(), acct, testRegion, st, testScanID)
			if tc.failOp == "" && err != nil {
				t.Fatalf("scanQuickSight: %v", err)
			}
			if tc.failOp != "" && (!isAPIErrorCode(err, "InternalFailureException") || classifyServiceError(err) != outcomeError) {
				t.Fatalf("err = %v; want the %s InternalFailureException as a hard error", err, tc.failOp)
			}
			wantTotal := 0
			if _, has := qsRows(t, st, TypeQuickSightDataSet)[qsTestARN("dataset", "ds1")]; has != (tc.failOp != "ListDataSets") {
				t.Errorf("data set stored = %v; want %v", has, tc.failOp != "ListDataSets")
			} else if has {
				wantTotal++
			}
			for _, c := range qsListCases {
				want := strings.TrimPrefix(c.op, "quicksight:") != tc.failOp
				if _, has := qsRows(t, st, c.rtype)[qsTestARN(c.kind, "x1")]; has != want {
					t.Errorf("%s row stored = %v; want %v", c.name, has, want)
				}
				if want {
					wantTotal++
				}
			}
			if total != wantTotal {
				t.Errorf("total = %d; want %d", total, wantTotal)
			}
		})
	}
}

// A transient error on one side is demoted to a warning, so the other side's
// hard error still reaches the dispatcher as a hard error.
func TestScanQuickSight_TransientDoesNotMaskHardError(t *testing.T) {
	type side struct{ op, warnOp string }
	older := side{"ListDataSets", qsListDataSetsOp}
	var pairs [][2]side
	for _, c := range qsListCases {
		newer := side{strings.TrimPrefix(c.op, "quicksight:"), c.op}
		pairs = append(pairs, [2]side{older, newer}, [2]side{newer, older})
	}
	for _, p := range pairs {
		transient, hard := p[0], p[1]
		t.Run(transient.op+" throttled, "+hard.op+" fails", func(t *testing.T) {
			st := newTestStore(t)
			var warned []string
			st.OnWarn = func(w store.ScanWarning) { warned = append(warned, w.Service) }
			h := qsTopLevelHandlers()
			h[transient.op] = func(any) (any, error) { return nil, apiErr("ThrottlingException", "slow down") }
			h[hard.op] = func(any) (any, error) { return nil, apiErr("ValidationException", "bad") }
			acct := &account{ID: testAccountID, Name: "Test Account", cfg: opStubConfig(h, qsNotServed)}

			_, _, err := scanQuickSight(context.Background(), acct, testRegion, st, testScanID)
			if !isAPIErrorCode(err, "ValidationException") || classifyServiceError(err) != outcomeError {
				t.Errorf("err = %v (outcome %v); want the %s ValidationException as outcomeError", err, classifyServiceError(err), hard.op)
			}
			if !slices.Equal(warned, []string{transient.warnOp}) {
				t.Errorf("warnings = %q; want one for %s", warned, transient.warnOp)
			}
		})
	}
}

func qsFolderMember(kind, id string) qstypes.MemberIdArnPair {
	return qstypes.MemberIdArnPair{MemberId: sdkaws.String(id), MemberArn: sdkaws.String(qsTestARN(kind, id))}
}

func qsGroupMember(name string) qstypes.GroupMember {
	return qstypes.GroupMember{MemberName: sdkaws.String(name), Arn: sdkaws.String(qsTestARN("user", "default/"+name))}
}

// seedQSGroups stores one group row per name in namespace and returns them as
// the group phase hands them on.
func seedQSGroups(t *testing.T, st *store.Store, namespace string, names ...string) []qsGroupRef {
	t.Helper()
	groups := make([]qsGroupRef, len(names))
	for i, name := range names {
		arn := qsTestARN("group", namespace+"/"+name)
		upsertTestResource(t, st, "aws", testAccountID, TypeQuickSightGroup, arn, testRegion, "{}")
		groups[i] = qsGroupRef{namespace: namespace, name: name, arn: arn}
	}
	return groups
}

// qsMemberClient answers op with answer, recording each call's parent key,
// NextToken and MaxResults as "key|token|max".
func qsMemberClient(op string, key func(any) (string, *string, *int32), answer func(key, token string) (any, error)) (*quicksight.Client, *parentCalls) {
	calls := &parentCalls{}
	h := map[string]func(any) (any, error){
		op: func(in any) (any, error) {
			k, token, maxResults := key(in)
			calls.add(fmt.Sprintf("%s|%s|%d", k, sv(token), sdkaws.ToInt32(maxResults)))
			return answer(k, sv(token))
		},
	}
	return quicksight.NewFromConfig(opStubConfig(h, apiErr("InternalFailureException", "unexpected op"))), calls
}

func qsFolderMembersKey(in any) (string, *string, *int32) {
	i := in.(*quicksight.ListFolderMembersInput)
	return *i.FolderId, i.NextToken, i.MaxResults
}

func qsGroupMembershipsKey(in any) (string, *string, *int32) {
	i := in.(*quicksight.ListGroupMembershipsInput)
	return *i.Namespace + "/" + *i.GroupName, i.NextToken, i.MaxResults
}

func TestScanQSFolderMembers_PaginatesEveryFolder(t *testing.T) {
	st := newTestStore(t)
	warns := countWarnings(st)
	folders := seedQSParents(t, st, TypeQuickSightFolder, "folder", "f1", "f2")
	client, calls := qsMemberClient("ListFolderMembers", qsFolderMembersKey, func(folder, token string) (any, error) {
		switch folder + "|" + token {
		case "f1|":
			return &quicksight.ListFolderMembersOutput{
				FolderMemberList: []qstypes.MemberIdArnPair{qsFolderMember("dashboard", "d1"), qsFolderMember("analysis", "d1")},
				NextToken:        sdkaws.String("p2"),
			}, nil
		case "f1|p2":
			return &quicksight.ListFolderMembersOutput{FolderMemberList: []qstypes.MemberIdArnPair{qsFolderMember("dataset", "ds1")}}, nil
		case "f2|":
			return &quicksight.ListFolderMembersOutput{FolderMemberList: []qstypes.MemberIdArnPair{
				qsFolderMember("datasource", "src1"), {MemberId: sdkaws.String("no-arn")},
			}}, nil
		}
		return nil, apiErr("InternalFailureException", "unexpected page "+folder+"|"+token)
	})

	total, inserted, err := scanQSFolderMembers(context.Background(), client, newTestAccount(testAccountID), testRegion, st, testScanID, folders)
	if err != nil {
		t.Fatalf("scanQSFolderMembers: %v", err)
	}
	if total != 4 || inserted != 4 {
		t.Errorf("total, inserted = %d, %d; want 4, 4 (the ARN-less member dropped)", total, inserted)
	}
	if got, want := calls.distinct(), []string{"f1|p2|100", "f1||100", "f2||100"}; !slices.Equal(got, want) {
		t.Errorf("calls = %q; want %q (every folder, every page, MaxResults 100)", got, want)
	}
	f1, f2 := "arn:aws:quicksight:us-east-1:123456789012:folder/f1", "arn:aws:quicksight:us-east-1:123456789012:folder/f2"
	want := map[string]string{
		f1 + "/member/arn:aws:quicksight:us-east-1:123456789012:dashboard/d1":    "d1",
		f1 + "/member/arn:aws:quicksight:us-east-1:123456789012:analysis/d1":     "d1",
		f1 + "/member/arn:aws:quicksight:us-east-1:123456789012:dataset/ds1":     "ds1",
		f2 + "/member/arn:aws:quicksight:us-east-1:123456789012:datasource/src1": "src1",
	}
	rows := qsRows(t, st, TypeQuickSightFolderMember)
	if len(rows) != len(want) {
		t.Errorf("stored %d folder members; want %d", len(rows), len(want))
	}
	for nativeID, name := range want {
		r, ok := rows[nativeID]
		if !ok {
			t.Errorf("folder member %s not stored", nativeID)
			continue
		}
		if r.Type != TypeQuickSightFolderMember || sv(r.Region) != testRegion || sv(r.Name) != name {
			t.Errorf("%s: type, region, name = %s, %s, %s; want %s, %s, %s", nativeID, r.Type, sv(r.Region), sv(r.Name), TypeQuickSightFolderMember, testRegion, name)
		}
		var attrs map[string]any
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil || attrs["MemberId"] != name {
			t.Errorf("%s: attrs = %s (err %v); want MemberId %q", nativeID, r.AttributesJSON, err, name)
		}
	}
	assertQSContains(t, st, f1,
		f1+"/member/arn:aws:quicksight:us-east-1:123456789012:dashboard/d1",
		f1+"/member/arn:aws:quicksight:us-east-1:123456789012:analysis/d1",
		f1+"/member/arn:aws:quicksight:us-east-1:123456789012:dataset/ds1")
	assertQSContains(t, st, f2, f2+"/member/arn:aws:quicksight:us-east-1:123456789012:datasource/src1")
	if *warns != 1 {
		t.Errorf("recorded %d warnings; want 1, for the ARN-less member", *warns)
	}
}

func TestScanQSGroupMemberships_PaginatesEveryGroupInItsNamespace(t *testing.T) {
	st := newTestStore(t)
	warns := countWarnings(st)
	groups := append(seedQSGroups(t, st, "default", "admins"), seedQSGroups(t, st, "team", "Role/readers")...)
	client, calls := qsMemberClient("ListGroupMemberships", qsGroupMembershipsKey, func(group, token string) (any, error) {
		switch group + "|" + token {
		case "default/admins|":
			return &quicksight.ListGroupMembershipsOutput{
				GroupMemberList: []qstypes.GroupMember{qsGroupMember("alice@example.com")},
				NextToken:       sdkaws.String("p2"),
			}, nil
		case "default/admins|p2":
			return &quicksight.ListGroupMembershipsOutput{GroupMemberList: []qstypes.GroupMember{qsGroupMember("ReadRole/bob")}}, nil
		case "team/Role/readers|":
			return &quicksight.ListGroupMembershipsOutput{GroupMemberList: []qstypes.GroupMember{
				qsGroupMember("carol"), {MemberName: sdkaws.String("no-arn")},
			}}, nil
		}
		return nil, apiErr("InternalFailureException", "unexpected page "+group+"|"+token)
	})

	total, inserted, err := scanQSGroupMemberships(context.Background(), client, newTestAccount(testAccountID), testRegion, st, testScanID, groups)
	if err != nil {
		t.Fatalf("scanQSGroupMemberships: %v", err)
	}
	if total != 3 || inserted != 3 {
		t.Errorf("total, inserted = %d, %d; want 3, 3 (the ARN-less member dropped)", total, inserted)
	}
	if got, want := calls.distinct(), []string{"default/admins|p2|100", "default/admins||100", "team/Role/readers||100"}; !slices.Equal(got, want) {
		t.Errorf("calls = %q; want %q (each group in its own namespace, every page, MaxResults 100)", got, want)
	}
	admins := "arn:aws:quicksight:us-east-1:123456789012:group/default/admins"
	readers := "arn:aws:quicksight:us-east-1:123456789012:group/team/Role/readers"
	want := map[string]string{
		admins + "/member/arn:aws:quicksight:us-east-1:123456789012:user/default/alice@example.com": "alice@example.com",
		admins + "/member/arn:aws:quicksight:us-east-1:123456789012:user/default/ReadRole/bob":      "ReadRole/bob",
		readers + "/member/arn:aws:quicksight:us-east-1:123456789012:user/default/carol":            "carol",
	}
	rows := qsRows(t, st, TypeQuickSightGroupMembership)
	if len(rows) != len(want) {
		t.Errorf("stored %d group memberships; want %d", len(rows), len(want))
	}
	for nativeID, name := range want {
		r, ok := rows[nativeID]
		if !ok {
			t.Errorf("group membership %s not stored", nativeID)
			continue
		}
		if r.Type != TypeQuickSightGroupMembership || sv(r.Region) != testRegion || sv(r.Name) != name {
			t.Errorf("%s: type, region, name = %s, %s, %s; want %s, %s, %s", nativeID, r.Type, sv(r.Region), sv(r.Name), TypeQuickSightGroupMembership, testRegion, name)
		}
		var attrs map[string]any
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil || attrs["MemberName"] != name {
			t.Errorf("%s: attrs = %s (err %v); want MemberName %q", nativeID, r.AttributesJSON, err, name)
		}
	}
	assertQSContains(t, st, admins,
		admins+"/member/arn:aws:quicksight:us-east-1:123456789012:user/default/alice@example.com",
		admins+"/member/arn:aws:quicksight:us-east-1:123456789012:user/default/ReadRole/bob")
	assertQSContains(t, st, readers, readers+"/member/arn:aws:quicksight:us-east-1:123456789012:user/default/carol")
	if *warns != 1 {
		t.Errorf("recorded %d warnings; want 1, for the ARN-less member", *warns)
	}
}

// qsMemberPager answers one parent's page request with the next page's token,
// or with an error.
type qsMemberPager func(parent, token string) (next *string, err error)

// qsMemberPhase runs one member phase over the parents named by keys; every
// page pager answers holds one member.
type qsMemberPhase struct {
	name, op, childType string
	run                 func(t *testing.T, st *store.Store, keys []string, pager qsMemberPager) (int, int, error)
}

var qsMemberPhases = []qsMemberPhase{
	{
		name: "folder members", op: qsListFolderMembersOp, childType: TypeQuickSightFolderMember,
		run: func(t *testing.T, st *store.Store, keys []string, pager qsMemberPager) (int, int, error) {
			folders := seedQSParents(t, st, TypeQuickSightFolder, "folder", keys...)
			client, _ := qsMemberClient("ListFolderMembers", qsFolderMembersKey, func(folder, token string) (any, error) {
				next, err := pager(folder, token)
				if err != nil {
					return nil, err
				}
				return &quicksight.ListFolderMembersOutput{
					FolderMemberList: []qstypes.MemberIdArnPair{qsFolderMember("dashboard", "d-"+folder)},
					NextToken:        next,
				}, nil
			})
			return scanQSFolderMembers(context.Background(), client, newTestAccount(testAccountID), testRegion, st, testScanID, folders)
		},
	},
	{
		name: "group memberships", op: qsListGroupMembershipsOp, childType: TypeQuickSightGroupMembership,
		run: func(t *testing.T, st *store.Store, keys []string, pager qsMemberPager) (int, int, error) {
			groups := seedQSGroups(t, st, "default", keys...)
			client, _ := qsMemberClient("ListGroupMemberships", qsGroupMembershipsKey, func(key, token string) (any, error) {
				group := strings.TrimPrefix(key, "default/")
				next, err := pager(group, token)
				if err != nil {
					return nil, err
				}
				return &quicksight.ListGroupMembershipsOutput{
					GroupMemberList: []qstypes.GroupMember{qsGroupMember("u-" + group)},
					NextToken:       next,
				}, nil
			})
			return scanQSGroupMemberships(context.Background(), client, newTestAccount(testAccountID), testRegion, st, testScanID, groups)
		},
	},
}

// qsOneMemberPage answers every parent with a single page, except the parents
// in fail, which answer with their error.
func qsOneMemberPage(fail map[string]error) qsMemberPager {
	return func(parent, _ string) (*string, error) { return nil, fail[parent] }
}

func TestScanQSMemberPhases_EmptyStoresNothing(t *testing.T) {
	for _, p := range qsMemberPhases {
		t.Run(p.name, func(t *testing.T) {
			st := newTestStore(t)
			total, inserted, err := p.run(t, st, nil, qsOneMemberPage(nil))
			if err != nil || total != 0 || inserted != 0 {
				t.Errorf("no parents: total, inserted, err = %d, %d, %v; want 0, 0, nil", total, inserted, err)
			}
			if got := len(qsRows(t, st, p.childType)); got != 0 {
				t.Errorf("stored %d rows; want 0", got)
			}
		})
	}
}

func TestScanQSMemberPhases_ErrorHandling(t *testing.T) {
	type outcome int
	const (
		skipped outcome = iota // the parent is dropped silently
		warned                 // the parent is dropped with one warning
		failed                 // the error is returned
	)
	cases := []struct {
		code string
		want [2]outcome // folder members, group memberships
	}{
		{"ResourceNotFoundException", [2]outcome{skipped, skipped}},
		{"AccessDeniedException", [2]outcome{warned, warned}},
		{"ThrottlingException", [2]outcome{warned, warned}},
		{"PreconditionNotMetException", [2]outcome{failed, warned}},
		{"ResourceUnavailableException", [2]outcome{failed, warned}},
		{"UnsupportedUserEditionException", [2]outcome{failed, failed}},
		{"InvalidParameterValueException", [2]outcome{failed, failed}},
		{"InternalFailureException", [2]outcome{failed, failed}},
	}
	for i, p := range qsMemberPhases {
		for _, tc := range cases {
			t.Run(p.name+"/"+tc.code, func(t *testing.T) {
				st := newTestStore(t)
				var warnings []string
				st.OnWarn = func(w store.ScanWarning) { warnings = append(warnings, w.Service) }
				total, _, err := p.run(t, st, []string{"bad", "ok1", "ok2"}, qsOneMemberPage(map[string]error{"bad": apiErr(tc.code, "x")}))
				want := tc.want[i]
				if want == failed {
					if !isAPIErrorCode(err, tc.code) {
						t.Fatalf("err = %v; want the %s returned", err, tc.code)
					}
					return
				}
				if err != nil {
					t.Fatalf("err = %v; want nil", err)
				}
				if got := len(qsRows(t, st, p.childType)); got != 2 || total != 2 {
					t.Errorf("stored %d rows, total %d; want 2, 2 (the two other parents)", got, total)
				}
				wantWarns := 0
				if want == warned {
					wantWarns = 1
				}
				if len(warnings) != wantWarns {
					t.Errorf("warnings = %q; want %d", warnings, wantWarns)
				}
				for _, svc := range warnings {
					if svc != p.op {
						t.Errorf("warning names %q; want %q", svc, p.op)
					}
				}
			})
		}
	}
}

func TestScanQSMemberPhases_WarnOncePerCode(t *testing.T) {
	for _, p := range qsMemberPhases {
		for name, failure := range map[string]error{
			"denied":    apiErr("AccessDeniedException", "User: x is not authorized to perform: quicksight:List"),
			"throttled": apiErr("ThrottlingException", "slow down"),
		} {
			t.Run(p.name+"/"+name, func(t *testing.T) {
				st := newTestStore(t)
				warns := countOpWarnings(st, p.op)
				_, _, err := p.run(t, st, []string{"d1", "d2", "d3", "ok"}, qsOneMemberPage(map[string]error{"d1": failure, "d2": failure, "d3": failure}))
				if err != nil {
					t.Fatalf("err = %v; want nil", err)
				}
				if *warns != 1 {
					t.Errorf("recorded %d warnings for three %s parents; want 1", *warns, name)
				}
				if got := len(qsRows(t, st, p.childType)); got != 1 {
					t.Errorf("stored %d rows; want the readable parent's 1", got)
				}
			})
		}
	}
	t.Run("group memberships/precondition and unavailable", func(t *testing.T) {
		st := newTestStore(t)
		warns := countOpWarnings(st, qsListGroupMembershipsOp)
		pre, gone := apiErr("PreconditionNotMetException", "x"), apiErr("ResourceUnavailableException", "x")
		_, _, err := qsMemberPhases[1].run(t, st, []string{"p1", "p2", "u1", "ok"}, qsOneMemberPage(map[string]error{"p1": pre, "p2": pre, "u1": gone}))
		if err != nil {
			t.Fatalf("err = %v; want nil", err)
		}
		if *warns != 2 {
			t.Errorf("recorded %d warnings; want 2 (one per code)", *warns)
		}
		if got := len(qsRows(t, st, TypeQuickSightGroupMembership)); got != 1 {
			t.Errorf("stored %d rows; want the readable group's 1", got)
		}
	})
}

// A stored parent without the key its member op needs is not handed on, and
// the parent phase says so in one warning: under its own list op for folders
// (storeParentList), under the member op for groups.
func TestScanQSParentPhases_WarnOnceForParentsWithoutKey(t *testing.T) {
	h := map[string]func(any) (any, error){
		"ListTagsForResource": func(any) (any, error) { return &quicksight.ListTagsForResourceOutput{}, nil },
		"ListFolders": func(any) (any, error) {
			return &quicksight.ListFoldersOutput{FolderSummaryList: []qstypes.FolderSummary{
				{FolderId: sdkaws.String("f1"), Arn: sdkaws.String(qsTestARN("folder", "f1"))},
				{Arn: sdkaws.String(qsTestARN("folder", "x1"))},
				{Arn: sdkaws.String(qsTestARN("folder", "x2"))},
			}}, nil
		},
		"ListGroups": func(any) (any, error) {
			return &quicksight.ListGroupsOutput{GroupList: []qstypes.Group{
				{GroupName: sdkaws.String("admins"), Arn: sdkaws.String(qsTestARN("group", "default/admins"))},
				{Arn: sdkaws.String(qsTestARN("group", "default/x1"))},
				{Arn: sdkaws.String(qsTestARN("group", "default/x2"))},
			}}, nil
		},
	}
	client := quicksight.NewFromConfig(opStubConfig(h, apiErr("InternalFailureException", "unexpected op")))
	acct := newTestAccount(testAccountID)

	t.Run("folders", func(t *testing.T) {
		st := newTestStore(t)
		warns := countOpWarnings(st, qsListFoldersOp)
		var p phaseTotals
		parents, _ := scanQSFolders(context.Background(), client, acct, testRegion, st, testScanID, &p)
		if _, _, err := p.result(st, acct, testRegion); err != nil {
			t.Fatalf("scanQSFolders: %v", err)
		}
		if want := []childParent{{id: "f1", arn: qsTestARN("folder", "f1")}}; p.total != 3 || !slices.Equal(parents, want) {
			t.Errorf("total, parents = %d, %v; want 3, %v", p.total, parents, want)
		}
		if *warns != 1 {
			t.Errorf("recorded %d warnings for two folders without a FolderId; want 1", *warns)
		}
	})
	t.Run("groups", func(t *testing.T) {
		st := newTestStore(t)
		warns := countOpWarnings(st, qsListGroupMembershipsOp)
		groups, total, _, err := scanQSGroups(context.Background(), client, acct, testRegion, st, testScanID,
			[]childParent{{id: "default", arn: qsTestARN("namespace", "default")}})
		if err != nil {
			t.Fatalf("scanQSGroups: %v", err)
		}
		if want := []qsGroupRef{{namespace: "default", name: "admins", arn: qsTestARN("group", "default/admins")}}; total != 3 || !slices.Equal(groups, want) {
			t.Errorf("total, groups = %d, %v; want 3, %v", total, groups, want)
		}
		if *warns != 1 {
			t.Errorf("recorded %d warnings for two groups without a GroupName; want 1", *warns)
		}
	})
}

func TestScanQSMemberPhases_StopsOnRepeatedToken(t *testing.T) {
	for _, p := range qsMemberPhases {
		t.Run(p.name, func(t *testing.T) {
			st := newTestStore(t)
			var mu sync.Mutex
			calls := 0
			_, _, err := p.run(t, st, []string{"only"}, func(_, _ string) (*string, error) {
				mu.Lock()
				defer mu.Unlock()
				if calls++; calls > 3 {
					return nil, apiErr("InternalFailureException", "the scan does not stop on a repeated token")
				}
				return sdkaws.String("same"), nil
			})
			if !errors.Is(err, errRepeatedNextToken) {
				t.Fatalf("err = %v; want errRepeatedNextToken (the listing stopped incomplete)", err)
			}
			if calls != 2 {
				t.Errorf("made %d calls; want 2 (stop when the token repeats)", calls)
			}
		})
	}
}

// qsMemberTopLevelHandlers serves, on top of qsTopLevelHandlers, the folder
// f1 and two namespaces each holding a group named "admins" and no users or
// assignments, with one member per folder and group. The member ops answer only for the FolderId and the
// (Namespace, GroupName) the parent phases listed, so a parent handed on
// under the wrong key fails the scan.
func qsMemberTopLevelHandlers() map[string]func(any) (any, error) {
	h := qsTopLevelHandlers()
	h["ListNamespaces"] = func(any) (any, error) {
		return &quicksight.ListNamespacesOutput{Namespaces: []qstypes.NamespaceInfoV2{
			{Name: sdkaws.String("default"), Arn: sdkaws.String(qsTestARN("namespace", "default"))},
			{Name: sdkaws.String("team"), Arn: sdkaws.String(qsTestARN("namespace", "team"))},
		}}, nil
	}
	h["ListFolders"] = func(any) (any, error) {
		return &quicksight.ListFoldersOutput{FolderSummaryList: []qstypes.FolderSummary{
			{FolderId: sdkaws.String("f1"), Arn: sdkaws.String(qsTestARN("folder", "f1"))},
		}}, nil
	}
	h["ListUsers"] = func(any) (any, error) { return &quicksight.ListUsersOutput{}, nil }
	h["ListIAMPolicyAssignments"] = func(any) (any, error) { return &quicksight.ListIAMPolicyAssignmentsOutput{}, nil }
	h["ListGroups"] = func(in any) (any, error) {
		ns := *in.(*quicksight.ListGroupsInput).Namespace
		return &quicksight.ListGroupsOutput{GroupList: []qstypes.Group{
			{GroupName: sdkaws.String("admins"), Arn: sdkaws.String(qsTestARN("group", ns+"/admins"))},
		}}, nil
	}
	h["ListFolderMembers"] = func(in any) (any, error) {
		if fid := *in.(*quicksight.ListFolderMembersInput).FolderId; fid != "f1" {
			return nil, apiErr("InternalFailureException", "unexpected FolderId "+fid)
		}
		return &quicksight.ListFolderMembersOutput{FolderMemberList: []qstypes.MemberIdArnPair{qsFolderMember("dashboard", "d1")}}, nil
	}
	h["ListGroupMemberships"] = func(in any) (any, error) {
		i := in.(*quicksight.ListGroupMembershipsInput)
		member, ok := map[string]string{"default/admins": "alice", "team/admins": "bob"}[*i.Namespace+"/"+*i.GroupName]
		if !ok {
			return nil, apiErr("InternalFailureException", "unexpected group "+*i.Namespace+"/"+*i.GroupName)
		}
		return &quicksight.ListGroupMembershipsOutput{GroupMemberList: []qstypes.GroupMember{qsGroupMember(member)}}, nil
	}
	return h
}

const (
	qsTestFolderMember     = "arn:aws:quicksight:us-east-1:123456789012:folder/f1/member/arn:aws:quicksight:us-east-1:123456789012:dashboard/d1"
	qsTestGroupMembership  = "arn:aws:quicksight:us-east-1:123456789012:group/default/admins/member/arn:aws:quicksight:us-east-1:123456789012:user/default/alice"
	qsTestGroupMembership2 = "arn:aws:quicksight:us-east-1:123456789012:group/team/admins/member/arn:aws:quicksight:us-east-1:123456789012:user/default/bob"
)

// TestScanQuickSight_MemberPhasesRunAfterOlderPhases pins the wiring: the
// member phases list the folders and groups the older phases stored, run even
// when a later older phase fails, and a member phase's hard error costs no
// other phase its rows.
func TestScanQuickSight_MemberPhasesRunAfterOlderPhases(t *testing.T) {
	cases := []struct {
		name, failOp             string
		wantFolderMember, wantGM bool
	}{
		{"all succeed", "", true, true},
		{"last older phase fails", "DescribeKeyRegistration", true, true},
		{"folder members fail", "ListFolderMembers", false, true},
		{"group memberships fail", "ListGroupMemberships", true, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			h := qsMemberTopLevelHandlers()
			if tc.failOp != "" {
				h[tc.failOp] = func(any) (any, error) { return nil, apiErr("InternalFailureException", "boom") }
			}
			acct := &account{ID: testAccountID, Name: "Test Account", cfg: opStubConfig(h, qsNotServed)}

			total, _, err := scanQuickSight(context.Background(), acct, testRegion, st, testScanID)
			if tc.failOp == "" && err != nil {
				t.Fatalf("scanQuickSight: %v", err)
			}
			if tc.failOp != "" && (!isAPIErrorCode(err, "InternalFailureException") || classifyServiceError(err) != outcomeError) {
				t.Fatalf("err = %v; want the %s InternalFailureException as a hard error", err, tc.failOp)
			}
			// data set, two namespaces, folder, two groups and one row per account-level list.
			wantTotal := 6 + len(qsListCases)
			for _, row := range [][2]string{
				{TypeQuickSightDataSet, qsTestARN("dataset", "ds1")},
				{TypeQuickSightFolder, qsTestARN("folder", "f1")},
				{TypeQuickSightGroup, qsTestARN("group", "default/admins")},
				{TypeQuickSightGroup, qsTestARN("group", "team/admins")},
			} {
				if _, ok := qsRows(t, st, row[0])[row[1]]; !ok {
					t.Errorf("%s %s not stored", row[0], row[1])
				}
			}
			for _, c := range qsListCases {
				if _, ok := qsRows(t, st, c.rtype)[qsTestARN(c.kind, "x1")]; !ok {
					t.Errorf("%s row not stored", c.name)
				}
			}
			if _, has := qsRows(t, st, TypeQuickSightFolderMember)[qsTestFolderMember]; has != tc.wantFolderMember {
				t.Errorf("folder member stored = %v; want %v", has, tc.wantFolderMember)
			} else if has {
				wantTotal++
				assertQSContains(t, st, qsTestARN("folder", "f1"), qsTestFolderMember)
			}
			for group, membership := range map[string]string{
				qsTestARN("group", "default/admins"): qsTestGroupMembership,
				qsTestARN("group", "team/admins"):    qsTestGroupMembership2,
			} {
				if _, has := qsRows(t, st, TypeQuickSightGroupMembership)[membership]; has != tc.wantGM {
					t.Errorf("group membership %s stored = %v; want %v", membership, has, tc.wantGM)
				} else if has {
					wantTotal++
					assertQSContains(t, st, group, membership)
				}
			}
			if total != wantTotal {
				t.Errorf("total = %d; want %d", total, wantTotal)
			}
		})
	}
}

// A throttled member phase skips its parents under one warning, so an older
// phase's hard error still reaches the dispatcher as a hard error, and the
// other way round.
func TestScanQuickSight_MemberPhaseTransientDoesNotMaskHardError(t *testing.T) {
	type side struct{ op, warnOp string }
	older := side{"DescribeKeyRegistration", "quicksight:DescribeKeyRegistration"}
	var pairs [][2]side
	for _, newer := range []side{{"ListFolderMembers", qsListFolderMembersOp}, {"ListGroupMemberships", qsListGroupMembershipsOp}} {
		pairs = append(pairs, [2]side{older, newer}, [2]side{newer, older})
	}
	for _, p := range pairs {
		transient, hard := p[0], p[1]
		t.Run(transient.op+" throttled, "+hard.op+" fails", func(t *testing.T) {
			st := newTestStore(t)
			var warned []string
			st.OnWarn = func(w store.ScanWarning) { warned = append(warned, w.Service) }
			h := qsMemberTopLevelHandlers()
			h[transient.op] = func(any) (any, error) { return nil, apiErr("ThrottlingException", "slow down") }
			h[hard.op] = func(any) (any, error) { return nil, apiErr("ValidationException", "bad") }
			acct := &account{ID: testAccountID, Name: "Test Account", cfg: opStubConfig(h, qsNotServed)}

			_, _, err := scanQuickSight(context.Background(), acct, testRegion, st, testScanID)
			if !isAPIErrorCode(err, "ValidationException") || classifyServiceError(err) != outcomeError {
				t.Errorf("err = %v (outcome %v); want the %s ValidationException as outcomeError", err, classifyServiceError(err), hard.op)
			}
			if !slices.Equal(warned, []string{transient.warnOp}) {
				t.Errorf("warnings = %q; want one for %s", warned, transient.warnOp)
			}
		})
	}
}

// qsTagCalls records the ResourceArn of every ListTagsForResource call and
// answers it from errs, or with the tag {"type": <the row's type>} for an ARN
// in types, or with no tags; calls run concurrently.
type qsTagCalls struct {
	mu    sync.Mutex
	arns  []string
	types map[string]string
	errs  map[string]error
}

func (c *qsTagCalls) handler(in any) (any, error) {
	arn := *in.(*quicksight.ListTagsForResourceInput).ResourceArn
	c.mu.Lock()
	c.arns = append(c.arns, arn)
	c.mu.Unlock()
	if err := c.errs[arn]; err != nil {
		return nil, err
	}
	out := &quicksight.ListTagsForResourceOutput{}
	if typ, ok := c.types[arn]; ok {
		out.Tags = []qstypes.Tag{{Key: sdkaws.String("type"), Value: sdkaws.String(typ)}}
	}
	return out, nil
}

// asked is the ARNs ListTagsForResource was called for, sorted.
func (c *qsTagCalls) asked() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return slices.Sorted(slices.Values(c.arns))
}

// qsTaggedFixture is one tagged type's account-level listing: op answers
// with page(ids), the items "tagged" and "bare" under ARNs of kind.
type qsTaggedFixture struct {
	typ, kind, op string
	page          func(ids []string) any
}

var qsFixtureIDs = []string{"tagged", "bare"}

func qsTaggedFixtures() []qsTaggedFixture {
	arn := func(kind, id string) *string { return sdkaws.String(qsTestARN(kind, id)) }
	return []qsTaggedFixture{
		{TypeQuickSightDataSet, "dataset", "ListDataSets", func(ids []string) any {
			out := &quicksight.ListDataSetsOutput{}
			for _, id := range ids {
				out.DataSetSummaries = append(out.DataSetSummaries, qstypes.DataSetSummary{DataSetId: sdkaws.String(id), Arn: arn("dataset", id)})
			}
			return out
		}},
		{TypeQuickSightNamespace, "namespace", "ListNamespaces", func(ids []string) any {
			out := &quicksight.ListNamespacesOutput{}
			for _, id := range ids {
				out.Namespaces = append(out.Namespaces, qstypes.NamespaceInfoV2{Name: sdkaws.String(id), Arn: arn("namespace", id)})
			}
			return out
		}},
		{TypeQuickSightAnalysis, "analysis", "ListAnalyses", func(ids []string) any {
			out := &quicksight.ListAnalysesOutput{}
			for _, id := range ids {
				out.AnalysisSummaryList = append(out.AnalysisSummaryList, qstypes.AnalysisSummary{AnalysisId: sdkaws.String(id), Arn: arn("analysis", id)})
			}
			return out
		}},
		{TypeQuickSightDashboard, "dashboard", "ListDashboards", func(ids []string) any {
			out := &quicksight.ListDashboardsOutput{}
			for _, id := range ids {
				out.DashboardSummaryList = append(out.DashboardSummaryList, qstypes.DashboardSummary{DashboardId: sdkaws.String(id), Arn: arn("dashboard", id)})
			}
			return out
		}},
		{TypeQuickSightDataSource, "datasource", "ListDataSources", func(ids []string) any {
			out := &quicksight.ListDataSourcesOutput{}
			for _, id := range ids {
				out.DataSources = append(out.DataSources, qstypes.DataSource{DataSourceId: sdkaws.String(id), Arn: arn("datasource", id)})
			}
			return out
		}},
		{TypeQuickSightFolder, "folder", "ListFolders", func(ids []string) any {
			out := &quicksight.ListFoldersOutput{}
			for _, id := range ids {
				out.FolderSummaryList = append(out.FolderSummaryList, qstypes.FolderSummary{FolderId: sdkaws.String(id), Arn: arn("folder", id)})
			}
			return out
		}},
		{TypeQuickSightTemplate, "template", "ListTemplates", func(ids []string) any {
			out := &quicksight.ListTemplatesOutput{}
			for _, id := range ids {
				out.TemplateSummaryList = append(out.TemplateSummaryList, qstypes.TemplateSummary{TemplateId: sdkaws.String(id), Arn: arn("template", id)})
			}
			return out
		}},
		{TypeQuickSightTheme, "theme", "ListThemes", func(ids []string) any {
			out := &quicksight.ListThemesOutput{}
			for _, id := range ids {
				out.ThemeSummaryList = append(out.ThemeSummaryList, qstypes.ThemeSummary{ThemeId: sdkaws.String(id), Arn: arn("theme", id)})
			}
			return out
		}},
		{TypeQuickSightTopic, "topic", "ListTopics", func(ids []string) any {
			out := &quicksight.ListTopicsOutput{}
			for _, id := range ids {
				out.TopicsSummaries = append(out.TopicsSummaries, qstypes.TopicSummary{TopicId: sdkaws.String(id), Arn: arn("topic", id)})
			}
			return out
		}},
		{TypeQuickSightVPCConnection, "vpcConnection", "ListVPCConnections", func(ids []string) any {
			out := &quicksight.ListVPCConnectionsOutput{}
			for _, id := range ids {
				out.VPCConnectionSummaries = append(out.VPCConnectionSummaries, qstypes.VPCConnectionSummary{VPCConnectionId: sdkaws.String(id), Arn: arn("vpcConnection", id)})
			}
			return out
		}},
		{TypeQuickSightCustomPermissions, "custompermissions", "ListCustomPermissions", func(ids []string) any {
			out := &quicksight.ListCustomPermissionsOutput{}
			for _, id := range ids {
				out.CustomPermissionsList = append(out.CustomPermissionsList, qstypes.CustomPermissions{CustomPermissionsName: sdkaws.String(id), Arn: arn("custompermissions", id)})
			}
			return out
		}},
		{TypeQuickSightActionConnector, "action-connector", "ListActionConnectors", func(ids []string) any {
			out := &quicksight.ListActionConnectorsOutput{}
			for _, id := range ids {
				out.ActionConnectorSummaries = append(out.ActionConnectorSummaries, qstypes.ActionConnectorSummary{ActionConnectorId: sdkaws.String(id), Arn: arn("action-connector", id)})
			}
			return out
		}},
		{TypeQuickSightBrand, "brand", "ListBrands", func(ids []string) any {
			out := &quicksight.ListBrandsOutput{}
			for _, id := range ids {
				out.Brands = append(out.Brands, qstypes.BrandSummary{BrandId: sdkaws.String(id), Arn: arn("brand", id)})
			}
			return out
		}},
		{TypeQuickSightFlow, "flow", "ListFlows", func(ids []string) any {
			out := &quicksight.ListFlowsOutput{}
			for _, id := range ids {
				out.FlowSummaryList = append(out.FlowSummaryList, qstypes.FlowSummary{FlowId: sdkaws.String(id), Arn: arn("flow", id)})
			}
			return out
		}},
		{TypeQuickSightOAuthClientApplication, "oauthClientApplication", "ListOAuthClientApplications", func(ids []string) any {
			out := &quicksight.ListOAuthClientApplicationsOutput{}
			for _, id := range ids {
				out.OAuthClientApplications = append(out.OAuthClientApplications, qstypes.OAuthClientApplicationSummary{OAuthClientApplicationId: sdkaws.String(id), Arn: arn("oauthClientApplication", id)})
			}
			return out
		}},
		{TypeQuickSightApp, "app", "ListApps", func(ids []string) any {
			out := &quicksight.ListAppsOutput{}
			for _, id := range ids {
				out.AppSummaryList = append(out.AppSummaryList, qstypes.AppSummary{AppId: sdkaws.String(id), Arn: arn("app", id)})
			}
			return out
		}},
		{TypeQuickSightDLPSetting, "dlpsetting", "ListDlpSettings", func(ids []string) any {
			out := &quicksight.ListDlpSettingsOutput{}
			for _, id := range ids {
				out.DlpSettingSummaries = append(out.DlpSettingSummaries, qstypes.DlpSettingSummary{DlpSettingId: sdkaws.String(id), Arn: arn("dlpsetting", id)})
			}
			return out
		}},
		// Users are listed per namespace: only the namespace "tagged" holds any.
		{TypeQuickSightUser, "user", "ListUsers", nil},
	}
}

// qsTestCustomization is the customization singleton's ARN in the fixture.
var qsTestCustomization = qsTestARN("customization", testAccountID)

// qsFixture serves every tagged type's listing, the customization
// singleton, and empty child listings, through tags; the DataSetId of
// every ListRefreshSchedules request is recorded in refreshDataSets. MaxResults of every
// list request is recorded per op (0 when unset) in maxResults.
type qsFixture struct {
	tags            *qsTagCalls
	mu              sync.Mutex
	maxResults      map[string][]int32
	refreshDataSets []string
}

func (f *qsFixture) record(op string, in any) {
	mr, _ := reflect.ValueOf(in).Elem().FieldByName("MaxResults").Interface().(*int32)
	f.mu.Lock()
	f.maxResults[op] = append(f.maxResults[op], sdkaws.ToInt32(mr))
	f.mu.Unlock()
}

func newQSFixture() (*qsFixture, map[string]func(any) (any, error)) {
	f := &qsFixture{tags: &qsTagCalls{types: map[string]string{qsTestCustomization: TypeQuickSightCustomization}}, maxResults: map[string][]int32{}}
	h := map[string]func(any) (any, error){
		"ListTagsForResource":         f.tags.handler,
		"DescribeAccountSubscription": qsSubscribed,
		"DescribeAccountCustomization": func(any) (any, error) {
			return &quicksight.DescribeAccountCustomizationOutput{
				Arn: sdkaws.String(qsTestCustomization), AccountCustomization: &qstypes.AccountCustomization{DefaultTheme: sdkaws.String("CLASSIC")},
			}, nil
		},
		"ListUsers": func(in any) (any, error) {
			f.record("ListUsers", in)
			out := &quicksight.ListUsersOutput{}
			if *in.(*quicksight.ListUsersInput).Namespace == "tagged" {
				for _, id := range qsFixtureIDs {
					out.UserList = append(out.UserList, qstypes.User{UserName: sdkaws.String(id), Arn: sdkaws.String(qsTestARN("user", id))})
				}
			}
			return out, nil
		},
		"ListRefreshSchedules": func(in any) (any, error) {
			f.mu.Lock()
			f.refreshDataSets = append(f.refreshDataSets, *in.(*quicksight.ListRefreshSchedulesInput).DataSetId)
			f.mu.Unlock()
			return &quicksight.ListRefreshSchedulesOutput{}, nil
		},
		"ListGroups":                func(any) (any, error) { return &quicksight.ListGroupsOutput{}, nil },
		"ListIAMPolicyAssignments":  func(any) (any, error) { return &quicksight.ListIAMPolicyAssignmentsOutput{}, nil },
		"ListFolderMembers":         func(any) (any, error) { return &quicksight.ListFolderMembersOutput{}, nil },
		"ListTemplateAliases":       func(any) (any, error) { return &quicksight.ListTemplateAliasesOutput{}, nil },
		"ListThemeAliases":          func(any) (any, error) { return &quicksight.ListThemeAliasesOutput{}, nil },
		"ListTopicRefreshSchedules": func(any) (any, error) { return &quicksight.ListTopicRefreshSchedulesOutput{}, nil },
	}
	for _, fx := range qsTaggedFixtures() {
		f.tags.types[qsTestARN(fx.kind, "tagged")] = fx.typ
		if fx.page == nil {
			continue
		}
		h[fx.op] = func(in any) (any, error) {
			f.record(fx.op, in)
			return fx.page(qsFixtureIDs), nil
		}
	}
	return f, h
}

// Every tagged QuickSight type is stored with the tags ListTagsForResource
// returns for its own ARN, or none; exactly those rows are tag-read; every
// list op asks for its model's page cap, 100; and a rescan inserts nothing.
func TestScanQuickSight_StoresTagsOfEveryTaggedType(t *testing.T) {
	st := newTestStore(t)
	warns := recordWarnings(st)
	f, h := newQSFixture()
	acct := &account{ID: testAccountID, Name: "Test Account", cfg: opStubConfig(h, qsNotServed)}

	total, inserted, err := scanQuickSight(context.Background(), acct, testRegion, st, testScanID)
	if err != nil {
		t.Fatalf("scanQuickSight: %v", err)
	}
	fixtures := qsTaggedFixtures()
	if want := 2*len(fixtures) + 1; total != want || inserted != want {
		t.Errorf("total, inserted = %d, %d; want %d, %d", total, inserted, want, want)
	}
	if len(*warns) != 0 {
		t.Errorf("warnings = %v; want none", *warns)
	}
	wantARNs := []string{qsTestCustomization}
	for _, fx := range fixtures {
		tagged, bare := qsTestARN(fx.kind, "tagged"), qsTestARN(fx.kind, "bare")
		wantARNs = append(wantARNs, tagged, bare)
		rows := rowsByNativeID(t, st, fx.typ)
		if len(rows) != 2 {
			t.Errorf("%s: %d rows; want 2", fx.typ, len(rows))
			continue
		}
		assertTagsMap(t, rows[tagged].TagsJSON, map[string]string{"type": fx.typ})
		if got := rows[bare].TagsJSON; got != nil {
			t.Errorf("%s bare TagsJSON = %s; want nil", fx.typ, *got)
		}
		if got := f.maxResults[fx.op]; len(got) == 0 || slices.ContainsFunc(got, func(n int32) bool { return n != 100 }) {
			t.Errorf("%s MaxResults = %v; want 100 on every request", fx.op, got)
		}
	}
	assertTagsMap(t, rowsByNativeID(t, st, TypeQuickSightCustomization)[qsTestCustomization].TagsJSON, map[string]string{"type": TypeQuickSightCustomization})
	if got := slices.Sorted(slices.Values(f.refreshDataSets)); !slices.Equal(got, []string{"bare", "tagged"}) {
		t.Errorf("ListRefreshSchedules DataSetIds = %v; want the stored data sets [bare tagged]", got)
	}
	if got := len(f.maxResults["ListUsers"]); got != 2 {
		t.Errorf("ListUsers called %d times; want 2 (once per namespace)", got)
	}
	slices.Sort(wantARNs)
	if got := f.tags.asked(); !slices.Equal(got, wantARNs) {
		t.Errorf("ListTagsForResource ARNs = %v; want %v", got, wantARNs)
	}

	total2, inserted2, err := scanQuickSight(context.Background(), acct, testRegion, st, testScanID)
	if err != nil || total2 != total || inserted2 != 0 {
		t.Errorf("rescan = (%d, %d, %v); want (%d, 0, nil)", total2, inserted2, err, total)
	}
}

// A failed tag read keeps the row without tags, except one QuickSight no
// longer finds, which is not stored; a denial warns once under the tag op
// with the account/region scope; a transient error and any other error are
// returned, the other phases' rows stored in every case.
func TestScanQuickSight_TagReadFailures(t *testing.T) {
	cases := []struct {
		name    string
		err     error
		kept    bool
		warns   int
		outcome serviceOutcome
		code    string
	}{
		{name: "not found", err: apiErr("ResourceNotFoundException", "gone")},
		{name: "denied", err: qsTopLevelDeny, kept: true, warns: 1},
		{name: "transient", err: apiErr("ThrottlingException", "Rate exceeded"), kept: true, outcome: outcomeTransient, code: "ThrottlingException"},
		{name: "other error", err: apiErr("InvalidParameterValueException", "bad arn"), kept: true, outcome: outcomeError, code: "InvalidParameterValueException"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := recordWarnings(st)
			f, h := newQSFixture()
			failing := qsTestARN("analysis", "tagged")
			f.tags.errs = map[string]error{failing: c.err}
			acct := &account{ID: testAccountID, Name: "Test Account", cfg: opStubConfig(h, qsNotServed)}

			total, _, err := scanQuickSight(context.Background(), acct, testRegion, st, testScanID)
			if c.code == "" && err != nil {
				t.Fatalf("err = %v; want nil", err)
			}
			if c.code != "" {
				assertAPICode(t, err, c.code)
				if got := classifyServiceError(err); got != c.outcome {
					t.Errorf("classifyServiceError = %v; want %v", got, c.outcome)
				}
			}
			want := 2*len(qsTaggedFixtures()) + 1
			if !c.kept {
				want--
			}
			if total != want {
				t.Errorf("total = %d; want %d", total, want)
			}
			rows := rowsByNativeID(t, st, TypeQuickSightAnalysis)
			r, ok := rows[failing]
			if ok != c.kept {
				t.Fatalf("failing analysis stored = %v; want %v", ok, c.kept)
			}
			if ok && r.TagsJSON != nil {
				t.Errorf("failing analysis TagsJSON = %s; want nil", *r.TagsJSON)
			}
			if _, ok := rows[qsTestARN("analysis", "bare")]; !ok {
				t.Error("sibling analysis not stored")
			}
			if _, ok := rowsByNativeID(t, st, TypeQuickSightUser)[qsTestARN("user", "tagged")]; !ok {
				t.Error("a later phase's row not stored")
			}
			var got []store.ScanWarning
			for _, w := range *warns {
				if w.Service == qsListTagsOp {
					got = append(got, w)
				}
			}
			if len(got) != c.warns || len(*warns) != c.warns {
				t.Fatalf("warnings = %v; want %d, under %s", *warns, c.warns, qsListTagsOp)
			}
			if c.warns > 0 && got[0].Scope != testAccountID+"/"+testRegion {
				t.Errorf("warning scope = %q; want %q", got[0].Scope, testAccountID+"/"+testRegion)
			}
		})
	}
}

// A throttled tag read beside a hard tag-read error, both of
// ListTagsForResource, is demoted to one warning under that op, so the hard
// error still reaches the dispatcher as a hard error.
func TestScanQuickSight_TagThrottleBesideHardTagErrorDemoted(t *testing.T) {
	for _, throttled := range []string{qsTestARN("analysis", "tagged"), qsTestARN("user", "tagged")} {
		t.Run(throttled, func(t *testing.T) {
			st := newTestStore(t)
			warns := recordWarnings(st)
			f, h := newQSFixture()
			f.tags.errs = map[string]error{
				throttled:                        apiErr("ThrottlingException", "Rate exceeded"),
				qsTestARN("dashboard", "tagged"): apiErr("InvalidParameterValueException", "bad arn"),
			}
			acct := &account{ID: testAccountID, Name: "Test Account", cfg: opStubConfig(h, qsNotServed)}

			_, _, err := scanQuickSight(context.Background(), acct, testRegion, st, testScanID)
			if got := classifyServiceError(err); got != outcomeError {
				t.Errorf("classifyServiceError = %v; want outcomeError (err %v)", got, err)
			}
			assertAPICode(t, err, "InvalidParameterValueException")
			if len(*warns) != 1 || (*warns)[0].Service != qsListTagsOp {
				t.Errorf("warnings = %v; want one under %s", *warns, qsListTagsOp)
			}
		})
	}
}

// An NXDOMAIN from the first phase with nothing stored is the region gap:
// returned as is, with no later phase called. Once rows are stored, a later
// NXDOMAIN, a tag read's or a listing's, is a failure of this scan.
func TestScanQuickSight_NXDOMAIN(t *testing.T) {
	nx := &net.DNSError{Err: "no such host", Name: "quicksight.us-east-1.amazonaws.com", IsNotFound: true}
	t.Run("first phase, nothing stored", func(t *testing.T) {
		st := newTestStore(t)
		var mu sync.Mutex
		var called []string
		h := map[string]func(any) (any, error){"DescribeAccountSubscription": qsSubscribed}
		for _, fx := range qsTaggedFixtures() {
			h[fx.op] = func(any) (any, error) {
				mu.Lock()
				called = append(called, fx.op)
				mu.Unlock()
				return nil, nx
			}
		}
		acct := &account{ID: testAccountID, Name: "Test Account", cfg: opStubConfig(h, qsNotServed)}

		_, _, err := scanQuickSight(context.Background(), acct, testRegion, st, testScanID)
		if got := classifyServiceError(err); got != outcomeUnavailable {
			t.Errorf("classifyServiceError = %v; want outcomeUnavailable (err %v)", got, err)
		}
		if !slices.Equal(called, []string{"ListDataSets"}) {
			t.Errorf("called %v; want only ListDataSets", called)
		}
	})
	for _, c := range []struct{ name, op, arn string }{
		{name: "tag read after rows stored", arn: qsTestARN("analysis", "tagged")},
		{name: "later listing after rows stored", op: "ListAnalyses"},
	} {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			f, h := newQSFixture()
			if c.arn != "" {
				f.tags.errs = map[string]error{c.arn: nx}
			} else {
				h[c.op] = func(any) (any, error) { return nil, nx }
			}
			acct := &account{ID: testAccountID, Name: "Test Account", cfg: opStubConfig(h, qsNotServed)}

			total, _, err := scanQuickSight(context.Background(), acct, testRegion, st, testScanID)
			if got := classifyServiceError(err); got != outcomeError {
				t.Errorf("classifyServiceError = %v; want outcomeError (err %v)", got, err)
			}
			if total == 0 {
				t.Error("total = 0; want the stored rows counted")
			}
		})
	}
}

// A member listing's NXDOMAIN is returned, never skipped as transient: after
// the parent phases stored rows the scan fails (resultLateNXDOMAIN); a member
// phase that stored nothing returns the NXDOMAIN as is, the region gap.
func TestScanQSMemberPhases_NXDOMAIN(t *testing.T) {
	nx := &net.DNSError{Err: "no such host", Name: "quicksight.us-east-1.amazonaws.com", IsNotFound: true}
	for _, p := range qsMemberPhases {
		t.Run(p.name+"/rows stored", func(t *testing.T) {
			st := newTestStore(t)
			warns := countOpWarnings(st, p.op)
			h := qsMemberTopLevelHandlers()
			h[strings.TrimPrefix(p.op, "quicksight:")] = func(any) (any, error) { return nil, nx }
			acct := &account{ID: testAccountID, Name: "Test Account", cfg: opStubConfig(h, qsNotServed)}

			total, _, err := scanQuickSight(context.Background(), acct, testRegion, st, testScanID)
			if got := classifyServiceError(err); got != outcomeError {
				t.Errorf("classifyServiceError = %v; want outcomeError (err %v)", got, err)
			}
			if total == 0 {
				t.Error("total = 0; want the stored rows counted")
			}
			if *warns != 0 {
				t.Errorf("recorded %d warnings; want 0: the NXDOMAIN is returned, not skipped", *warns)
			}
		})
		t.Run(p.name+"/nothing stored", func(t *testing.T) {
			st := newTestStore(t)
			warns := countOpWarnings(st, p.op)
			total, _, err := p.run(t, st, []string{"only"}, func(_, _ string) (*string, error) { return nil, nx })
			if got := classifyServiceError(err); got != outcomeUnavailable {
				t.Errorf("classifyServiceError = %v; want outcomeUnavailable (err %v)", got, err)
			}
			if total != 0 || *warns != 0 {
				t.Errorf("total, warnings = %d, %d; want 0, 0", total, *warns)
			}
		})
	}
}

// A failed write stops the scan: no later phase's op is called, and no tag
// denial of the lost rows is warned about.
func TestScanQuickSight_FailedWriteStopsAndSendsNoTagWarning(t *testing.T) {
	for _, c := range []struct{ kind, nextOp string }{
		{"analysis", "ListDashboards"},
		{"user", "ListApps"},
	} {
		t.Run(c.kind, func(t *testing.T) {
			st, closeStore := openClosingStore(t)
			warns := recordWarnings(st)
			f, h := newQSFixture()
			f.tags.errs = map[string]error{qsTestARN(c.kind, "bare"): qsTopLevelDeny}
			tagRead := h["ListTagsForResource"]
			h["ListTagsForResource"] = func(in any) (any, error) {
				if *in.(*quicksight.ListTagsForResourceInput).ResourceArn == qsTestARN(c.kind, "tagged") {
					closeStore()
				}
				return tagRead(in)
			}
			var later atomic.Bool
			next := h[c.nextOp]
			h[c.nextOp] = func(in any) (any, error) {
				later.Store(true)
				return next(in)
			}
			acct := &account{ID: testAccountID, Name: "Test Account", cfg: opStubConfig(h, qsNotServed)}

			_, _, err := scanQuickSight(context.Background(), acct, testRegion, st, testScanID)
			if !errors.Is(err, store.ErrStoreWrite) {
				t.Fatalf("err = %v; want store.ErrStoreWrite", err)
			}
			if later.Load() {
				t.Errorf("%s called after the write failed", c.nextOp)
			}
			if len(*warns) != 0 {
				t.Errorf("warnings = %v; want none (the denied rows were lost)", *warns)
			}
		})
	}
}

// A customization whose response carries its ARN is tag-read under it, and
// without tags stored with none; one without an ARN is stored under the
// synthesized NativeID without a tag read, which ListTagsForResource would
// reject.
func TestScanQSAccountCustomization_Tags(t *testing.T) {
	synthesized := fmt.Sprintf("arn:aws:quicksight:%s:%s:account-customization", testRegion, testAccountID)
	for _, c := range []struct {
		name, arn, wantNativeID string
		wantAsked               []string
	}{
		{"with ARN", qsTestCustomization, qsTestCustomization, []string{qsTestCustomization}},
		{"without ARN", "", synthesized, nil},
	} {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			tags := &qsTagCalls{}
			h := map[string]func(any) (any, error){
				"ListTagsForResource": tags.handler,
				"DescribeAccountCustomization": func(any) (any, error) {
					out := &quicksight.DescribeAccountCustomizationOutput{AccountCustomization: &qstypes.AccountCustomization{DefaultTheme: sdkaws.String("CLASSIC")}}
					if c.arn != "" {
						out.Arn = sdkaws.String(c.arn)
					}
					return out, nil
				},
			}
			client := quicksight.NewFromConfig(opStubConfig(h, qsNotServed))
			acct := newTestAccount(testAccountID)

			var p phaseTotals
			scanQSAccountCustomization(context.Background(), client, acct, testRegion, st, testScanID, &p)
			if _, _, err := p.result(st, acct, testRegion); err != nil || p.total != 1 {
				t.Fatalf("result = (%d, %v); want (1, nil)", p.total, err)
			}
			r, ok := rowsByNativeID(t, st, TypeQuickSightCustomization)[c.wantNativeID]
			if !ok || r.TagsJSON != nil {
				t.Errorf("row %s stored = %v, TagsJSON = %v; want stored without tags", c.wantNativeID, ok, r.TagsJSON)
			}
			if got := tags.asked(); !slices.Equal(got, c.wantAsked) {
				t.Errorf("ListTagsForResource ARNs = %v; want %v", got, c.wantAsked)
			}
		})
	}
}

// qsNamespacePhase is one per-namespace QuickSight phase: op answers a
// namespace's page with page(ns, id, next), run scans the namespaces, and
// nativeID is the row stored for item id of namespace ns.
type qsNamespacePhase struct {
	op, rtype string
	warnCodes []string
	page      func(ns, id string, next *string) any
	run       func(ctx context.Context, client quickSightAPI, st *store.Store, namespaces []childParent) (int, int, error)
	nativeID  func(ns, id string) string
}

func qsNamespacePhases() []qsNamespacePhase {
	acct := newTestAccount(testAccountID)
	return []qsNamespacePhase{
		{
			op: "ListGroups", rtype: TypeQuickSightGroup, warnCodes: []string{qsPrecondition, "ResourceUnavailableException"},
			page: func(ns, id string, next *string) any {
				return &quicksight.ListGroupsOutput{NextToken: next, GroupList: []qstypes.Group{{GroupName: sdkaws.String(id), Arn: sdkaws.String(qsTestARN("group", ns+"/"+id))}}}
			},
			run: func(ctx context.Context, client quickSightAPI, st *store.Store, namespaces []childParent) (int, int, error) {
				_, t, i, err := scanQSGroups(ctx, client, acct, testRegion, st, testScanID, namespaces)
				return t, i, err
			},
			nativeID: func(ns, id string) string { return qsTestARN("group", ns+"/"+id) },
		},
		{
			op: "ListUsers", rtype: TypeQuickSightUser, warnCodes: []string{qsPrecondition, "ResourceUnavailableException"},
			page: func(ns, id string, next *string) any {
				return &quicksight.ListUsersOutput{NextToken: next, UserList: []qstypes.User{{UserName: sdkaws.String(id), Arn: sdkaws.String(qsTestARN("user", ns+"/"+id))}}}
			},
			run: func(ctx context.Context, client quickSightAPI, st *store.Store, namespaces []childParent) (int, int, error) {
				return qsPhaseResult(st, func(p *phaseTotals) bool {
					return scanQSUsers(ctx, client, acct, testRegion, st, testScanID, namespaces, p)
				})
			},
			nativeID: func(ns, id string) string { return qsTestARN("user", ns+"/"+id) },
		},
		{
			op: "ListIAMPolicyAssignments", rtype: TypeQuickSightAssignment,
			page: func(_, id string, next *string) any {
				return &quicksight.ListIAMPolicyAssignmentsOutput{NextToken: next, IAMPolicyAssignments: []qstypes.IAMPolicyAssignmentSummary{{AssignmentName: sdkaws.String(id)}}}
			},
			run: func(ctx context.Context, client quickSightAPI, st *store.Store, namespaces []childParent) (int, int, error) {
				return scanQSAssignments(ctx, client, acct, testRegion, st, testScanID, namespaces)
			},
			nativeID: func(ns, id string) string { return qsTestARN("namespace", ns) + "/assignment/" + id },
		},
	}
}

func qsTestNamespaces(names ...string) []childParent {
	out := make([]childParent, len(names))
	for i, n := range names {
		out[i] = childParent{id: n, arn: qsTestARN("namespace", n)}
	}
	return out
}

// qsNamespaceInput reads the namespace, page token and page size of a
// per-namespace request.
func qsNamespaceInput(in any) (ns string, token *string, maxResults int32) {
	v := reflect.ValueOf(in).Elem()
	return *v.FieldByName("Namespace").Interface().(*string), v.FieldByName("NextToken").Interface().(*string),
		sdkaws.ToInt32(v.FieldByName("MaxResults").Interface().(*int32))
}

// Per namespace: a namespace QuickSight no longer finds is skipped, silently
// when nothing was read from it and with a warning otherwise; a first-page
// code of the phase's warnCodes skips the namespace with a warning; a denial
// warns; every other code, and a warned code on a later page, is returned.
// The namespaces listed before and after the failing one keep their rows,
// and every request asks for the model's page cap, 100.
func TestScanQSNamespacePhases_Failures(t *testing.T) {
	codes := append([]string{"ResourceNotFoundException"}, qsTestCodes...)
	for _, ph := range qsNamespacePhases() {
		for _, later := range []bool{false, true} {
			for _, code := range codes {
				name := ph.op + "/first page/" + code
				if later {
					name = ph.op + "/later page/" + code
				}
				t.Run(name, func(t *testing.T) {
					st := newTestStore(t)
					warns := countOpWarnings(st, "quicksight:"+ph.op)
					var sizes []int32
					h := map[string]func(any) (any, error){
						"ListTagsForResource": func(any) (any, error) { return &quicksight.ListTagsForResourceOutput{}, nil },
						ph.op: func(in any) (any, error) {
							ns, token, size := qsNamespaceInput(in)
							sizes = append(sizes, size)
							switch {
							case ns != "failing":
								return ph.page(ns, ns, nil), nil
							case token == nil && later:
								return ph.page(ns, "p1", sdkaws.String("p2")), nil
							}
							return nil, apiErr(code, "x")
						},
					}
					client := quicksight.NewFromConfig(opStubConfig(h, qsNotServed))

					total, _, err := ph.run(context.Background(), client, st, qsTestNamespaces("before", "failing", "after"))
					silent := code == "ResourceNotFoundException" || code == "AccessDeniedException" ||
						(!later && slices.Contains(ph.warnCodes, code))
					if silent && err != nil {
						t.Fatalf("err = %v; want nil", err)
					}
					if !silent && !isAPIErrorCode(err, code) {
						t.Fatalf("err = %v; want %s", err, code)
					}
					want := []string{ph.nativeID("before", "before"), ph.nativeID("after", "after")}
					if later {
						want = append(want, ph.nativeID("failing", "p1"))
					}
					rows := rowsByNativeID(t, st, ph.rtype)
					for _, id := range want {
						if _, ok := rows[id]; !ok {
							t.Errorf("row %s not stored", id)
						}
					}
					if len(rows) != len(want) || total != len(want) {
						t.Errorf("stored %d rows (total %d); want %d", len(rows), total, len(want))
					}
					wantWarns := 1
					if code == "ResourceNotFoundException" && !later || !silent {
						wantWarns = 0
					}
					if *warns != wantWarns {
						t.Errorf("recorded %d warnings under %s; want %d", *warns, ph.op, wantWarns)
					}
					if slices.ContainsFunc(sizes, func(n int32) bool { return n != 100 }) {
						t.Errorf("MaxResults = %v; want 100 on every request", sizes)
					}
				})
			}
		}
	}
}

// A per-namespace listing that repeats its NextToken stops with
// errRepeatedNextToken, keeping the rows read, and the later namespaces are
// still read.
func TestScanQSNamespacePhases_StopOnRepeatedToken(t *testing.T) {
	for _, ph := range qsNamespacePhases() {
		t.Run(ph.op, func(t *testing.T) {
			st := newTestStore(t)
			h := map[string]func(any) (any, error){
				"ListTagsForResource": func(any) (any, error) { return &quicksight.ListTagsForResourceOutput{}, nil },
				ph.op: func(in any) (any, error) {
					ns, token, _ := qsNamespaceInput(in)
					if ns != "looping" {
						return ph.page(ns, ns, nil), nil
					}
					return ph.page(ns, "p"+sv(token), sdkaws.String("again")), nil
				},
			}
			client := quicksight.NewFromConfig(opStubConfig(h, qsNotServed))

			total, _, err := ph.run(context.Background(), client, st, qsTestNamespaces("looping", "after"))
			if !errors.Is(err, errRepeatedNextToken) {
				t.Fatalf("err = %v; want errRepeatedNextToken", err)
			}
			if n := len(rowsByNativeID(t, st, ph.rtype)); n != 3 || total != 3 {
				t.Errorf("stored %d rows (total %d); want 3 (two looping pages and the later namespace)", n, total)
			}
		})
	}
}

// When ctx ends while a namespace is read, no further namespace is read, the
// users read are stored without a tag read, and the stop is one error.
func TestScanQSUsers_ContextStopIsOneError(t *testing.T) {
	st := newTestStore(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var asked []string
	h := map[string]func(context.Context, any) (any, error){
		"ListTagsForResource": func(context.Context, any) (any, error) {
			t.Error("ListTagsForResource called after the stop")
			return &quicksight.ListTagsForResourceOutput{}, nil
		},
		"ListUsers": func(ctx context.Context, in any) (any, error) {
			ns, _, _ := qsNamespaceInput(in)
			asked = append(asked, ns)
			if ns == "stopping" {
				cancel()
				return nil, ctx.Err()
			}
			return &quicksight.ListUsersOutput{UserList: []qstypes.User{{UserName: sdkaws.String(ns), Arn: sdkaws.String(qsTestARN("user", ns))}}}, nil
		},
	}
	client := quicksight.NewFromConfig(opStubConfigCtx(h, qsNotServed))

	var p phaseTotals
	scanQSUsers(ctx, client, newTestAccount(testAccountID), testRegion, st, testScanID, qsTestNamespaces("before", "stopping", "after"), &p)
	if len(p.errs) != 1 {
		t.Fatalf("phase errors = %d; want 1", len(p.errs))
	}
	joined, ok := p.errs[0].err.(interface{ Unwrap() []error })
	if !errors.Is(p.errs[0].err, context.Canceled) || !ok || len(joined.Unwrap()) != 1 {
		t.Errorf("err = %v; want one context.Canceled member", p.errs[0].err)
	}
	if !slices.Equal(asked, []string{"before", "stopping"}) {
		t.Errorf("ListUsers namespaces = %v; want [before stopping]", asked)
	}
	if _, ok := rowsByNativeID(t, st, TypeQuickSightUser)[qsTestARN("user", "before")]; !ok || p.total != 1 {
		t.Errorf("before's user stored = %v, total = %d; want stored, 1", ok, p.total)
	}
}

// A user without a UserName is stored under its ARN as its name; the
// namespace names it only in the warning about a user without an ARN.
func TestScanQSUsers_NameFallsBackToARN(t *testing.T) {
	st := newTestStore(t)
	warnings := recordWarnings(st)
	arn := qsTestARN("user", "default/anon")
	h := map[string]func(any) (any, error){
		"ListTagsForResource": func(any) (any, error) { return &quicksight.ListTagsForResourceOutput{}, nil },
		"ListUsers": func(any) (any, error) {
			return &quicksight.ListUsersOutput{UserList: []qstypes.User{{Arn: sdkaws.String(arn)}, {}}}, nil
		},
	}
	client := quicksight.NewFromConfig(opStubConfig(h, qsNotServed))
	if _, _, err := qsPhaseResult(st, func(p *phaseTotals) bool {
		return scanQSUsers(context.Background(), client, newTestAccount(testAccountID), testRegion, st, testScanID, qsTestNamespaces("default"), p)
	}); err != nil {
		t.Fatalf("scanQSUsers: %v", err)
	}
	if got := sv(rowsByNativeID(t, st, TypeQuickSightUser)[arn].Name); got != arn {
		t.Errorf("Name = %q; want the ARN", got)
	}
	if len(*warnings) != 1 || !strings.Contains((*warnings)[0].Message, "a user in namespace default") {
		t.Errorf("warnings = %v; want one naming the ARN-less user by its namespace", *warnings)
	}
}

// A user QuickSight no longer finds when its tags are read was deleted since
// it was listed, and is not stored.
func TestScanQSUsers_TagReadNotFoundDropsUser(t *testing.T) {
	st := newTestStore(t)
	gone, kept := qsTestARN("user", "default/gone"), qsTestARN("user", "default/kept")
	tags := &qsTagCalls{errs: map[string]error{gone: apiErr("ResourceNotFoundException", "x")}}
	h := map[string]func(any) (any, error){
		"ListTagsForResource": tags.handler,
		"ListUsers": func(any) (any, error) {
			return &quicksight.ListUsersOutput{UserList: []qstypes.User{
				{UserName: sdkaws.String("gone"), Arn: sdkaws.String(gone)},
				{UserName: sdkaws.String("kept"), Arn: sdkaws.String(kept)},
			}}, nil
		},
	}
	client := quicksight.NewFromConfig(opStubConfig(h, qsNotServed))
	total, _, err := qsPhaseResult(st, func(p *phaseTotals) bool {
		return scanQSUsers(context.Background(), client, newTestAccount(testAccountID), testRegion, st, testScanID, qsTestNamespaces("default"), p)
	})
	if err != nil || total != 1 {
		t.Fatalf("scanQSUsers = (%d, %v); want (1, nil)", total, err)
	}
	rows := rowsByNativeID(t, st, TypeQuickSightUser)
	if _, ok := rows[gone]; ok {
		t.Error("the user not found by its tag read was stored")
	}
	if _, ok := rows[kept]; !ok {
		t.Error("the other user was not stored")
	}
}

// qsTaggedSkipCodes is, per tagged listing op, the first-page codes it
// skips silently: those of its op's model that say the account cannot use
// it, and the code ListActionConnectors was seen to answer where the
// feature is not deployed.
var qsTaggedSkipCodes = map[string][]string{
	"ListNamespaces":              {qsPrecondition, qsNoSubscription},
	"ListAnalyses":                {qsEditionGap},
	"ListDashboards":              {qsEditionGap},
	"ListFolders":                 {qsNoSubscription, qsEditionGap},
	"ListTemplates":               {qsNoSubscription, qsEditionGap},
	"ListThemes":                  {qsNoSubscription, qsEditionGap},
	"ListVPCConnections":          {qsEditionGap},
	"ListCustomPermissions":       {qsPrecondition, qsNoSubscription},
	"ListActionConnectors":        {"InternalFailure"},
	"ListBrands":                  {qsFeatureOff},
	"ListOAuthClientApplications": {qsNoSubscription},
	"ListDlpSettings":             {qsFeatureOff},
}

// Each tagged account-level listing skips silently only its own first-page
// codes, warns about a denial, and returns every other code; on a later
// page only a denial is not returned, and the rows read before it are kept.
func TestScanQuickSight_TaggedListErrors(t *testing.T) {
	codes := append([]string{"InternalFailure"}, qsTestCodes...)
	for _, fx := range qsTaggedFixtures() {
		if fx.page == nil {
			continue
		}
		for _, later := range []bool{false, true} {
			for _, code := range codes {
				name := fx.op + "/first page/" + code
				if later {
					name = fx.op + "/later page/" + code
				}
				t.Run(name, func(t *testing.T) {
					st := newTestStore(t)
					warns := countOpWarnings(st, "quicksight:"+fx.op)
					_, h := newQSFixture()
					h[fx.op] = func(in any) (any, error) {
						if !later || reflect.ValueOf(in).Elem().FieldByName("NextToken").Interface().(*string) != nil {
							return nil, apiErr(code, "x")
						}
						out := fx.page(qsFixtureIDs)
						reflect.ValueOf(out).Elem().FieldByName("NextToken").Set(reflect.ValueOf(sdkaws.String("p2")))
						return out, nil
					}
					acct := &account{ID: testAccountID, Name: "Test Account", cfg: opStubConfig(h, qsNotServed)}

					_, _, err := scanQuickSight(context.Background(), acct, testRegion, st, testScanID)
					denied := code == "AccessDeniedException"
					skipped := !later && slices.Contains(qsTaggedSkipCodes[fx.op], code)
					if denied || skipped {
						if err != nil {
							t.Fatalf("err = %v; want nil", err)
						}
					} else if !isAPIErrorCode(err, code) {
						t.Fatalf("err = %v; want %s", err, code)
					}
					wantRows := 0
					if later {
						wantRows = len(qsFixtureIDs)
					}
					if n := len(rowsByNativeID(t, st, fx.typ)); n != wantRows {
						t.Errorf("stored %d %s rows; want %d", n, fx.typ, wantRows)
					}
					if want := map[bool]int{true: 1, false: 0}[denied]; *warns != want {
						t.Errorf("recorded %d warnings under %s; want %d", *warns, fx.op, want)
					}
				})
			}
		}
	}
}

// fakeQSActionConnectors answers ListActionConnectors with one empty page
// and records the retry budget its per-call options leave.
type fakeQSActionConnectors struct {
	quickSightAPI
	maxAttempts *int
}

func (f fakeQSActionConnectors) ListActionConnectors(_ context.Context, _ *quicksight.ListActionConnectorsInput, optFns ...func(*quicksight.Options)) (*quicksight.ListActionConnectorsOutput, error) {
	o := quicksight.Options{RetryMaxAttempts: 10}
	for _, fn := range optFns {
		fn(&o)
	}
	*f.maxAttempts = o.RetryMaxAttempts
	return &quicksight.ListActionConnectorsOutput{}, nil
}

// ListActionConnectors answers a region without the feature with a 500
// InternalFailure the global retry budget would spend minutes on, so each
// call is held to two attempts.
func TestScanQSActionConnectors_ClampsRetries(t *testing.T) {
	var got int
	var p phaseTotals
	scanQSActionConnectors(context.Background(), fakeQSActionConnectors{maxAttempts: &got}, newTestAccount(testAccountID), testRegion, newTestStore(t), testScanID, &p)
	if got != 2 {
		t.Errorf("RetryMaxAttempts = %d; want 2", got)
	}
}

// An alias listing that repeats its NextToken stops with
// errRepeatedNextToken; the aliases read are stored.
func TestScanQSAliases_StopOnRepeatedToken(t *testing.T) {
	ctx, acct := context.Background(), newTestAccount(testAccountID)
	cases := []struct {
		name, parentType, kind, childType string
		run                               func(st *store.Store, parents []childParent) (int, int, error)
	}{
		{"template aliases", TypeQuickSightTemplate, "template", TypeQuickSightTemplateAlias, func(st *store.Store, parents []childParent) (int, int, error) {
			fake := fakeQSChildren{templateAliases: map[string]map[string]*quicksight.ListTemplateAliasesOutput{"t1": {
				"":   {TemplateAliasList: []qstypes.TemplateAlias{templateAlias("t1", "a", 1)}, NextToken: sdkaws.String("p2")},
				"p2": {TemplateAliasList: []qstypes.TemplateAlias{templateAlias("t1", "b", 1)}, NextToken: sdkaws.String("p2")},
			}}}
			return scanQSTemplateAliases(ctx, fake, acct, testRegion, st, testScanID, parents)
		}},
		{"theme aliases", TypeQuickSightTheme, "theme", TypeQuickSightThemeAlias, func(st *store.Store, parents []childParent) (int, int, error) {
			fake := fakeQSChildren{themeAliases: map[string]map[string]*quicksight.ListThemeAliasesOutput{"t1": {
				"":   {ThemeAliasList: []qstypes.ThemeAlias{themeAlias("t1", "a")}, NextToken: sdkaws.String("p2")},
				"p2": {ThemeAliasList: []qstypes.ThemeAlias{themeAlias("t1", "b")}, NextToken: sdkaws.String("p2")},
			}}}
			return scanQSThemeAliases(ctx, fake, acct, testRegion, st, testScanID, parents)
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			total, _, err := c.run(st, seedQSParents(t, st, c.parentType, c.kind, "t1"))
			if !errors.Is(err, errRepeatedNextToken) {
				t.Fatalf("err = %v; want errRepeatedNextToken", err)
			}
			if n := len(qsRows(t, st, c.childType)); n != 2 || total != 2 {
				t.Errorf("stored %d aliases (total %d); want 2", n, total)
			}
		})
	}
}

// Each stored data set's refresh schedules are listed; a data set QuickSight
// no longer finds is skipped silently, a denial warns once, and any other
// code is returned with the other data sets' schedules stored.
func TestScanQSRefreshSchedules_DataSetFailures(t *testing.T) {
	codes := append([]string{"LimitExceededException"}, qsTestCodes...)
	for _, code := range append(codes, "ResourceNotFoundException") {
		t.Run(code, func(t *testing.T) {
			st := newTestStore(t)
			warns := countOpWarnings(st, "quicksight:ListRefreshSchedules")
			h := map[string]func(any) (any, error){
				"ListRefreshSchedules": func(in any) (any, error) {
					ds := *in.(*quicksight.ListRefreshSchedulesInput).DataSetId
					if ds == "failing" {
						return nil, apiErr(code, "x")
					}
					return &quicksight.ListRefreshSchedulesOutput{RefreshSchedules: []qstypes.RefreshSchedule{
						{ScheduleId: sdkaws.String("s-" + ds), Arn: sdkaws.String(qsTestARN("dataset", ds) + "/refresh-schedule/s-" + ds)},
					}}, nil
				},
			}
			client := quicksight.NewFromConfig(opStubConfig(h, qsNotServed))

			total, _, err := scanQSRefreshSchedules(context.Background(), client, newTestAccount(testAccountID), testRegion, st, testScanID,
				[]childParent{{id: "before"}, {id: "failing"}, {id: "after"}})
			silent := code == "ResourceNotFoundException" || code == "AccessDeniedException"
			if silent && err != nil {
				t.Fatalf("err = %v; want nil", err)
			}
			if !silent && !isAPIErrorCode(err, code) {
				t.Fatalf("err = %v; want %s", err, code)
			}
			if n := len(qsRows(t, st, TypeQuickSightRefreshSchedule)); n != 2 || total != 2 {
				t.Errorf("stored %d schedules (total %d); want 2", n, total)
			}
			if want := map[bool]int{true: 1, false: 0}[code == "AccessDeniedException"]; *warns != want {
				t.Errorf("recorded %d warnings; want %d", *warns, want)
			}
		})
	}
}

// The account settings and customization singletons skip silently only a
// not-found (the account has none) and an op the region does not serve; a
// denial warns; any other code is returned.
func TestScanQSAccountSingletons_Errors(t *testing.T) {
	runs := map[string]func(ctx context.Context, client quickSightAPI, st *store.Store) (int, int, error){
		"DescribeAccountSettings": func(ctx context.Context, client quickSightAPI, st *store.Store) (int, int, error) {
			return scanQSAccountSettings(ctx, client, newTestAccount(testAccountID), testRegion, st, testScanID)
		},
		"DescribeAccountCustomization": func(ctx context.Context, client quickSightAPI, st *store.Store) (int, int, error) {
			return qsPhaseResult(st, func(p *phaseTotals) bool {
				return scanQSAccountCustomization(ctx, client, newTestAccount(testAccountID), testRegion, st, testScanID, p)
			})
		},
	}
	for op, run := range runs {
		for _, code := range append([]string{"ResourceNotFoundException", "UnknownOperationException"}, qsTestCodes...) {
			t.Run(op+"/"+code, func(t *testing.T) {
				st := newTestStore(t)
				warns := countOpWarnings(st, "quicksight:"+op)
				h := map[string]func(any) (any, error){op: func(any) (any, error) { return nil, apiErr(code, "x") }}
				_, _, err := run(context.Background(), quicksight.NewFromConfig(opStubConfig(h, qsNotServed)), st)
				silent := code == "ResourceNotFoundException" || code == "UnknownOperationException" || code == "AccessDeniedException"
				if silent && err != nil {
					t.Fatalf("err = %v; want nil", err)
				}
				if !silent && !isAPIErrorCode(err, code) {
					t.Fatalf("err = %v; want %s", err, code)
				}
				if want := map[bool]int{true: 1, false: 0}[code == "AccessDeniedException"]; *warns != want {
					t.Errorf("recorded %d warnings; want %d", *warns, want)
				}
			})
		}
	}
}

// qsSubscribed answers DescribeAccountSubscription for a subscribed account.
func qsSubscribed(any) (any, error) {
	return &quicksight.DescribeAccountSubscriptionOutput{AccountInfo: &qstypes.AccountInfo{
		AccountName: sdkaws.String("acme"), Edition: qstypes.EditionEnterprise, AccountSubscriptionStatus: sdkaws.String("ACCOUNT_CREATED"),
	}}, nil
}

// The subscription gate runs before every phase. A "not subscribed" answer
// is confirmed by one ListDataSets probe (MaxResults 1): only a probe that
// is not found either disables the service, and any other probe answer runs
// the phases without a warning. An op the region does not serve is
// unavailable, and a transient gate error and an NXDOMAIN are returned, all
// with no phase called; a denied gate warns and is decided by the probe, as
// a "not subscribed" one is; any other gate error is returned beside what the
// phases stored.
func TestScanQuickSight_SubscriptionGate(t *testing.T) {
	// qsGateNoError marks a case whose scan returns no error.
	const qsGateNoError serviceOutcome = -1
	notFound := func(any) (any, error) { return nil, apiErr("ResourceNotFoundException", "directory not found") }
	noAccount := func(any) (any, error) { return &quicksight.DescribeAccountSubscriptionOutput{}, nil }
	probeOK := func(any) (any, error) { return &quicksight.ListDataSetsOutput{}, nil }
	qsGateDenied := func(any) (any, error) {
		return nil, apiErr("AccessDeniedException", "User: x is not authorized to perform: quicksight:DescribeAccountSubscription")
	}
	cases := []struct {
		name          string
		answer, probe func(any) (any, error)
		wantOutcome   serviceOutcome
		wantPhases    bool
		wantWarns     int
	}{
		{"subscribed", qsSubscribed, nil, qsGateNoError, true, 0},
		{"not subscribed, confirmed", notFound, notFound, outcomeDisabled, false, 0},
		{"no account in the response, confirmed", noAccount, notFound, outcomeDisabled, false, 0},
		{"not subscribed, data sets listed", notFound, probeOK, qsGateNoError, true, 0},
		{"no account in the response, data sets listed", noAccount, probeOK, qsGateNoError, true, 0},
		{"not subscribed, probe denied", notFound, func(any) (any, error) {
			return nil, apiErr("AccessDeniedException", "User: x is not authorized to perform: quicksight:ListDataSets")
		}, qsGateNoError, true, 0},
		{"not subscribed, probe throttled", notFound, func(any) (any, error) { return nil, apiErr("ThrottlingException", "slow down") }, qsGateNoError, true, 0},
		{"op not served in the region", func(any) (any, error) { return nil, apiErr("UnknownOperationException", "x") }, nil, outcomeUnavailable, false, 0},
		{"transient", func(any) (any, error) { return nil, apiErr("ThrottlingException", "slow down") }, nil, outcomeTransient, false, 0},
		{"denied, data sets listed", qsGateDenied, probeOK, qsGateNoError, true, 1},
		{"denied, not subscribed", qsGateDenied, notFound, outcomeDisabled, false, 1},
		{"NXDOMAIN", func(any) (any, error) {
			return nil, &net.DNSError{Err: "no such host", Name: "quicksight.us-east-1.amazonaws.com", IsNotFound: true}
		}, nil, outcomeUnavailable, false, 0},
		{"other error", func(any) (any, error) { return nil, apiErr("InvalidParameterValueException", "x") }, nil, outcomeError, true, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			warnings := recordWarnings(st)
			var (
				phaseCalls atomic.Int32
				mu         sync.Mutex
				probes     []*quicksight.ListDataSetsInput
			)
			h := map[string]func(any) (any, error){"DescribeAccountSubscription": c.answer}
			for op, handle := range qsTopLevelHandlers() {
				if op == "DescribeAccountSubscription" {
					continue
				}
				h[op] = func(in any) (any, error) {
					if ds, ok := in.(*quicksight.ListDataSetsInput); ok && sdkaws.ToInt32(ds.MaxResults) == 1 {
						mu.Lock()
						probes = append(probes, ds)
						mu.Unlock()
						if c.probe == nil {
							t.Error("ListDataSets probe sent after a subscribed answer")
							return handle(in)
						}
						return c.probe(in)
					}
					phaseCalls.Add(1)
					return handle(in)
				}
			}
			acct := &account{ID: testAccountID, Name: "Test Account", cfg: opStubConfig(h, qsNotServed)}

			total, _, err := scanQuickSight(context.Background(), acct, testRegion, st, testScanID)
			if c.wantOutcome == qsGateNoError {
				if err != nil {
					t.Errorf("err = %v; want nil", err)
				}
			} else if got := classifyServiceError(err); got != c.wantOutcome {
				t.Errorf("outcome = %v; want %v (err %v)", got, c.wantOutcome, err)
			}
			if c.wantOutcome == outcomeError && !isAPIErrorCode(err, "InvalidParameterValueException") {
				t.Errorf("err = %v; want the gate's InvalidParameterValueException", err)
			}
			if got := phaseCalls.Load() > 0; got != c.wantPhases {
				t.Errorf("phases called = %v; want %v", got, c.wantPhases)
			}
			if c.wantPhases != (total > 0) {
				t.Errorf("total = %d; want rows stored = %v", total, c.wantPhases)
			}
			if c.probe != nil && (len(probes) != 1 || sv(probes[0].AwsAccountId) != testAccountID) {
				t.Errorf("probes = %d; want one ListDataSets(AwsAccountId %s, MaxResults 1)", len(probes), testAccountID)
			}
			if len(*warnings) != c.wantWarns {
				t.Errorf("warnings = %v; want %d", *warnings, c.wantWarns)
			}
			for _, w := range *warnings {
				if w.Service != qsDescribeSubscription {
					t.Errorf("warning under %s; want %s", w.Service, qsDescribeSubscription)
				}
			}
		})
	}
}

// A member listing whose later page fails keeps the members read before it
// and returns the error.
func TestScanQSMemberPhases_FailedPageKeepsEarlierPages(t *testing.T) {
	for _, p := range qsMemberPhases {
		t.Run(p.name, func(t *testing.T) {
			st := newTestStore(t)
			total, _, err := p.run(t, st, []string{"only"}, func(_, token string) (*string, error) {
				if token == "" {
					return sdkaws.String("p2"), nil
				}
				return nil, apiErr("ValidationException", "bad")
			})
			if !isAPIErrorCode(err, "ValidationException") {
				t.Fatalf("err = %v; want the later page's ValidationException", err)
			}
			if n := len(qsRows(t, st, p.childType)); n != 1 || total != 1 {
				t.Errorf("stored %d rows (total %d); want the first page's 1", n, total)
			}
		})
	}
}

// A member phase whose write fails sends none of its warnings, the
// transient-skip one included: none may claim rows the write lost.
func TestScanQSMemberPhases_NoWarningAfterFailedWrite(t *testing.T) {
	for _, p := range qsMemberPhases {
		t.Run(p.name, func(t *testing.T) {
			st, closeStore := openClosingStore(t)
			warns := countOpWarnings(st, p.op)
			_, _, err := p.run(t, st, []string{"slow", "ok"}, func(parent, _ string) (*string, error) {
				closeStore()
				if strings.HasSuffix(parent, "slow") {
					return nil, apiErr("ThrottlingException", "slow down")
				}
				return nil, nil
			})
			if !errors.Is(err, store.ErrStoreWrite) {
				t.Fatalf("err = %v; want the failed write", err)
			}
			if *warns != 0 {
				t.Errorf("recorded %d warnings after the failed write; want 0", *warns)
			}
		})
	}
}

// A parent deleted between its pages keeps its first page and warns once.
func TestScanQSMemberPhases_ParentGoneMidListingWarns(t *testing.T) {
	for _, p := range qsMemberPhases {
		t.Run(p.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := countOpWarnings(st, p.op)
			_, _, err := p.run(t, st, []string{"gone"}, func(_, token string) (*string, error) {
				if token == "" {
					return sdkaws.String("p2"), nil
				}
				return nil, apiErr("ResourceNotFoundException", "folder not found")
			})
			if err != nil {
				t.Fatalf("err = %v; want nil", err)
			}
			if got := len(qsRows(t, st, p.childType)); got != 1 {
				t.Errorf("stored %d %s rows; want the first page's 1", got, p.childType)
			}
			if *warns != 1 {
				t.Errorf("recorded %d warnings; want 1 for the vanished parent", *warns)
			}
		})
	}
}

// A failed closure write after the rows were stored sends none of the
// warnings: not the transient skip, not the denied parent.
func TestScanQSMemberPhases_NoWarningAfterFailedClosure(t *testing.T) {
	for _, p := range qsMemberPhases {
		t.Run(p.name, func(t *testing.T) {
			st := newTestStore(t)
			refuseClosureWrites(t, st)
			warns := countOpWarnings(st, p.op)
			_, _, err := p.run(t, st, []string{"slow", "denied", "ok"}, func(parent, _ string) (*string, error) {
				switch {
				case strings.HasSuffix(parent, "slow"):
					return nil, apiErr("ThrottlingException", "slow down")
				case strings.HasSuffix(parent, "denied"):
					return nil, apiErr("AccessDeniedException", "User: x is not authorized to perform: "+p.op)
				}
				return nil, nil
			})
			if err == nil {
				t.Fatal("err = nil; want the failed closure write")
			}
			if n := countStoredRows(t, st); n == 0 {
				t.Fatal("no rows stored; want the ok parent's rows written before the closure")
			}
			if *warns != 0 {
				t.Errorf("recorded %d warnings after the failed closure write; want 0", *warns)
			}
		})
	}
}

// qsStopMembers counts the members of err's joins that are stop.
func qsStopMembers(err, stop error) int {
	if members, ok := err.(interface{ Unwrap() []error }); ok {
		n := 0
		for _, m := range members.Unwrap() {
			n += qsStopMembers(m, stop)
		}
		return n
	}
	if err != nil && errors.Is(err, stop) {
		return 1
	}
	return 0
}

// When ctx ends the scan reports the stop once, whichever phases were in
// flight and however many were not run; no phase starts after it; and a
// hard error that shared a join with an in-flight stop survives. A ctx that
// ends before or during the gate, or while its probe runs, is one stop
// with no phase called, no warning and no service disabled, whatever code
// the cut-short call answered.
func TestScanQuickSight_ContextStopIsOneError(t *testing.T) {
	t.Run("in flight beside a hard error", func(t *testing.T) {
		st := newTestStore(t)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		h := qsMemberTopLevelHandlers()
		h["ListFolders"] = func(any) (any, error) {
			return &quicksight.ListFoldersOutput{FolderSummaryList: []qstypes.FolderSummary{
				{FolderId: sdkaws.String("failing"), Arn: sdkaws.String(qsTestARN("folder", "failing"))},
				{FolderId: sdkaws.String("stopping"), Arn: sdkaws.String(qsTestARN("folder", "stopping"))},
			}}, nil
		}
		h["ListFolderMembers"] = func(in any) (any, error) {
			if *in.(*quicksight.ListFolderMembersInput).FolderId == "failing" {
				return nil, apiErr("ValidationException", "bad")
			}
			cancel()
			return nil, context.Canceled
		}
		acct := &account{ID: testAccountID, Name: "Test Account", cfg: opStubConfig(h, qsNotServed)}

		_, _, err := scanQuickSight(ctx, acct, testRegion, st, testScanID)
		if n := qsStopMembers(err, context.Canceled); n != 1 {
			t.Errorf("stop members = %d; want 1 (err %v)", n, err)
		}
		if !isAPIErrorCode(err, "ValidationException") {
			t.Errorf("err = %v; want the failing folder's ValidationException kept", err)
		}
	})
	t.Run("no phase starts after the stop", func(t *testing.T) {
		st := newTestStore(t)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		h := qsMemberTopLevelHandlers()
		// The stop is reported twice in flight, by the second page and by
		// the tag reads of the first page's analyses.
		h["ListAnalyses"] = func(in any) (any, error) {
			if in.(*quicksight.ListAnalysesInput).NextToken == nil {
				return &quicksight.ListAnalysesOutput{
					AnalysisSummaryList: []qstypes.AnalysisSummary{{AnalysisId: sdkaws.String("a1"), Arn: sdkaws.String(qsTestARN("analysis", "a1"))}},
					NextToken:           sdkaws.String("p2"),
				}, nil
			}
			cancel()
			return nil, context.Canceled
		}
		var later atomic.Bool
		h["ListDashboards"] = func(any) (any, error) {
			later.Store(true)
			return &quicksight.ListDashboardsOutput{}, nil
		}
		acct := &account{ID: testAccountID, Name: "Test Account", cfg: opStubConfig(h, qsNotServed)}

		_, _, err := scanQuickSight(ctx, acct, testRegion, st, testScanID)
		if n := qsStopMembers(err, context.Canceled); n != 1 {
			t.Errorf("stop members = %d; want 1 (err %v)", n, err)
		}
		if later.Load() {
			t.Error("ListDashboards called after the stop")
		}
	})
	notFound := func(context.Context, func()) (any, error) {
		return nil, apiErr("ResourceNotFoundException", "directory not found")
	}
	for _, c := range []struct {
		name          string
		precancel     bool
		answer, probe func(ctx context.Context, cancel func()) (any, error)
	}{
		{"before the gate", true, func(ctx context.Context, _ func()) (any, error) { return nil, ctx.Err() }, nil},
		{"during the gate, answered with a denial", false, func(_ context.Context, cancel func()) (any, error) {
			cancel()
			return nil, apiErr("AccessDeniedException", "User: x is not authorized to perform: quicksight:DescribeAccountSubscription")
		}, notFound},
		{"during the probe, answered not found", false, notFound, func(_ context.Context, cancel func()) (any, error) {
			cancel()
			return nil, apiErr("ResourceNotFoundException", "directory not found")
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			warnings := recordWarnings(st)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if c.precancel {
				cancel()
			}
			var phaseCalls atomic.Int32
			h := map[string]func(context.Context, any) (any, error){
				"DescribeAccountSubscription": func(ctx context.Context, _ any) (any, error) { return c.answer(ctx, cancel) },
			}
			for op, handle := range qsTopLevelHandlers() {
				if op == "DescribeAccountSubscription" {
					continue
				}
				h[op] = func(ctx context.Context, in any) (any, error) {
					if ds, ok := in.(*quicksight.ListDataSetsInput); ok && sdkaws.ToInt32(ds.MaxResults) == 1 && c.probe != nil {
						return c.probe(ctx, cancel)
					}
					phaseCalls.Add(1)
					return handle(in)
				}
			}
			acct := &account{ID: testAccountID, Name: "Test Account", cfg: opStubConfigCtx(h, qsNotServed)}

			_, _, err := scanQuickSight(ctx, acct, testRegion, st, testScanID)
			if n := qsStopMembers(err, context.Canceled); n != 1 {
				t.Errorf("stop members = %d; want 1 (err %v)", n, err)
			}
			if errors.Is(err, errServiceDisabled) {
				t.Errorf("err = %v; want the stop, not the service disabled", err)
			}
			if phaseCalls.Load() != 0 {
				t.Errorf("%d phase calls; want none", phaseCalls.Load())
			}
			if len(*warnings) != 0 {
				t.Errorf("warnings = %v; want none", *warnings)
			}
		})
	}
}

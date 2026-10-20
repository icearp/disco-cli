package aws

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/quicksight"
	qstypes "github.com/aws/aws-sdk-go-v2/service/quicksight/types"
	smithyhttp "github.com/aws/smithy-go/transport/http"
	"github.com/icearp/disco-cli/internal/util"
	"github.com/icearp/disco-cli/store"
)

// TestQSSoftSkip covers the ListAgents region gap: newer QuickSight (Q) ops
// return a 404 HTML body the SDK can't map to a typed code, so qsSoftSkip falls
// back to the HTTP status. Access-denied and the existing typed codes still
// soft-skip; an unrelated error does not.
func TestQSSoftSkip(t *testing.T) {
	resp404 := &smithyhttp.ResponseError{
		Response: &smithyhttp.Response{Response: &http.Response{StatusCode: 404}},
		Err:      apiErr("UnknownError", "deserialization failed"),
	}
	if !qsSoftSkip(resp404) {
		t.Error("404 HTML response (ListAgents region gap) should soft-skip")
	}
	if !qsSoftSkip(apiErr("AccessDeniedException", "denied")) {
		t.Error("access-denied should soft-skip")
	}
	if !qsSoftSkip(apiErr("UnsupportedUserEditionException", "")) {
		t.Error("existing typed code should soft-skip")
	}
	if qsSoftSkip(apiErr("ValidationException", "bad input")) {
		t.Error("unrelated error must not soft-skip")
	}
}

// fakeQSChildren serves the QuickSight child list ops from per-parent pages
// keyed by NextToken ("" is the first page); a parent listed in errs answers
// with that error instead.
type fakeQSChildren struct {
	quickSightAPI
	templateAliases map[string]map[string]*quicksight.ListTemplateAliasesOutput
	themeAliases    map[string]map[string]*quicksight.ListThemeAliasesOutput
	topicSchedules  map[string]*quicksight.ListTopicRefreshSchedulesOutput
	errs            map[string]error
}

func (f fakeQSChildren) ListTemplateAliases(_ context.Context, in *quicksight.ListTemplateAliasesInput, _ ...func(*quicksight.Options)) (*quicksight.ListTemplateAliasesOutput, error) {
	if err := f.errs[*in.TemplateId]; err != nil {
		return nil, err
	}
	return f.templateAliases[*in.TemplateId][sv(in.NextToken)], nil
}

func (f fakeQSChildren) ListThemeAliases(_ context.Context, in *quicksight.ListThemeAliasesInput, _ ...func(*quicksight.Options)) (*quicksight.ListThemeAliasesOutput, error) {
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
// two listing one child each.
func TestScanQSChildren_ErrorHandling(t *testing.T) {
	cases := []struct {
		name      string
		err       error
		wantErr   bool
		wantRows  int
		wantWarns int
	}{
		{"parent deleted since listing is skipped", apiErr("ResourceNotFoundException", "gone"), false, 2, 0},
		{"edition gap is skipped silently", apiErr("UnsupportedUserEditionException", "standard edition"), false, 2, 0},
		{"invalid parameter is skipped silently", apiErr("InvalidParameterValueException", "x"), false, 2, 0},
		{"access denied skips the parent with a warning", apiErr("AccessDeniedException", "User: x is not authorized to perform: quicksight:List"), false, 2, 1},
		{"other error propagates", apiErr("InternalFailureException", "boom"), true, 0, 0},
	}
	for _, op := range qsChildOps {
		for _, tc := range cases {
			t.Run(op.name+"/"+tc.name, func(t *testing.T) {
				st := newTestStore(t)
				var warned []string
				st.OnWarn = func(w store.ScanWarning) { warned = append(warned, w.Service) }
				parents := seedQSParents(t, st, op.parentType, op.kind, "bad", "ok1", "ok2")
				err := op.run(st, parents, map[string]error{"bad": tc.err})
				if tc.wantErr {
					if !isAPIErrorCode(err, "InternalFailureException") {
						t.Fatalf("err = %v; want the InternalFailureException propagated", err)
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

func TestScanQSThemeAliases_PaginatesAndSkipsInvalidParameter(t *testing.T) {
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
		errs: map[string]error{"bad": apiErr("InvalidParameterValueException", "x")},
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
	if *warns != 0 {
		t.Errorf("recorded %d warnings; want 0 (InvalidParameterValue is a silent skip)", *warns)
	}
	assertQSContains(t, st, parents[0].arn, qsTestARN("theme", "th1")+"/alias/prod", qsTestARN("theme", "th1")+"/alias/dev")
}

func TestScanQSThemes_HandsOnlyOwnThemesToAliasPhase(t *testing.T) {
	st := newTestStore(t)
	stub := stubResponses(t, map[string][]stubCall{
		"ListThemes": {{Output: &quicksight.ListThemesOutput{ThemeSummaryList: []qstypes.ThemeSummary{
			{ThemeId: sdkaws.String("mine"), Arn: sdkaws.String(qsTestARN("theme", "mine")), Name: sdkaws.String("Mine")},
			{ThemeId: sdkaws.String("CLASSIC"), Arn: sdkaws.String("arn:aws:quicksight::aws:theme/CLASSIC"), Name: sdkaws.String("Classic")},
		}}}},
	})
	client := quicksight.NewFromConfig(cfgWithStub(stub, testRegion))

	parents, total, _, err := scanQSThemes(context.Background(), client, newTestAccount(testAccountID), testRegion, st, testScanID)
	if err != nil {
		t.Fatalf("scanQSThemes: %v", err)
	}
	if total != 2 {
		t.Errorf("total = %d; want 2 (the starter theme is still stored)", total)
	}
	want := []childParent{{id: "mine", arn: qsTestARN("theme", "mine")}}
	if !slices.Equal(parents, want) {
		t.Errorf("parents = %v; want %v (starter themes have no account aliases)", parents, want)
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
		name    string
		call    stubCall
		wantErr bool
		wantRow bool
	}{
		{"registered key is stored", stubCall{Output: registered}, false, true},
		{"no registered key stores nothing", stubCall{Output: &quicksight.DescribeKeyRegistrationOutput{AwsAccountId: sdkaws.String(testAccountID)}}, false, false},
		{"AWS-owned Q data key alone stores nothing", stubCall{Output: &quicksight.DescribeKeyRegistrationOutput{
			QDataKey: &qstypes.QDataKey{QDataKeyType: qstypes.QDataKeyTypeAwsOwned},
		}}, false, false},
		{"CMK Q data key alone is stored", stubCall{Output: &quicksight.DescribeKeyRegistrationOutput{
			QDataKey: &qstypes.QDataKey{QDataKeyType: qstypes.QDataKeyTypeCmk, QDataKeyArn: sdkaws.String(keyARN)},
		}}, false, true},
		{"access denied is a silent skip", stubCall{Err: apiErr("AccessDeniedException", "not subscribed")}, false, false},
		{"invalid parameter is a silent skip", stubCall{Err: apiErr("InvalidParameterValueException", "x")}, false, false},
		{"unsubscribed account is a silent skip", stubCall{Err: apiErr("ResourceNotFoundException", "directory not found")}, false, false},
		{"standard edition is a silent skip", stubCall{Err: apiErr("UnsupportedUserEditionException", "x")}, false, false},
		{"other error propagates", stubCall{Err: apiErr("InternalFailureException", "boom")}, true, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := countWarnings(st)
			client := quicksight.NewFromConfig(cfgWithStub(stubResponses(t, map[string][]stubCall{"DescribeKeyRegistration": {tc.call}}), testRegion))

			_, _, err := scanQSKeyRegistration(context.Background(), client, newTestAccount(testAccountID), testRegion, st, testScanID)
			if tc.wantErr != (err != nil) || (tc.wantErr && !isAPIErrorCode(err, "InternalFailureException")) {
				t.Fatalf("err = %v; wantErr %v", err, tc.wantErr)
			}
			if *warns != 0 {
				t.Errorf("recorded %d warnings; want 0", *warns)
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

// qsListCase is one account-level QuickSight list phase: run scans it, input
// reads a request's paging fields, page builds a response listing ids.
type qsListCase struct {
	name, op, kind, rtype, arnKey, wantStatus string
	run                                       func(ctx context.Context, client quickSightAPI, st *store.Store) (int, int, error)
	input                                     func(in any) (token *string, maxResults *int32)
	page                                      func(next *string, ids ...string) any
}

var qsListCases = []qsListCase{
	{
		name: "apps", op: qsListAppsOp, kind: "app", rtype: TypeQuickSightApp, arnKey: "Arn",
		run: func(ctx context.Context, client quickSightAPI, st *store.Store) (int, int, error) {
			return scanQSApps(ctx, client, newTestAccount(testAccountID), testRegion, st, testScanID)
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
		run: func(ctx context.Context, client quickSightAPI, st *store.Store) (int, int, error) {
			return scanQSDLPSettings(ctx, client, newTestAccount(testAccountID), testRegion, st, testScanID)
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
			warns := countWarnings(st)
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
			if !slices.Equal(calls.tokens, []string{"", "p2"}) || !slices.Equal(calls.maxResults, []int32{100, 100}) {
				t.Errorf("requests tokens=%q maxResults=%v; want [\"\" p2] [100 100]", calls.tokens, calls.maxResults)
			}
			if *warns != 0 {
				t.Errorf("recorded %d warnings; want 0", *warns)
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
				if r.Type != c.rtype || sv(r.Name) != name || sv(r.Region) != testRegion || sv(r.CreatedAt) != "2026-01-02T03:04:05Z" || sv(r.Status) != c.wantStatus {
					t.Errorf("row %q: type=%q name=%q region=%q created=%q status=%q; want %q %q %q 2026-01-02T03:04:05Z %q",
						arn, r.Type, sv(r.Name), sv(r.Region), sv(r.CreatedAt), sv(r.Status), c.rtype, name, testRegion, c.wantStatus)
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
			if err != nil {
				t.Fatalf("scan: %v", err)
			}
			if len(calls.tokens) != 2 || total != 2 {
				t.Errorf("made %d requests storing %d rows; want 2 and 2 (stop when the service repeats a token)", len(calls.tokens), total)
			}
		})
	}
}

func TestScanQSAccountLists_ErrorHandling(t *testing.T) {
	resp404 := &smithyhttp.ResponseError{
		Response: &smithyhttp.Response{Response: &http.Response{StatusCode: 404}},
		Err:      apiErr("UnknownError", "deserialization failed"),
	}
	firstPage := func(err error) map[string]qsListPage { return map[string]qsListPage{"": {err: err}} }
	secondPage := func(err error) map[string]qsListPage {
		return map[string]qsListPage{"": {ids: []string{"a1"}, next: sdkaws.String("p2")}, "p2": {err: err}}
	}
	cases := []struct {
		name      string
		pages     map[string]qsListPage
		wantCode  string
		wantRows  int
		wantWarns int
		// featureOffFor is the op that models this error as its feature not
		// being activated, so it skips silently instead.
		featureOffFor string
	}{
		{"first-page feature not activated skips only DLP settings", firstPage(apiErr("InvalidRequestException", "feature not activated")), "InvalidRequestException", 0, 0, qsListDlpSettingsOp},
		{"later-page feature-not-activated code propagates", secondPage(apiErr("InvalidRequestException", "x")), "InvalidRequestException", 1, 0, ""},
		{"first-page access denied is a silent skip", firstPage(apiErr("AccessDeniedException", "not subscribed")), "", 0, 0, ""},
		{"first-page unsubscribed account is a silent skip", firstPage(apiErr("ResourceNotFoundException", "directory not found")), "", 0, 0, ""},
		{"first-page edition gap is a silent skip", firstPage(apiErr("UnsupportedUserEditionException", "standard edition")), "", 0, 0, ""},
		{"first-page missing subscription is a silent skip", firstPage(apiErr("PreconditionNotMetException", "x")), "", 0, 0, ""},
		{"first-page HTML 404 is a silent skip", firstPage(resp404), "", 0, 0, ""},
		{"first-page other error propagates", firstPage(apiErr("InternalFailureException", "boom")), "InternalFailureException", 0, 0, ""},
		{"later-page denial warns and keeps the listed rows", secondPage(apiErr("AccessDeniedException", "User: x is not authorized to perform: quicksight:List")), "", 1, 1, ""},
		{"later-page soft-skip code propagates and keeps the listed rows", secondPage(apiErr("ResourceNotFoundException", "x")), "ResourceNotFoundException", 1, 0, ""},
		{"later-page other error propagates and keeps the listed rows", secondPage(apiErr("InternalFailureException", "boom")), "InternalFailureException", 1, 0, ""},
	}
	for _, c := range qsListCases {
		for _, tc := range cases {
			t.Run(c.name+"/"+tc.name, func(t *testing.T) {
				st := newTestStore(t)
				warns := countOpWarnings(st, c.op)
				client, _ := qsListClient(t, c, tc.pages)

				wantCode := tc.wantCode
				if c.op == tc.featureOffFor {
					wantCode = ""
				}
				total, _, err := c.run(context.Background(), client, st)
				if wantCode == "" && err != nil {
					t.Fatalf("err = %v; want nil", err)
				}
				if wantCode != "" && !isAPIErrorCode(err, wantCode) {
					t.Fatalf("err = %v; want %s", err, wantCode)
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

// qsTopLevelHandlers answers ListDataSets with one data set and each
// account-level list op with one item; every other op is left to the deny
// default, which the older phases soft-skip.
func qsTopLevelHandlers() map[string]func(any) (any, error) {
	h := map[string]func(any) (any, error){
		"ListDataSets": func(any) (any, error) {
			return &quicksight.ListDataSetsOutput{DataSetSummaries: []qstypes.DataSetSummary{
				{DataSetId: sdkaws.String("ds1"), Arn: sdkaws.String(qsTestARN("dataset", "ds1"))},
			}}, nil
		},
	}
	for _, c := range qsListCases {
		h[strings.TrimPrefix(c.op, "quicksight:")] = func(any) (any, error) { return c.page(nil, "x1"), nil }
	}
	return h
}

var qsTopLevelDeny = apiErr("AccessDeniedException", "not subscribed")

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
			acct := &account{ID: testAccountID, Name: "Test Account", cfg: opStubConfig(h, qsTopLevelDeny)}

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
	older := side{"ListDataSets", "quicksight"}
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
			acct := &account{ID: testAccountID, Name: "Test Account", cfg: opStubConfig(h, qsTopLevelDeny)}

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
	if *warns != 0 {
		t.Errorf("recorded %d warnings; want 0", *warns)
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
	if *warns != 0 {
		t.Errorf("recorded %d warnings; want 0", *warns)
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
// the parent phase says so in one warning under the member op.
func TestScanQSParentPhases_WarnOnceForParentsWithoutKey(t *testing.T) {
	h := map[string]func(any) (any, error){
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
		warns := countOpWarnings(st, qsListFolderMembersOp)
		parents, total, _, err := scanQSFolders(context.Background(), client, acct, testRegion, st, testScanID)
		if err != nil {
			t.Fatalf("scanQSFolders: %v", err)
		}
		if want := []childParent{{id: "f1", arn: qsTestARN("folder", "f1")}}; total != 3 || !slices.Equal(parents, want) {
			t.Errorf("total, parents = %d, %v; want 3, %v", total, parents, want)
		}
		if *warns != 1 {
			t.Errorf("recorded %d warnings for two folders without a FolderId; want 1", *warns)
		}
	})
	t.Run("groups", func(t *testing.T) {
		st := newTestStore(t)
		warns := countOpWarnings(st, qsListGroupMembershipsOp)
		groups, total, _, err := scanQSGroups(context.Background(), client, acct, testRegion, st, testScanID,
			[]nsRef{{name: "default", arn: qsTestARN("namespace", "default")}})
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
			if err != nil {
				t.Fatalf("err = %v; want nil", err)
			}
			if calls != 2 {
				t.Errorf("made %d calls; want 2 (stop when the token repeats)", calls)
			}
		})
	}
}

// qsMemberTopLevelHandlers serves, on top of qsTopLevelHandlers, the folder
// f1 and two namespaces each holding a group named "admins", with one member
// per folder and group. The member ops answer only for the FolderId and the
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
			acct := &account{ID: testAccountID, Name: "Test Account", cfg: opStubConfig(h, qsTopLevelDeny)}

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
	older := side{"DescribeKeyRegistration", "quicksight"}
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
			acct := &account{ID: testAccountID, Name: "Test Account", cfg: opStubConfig(h, qsTopLevelDeny)}

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

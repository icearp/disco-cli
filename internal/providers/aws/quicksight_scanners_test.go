package aws

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"
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
	rows, err := st.ListResources(store.ResourceFilter{Types: []string{rtype}, IncludeManaged: true, Limit: util.AllResources})
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
			if !r.ManagedByProvider || sv(r.Region) != testRegion {
				t.Errorf("managed=%v region=%q; want true %q", r.ManagedByProvider, sv(r.Region), testRegion)
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

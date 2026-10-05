package aws

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"testing"

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
	client := quicksight.NewFromConfig(cloud9CfgWithStub(stub, testRegion))

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
			client := quicksight.NewFromConfig(cloud9CfgWithStub(stubResponses(t, map[string][]stubCall{"DescribeKeyRegistration": {tc.call}}), testRegion))

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
	acct := &account{ID: testAccountID, Name: "Test Account", cfg: cloud9CfgWithStub(stub, testRegion)}

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

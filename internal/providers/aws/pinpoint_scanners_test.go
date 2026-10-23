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
	"sync"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/pinpoint"
	pptypes "github.com/aws/aws-sdk-go-v2/service/pinpoint/types"
	"github.com/aws/smithy-go"
	"github.com/icearp/disco-cli/internal/redact"
	"github.com/icearp/disco-cli/store"
)

var (
	ppDenied    = &smithy.GenericAPIError{Code: "ForbiddenException", Message: "User: arn:aws:iam::123456789012:user/t is not authorized to perform: mobiletargeting:Get"}
	ppBadInput  = &smithy.GenericAPIError{Code: "BadRequestException", Message: "bad"}
	ppGone      = &smithy.GenericAPIError{Code: "NotFoundException", Message: "not found"}
	ppThrottled = apiErr("TooManyRequestsException", "Rate exceeded")
	ppTags      = map[string]string{"team": "pinpoint"}
)

func ppAppARN(id string) string {
	return fmt.Sprintf("arn:aws:mobiletargeting:%s:%s:apps/%s", testRegion, testAccountID, id)
}

func ppChildARN(app, path string) string { return ppAppARN(app) + "/" + path }

func ppTemplateARN(name, typ string) string {
	return fmt.Sprintf("arn:aws:mobiletargeting:%s:%s:templates/%s/%s", testRegion, testAccountID, name, typ)
}

// ppSingleton is a per-app Get op the stub serves for app-1 only, answering
// every other app with NotFoundException.
type ppSingleton struct {
	op, rtype, path, name string
	body                  func(app string) any
	output                func(body any) any
}

// ppSingletons lists the per-app Get ops in the order scanPinpoint runs them.
// The Baidu and GCM bodies carry a credential the store must redact.
var ppSingletons = []ppSingleton{
	{
		"GetApplicationSettings", TypePinpointApplicationSettings, "settings", "settings",
		func(app string) any {
			return &pptypes.ApplicationSettingsResource{ApplicationId: &app, LastModifiedDate: ptrStr("2026-01-02")}
		},
		func(b any) any {
			return &pinpoint.GetApplicationSettingsOutput{ApplicationSettingsResource: b.(*pptypes.ApplicationSettingsResource)}
		},
	},
	{
		"GetEventStream", TypePinpointEventStream, "eventstream", "eventstream",
		func(app string) any {
			return &pptypes.EventStream{ApplicationId: &app, DestinationStreamArn: ptrStr("arn:aws:kinesis:us-east-1:123456789012:stream/s"), RoleArn: ptrStr("arn:aws:iam::123456789012:role/r")}
		},
		func(b any) any { return &pinpoint.GetEventStreamOutput{EventStream: b.(*pptypes.EventStream)} },
	},
	{
		"GetAdmChannel", TypePinpointADMChannel, "channels/adm", "adm",
		func(app string) any { return &pptypes.ADMChannelResponse{ApplicationId: &app, Platform: ptrStr("ADM")} },
		func(b any) any {
			return &pinpoint.GetAdmChannelOutput{ADMChannelResponse: b.(*pptypes.ADMChannelResponse)}
		},
	},
	{
		"GetApnsChannel", TypePinpointAPNSChannel, "channels/apns", "apns",
		func(app string) any {
			return &pptypes.APNSChannelResponse{ApplicationId: &app, Platform: ptrStr("APNS")}
		},
		func(b any) any {
			return &pinpoint.GetApnsChannelOutput{APNSChannelResponse: b.(*pptypes.APNSChannelResponse)}
		},
	},
	{
		"GetApnsSandboxChannel", TypePinpointAPNSSandboxChannel, "channels/apns_sandbox", "apns_sandbox",
		func(app string) any {
			return &pptypes.APNSSandboxChannelResponse{ApplicationId: &app, Platform: ptrStr("APNS_SANDBOX")}
		},
		func(b any) any {
			return &pinpoint.GetApnsSandboxChannelOutput{APNSSandboxChannelResponse: b.(*pptypes.APNSSandboxChannelResponse)}
		},
	},
	{
		"GetApnsVoipChannel", TypePinpointAPNSVoipChannel, "channels/apns_voip", "apns_voip",
		func(app string) any {
			return &pptypes.APNSVoipChannelResponse{ApplicationId: &app, Platform: ptrStr("APNS_VOIP")}
		},
		func(b any) any {
			return &pinpoint.GetApnsVoipChannelOutput{APNSVoipChannelResponse: b.(*pptypes.APNSVoipChannelResponse)}
		},
	},
	{
		"GetApnsVoipSandboxChannel", TypePinpointAPNSVoipSandboxChannel, "channels/apns_voip_sandbox", "apns_voip_sandbox",
		func(app string) any {
			return &pptypes.APNSVoipSandboxChannelResponse{ApplicationId: &app, Platform: ptrStr("APNS_VOIP_SANDBOX")}
		},
		func(b any) any {
			return &pinpoint.GetApnsVoipSandboxChannelOutput{APNSVoipSandboxChannelResponse: b.(*pptypes.APNSVoipSandboxChannelResponse)}
		},
	},
	{
		"GetBaiduChannel", TypePinpointBaiduChannel, "channels/baidu", "baidu",
		func(app string) any {
			return &pptypes.BaiduChannelResponse{ApplicationId: &app, Platform: ptrStr("BAIDU"), Credential: ptrStr("baidu-api-key-secret")}
		},
		func(b any) any {
			return &pinpoint.GetBaiduChannelOutput{BaiduChannelResponse: b.(*pptypes.BaiduChannelResponse)}
		},
	},
	{
		"GetEmailChannel", TypePinpointEmailChannel, "channels/email", "email",
		func(app string) any {
			return &pptypes.EmailChannelResponse{ApplicationId: &app, Platform: ptrStr("EMAIL"), FromAddress: ptrStr("a@example.com")}
		},
		func(b any) any {
			return &pinpoint.GetEmailChannelOutput{EmailChannelResponse: b.(*pptypes.EmailChannelResponse)}
		},
	},
	{
		"GetGcmChannel", TypePinpointGCMChannel, "channels/gcm", "gcm",
		func(app string) any {
			return &pptypes.GCMChannelResponse{ApplicationId: &app, Platform: ptrStr("GCM"), Credential: ptrStr("gcm-web-api-key-secret")}
		},
		func(b any) any {
			return &pinpoint.GetGcmChannelOutput{GCMChannelResponse: b.(*pptypes.GCMChannelResponse)}
		},
	},
	{
		"GetSmsChannel", TypePinpointSMSChannel, "channels/sms", "sms",
		func(app string) any { return &pptypes.SMSChannelResponse{ApplicationId: &app, Platform: ptrStr("SMS")} },
		func(b any) any {
			return &pinpoint.GetSmsChannelOutput{SMSChannelResponse: b.(*pptypes.SMSChannelResponse)}
		},
	},
	{
		"GetVoiceChannel", TypePinpointVoiceChannel, "channels/voice", "voice",
		func(app string) any {
			return &pptypes.VoiceChannelResponse{ApplicationId: &app, Platform: ptrStr("VOICE")}
		},
		func(b any) any {
			return &pinpoint.GetVoiceChannelOutput{VoiceChannelResponse: b.(*pptypes.VoiceChannelResponse)}
		},
	},
}

// ppFixture is what the stub lists: every page-one element is tagged, the
// other elements are not, and each listing holds an element with no Arn.
// app-2 has no Name and app-3 has no Id. Templates carry Tags in the stub
// although the real list response never does, to show they are not read.
type ppFixture struct {
	apps      [][]pptypes.ApplicationResponse
	campaigns map[string][][]pptypes.CampaignResponse // by app id
	segments  map[string][][]pptypes.SegmentResponse  // by app id
	templates [][]pptypes.TemplateResponse
}

func ppTagsOn(tagged bool) map[string]string {
	if tagged {
		return ppTags
	}
	return nil
}

func newPPFixture() ppFixture {
	camp := func(app, id string, tagged bool) pptypes.CampaignResponse {
		return pptypes.CampaignResponse{Arn: ptrStr(ppChildARN(app, "campaigns/"+id)), Id: ptrStr(id), Name: ptrStr("name-" + id), ApplicationId: ptrStr(app), Tags: ppTagsOn(tagged)}
	}
	seg := func(app, id string, tagged bool) pptypes.SegmentResponse {
		return pptypes.SegmentResponse{Arn: ptrStr(ppChildARN(app, "segments/"+id)), Id: ptrStr(id), Name: ptrStr("name-" + id), ApplicationId: ptrStr(app), Tags: ppTagsOn(tagged)}
	}
	tpl := func(name string, typ pptypes.TemplateType) pptypes.TemplateResponse {
		return pptypes.TemplateResponse{Arn: ptrStr(ppTemplateARN(name, string(typ))), TemplateName: ptrStr(name), TemplateType: typ, Tags: ppTags}
	}
	return ppFixture{
		apps: [][]pptypes.ApplicationResponse{
			{{Arn: ptrStr(ppAppARN("app-1")), Id: ptrStr("app-1"), Name: ptrStr("name-app-1"), Tags: ppTags}},
			{
				{Arn: ptrStr(ppAppARN("app-2")), Id: ptrStr("app-2")},
				{Id: ptrStr("app-noarn")},
				{Arn: ptrStr(ppAppARN("app-3")), Name: ptrStr("name-app-3")},
			},
		},
		campaigns: map[string][][]pptypes.CampaignResponse{
			"app-1": {{camp("app-1", "c-1", true)}, {{Id: ptrStr("c-noarn")}}},
			"app-2": {{camp("app-2", "c-2", false)}},
		},
		segments: map[string][][]pptypes.SegmentResponse{
			"app-1": {{seg("app-1", "s-1", true)}, {{Id: ptrStr("s-noarn")}}},
			"app-2": {{seg("app-2", "s-2", false)}},
		},
		templates: [][]pptypes.TemplateResponse{
			{tpl("tpl-email", pptypes.TemplateTypeEmail), tpl("tpl-voice", pptypes.TemplateTypeVoice)},
			{
				tpl("tpl-inapp", pptypes.TemplateTypeInapp), tpl("tpl-push", pptypes.TemplateTypePush), tpl("tpl-sms", pptypes.TemplateTypeSms),
				{TemplateName: ptrStr("tpl-noarn"), TemplateType: pptypes.TemplateTypeEmail},
				tpl("tpl-fax", pptypes.TemplateType("FAX")),
			},
		},
	}
}

// ppPinned fails a call whose input sets anything beyond want: no PageSize,
// TemplateType, Prefix or other filter is sent.
func ppPinned(got, want any) error {
	if !reflect.DeepEqual(got, want) {
		return fmt.Errorf("input = %+v, want %+v", got, want)
	}
	return nil
}

// ppSingletonHandler serves s: its input must carry only ApplicationId.
func ppSingletonHandler(s ppSingleton, fail map[string]map[string]error, calls *parentCalls) func(in any) (any, error) {
	return func(in any) (any, error) {
		v := reflect.ValueOf(in).Elem()
		app := sv(v.FieldByName("ApplicationId").Interface().(*string))
		calls.add(s.op + " " + app)
		want := reflect.New(v.Type())
		want.Elem().FieldByName("ApplicationId").Set(reflect.ValueOf(&app))
		if err := ppPinned(in, want.Interface()); err != nil {
			return nil, err
		}
		if err := fail[s.op][app+"/"]; err != nil {
			return nil, err
		}
		if app != "app-1" {
			return nil, ppGone
		}
		return s.output(s.body(app)), nil
	}
}

// ppHandlers serves f; fail is keyed by op, then by "<app>/<token>" (the app
// is "" for the account-wide listings; the token is "" on page one). calls
// records "<op> <app>" for every call ("" app for account-wide ones).
func ppHandlers(f ppFixture, fail map[string]map[string]error, calls *parentCalls) map[string]func(in any) (any, error) {
	h := map[string]func(in any) (any, error){
		"GetApps": func(in any) (any, error) {
			p := in.(*pinpoint.GetAppsInput)
			calls.add("GetApps ")
			if err := ppPinned(p, &pinpoint.GetAppsInput{Token: p.Token}); err != nil {
				return nil, err
			}
			items, next, err := emrcServe(f.apps, "", p.Token, fail["GetApps"])
			if err != nil {
				return nil, err
			}
			return &pinpoint.GetAppsOutput{ApplicationsResponse: &pptypes.ApplicationsResponse{Item: items, NextToken: next}}, nil
		},
		"GetCampaigns": func(in any) (any, error) {
			p := in.(*pinpoint.GetCampaignsInput)
			app := sv(p.ApplicationId)
			calls.add("GetCampaigns " + app)
			if err := ppPinned(p, &pinpoint.GetCampaignsInput{ApplicationId: p.ApplicationId, Token: p.Token}); err != nil {
				return nil, err
			}
			items, next, err := emrcServe(f.campaigns[app], app, p.Token, fail["GetCampaigns"])
			if err != nil {
				return nil, err
			}
			return &pinpoint.GetCampaignsOutput{CampaignsResponse: &pptypes.CampaignsResponse{Item: items, NextToken: next}}, nil
		},
		"GetSegments": func(in any) (any, error) {
			p := in.(*pinpoint.GetSegmentsInput)
			app := sv(p.ApplicationId)
			calls.add("GetSegments " + app)
			if err := ppPinned(p, &pinpoint.GetSegmentsInput{ApplicationId: p.ApplicationId, Token: p.Token}); err != nil {
				return nil, err
			}
			items, next, err := emrcServe(f.segments[app], app, p.Token, fail["GetSegments"])
			if err != nil {
				return nil, err
			}
			return &pinpoint.GetSegmentsOutput{SegmentsResponse: &pptypes.SegmentsResponse{Item: items, NextToken: next}}, nil
		},
		"ListTemplates": func(in any) (any, error) {
			p := in.(*pinpoint.ListTemplatesInput)
			calls.add("ListTemplates ")
			if err := ppPinned(p, &pinpoint.ListTemplatesInput{NextToken: p.NextToken}); err != nil {
				return nil, err
			}
			items, next, err := emrcServe(f.templates, "", p.NextToken, fail["ListTemplates"])
			if err != nil {
				return nil, err
			}
			return &pinpoint.ListTemplatesOutput{TemplatesResponse: &pptypes.TemplatesResponse{Item: items, NextToken: next}}, nil
		},
	}
	for _, s := range ppSingletons {
		h[s.op] = ppSingletonHandler(s, fail, calls)
	}
	return h
}

// ppRun is one scanPinpoint run against the stub.
type ppRun struct {
	total, inserted int
	err             error
	warns           []store.ScanWarning
	calls           []string // distinct "<op> <app>"
}

// ops is the distinct ops the run called, sorted.
func (r ppRun) ops() []string {
	var out []string
	for _, c := range r.calls {
		op, _, _ := strings.Cut(c, " ")
		if !slices.Contains(out, op) {
			out = append(out, op)
		}
	}
	slices.Sort(out)
	return out
}

// appsCalled is the distinct apps the run called op for, sorted.
func (r ppRun) appsCalled(op string) []string {
	var out []string
	for _, c := range r.calls {
		if o, app, _ := strings.Cut(c, " "); o == op {
			out = append(out, app)
		}
	}
	return out
}

// opWarns is the run's warnings under the op label label.
func (r ppRun) opWarns(label string) []store.ScanWarning {
	var out []store.ScanWarning
	for _, w := range r.warns {
		if w.Service == label {
			out = append(out, w)
		}
	}
	return out
}

func runPP(t *testing.T, st *store.Store, fail map[string]map[string]error) ppRun {
	t.Helper()
	calls := &parentCalls{}
	return runPPCtx(context.Background(), t, st, ppHandlers(newPPFixture(), fail, calls), calls)
}

// runPPCtx runs scanPinpoint with ctx against handlers, which record their
// calls in calls.
func runPPCtx(ctx context.Context, t *testing.T, st *store.Store, handlers map[string]func(in any) (any, error), calls *parentCalls) ppRun {
	t.Helper()
	warns := recordWarnings(st)
	acct := newTestAccount(testAccountID)
	acct.cfg = opStubConfig(handlers, ppDenied)
	total, inserted, err := scanPinpoint(ctx, acct, testRegion, st, testScanID)
	return ppRun{total: total, inserted: inserted, err: err, warns: *warns, calls: calls.distinct()}
}

// assertPPWarns fails unless label warned once per key, in order, each scoped
// to the test account and region and naming its key. ScanWarning has no key
// field, so the key is matched inside the message.
func assertPPWarns(t *testing.T, r ppRun, label string, keys ...string) {
	t.Helper()
	got := r.opWarns(label)
	if len(got) != len(keys) {
		t.Fatalf("%s warnings = %+v, want %d naming %v", label, got, len(keys), keys)
	}
	for i, w := range got {
		if w.Scope != testAccountID+"/"+testRegion || !strings.Contains(w.Message, keys[i]) {
			t.Errorf("%s warning %d scope/message = %q/%q, want %q naming %q", label, i, w.Scope, w.Message, testAccountID+"/"+testRegion, keys[i])
		}
	}
}

// ppTypes is every type scanPinpoint stores.
func ppTypes() []string {
	out := []string{
		TypePinpointApp, TypePinpointCampaign, TypePinpointSegment,
		TypePinpointEmailTemplate, TypePinpointInAppTemplate, TypePinpointPushTemplate, TypePinpointSmsTemplate, TypePinpointVoiceTemplate,
	}
	for _, s := range ppSingletons {
		out = append(out, s.rtype)
	}
	return out
}

// ppAllRows is every NativeID the full fixture stores, per type.
func ppAllRows() map[string][]string {
	rows := map[string][]string{
		TypePinpointApp:           {ppAppARN("app-1"), ppAppARN("app-2"), ppAppARN("app-3")},
		TypePinpointCampaign:      {ppChildARN("app-1", "campaigns/c-1"), ppChildARN("app-2", "campaigns/c-2")},
		TypePinpointSegment:       {ppChildARN("app-1", "segments/s-1"), ppChildARN("app-2", "segments/s-2")},
		TypePinpointEmailTemplate: {ppTemplateARN("tpl-email", "EMAIL")},
		TypePinpointInAppTemplate: {ppTemplateARN("tpl-inapp", "INAPP")},
		TypePinpointPushTemplate:  {ppTemplateARN("tpl-push", "PUSH")},
		TypePinpointSmsTemplate:   {ppTemplateARN("tpl-sms", "SMS")},
		TypePinpointVoiceTemplate: {ppTemplateARN("tpl-voice", "VOICE")},
	}
	for _, s := range ppSingletons {
		rows[s.rtype] = []string{ppChildARN("app-1", s.path)}
	}
	return rows
}

// ppRowsWith is ppAllRows with the types in set replaced (nil drops a type).
func ppRowsWith(set map[string][]string) map[string][]string {
	rows := ppAllRows()
	for typ, ids := range set {
		rows[typ] = ids
	}
	return rows
}

// assertPPRows checks the stored NativeIDs of every type, and that the run
// counted each as total and as inserted (a first scan of an empty store).
func assertPPRows(t *testing.T, st *store.Store, r ppRun, want map[string][]string) {
	t.Helper()
	n := 0
	for _, typ := range ppTypes() {
		got := nativeIDs(rowsByNativeID(t, st, typ))
		w := slices.Clone(want[typ])
		slices.Sort(w)
		if !slices.Equal(got, w) {
			t.Errorf("%s rows = %v, want %v", typ, got, w)
		}
		n += len(w)
	}
	if r.total != n || r.inserted != n {
		t.Errorf("total/inserted = %d/%d, want %d/%d", r.total, r.inserted, n, n)
	}
}

// assertPPRow checks a stored row's name, region, attributes and tags (nil
// wantTags: no tags stored).
func assertPPRow(t *testing.T, row store.Resource, name, attrs string, wantTags map[string]string) {
	t.Helper()
	if sv(row.Name) != name || sv(row.Region) != testRegion {
		t.Errorf("%s: name/region = %q/%q; want %q/%q", row.NativeID, sv(row.Name), sv(row.Region), name, testRegion)
	}
	// A type with a Redact rule is stored re-encoded, so compare values, not bytes.
	assertJSONEqual(t, row.NativeID+" attrs", row.AttributesJSON, attrs)
	if wantTags == nil {
		if row.TagsJSON != nil {
			t.Errorf("%s: tags = %s, want none", row.NativeID, *row.TagsJSON)
		}
		return
	}
	assertTagsMap(t, row.TagsJSON, wantTags)
}

func TestScanPinpoint_StoresEveryTypeWithTags(t *testing.T) {
	st := newTestStore(t)
	r := runPP(t, st, nil)
	if r.err != nil {
		t.Fatalf("scanPinpoint: %v", r.err)
	}
	assertPPRows(t, st, r, ppAllRows())

	f := newPPFixture()
	apps := rowsByNativeID(t, st, TypePinpointApp)
	assertPPRow(t, apps[ppAppARN("app-1")], "name-app-1", mustJSON(f.apps[0][0]), ppTags)
	assertPPRow(t, apps[ppAppARN("app-2")], "app-2", mustJSON(f.apps[1][0]), nil)
	assertPPRow(t, apps[ppAppARN("app-3")], "name-app-3", mustJSON(f.apps[1][2]), nil)
	camps := rowsByNativeID(t, st, TypePinpointCampaign)
	assertPPRow(t, camps[ppChildARN("app-1", "campaigns/c-1")], "name-c-1", mustJSON(f.campaigns["app-1"][0][0]), ppTags)
	assertPPRow(t, camps[ppChildARN("app-2", "campaigns/c-2")], "name-c-2", mustJSON(f.campaigns["app-2"][0][0]), nil)
	segs := rowsByNativeID(t, st, TypePinpointSegment)
	assertPPRow(t, segs[ppChildARN("app-1", "segments/s-1")], "name-s-1", mustJSON(f.segments["app-1"][0][0]), ppTags)
	assertPPRow(t, segs[ppChildARN("app-2", "segments/s-2")], "name-s-2", mustJSON(f.segments["app-2"][0][0]), nil)
	for typ, tpl := range map[string]pptypes.TemplateResponse{
		TypePinpointEmailTemplate: f.templates[0][0],
		TypePinpointInAppTemplate: f.templates[1][0],
		TypePinpointPushTemplate:  f.templates[1][1],
		TypePinpointSmsTemplate:   f.templates[1][2],
		TypePinpointVoiceTemplate: f.templates[0][1],
	} {
		assertPPRow(t, rowsByNativeID(t, st, typ)[sv(tpl.Arn)], sv(tpl.TemplateName), mustJSON(tpl), nil)
	}
	for _, s := range ppSingletons {
		attrs, err := redact.Apply(s.rtype, mustJSON(s.body("app-1")))
		if err != nil {
			t.Fatalf("redact.Apply(%s): %v", s.rtype, err)
		}
		assertPPRow(t, rowsByNativeID(t, st, s.rtype)[ppChildARN("app-1", s.path)], s.name, attrs, nil)
	}

	// One warning each for the element with no Arn, for app-3, stored with no
	// Id, whose children were not read, and for the template of a type disco
	// declares none for.
	assertPPWarns(t, r, "pinpoint:GetApps", "app-noarn", ppAppARN("app-3"))
	assertPPWarns(t, r, "pinpoint:GetCampaigns", "c-noarn")
	assertPPWarns(t, r, "pinpoint:GetSegments", "s-noarn")
	assertPPWarns(t, r, "pinpoint:ListTemplates", "tpl-noarn", "tpl-fax (FAX)")
	if len(r.warns) != 6 {
		t.Errorf("warnings = %+v, want 6", r.warns)
	}
	for _, op := range []string{"GetCampaigns", "GetSegments", "GetApplicationSettings", "GetVoiceChannel"} {
		if got, want := r.appsCalled(op), []string{"app-1", "app-2"}; !slices.Equal(got, want) {
			t.Errorf("%s called for %v, want %v", op, got, want)
		}
	}
}

// The redacted credentials are the stored fields, not lookalikes: the Baidu
// and GCM channel rows carry the placeholder at Credential.
func TestScanPinpoint_RedactsChannelCredentials(t *testing.T) {
	st := newTestStore(t)
	if r := runPP(t, st, nil); r.err != nil {
		t.Fatalf("scanPinpoint: %v", r.err)
	}
	for typ, path := range map[string]string{TypePinpointBaiduChannel: "channels/baidu", TypePinpointGCMChannel: "channels/gcm"} {
		row := rowsByNativeID(t, st, typ)[ppChildARN("app-1", path)]
		var got struct{ Credential, Platform string }
		if err := json.Unmarshal([]byte(row.AttributesJSON), &got); err != nil {
			t.Fatalf("%s attrs %q: %v", typ, row.AttributesJSON, err)
		}
		if got.Credential != redact.Placeholder || got.Platform == "" {
			t.Errorf("%s Credential/Platform = %q/%q, want %q/kept", typ, got.Credential, got.Platform, redact.Placeholder)
		}
	}
}

// A failed page two keeps the rows read before it, the error is returned, and
// every later phase still runs, the per-app phases over the apps stored.
func TestScanPinpoint_FailedPageKeepsRowsAndRunsLaterPhases(t *testing.T) {
	appOne := ppAllRows()
	for _, tc := range []struct {
		op, key string
		want    map[string][]string
	}{
		{"GetApps", "/p2", ppRowsWith(map[string][]string{
			TypePinpointApp:      {ppAppARN("app-1")},
			TypePinpointCampaign: appOne[TypePinpointCampaign][:1],
			TypePinpointSegment:  appOne[TypePinpointSegment][:1],
		})},
		{"GetCampaigns", "app-1/p2", ppAllRows()},
		{"GetSegments", "app-1/p2", ppAllRows()},
		{"ListTemplates", "/p2", ppRowsWith(map[string][]string{
			TypePinpointInAppTemplate: nil, TypePinpointPushTemplate: nil, TypePinpointSmsTemplate: nil,
		})},
	} {
		t.Run(tc.op, func(t *testing.T) {
			st := newTestStore(t)
			r := runPP(t, st, map[string]map[string]error{tc.op: {tc.key: ppBadInput}})
			if classifyServiceError(r.err) != outcomeError || !isAPIErrorCode(r.err, "BadRequestException") {
				t.Fatalf("err = %v, want a BadRequestException error", r.err)
			}
			assertPPRows(t, st, r, tc.want)
		})
	}
}

// A per-app Get that fails with a code other than not-found or denial fails
// its phase after the other apps' rows are stored, and every later phase
// still runs.
func TestScanPinpoint_PerAppGetErrorReturnedAndLaterPhasesRun(t *testing.T) {
	for _, s := range ppSingletons {
		t.Run(s.op, func(t *testing.T) {
			st := newTestStore(t)
			r := runPP(t, st, map[string]map[string]error{s.op: {"app-1/": ppBadInput}})
			if classifyServiceError(r.err) != outcomeError || !isAPIErrorCode(r.err, "BadRequestException") {
				t.Fatalf("err = %v, want a BadRequestException error", r.err)
			}
			assertPPRows(t, st, r, ppRowsWith(map[string][]string{s.rtype: nil}))
		})
	}
}

// A denial warns once for the op, naming the first denied app when the op
// fans out over apps (an account-wide listing has no key to name), and is not
// an error; the other phases keep their rows.
func TestScanPinpoint_DeniedWarnsOnce(t *testing.T) {
	type tcase struct {
		op, label string
		apps      []string
		key       string
		want      map[string][]string
	}
	cases := []tcase{
		{"GetApps", "pinpoint:GetApps", []string{""}, "", ppRowsWith(func() map[string][]string {
			m := map[string][]string{TypePinpointApp: nil, TypePinpointCampaign: nil, TypePinpointSegment: nil}
			for _, s := range ppSingletons {
				m[s.rtype] = nil
			}
			return m
		}())},
		{"GetCampaigns", "pinpoint:GetCampaigns", []string{"app-1", "app-2"}, "first app-1", ppRowsWith(map[string][]string{TypePinpointCampaign: nil})},
		{"GetSegments", "pinpoint:GetSegments", []string{"app-1", "app-2"}, "first app-1", ppRowsWith(map[string][]string{TypePinpointSegment: nil})},
		{"ListTemplates", "pinpoint:ListTemplates", []string{""}, "", ppRowsWith(map[string][]string{
			TypePinpointEmailTemplate: nil, TypePinpointInAppTemplate: nil, TypePinpointPushTemplate: nil, TypePinpointSmsTemplate: nil, TypePinpointVoiceTemplate: nil,
		})},
	}
	for _, s := range ppSingletons {
		cases = append(cases, tcase{s.op, "pinpoint:" + s.op, []string{"app-1", "app-2"}, "first app-1", ppRowsWith(map[string][]string{s.rtype: nil})})
	}
	for _, tc := range cases {
		t.Run(tc.op, func(t *testing.T) {
			st := newTestStore(t)
			fail := map[string]error{}
			for _, a := range tc.apps {
				fail[a+"/"] = ppDenied
			}
			r := runPP(t, st, map[string]map[string]error{tc.op: fail})
			if r.err != nil {
				t.Fatalf("err = %v, want nil", r.err)
			}
			// The elements with no Arn on the denied listing's page two are never read.
			assertPPWarns(t, r, tc.label, tc.key)
			assertPPRows(t, st, r, tc.want)
		})
	}
}

// An app the per-app listing no longer finds is skipped silently when nothing
// was read from it, and with one warning naming it when page one was read.
func TestScanPinpoint_AppNotFound(t *testing.T) {
	for _, op := range []string{"GetCampaigns", "GetSegments"} {
		typ := map[string]string{"GetCampaigns": TypePinpointCampaign, "GetSegments": TypePinpointSegment}[op]
		t.Run(op+"/before any page", func(t *testing.T) {
			st := newTestStore(t)
			r := runPP(t, st, map[string]map[string]error{op: {"app-1/": ppGone}})
			if r.err != nil {
				t.Fatalf("err = %v, want nil", r.err)
			}
			assertPPWarns(t, r, "pinpoint:"+op)
			assertPPRows(t, st, r, ppRowsWith(map[string][]string{typ: ppAllRows()[typ][1:]}))
		})
		t.Run(op+"/after page one", func(t *testing.T) {
			st := newTestStore(t)
			r := runPP(t, st, map[string]map[string]error{op: {"app-1/p2": ppGone}})
			if r.err != nil {
				t.Fatalf("err = %v, want nil", r.err)
			}
			assertPPWarns(t, r, "pinpoint:"+op, "first app-1")
			assertPPRows(t, st, r, ppAllRows())
		})
	}
}

// A throttled call is not skipped: alone it is returned, leaving the scan
// transient with every other row stored; beside another phase's hard error it
// is warned about once and the hard error is returned.
func TestScanPinpoint_TransientBesideHardError(t *testing.T) {
	for _, tc := range []struct {
		op, label, key string
		want           map[string][]string
	}{
		{"GetBaiduChannel", "pinpoint:GetBaiduChannel", "app-1/", ppRowsWith(map[string][]string{TypePinpointBaiduChannel: nil})},
		{"GetCampaigns", "pinpoint:GetCampaigns", "app-2/", ppRowsWith(map[string][]string{TypePinpointCampaign: ppAllRows()[TypePinpointCampaign][:1]})},
		{"GetApps", "pinpoint:GetApps", "/p2", ppRowsWith(map[string][]string{
			TypePinpointApp:      {ppAppARN("app-1")},
			TypePinpointCampaign: ppAllRows()[TypePinpointCampaign][:1],
			TypePinpointSegment:  ppAllRows()[TypePinpointSegment][:1],
		})},
	} {
		for _, hard := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/hard=%v", tc.op, hard), func(t *testing.T) {
				st := newTestStore(t)
				fail := map[string]map[string]error{tc.op: {tc.key: ppThrottled}}
				want := tc.want
				wantOutcome, wantWarns := outcomeTransient, 0
				if hard {
					fail["ListTemplates"] = map[string]error{"/p2": ppBadInput}
					want = maps.Clone(tc.want)
					want[TypePinpointInAppTemplate], want[TypePinpointPushTemplate], want[TypePinpointSmsTemplate] = nil, nil, nil
					wantOutcome, wantWarns = outcomeError, 1
				}
				r := runPP(t, st, fail)
				if got := classifyServiceError(r.err); got != wantOutcome {
					t.Errorf("classifyServiceError(%v) = %v, want %v", r.err, got, wantOutcome)
				}
				if hard && !isAPIErrorCode(r.err, "BadRequestException") {
					t.Errorf("err = %v, want the ListTemplates BadRequestException", r.err)
				}
				var throttleWarns int
				for _, w := range r.opWarns(tc.label) {
					if strings.Contains(w.Message, "TooManyRequestsException") {
						throttleWarns++
					}
				}
				if throttleWarns != wantWarns {
					t.Errorf("%s throttle warnings = %d, want %d: %+v", tc.label, throttleWarns, wantWarns, r.warns)
				}
				assertPPRows(t, st, r, want)
			})
		}
	}
}

// A failed write stops the scan with no warning claiming the lost rows, and no
// later phase runs.
func TestScanPinpoint_WriteFailureStops(t *testing.T) {
	for _, tc := range []struct {
		typ, label string
		ops        []string
	}{
		{TypePinpointApp, "pinpoint:GetApps", []string{"GetApps"}},
		{TypePinpointCampaign, "pinpoint:GetCampaigns", []string{"GetApplicationSettings", "GetApps", "GetCampaigns", "GetEventStream"}},
	} {
		t.Run(tc.typ, func(t *testing.T) {
			st := newTestStore(t)
			refuseStoreWrites(t, st, tc.typ)
			r := runPP(t, st, nil)
			if !errors.Is(r.err, store.ErrStoreWrite) {
				t.Fatalf("err = %v; want the store write failure", r.err)
			}
			if got := r.opWarns(tc.label); len(got) != 0 {
				t.Errorf("%s warnings = %+v; want none after a failed write", tc.label, got)
			}
			if got := r.ops(); !slices.Equal(got, tc.ops) {
				t.Errorf("ops called = %v; want only %v", got, tc.ops)
			}
		})
	}
}

// Once ctx ends the per-app phases read no app, and the stop is returned.
func TestScanPinpoint_ContextStopsPerAppReads(t *testing.T) {
	st := newTestStore(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	calls := &parentCalls{}
	h := ppHandlers(newPPFixture(), nil, calls)
	getApps := h["GetApps"]
	var once sync.Once
	h["GetApps"] = func(in any) (any, error) {
		out, err := getApps(in)
		if in.(*pinpoint.GetAppsInput).Token != nil {
			once.Do(cancel)
		}
		return out, err
	}
	r := runPPCtx(ctx, t, st, h, calls)
	if !errors.Is(r.err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", r.err)
	}
	for _, s := range ppSingletons {
		if got := r.appsCalled(s.op); len(got) != 0 {
			t.Errorf("%s called for %v after ctx ended", s.op, got)
		}
	}
	if got := r.appsCalled("GetCampaigns"); len(got) != 0 {
		t.Errorf("GetCampaigns called for %v after ctx ended", got)
	}
	if got := nativeIDs(rowsByNativeID(t, st, TypePinpointApp)); len(got) != 3 {
		t.Errorf("apps stored = %v, want all 3", got)
	}
}

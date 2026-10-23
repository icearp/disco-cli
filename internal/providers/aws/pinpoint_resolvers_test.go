package aws

import (
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	pptypes "github.com/aws/aws-sdk-go-v2/service/pinpoint/types"
	"github.com/icearp/disco-cli/store"
)

const ppOtherRegion = "eu-west-1"

func ppRegionAppARN(region, id string) string {
	return fmt.Sprintf("arn:aws:mobiletargeting:%s:%s:apps/%s", region, testAccountID, id)
}

func ppRelsFrom(t *testing.T, st *store.Store, id string) []store.Relationship {
	t.Helper()
	rels, err := st.RelationshipsFrom(id)
	if err != nil {
		t.Fatalf("RelationshipsFrom(%s): %v", id, err)
	}
	return rels
}

// assertPPEdges fails unless the edges from id are exactly want, as
// "<kind> <toID>".
func assertPPEdges(t *testing.T, st *store.Store, id string, want ...string) {
	t.Helper()
	rels := ppRelsFrom(t, st, id)
	got := map[string]bool{}
	for _, r := range rels {
		got[r.Kind+" "+r.ToID] = true
	}
	if len(got) != len(want) {
		t.Errorf("edges from %s = %v, want %v", id, got, want)
		return
	}
	for _, w := range want {
		if !got[w] {
			t.Errorf("edges from %s = %v, missing %s", id, got, w)
		}
	}
}

// assertPPDecodeErr fails unless err holds an attribute decode error.
func assertPPDecodeErr(t *testing.T, err error) {
	t.Helper()
	var typeErr *json.UnmarshalTypeError
	if !errors.As(err, &typeErr) {
		t.Fatalf("err = %v, want the attribute decode error", err)
	}
}

// Each channel and the settings row link to the app of their own region only:
// app-2 is scanned in testRegion, so the channel naming it from another region
// gets no edge.
func TestResolvePinpointChannelApps(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	appID := upsertTestResource(t, st, "aws", acct.ID, TypePinpointApp, ppAppARN("app-1"), testRegion, "{}")
	upsertTestResource(t, st, "aws", acct.ID, TypePinpointApp, ppAppARN("app-2"), testRegion, "{}")
	app := "app-1"
	adm := upsertTestResource(t, st, "aws", acct.ID, TypePinpointADMChannel, ppChildARN(app, "channels/adm"), testRegion, mustJSON(pptypes.ADMChannelResponse{ApplicationId: &app}))
	set := upsertTestResource(t, st, "aws", acct.ID, TypePinpointApplicationSettings, ppChildARN(app, "settings"), testRegion, mustJSON(pptypes.ApplicationSettingsResource{ApplicationId: &app}))
	other := upsertTestResource(t, st, "aws", acct.ID, TypePinpointSMSChannel, ppRegionAppARN(ppOtherRegion, "app-2")+"/channels/sms", ppOtherRegion, mustJSON(pptypes.SMSChannelResponse{ApplicationId: ptrStr("app-2")}))
	empty := upsertTestResource(t, st, "aws", acct.ID, TypePinpointVoiceChannel, ppChildARN(app, "channels/voice"), testRegion, "{}")
	if err := resolvePinpointChannelApps(acct, st); err != nil {
		t.Fatalf("resolvePinpointChannelApps: %v", err)
	}
	assertPPEdges(t, st, adm, store.RelAttachedTo+" "+appID)
	assertPPEdges(t, st, set, store.RelAttachedTo+" "+appID)
	assertPPEdges(t, st, other)
	assertPPEdges(t, st, empty)
}

// A row whose attributes do not decode is returned as an error after the
// other rows are linked.
func TestResolvePinpointChannelApps_DecodeErrorReturnedAfterOthers(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	appID := upsertTestResource(t, st, "aws", acct.ID, TypePinpointApp, ppAppARN("app-1"), testRegion, "{}")
	// The bad row's type sorts first, so it is read before the good one.
	upsertTestResource(t, st, "aws", acct.ID, TypePinpointADMChannel, ppChildARN("app-0", "channels/adm"), testRegion, `{"ApplicationId":7}`)
	app := "app-1"
	good := upsertTestResource(t, st, "aws", acct.ID, TypePinpointGCMChannel, ppChildARN(app, "channels/gcm"), testRegion, mustJSON(pptypes.GCMChannelResponse{ApplicationId: &app}))
	assertPPDecodeErr(t, resolvePinpointChannelApps(acct, st))
	assertPPEdges(t, st, good, store.RelAttachedTo+" "+appID)
}

func TestResolvePinpointSegments(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	appID := upsertTestResource(t, st, "aws", acct.ID, TypePinpointApp, ppAppARN("app-1"), testRegion, "{}")
	app := "app-1"
	seg := upsertTestResource(t, st, "aws", acct.ID, TypePinpointSegment, ppChildARN(app, "segments/s-1"), testRegion, mustJSON(pptypes.SegmentResponse{ApplicationId: &app}))
	empty := upsertTestResource(t, st, "aws", acct.ID, TypePinpointSegment, ppChildARN(app, "segments/s-2"), testRegion, "{}")
	if err := resolvePinpointSegments(acct, st); err != nil {
		t.Fatalf("resolvePinpointSegments: %v", err)
	}
	assertPPEdges(t, st, seg, store.RelAttachedTo+" "+appID)
	assertPPEdges(t, st, empty)
}

func TestResolvePinpointSegments_DecodeErrorReturnedAfterOthers(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	appID := upsertTestResource(t, st, "aws", acct.ID, TypePinpointApp, ppAppARN("app-1"), testRegion, "{}")
	// Named so the bad row is read first: ListResources orders by name.
	upsertTestResourceNamed(t, st, TypePinpointSegment, ppChildARN("app-1", "segments/bad"), testRegion, `{"ApplicationId":7}`, "a-bad")
	app := "app-1"
	good := upsertTestResourceNamed(t, st, TypePinpointSegment, ppChildARN(app, "segments/s-1"), testRegion, mustJSON(pptypes.SegmentResponse{ApplicationId: &app}), "b-good")
	assertPPDecodeErr(t, resolvePinpointSegments(acct, st))
	assertPPEdges(t, st, good, store.RelAttachedTo+" "+appID)
}

// A campaign links to its app, its segment and the templates of its own
// region; a same-named template in another region is not linked.
func TestResolvePinpointCampaigns(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	appID := upsertTestResource(t, st, "aws", acct.ID, TypePinpointApp, ppAppARN("app-1"), testRegion, "{}")
	segID := upsertTestResource(t, st, "aws", acct.ID, TypePinpointSegment, ppChildARN("app-1", "segments/s-1"), testRegion, "{}")
	emailID := upsertTestResourceNamed(t, st, TypePinpointEmailTemplate, ppTemplateARN("welcome", "EMAIL"), testRegion, "{}", "welcome")
	upsertTestResourceNamed(t, st, TypePinpointEmailTemplate, fmt.Sprintf("arn:aws:mobiletargeting:%s:%s:templates/welcome/EMAIL", ppOtherRegion, testAccountID), ppOtherRegion, "{}", "welcome")
	upsertTestResourceNamed(t, st, TypePinpointSmsTemplate, fmt.Sprintf("arn:aws:mobiletargeting:%s:%s:templates/text/SMS", ppOtherRegion, testAccountID), ppOtherRegion, "{}", "text")
	inAppID := upsertTestResourceNamed(t, st, TypePinpointInAppTemplate, ppTemplateARN("banner", "INAPP"), testRegion, "{}", "banner")
	voiceID := upsertTestResourceNamed(t, st, TypePinpointVoiceTemplate, ppTemplateARN("call", "VOICE"), testRegion, "{}", "call")
	camp := pptypes.CampaignResponse{
		ApplicationId: ptrStr("app-1"), SegmentId: ptrStr("s-1"),
		TemplateConfiguration: &pptypes.TemplateConfiguration{
			EmailTemplate: &pptypes.Template{Name: ptrStr("welcome")},
			SMSTemplate:   &pptypes.Template{Name: ptrStr("text")},
			InAppTemplate: &pptypes.Template{Name: ptrStr("banner")},
			VoiceTemplate: &pptypes.Template{Name: ptrStr("call")},
		},
	}
	campID := upsertTestResource(t, st, "aws", acct.ID, TypePinpointCampaign, ppChildARN("app-1", "campaigns/c-1"), testRegion, mustJSON(camp))
	empty := upsertTestResource(t, st, "aws", acct.ID, TypePinpointCampaign, ppChildARN("app-1", "campaigns/c-2"), testRegion, "{}")
	if err := resolvePinpointCampaigns(acct, st); err != nil {
		t.Fatalf("resolvePinpointCampaigns: %v", err)
	}
	assertPPEdges(t, st, campID,
		store.RelAttachedTo+" "+appID, store.RelUses+" "+segID, store.RelUses+" "+emailID, store.RelUses+" "+inAppID, store.RelUses+" "+voiceID)
	assertPPEdges(t, st, empty)
}

func TestResolvePinpointCampaigns_DecodeErrorReturnedAfterOthers(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	appID := upsertTestResource(t, st, "aws", acct.ID, TypePinpointApp, ppAppARN("app-1"), testRegion, "{}")
	upsertTestResourceNamed(t, st, TypePinpointCampaign, ppChildARN("app-1", "campaigns/bad"), testRegion, `{"SegmentId":7}`, "a-bad")
	good := upsertTestResourceNamed(t, st, TypePinpointCampaign, ppChildARN("app-1", "campaigns/c-1"), testRegion,
		mustJSON(pptypes.CampaignResponse{ApplicationId: ptrStr("app-1")}), "b-good")
	assertPPDecodeErr(t, resolvePinpointCampaigns(acct, st))
	assertPPEdges(t, st, good, store.RelAttachedTo+" "+appID)
}

// An event stream links to its app, to the Kinesis stream or Firehose
// delivery stream it delivers to, and to its role; an unscanned destination
// gets no edge.
func TestResolvePinpointEventStreams(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	appID := upsertTestResource(t, st, "aws", acct.ID, TypePinpointApp, ppAppARN("app-1"), testRegion, "{}")
	appID2 := upsertTestResource(t, st, "aws", acct.ID, TypePinpointApp, ppAppARN("app-2"), testRegion, "{}")
	kinARN := fmt.Sprintf("arn:aws:kinesis:%s:%s:stream/events", testRegion, testAccountID)
	kinID := upsertTestResource(t, st, "aws", acct.ID, TypeKinesisStream, kinARN, testRegion, "{}")
	fhARN := fmt.Sprintf("arn:aws:firehose:%s:%s:deliverystream/events", testRegion, testAccountID)
	fhID := upsertTestResource(t, st, "aws", acct.ID, TypeFirehoseDeliveryStream, fhARN, testRegion, "{}")
	roleARN := fmt.Sprintf("arn:aws:iam::%s:role/pinpoint-events", testAccountID)
	roleID := upsertTestResource(t, st, "aws", acct.ID, TypeIAMRole, roleARN, "", "{}")
	toKinesis := upsertTestResource(t, st, "aws", acct.ID, TypePinpointEventStream, ppChildARN("app-1", "eventstream"), testRegion,
		mustJSON(pptypes.EventStream{ApplicationId: ptrStr("app-1"), DestinationStreamArn: &kinARN, RoleArn: &roleARN}))
	toFirehose := upsertTestResource(t, st, "aws", acct.ID, TypePinpointEventStream, ppChildARN("app-2", "eventstream"), testRegion,
		mustJSON(pptypes.EventStream{ApplicationId: ptrStr("app-2"), DestinationStreamArn: &fhARN}))
	unscanned := upsertTestResource(t, st, "aws", acct.ID, TypePinpointEventStream, ppChildARN("app-3", "eventstream"), testRegion,
		mustJSON(pptypes.EventStream{ApplicationId: ptrStr("app-3"), DestinationStreamArn: ptrStr(kinARN + "-gone")}))
	empty := upsertTestResource(t, st, "aws", acct.ID, TypePinpointEventStream, ppChildARN("app-4", "eventstream"), testRegion, "{}")
	unscannedRole := upsertTestResource(t, st, "aws", acct.ID, TypePinpointEventStream, ppChildARN("app-5", "eventstream"), testRegion,
		mustJSON(pptypes.EventStream{ApplicationId: ptrStr("app-5"), RoleArn: ptrStr(roleARN + "-gone")}))
	if err := resolvePinpointEventStreams(acct, st); err != nil {
		t.Fatalf("resolvePinpointEventStreams: %v", err)
	}
	assertPPEdges(t, st, toKinesis, store.RelAttachedTo+" "+appID, store.RelUses+" "+kinID, store.RelAssumes+" "+roleID)
	assertPPEdges(t, st, toFirehose, store.RelAttachedTo+" "+appID2, store.RelUses+" "+fhID)
	assertPPEdges(t, st, unscanned)
	assertPPEdges(t, st, empty)
	assertPPEdges(t, st, unscannedRole)
}

func TestResolvePinpointEventStreams_DecodeErrorReturnedAfterOthers(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	appID := upsertTestResource(t, st, "aws", acct.ID, TypePinpointApp, ppAppARN("app-1"), testRegion, "{}")
	upsertTestResourceNamed(t, st, TypePinpointEventStream, ppChildARN("app-0", "eventstream"), testRegion, `{"RoleArn":7}`, "a-bad")
	good := upsertTestResourceNamed(t, st, TypePinpointEventStream, ppChildARN("app-1", "eventstream"), testRegion,
		mustJSON(pptypes.EventStream{ApplicationId: ptrStr("app-1")}), "b-good")
	assertPPDecodeErr(t, resolvePinpointEventStreams(acct, st))
	assertPPEdges(t, st, good, store.RelAttachedTo+" "+appID)
}

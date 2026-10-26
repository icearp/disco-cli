package aws

import (
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	cmtypes "github.com/aws/aws-sdk-go-v2/service/chimesdkmessaging/types"
	cvtypes "github.com/aws/aws-sdk-go-v2/service/chimesdkvoice/types"
	"github.com/icearp/disco-cli/store"
)

func TestResolveChimeAppInstanceChildren(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	aiARN := fmt.Sprintf("arn:aws:chime:%s:%s:app-instance/ai-1", testRegion, acct.ID)
	aiID := upsertTestResource(t, st, "aws", acct.ID, TypeChimeAppInstance, aiARN, testRegion, "{}")

	botARN := aiARN + "/bot/b-1"
	botID := upsertTestResource(t, st, "aws", acct.ID, TypeChimeAppInstanceBot, botARN, testRegion, "{}")
	userARN := aiARN + "/user/u-1"
	userID := upsertTestResource(t, st, "aws", acct.ID, TypeChimeAppInstanceUser, userARN, testRegion, "{}")

	cfARN := fmt.Sprintf("arn:aws:chime:%s:%s:app-instance/ai-1/channel-flow/cf-1", testRegion, acct.ID)
	cfAttrs, _ := json.Marshal(chimeChannelFlowAttrs{
		ChannelFlowSummary: cmtypes.ChannelFlowSummary{ChannelFlowArn: ptrStr(cfARN), Name: ptrStr("flow")},
		AppInstanceArn:     ptrStr(aiARN),
	})
	cfID := upsertTestResource(t, st, "aws", acct.ID, TypeChimeChannelFlow, cfARN, testRegion, string(cfAttrs))

	if err := resolveChimeAppInstanceChildren(acct, st); err != nil {
		t.Fatalf("resolveChimeAppInstanceChildren: %v", err)
	}
	for _, src := range []string{botID, userID, cfID} {
		rels, _ := st.RelationshipsFrom(src)
		assertRelationship(t, rels, src, aiID, store.RelAttachedTo)
	}
}

func TestResolveChimeAppInstanceChildren_NoEdge(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	// An app-instance exists so the resolver runs, but the bot points elsewhere.
	upsertTestResource(t, st, "aws", acct.ID, TypeChimeAppInstance, fmt.Sprintf("arn:aws:chime:%s:%s:app-instance/real", testRegion, acct.ID), testRegion, "{}")
	botARN := fmt.Sprintf("arn:aws:chime:%s:%s:app-instance/gone/bot/b-1", testRegion, acct.ID)
	botID := upsertTestResource(t, st, "aws", acct.ID, TypeChimeAppInstanceBot, botARN, testRegion, "{}")

	if err := resolveChimeAppInstanceChildren(acct, st); err != nil {
		t.Fatalf("resolveChimeAppInstanceChildren: %v", err)
	}
	rels, _ := st.RelationshipsFrom(botID)
	if len(rels) != 0 {
		t.Errorf("emitted %d edges, want 0", len(rels))
	}
}

func TestResolveChimeVoiceProfileDomain(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	keyARN := fmt.Sprintf("arn:aws:kms:%s:%s:key/k-1", testRegion, acct.ID)
	keyID := upsertTestResource(t, st, "aws", acct.ID, TypeKMSKey, keyARN, testRegion, fmt.Sprintf(`{"KeyId":"k-1","Arn":%q}`, keyARN))

	domARN := fmt.Sprintf("arn:aws:chime:%s:%s:voice-profile-domain/d-1", testRegion, acct.ID)
	domBody, _ := json.Marshal(cvtypes.VoiceProfileDomain{
		VoiceProfileDomainId:  ptrStr("d-1"),
		VoiceProfileDomainArn: ptrStr(domARN),
		ServerSideEncryptionConfiguration: &cvtypes.ServerSideEncryptionConfiguration{
			KmsKeyArn: ptrStr(keyARN),
		},
	})
	domID := upsertTestResource(t, st, "aws", acct.ID, TypeChimeVoiceProfileDomain, domARN, testRegion, string(domBody))

	profARN := fmt.Sprintf("arn:aws:chime:%s:%s:voice-profile-domain/d-1/voice-profile/p-1", testRegion, acct.ID)
	profBody, _ := json.Marshal(cvtypes.VoiceProfileSummary{VoiceProfileArn: ptrStr(profARN), VoiceProfileDomainId: ptrStr("d-1")})
	profID := upsertTestResource(t, st, "aws", acct.ID, TypeChimeVoiceProfile, profARN, testRegion, string(profBody))

	if err := resolveChimeVoiceProfileDomain(acct, st); err != nil {
		t.Fatalf("resolveChimeVoiceProfileDomain: %v", err)
	}
	profRels, _ := st.RelationshipsFrom(profID)
	assertRelationship(t, profRels, profID, domID, store.RelAttachedTo)
	domRels, _ := st.RelationshipsFrom(domID)
	assertRelationship(t, domRels, domID, keyID, store.RelUses)
}

func TestResolveChimeVoiceProfileDomain_NoEdge(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	domARN := fmt.Sprintf("arn:aws:chime:%s:%s:voice-profile-domain/d-1", testRegion, acct.ID)
	upsertTestResource(t, st, "aws", acct.ID, TypeChimeVoiceProfileDomain, domARN, testRegion, `{"VoiceProfileDomainId":"d-1"}`)

	// Profile references a different (unscanned) domain id → no edge.
	profARN := fmt.Sprintf("arn:aws:chime:%s:%s:voice-profile-domain/other/voice-profile/p-1", testRegion, acct.ID)
	profID := upsertTestResource(t, st, "aws", acct.ID, TypeChimeVoiceProfile, profARN, testRegion, `{"VoiceProfileDomainId":"missing"}`)

	if err := resolveChimeVoiceProfileDomain(acct, st); err != nil {
		t.Fatalf("resolveChimeVoiceProfileDomain: %v", err)
	}
	rels, _ := st.RelationshipsFrom(profID)
	if len(rels) != 0 {
		t.Errorf("emitted %d edges, want 0", len(rels))
	}
}

func TestResolveChimeSipMediaApplicationLambda(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	fnARN := fmt.Sprintf("arn:aws:lambda:%s:%s:function:sip-handler", testRegion, acct.ID)
	fnID := upsertTestResource(t, st, "aws", acct.ID, TypeLambdaFunction, fnARN, testRegion, "{}")

	smaARN := fmt.Sprintf("arn:aws:chime:%s:%s:sma/sma-1", testRegion, acct.ID)
	smaBody, _ := json.Marshal(cvtypes.SipMediaApplication{
		SipMediaApplicationArn: ptrStr(smaARN),
		Endpoints:              []cvtypes.SipMediaApplicationEndpoint{{LambdaArn: ptrStr(fnARN)}},
	})
	smaID := upsertTestResource(t, st, "aws", acct.ID, TypeChimeSipMediaApplication, smaARN, testRegion, string(smaBody))

	if err := resolveChimeSipMediaApplicationLambda(acct, st); err != nil {
		t.Fatalf("resolveChimeSipMediaApplicationLambda: %v", err)
	}
	rels, _ := st.RelationshipsFrom(smaID)
	assertRelationship(t, rels, smaID, fnID, store.RelUses)
}

func TestResolveChimeSipMediaApplicationLambda_NoEdge(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	smaARN := fmt.Sprintf("arn:aws:chime:%s:%s:sma/sma-1", testRegion, acct.ID)
	missing := fmt.Sprintf("arn:aws:lambda:%s:%s:function:gone", testRegion, acct.ID)
	smaAttrs := fmt.Sprintf(`{"Endpoints":[{"LambdaArn":%q}]}`, missing)
	smaID := upsertTestResource(t, st, "aws", acct.ID, TypeChimeSipMediaApplication, smaARN, testRegion, smaAttrs)

	if err := resolveChimeSipMediaApplicationLambda(acct, st); err != nil {
		t.Fatalf("resolveChimeSipMediaApplicationLambda: %v", err)
	}
	rels, _ := st.RelationshipsFrom(smaID)
	if len(rels) != 0 {
		t.Errorf("emitted %d edges, want 0", len(rels))
	}
}

// seedChimeSMA stores a SIP media application with the attributes the scanner
// stores, and returns its resource id.
func seedChimeSMA(t *testing.T, st *store.Store, region, id string) string {
	t.Helper()
	arn := fmt.Sprintf("arn:aws:chime:%s:%s:sma/%s", region, testAccountID, id)
	return upsertTestResource(t, st, "aws", testAccountID, TypeChimeSipMediaApplication, arn, region,
		mustJSON(cvtypes.SipMediaApplication{SipMediaApplicationArn: &arn, SipMediaApplicationId: &id, AwsRegion: &region}))
}

func seedChimeSipRule(t *testing.T, st *store.Store, id string, targets ...cvtypes.SipRuleTargetApplication) string {
	t.Helper()
	return upsertTestResource(t, st, "aws", testAccountID, TypeChimeSipRule, chimeSipRuleNativeID("aws", testRegion, testAccountID, id), testRegion,
		mustJSON(cvtypes.SipRule{SipRuleId: &id, TargetApplications: targets}))
}

func chimeTarget(region, id string) cvtypes.SipRuleTargetApplication {
	return cvtypes.SipRuleTargetApplication{AwsRegion: &region, SipMediaApplicationId: &id}
}

// A rule routes to each scanned target application, matched on region and id;
// a target in another region or naming an unscanned application gets no edge.
func TestResolveChimeSipRuleTargets_ScannedTargetsOnly(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	east := seedChimeSMA(t, st, testRegion, "sma-1")
	west := seedChimeSMA(t, st, "us-west-2", "sma-2")
	seedChimeSMA(t, st, "us-west-2", "sma-3")
	ruleID := seedChimeSipRule(t, st, "rule-1",
		chimeTarget(testRegion, "sma-1"), chimeTarget("us-west-2", "sma-2"),
		chimeTarget(testRegion, "sma-3"), chimeTarget(testRegion, "gone"))

	if err := resolveChimeSipRuleTargets(acct, st); err != nil {
		t.Fatalf("resolveChimeSipRuleTargets: %v", err)
	}
	rels, err := st.RelationshipsFrom(ruleID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	assertRelationship(t, rels, ruleID, east, store.RelRoutesTo)
	assertRelationship(t, rels, ruleID, west, store.RelRoutesTo)
	if len(rels) != 2 {
		t.Errorf("emitted %d edges, want 2: %v", len(rels), rels)
	}
}

func TestResolveChimeSipRuleTargets_NoAttrs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	seedChimeSMA(t, st, testRegion, "sma-1")
	ruleID := upsertTestResource(t, st, "aws", acct.ID, TypeChimeSipRule, chimeSipRuleNativeID("aws", testRegion, acct.ID, "r"), testRegion, "{}")

	if err := resolveChimeSipRuleTargets(acct, st); err != nil {
		t.Fatalf("resolveChimeSipRuleTargets: %v", err)
	}
	rels, _ := st.RelationshipsFrom(ruleID)
	if len(rels) != 0 {
		t.Errorf("emitted %d edges, want 0", len(rels))
	}
}

func TestResolveChimeSipRuleTargets_BadAttrsReportedOthersWired(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	smaID := seedChimeSMA(t, st, testRegion, "sma-1")
	before := seedChimeSipRule(t, st, "a", chimeTarget(testRegion, "sma-1"))
	upsertTestResource(t, st, "aws", acct.ID, TypeChimeSipRule, chimeSipRuleNativeID("aws", testRegion, acct.ID, "b"), testRegion, `{"TargetApplications":7}`)
	after := seedChimeSipRule(t, st, "c", chimeTarget(testRegion, "sma-1"))

	var typeErr *json.UnmarshalTypeError
	if err := resolveChimeSipRuleTargets(acct, st); !errors.As(err, &typeErr) {
		t.Errorf("err = %v, want a JSON type error", err)
	}
	for _, id := range []string{before, after} {
		rels, _ := st.RelationshipsFrom(id)
		assertRelationship(t, rels, id, smaID, store.RelRoutesTo)
	}
}

// seedChimeVC stores a voice connector with the attributes the scanner stores.
func seedChimeVC(t *testing.T, st *store.Store, region, id string) string {
	t.Helper()
	arn := fmt.Sprintf("arn:aws:chime:%s:%s:vc/%s", region, testAccountID, id)
	return upsertTestResource(t, st, "aws", testAccountID, TypeChimeVoiceConnector, arn, region,
		mustJSON(cvtypes.VoiceConnector{VoiceConnectorArn: &arn, VoiceConnectorId: &id}))
}

func seedChimeVCGroup(t *testing.T, st *store.Store, id string, vcIDs ...string) string {
	t.Helper()
	arn := fmt.Sprintf("arn:aws:chime:%s:%s:vcg/%s", testRegion, testAccountID, id)
	g := cvtypes.VoiceConnectorGroup{VoiceConnectorGroupArn: &arn, VoiceConnectorGroupId: &id}
	for _, vc := range vcIDs {
		g.VoiceConnectorItems = append(g.VoiceConnectorItems, cvtypes.VoiceConnectorItem{VoiceConnectorId: &vc})
	}
	return upsertTestResource(t, st, "aws", testAccountID, TypeChimeVoiceConnectorGroup, arn, testRegion, mustJSON(g))
}

// A group routes to every scanned connector it lists, in any region; an
// unscanned connector gets no edge.
func TestResolveChimeVoiceConnectorGroupItems_ScannedConnectorsOnly(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	east := seedChimeVC(t, st, testRegion, "vc-east")
	west := seedChimeVC(t, st, "us-west-2", "vc-west")
	groupID := seedChimeVCGroup(t, st, "g-1", "vc-east", "vc-west", "vc-gone")

	if err := resolveChimeVoiceConnectorGroupItems(acct, st); err != nil {
		t.Fatalf("resolveChimeVoiceConnectorGroupItems: %v", err)
	}
	rels, err := st.RelationshipsFrom(groupID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	assertRelationship(t, rels, groupID, east, store.RelRoutesTo)
	assertRelationship(t, rels, groupID, west, store.RelRoutesTo)
	if len(rels) != 2 {
		t.Errorf("emitted %d edges, want 2: %v", len(rels), rels)
	}
}

func TestResolveChimeVoiceConnectorGroupItems_NoAttrs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	seedChimeVC(t, st, testRegion, "vc-1")
	groupID := upsertTestResource(t, st, "aws", acct.ID, TypeChimeVoiceConnectorGroup,
		fmt.Sprintf("arn:aws:chime:%s:%s:vcg/g-1", testRegion, acct.ID), testRegion, "{}")

	if err := resolveChimeVoiceConnectorGroupItems(acct, st); err != nil {
		t.Fatalf("resolveChimeVoiceConnectorGroupItems: %v", err)
	}
	rels, _ := st.RelationshipsFrom(groupID)
	if len(rels) != 0 {
		t.Errorf("emitted %d edges, want 0", len(rels))
	}
}

func TestResolveChimeVoiceConnectorGroupItems_BadAttrsReportedOthersWired(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	vcID := seedChimeVC(t, st, testRegion, "vc-1")
	before := seedChimeVCGroup(t, st, "a", "vc-1")
	upsertTestResource(t, st, "aws", acct.ID, TypeChimeVoiceConnectorGroup,
		fmt.Sprintf("arn:aws:chime:%s:%s:vcg/b", testRegion, acct.ID), testRegion, `{"VoiceConnectorItems":7}`)
	after := seedChimeVCGroup(t, st, "c", "vc-1")

	var typeErr *json.UnmarshalTypeError
	if err := resolveChimeVoiceConnectorGroupItems(acct, st); !errors.As(err, &typeErr) {
		t.Errorf("err = %v, want a JSON type error", err)
	}
	for _, id := range []string{before, after} {
		rels, _ := st.RelationshipsFrom(id)
		assertRelationship(t, rels, id, vcID, store.RelRoutesTo)
	}
}

// A target row whose attributes do not decode is reported, and the other
// targets still get their edges.
func TestResolveChimeVoiceConnectorGroupItems_BadTargetAttrsReported(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	vcID := seedChimeVC(t, st, testRegion, "vc-1")
	upsertTestResource(t, st, "aws", acct.ID, TypeChimeVoiceConnector,
		fmt.Sprintf("arn:aws:chime:%s:%s:vc/vc-bad", testRegion, acct.ID), testRegion, `{"VoiceConnectorId":7}`)
	groupID := seedChimeVCGroup(t, st, "g-1", "vc-1")

	var typeErr *json.UnmarshalTypeError
	if err := resolveChimeVoiceConnectorGroupItems(acct, st); !errors.As(err, &typeErr) {
		t.Errorf("err = %v, want a JSON type error", err)
	}
	rels, _ := st.RelationshipsFrom(groupID)
	assertRelationship(t, rels, groupID, vcID, store.RelRoutesTo)
}

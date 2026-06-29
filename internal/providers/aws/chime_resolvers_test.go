package aws

import (
	"encoding/json"
	"fmt"
	"testing"

	"codeberg.org/icearp/disco/store"
	cmtypes "github.com/aws/aws-sdk-go-v2/service/chimesdkmessaging/types"
	cvtypes "github.com/aws/aws-sdk-go-v2/service/chimesdkvoice/types"
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

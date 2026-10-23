package aws

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/icearp/disco-cli/store"
)

func cbAttrs(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(b)
}

func TestResolveChatbotRelationships_RolesAndTopics(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	const (
		unscannedRole  = "arn:aws:iam::123456789012:role/unscanned"
		topicARN       = "arn:aws:sns:us-east-2:123456789012:alerts"
		unscannedTopic = "arn:aws:sns:us-east-2:123456789012:unscanned"
	)
	roleID := upsertTestResource(t, st, "aws", testAccountID, TypeIAMRole, cbRoleARN, "global", "{}")
	topicID := upsertTestResource(t, st, "aws", testAccountID, TypeSNSTopic, topicARN, "us-east-2", "{}")

	chime := cbChime(cbChimeARN1, "c1")
	chime.SnsTopicArns = []string{topicARN, unscannedTopic}
	chimeID := upsertTestResource(t, st, "aws", testAccountID, TypeChatbotChimeWebhookConfiguration, cbChimeARN1, "global", cbAttrs(t, chime))
	slackID := upsertTestResource(t, st, "aws", testAccountID, TypeChatbotSlackUserIdentity,
		chatbotSlackUserIdentityNativeID(cbSlackARN, "T01", "U1"), "global", cbAttrs(t, cbSlackUser(cbSlackARN, "U1")))
	teams := cbTeamsUser(cbTeamsUser1)
	teamsID := upsertTestResource(t, st, "aws", testAccountID, TypeChatbotMicrosoftTeamsUserIdentity,
		chatbotTeamsUserIdentityNativeID(cbTeamsARN, cbTeamsUser1), "global", cbAttrs(t, teams))
	unscanned := cbTeamsUser(cbTeamsUser2)
	unscanned.IamRoleArn = ptrStr(unscannedRole)
	unscannedID := upsertTestResource(t, st, "aws", testAccountID, TypeChatbotMicrosoftTeamsUserIdentity,
		chatbotTeamsUserIdentityNativeID(cbTeamsARN, cbTeamsUser2), "global", cbAttrs(t, unscanned))

	if err := resolveChatbotRelationships(acct, st); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	cases := []struct {
		name   string
		fromID string
		want   []string
	}{
		{"chime webhook", chimeID, []string{roleID, topicID}},
		{"slack identity", slackID, []string{roleID}},
		{"teams identity", teamsID, []string{roleID}},
		{"teams identity with unscanned role", unscannedID, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rels, err := st.RelationshipsFrom(c.fromID, store.RelUses)
			if err != nil {
				t.Fatalf("RelationshipsFrom: %v", err)
			}
			if len(rels) != len(c.want) {
				t.Errorf("uses edges = %v, want %d to %v", rels, len(c.want), c.want)
			}
			for _, to := range c.want {
				assertRelationship(t, rels, c.fromID, to, store.RelUses)
			}
		})
	}
}

func TestResolveChatbotRelationships_NoAttrs(t *testing.T) {
	st := newTestStore(t)
	upsertTestResource(t, st, "aws", testAccountID, TypeIAMRole, cbRoleARN, "global", "{}")
	for _, typ := range []string{TypeChatbotChimeWebhookConfiguration, TypeChatbotSlackUserIdentity, TypeChatbotMicrosoftTeamsUserIdentity} {
		id := upsertTestResource(t, st, "aws", testAccountID, typ, typ+"/x", "global", "{}")
		if err := resolveChatbotRelationships(newTestAccount(testAccountID), st); err != nil {
			t.Fatalf("resolve: %v", err)
		}
		rels, err := st.RelationshipsFrom(id, store.RelUses)
		if err != nil || len(rels) != 0 {
			t.Errorf("%s uses edges = %v (%v), want none", typ, rels, err)
		}
	}
}

// A row that fails to decode is reported, and costs no other row its edges.
func TestResolveChatbotRelationships_DecodeErrorReturned(t *testing.T) {
	st := newTestStore(t)
	roleID := upsertTestResource(t, st, "aws", testAccountID, TypeIAMRole, cbRoleARN, "global", "{}")
	// ListResources orders by name, so the good row is resolved after the bad one.
	goodNative := chatbotSlackUserIdentityNativeID(cbSlackARN, "T01", "U1")
	if _, err := st.UpsertResources([]*store.Resource{
		{
			Provider: "aws", AccountID: testAccountID, Type: TypeChatbotSlackUserIdentity, NativeID: "bad", Name: ptrStr("a"),
			Region: regionGlobal, AttributesJSON: `{"IamRoleArn":7}`, DiscoveredBy: testScanID,
		},
		{
			Provider: "aws", AccountID: testAccountID, Type: TypeChatbotSlackUserIdentity, NativeID: goodNative, Name: ptrStr("b"),
			Region: regionGlobal, AttributesJSON: cbAttrs(t, cbSlackUser(cbSlackARN, "U1")), DiscoveredBy: testScanID,
		},
	}); err != nil {
		t.Fatalf("UpsertResources: %v", err)
	}
	goodID := store.ResourceID("aws", testAccountID, goodNative)
	var syntaxErr *json.UnmarshalTypeError
	if err := resolveChatbotRelationships(newTestAccount(testAccountID), st); !errors.As(err, &syntaxErr) {
		t.Fatalf("resolve err = %v, want the decode error", err)
	}
	rels, err := st.RelationshipsFrom(goodID, store.RelUses)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 1 {
		t.Errorf("uses edges = %v, want 1 to %s", rels, roleID)
	}
	assertRelationship(t, rels, goodID, roleID, store.RelUses)
}

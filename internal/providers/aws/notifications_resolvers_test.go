package aws

import (
	"fmt"
	"testing"

	"codeberg.org/icearp/disco/store"
)

func TestResolveNotifChildrenToConfig(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	cARN := fmt.Sprintf("arn:aws:notifications::%s:configuration/c-1", acct.ID)
	cID := upsertTestResource(t, st, "aws", acct.ID, TypeNotificationsNotificationConfiguration, cARN, testRegion, "{}")
	chID := upsertTestResource(t, st, "aws", acct.ID, TypeNotificationsChannelAssociation,
		cARN+"/channel-association/arn:aws:chatbot::"+acct.ID+":chat-configuration/slack/foo", testRegion,
		fmt.Sprintf(`{"NotificationConfigurationArn":"%s"}`, cARN))
	erARN := fmt.Sprintf("arn:aws:notifications::%s:configuration/c-1/event-rule/er-1", acct.ID)
	erID := upsertTestResource(t, st, "aws", acct.ID, TypeNotificationsEventRule, erARN, testRegion,
		fmt.Sprintf(`{"NotificationConfigurationArn":"%s","EventType":"foo"}`, cARN))
	ouID := upsertTestResource(t, st, "aws", acct.ID, TypeNotificationsOrganizationalUnitAssociation,
		cARN+"/ou-association/ou-x", testRegion,
		fmt.Sprintf(`{"NotificationConfigurationArn":"%s","OrganizationalUnitId":"ou-x"}`, cARN))
	if err := resolveNotifChildrenToConfig(acct, st); err != nil {
		t.Fatalf("resolveNotifChildrenToConfig: %v", err)
	}
	for _, srcID := range []string{chID, erID, ouID} {
		rels, _ := st.RelationshipsFrom(srcID)
		assertRelationship(t, rels, srcID, cID, store.RelAttachedTo)
	}
}

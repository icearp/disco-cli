package aws

import (
	"testing"

	"github.com/icearp/disco-cli/store"
)

func TestResolveAutoScalingNotificationTopicEdges_ScannedTopic(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	topicID := upsertTestResource(t, st, "aws", testAccountID, TypeSNSTopic, asTestTopicA, testRegion, `{}`)
	otherTopicID := store.ResourceID("aws", testAccountID, asTestTopicB)
	scanned := upsertTestResource(t, st, "aws", testAccountID, TypeAutoScalingNotificationConfiguration, asTestNotifyWebA, testRegion,
		mustJSON(autoScalingNotificationConfiguration{AutoScalingGroupName: "web", TopicARN: asTestTopicA, NotificationTypes: []string{asTestLaunch}}))
	unscanned := upsertTestResource(t, st, "aws", testAccountID, TypeAutoScalingNotificationConfiguration, asTestNotifyOrphanB, testRegion,
		mustJSON(autoScalingNotificationConfiguration{AutoScalingGroupName: "gone", TopicARN: asTestTopicB, NotificationTypes: []string{asTestLaunch}}))

	if err := resolveAutoScalingRelationships(acct, st); err != nil {
		t.Fatalf("resolveAutoScalingRelationships: %v", err)
	}

	rels, err := st.RelationshipsFrom(scanned, store.RelUses)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 1 {
		t.Errorf("edges from %s = %d, want 1: %+v", asTestNotifyWebA, len(rels), rels)
	}
	assertRelationship(t, rels, scanned, topicID, store.RelUses)

	rels, err = st.RelationshipsFrom(unscanned, store.RelUses)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 0 {
		t.Errorf("edges from %s to unscanned topic %s = %+v, want none", asTestNotifyOrphanB, otherTopicID, rels)
	}
}

func TestResolveAutoScalingNotificationTopicEdges_NoAttrs(t *testing.T) {
	st := newTestStore(t)
	upsertTestResource(t, st, "aws", testAccountID, TypeSNSTopic, asTestTopicA, testRegion, `{}`)
	id := upsertTestResource(t, st, "aws", testAccountID, TypeAutoScalingNotificationConfiguration, asTestNotifyWebA, testRegion, `{}`)

	if err := resolveAutoScalingRelationships(newTestAccount(testAccountID), st); err != nil {
		t.Fatalf("resolveAutoScalingRelationships: %v", err)
	}
	rels, err := st.RelationshipsFrom(id, store.RelUses)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 0 {
		t.Errorf("edges from attr-less row = %+v, want none", rels)
	}
}

func TestResolveAutoScalingNotificationTopicEdges_UndecodableAttrsIsError(t *testing.T) {
	st := newTestStore(t)
	upsertTestResource(t, st, "aws", testAccountID, TypeAutoScalingNotificationConfiguration, asTestNotifyWebA, testRegion, `{"TopicARN":7}`)

	if err := resolveAutoScalingRelationships(newTestAccount(testAccountID), st); err == nil {
		t.Fatal("resolveAutoScalingRelationships = nil, want the decode error")
	}
}

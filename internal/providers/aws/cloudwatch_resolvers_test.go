package aws

import (
	"fmt"
	"testing"

	"codeberg.org/icearp/disco/internal/store"
)

const testCWRegion = "us-east-1"

// --- resolveAlarmSNSActions ---

func TestResolveAlarmSNSActions(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	alarmARN := fmt.Sprintf("arn:aws:cloudwatch:%s:%s:alarm:my-alarm", testCWRegion, testAccountID)
	topicARN := fmt.Sprintf("arn:aws:sns:%s:%s:my-topic", testCWRegion, testAccountID)
	attrsJSON := fmt.Sprintf(`{"AlarmArn":%q,"AlarmName":"my-alarm","AlarmActions":[%q],"OKActions":[],"InsufficientDataActions":[]}`, alarmARN, topicARN)

	alarmID := upsertTestResource(t, st, "aws", acct.ID, TypeCloudWatchAlarm, alarmARN, testCWRegion, attrsJSON)
	topicID := upsertTestResource(t, st, "aws", acct.ID, TypeSNSTopic, topicARN, testCWRegion, "{}")

	if err := resolveAlarmSNSActions(acct, st); err != nil {
		t.Fatalf("resolveAlarmSNSActions: %v", err)
	}

	rels, err := st.RelationshipsFrom(alarmID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 1 {
		t.Fatalf("expected 1 relationship, got %d", len(rels))
	}
	assertRelationship(t, rels, alarmID, topicID, "uses")
}

func TestResolveAlarmSNSActions_MultipleActions(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	alarmARN := fmt.Sprintf("arn:aws:cloudwatch:%s:%s:alarm:multi-alarm", testCWRegion, testAccountID)
	topic1ARN := fmt.Sprintf("arn:aws:sns:%s:%s:topic-1", testCWRegion, testAccountID)
	topic2ARN := fmt.Sprintf("arn:aws:sns:%s:%s:topic-2", testCWRegion, testAccountID)
	// Same topic in both AlarmActions and OKActions — deduplication should produce one edge.
	attrsJSON := fmt.Sprintf(`{"AlarmArn":%q,"AlarmName":"multi-alarm","AlarmActions":[%q,%q],"OKActions":[%q],"InsufficientDataActions":[]}`,
		alarmARN, topic1ARN, topic2ARN, topic1ARN)

	alarmID := upsertTestResource(t, st, "aws", acct.ID, TypeCloudWatchAlarm, alarmARN, testCWRegion, attrsJSON)
	topic1ID := upsertTestResource(t, st, "aws", acct.ID, TypeSNSTopic, topic1ARN, testCWRegion, "{}")
	topic2ID := upsertTestResource(t, st, "aws", acct.ID, TypeSNSTopic, topic2ARN, testCWRegion, "{}")

	if err := resolveAlarmSNSActions(acct, st); err != nil {
		t.Fatalf("resolveAlarmSNSActions: %v", err)
	}

	rels, err := st.RelationshipsFrom(alarmID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 2 {
		t.Fatalf("expected 2 relationships, got %d", len(rels))
	}
	assertRelationship(t, rels, alarmID, topic1ID, "uses")
	assertRelationship(t, rels, alarmID, topic2ID, "uses")
}

func TestResolveAlarmSNSActions_NoAttrs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	alarmARN := fmt.Sprintf("arn:aws:cloudwatch:%s:%s:alarm:empty-alarm", testCWRegion, testAccountID)
	upsertTestResource(t, st, "aws", acct.ID, TypeCloudWatchAlarm, alarmARN, testCWRegion, "{}")

	if err := resolveAlarmSNSActions(acct, st); err != nil {
		t.Fatalf("resolveAlarmSNSActions with empty attrs: %v", err)
	}
}

// --- resolveCompositeAlarmChildren ---

func TestResolveCompositeAlarmChildren_ByARN(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	childARN := fmt.Sprintf("arn:aws:cloudwatch:%s:%s:alarm:child-alarm", testCWRegion, testAccountID)
	compositeARN := fmt.Sprintf("arn:aws:cloudwatch:%s:%s:alarm:composite-alarm", testCWRegion, testAccountID)
	alarmRule := fmt.Sprintf(`ALARM(%q)`, childARN)
	compositeAttrs := fmt.Sprintf(`{"AlarmArn":%q,"AlarmName":"composite-alarm","AlarmRule":%q}`, compositeARN, alarmRule)

	childID := upsertTestResource(t, st, "aws", acct.ID, TypeCloudWatchAlarm, childARN, testCWRegion,
		fmt.Sprintf(`{"AlarmArn":%q,"AlarmName":"child-alarm"}`, childARN))
	compositeID := upsertTestResource(t, st, "aws", acct.ID, TypeCloudWatchCompositeAlarm, compositeARN, testCWRegion, compositeAttrs)

	if err := resolveCompositeAlarmChildren(acct, st); err != nil {
		t.Fatalf("resolveCompositeAlarmChildren: %v", err)
	}

	rels, err := st.RelationshipsFrom(compositeID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 1 {
		t.Fatalf("expected 1 relationship, got %d", len(rels))
	}
	assertRelationship(t, rels, compositeID, childID, "contains")
}

func TestResolveCompositeAlarmChildren_ByName(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	childARN := fmt.Sprintf("arn:aws:cloudwatch:%s:%s:alarm:named-child", testCWRegion, testAccountID)
	compositeARN := fmt.Sprintf("arn:aws:cloudwatch:%s:%s:alarm:named-composite", testCWRegion, testAccountID)
	// AlarmRule references the child by plain name rather than ARN.
	compositeAttrs := fmt.Sprintf(`{"AlarmArn":%q,"AlarmName":"named-composite","AlarmRule":"ALARM(\"named-child\")"}`, compositeARN)

	// Insert the child alarm with Name set so the name-based lookup in the
	// resolver can find it. upsertTestResource does not set Name, so we
	// call UpsertResource directly here.
	childName := "named-child"
	region := testCWRegion
	if _, err := st.UpsertResource(&store.Resource{
		Provider: "aws", AccountID: acct.ID,
		Type:           TypeCloudWatchAlarm,
		NativeID:       childARN,
		Name:           &childName,
		Region:         &region,
		AttributesJSON: fmt.Sprintf(`{"AlarmArn":%q,"AlarmName":"named-child"}`, childARN),
		DiscoveredBy:   testScanID,
	}); err != nil {
		t.Fatalf("UpsertResource child: %v", err)
	}
	childID := store.ResourceID("aws", acct.ID, TypeCloudWatchAlarm, childARN)
	compositeID := upsertTestResource(t, st, "aws", acct.ID, TypeCloudWatchCompositeAlarm, compositeARN, testCWRegion, compositeAttrs)

	if err := resolveCompositeAlarmChildren(acct, st); err != nil {
		t.Fatalf("resolveCompositeAlarmChildren: %v", err)
	}

	rels, err := st.RelationshipsFrom(compositeID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 1 {
		t.Fatalf("expected 1 relationship, got %d", len(rels))
	}
	assertRelationship(t, rels, compositeID, childID, "contains")
}

func TestResolveCompositeAlarmChildren_NoRule(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	compositeARN := fmt.Sprintf("arn:aws:cloudwatch:%s:%s:alarm:empty-composite", testCWRegion, testAccountID)
	upsertTestResource(t, st, "aws", acct.ID, TypeCloudWatchCompositeAlarm, compositeARN, testCWRegion, "{}")

	if err := resolveCompositeAlarmChildren(acct, st); err != nil {
		t.Fatalf("resolveCompositeAlarmChildren with empty attrs: %v", err)
	}
}

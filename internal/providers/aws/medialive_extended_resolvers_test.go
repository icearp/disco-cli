package aws

import (
	"fmt"
	"testing"

	"codeberg.org/icearp/disco/internal/store"
)

func TestResolveMediaLiveCWAlarmTemplateGroup(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	gARN := fmt.Sprintf("arn:aws:medialive:%s:%s:cloudwatch-alarm-template-group:g1", testRegion, acct.ID)
	gID := upsertTestResource(t, st, "aws", acct.ID, TypeMediaLiveCloudWatchAlarmTemplateGroup, gARN, testRegion, `{"Id":"g1"}`)
	tARN := fmt.Sprintf("arn:aws:medialive:%s:%s:cloudwatch-alarm-template:t1", testRegion, acct.ID)
	tID := upsertTestResource(t, st, "aws", acct.ID, TypeMediaLiveCloudWatchAlarmTemplate, tARN, testRegion, `{"Id":"t1","GroupId":"g1"}`)
	if err := resolveMediaLiveCWAlarmTemplateGroup(acct, st); err != nil {
		t.Fatalf("resolveMediaLiveCWAlarmTemplateGroup: %v", err)
	}
	rels, _ := st.RelationshipsFrom(tID)
	assertRelationship(t, rels, tID, gID, store.RelAttachedTo)
}

func TestResolveMediaLiveEBRuleTemplateGroup(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	gARN := fmt.Sprintf("arn:aws:medialive:%s:%s:eventbridge-rule-template-group:g1", testRegion, acct.ID)
	gID := upsertTestResource(t, st, "aws", acct.ID, TypeMediaLiveEventBridgeRuleTemplateGroup, gARN, testRegion, `{"Id":"g1"}`)
	tARN := fmt.Sprintf("arn:aws:medialive:%s:%s:eventbridge-rule-template:t1", testRegion, acct.ID)
	tID := upsertTestResource(t, st, "aws", acct.ID, TypeMediaLiveEventBridgeRuleTemplate, tARN, testRegion, `{"Id":"t1","GroupId":"g1"}`)
	if err := resolveMediaLiveEBRuleTemplateGroup(acct, st); err != nil {
		t.Fatalf("resolveMediaLiveEBRuleTemplateGroup: %v", err)
	}
	rels, _ := st.RelationshipsFrom(tID)
	assertRelationship(t, rels, tID, gID, store.RelAttachedTo)
}

func TestResolveMediaLiveClusterRefs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	roleARN := fmt.Sprintf("arn:aws:iam::%s:role/Cluster", acct.ID)
	rID := upsertTestResource(t, st, "aws", acct.ID, TypeIAMRole, roleARN, testRegion, "{}")
	nARN := fmt.Sprintf("arn:aws:medialive:%s:%s:network:n1", testRegion, acct.ID)
	nID := upsertTestResource(t, st, "aws", acct.ID, TypeMediaLiveNetwork, nARN, testRegion, `{"Id":"n1"}`)
	cARN := fmt.Sprintf("arn:aws:medialive:%s:%s:cluster:c1", testRegion, acct.ID)
	attrs := fmt.Sprintf(`{"InstanceRoleArn":%q,"NetworkSettings":{"InterfaceMappings":[{"NetworkId":"n1"}]}}`, roleARN)
	cID := upsertTestResource(t, st, "aws", acct.ID, TypeMediaLiveCluster, cARN, testRegion, attrs)
	if err := resolveMediaLiveClusterRefs(acct, st); err != nil {
		t.Fatalf("resolveMediaLiveClusterRefs: %v", err)
	}
	rels, _ := st.RelationshipsFrom(cID)
	assertRelationship(t, rels, cID, rID, store.RelAssumes)
	assertRelationship(t, rels, cID, nID, store.RelAttachedTo)
}

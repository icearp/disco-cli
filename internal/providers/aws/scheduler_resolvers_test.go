package aws

import (
	"fmt"
	"testing"

	"codeberg.org/icearp/disco/internal/store"
)

func TestResolveSchedulerScheduleTargets_LambdaAndGroup(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	fnARN := fmt.Sprintf("arn:aws:lambda:%s:%s:function:job", testRegion, acct.ID)
	fnID := upsertTestResource(t, st, "aws", acct.ID, TypeLambdaFunction, fnARN, testRegion, `{}`)

	groupARN := fmt.Sprintf("arn:aws:scheduler:%s:%s:schedule-group/default", testRegion, acct.ID)
	groupID := upsertTestResource(t, st, "aws", acct.ID, TypeSchedulerScheduleGroup, groupARN, testRegion, `{}`)

	schedARN := fmt.Sprintf("arn:aws:scheduler:%s:%s:schedule/default/job", testRegion, acct.ID)
	attrs := fmt.Sprintf(`{"GroupName":"default","Target":{"Arn":%q}}`, fnARN)
	schedID := upsertTestResource(t, st, "aws", acct.ID, TypeSchedulerSchedule, schedARN, testRegion, attrs)

	if err := resolveSchedulerScheduleTargets(acct, st); err != nil {
		t.Fatalf("resolveSchedulerScheduleTargets: %v", err)
	}
	rels, err := st.RelationshipsFrom(schedID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 2 {
		t.Fatalf("expected 2 relationships, got %d (%+v)", len(rels), rels)
	}
	assertRelationship(t, rels, schedID, groupID, store.RelAttachedTo)
	assertRelationship(t, rels, schedID, fnID, store.RelRoutesTo)
}

func TestResolveSchedulerScheduleTargets_UnscannedTargetSkipped(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	schedARN := fmt.Sprintf("arn:aws:scheduler:%s:%s:schedule/default/missing", testRegion, acct.ID)
	attrs := `{"GroupName":"default","Target":{"Arn":"arn:aws:wafv2:us-east-1:131546573061:regional/webacl/foo"}}`
	schedID := upsertTestResource(t, st, "aws", acct.ID, TypeSchedulerSchedule, schedARN, testRegion, attrs)

	if err := resolveSchedulerScheduleTargets(acct, st); err != nil {
		t.Fatalf("resolveSchedulerScheduleTargets: %v", err)
	}
	rels, _ := st.RelationshipsFrom(schedID)
	if len(rels) != 0 {
		t.Errorf("expected 0 relationships for unmapped target type, got %d", len(rels))
	}
}

func TestResolveSchedulerScheduleTargets_EmptyAttrs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	schedARN := fmt.Sprintf("arn:aws:scheduler:%s:%s:schedule/default/bare", testRegion, acct.ID)
	upsertTestResource(t, st, "aws", acct.ID, TypeSchedulerSchedule, schedARN, testRegion, `{}`)

	if err := resolveSchedulerScheduleTargets(acct, st); err != nil {
		t.Fatalf("resolveSchedulerScheduleTargets: %v", err)
	}
}

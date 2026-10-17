package aws

import (
	"fmt"
	"testing"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	snstypes "github.com/aws/aws-sdk-go-v2/service/sns/types"
	"github.com/icearp/disco-cli/store"
)

func TestResolveSNSSubscriptionRefs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	topicARN := fmt.Sprintf("arn:aws:sns:%s:%s:my-topic", testRegion, acct.ID)
	topicID := upsertTestResource(t, st, "aws", acct.ID, TypeSNSTopic, topicARN, testRegion, "{}")
	fnARN := fmt.Sprintf("arn:aws:lambda:%s:%s:function:my-fn", testRegion, acct.ID)
	fnID := upsertTestResource(t, st, "aws", acct.ID, TypeLambdaFunction, fnARN, testRegion, "{}")
	qARN := fmt.Sprintf("arn:aws:sqs:%s:%s:my-q", testRegion, acct.ID)
	qID := upsertTestResource(t, st, "aws", acct.ID, TypeSQSQueue, qARN, testRegion, "{}")

	subL := topicARN + ":sub-l"
	subLID := upsertTestResource(t, st, "aws", acct.ID, TypeSNSSubscription, subL, testRegion, fmt.Sprintf(`{"TopicArn":"%s","Endpoint":"%s","Protocol":"lambda"}`, topicARN, fnARN))
	subQ := topicARN + ":sub-q"
	subQID := upsertTestResource(t, st, "aws", acct.ID, TypeSNSSubscription, subQ, testRegion, fmt.Sprintf(`{"TopicArn":"%s","Endpoint":"%s","Protocol":"sqs"}`, topicARN, qARN))

	if err := resolveSNSSubscriptionRefs(acct, st); err != nil {
		t.Fatalf("resolveSNSSubscriptionRefs: %v", err)
	}
	rels, _ := st.RelationshipsFrom(subLID)
	assertRelationship(t, rels, subLID, topicID, store.RelAttachedTo)
	assertRelationship(t, rels, subLID, fnID, store.RelUses)
	rels, _ = st.RelationshipsFrom(subQID)
	assertRelationship(t, rels, subQID, topicID, store.RelAttachedTo)
	assertRelationship(t, rels, subQID, qID, store.RelUses)
}

func TestResolveSNSTopicPolicyToTopic(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	topicARN := fmt.Sprintf("arn:aws:sns:%s:%s:t1", testRegion, acct.ID)
	topicID := upsertTestResource(t, st, "aws", acct.ID, TypeSNSTopic, topicARN, testRegion, "{}")
	pID := upsertTestResource(t, st, "aws", acct.ID, TypeSNSTopicPolicy, topicARN+"/policy", testRegion, "{}")
	if err := resolveSNSTopicPolicyToTopic(acct, st); err != nil {
		t.Fatalf("resolveSNSTopicPolicyToTopic: %v", err)
	}
	rels, _ := st.RelationshipsFrom(pID)
	assertRelationship(t, rels, pID, topicID, store.RelAttachedTo)
}

func TestResolveSNSPlatformApplicationRefs_TopicsAndRoles(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	topicIDs := map[string]string{}
	for _, key := range []string{"EventEndpointCreated", "EventEndpointDeleted", "EventEndpointUpdated", "EventDeliveryFailure"} {
		arn := "arn:aws:sns:us-east-1:123456789012:" + key
		topicIDs[key] = upsertTestResource(t, st, "aws", acct.ID, TypeSNSTopic, arn, testRegion, "{}")
	}
	okRole := "arn:aws:iam::123456789012:role/success"
	roleID := upsertTestResource(t, st, "aws", acct.ID, TypeIAMRole, okRole, "", "{}")
	failRole := "arn:aws:iam::123456789012:role/failure"
	failRoleID := upsertTestResource(t, st, "aws", acct.ID, TypeIAMRole, failRole, "", "{}")
	attrs := map[string]string{
		"SuccessFeedbackRoleArn": okRole, "FailureFeedbackRoleArn": failRole,
		"PlatformCredential": "arn:aws:sns:us-east-1:123456789012:not-a-ref",
	}
	for key := range topicIDs {
		attrs[key] = "arn:aws:sns:us-east-1:123456789012:" + key
	}
	appID := upsertTestResource(t, st, "aws", acct.ID, TypeSNSPlatformApplication, snsAppARN1, testRegion,
		mustJSON(snstypes.PlatformApplication{PlatformApplicationArn: sdkaws.String(snsAppARN1), Attributes: attrs}))
	bareID := upsertTestResource(t, st, "aws", acct.ID, TypeSNSPlatformApplication, snsAppARN2, testRegion,
		mustJSON(snstypes.PlatformApplication{PlatformApplicationArn: sdkaws.String(snsAppARN2)}))

	if err := resolveSNSPlatformApplicationRefs(acct, st); err != nil {
		t.Fatalf("resolveSNSPlatformApplicationRefs: %v", err)
	}
	rels, err := st.RelationshipsFrom(appID)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range topicIDs {
		assertRelationship(t, rels, appID, id, store.RelUses)
	}
	assertRelationship(t, rels, appID, roleID, store.RelUses)
	assertRelationship(t, rels, appID, failRoleID, store.RelUses)
	if len(rels) != 6 {
		t.Errorf("edges from the application = %d; want 6 (4 topics, 2 roles)", len(rels))
	}
	if bare, _ := st.RelationshipsFrom(bareID); len(bare) != 0 {
		t.Errorf("application without attributes has edges %v; want none", bare)
	}
}

func TestResolveSNSPlatformApplicationRefs_DecodeError(t *testing.T) {
	st := newTestStore(t)
	upsertTestResource(t, st, "aws", testAccountID, TypeSNSPlatformApplication, snsAppARN1, testRegion, `{"Attributes":"not-a-map"}`)
	if err := resolveSNSPlatformApplicationRefs(newTestAccount(testAccountID), st); err == nil {
		t.Error("err = nil; want the decode error returned")
	}
}

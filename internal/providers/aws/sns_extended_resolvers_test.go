package aws

import (
	"fmt"
	"testing"

	"codeberg.org/icearp/disco/internal/store"
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

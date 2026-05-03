package aws

import (
	"fmt"
	"testing"

	"codeberg.org/icearp/disco/internal/store"
)

func TestLogGroupARNFromChild(t *testing.T) {
	cases := []struct{ in, want string }{
		{"arn:aws:logs:us-east-1:123:log-group:/svc/foo:*/stream/2024-01-01", "arn:aws:logs:us-east-1:123:log-group:/svc/foo:*"},
		{"arn:aws:logs:us-east-1:123:log-group:/svc/foo/filter/my-filter", "arn:aws:logs:us-east-1:123:log-group:/svc/foo"},
		{"arn:aws:logs:us-east-1:123:log-group:/svc/foo/subscription/sub-1", "arn:aws:logs:us-east-1:123:log-group:/svc/foo"},
		{"arn:aws:logs:us-east-1:123:log-group:/svc/foo", ""},
	}
	for _, c := range cases {
		if got := logGroupARNFromChild(c.in); got != c.want {
			t.Errorf("logGroupARNFromChild(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestResolveLogsLogStreamParent(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	lgARN := logGroupNativeIDFromName(acct.ID, testRegion, "/svc/foo")
	lgID := upsertTestResource(t, st, "aws", acct.ID, TypeLogsLogGroup, lgARN, testRegion, "{}")
	lsARN := lgARN + "/stream/2024-01-01"
	lsID := upsertTestResource(t, st, "aws", acct.ID, TypeLogsLogStream, lsARN, testRegion, "{}")
	if err := resolveLogsLogStreamParent(acct, st); err != nil {
		t.Fatalf("resolveLogsLogStreamParent: %v", err)
	}
	rels, _ := st.RelationshipsFrom(lsID)
	assertRelationship(t, rels, lsID, lgID, store.RelAttachedTo)
}

func TestResolveLogsSubscriptionFilterRefs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	lgARN := logGroupNativeIDFromName(acct.ID, testRegion, "/svc/bar")
	lgID := upsertTestResource(t, st, "aws", acct.ID, TypeLogsLogGroup, lgARN, testRegion, "{}")
	fnARN := fmt.Sprintf("arn:aws:lambda:%s:%s:function:my-fn", testRegion, acct.ID)
	fnID := upsertTestResource(t, st, "aws", acct.ID, TypeLambdaFunction, fnARN, testRegion, "{}")

	subARN := lgARN + "/subscription/sub-1"
	attrs := fmt.Sprintf(`{"DestinationArn":%q}`, fnARN)
	subID := upsertTestResource(t, st, "aws", acct.ID, TypeLogsSubscriptionFilter, subARN, testRegion, attrs)

	if err := resolveLogsSubscriptionFilterRefs(acct, st); err != nil {
		t.Fatalf("resolveLogsSubscriptionFilterRefs: %v", err)
	}
	rels, _ := st.RelationshipsFrom(subID)
	assertRelationship(t, rels, subID, lgID, store.RelAttachedTo)
	assertRelationship(t, rels, subID, fnID, store.RelRoutesTo)
}

func TestResolveLogsDestinationTargets(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	streamARN := fmt.Sprintf("arn:aws:kinesis:%s:%s:stream/my-stream", testRegion, acct.ID)
	streamID := upsertTestResource(t, st, "aws", acct.ID, TypeKinesisStream, streamARN, testRegion, "{}")
	roleARN := fmt.Sprintf("arn:aws:iam::%s:role/dest-role", acct.ID)
	roleID := upsertTestResource(t, st, "aws", acct.ID, TypeIAMRole, roleARN, "", "{}")
	destARN := fmt.Sprintf("arn:aws:logs:%s:%s:destination:my-dest", testRegion, acct.ID)
	attrs := fmt.Sprintf(`{"TargetArn":%q,"RoleArn":%q}`, streamARN, roleARN)
	destID := upsertTestResource(t, st, "aws", acct.ID, TypeLogsDestination, destARN, testRegion, attrs)
	if err := resolveLogsDestinationTargets(acct, st); err != nil {
		t.Fatalf("resolveLogsDestinationTargets: %v", err)
	}
	rels, _ := st.RelationshipsFrom(destID)
	assertRelationship(t, rels, destID, streamID, store.RelRoutesTo)
	assertRelationship(t, rels, destID, roleID, store.RelAssumes)
}

func TestResolveLogsQueryDefinitionLogGroups(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	lgARN := logGroupNativeIDFromName(acct.ID, testRegion, "/svc/q")
	lgID := upsertTestResource(t, st, "aws", acct.ID, TypeLogsLogGroup, lgARN, testRegion, "{}")
	qID := upsertTestResource(t, st, "aws", acct.ID, TypeLogsQueryDefinition, "qd-1", testRegion, `{"LogGroupNames":["/svc/q"]}`)
	if err := resolveLogsQueryDefinitionLogGroups(acct, st); err != nil {
		t.Fatalf("resolveLogsQueryDefinitionLogGroups: %v", err)
	}
	rels, _ := st.RelationshipsFrom(qID)
	assertRelationship(t, rels, qID, lgID, store.RelUses)
}

package aws

import (
	"fmt"
	"testing"

	"codeberg.org/icearp/disco/store"
)

func TestResolveKAV1ChildrenToApp(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	appARN := fmt.Sprintf("arn:aws:kinesisanalytics:%s:%s:application/app1", testRegion, acct.ID)
	appID := upsertTestResource(t, st, "aws", acct.ID, TypeKinesisAnalyticsApplication, appARN, testRegion, "{}")
	outARN := appARN + "/output/o1"
	outID := upsertTestResource(t, st, "aws", acct.ID, TypeKinesisAnalyticsApplicationOutput, outARN, testRegion, "{}")
	refARN := appARN + "/reference-data-source/r1"
	refID := upsertTestResource(t, st, "aws", acct.ID, TypeKinesisAnalyticsApplicationReferenceData, refARN, testRegion, "{}")
	if err := resolveKAV1ChildrenToApp(acct, st); err != nil {
		t.Fatalf("resolveKAV1ChildrenToApp: %v", err)
	}
	for _, c := range []string{outID, refID} {
		rels, _ := st.RelationshipsFrom(c)
		assertRelationship(t, rels, c, appID, store.RelAttachedTo)
	}
}

func TestResolveKAV2ChildrenToApp(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	appARN := fmt.Sprintf("arn:aws:kinesisanalytics:%s:%s:application/app1", testRegion, acct.ID)
	appID := upsertTestResource(t, st, "aws", acct.ID, TypeKAV2Application, appARN, testRegion, "{}")
	cwARN := appARN + "/cloud-watch-logging-option/c1"
	cwID := upsertTestResource(t, st, "aws", acct.ID, TypeKAV2ApplicationCloudWatchLogOpt, cwARN, testRegion, "{}")
	outARN := appARN + "/output/o1"
	outID := upsertTestResource(t, st, "aws", acct.ID, TypeKAV2ApplicationOutput, outARN, testRegion, "{}")
	if err := resolveKAV2ChildrenToApp(acct, st); err != nil {
		t.Fatalf("resolveKAV2ChildrenToApp: %v", err)
	}
	for _, c := range []string{cwID, outID} {
		rels, _ := st.RelationshipsFrom(c)
		assertRelationship(t, rels, c, appID, store.RelAttachedTo)
	}
}

func TestResolveKAV1AppLogging(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	appARN := fmt.Sprintf("arn:aws:kinesisanalytics:%s:%s:application/app1", testRegion, acct.ID)
	roleARN := fmt.Sprintf("arn:aws:iam::%s:role/kinesisanalytics", acct.ID)
	lgARN := logGroupNativeIDFromName(acct.ID, testRegion, "/aws/kinesis-analytics/app1")
	streamARN := lgARN + ":log-stream:cloudwatch"
	attrs := fmt.Sprintf(`{"CloudWatchLoggingOptionDescriptions":[{"LogStreamARN":%q,"RoleARN":%q}]}`, streamARN, roleARN)

	aID := upsertTestResource(t, st, "aws", acct.ID, TypeKinesisAnalyticsApplication, appARN, testRegion, attrs)
	rID := upsertTestResource(t, st, "aws", acct.ID, TypeIAMRole, roleARN, testRegion, "{}")
	lID := upsertTestResource(t, st, "aws", acct.ID, TypeLogsLogGroup, lgARN, testRegion, "{}")

	if err := resolveKAV1AppLogging(acct, st); err != nil {
		t.Fatalf("resolveKAV1AppLogging: %v", err)
	}
	rels, _ := st.RelationshipsFrom(aID)
	assertRelationship(t, rels, aID, rID, store.RelAssumes)
	assertRelationship(t, rels, aID, lID, store.RelUses)
}

func TestResolveKAV2AppRefs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	appARN := fmt.Sprintf("arn:aws:kinesisanalytics:%s:%s:application/app2", testRegion, acct.ID)
	roleARN := fmt.Sprintf("arn:aws:iam::%s:role/kdav2-svc", acct.ID)
	lgARN := logGroupNativeIDFromName(acct.ID, testRegion, "/aws/kinesis-analytics-v2/app2")
	streamARN := lgARN + ":log-stream:cloudwatch"
	attrs := fmt.Sprintf(`{"ServiceExecutionRole":%q,"CloudWatchLoggingOptionDescriptions":[{"LogStreamARN":%q}]}`, roleARN, streamARN)

	aID := upsertTestResource(t, st, "aws", acct.ID, TypeKAV2Application, appARN, testRegion, attrs)
	rID := upsertTestResource(t, st, "aws", acct.ID, TypeIAMRole, roleARN, testRegion, "{}")
	lID := upsertTestResource(t, st, "aws", acct.ID, TypeLogsLogGroup, lgARN, testRegion, "{}")

	if err := resolveKAV2AppRefs(acct, st); err != nil {
		t.Fatalf("resolveKAV2AppRefs: %v", err)
	}
	rels, _ := st.RelationshipsFrom(aID)
	assertRelationship(t, rels, aID, rID, store.RelAssumes)
	assertRelationship(t, rels, aID, lID, store.RelUses)
}

package aws

import (
	"fmt"
	"testing"

	"codeberg.org/icearp/disco/internal/store"
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

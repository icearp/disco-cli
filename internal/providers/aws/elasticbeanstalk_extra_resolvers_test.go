package aws

import (
	"testing"

	"codeberg.org/icearp/disco/store"
)

func TestResolveBeanstalkApplicationVersionRelationships(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	region := "us-east-1"

	appARN := "arn:aws:elasticbeanstalk:" + region + ":" + testAccountID + ":application/my-app"
	appID := upsertTestResourceNamed(t, st, TypeBeanstalkApplication, appARN, region, "{}", "my-app")
	verARN := "arn:aws:elasticbeanstalk:" + region + ":" + testAccountID + ":applicationversion/my-app/v1"
	verID := upsertTestResource(t, st, "aws", acct.ID, TypeBeanstalkApplicationVersion, verARN, region, `{"ApplicationName":"my-app"}`)

	if err := resolveBeanstalkApplicationVersionRelationships(acct, st); err != nil {
		t.Fatalf("resolveBeanstalkApplicationVersionRelationships: %v", err)
	}
	rels, _ := st.RelationshipsFrom(verID)
	assertRelationship(t, rels, verID, appID, store.RelAttachedTo)
}

func TestResolveBeanstalkApplicationVersionRelationships_NoAttrs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	region := "us-east-1"
	verARN := "arn:aws:elasticbeanstalk:" + region + ":" + testAccountID + ":applicationversion/my-app/v1"
	verID := upsertTestResource(t, st, "aws", acct.ID, TypeBeanstalkApplicationVersion, verARN, region, "{}")
	if err := resolveBeanstalkApplicationVersionRelationships(acct, st); err != nil {
		t.Fatalf("empty: %v", err)
	}
	if rels, _ := st.RelationshipsFrom(verID); len(rels) != 0 {
		t.Errorf("emitted %d edges, want 0", len(rels))
	}
}

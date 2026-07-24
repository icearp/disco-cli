package aws

import (
	"fmt"
	"testing"

	"github.com/icearp/disco-cli/store"
)

const (
	testEBAppName = "myapp"
	testEBEnvName = "myapp-prod"
)

func ebApplicationARN() string {
	return fmt.Sprintf("arn:aws:elasticbeanstalk:%s:%s:application/%s", testRegion, testAccountID, testEBAppName)
}

func ebEnvironmentARN() string {
	return fmt.Sprintf("arn:aws:elasticbeanstalk:%s:%s:environment/%s/%s", testRegion, testAccountID, testEBAppName, testEBEnvName)
}

// TestResolveBeanstalkEnvironmentTargets verifies application → environment
// `contains` edge lands when both are scanned.
func TestResolveBeanstalkEnvironmentTargets(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	// Application — bypass upsertTestResource to set Name (resolver
	// keys by app name, not by ARN, since EnvironmentDescription only
	// carries ApplicationName).
	appName := testEBAppName
	region := testRegion
	appR := &store.Resource{
		Provider: "aws", AccountID: acct.ID, Type: TypeBeanstalkApplication,
		NativeID: ebApplicationARN(), Region: &region, Name: &appName,
		AttributesJSON: `{}`, DiscoveredBy: testScanID,
	}
	if _, err := st.UpsertResource(appR); err != nil {
		t.Fatalf("upsert app: %v", err)
	}
	appID := store.ResourceID("aws", acct.ID, ebApplicationARN())

	envAttrs := fmt.Sprintf(`{"EnvironmentArn":%q,"EnvironmentName":%q,"ApplicationName":%q,"Status":"Ready"}`,
		ebEnvironmentARN(), testEBEnvName, testEBAppName)
	envID := upsertTestResource(t, st, "aws", acct.ID, TypeBeanstalkEnvironment, ebEnvironmentARN(), testRegion, envAttrs)

	if err := resolveBeanstalkEnvironmentTargets(acct, st); err != nil {
		t.Fatalf("resolveBeanstalkEnvironmentTargets: %v", err)
	}
	rels, err := st.RelationshipsFrom(appID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	assertRelationship(t, rels, appID, envID, store.RelContains)
}

// TestResolveBeanstalkEnvironmentTargets_FKSafe verifies environments
// with no scanned application skip without erroring.
func TestResolveBeanstalkEnvironmentTargets_FKSafe(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	// Seed at least one app with a different name so the resolver
	// iterates rather than fast-pathing past on empty index.
	otherName := "other-app"
	region := testRegion
	otherR := &store.Resource{
		Provider: "aws", AccountID: acct.ID, Type: TypeBeanstalkApplication,
		NativeID: "arn:aws:elasticbeanstalk:us-east-1:123456789012:application/other-app",
		Region:   &region, Name: &otherName, AttributesJSON: `{}`, DiscoveredBy: testScanID,
	}
	if _, err := st.UpsertResource(otherR); err != nil {
		t.Fatalf("upsert other app: %v", err)
	}

	envAttrs := fmt.Sprintf(`{"ApplicationName":%q}`, testEBAppName)
	envID := upsertTestResource(t, st, "aws", acct.ID, TypeBeanstalkEnvironment, ebEnvironmentARN(), testRegion, envAttrs)

	if err := resolveBeanstalkEnvironmentTargets(acct, st); err != nil {
		t.Fatalf("resolveBeanstalkEnvironmentTargets: %v", err)
	}
	rels, err := st.RelationshipsFrom(envID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 0 {
		t.Errorf("expected zero edges, got %d", len(rels))
	}
}

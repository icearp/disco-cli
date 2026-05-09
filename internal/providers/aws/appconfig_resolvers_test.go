package aws

import (
	"fmt"
	"testing"

	"codeberg.org/icearp/disco/store"
)

func TestAppconfigAppARNFromChild(t *testing.T) {
	cases := []struct{ in, want string }{
		{"arn:aws:appconfig:us-east-1:123:application/a1/environment/e1", "arn:aws:appconfig:us-east-1:123:application/a1"},
		{"arn:aws:appconfig:us-east-1:123:application/a1/configurationprofile/c1/hostedconfigurationversion/1", "arn:aws:appconfig:us-east-1:123:application/a1"},
		{"arn:aws:appconfig:us-east-1:123:deploymentstrategy/ds1", ""},
		{"", ""},
	}
	for _, c := range cases {
		if got := appconfigAppARNFromChild(c.in); got != c.want {
			t.Errorf("appconfigAppARNFromChild(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestResolveAppConfigChildren(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	appARN := fmt.Sprintf("arn:aws:appconfig:%s:%s:application/a1", testRegion, acct.ID)
	appID := upsertTestResource(t, st, "aws", acct.ID, TypeAppConfigApplication, appARN, testRegion, "{}")
	envARN := appARN + "/environment/e1"
	envID := upsertTestResource(t, st, "aws", acct.ID, TypeAppConfigEnvironment, envARN, testRegion, "{}")
	cpARN := appARN + "/configurationprofile/c1"
	cpID := upsertTestResource(t, st, "aws", acct.ID, TypeAppConfigConfigurationProfile, cpARN, testRegion, "{}")
	depARN := envARN + "/deployment/1"
	depID := upsertTestResource(t, st, "aws", acct.ID, TypeAppConfigDeployment, depARN, testRegion, "{}")
	hcvARN := cpARN + "/hostedconfigurationversion/1"
	hcvID := upsertTestResource(t, st, "aws", acct.ID, TypeAppConfigHostedConfigurationVersion, hcvARN, testRegion, "{}")

	if err := resolveAppConfigChildren(acct, st); err != nil {
		t.Fatalf("resolveAppConfigChildren: %v", err)
	}
	rels, _ := st.RelationshipsFrom(envID)
	assertRelationship(t, rels, envID, appID, store.RelAttachedTo)
	rels, _ = st.RelationshipsFrom(cpID)
	assertRelationship(t, rels, cpID, appID, store.RelAttachedTo)
	rels, _ = st.RelationshipsFrom(depID)
	assertRelationship(t, rels, depID, appID, store.RelAttachedTo)
	assertRelationship(t, rels, depID, envID, store.RelAttachedTo)
	rels, _ = st.RelationshipsFrom(hcvID)
	assertRelationship(t, rels, hcvID, appID, store.RelAttachedTo)
	assertRelationship(t, rels, hcvID, cpID, store.RelAttachedTo)
}

func TestResolveAppConfigExtensionAssociation(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	extARN := fmt.Sprintf("arn:aws:appconfig:%s:%s:extension/x1/1", testRegion, acct.ID)
	extID := upsertTestResource(t, st, "aws", acct.ID, TypeAppConfigExtension, extARN, testRegion, "{}")
	eaARN := fmt.Sprintf("arn:aws:appconfig:%s:%s:extensionassociation/ea1", testRegion, acct.ID)
	attrs := fmt.Sprintf(`{"ExtensionArn":%q}`, extARN)
	eaID := upsertTestResource(t, st, "aws", acct.ID, TypeAppConfigExtensionAssociation, eaARN, testRegion, attrs)

	if err := resolveAppConfigExtensionAssociation(acct, st); err != nil {
		t.Fatalf("resolveAppConfigExtensionAssociation: %v", err)
	}
	rels, _ := st.RelationshipsFrom(eaID)
	assertRelationship(t, rels, eaID, extID, store.RelUses)
}

package aws

import (
	"fmt"
	"testing"

	"codeberg.org/icearp/disco/internal/store"
)

func TestDatazoneDomainARNFromChild(t *testing.T) {
	cases := []struct{ in, want string }{
		{"arn:aws:datazone:us-east-1:123:domain/d1/project/p1", "arn:aws:datazone:us-east-1:123:domain/d1"},
		{"arn:aws:datazone:us-east-1:123:domain/d1/environment-action/e1/a1", "arn:aws:datazone:us-east-1:123:domain/d1"},
		{"arn:aws:datazone:us-east-1:123:domain/d1", ""},
		{"", ""},
	}
	for _, c := range cases {
		if got := datazoneDomainARNFromChild(c.in); got != c.want {
			t.Errorf("datazoneDomainARNFromChild(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestResolveDataZoneChildrenToDomain(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	dARN := fmt.Sprintf("arn:aws:datazone:%s:%s:domain/d1", testRegion, acct.ID)
	dID := upsertTestResource(t, st, "aws", acct.ID, TypeDataZoneDomain, dARN, testRegion, "{}")
	pARN := dARN + "/project/p1"
	pID := upsertTestResource(t, st, "aws", acct.ID, TypeDataZoneProject, pARN, testRegion, "{}")
	dsARN := dARN + "/data-source/ds1"
	dsID := upsertTestResource(t, st, "aws", acct.ID, TypeDataZoneDataSource, dsARN, testRegion, "{}")
	if err := resolveDataZoneChildrenToDomain(acct, st); err != nil {
		t.Fatalf("resolveDataZoneChildrenToDomain: %v", err)
	}
	rels, _ := st.RelationshipsFrom(pID)
	assertRelationship(t, rels, pID, dID, store.RelAttachedTo)
	rels, _ = st.RelationshipsFrom(dsID)
	assertRelationship(t, rels, dsID, dID, store.RelAttachedTo)
}

func TestResolveDataZoneEnvActionsToEnvironment(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	dARN := fmt.Sprintf("arn:aws:datazone:%s:%s:domain/d1", testRegion, acct.ID)
	envARN := dARN + "/environment/env1"
	envID := upsertTestResource(t, st, "aws", acct.ID, TypeDataZoneEnvironment, envARN, testRegion, "{}")
	eaARN := dARN + "/environment-action/env1/act1"
	eaID := upsertTestResource(t, st, "aws", acct.ID, TypeDataZoneEnvironmentActions, eaARN, testRegion, "{}")
	stARN := dARN + "/subscription-target/env1/st1"
	stID := upsertTestResource(t, st, "aws", acct.ID, TypeDataZoneSubscriptionTarget, stARN, testRegion, "{}")
	if err := resolveDataZoneEnvActionsToEnvironment(acct, st); err != nil {
		t.Fatalf("resolveDataZoneEnvActionsToEnvironment: %v", err)
	}
	rels, _ := st.RelationshipsFrom(eaID)
	assertRelationship(t, rels, eaID, envID, store.RelAttachedTo)
	rels, _ = st.RelationshipsFrom(stID)
	assertRelationship(t, rels, stID, envID, store.RelAttachedTo)
}

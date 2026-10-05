package aws

import (
	"fmt"
	"testing"

	"github.com/icearp/disco-cli/store"
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
	cases := []struct {
		typ, kind string
		managed   bool
	}{
		{typ: TypeDataZoneProject, kind: "project"},
		{typ: TypeDataZoneDataSource, kind: "data-source"},
		{typ: TypeDataZoneAccountPool, kind: "account-pool"},
		{typ: TypeDataZoneEnvironmentBlueprint, kind: "environment-blueprint"},
		// AWS-managed blueprints are stored ManagedByProvider, which the
		// default ListResources filter hides; they must still attach.
		{typ: TypeDataZoneEnvironmentBlueprint, kind: "environment-blueprint", managed: true},
		{typ: TypeDataZoneNotebook, kind: "notebook"},
		{typ: TypeDataZoneRule, kind: "rule"},
		{typ: TypeDataZoneSubscription, kind: "subscription"},
		{typ: TypeDataZoneSubscriptionGrant, kind: "subscription-grant"},
	}
	for _, tc := range cases {
		t.Run(fmt.Sprintf("%s/managed=%t", tc.kind, tc.managed), func(t *testing.T) {
			st := newTestStore(t)
			acct := newTestAccount(testAccountID)
			dARN := fmt.Sprintf("arn:aws:datazone:%s:%s:domain/d1", testRegion, acct.ID)
			dID := upsertTestResource(t, st, "aws", acct.ID, TypeDataZoneDomain, dARN, testRegion, "{}")
			region := testRegion
			child := &store.Resource{
				Provider: "aws", AccountID: acct.ID, Type: tc.typ,
				NativeID: dARN + "/" + tc.kind + "/c1", Region: &region,
				AttributesJSON: "{}", DiscoveredBy: testScanID, ManagedByProvider: tc.managed,
			}
			if _, err := st.UpsertResource(child); err != nil {
				t.Fatal(err)
			}
			childID := store.ResourceID("aws", acct.ID, child.NativeID)
			if err := resolveDataZoneChildrenToDomain(acct, st); err != nil {
				t.Fatalf("resolveDataZoneChildrenToDomain: %v", err)
			}
			rels, err := st.RelationshipsFrom(childID)
			if err != nil {
				t.Fatalf("RelationshipsFrom: %v", err)
			}
			assertRelationship(t, rels, childID, dID, store.RelAttachedTo)
		})
	}
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

func TestResolveDataZoneDomainRefs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	dARN := "arn:aws:datazone:us-east-1:" + testAccountID + ":domain/dom-1"
	keyARN := "arn:aws:kms:us-east-1:" + testAccountID + ":key/k-dz"
	roleARN := "arn:aws:iam::" + testAccountID + ":role/dz"
	attrs := `{"KmsKeyIdentifier":"` + keyARN + `","DomainExecutionRole":"` + roleARN + `"}`

	dID := upsertTestResource(t, st, "aws", acct.ID, TypeDataZoneDomain, dARN, testRegion, attrs)
	kID := upsertTestResource(t, st, "aws", acct.ID, TypeKMSKey, keyARN, testRegion, "{}")
	rID := upsertTestResource(t, st, "aws", acct.ID, TypeIAMRole, roleARN, testRegion, "{}")

	if err := resolveDataZoneDomainRefs(acct, st); err != nil {
		t.Fatalf("resolveDataZoneDomainRefs: %v", err)
	}
	rels, _ := st.RelationshipsFrom(dID)
	assertRelationship(t, rels, dID, kID, store.RelUses)
	assertRelationship(t, rels, dID, rID, store.RelAssumes)
}

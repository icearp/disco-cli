package aws

import (
	"fmt"
	"testing"

	"codeberg.org/icearp/disco/internal/store"
)

func TestDeadlineFarmARNFromChild(t *testing.T) {
	cases := []struct{ in, want string }{
		{"arn:aws:deadline:us-east-1:123:farm/farm-1/queue/q-1", "arn:aws:deadline:us-east-1:123:farm/farm-1"},
		{"arn:aws:deadline:us-east-1:123:farm/farm-1/queue/q-1/fleet/f-1/association", "arn:aws:deadline:us-east-1:123:farm/farm-1"},
		{"arn:aws:deadline:us-east-1:123:farm/farm-1", ""},
	}
	for _, c := range cases {
		if got := deadlineFarmARNFromChild(c.in); got != c.want {
			t.Errorf("deadlineFarmARNFromChild(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestResolveDeadlineFarmChildren(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	farmARN := fmt.Sprintf("arn:aws:deadline:%s:%s:farm/%s", testRegion, acct.ID, "farm-1")
	farmID := upsertTestResource(t, st, "aws", acct.ID, TypeDeadlineFarm, farmARN, testRegion, "{}")
	fleetARN := farmARN + "/fleet/fleet-1"
	fleetID := upsertTestResource(t, st, "aws", acct.ID, TypeDeadlineFleet, fleetARN, testRegion, "{}")
	queueARN := farmARN + "/queue/q-1"
	queueID := upsertTestResource(t, st, "aws", acct.ID, TypeDeadlineQueue, queueARN, testRegion, "{}")

	if err := resolveDeadlineFarmChildren(acct, st); err != nil {
		t.Fatalf("resolveDeadlineFarmChildren: %v", err)
	}
	rels, _ := st.RelationshipsFrom(fleetID)
	assertRelationship(t, rels, fleetID, farmID, store.RelAttachedTo)
	rels, _ = st.RelationshipsFrom(queueID)
	assertRelationship(t, rels, queueID, farmID, store.RelAttachedTo)
}

func TestResolveDeadlineQueueEnvParent(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	farmARN := fmt.Sprintf("arn:aws:deadline:%s:%s:farm/%s", testRegion, acct.ID, "farm-1")
	queueARN := farmARN + "/queue/q-1"
	queueID := upsertTestResource(t, st, "aws", acct.ID, TypeDeadlineQueue, queueARN, testRegion, "{}")
	envARN := queueARN + "/queue-environment/env-1"
	envID := upsertTestResource(t, st, "aws", acct.ID, TypeDeadlineQueueEnvironment, envARN, testRegion, "{}")
	if err := resolveDeadlineQueueEnvParent(acct, st); err != nil {
		t.Fatalf("resolveDeadlineQueueEnvParent: %v", err)
	}
	rels, _ := st.RelationshipsFrom(envID)
	assertRelationship(t, rels, envID, queueID, store.RelAttachedTo)
}

func TestResolveDeadlineQueueFleetAssoc(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	farmARN := fmt.Sprintf("arn:aws:deadline:%s:%s:farm/%s", testRegion, acct.ID, "farm-1")
	queueARN := farmARN + "/queue/q-1"
	queueID := upsertTestResource(t, st, "aws", acct.ID, TypeDeadlineQueue, queueARN, testRegion, "{}")
	fleetARN := farmARN + "/fleet/f-1"
	fleetID := upsertTestResource(t, st, "aws", acct.ID, TypeDeadlineFleet, fleetARN, testRegion, "{}")
	assocARN := farmARN + "/queue/q-1/fleet/f-1/association"
	assocID := upsertTestResource(t, st, "aws", acct.ID, TypeDeadlineQueueFleetAssociation, assocARN, testRegion, "{}")
	if err := resolveDeadlineQueueFleetAssoc(acct, st); err != nil {
		t.Fatalf("resolveDeadlineQueueFleetAssoc: %v", err)
	}
	rels, _ := st.RelationshipsFrom(assocID)
	assertRelationship(t, rels, assocID, queueID, store.RelAttachedTo)
	assertRelationship(t, rels, assocID, fleetID, store.RelAttachedTo)
}

func TestResolveDeadlineMeteredProductParent(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	leARN := fmt.Sprintf("arn:aws:deadline:%s:%s:license-endpoint/le-1", testRegion, acct.ID)
	leID := upsertTestResource(t, st, "aws", acct.ID, TypeDeadlineLicenseEndpoint, leARN, testRegion, "{}")
	mpARN := leARN + "/metered-product/prod-1"
	mpID := upsertTestResource(t, st, "aws", acct.ID, TypeDeadlineMeteredProduct, mpARN, testRegion, "{}")
	if err := resolveDeadlineMeteredProductParent(acct, st); err != nil {
		t.Fatalf("resolveDeadlineMeteredProductParent: %v", err)
	}
	rels, _ := st.RelationshipsFrom(mpID)
	assertRelationship(t, rels, mpID, leID, store.RelAttachedTo)
}

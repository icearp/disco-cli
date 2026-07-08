package gcp

import (
	"testing"

	"codeberg.org/icearp/disco/store"
)

func TestResolveCloudIdentityOrgRelationships(t *testing.T) {
	st := newTestStore(t)

	customerID := "C0123abc"
	groupEmail := "eng-team@example.com"
	groupID := upsertTestResource(t, st, "gcp", customerID, TypeCloudIdentityGroup, "groups/g1", "",
		`{"groupKey": {"id": "`+groupEmail+`"}}`)

	userEmail := "alice@example.com"
	userID := upsertTestResource(t, st, "gcp", customerID, TypeWorkspaceUser, "users/u1", "",
		`{"primaryEmail": "`+userEmail+`"}`)

	deviceUserID := upsertTestResource(t, st, "gcp", customerID, TypeCloudIdentityDeviceUser, "devices/d1/deviceUsers/du1", "",
		`{"userEmail": "`+userEmail+`"}`)

	ssoID := upsertTestResource(t, st, "gcp", customerID, TypeCloudIdentityInboundSsoAssignment, "inboundSsoAssignments/sso1", "",
		`{"targetGroup": "groups/g1"}`)

	membershipGroupRefID := upsertTestResource(t, st, "gcp", customerID, TypeCloudIdentityMembership, "groups/g2/memberships/m1", "",
		`{"type": "GROUP", "preferredMemberKey": {"id": "`+groupEmail+`"}}`)
	membershipUserRefID := upsertTestResource(t, st, "gcp", customerID, TypeCloudIdentityMembership, "groups/g2/memberships/m2", "",
		`{"type": "USER", "preferredMemberKey": {"id": "`+userEmail+`"}}`)

	policyID := upsertTestResource(t, st, "gcp", customerID, TypeCloudIdentityPolicy, "policies/p1", "",
		`{"policyQuery": {"group": "groups/g1"}}`)

	if err := resolveCloudIdentityOrgRelationships(st); err != nil {
		t.Fatalf("resolveCloudIdentityOrgRelationships: %v", err)
	}

	duRels, _ := st.RelationshipsFrom(deviceUserID)
	if len(duRels) != 1 || duRels[0].ToID != userID || duRels[0].Kind != store.RelUses {
		t.Errorf("deviceUser edge: got %+v, want →workspaceUser uses", duRels)
	}

	ssoRels, _ := st.RelationshipsFrom(ssoID)
	if len(ssoRels) != 1 || ssoRels[0].ToID != groupID || ssoRels[0].Kind != store.RelUses {
		t.Errorf("inboundSsoAssignment edge: got %+v, want →group uses", ssoRels)
	}

	mgRels, _ := st.RelationshipsFrom(membershipGroupRefID)
	if len(mgRels) != 1 || mgRels[0].ToID != groupID || mgRels[0].Kind != store.RelUses {
		t.Errorf("membership(GROUP) edge: got %+v, want →group uses", mgRels)
	}
	muRels, _ := st.RelationshipsFrom(membershipUserRefID)
	if len(muRels) != 1 || muRels[0].ToID != userID || muRels[0].Kind != store.RelUses {
		t.Errorf("membership(USER) edge: got %+v, want →workspaceUser uses", muRels)
	}

	polRels, _ := st.RelationshipsFrom(policyID)
	if len(polRels) != 1 || polRels[0].ToID != groupID || polRels[0].Kind != store.RelUses {
		t.Errorf("policy edge: got %+v, want →group uses", polRels)
	}
}

func TestResolveCloudIdentityOrgRelationships_UnscannedTargetsSkipped(t *testing.T) {
	st := newTestStore(t)
	customerID := "C0123abc"

	deviceUserID := upsertTestResource(t, st, "gcp", customerID, TypeCloudIdentityDeviceUser, "devices/d1/deviceUsers/du1", "",
		`{"userEmail": "not-scanned@example.com"}`)
	ssoID := upsertTestResource(t, st, "gcp", customerID, TypeCloudIdentityInboundSsoAssignment, "inboundSsoAssignments/sso1", "",
		`{"targetGroup": "groups/not-scanned"}`)
	membershipID := upsertTestResource(t, st, "gcp", customerID, TypeCloudIdentityMembership, "groups/g2/memberships/m1", "",
		`{"type": "USER", "preferredMemberKey": {"id": "not-scanned@example.com"}}`)
	serviceAcctMembershipID := upsertTestResource(t, st, "gcp", customerID, TypeCloudIdentityMembership, "groups/g2/memberships/m3", "",
		`{"type": "SERVICE_ACCOUNT", "preferredMemberKey": {"id": "svc@example.com"}}`)
	policyID := upsertTestResource(t, st, "gcp", customerID, TypeCloudIdentityPolicy, "policies/p1", "",
		`{"policyQuery": {"group": "groups/not-scanned"}}`)

	if err := resolveCloudIdentityOrgRelationships(st); err != nil {
		t.Fatalf("resolveCloudIdentityOrgRelationships: %v", err)
	}

	for label, id := range map[string]string{
		"deviceUser": deviceUserID, "sso": ssoID, "membership": membershipID,
		"serviceAccountMembership": serviceAcctMembershipID, "policy": policyID,
	} {
		rels, err := st.RelationshipsFrom(id)
		if err != nil {
			t.Fatalf("RelationshipsFrom(%s): %v", label, err)
		}
		if len(rels) != 0 {
			t.Errorf("%s: want no edges for unscanned/unresolvable targets, got %+v", label, rels)
		}
	}
}

func TestResolveCloudIdentityOrgRelationships_NoFieldsNoPanic(t *testing.T) {
	st := newTestStore(t)
	customerID := "C0123abc"

	deviceUserID := upsertTestResource(t, st, "gcp", customerID, TypeCloudIdentityDeviceUser, "devices/d1/deviceUsers/du1", "", "{}")
	ssoID := upsertTestResource(t, st, "gcp", customerID, TypeCloudIdentityInboundSsoAssignment, "inboundSsoAssignments/sso1", "", "{}")
	membershipID := upsertTestResource(t, st, "gcp", customerID, TypeCloudIdentityMembership, "groups/g2/memberships/m1", "", "{}")
	policyID := upsertTestResource(t, st, "gcp", customerID, TypeCloudIdentityPolicy, "policies/p1", "", "{}")

	if err := resolveCloudIdentityOrgRelationships(st); err != nil {
		t.Fatalf("resolveCloudIdentityOrgRelationships: %v", err)
	}

	for label, id := range map[string]string{
		"deviceUser": deviceUserID, "sso": ssoID, "membership": membershipID, "policy": policyID,
	} {
		rels, err := st.RelationshipsFrom(id)
		if err != nil {
			t.Fatalf("RelationshipsFrom(%s): %v", label, err)
		}
		if len(rels) != 0 {
			t.Errorf("%s: want no edges when fields are unset, got %+v", label, rels)
		}
	}
}

func TestResolveCloudIdentityOrgRelationships_EmptyStoreNoResources(t *testing.T) {
	st := newTestStore(t)
	if err := resolveCloudIdentityOrgRelationships(st); err != nil {
		t.Fatalf("resolveCloudIdentityOrgRelationships on empty store: %v", err)
	}
}

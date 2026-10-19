package aws

import (
	"encoding/json"
	"errors"
	"testing"

	grafanatypes "github.com/aws/aws-sdk-go-v2/service/grafana/types"
	"github.com/icearp/disco-cli/store"
)

func gfPermissionAttrs(t *testing.T, userType grafanatypes.UserType, id string) string {
	t.Helper()
	b, err := json.Marshal(grafanatypes.PermissionEntry{Role: grafanatypes.RoleAdmin, User: &grafanatypes.User{Id: &id, Type: userType}})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(b)
}

// seedGFPermission stores a grant under its workspace, as the scanner does.
func seedGFPermission(t *testing.T, st *store.Store, nativeID, attrs string) string {
	t.Helper()
	wsID := upsertTestResource(t, st, "aws", testAccountID, TypeGrafanaWorkspace, gfWsA, testRegion, "{}")
	id := upsertTestResource(t, st, "aws", testAccountID, TypeGrafanaPermission, nativeID, testRegion, attrs)
	if err := st.RecordHierarchyBatch([][2]string{{id, wsID}}); err != nil {
		t.Fatalf("hierarchy: %v", err)
	}
	return id
}

// A user grant links to the stored user and a group grant to the stored
// group, each matched by id and type only: the grant to a user id that is
// stored only as a group links nowhere.
func TestResolveGrafanaPermissionPrincipals_UserAndGroup(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	userID := upsertTestResource(t, st, "aws", acct.ID, TypeIdentityStoreUser, identityStoreUserNativeID(acct.ID, "d-1", "u-1"), "", "{}")
	groupID := upsertTestResource(t, st, "aws", acct.ID, TypeIdentityStoreGroup, identityStoreGroupNativeID(acct.ID, "d-1", "g-1"), "", "{}")
	userPerm := seedGFPermission(t, st, gfPermAUser, gfPermissionAttrs(t, grafanatypes.UserTypeSsoUser, "u-1"))
	groupPerm := seedGFPermission(t, st, gfPermAGroup, gfPermissionAttrs(t, grafanatypes.UserTypeSsoGroup, "g-1"))
	crossed := seedGFPermission(t, st, gfWsA+"/permission/SSO_USER/g-1", gfPermissionAttrs(t, grafanatypes.UserTypeSsoUser, "g-1"))

	if err := resolveGrafanaPermissionPrincipals(acct, st); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	rels, _ := st.RelationshipsFrom(userPerm)
	assertRelationship(t, rels, userPerm, userID, store.RelUses)
	rels, _ = st.RelationshipsFrom(groupPerm)
	assertRelationship(t, rels, groupPerm, groupID, store.RelUses)
	if rels, _ := st.RelationshipsFrom(crossed, store.RelUses); len(rels) != 0 {
		t.Errorf("user grant to group id g-1 has edges %v, want none", rels)
	}
}

// A grant with no user, or to a principal that is not stored, gets no edge
// and no error.
func TestResolveGrafanaPermissionPrincipals_NoAttrsOrUnscanned(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	upsertTestResource(t, st, "aws", acct.ID, TypeIdentityStoreUser, identityStoreUserNativeID(acct.ID, "d-1", "u-1"), "", "{}")
	empty := seedGFPermission(t, st, gfPermAUser, "{}")
	unscanned := seedGFPermission(t, st, gfPermB, gfPermissionAttrs(t, grafanatypes.UserTypeSsoUser, "u-2"))

	if err := resolveGrafanaPermissionPrincipals(acct, st); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	for _, id := range []string{empty, unscanned} {
		if rels, _ := st.RelationshipsFrom(id, store.RelUses); len(rels) != 0 {
			t.Errorf("%s has edges %v, want none", id, rels)
		}
	}
}

// A principal stored under another account is not matched, though its id is
// the grant's.
func TestResolveGrafanaPermissionPrincipals_OtherAccountPrincipalUnmatched(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	const other = "210987654321"
	upsertTestResource(t, st, "aws", other, TypeIdentityStoreUser, identityStoreUserNativeID(other, "d-1", "u-1"), "", "{}")
	perm := seedGFPermission(t, st, gfPermAUser, gfPermissionAttrs(t, grafanatypes.UserTypeSsoUser, "u-1"))

	if err := resolveGrafanaPermissionPrincipals(acct, st); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if rels, _ := st.RelationshipsFrom(perm, store.RelUses); len(rels) != 0 {
		t.Errorf("grant has edges %v to another account's principal, want none", rels)
	}
}

// A grant whose attributes do not decode is reported, and the other grants
// still get their edges.
func TestResolveGrafanaPermissionPrincipals_DecodeErrorKeepsOtherEdges(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	userID := upsertTestResource(t, st, "aws", acct.ID, TypeIdentityStoreUser, identityStoreUserNativeID(acct.ID, "d-1", "u-1"), "", "{}")
	seedGFPermission(t, st, gfPermAGroup, `{"User":"not an object"}`)
	good := seedGFPermission(t, st, gfPermAUser, gfPermissionAttrs(t, grafanatypes.UserTypeSsoUser, "u-1"))

	var typeErr *json.UnmarshalTypeError
	if err := resolveGrafanaPermissionPrincipals(acct, st); !errors.As(err, &typeErr) {
		t.Errorf("err = %v, want a JSON type error", err)
	}
	rels, _ := st.RelationshipsFrom(good)
	assertRelationship(t, rels, good, userID, store.RelUses)
}

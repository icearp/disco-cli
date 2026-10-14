package aws

import (
	"testing"

	organizationstypes "github.com/aws/aws-sdk-go-v2/service/organizations/types"
	watypes "github.com/aws/aws-sdk-go-v2/service/wellarchitected/types"
	"github.com/icearp/disco-cli/store"
)

const (
	waOrgAccountID  = "222222222222"
	waOrgAccountARN = "arn:aws:organizations::123456789012:account/o-abc123/222222222222"
	waOrgOUID       = "ou-ab12-cdef3456"
	waOrgOUARN      = "arn:aws:organizations::123456789012:ou/o-abc123/ou-ab12-cdef3456"
)

func seedWAOrgTree(t *testing.T, st *store.Store) (accountID, ouID string) {
	t.Helper()
	accountID = upsertTestResource(t, st, "aws", testAccountID, TypeOrganizationsAccount, waOrgAccountARN, "",
		mustJSON(organizationstypes.Account{Id: ptrStr(waOrgAccountID), Arn: ptrStr(waOrgAccountARN)}))
	ouID = upsertTestResource(t, st, "aws", testAccountID, TypeOrganizationsOU, waOrgOUARN, "",
		mustJSON(organizationstypes.OrganizationalUnit{Id: ptrStr(waOrgOUID), Arn: ptrStr(waOrgOUARN)}))
	return accountID, ouID
}

func TestResolveWAShareGrantees(t *testing.T) {
	st := newTestStore(t)
	accountID, ouID := seedWAOrgTree(t, st)
	share := func(typ, id, sharedWith string, status watypes.ShareStatus) string {
		return upsertTestResource(t, st, "aws", testAccountID, typ, waARN("workload", "w1")+"/share/"+id, testRegion,
			mustJSON(watypes.WorkloadShareSummary{ShareId: ptrStr(id), SharedWith: ptrStr(sharedWith), Status: status}))
	}
	toAccount := share(TypeWellArchitectedWorkloadShare, "s1", waOrgAccountID, watypes.ShareStatusAccepted)
	toOU := share(TypeWellArchitectedTemplateShare, "s2", waOrgOUID, watypes.ShareStatusAssociated)
	toOrg := share(TypeWellArchitectedProfileShare, "s4", "o-abc123", watypes.ShareStatusAccepted)
	outside := share(TypeWellArchitectedWorkloadShare, "s5", "333333333333", watypes.ShareStatusAccepted)

	if err := resolveWAShareGrantees(newTestAccount(testAccountID), st); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	for _, c := range []struct {
		name, from, to string
	}{
		{"accepted share to org account", toAccount, accountID},
		{"associated share to OU", toOU, ouID},
	} {
		rels, err := st.RelationshipsFrom(c.from, store.RelAttachedTo)
		if err != nil {
			t.Fatalf("RelationshipsFrom: %v", err)
		}
		if len(rels) != 1 {
			t.Errorf("%s: %d edges, want 1", c.name, len(rels))
		}
		assertRelationship(t, rels, c.from, c.to, store.RelAttachedTo)
	}
	for name, from := range map[string]string{"share to the organization": toOrg, "share to an account outside the tree": outside} {
		if rels, err := st.RelationshipsFrom(from, store.RelAttachedTo); err != nil || len(rels) != 0 {
			t.Errorf("%s: edges = %v (%v), want none", name, rels, err)
		}
	}
}

// Only an accepted or associated share grants access; every other state, a
// pending invitation included, must leave no edge to an in-tree grantee.
func TestResolveWAShareGrantees_EdgeOnlyForActiveStatus(t *testing.T) {
	cases := []struct {
		status   watypes.ShareStatus
		wantEdge bool
	}{
		{watypes.ShareStatusAccepted, true},
		{watypes.ShareStatusRejected, false},
		{watypes.ShareStatusPending, false},
		{watypes.ShareStatusRevoked, false},
		{watypes.ShareStatusExpired, false},
		{watypes.ShareStatusAssociating, false},
		{watypes.ShareStatusAssociated, true},
		{watypes.ShareStatusFailed, false},
	}
	if len(cases) != len(watypes.ShareStatus("").Values()) {
		t.Fatalf("cases cover %d statuses, SDK enum has %d", len(cases), len(watypes.ShareStatus("").Values()))
	}
	for _, c := range cases {
		t.Run(string(c.status), func(t *testing.T) {
			st := newTestStore(t)
			accountID, _ := seedWAOrgTree(t, st)
			id := upsertTestResource(t, st, "aws", testAccountID, TypeWellArchitectedWorkloadShare, waARN("workload", "w1")+"/share/s1", testRegion,
				mustJSON(watypes.WorkloadShareSummary{ShareId: ptrStr("s1"), SharedWith: ptrStr(waOrgAccountID), Status: c.status}))
			if err := resolveWAShareGrantees(newTestAccount(testAccountID), st); err != nil {
				t.Fatalf("resolve: %v", err)
			}
			rels, err := st.RelationshipsFrom(id, store.RelAttachedTo)
			if err != nil {
				t.Fatalf("RelationshipsFrom: %v", err)
			}
			if !c.wantEdge {
				if len(rels) != 0 {
					t.Errorf("status %s: edges = %v, want none", c.status, rels)
				}
				return
			}
			if len(rels) != 1 {
				t.Errorf("status %s: %d edges, want 1", c.status, len(rels))
			}
			assertRelationship(t, rels, id, accountID, store.RelAttachedTo)
		})
	}
}

func TestResolveWAShareGrantees_NoOrgTreeNoEdges(t *testing.T) {
	st := newTestStore(t)
	id := upsertTestResource(t, st, "aws", testAccountID, TypeWellArchitectedWorkloadShare, waARN("workload", "w1")+"/share/s1", testRegion,
		mustJSON(watypes.WorkloadShareSummary{ShareId: ptrStr("s1"), SharedWith: ptrStr(waOrgAccountID), Status: watypes.ShareStatusAccepted}))
	if err := resolveWAShareGrantees(newTestAccount(testAccountID), st); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if rels, err := st.RelationshipsFrom(id); err != nil || len(rels) != 0 {
		t.Errorf("edges = %v (%v), want none", rels, err)
	}
}

func TestResolveWAShareGrantees_EmptyAttrs(t *testing.T) {
	st := newTestStore(t)
	seedWAOrgTree(t, st)
	id := upsertTestResource(t, st, "aws", testAccountID, TypeWellArchitectedLensShare, waARN("lens", "l1")+"/share/s1", testRegion, "{}")
	if err := resolveWAShareGrantees(newTestAccount(testAccountID), st); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if rels, err := st.RelationshipsFrom(id); err != nil || len(rels) != 0 {
		t.Errorf("edges = %v (%v), want none", rels, err)
	}
}

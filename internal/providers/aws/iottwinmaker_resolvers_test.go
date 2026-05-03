package aws

import (
	"fmt"
	"testing"

	"codeberg.org/icearp/disco/internal/store"
)

func TestIoTTMWorkspaceARN(t *testing.T) {
	cases := []struct{ in, want string }{
		{"arn:aws:iottwinmaker:us-east-1:123:workspace/ws-1/component-type/ct-1", "arn:aws:iottwinmaker:us-east-1:123:workspace/ws-1"},
		{"arn:aws:iottwinmaker:us-east-1:123:workspace/ws-1/entity/e-1", "arn:aws:iottwinmaker:us-east-1:123:workspace/ws-1"},
		{"arn:aws:iottwinmaker:us-east-1:123:workspace/ws-1", ""},
		{"", ""},
	}
	for _, c := range cases {
		if got := iotTMWorkspaceARN(c.in); got != c.want {
			t.Errorf("iotTMWorkspaceARN(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestResolveIoTTwinMakerChildrenToWorkspace(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	wsARN := fmt.Sprintf("arn:aws:iottwinmaker:%s:%s:workspace/ws-1", testRegion, acct.ID)
	wsID := upsertTestResource(t, st, "aws", acct.ID, TypeIoTTwinMakerWorkspace, wsARN, testRegion, "{}")
	for _, kind := range []struct{ seg, typ string }{
		{"component-type/ct-1", TypeIoTTwinMakerComponentType},
		{"entity/e-1", TypeIoTTwinMakerEntity},
		{"scene/s-1", TypeIoTTwinMakerScene},
		{"sync-job/sj-1", TypeIoTTwinMakerSyncJob},
	} {
		cARN := wsARN + "/" + kind.seg
		cID := upsertTestResource(t, st, "aws", acct.ID, kind.typ, cARN, testRegion, "{}")
		if err := resolveIoTTwinMakerChildrenToWorkspace(acct, st); err != nil {
			t.Fatalf("resolveIoTTwinMakerChildrenToWorkspace: %v", err)
		}
		rels, _ := st.RelationshipsFrom(cID)
		assertRelationship(t, rels, cID, wsID, store.RelAttachedTo)
	}
}

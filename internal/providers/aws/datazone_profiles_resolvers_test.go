package aws

import (
	"encoding/json"
	"fmt"
	"testing"

	"codeberg.org/icearp/disco/store"
	dztypes "github.com/aws/aws-sdk-go-v2/service/datazone/types"
)

func TestResolveDataZoneGroupProfileRefs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	roleARN := fmt.Sprintf("arn:aws:iam::%s:role/dz-session", acct.ID)
	roleID := upsertTestResource(t, st, "aws", acct.ID, TypeIAMRole, roleARN, "", "{}")

	gpARN := dzARN(testRegion, acct.ID, "dom-1", "group-profile", "gp-1")
	gpBody, _ := json.Marshal(dztypes.GroupProfileSummary{
		Id: ptrStr("gp-1"), GroupName: ptrStr("analysts"), RolePrincipalArn: ptrStr(roleARN),
	})
	gpID := upsertTestResource(t, st, "aws", acct.ID, TypeDataZoneGroupProfile, gpARN, testRegion, string(gpBody))

	if err := resolveDataZoneGroupProfileRefs(acct, st); err != nil {
		t.Fatalf("resolveDataZoneGroupProfileRefs: %v", err)
	}
	rels, _ := st.RelationshipsFrom(gpID)
	assertRelationship(t, rels, gpID, roleID, store.RelUses)
}

func TestResolveDataZoneGroupProfileRefs_NoAttrs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	gpARN := dzARN(testRegion, acct.ID, "dom-1", "group-profile", "gp-1")
	gpID := upsertTestResource(t, st, "aws", acct.ID, TypeDataZoneGroupProfile, gpARN, testRegion, "{}")
	if err := resolveDataZoneGroupProfileRefs(acct, st); err != nil {
		t.Fatalf("resolveDataZoneGroupProfileRefs: %v", err)
	}
	if rels, _ := st.RelationshipsFrom(gpID); len(rels) != 0 {
		t.Errorf("emitted %d edges, want 0", len(rels))
	}
}

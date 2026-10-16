package aws

import (
	"testing"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/icearp/disco-cli/store"
)

func TestResolveRUMMetricsDestinationRoles_RoleEdge(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	ev := rumEvidently()
	ev.IamRoleArn = sdkaws.String("arn:aws:iam::" + testAccountID + ":role/rum-evidently")
	roleID := upsertTestResource(t, st, "aws", acct.ID, TypeIAMRole, *ev.IamRoleArn, "", "{}")
	evID := upsertTestResource(t, st, "aws", acct.ID, TypeRUMMetricsDestination, rumEvDestID("m1"), testRegion, mustJSON(ev))
	cwID := upsertTestResource(t, st, "aws", acct.ID, TypeRUMMetricsDestination, rumCWDestID("m1"), testRegion, mustJSON(rumCloudWatch()))
	unscanned := rumEvidently()
	unscanned.IamRoleArn = sdkaws.String("arn:aws:iam::" + testAccountID + ":role/not-scanned")
	unscannedID := upsertTestResource(t, st, "aws", acct.ID, TypeRUMMetricsDestination, rumEvDestID("m2"), testRegion, mustJSON(unscanned))

	if err := resolveRUMMetricsDestinationRoles(acct, st); err != nil {
		t.Fatalf("resolveRUMMetricsDestinationRoles: %v", err)
	}
	rels, err := st.RelationshipsFrom(evID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	assertRelationship(t, rels, evID, roleID, store.RelUses)
	for _, id := range []string{cwID, unscannedID} {
		if rels, _ := st.RelationshipsFrom(id); len(rels) != 0 {
			t.Errorf("destination %s: edges = %v; want none (no role, or role not scanned)", id, rels)
		}
	}
}

func TestResolveRUMMetricsDestinationRoles_NoDestinations(t *testing.T) {
	st := newTestStore(t)
	if err := resolveRUMMetricsDestinationRoles(newTestAccount(testAccountID), st); err != nil {
		t.Errorf("err = %v; want nil", err)
	}
}

func TestResolveRUMMetricsDestinationRoles_DecodeErrorReturned(t *testing.T) {
	st := newTestStore(t)
	upsertTestResource(t, st, "aws", testAccountID, TypeIAMRole, "arn:aws:iam::"+testAccountID+":role/r", "", "{}")
	upsertTestResource(t, st, "aws", testAccountID, TypeRUMMetricsDestination, rumCWDestID("m1"), testRegion, `{"IamRoleArn":7}`)
	if err := resolveRUMMetricsDestinationRoles(newTestAccount(testAccountID), st); err == nil {
		t.Error("err = nil; want the attribute decode error")
	}
}

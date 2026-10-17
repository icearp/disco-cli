package aws

import (
	"encoding/json"
	"slices"
	"testing"

	sbtypes "github.com/aws/aws-sdk-go-v2/service/snowball/types"
	"github.com/icearp/disco-cli/store"
)

func sbClusterAttrs(t *testing.T, m *sbtypes.ClusterMetadata) string {
	t.Helper()
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("marshal cluster: %v", err)
	}
	return string(b)
}

func sbUsesTargets(t *testing.T, st *store.Store, from string) []string {
	t.Helper()
	rels, err := st.RelationshipsFrom(from, store.RelUses)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	var to []string
	for _, r := range rels {
		to = append(to, r.ToID)
	}
	slices.Sort(to)
	return to
}

func TestResolveSnowballClusterRelationships_LinksScannedTargets(t *testing.T) {
	st := newTestStore(t)
	keyARN := "arn:aws:kms:" + testRegion + ":" + testAccountID + ":key/k-1"
	roleARN := "arn:aws:iam::" + testAccountID + ":role/snow"
	topicARN := "arn:aws:sns:" + testRegion + ":" + testAccountID + ":snow-events"
	pickupARN := "arn:aws:sns:" + testRegion + ":" + testAccountID + ":snow-pickup"
	keyID := upsertTestResource(t, st, "aws", testAccountID, TypeKMSKey, keyARN, testRegion, "{}")
	roleID := upsertTestResource(t, st, "aws", testAccountID, TypeIAMRole, roleARN, "", "{}")
	topicID := upsertTestResource(t, st, "aws", testAccountID, TypeSNSTopic, topicARN, testRegion, "{}")
	pickupID := upsertTestResource(t, st, "aws", testAccountID, TypeSNSTopic, pickupARN, testRegion, "{}")
	addrID := upsertTestResource(t, st, "aws", testAccountID, TypeSnowballAddress, sbAddressNative("1"), testRegion, "{}")
	fwdID := upsertTestResource(t, st, "aws", testAccountID, TypeSnowballAddress, sbAddressNative("2"), testRegion, "{}")

	full := sbCluster("1")
	full.ForwardingAddressId = ptrStr(sbAddressID("2"))
	full.RoleARN = &roleARN
	full.Notification = &sbtypes.Notification{SnsTopicARN: &topicARN, DevicePickupSnsTopicARN: &pickupARN}
	fullID := upsertTestResource(t, st, "aws", testAccountID, TypeSnowballCluster, sbClusterNative(testRegion, "1"), testRegion, sbClusterAttrs(t, full))

	// Every ref names something not scanned: no edge may be emitted.
	unscanned := sbCluster("3")
	unscanned.KmsKeyARN = ptrStr("arn:aws:kms:" + testRegion + ":" + testAccountID + ":key/k-unscanned")
	unscanned.RoleARN = ptrStr("arn:aws:iam::" + testAccountID + ":role/unscanned")
	unscanned.Notification = &sbtypes.Notification{SnsTopicARN: ptrStr(topicARN + "-unscanned")}
	unscannedID := upsertTestResource(t, st, "aws", testAccountID, TypeSnowballCluster, sbClusterNative(testRegion, "3"), testRegion, sbClusterAttrs(t, unscanned))

	if err := resolveSnowballClusterRelationships(newTestAccount(testAccountID), st); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	want := []string{keyID, roleID, topicID, pickupID, addrID, fwdID}
	slices.Sort(want)
	if got := sbUsesTargets(t, st, fullID); !slices.Equal(got, want) {
		t.Errorf("uses targets of cluster 1 = %v, want %v", got, want)
	}
	if got := sbUsesTargets(t, st, unscannedID); len(got) != 0 {
		t.Errorf("cluster with unscanned refs has edges to %v, want none", got)
	}
}

// A cluster with no refs gets no edge; a row whose attributes do not decode
// is reported without costing the other rows their edges.
func TestResolveSnowballClusterRelationships_NoAttrsAndBadAttrs(t *testing.T) {
	st := newTestStore(t)
	addrID := upsertTestResource(t, st, "aws", testAccountID, TypeSnowballAddress, sbAddressNative("1"), testRegion, "{}")
	empty := upsertTestResource(t, st, "aws", testAccountID, TypeSnowballCluster, sbClusterNative(testRegion, "1"), testRegion, "{}")
	// Named so the bad row is listed before the linked one (ListResources
	// orders by name): a resolver that stops at the bad row loses the edge.
	upsertTestResourceNamed(t, st, TypeSnowballCluster, sbClusterNative(testRegion, "2"), testRegion, `{"AddressId":7}`, "a-bad")
	linked := upsertTestResourceNamed(t, st, TypeSnowballCluster, sbClusterNative(testRegion, "3"), testRegion,
		sbClusterAttrs(t, &sbtypes.ClusterMetadata{AddressId: ptrStr(sbAddressID("1"))}), "z-linked")

	if err := resolveSnowballClusterRelationships(newTestAccount(testAccountID), st); err == nil {
		t.Error("resolve err = nil, want the cluster 2 decode error")
	}
	if got := sbUsesTargets(t, st, empty); len(got) != 0 {
		t.Errorf("cluster without refs has edges to %v, want none", got)
	}
	if got := sbUsesTargets(t, st, linked); !slices.Equal(got, []string{addrID}) {
		t.Errorf("uses targets of cluster 3 = %v, want [%s]", got, addrID)
	}
}

package aws

import (
	"fmt"
	"testing"

	"codeberg.org/icearp/disco/store"
)

func TestResolveMediaLiveISGRefs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	chARN := mediaLiveColonARN(testRegion, acct.ID, "channel", "ch-1")
	chID := upsertTestResource(t, st, "aws", acct.ID, TypeMediaLiveChannel, chARN, testRegion, "{}")
	inARN := mediaLiveColonARN(testRegion, acct.ID, "input", "in-1")
	inID := upsertTestResource(t, st, "aws", acct.ID, TypeMediaLiveInput, inARN, testRegion, "{}")
	isgARN := fmt.Sprintf("arn:aws:medialive:%s:%s:inputSecurityGroup:isg-1", testRegion, acct.ID)
	attrs := `{"Channels":["ch-1"],"Inputs":["in-1"]}`
	isgID := upsertTestResource(t, st, "aws", acct.ID, TypeMediaLiveInputSecurityGroup, isgARN, testRegion, attrs)
	if err := resolveMediaLiveISGRefs(acct, st); err != nil {
		t.Fatalf("resolveMediaLiveISGRefs: %v", err)
	}
	rels, _ := st.RelationshipsFrom(isgID)
	assertRelationship(t, rels, isgID, chID, store.RelAttachedTo)
	assertRelationship(t, rels, isgID, inID, store.RelAttachedTo)
}

func TestResolveMediaLiveSdiSourceRefs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	inARN := mediaLiveColonARN(testRegion, acct.ID, "input", "in-1")
	inID := upsertTestResource(t, st, "aws", acct.ID, TypeMediaLiveInput, inARN, testRegion, "{}")
	sdiARN := mediaLiveColonARN(testRegion, acct.ID, "sdiSource", "sdi-1")
	attrs := `{"Inputs":["in-1"]}`
	sdiID := upsertTestResource(t, st, "aws", acct.ID, TypeMediaLiveSdiSource, sdiARN, testRegion, attrs)
	if err := resolveMediaLiveSdiSourceRefs(acct, st); err != nil {
		t.Fatalf("resolveMediaLiveSdiSourceRefs: %v", err)
	}
	rels, _ := st.RelationshipsFrom(sdiID)
	assertRelationship(t, rels, sdiID, inID, store.RelAttachedTo)
}

func TestResolveMediaLiveNetworkRefs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	clARN := mediaLiveColonARN(testRegion, acct.ID, "cluster", "cl-1")
	clID := upsertTestResource(t, st, "aws", acct.ID, TypeMediaLiveCluster, clARN, testRegion, "{}")
	netARN := mediaLiveColonARN(testRegion, acct.ID, "network", "net-1")
	attrs := `{"AssociatedClusterIds":["cl-1"]}`
	netID := upsertTestResource(t, st, "aws", acct.ID, TypeMediaLiveNetwork, netARN, testRegion, attrs)
	if err := resolveMediaLiveNetworkRefs(acct, st); err != nil {
		t.Fatalf("resolveMediaLiveNetworkRefs: %v", err)
	}
	rels, _ := st.RelationshipsFrom(netID)
	assertRelationship(t, rels, netID, clID, store.RelAttachedTo)
}

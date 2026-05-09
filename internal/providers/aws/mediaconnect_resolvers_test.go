package aws

import (
	"fmt"
	"testing"

	"codeberg.org/icearp/disco/store"
)

func TestMcBridgeARNFromChild(t *testing.T) {
	cases := []struct{ in, want string }{
		{"arn:aws:mediaconnect:us-east-1:123:bridge:1:b1/output/o1", "arn:aws:mediaconnect:us-east-1:123:bridge:1:b1"},
		{"arn:aws:mediaconnect:us-east-1:123:bridge:1:b1/source/s1", "arn:aws:mediaconnect:us-east-1:123:bridge:1:b1"},
		{"arn:aws:mediaconnect:us-east-1:123:bridge:1:b1", ""},
	}
	for _, c := range cases {
		if got := mcBridgeARNFromChild(c.in); got != c.want {
			t.Errorf("mcBridgeARNFromChild(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestResolveMediaConnectBridgeChildren(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	bARN := fmt.Sprintf("arn:aws:mediaconnect:%s:%s:bridge:1:b1", testRegion, acct.ID)
	bID := upsertTestResource(t, st, "aws", acct.ID, TypeMediaConnectBridge, bARN, testRegion, "{}")
	oARN := bARN + "/output/o1"
	oID := upsertTestResource(t, st, "aws", acct.ID, TypeMediaConnectBridgeOutput, oARN, testRegion, "{}")
	if err := resolveMediaConnectBridgeChildren(acct, st); err != nil {
		t.Fatalf("resolveMediaConnectBridgeChildren: %v", err)
	}
	rels, _ := st.RelationshipsFrom(oID)
	assertRelationship(t, rels, oID, bID, store.RelAttachedTo)
}

func TestResolveMediaConnectFlowVpcInterface(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	fARN := fmt.Sprintf("arn:aws:mediaconnect:%s:%s:flow:1:f1", testRegion, acct.ID)
	fID := upsertTestResource(t, st, "aws", acct.ID, TypeMediaConnectFlow, fARN, testRegion, "{}")
	vARN := fARN + "/vpc-interface/iface-1"
	vID := upsertTestResource(t, st, "aws", acct.ID, TypeMediaConnectFlowVpcInterface, vARN, testRegion, "{}")
	if err := resolveMediaConnectFlowVpcInterface(acct, st); err != nil {
		t.Fatalf("resolveMediaConnectFlowVpcInterface: %v", err)
	}
	rels, _ := st.RelationshipsFrom(vID)
	assertRelationship(t, rels, vID, fID, store.RelAttachedTo)
}

func TestResolveMediaConnectFlowChildren(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	fARN := fmt.Sprintf("arn:aws:mediaconnect:%s:%s:flow:1:f1", testRegion, acct.ID)
	srcARN := fmt.Sprintf("arn:aws:mediaconnect:%s:%s:source:1:s1", testRegion, acct.ID)
	outARN := fmt.Sprintf("arn:aws:mediaconnect:%s:%s:output:1:o1", testRegion, acct.ID)
	entARN := fmt.Sprintf("arn:aws:mediaconnect:%s:%s:entitlement:1:e1", testRegion, acct.ID)
	flowAttrs := fmt.Sprintf(`{"Sources":[{"SourceArn":%q}],"Outputs":[{"OutputArn":%q}],"Entitlements":[{"EntitlementArn":%q}]}`, srcARN, outARN, entARN)
	fID := upsertTestResource(t, st, "aws", acct.ID, TypeMediaConnectFlow, fARN, testRegion, flowAttrs)
	srcID := upsertTestResource(t, st, "aws", acct.ID, TypeMediaConnectFlowSource, srcARN, testRegion, "{}")
	outID := upsertTestResource(t, st, "aws", acct.ID, TypeMediaConnectFlowOutput, outARN, testRegion, "{}")
	entID := upsertTestResource(t, st, "aws", acct.ID, TypeMediaConnectFlowEntitlement, entARN, testRegion, "{}")

	if err := resolveMediaConnectFlowChildren(acct, st); err != nil {
		t.Fatalf("resolveMediaConnectFlowChildren: %v", err)
	}
	rels, _ := st.RelationshipsFrom(srcID)
	assertRelationship(t, rels, srcID, fID, store.RelAttachedTo)
	rels, _ = st.RelationshipsFrom(outID)
	assertRelationship(t, rels, outID, fID, store.RelAttachedTo)
	rels, _ = st.RelationshipsFrom(entID)
	assertRelationship(t, rels, entID, fID, store.RelAttachedTo)
}

func TestResolveMediaConnectBridgePlacement(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	bARN := fmt.Sprintf("arn:aws:mediaconnect:%s:%s:bridge:1", testRegion, acct.ID)
	gARN := fmt.Sprintf("arn:aws:mediaconnect:%s:%s:gateway:gw-1", testRegion, acct.ID)
	attrs := fmt.Sprintf(`{"PlacementArn":%q}`, gARN)

	bID := upsertTestResource(t, st, "aws", acct.ID, TypeMediaConnectBridge, bARN, testRegion, attrs)
	gID := upsertTestResource(t, st, "aws", acct.ID, TypeMediaConnectGateway, gARN, testRegion, "{}")

	if err := resolveMediaConnectBridgePlacement(acct, st); err != nil {
		t.Fatalf("resolveMediaConnectBridgePlacement: %v", err)
	}
	rels, _ := st.RelationshipsFrom(bID)
	assertRelationship(t, rels, bID, gID, store.RelAttachedTo)
}

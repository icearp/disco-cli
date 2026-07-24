package aws

import (
	"testing"

	"github.com/icearp/disco-cli/store"
)

func TestResolveCoipPoolRelationships(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	region := "us-east-1"

	rtARN := "arn:aws:ec2:" + region + ":" + testAccountID + ":local-gateway-route-table/lgw-rtb-1"
	rtID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2LocalGatewayRouteTable, rtARN, region, "{}")
	poolARN := "arn:aws:ec2:" + region + ":" + testAccountID + ":coip-pool/coip-pool-1"
	poolID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2CoipPool, poolARN, region, `{"LocalGatewayRouteTableId":"lgw-rtb-1"}`)

	if err := resolveCoipPoolRelationships(acct, st); err != nil {
		t.Fatalf("resolveCoipPoolRelationships: %v", err)
	}
	rels, _ := st.RelationshipsFrom(poolID)
	assertRelationship(t, rels, poolID, rtID, store.RelAttachedTo)
}

func TestResolveCoipPoolRelationships_NoAttrs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	region := "us-east-1"
	poolARN := "arn:aws:ec2:" + region + ":" + testAccountID + ":coip-pool/coip-pool-1"
	poolID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2CoipPool, poolARN, region, "{}")
	if err := resolveCoipPoolRelationships(acct, st); err != nil {
		t.Fatalf("empty: %v", err)
	}
	if rels, _ := st.RelationshipsFrom(poolID); len(rels) != 0 {
		t.Errorf("emitted %d edges, want 0", len(rels))
	}
}

func TestResolveOutpostLagRelationships(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	region := "us-east-1"

	viARN := ec2ARN(region, acct.ID, "local-gateway-virtual-interface", "lgw-vif-1")
	viID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2LocalGatewayVirtualInterface, viARN, region, "{}")
	lagARN := ec2ARN(region, acct.ID, "outpost-lag", "ol-1")
	lagID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2OutpostLag, lagARN, region, `{"LocalGatewayVirtualInterfaceIds":["lgw-vif-1"]}`)

	if err := resolveOutpostLagRelationships(acct, st); err != nil {
		t.Fatalf("resolveOutpostLagRelationships: %v", err)
	}
	rels, _ := st.RelationshipsFrom(lagID)
	assertRelationship(t, rels, lagID, viID, store.RelAttachedTo)
}

func TestResolveOutpostLagRelationships_NoAttrs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	region := "us-east-1"
	lagARN := ec2ARN(region, acct.ID, "outpost-lag", "ol-1")
	lagID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2OutpostLag, lagARN, region, "{}")
	if err := resolveOutpostLagRelationships(acct, st); err != nil {
		t.Fatalf("empty: %v", err)
	}
	if rels, _ := st.RelationshipsFrom(lagID); len(rels) != 0 {
		t.Errorf("emitted %d edges, want 0", len(rels))
	}
}

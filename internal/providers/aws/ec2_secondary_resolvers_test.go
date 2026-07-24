package aws

import (
	"testing"

	"github.com/icearp/disco-cli/store"
)

func TestResolveSecondaryInterfaceRelationships(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	region := "us-east-1"

	netARN := "arn:aws:ec2:" + region + ":" + testAccountID + ":secondary-network/sn-1"
	netID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2SecondaryNetwork, netARN, region, "{}")
	subARN := "arn:aws:ec2:" + region + ":" + testAccountID + ":secondary-subnet/ss-1"
	subID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2SecondarySubnet, subARN, region, "{}")
	ifARN := "arn:aws:ec2:" + region + ":" + testAccountID + ":secondary-interface/si-1"
	attrs := `{"SecondaryNetworkId":"sn-1","SecondarySubnetId":"ss-1"}`
	ifID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2SecondaryInterface, ifARN, region, attrs)

	if err := resolveSecondaryInterfaceRelationships(acct, st); err != nil {
		t.Fatalf("resolveSecondaryInterfaceRelationships: %v", err)
	}
	rels, _ := st.RelationshipsFrom(ifID)
	assertRelationship(t, rels, ifID, netID, store.RelAttachedTo)
	assertRelationship(t, rels, ifID, subID, store.RelAttachedTo)
}

func TestResolveSecondaryInterfaceRelationships_NoAttrs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	region := "us-east-1"
	ifARN := "arn:aws:ec2:" + region + ":" + testAccountID + ":secondary-interface/si-1"
	ifID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2SecondaryInterface, ifARN, region, "{}")
	if err := resolveSecondaryInterfaceRelationships(acct, st); err != nil {
		t.Fatalf("empty: %v", err)
	}
	if rels, _ := st.RelationshipsFrom(ifID); len(rels) != 0 {
		t.Errorf("emitted %d edges, want 0", len(rels))
	}
}

func TestResolveSecondarySubnetRelationships(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	region := "us-east-1"

	netARN := "arn:aws:ec2:" + region + ":" + testAccountID + ":secondary-network/sn-1"
	netID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2SecondaryNetwork, netARN, region, "{}")
	subARN := "arn:aws:ec2:" + region + ":" + testAccountID + ":secondary-subnet/ss-1"
	subID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2SecondarySubnet, subARN, region, `{"SecondaryNetworkId":"sn-1"}`)

	if err := resolveSecondarySubnetRelationships(acct, st); err != nil {
		t.Fatalf("resolveSecondarySubnetRelationships: %v", err)
	}
	rels, _ := st.RelationshipsFrom(subID)
	assertRelationship(t, rels, subID, netID, store.RelAttachedTo)
}

func TestResolveSecondarySubnetRelationships_NoAttrs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	region := "us-east-1"
	subARN := "arn:aws:ec2:" + region + ":" + testAccountID + ":secondary-subnet/ss-1"
	subID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2SecondarySubnet, subARN, region, "{}")
	if err := resolveSecondarySubnetRelationships(acct, st); err != nil {
		t.Fatalf("empty: %v", err)
	}
	if rels, _ := st.RelationshipsFrom(subID); len(rels) != 0 {
		t.Errorf("emitted %d edges, want 0", len(rels))
	}
}

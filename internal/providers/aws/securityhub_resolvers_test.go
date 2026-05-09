package aws

import (
	"fmt"
	"testing"

	"codeberg.org/icearp/disco/store"
)

func TestResolveSecurityHubProductSubscriptions_GuardDuty(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	subARN := fmt.Sprintf("arn:aws:securityhub:%s:%s:product-subscription/aws/guardduty", testRegion, acct.ID)
	detARN := fmt.Sprintf("arn:aws:guardduty:%s:%s:detector/abc123", testRegion, acct.ID)

	sID := upsertTestResource(t, st, "aws", acct.ID, TypeSecurityHubProductSubscription, subARN, testRegion, "{}")
	dID := upsertTestResource(t, st, "aws", acct.ID, TypeGuardDutyDetector, detARN, testRegion, "{}")

	if err := resolveSecurityHubProductSubscriptions(acct, st); err != nil {
		t.Fatalf("resolveSecurityHubProductSubscriptions: %v", err)
	}

	rels, _ := st.RelationshipsFrom(sID)
	assertRelationship(t, rels, sID, dID, store.RelUses)
	if len(rels) != 1 {
		t.Errorf("got %d edges, want 1", len(rels))
	}
}

func TestResolveSecurityHubProductSubscriptions_Macie(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	subARN := fmt.Sprintf("arn:aws:securityhub:%s:%s:product-subscription/aws/macie", testRegion, acct.ID)
	macieARN := macieSessionNativeID(acct.ID, testRegion)

	sID := upsertTestResource(t, st, "aws", acct.ID, TypeSecurityHubProductSubscription, subARN, testRegion, "{}")
	mID := upsertTestResource(t, st, "aws", acct.ID, TypeMacieSession, macieARN, testRegion, "{}")

	if err := resolveSecurityHubProductSubscriptions(acct, st); err != nil {
		t.Fatalf("resolveSecurityHubProductSubscriptions: %v", err)
	}

	rels, _ := st.RelationshipsFrom(sID)
	assertRelationship(t, rels, sID, mID, store.RelUses)
}

func TestResolveSecurityHubProductSubscriptions_SelfSkipped(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	subARN := fmt.Sprintf("arn:aws:securityhub:%s:%s:product-subscription/aws/securityhub", testRegion, acct.ID)
	sID := upsertTestResource(t, st, "aws", acct.ID, TypeSecurityHubProductSubscription, subARN, testRegion, "{}")

	if err := resolveSecurityHubProductSubscriptions(acct, st); err != nil {
		t.Fatalf("resolveSecurityHubProductSubscriptions: %v", err)
	}
	rels, _ := st.RelationshipsFrom(sID)
	if len(rels) != 0 {
		t.Errorf("self subscription should emit no edges, got %d", len(rels))
	}
}

func TestResolveSecurityHubProductSubscriptions_NoUpstream(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	// product matches mapping (guardduty) but no detector scanned — must skip.
	subARN := fmt.Sprintf("arn:aws:securityhub:%s:%s:product-subscription/aws/guardduty", testRegion, acct.ID)
	sID := upsertTestResource(t, st, "aws", acct.ID, TypeSecurityHubProductSubscription, subARN, testRegion, "{}")

	if err := resolveSecurityHubProductSubscriptions(acct, st); err != nil {
		t.Fatalf("resolveSecurityHubProductSubscriptions: %v", err)
	}
	rels, _ := st.RelationshipsFrom(sID)
	if len(rels) != 0 {
		t.Errorf("missing upstream should emit no edges, got %d", len(rels))
	}
}

func TestResolveSecurityHubProductSubscriptions_ThirdParty(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	subARN := fmt.Sprintf("arn:aws:securityhub:%s:%s:product-subscription/crowdstrike/falcon", testRegion, acct.ID)
	sID := upsertTestResource(t, st, "aws", acct.ID, TypeSecurityHubProductSubscription, subARN, testRegion, "{}")

	if err := resolveSecurityHubProductSubscriptions(acct, st); err != nil {
		t.Fatalf("resolveSecurityHubProductSubscriptions: %v", err)
	}
	rels, _ := st.RelationshipsFrom(sID)
	if len(rels) != 0 {
		t.Errorf("third-party vendor should emit no edges, got %d", len(rels))
	}
}

func TestParseSecurityHubProductSubscriptionARN(t *testing.T) {
	cases := []struct {
		arn             string
		vendor, product string
		ok              bool
	}{
		{"arn:aws:securityhub:us-east-1:111122223333:product-subscription/aws/guardduty", "aws", "guardduty", true},
		{"arn:aws:securityhub:us-east-1:111122223333:product-subscription/crowdstrike/falcon-host", "crowdstrike", "falcon-host", true},
		{"arn:aws:securityhub:us-east-1:111122223333:hub/default", "", "", false},
		{"arn:aws:securityhub:us-east-1:111122223333:product-subscription/aws/", "", "", false},
		{"", "", "", false},
	}
	for _, c := range cases {
		v, p, ok := parseSecurityHubProductSubscriptionARN(c.arn)
		if v != c.vendor || p != c.product || ok != c.ok {
			t.Errorf("parse(%q) = (%q,%q,%v), want (%q,%q,%v)", c.arn, v, p, ok, c.vendor, c.product, c.ok)
		}
	}
}

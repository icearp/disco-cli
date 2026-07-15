package aws

import (
	"fmt"
	"testing"

	"codeberg.org/icearp/disco/store"
)

func TestResolveBillingConductorCustomLineItemToBillingGroup(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	region := "us-east-1"

	bgArn := fmt.Sprintf("arn:aws:billingconductor::%s:billinggroup/bg-1", acct.ID)
	cliArn := fmt.Sprintf("arn:aws:billingconductor::%s:customlineitem/cli-1", acct.ID)

	upsertTestResource(t, st, "aws", acct.ID, TypeBillingConductorBillingGroup, bgArn, region, `{}`)
	upsertTestResource(t, st, "aws", acct.ID, TypeBillingConductorCustomLineItem, cliArn, region,
		fmt.Sprintf(`{"BillingGroupArn":%q}`, bgArn))

	if err := resolveBillingConductor(acct, st); err != nil {
		t.Fatalf("resolve: %v", err)
	}

	cliID := store.ResourceID("aws", acct.ID, cliArn)
	bgID := store.ResourceID("aws", acct.ID, bgArn)
	rels, err := st.RelationshipsFrom(cliID)
	if err != nil {
		t.Fatalf("relationships: %v", err)
	}
	var found bool
	for _, r := range rels {
		if r.ToID == bgID && r.Kind == store.RelAttachedTo {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected attached-to edge cli→bg, got %+v", rels)
	}
}

func TestResolveBillingConductorEmpty(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	if err := resolveBillingConductor(acct, st); err != nil {
		t.Fatalf("resolve: %v", err)
	}
}

func TestResolveBillingConductorSkipsUnscannedTarget(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	region := "us-east-1"

	bgArn := fmt.Sprintf("arn:aws:billingconductor::%s:billinggroup/missing", acct.ID)
	cliArn := fmt.Sprintf("arn:aws:billingconductor::%s:customlineitem/cli-orphan", acct.ID)

	// Custom line item references billing group that was NOT scanned.
	upsertTestResource(t, st, "aws", acct.ID, TypeBillingConductorCustomLineItem, cliArn, region,
		fmt.Sprintf(`{"BillingGroupArn":%q}`, bgArn))

	if err := resolveBillingConductor(acct, st); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	cliID := store.ResourceID("aws", acct.ID, cliArn)
	rels, err := st.RelationshipsFrom(cliID)
	if err != nil {
		t.Fatalf("relationships: %v", err)
	}
	if len(rels) != 0 {
		t.Errorf("expected no edges (FK-safe), got %+v", rels)
	}
}

func TestBillingConductorRegistrationCovered(t *testing.T) {
	want := map[string]bool{
		TypeBillingConductorBillingGroup:   false,
		TypeBillingConductorCustomLineItem: false,
		TypeBillingConductorPricingPlan:    false,
		TypeBillingConductorPricingRule:    false,
	}
	if !serviceRegistered("aws:billingconductor") {
		t.Fatalf("aws:billingconductor service not registered")
	}
	for k := range want {
		if !descriptorEmitted(k) {
			t.Errorf("aws:billingconductor missing emit %s", k)
		}
	}
}

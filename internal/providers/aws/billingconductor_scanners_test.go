package aws

import (
	"context"
	"fmt"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/billingconductor"
	bctypes "github.com/aws/aws-sdk-go-v2/service/billingconductor/types"
	"github.com/icearp/disco-cli/store"
)

type stubBillingConductor struct {
	groups []bctypes.BillingGroupListElement
	clis   []bctypes.CustomLineItemListElement
	plans  []bctypes.PricingPlanListElement
	rules  []bctypes.PricingRuleListElement
}

func (s *stubBillingConductor) ListBillingGroups(_ context.Context, _ *billingconductor.ListBillingGroupsInput, _ ...func(*billingconductor.Options)) (*billingconductor.ListBillingGroupsOutput, error) {
	return &billingconductor.ListBillingGroupsOutput{BillingGroups: s.groups}, nil
}

func (s *stubBillingConductor) ListCustomLineItems(_ context.Context, _ *billingconductor.ListCustomLineItemsInput, _ ...func(*billingconductor.Options)) (*billingconductor.ListCustomLineItemsOutput, error) {
	return &billingconductor.ListCustomLineItemsOutput{CustomLineItems: s.clis}, nil
}

func (s *stubBillingConductor) ListPricingPlans(_ context.Context, _ *billingconductor.ListPricingPlansInput, _ ...func(*billingconductor.Options)) (*billingconductor.ListPricingPlansOutput, error) {
	return &billingconductor.ListPricingPlansOutput{PricingPlans: s.plans}, nil
}

func (s *stubBillingConductor) ListPricingRules(_ context.Context, _ *billingconductor.ListPricingRulesInput, _ ...func(*billingconductor.Options)) (*billingconductor.ListPricingRulesOutput, error) {
	return &billingconductor.ListPricingRulesOutput{PricingRules: s.rules}, nil
}

func TestScanBillingConductor(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	bgArn := fmt.Sprintf("arn:aws:billingconductor::%s:billinggroup/bg-1", acct.ID)
	bgName := "team-a"
	cliArn := fmt.Sprintf("arn:aws:billingconductor::%s:customlineitem/cli-1", acct.ID)
	cliName := "discount"
	ppArn := fmt.Sprintf("arn:aws:billingconductor::%s:pricingplan/pp-1", acct.ID)
	ppName := "plan"
	prArn := fmt.Sprintf("arn:aws:billingconductor::%s:pricingrule/pr-1", acct.ID)
	prName := "rule"

	stub := &stubBillingConductor{
		groups: []bctypes.BillingGroupListElement{{Arn: &bgArn, Name: &bgName, CreationTime: 1700000000000}},
		clis:   []bctypes.CustomLineItemListElement{{Arn: &cliArn, Name: &cliName, BillingGroupArn: &bgArn, CreationTime: 1700000001000}},
		plans:  []bctypes.PricingPlanListElement{{Arn: &ppArn, Name: &ppName, CreationTime: 1700000002000}},
		rules:  []bctypes.PricingRuleListElement{{Arn: &prArn, Name: &prName, CreationTime: 1700000003000}},
	}
	total, inserted, err := scanBillingConductor(context.Background(), stub, acct, st, testScanID)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != 4 || inserted != 4 {
		t.Fatalf("total=%d inserted=%d want 4/4", total, inserted)
	}
	for _, want := range []struct{ rtype, native string }{
		{TypeBillingConductorBillingGroup, bgArn},
		{TypeBillingConductorCustomLineItem, cliArn},
		{TypeBillingConductorPricingPlan, ppArn},
		{TypeBillingConductorPricingRule, prArn},
	} {
		if _, err := st.GetResource(store.ResourceID("aws", acct.ID, want.native)); err != nil {
			t.Errorf("%s missing: %v", want.rtype, err)
		}
	}
}

func TestScanBillingConductorEmpty(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	stub := &stubBillingConductor{}
	total, inserted, err := scanBillingConductor(context.Background(), stub, acct, st, testScanID)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != 0 || inserted != 0 {
		t.Fatalf("total=%d inserted=%d want 0/0", total, inserted)
	}
}

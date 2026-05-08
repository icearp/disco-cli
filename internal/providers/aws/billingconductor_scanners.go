package aws

import (
	"context"
	"fmt"
	"time"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/internal/store"
	"github.com/aws/aws-sdk-go-v2/service/billingconductor"
)

// isBillingConductorPayerOnly disambiguates the "Only payer account is
// authorized" state from a real IAM denial.
func isBillingConductorPayerOnly(err error) bool {
	return isAccessDeniedWithMessage(err, "Only payer account is authorized")
}

// AWS Billing Conductor is a global service — endpoints resolve only via
// us-east-1.
const billingConductorRegion = "us-east-1"

func init() {
	registerService(serviceEntry{
		name:   "aws:billingconductor",
		global: true,
		fn: func(ctx context.Context, acct *account, _ string, st *store.Store, scanID string) (total, inserted int, err error) {
			client := billingconductor.NewFromConfig(acct.cfg, func(o *billingconductor.Options) { o.Region = billingConductorRegion })
			return scanBillingConductor(ctx, client, acct, st, scanID)
		},
		emits: []coverage.TypeDecl{
			{Service: "billingconductor", DiscoType: TypeBillingConductorBillingGroup, Leaf: true},
			{Service: "billingconductor", DiscoType: TypeBillingConductorCustomLineItem},
			{Service: "billingconductor", DiscoType: TypeBillingConductorPricingPlan, Leaf: true},
			{Service: "billingconductor", DiscoType: TypeBillingConductorPricingRule, Leaf: true},
		},
	})
}

// billingConductorAPI is the narrow surface scanBillingConductor uses. All
// four list ops are paginator-native and return summaries that already carry
// the canonical ARN — no Get fan-out needed for identity.
type billingConductorAPI interface {
	ListBillingGroups(context.Context, *billingconductor.ListBillingGroupsInput, ...func(*billingconductor.Options)) (*billingconductor.ListBillingGroupsOutput, error)
	ListCustomLineItems(context.Context, *billingconductor.ListCustomLineItemsInput, ...func(*billingconductor.Options)) (*billingconductor.ListCustomLineItemsOutput, error)
	ListPricingPlans(context.Context, *billingconductor.ListPricingPlansInput, ...func(*billingconductor.Options)) (*billingconductor.ListPricingPlansOutput, error)
	ListPricingRules(context.Context, *billingconductor.ListPricingRulesInput, ...func(*billingconductor.Options)) (*billingconductor.ListPricingRulesOutput, error)
}

func scanBillingConductor(ctx context.Context, client billingConductorAPI, acct *account, st *store.Store, scanID string) (total, inserted int, err error) {
	bgT, bgI, err := scanBillingConductorBillingGroups(ctx, client, acct, st, scanID)
	total, inserted = total+bgT, inserted+bgI
	if err != nil {
		return total, inserted, err
	}
	cliT, cliI, err := scanBillingConductorCustomLineItems(ctx, client, acct, st, scanID)
	total, inserted = total+cliT, inserted+cliI
	if err != nil {
		return total, inserted, err
	}
	ppT, ppI, err := scanBillingConductorPricingPlans(ctx, client, acct, st, scanID)
	total, inserted = total+ppT, inserted+ppI
	if err != nil {
		return total, inserted, err
	}
	prT, prI, err := scanBillingConductorPricingRules(ctx, client, acct, st, scanID)
	total, inserted = total+prT, inserted+prI
	return total, inserted, err
}

// epochMillisToTime converts a billingconductor int64 epoch-millis timestamp
// to *time.Time for store.Resource.CreatedAt. Zero values stay nil.
func epochMillisToTime(ms int64) *time.Time {
	if ms == 0 {
		return nil
	}
	t := time.UnixMilli(ms).UTC()
	return &t
}

func scanBillingConductorBillingGroups(ctx context.Context, client billingConductorAPI, acct *account, st *store.Store, scanID string) (total, inserted int, err error) {
	region := billingConductorRegion
	pager := billingconductor.NewListBillingGroupsPaginator(client, &billingconductor.ListBillingGroupsInput{})
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isBillingConductorPayerOnly(perr) {
				return total, inserted, markServiceDisabled(perr)
			}
			if isAccessDenied(perr) {
				return total, inserted, skipIfAccessDenied(st, "billingconductor:ListBillingGroups", acct.ID, region, perr)
			}
			return total, inserted, fmt.Errorf("billingconductor:ListBillingGroups: %w", perr)
		}
		batch := make([]*store.Resource, 0, len(out.BillingGroups))
		for _, bg := range out.BillingGroups {
			arn := sv(bg.Arn)
			if arn == "" {
				continue
			}
			name := sv(bg.Name)
			batch = append(batch, &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeBillingConductorBillingGroup,
				NativeID:       arn,
				Name:           &name,
				Region:         regionGlobal,
				CreatedAt:      tp(epochMillisToTime(bg.CreationTime)),
				AttributesJSON: mustJSON(bg),
				DiscoveredBy:   scanID,
			})
		}
		if len(batch) > 0 {
			n, uerr := st.UpsertResources(batch)
			if uerr != nil {
				return total, inserted, fmt.Errorf("upsert billingconductor billing groups: %w", uerr)
			}
			total += len(batch)
			inserted += n
		}
	}
	return total, inserted, nil
}

func scanBillingConductorCustomLineItems(ctx context.Context, client billingConductorAPI, acct *account, st *store.Store, scanID string) (total, inserted int, err error) {
	region := billingConductorRegion
	pager := billingconductor.NewListCustomLineItemsPaginator(client, &billingconductor.ListCustomLineItemsInput{})
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isBillingConductorPayerOnly(perr) {
				return total, inserted, markServiceDisabled(perr)
			}
			if isAccessDenied(perr) {
				return total, inserted, skipIfAccessDenied(st, "billingconductor:ListCustomLineItems", acct.ID, region, perr)
			}
			return total, inserted, fmt.Errorf("billingconductor:ListCustomLineItems: %w", perr)
		}
		batch := make([]*store.Resource, 0, len(out.CustomLineItems))
		for _, cli := range out.CustomLineItems {
			arn := sv(cli.Arn)
			if arn == "" {
				continue
			}
			name := sv(cli.Name)
			batch = append(batch, &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeBillingConductorCustomLineItem,
				NativeID:       arn,
				Name:           &name,
				Region:         regionGlobal,
				CreatedAt:      tp(epochMillisToTime(cli.CreationTime)),
				AttributesJSON: mustJSON(cli),
				DiscoveredBy:   scanID,
			})
		}
		if len(batch) > 0 {
			n, uerr := st.UpsertResources(batch)
			if uerr != nil {
				return total, inserted, fmt.Errorf("upsert billingconductor custom line items: %w", uerr)
			}
			total += len(batch)
			inserted += n
		}
	}
	return total, inserted, nil
}

func scanBillingConductorPricingPlans(ctx context.Context, client billingConductorAPI, acct *account, st *store.Store, scanID string) (total, inserted int, err error) {
	region := billingConductorRegion
	pager := billingconductor.NewListPricingPlansPaginator(client, &billingconductor.ListPricingPlansInput{})
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isBillingConductorPayerOnly(perr) {
				return total, inserted, markServiceDisabled(perr)
			}
			if isAccessDenied(perr) {
				return total, inserted, skipIfAccessDenied(st, "billingconductor:ListPricingPlans", acct.ID, region, perr)
			}
			return total, inserted, fmt.Errorf("billingconductor:ListPricingPlans: %w", perr)
		}
		batch := make([]*store.Resource, 0, len(out.PricingPlans))
		for _, pp := range out.PricingPlans {
			arn := sv(pp.Arn)
			if arn == "" {
				continue
			}
			name := sv(pp.Name)
			batch = append(batch, &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeBillingConductorPricingPlan,
				NativeID:       arn,
				Name:           &name,
				Region:         regionGlobal,
				CreatedAt:      tp(epochMillisToTime(pp.CreationTime)),
				AttributesJSON: mustJSON(pp),
				DiscoveredBy:   scanID,
			})
		}
		if len(batch) > 0 {
			n, uerr := st.UpsertResources(batch)
			if uerr != nil {
				return total, inserted, fmt.Errorf("upsert billingconductor pricing plans: %w", uerr)
			}
			total += len(batch)
			inserted += n
		}
	}
	return total, inserted, nil
}

func scanBillingConductorPricingRules(ctx context.Context, client billingConductorAPI, acct *account, st *store.Store, scanID string) (total, inserted int, err error) {
	region := billingConductorRegion
	pager := billingconductor.NewListPricingRulesPaginator(client, &billingconductor.ListPricingRulesInput{})
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isBillingConductorPayerOnly(perr) {
				return total, inserted, markServiceDisabled(perr)
			}
			if isAccessDenied(perr) {
				return total, inserted, skipIfAccessDenied(st, "billingconductor:ListPricingRules", acct.ID, region, perr)
			}
			return total, inserted, fmt.Errorf("billingconductor:ListPricingRules: %w", perr)
		}
		batch := make([]*store.Resource, 0, len(out.PricingRules))
		for _, pr := range out.PricingRules {
			arn := sv(pr.Arn)
			if arn == "" {
				continue
			}
			name := sv(pr.Name)
			batch = append(batch, &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeBillingConductorPricingRule,
				NativeID:       arn,
				Name:           &name,
				Region:         regionGlobal,
				CreatedAt:      tp(epochMillisToTime(pr.CreationTime)),
				AttributesJSON: mustJSON(pr),
				DiscoveredBy:   scanID,
			})
		}
		if len(batch) > 0 {
			n, uerr := st.UpsertResources(batch)
			if uerr != nil {
				return total, inserted, fmt.Errorf("upsert billingconductor pricing rules: %w", uerr)
			}
			total += len(batch)
			inserted += n
		}
	}
	return total, inserted, nil
}

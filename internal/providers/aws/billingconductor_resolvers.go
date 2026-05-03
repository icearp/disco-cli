package aws

import (
	"encoding/json"

	"codeberg.org/icearp/disco/internal/store"
)

func init() {
	registerResolver(resolveBillingConductor,
		EdgeDecl{TypeBillingConductorCustomLineItem, TypeBillingConductorBillingGroup, store.RelAttachedTo},
	)
}

// billingConductorCustomLineItemAttrs is a partial projection of the
// CustomLineItemListElement payload covering the cross-resource ARN field
// the resolver consumes. PascalCase JSON tags match `mustJSON` output.
type billingConductorCustomLineItemAttrs struct {
	BillingGroupArn *string `json:"BillingGroupArn"`
}

// resolveBillingConductor wires Billing Conductor structural edges:
//
//   - custom-line-item → billing-group (attached-to via BillingGroupArn).
//
// PricingPlan ↔ PricingRule association edges live behind
// `ListPricingRulesAssociatedToPricingPlan` — a per-plan fan-out warranting
// a focused session, deferred. BillingGroup → PricingPlan via
// `ComputationPreference.PricingPlanArn` is also deferred (nested struct
// projection; same focused-session bucket).
func resolveBillingConductor(acct *account, st *store.Store) error {
	bgIDs, err := scannedIDSet(acct, st, TypeBillingConductorBillingGroup)
	if err != nil {
		return err
	}
	if len(bgIDs) == 0 {
		return nil
	}
	clis, err := st.ListResources(store.ResourceFilter{
		Provider:       "aws",
		AccountID:      acct.ID,
		Types:          []string{TypeBillingConductorCustomLineItem},
		IncludeManaged: true,
		Limit:          0,
	})
	if err != nil {
		return err
	}
	for _, cli := range clis {
		var attrs billingConductorCustomLineItemAttrs
		if err := json.Unmarshal([]byte(cli.AttributesJSON), &attrs); err != nil {
			continue
		}
		bgArn := sv(attrs.BillingGroupArn)
		if bgArn == "" {
			continue
		}
		bgID := store.ResourceID("aws", acct.ID, TypeBillingConductorBillingGroup, bgArn)
		if !bgIDs[bgID] {
			continue
		}
		if err := st.UpsertRelationship(cli.ID, bgID, store.RelAttachedTo, "", nil); err != nil {
			return err
		}
	}
	return nil
}

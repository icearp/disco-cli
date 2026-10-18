package aws

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/icearp/disco-cli/internal/util"
	"github.com/icearp/disco-cli/store"
)

// pricingPlanSubscriptionTargets are the scanned types a subscription's
// ResourceArns can name. The API docs call this list non-exhaustive ("other
// supported resources, such as ..."); an ARN of any other type gets no edge.
var pricingPlanSubscriptionTargets = []string{
	TypeCloudFrontDistribution,
	TypeWAFv2WebACL,
	TypeRoute53HostedZone,
	TypeCloudFrontKeyValueStore,
}

func init() {
	decls := make([]EdgeDecl, 0, len(pricingPlanSubscriptionTargets))
	for _, t := range pricingPlanSubscriptionTargets {
		decls = append(decls, EdgeDecl{TypePricingPlanManagerSubscription, t, store.RelUses})
	}
	registerResolver(resolvePricingPlanSubscriptionResources, decls...)
}

// resolvePricingPlanSubscriptionResources wires each flat-rate pricing
// subscription to the resources it covers (SubscriptionSummary.ResourceArns),
// when they are scanned in the same account. The distribution, web ACL and
// key value store scanners key their rows by the ARN the service returns; the
// hosted-zone NativeID is synthesised, and that the subscription names a zone
// in the same form is assumed (unverified).
func resolvePricingPlanSubscriptionResources(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypePricingPlanManagerSubscription}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	scanned := map[string]bool{}
	for _, t := range pricingPlanSubscriptionTargets {
		ids, err := scannedIDSet(acct, st, t)
		if err != nil {
			return err
		}
		for id := range ids {
			scanned[id] = true
		}
	}
	// A row that fails to decode is reported at the end so that it costs the
	// other rows none of their edges.
	var decodeErrs []error
	for _, r := range rows {
		var attrs struct {
			ResourceArns []string `json:"ResourceArns"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			decodeErrs = append(decodeErrs, fmt.Errorf("decode %s %s attributes: %w", TypePricingPlanManagerSubscription, r.NativeID, err))
			continue
		}
		for _, arn := range attrs.ResourceArns {
			targetID := store.ResourceID("aws", acct.ID, arn)
			if !scanned[targetID] {
				continue
			}
			if err := st.UpsertRelationship(r.ID, targetID, store.RelUses, "directed", nil); err != nil {
				return fmt.Errorf("upsert pricing plan subscription→resource: %w", err)
			}
		}
	}
	return errors.Join(decodeErrs...)
}

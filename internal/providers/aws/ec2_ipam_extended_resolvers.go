package aws

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/icearp/disco-cli/internal/util"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerResolver(
		resolveIpamByoasnRelationships,
		EdgeDecl{TypeEC2IpamByoasn, TypeEC2IPAM, store.RelAttachedTo},
	)
	registerResolver(
		noopIpamPolicyAllocationRuleHierarchy,
		EdgeDecl{TypeEC2IpamPolicy, TypeEC2IpamPolicyAllocationRule, store.RelContains},
	)
}

// noopIpamPolicyAllocationRuleHierarchy only carries the EdgeDecl:
// scanEC2IpamPolicyAllocationRules records the policy→allocation-rule contains
// pairs itself through childFanOut.
func noopIpamPolicyAllocationRuleHierarchy(_ *account, _ *store.Store) error {
	return nil
}

// resolveIpamByoasnRelationships links each BYOASN to the scanned IPAM it is
// provisioned to. A row whose attributes do not decode is reported and costs
// no other row its edge.
func resolveIpamByoasnRelationships(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeEC2IpamByoasn}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	idx, err := ipamByShortID(acct, st)
	if err != nil {
		return err
	}
	var decodeErrs []error
	for _, r := range rows {
		var attrs struct {
			IpamID *string `json:"IpamId"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			decodeErrs = append(decodeErrs, fmt.Errorf("decode ipam-byoasn %s attributes: %w", r.NativeID, err))
			continue
		}
		if tgtID, ok := idx[sv(attrs.IpamID)]; ok {
			if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
				return fmt.Errorf("upsert ipam-byoasn→ipam: %w", err)
			}
		}
	}
	return errors.Join(decodeErrs...)
}

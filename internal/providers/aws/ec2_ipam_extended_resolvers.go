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
		resolveIpamInternetRegistryAssociationRelationships,
		EdgeDecl{TypeEC2IpamInternetRegistryAssociation, TypeEC2IPAM, store.RelAttachedTo},
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
// provisioned to.
func resolveIpamByoasnRelationships(acct *account, st *store.Store) error {
	return linkToScannedIpam(acct, st, TypeEC2IpamByoasn)
}

// resolveIpamInternetRegistryAssociationRelationships links each internet
// registry association to the scanned IPAM it belongs to.
func resolveIpamInternetRegistryAssociationRelationships(acct *account, st *store.Store) error {
	return linkToScannedIpam(acct, st, TypeEC2IpamInternetRegistryAssociation)
}

// linkToScannedIpam links each rtype row to the scanned IPAM its IpamId
// attribute names. A row whose attributes do not decode is reported and costs
// no other row its edge.
func linkToScannedIpam(acct *account, st *store.Store, rtype string) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{rtype}, Limit: util.AllResources,
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
			decodeErrs = append(decodeErrs, fmt.Errorf("decode %s %s attributes: %w", rtype, r.NativeID, err))
			continue
		}
		if tgtID, ok := idx[sv(attrs.IpamID)]; ok {
			if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
				return fmt.Errorf("upsert %s→%s: %w", rtype, TypeEC2IPAM, err)
			}
		}
	}
	return errors.Join(decodeErrs...)
}

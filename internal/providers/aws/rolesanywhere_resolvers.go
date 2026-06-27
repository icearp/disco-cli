package aws

import (
	"encoding/json"
	"fmt"

	"codeberg.org/icearp/disco/internal/util"
	"codeberg.org/icearp/disco/store"
)

func init() {
	registerResolver(
		resolveRACRLToTrustAnchor,
		EdgeDecl{TypeRolesAnywhereCRL, TypeRolesAnywhereTrustAnchor, store.RelAttachedTo},
	)
	registerResolver(
		resolveRAProfileRefs,
		EdgeDecl{TypeRolesAnywhereProfile, TypeIAMRole, store.RelUses},
		EdgeDecl{TypeRolesAnywhereProfile, TypeIAMPolicy, store.RelUses},
	)
}

// resolveRACRLToTrustAnchor wires each CRL to its associated trust-anchor
// (TrustAnchorArn).
func resolveRACRLToTrustAnchor(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeRolesAnywhereCRL}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	taSet, err := scannedIDSet(acct, st, TypeRolesAnywhereTrustAnchor)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			TrustAnchorArn *string `json:"TrustAnchorArn"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if a := sv(attrs.TrustAnchorArn); a != "" {
			tgtID := store.ResourceID("aws", acct.ID, TypeRolesAnywhereTrustAnchor, a)
			if taSet[tgtID] {
				if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
					return fmt.Errorf("upsert ra crl→ta: %w", err)
				}
			}
		}
	}
	return nil
}

// resolveRAProfileRefs wires profile → IAM roles (RoleArns) and managed
// policies (ManagedPolicyArns).
func resolveRAProfileRefs(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeRolesAnywhereProfile}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	roleSet, err := scannedIDSet(acct, st, TypeIAMRole)
	if err != nil {
		return err
	}
	polSet, err := scannedIDSet(acct, st, TypeIAMPolicy)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			RoleArns          []string `json:"RoleArns"`
			ManagedPolicyArns []string `json:"ManagedPolicyArns"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		for _, ra := range attrs.RoleArns {
			if ra == "" {
				continue
			}
			tgtID := store.ResourceID("aws", acct.ID, TypeIAMRole, ra)
			if roleSet[tgtID] {
				if err := st.UpsertRelationship(r.ID, tgtID, store.RelUses, "directed", nil); err != nil {
					return fmt.Errorf("upsert ra profile→role: %w", err)
				}
			}
		}
		for _, pa := range attrs.ManagedPolicyArns {
			if pa == "" {
				continue
			}
			tgtID := store.ResourceID("aws", acct.ID, TypeIAMPolicy, pa)
			if polSet[tgtID] {
				if err := st.UpsertRelationship(r.ID, tgtID, store.RelUses, "directed", nil); err != nil {
					return fmt.Errorf("upsert ra profile→policy: %w", err)
				}
			}
		}
	}
	return nil
}

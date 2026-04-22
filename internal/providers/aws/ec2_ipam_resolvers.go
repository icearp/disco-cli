package aws

import (
	"encoding/json"
	"fmt"

	"codeberg.org/icearp/disco/internal/store"
	"codeberg.org/icearp/disco/internal/util"
)

func init() {
	registerResolver(resolveIPAMRelationships)
	registerResolver(resolveIPAMScopeRelationships)
	registerResolver(resolveIPAMPoolRelationships)
	registerResolver(resolveIPAMResourceDiscoveryAssociationRelationships)
}

// resolveIPAMRelationships: IPAM scopes are owned by an IPAM — each scope
// in the attributes IpamArn field points back to its parent IPAM.
// (Relationship written from the scope, not the IPAM itself.)
func resolveIPAMRelationships(_ *account, _ *store.Store) error {
	// Top-level; no parent to relate to. Reserved for future cross-resource edges.
	return nil
}

// resolveIPAMScopeRelationships links each IPAM scope to its parent IPAM.
func resolveIPAMScopeRelationships(acct *account, st *store.Store) error {
	scopes, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeEC2IPAMScope},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	for _, r := range scopes {
		var attrs struct {
			IpamArn *string `json:"IpamArn"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if attrs.IpamArn != nil {
			ipamID := store.ResourceID("aws", acct.ID, TypeEC2IPAM, *attrs.IpamArn)
			if err := st.UpsertRelationship(r.ID, ipamID, store.RelAttachedTo, "directed", nil); err != nil {
				return fmt.Errorf("upsert ipam-scope→ipam relationship: %w", err)
			}
		}
	}
	return nil
}

// resolveIPAMPoolRelationships links each IPAM pool to its owning scope.
func resolveIPAMPoolRelationships(acct *account, st *store.Store) error {
	pools, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeEC2IPAMPool},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	for _, r := range pools {
		var attrs struct {
			IpamScopeArn *string `json:"IpamScopeArn"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if attrs.IpamScopeArn != nil {
			scopeID := store.ResourceID("aws", acct.ID, TypeEC2IPAMScope, *attrs.IpamScopeArn)
			if err := st.UpsertRelationship(r.ID, scopeID, store.RelAttachedTo, "directed", nil); err != nil {
				return fmt.Errorf("upsert ipam-pool→ipam-scope relationship: %w", err)
			}
		}
	}
	return nil
}

// resolveIPAMResourceDiscoveryAssociationRelationships links each association
// to its parent IPAM and the resource discovery it references.
func resolveIPAMResourceDiscoveryAssociationRelationships(acct *account, st *store.Store) error {
	assocs, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeEC2IPAMResourceDiscoveryAssociation},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	for _, r := range assocs {
		var attrs struct {
			IpamArn                  *string `json:"IpamArn"`
			IpamResourceDiscoveryArn *string `json:"IpamResourceDiscoveryArn"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if attrs.IpamArn != nil {
			ipamID := store.ResourceID("aws", acct.ID, TypeEC2IPAM, *attrs.IpamArn)
			if err := st.UpsertRelationship(r.ID, ipamID, store.RelAttachedTo, "directed", nil); err != nil {
				return fmt.Errorf("upsert ipam-rda→ipam relationship: %w", err)
			}
		}
		if attrs.IpamResourceDiscoveryArn != nil {
			rdID := store.ResourceID("aws", acct.ID, TypeEC2IPAMResourceDiscovery, *attrs.IpamResourceDiscoveryArn)
			if err := st.UpsertRelationship(r.ID, rdID, store.RelUses, "directed", nil); err != nil {
				return fmt.Errorf("upsert ipam-rda→resource-discovery relationship: %w", err)
			}
		}
	}
	return nil
}

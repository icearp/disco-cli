package aws

import (
	"encoding/json"
	"fmt"

	"github.com/icearp/disco-cli/internal/util"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerResolver(resolveIPAMRelationships)
	registerResolver(
		resolveIPAMScopeRelationships,
		EdgeDecl{TypeEC2IPAMScope, TypeEC2IPAM, store.RelAttachedTo},
	)
	registerResolver(
		resolveIPAMPoolRelationships,
		EdgeDecl{TypeEC2IPAMPool, TypeEC2IPAMScope, store.RelAttachedTo},
	)
	registerResolver(
		resolveIPAMResourceDiscoveryAssociationRelationships,
		EdgeDecl{TypeEC2IPAMResourceDiscoveryAssociation, TypeEC2IPAM, store.RelAttachedTo},
		EdgeDecl{TypeEC2IPAMResourceDiscoveryAssociation, TypeEC2IPAMResourceDiscovery, store.RelUses},
	)
}

// resolveIPAMRelationships: IPAM scopes are owned by an IPAM — each scope's
// attrs.IpamArn points back to its parent IPAM (edge written from the scope).
func resolveIPAMRelationships(_ *account, _ *store.Store) error {
	// Top-level; no parent to relate to. Reserved for future cross-resource edges.
	return nil
}

// resolveIPAMScopeRelationships links each IPAM scope to its parent IPAM.
func resolveIPAMScopeRelationships(acct *account, st *store.Store) error {
	scopes, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeEC2IPAMScope},
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
			ipamID := store.ResourceID("aws", acct.ID, *attrs.IpamArn)
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
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeEC2IPAMPool},
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
			scopeID := store.ResourceID("aws", acct.ID, *attrs.IpamScopeArn)
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
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeEC2IPAMResourceDiscoveryAssociation},
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
			ipamID := store.ResourceID("aws", acct.ID, *attrs.IpamArn)
			if err := st.UpsertRelationship(r.ID, ipamID, store.RelAttachedTo, "directed", nil); err != nil {
				return fmt.Errorf("upsert ipam-rda→ipam relationship: %w", err)
			}
		}
		if attrs.IpamResourceDiscoveryArn != nil {
			rdID := store.ResourceID("aws", acct.ID, *attrs.IpamResourceDiscoveryArn)
			if err := st.UpsertRelationship(r.ID, rdID, store.RelUses, "directed", nil); err != nil {
				return fmt.Errorf("upsert ipam-rda→resource-discovery relationship: %w", err)
			}
		}
	}
	return nil
}

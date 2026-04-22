package gcp

import (
	"encoding/json"
	"fmt"

	"codeberg.org/icearp/disco/internal/store"
	"codeberg.org/icearp/disco/internal/util"
)

func init() {
	registerResolver(resolveComputeInstanceRelationships)
	registerResolver(resolveSubnetworkRelationships)
}

func resolveComputeInstanceRelationships(p *project, st *store.Store) error {
	instances, err := st.ListResources(store.ResourceFilter{
		Provider: "gcp", AccountID: p.ID, Types: []string{TypeComputeInstance},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	for _, r := range instances {
		var attrs struct {
			NetworkInterfaces []struct {
				Network    string `json:"network"`
				Subnetwork string `json:"subnetwork"`
			} `json:"networkInterfaces"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		for _, nic := range attrs.NetworkInterfaces {
			if nic.Network != "" {
				netID := store.ResourceID("gcp", p.ID, TypeComputeNetwork, nic.Network)
				if err := st.UpsertRelationship(r.ID, netID, store.RelAttachedTo, "directed", nil); err != nil {
					return fmt.Errorf("upsert instance→network relationship: %w", err)
				}
			}
			if nic.Subnetwork != "" {
				snID := store.ResourceID("gcp", p.ID, TypeComputeSubnet, nic.Subnetwork)
				if err := st.UpsertRelationship(r.ID, snID, store.RelAttachedTo, "directed", nil); err != nil {
					return fmt.Errorf("upsert instance→subnetwork relationship: %w", err)
				}
			}
		}
	}
	return nil
}

func resolveSubnetworkRelationships(p *project, st *store.Store) error {
	subnets, err := st.ListResources(store.ResourceFilter{
		Provider: "gcp", AccountID: p.ID, Types: []string{TypeComputeSubnet},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	for _, r := range subnets {
		var attrs struct {
			Network string `json:"network"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if attrs.Network != "" {
			netID := store.ResourceID("gcp", p.ID, TypeComputeNetwork, attrs.Network)
			if err := st.UpsertRelationship(r.ID, netID, store.RelAttachedTo, "directed", nil); err != nil {
				return fmt.Errorf("upsert subnetwork→network relationship: %w", err)
			}
		}
	}
	return nil
}

package gcp

import (
	"encoding/json"
	"fmt"

	"codeburg.org/icearp/disco/internal/store"
	"codeburg.org/icearp/disco/internal/util"
)

func resolveComputeInstanceRelationships(p *project, st *store.Store) error {
	instances, err := st.ListResources(store.ResourceFilter{
		Provider: "gcp", AccountID: p.ID, Types: []string{"gcp:compute:instance"},
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
				netID := store.ResourceID("gcp", p.ID, "gcp:compute:network", nic.Network)
				if err := st.UpsertRelationship(r.ID, netID, store.RelAttachedTo, "directed", nil); err != nil {
					return fmt.Errorf("upsert instance→network relationship: %w", err)
				}
			}
			if nic.Subnetwork != "" {
				snID := store.ResourceID("gcp", p.ID, "gcp:compute:subnetwork", nic.Subnetwork)
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
		Provider: "gcp", AccountID: p.ID, Types: []string{"gcp:compute:subnetwork"},
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
			netID := store.ResourceID("gcp", p.ID, "gcp:compute:network", attrs.Network)
			if err := st.UpsertRelationship(r.ID, netID, store.RelAttachedTo, "directed", nil); err != nil {
				return fmt.Errorf("upsert subnetwork→network relationship: %w", err)
			}
		}
	}
	return nil
}

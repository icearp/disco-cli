package gcp

import (
	"context"
	"encoding/json"
	"math"

	"codeburg.org/icearp/disco/internal/store"
)

const allResources = uint64(math.MaxUint32)

// resolveRelationships is phase 2 for GCP: derive edges between resources that
// have already been written to the DB.
func resolveRelationships(_ context.Context, p *project, st *store.Store) error {
	if err := resolveComputeInstanceRelationships(p, st); err != nil {
		return err
	}
	return resolveSubnetworkRelationships(p, st)
}

func resolveComputeInstanceRelationships(p *project, st *store.Store) error {
	instances, err := st.ListResources(store.ResourceFilter{
		Provider: "gcp", AccountID: p.ID, Types: []string{"gcp:compute:instance"},
		Limit: allResources,
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
				_ = st.UpsertRelationship(r.ID, netID, store.RelAttachedTo, "directed", nil)
			}
			if nic.Subnetwork != "" {
				snID := store.ResourceID("gcp", p.ID, "gcp:compute:subnetwork", nic.Subnetwork)
				_ = st.UpsertRelationship(r.ID, snID, store.RelAttachedTo, "directed", nil)
			}
		}
	}
	return nil
}

func resolveSubnetworkRelationships(p *project, st *store.Store) error {
	subnets, err := st.ListResources(store.ResourceFilter{
		Provider: "gcp", AccountID: p.ID, Types: []string{"gcp:compute:subnetwork"},
		Limit: allResources,
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
			_ = st.UpsertRelationship(r.ID, netID, store.RelAttachedTo, "directed", nil)
		}
	}
	return nil
}

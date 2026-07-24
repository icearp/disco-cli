package gcp

import (
	"encoding/json"
	"fmt"

	"github.com/icearp/disco-cli/internal/util"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerResolver(resolveFirewallRelationships,
		EdgeDecl{TypeComputeFirewall, TypeComputeNetwork, store.RelAttachedTo},
		EdgeDecl{TypeComputeFirewall, TypeComputeInstance, store.RelUses},
	)
}

// resolveFirewallRelationships derives two firewall edge classes:
//
//  1. firewall -[attached-to]-> network (VPC) via the firewall's `network` URL.
//  2. firewall -[uses]-> instance for every instance whose `tags.items[]`
//     intersects the firewall's `targetTags[]` AND whose primary NIC's
//     `network` matches the firewall's network. Firewalls with no targetTags
//     are not exploded into per-instance edges — they apply to every instance
//     in the network, already conveyed by the firewall→network edge; reverse-graph
//     from the network surfaces membership.
//
// targetServiceAccounts → service-account edges deferred: rarer than tag-based
// targeting, and the SA email index pattern from R4.1 is reusable for that follow-up.
func resolveFirewallRelationships(p *project, st *store.Store) error {
	fws, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"gcp"}, AccountID: p.ID, Types: []string{TypeComputeFirewall},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(fws) == 0 {
		return nil
	}

	type fwAttrs struct {
		Network    string   `json:"network"`
		TargetTags []string `json:"targetTags"`
	}
	type fwParsed struct {
		id      string
		network string
		tags    map[string]bool
	}
	parsed := make([]fwParsed, 0, len(fws))
	for _, fw := range fws {
		var a fwAttrs
		if err := json.Unmarshal([]byte(fw.AttributesJSON), &a); err != nil {
			continue
		}
		if a.Network != "" {
			netID := store.ResourceID("gcp", p.ID, a.Network)
			if err := st.UpsertRelationship(fw.ID, netID, store.RelAttachedTo, "directed", nil); err != nil {
				return fmt.Errorf("upsert firewall→network: %w", err)
			}
		}
		if len(a.TargetTags) == 0 {
			continue
		}
		tagSet := make(map[string]bool, len(a.TargetTags))
		for _, t := range a.TargetTags {
			tagSet[t] = true
		}
		parsed = append(parsed, fwParsed{id: fw.ID, network: a.Network, tags: tagSet})
	}
	if len(parsed) == 0 {
		return nil
	}

	// Fan out: per instance, check tags against each tag-targeting firewall in the
	// same network. Linear scan is fine — firewall/instance counts per project are modest.
	insts, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"gcp"}, AccountID: p.ID, Types: []string{TypeComputeInstance},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	type instAttrs struct {
		Tags struct {
			Items []string `json:"items"`
		} `json:"tags"`
		NetworkInterfaces []struct {
			Network string `json:"network"`
		} `json:"networkInterfaces"`
	}
	for _, inst := range insts {
		var a instAttrs
		if err := json.Unmarshal([]byte(inst.AttributesJSON), &a); err != nil {
			continue
		}
		if len(a.Tags.Items) == 0 {
			continue
		}
		instNets := make(map[string]bool, len(a.NetworkInterfaces))
		for _, n := range a.NetworkInterfaces {
			if n.Network != "" {
				instNets[n.Network] = true
			}
		}
		for _, fw := range parsed {
			if !instNets[fw.network] {
				continue
			}
			matched := false
			for _, t := range a.Tags.Items {
				if fw.tags[t] {
					matched = true
					break
				}
			}
			if !matched {
				continue
			}
			if err := st.UpsertRelationship(fw.id, inst.ID, store.RelUses, "directed", nil); err != nil {
				return fmt.Errorf("upsert firewall→instance: %w", err)
			}
		}
	}
	return nil
}

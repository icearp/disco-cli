package gcp

import (
	"encoding/json"

	"codeberg.org/icearp/disco/internal/util"
	"codeberg.org/icearp/disco/store"
)

// Resolver Wave R6 of the resolver-implementation backlog (ROADMAP.md
// "Resolver buildout"): the unambiguous single-target-type edges remaining in
// the "compute" bucket — Network's own peering/firewall-policy links,
// NetworkFirewallPolicy's network attachment, NetworkAttachment,
// ServiceAttachment, RegionCommitment, and NodeGroup. Fields whose self-link
// target type is genuinely ambiguous (BackendService.HealthChecks mixing
// modern/legacy health-check types, Backend.Group spanning five possible
// group/NEG types, Autoscaler.Target, HealthCheckService/HealthSource/
// CompositeHealthCheck's cross-referencing chain) are deferred to a later
// wave rather than guessed — see ROADMAP.md Wave R6 note.
func init() {
	registerResolver(resolveNetworkRelationships,
		EdgeDecl{TypeComputeNetwork, TypeComputeNetworkFirewallPolicy, store.RelAttachedTo},
		EdgeDecl{TypeComputeNetwork, TypeComputeNetwork, store.RelAttachedTo},
	)
	registerResolver(resolveNetworkFirewallPolicyRelationships,
		EdgeDecl{TypeComputeNetworkFirewallPolicy, TypeComputeNetwork, store.RelAttachedTo},
		EdgeDecl{TypeComputeRegionNetworkFirewallPolicy, TypeComputeNetwork, store.RelAttachedTo},
	)
	registerResolver(resolveNetworkAttachmentRelationships,
		EdgeDecl{TypeComputeNetworkAttachment, TypeComputeNetwork, store.RelAttachedTo},
		EdgeDecl{TypeComputeNetworkAttachment, TypeComputeSubnet, store.RelAttachedTo},
	)
	registerResolver(resolveServiceAttachmentRelationships,
		EdgeDecl{TypeComputeServiceAttachment, TypeComputeForwardingRule, store.RelAttachedTo},
	)
	registerResolver(resolveRegionCommitmentRelationships,
		EdgeDecl{TypeComputeRegionCommitment, TypeComputeReservation, store.RelAttachedTo},
	)
	registerResolver(resolveNodeGroupRelationships,
		EdgeDecl{TypeComputeNodeGroup, TypeComputeNodeTemplate, store.RelUses},
	)
}

// resolveNetworkRelationships wires Network -> its associated
// NetworkFirewallPolicy (network's own `firewallPolicy` self-link) and
// Network -> each peer Network (`peerings[].network`).
func resolveNetworkRelationships(p *project, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"gcp"}, AccountID: p.ID, Types: []string{TypeComputeNetwork},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	scanned, err := scannedIDSet(p, st, TypeComputeNetworkFirewallPolicy, TypeComputeNetwork)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			FirewallPolicy string `json:"firewallPolicy"`
			Peerings       []struct {
				Network string `json:"network"`
			} `json:"peerings"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if err := upsertIfScanned(st, scanned, r.ID, "gcp", p.ID, TypeComputeNetworkFirewallPolicy, attrs.FirewallPolicy, store.RelAttachedTo); err != nil {
			return err
		}
		for _, peering := range attrs.Peerings {
			if err := upsertIfScanned(st, scanned, r.ID, "gcp", p.ID, TypeComputeNetwork, peering.Network, store.RelAttachedTo); err != nil {
				return err
			}
		}
	}
	return nil
}

// resolveNetworkFirewallPolicyRelationships wires (Region)NetworkFirewallPolicy
// -> the Network each association attaches to. Both disco types share the
// compute.FirewallPolicy struct (see compute_networking_scanners.go), so one
// resolver covers both.
func resolveNetworkFirewallPolicyRelationships(p *project, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"gcp"}, AccountID: p.ID,
		Types: []string{TypeComputeNetworkFirewallPolicy, TypeComputeRegionNetworkFirewallPolicy},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	scanned, err := scannedIDSet(p, st, TypeComputeNetwork)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			Associations []struct {
				AttachmentTarget string `json:"attachmentTarget"`
			} `json:"associations"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		for _, a := range attrs.Associations {
			if err := upsertIfScanned(st, scanned, r.ID, "gcp", p.ID, TypeComputeNetwork, a.AttachmentTarget, store.RelAttachedTo); err != nil {
				return err
			}
		}
	}
	return nil
}

func resolveNetworkAttachmentRelationships(p *project, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"gcp"}, AccountID: p.ID, Types: []string{TypeComputeNetworkAttachment},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	scanned, err := scannedIDSet(p, st, TypeComputeNetwork, TypeComputeSubnet)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			Network     string   `json:"network"`
			Subnetworks []string `json:"subnetworks"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if err := upsertIfScanned(st, scanned, r.ID, "gcp", p.ID, TypeComputeNetwork, attrs.Network, store.RelAttachedTo); err != nil {
			return err
		}
		for _, sub := range attrs.Subnetworks {
			if err := upsertIfScanned(st, scanned, r.ID, "gcp", p.ID, TypeComputeSubnet, sub, store.RelAttachedTo); err != nil {
				return err
			}
		}
	}
	return nil
}

// resolveServiceAttachmentRelationships wires ServiceAttachment -> the
// producer ForwardingRule serving it. ServiceAttachment is always a regional
// resource, so producerForwardingRule always names a regional ForwardingRule,
// never a GlobalForwardingRule.
func resolveServiceAttachmentRelationships(p *project, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"gcp"}, AccountID: p.ID, Types: []string{TypeComputeServiceAttachment},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	scanned, err := scannedIDSet(p, st, TypeComputeForwardingRule)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			ProducerForwardingRule string `json:"producerForwardingRule"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if err := upsertIfScanned(st, scanned, r.ID, "gcp", p.ID, TypeComputeForwardingRule, attrs.ProducerForwardingRule, store.RelAttachedTo); err != nil {
			return err
		}
	}
	return nil
}

func resolveRegionCommitmentRelationships(p *project, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"gcp"}, AccountID: p.ID, Types: []string{TypeComputeRegionCommitment},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	scanned, err := scannedIDSet(p, st, TypeComputeReservation)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			ExistingReservations []string `json:"existingReservations"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		for _, res := range attrs.ExistingReservations {
			if err := upsertIfScanned(st, scanned, r.ID, "gcp", p.ID, TypeComputeReservation, res, store.RelAttachedTo); err != nil {
				return err
			}
		}
	}
	return nil
}

func resolveNodeGroupRelationships(p *project, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"gcp"}, AccountID: p.ID, Types: []string{TypeComputeNodeGroup},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	scanned, err := scannedIDSet(p, st, TypeComputeNodeTemplate)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			NodeTemplate string `json:"nodeTemplate"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if err := upsertIfScanned(st, scanned, r.ID, "gcp", p.ID, TypeComputeNodeTemplate, attrs.NodeTemplate, store.RelUses); err != nil {
			return err
		}
	}
	return nil
}

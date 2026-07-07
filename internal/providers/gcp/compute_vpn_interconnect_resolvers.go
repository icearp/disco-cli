package gcp

import (
	"encoding/json"

	"codeberg.org/icearp/disco/internal/util"
	"codeberg.org/icearp/disco/store"
)

// Resolver Wave R5 of the resolver-implementation backlog (ROADMAP.md
// "Resolver buildout"): the VPN (VpnGateway/VpnTunnel/TargetVpnGateway) and
// Interconnect (InterconnectAttachment/-Group, InterconnectGroup, WireGroup,
// NetworkEdgeSecurityService) surfaces. All fields read straight off
// already-scanned AttributesJSON.
func init() {
	registerResolver(resolveVpnRelationships,
		EdgeDecl{TypeComputeVpnGateway, TypeComputeNetwork, store.RelAttachedTo},
		EdgeDecl{TypeComputeTargetVpnGateway, TypeComputeNetwork, store.RelAttachedTo},
		EdgeDecl{TypeComputeVpnTunnel, TypeComputeVpnGateway, store.RelAttachedTo},
		EdgeDecl{TypeComputeVpnTunnel, TypeComputeTargetVpnGateway, store.RelAttachedTo},
		EdgeDecl{TypeComputeVpnTunnel, TypeComputeRouter, store.RelAttachedTo},
	)
	registerResolver(resolveInterconnectRelationships,
		EdgeDecl{TypeComputeInterconnectAttachment, TypeComputeInterconnect, store.RelAttachedTo},
		EdgeDecl{TypeComputeInterconnectAttachment, TypeComputeRouter, store.RelAttachedTo},
		EdgeDecl{TypeComputeInterconnectAttachmentGroup, TypeComputeInterconnectAttachment, store.RelAttachedTo},
		EdgeDecl{TypeComputeInterconnectGroup, TypeComputeInterconnect, store.RelAttachedTo},
		EdgeDecl{TypeComputeWireGroup, TypeComputeInterconnect, store.RelAttachedTo},
		EdgeDecl{TypeComputeNetworkEdgeSecurityService, TypeComputeSecurityPolicy, store.RelUses},
	)
}

// resolveVpnRelationships wires the classic-VPN and HA-VPN chain:
// (target)VpnGateway → network; vpnTunnel → its gateway(s) and Cloud Router.
// vpnTunnel.peerGcpGateway (HA-VPN-to-HA-VPN peering) is deferred — a genuine
// second edge target, but rare enough (single-project HA-VPN mesh) to defer
// past this wave rather than complicate the shared attrs struct further.
func resolveVpnRelationships(p *project, st *store.Store) error {
	gateways, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"gcp"}, AccountID: p.ID,
		Types: []string{TypeComputeVpnGateway, TypeComputeTargetVpnGateway},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	tunnels, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"gcp"}, AccountID: p.ID, Types: []string{TypeComputeVpnTunnel},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(gateways) == 0 && len(tunnels) == 0 {
		return nil
	}
	scanned, err := scannedIDSet(p, st, TypeComputeNetwork, TypeComputeVpnGateway, TypeComputeTargetVpnGateway, TypeComputeRouter)
	if err != nil {
		return err
	}
	for _, r := range gateways {
		var attrs struct {
			Network string `json:"network"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if err := upsertIfScanned(st, scanned, r.ID, "gcp", p.ID, TypeComputeNetwork, attrs.Network, store.RelAttachedTo); err != nil {
			return err
		}
	}
	for _, r := range tunnels {
		var attrs struct {
			VpnGateway       string `json:"vpnGateway"`
			TargetVpnGateway string `json:"targetVpnGateway"`
			Router           string `json:"router"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if err := upsertIfScanned(st, scanned, r.ID, "gcp", p.ID, TypeComputeVpnGateway, attrs.VpnGateway, store.RelAttachedTo); err != nil {
			return err
		}
		if err := upsertIfScanned(st, scanned, r.ID, "gcp", p.ID, TypeComputeTargetVpnGateway, attrs.TargetVpnGateway, store.RelAttachedTo); err != nil {
			return err
		}
		if err := upsertIfScanned(st, scanned, r.ID, "gcp", p.ID, TypeComputeRouter, attrs.Router, store.RelAttachedTo); err != nil {
			return err
		}
	}
	return nil
}

// resolveInterconnectRelationships wires the physical/logical interconnect
// hierarchy: attachment → interconnect + router; attachment-group → its
// member attachments; interconnect-group → its member interconnects;
// wire-group → the interconnects its wires' endpoints terminate on;
// NetworkEdgeSecurityService → the Cloud Armor SecurityPolicy it wraps.
// Interconnect itself carries no outbound resolver-worthy self-link (all
// fields describe the physical circuit) and is Leaf-flagged accordingly.
func resolveInterconnectRelationships(p *project, st *store.Store) error {
	scanned, err := scannedIDSet(p, st,
		TypeComputeInterconnect, TypeComputeRouter,
		TypeComputeInterconnectAttachment, TypeComputeSecurityPolicy,
	)
	if err != nil {
		return err
	}

	attachments, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"gcp"}, AccountID: p.ID, Types: []string{TypeComputeInterconnectAttachment},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	for _, r := range attachments {
		var attrs struct {
			Interconnect string `json:"interconnect"`
			Router       string `json:"router"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if err := upsertIfScanned(st, scanned, r.ID, "gcp", p.ID, TypeComputeInterconnect, attrs.Interconnect, store.RelAttachedTo); err != nil {
			return err
		}
		if err := upsertIfScanned(st, scanned, r.ID, "gcp", p.ID, TypeComputeRouter, attrs.Router, store.RelAttachedTo); err != nil {
			return err
		}
	}

	attachmentGroups, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"gcp"}, AccountID: p.ID, Types: []string{TypeComputeInterconnectAttachmentGroup},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	for _, r := range attachmentGroups {
		var attrs struct {
			Attachments map[string]struct {
				Attachment string `json:"attachment"`
			} `json:"attachments"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		for _, a := range attrs.Attachments {
			if err := upsertIfScanned(st, scanned, r.ID, "gcp", p.ID, TypeComputeInterconnectAttachment, a.Attachment, store.RelAttachedTo); err != nil {
				return err
			}
		}
	}

	interconnectGroups, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"gcp"}, AccountID: p.ID, Types: []string{TypeComputeInterconnectGroup},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	for _, r := range interconnectGroups {
		var attrs struct {
			Interconnects map[string]struct {
				Interconnect string `json:"interconnect"`
			} `json:"interconnects"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		for _, ic := range attrs.Interconnects {
			if err := upsertIfScanned(st, scanned, r.ID, "gcp", p.ID, TypeComputeInterconnect, ic.Interconnect, store.RelAttachedTo); err != nil {
				return err
			}
		}
	}

	wireGroups, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"gcp"}, AccountID: p.ID, Types: []string{TypeComputeWireGroup},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	for _, r := range wireGroups {
		var attrs struct {
			Wires []struct {
				Endpoints []struct {
					Interconnect string `json:"interconnect"`
				} `json:"endpoints"`
			} `json:"wires"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		for _, w := range attrs.Wires {
			for _, ep := range w.Endpoints {
				if err := upsertIfScanned(st, scanned, r.ID, "gcp", p.ID, TypeComputeInterconnect, ep.Interconnect, store.RelAttachedTo); err != nil {
					return err
				}
			}
		}
	}

	edgeSecurityServices, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"gcp"}, AccountID: p.ID, Types: []string{TypeComputeNetworkEdgeSecurityService},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	for _, r := range edgeSecurityServices {
		var attrs struct {
			SecurityPolicy string `json:"securityPolicy"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if err := upsertIfScanned(st, scanned, r.ID, "gcp", p.ID, TypeComputeSecurityPolicy, attrs.SecurityPolicy, store.RelUses); err != nil {
			return err
		}
	}
	return nil
}

package gcp

import (
	"encoding/json"

	"codeberg.org/icearp/disco/internal/util"
	"codeberg.org/icearp/disco/store"
)

// Resolver Wave R3 of the resolver-implementation backlog (ROADMAP.md
// "Resolver buildout"): Address/GlobalAddress network attachment, Router
// network attachment, and Route's network + next-hop edges. All fields read
// straight off already-scanned AttributesJSON.
func init() {
	registerResolver(resolveComputeAddressRelationships,
		EdgeDecl{TypeComputeAddress, TypeComputeNetwork, store.RelAttachedTo},
		EdgeDecl{TypeComputeAddress, TypeComputeSubnet, store.RelAttachedTo},
		EdgeDecl{TypeComputeGlobalAddress, TypeComputeNetwork, store.RelAttachedTo},
		EdgeDecl{TypeComputeGlobalAddress, TypeComputeSubnet, store.RelAttachedTo},
	)
	registerResolver(resolveRouterRelationships,
		EdgeDecl{TypeComputeRouter, TypeComputeNetwork, store.RelAttachedTo},
	)
	registerResolver(resolveRouteRelationships,
		EdgeDecl{TypeComputeRoute, TypeComputeNetwork, store.RelAttachedTo},
		EdgeDecl{TypeComputeRoute, TypeComputeInstance, store.RelAttachedTo},
		EdgeDecl{TypeComputeRoute, TypeComputeVpnTunnel, store.RelAttachedTo},
		EdgeDecl{TypeComputeRoute, TypeComputeInterconnectAttachment, store.RelAttachedTo},
		EdgeDecl{TypeComputeRoute, TypeComputeForwardingRule, store.RelAttachedTo},
	)
}

func resolveComputeAddressRelationships(p *project, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"gcp"}, AccountID: p.ID,
		Types: []string{TypeComputeAddress, TypeComputeGlobalAddress},
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
			Network    string `json:"network"`
			Subnetwork string `json:"subnetwork"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if err := upsertIfScanned(st, scanned, r.ID, "gcp", p.ID, TypeComputeNetwork, attrs.Network, store.RelAttachedTo); err != nil {
			return err
		}
		if err := upsertIfScanned(st, scanned, r.ID, "gcp", p.ID, TypeComputeSubnet, attrs.Subnetwork, store.RelAttachedTo); err != nil {
			return err
		}
	}
	return nil
}

func resolveRouterRelationships(p *project, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"gcp"}, AccountID: p.ID, Types: []string{TypeComputeRouter},
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
			Network string `json:"network"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if err := upsertIfScanned(st, scanned, r.ID, "gcp", p.ID, TypeComputeNetwork, attrs.Network, store.RelAttachedTo); err != nil {
			return err
		}
	}
	return nil
}

// resolveRouteRelationships wires each Route to its network and (when
// present) the concrete next-hop resource. NextHopGateway is excluded — its
// value is either the literal sentinel "default-internet-gateway" or a
// self-link to a pseudo-resource GCP doesn't expose as a scannable type.
// NextHopIp/NextHopMed/etc are scalars with no resource identity.
func resolveRouteRelationships(p *project, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"gcp"}, AccountID: p.ID, Types: []string{TypeComputeRoute},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	scanned, err := scannedIDSet(p, st,
		TypeComputeNetwork, TypeComputeInstance, TypeComputeVpnTunnel,
		TypeComputeInterconnectAttachment, TypeComputeForwardingRule,
	)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			Network                       string `json:"network"`
			NextHopNetwork                string `json:"nextHopNetwork"`
			NextHopInstance               string `json:"nextHopInstance"`
			NextHopVpnTunnel              string `json:"nextHopVpnTunnel"`
			NextHopInterconnectAttachment string `json:"nextHopInterconnectAttachment"`
			NextHopIlb                    string `json:"nextHopIlb"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		edges := []struct {
			rtype    string
			selfLink string
		}{
			{TypeComputeNetwork, attrs.Network},
			{TypeComputeNetwork, attrs.NextHopNetwork},
			{TypeComputeInstance, attrs.NextHopInstance},
			{TypeComputeVpnTunnel, attrs.NextHopVpnTunnel},
			{TypeComputeInterconnectAttachment, attrs.NextHopInterconnectAttachment},
			{TypeComputeForwardingRule, attrs.NextHopIlb},
		}
		for _, e := range edges {
			if err := upsertIfScanned(st, scanned, r.ID, "gcp", p.ID, e.rtype, e.selfLink, store.RelAttachedTo); err != nil {
				return err
			}
		}
	}
	return nil
}

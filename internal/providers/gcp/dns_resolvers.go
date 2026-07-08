package gcp

import (
	"encoding/json"
	"fmt"

	"codeberg.org/icearp/disco/internal/util"
	"codeberg.org/icearp/disco/store"
)

func init() {
	registerResolver(resolveDNSRelationships,
		EdgeDecl{TypeDNSRecordSet, TypeComputeForwardingRule, store.RelRoutesTo},
	)
	registerResolver(resolveDNSManagedZoneRelationships,
		EdgeDecl{TypeDNSManagedZone, TypeComputeNetwork, store.RelAttachedTo},
		EdgeDecl{TypeDNSManagedZone, TypeComputeNetwork, store.RelUses},
	)
	registerResolver(resolveDNSPolicyRelationships,
		EdgeDecl{TypeDNSPolicy, TypeComputeNetwork, store.RelAttachedTo},
	)
	registerResolver(resolveDNSResponsePolicyRelationships,
		EdgeDecl{TypeDNSResponsePolicy, TypeComputeNetwork, store.RelAttachedTo},
	)
}

// resolveDNSRelationships derives A/AAAA record-set -[routes-to]-> forwarding-rule
// edges by matching the record's `rrdatas[]` IP literals against each
// forwarding rule's `IPAddress`. Forwarding rules (esp. global LB) are the
// canonical target for "DNS hostname points to load balancer."
//
// Deferred:
//   - CNAME → record-set / forwarding-rule (CNAME points at a name, not an
//     IP; needs canonical-name resolution chain across DNS resources).
//   - record-set → public IP resource (gcp:compute:address) — public-IP
//     scanner not yet implemented.
//   - record-set → backend-bucket / storage bucket (CDN-fronted; needs
//     CNAME chain or bucket-domain match).
//   - DNS routing-policy targets (GeoLB / WRR) — skip until rule-engine
//     queries them.
func resolveDNSRelationships(p *project, st *store.Store) error {
	frs, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"gcp"}, AccountID: p.ID, Types: []string{TypeComputeForwardingRule},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(frs) == 0 {
		return nil
	}
	// Multiple forwarding rules can share an IP across protocols (HTTP +
	// HTTPS) — collect all matches per IP rather than picking one.
	frsByIP := make(map[string][]string, len(frs))
	for _, fr := range frs {
		var a struct {
			IPAddress string `json:"IPAddress"`
		}
		if err := json.Unmarshal([]byte(fr.AttributesJSON), &a); err != nil {
			continue
		}
		if a.IPAddress == "" {
			continue
		}
		frsByIP[a.IPAddress] = append(frsByIP[a.IPAddress], fr.ID)
	}
	if len(frsByIP) == 0 {
		return nil
	}

	rrsets, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"gcp"}, AccountID: p.ID, Types: []string{TypeDNSRecordSet},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	for _, rr := range rrsets {
		var a struct {
			Type    string   `json:"type"`
			Rrdatas []string `json:"rrdatas"`
		}
		if err := json.Unmarshal([]byte(rr.AttributesJSON), &a); err != nil {
			continue
		}
		if a.Type != "A" && a.Type != "AAAA" {
			continue
		}
		for _, ip := range a.Rrdatas {
			for _, frID := range frsByIP[ip] {
				if err := st.UpsertRelationship(rr.ID, frID, store.RelRoutesTo, "directed", nil); err != nil {
					return fmt.Errorf("upsert recordSet→forwardingRule: %w", err)
				}
			}
		}
	}
	return nil
}

// networkIDByNetworkURL indexes every scanned Compute Network by its own
// NativeID (a Compute API self-link URL). DNS's Network-binding fields
// (`ManagedZone.privateVisibilityConfig.networks[].networkUrl`,
// `.peeringConfig.targetNetwork.networkUrl`, `Policy.networks[].networkUrl`,
// `ResponsePolicy.networks[].networkUrl`) are documented as that exact same
// self-link shape — unlike Wave R9's cross-API relative-path mismatches,
// this is an exact match, no bare-name conversion needed.
func networkIDByNetworkURL(p *project, st *store.Store) (map[string]string, error) {
	return nativeIDIndex(p, st, TypeComputeNetwork)
}

// resolveDNSManagedZoneRelationships wires ManagedZone -> Network:
// `privateVisibilityConfig.networks[]` (the zone is visible from / bound to
// these VPCs, an attached-to structural relationship) and
// `peeringConfig.targetNetwork` (the zone forwards queries to this VPC for
// resolution, a uses dependency).
func resolveDNSManagedZoneRelationships(p *project, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"gcp"}, AccountID: p.ID, Types: []string{TypeDNSManagedZone},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	netIDByURL, err := networkIDByNetworkURL(p, st)
	if err != nil {
		return err
	}
	if len(netIDByURL) == 0 {
		return nil
	}
	for _, r := range rows {
		var attrs struct {
			PrivateVisibilityConfig *struct {
				Networks []struct {
					NetworkURL string `json:"networkUrl"`
				} `json:"networks"`
			} `json:"privateVisibilityConfig"`
			PeeringConfig *struct {
				TargetNetwork *struct {
					NetworkURL string `json:"networkUrl"`
				} `json:"targetNetwork"`
			} `json:"peeringConfig"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if attrs.PrivateVisibilityConfig != nil {
			for _, n := range attrs.PrivateVisibilityConfig.Networks {
				if netID, ok := netIDByURL[n.NetworkURL]; ok {
					if err := st.UpsertRelationship(r.ID, netID, store.RelAttachedTo, "directed", nil); err != nil {
						return fmt.Errorf("upsert managedZone→network (visibility): %w", err)
					}
				}
			}
		}
		if attrs.PeeringConfig != nil && attrs.PeeringConfig.TargetNetwork != nil {
			if netID, ok := netIDByURL[attrs.PeeringConfig.TargetNetwork.NetworkURL]; ok {
				if err := st.UpsertRelationship(r.ID, netID, store.RelUses, "directed", nil); err != nil {
					return fmt.Errorf("upsert managedZone→network (peering): %w", err)
				}
			}
		}
	}
	return nil
}

// resolveDNSPolicyRelationships wires Policy -> the Network(s) it's bound to
// (`networks[].networkUrl`).
func resolveDNSPolicyRelationships(p *project, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"gcp"}, AccountID: p.ID, Types: []string{TypeDNSPolicy},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	netIDByURL, err := networkIDByNetworkURL(p, st)
	if err != nil {
		return err
	}
	if len(netIDByURL) == 0 {
		return nil
	}
	for _, r := range rows {
		var attrs struct {
			Networks []struct {
				NetworkURL string `json:"networkUrl"`
			} `json:"networks"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		for _, n := range attrs.Networks {
			if netID, ok := netIDByURL[n.NetworkURL]; ok {
				if err := st.UpsertRelationship(r.ID, netID, store.RelAttachedTo, "directed", nil); err != nil {
					return fmt.Errorf("upsert policy→network: %w", err)
				}
			}
		}
	}
	return nil
}

// resolveDNSResponsePolicyRelationships wires ResponsePolicy -> the
// Network(s) it applies to (`networks[].networkUrl`) — same field shape as
// Policy, distinct type (Response Policy is a rewrite-rule collection, not
// the forwarding/logging Policy above).
func resolveDNSResponsePolicyRelationships(p *project, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"gcp"}, AccountID: p.ID, Types: []string{TypeDNSResponsePolicy},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	netIDByURL, err := networkIDByNetworkURL(p, st)
	if err != nil {
		return err
	}
	if len(netIDByURL) == 0 {
		return nil
	}
	for _, r := range rows {
		var attrs struct {
			Networks []struct {
				NetworkURL string `json:"networkUrl"`
			} `json:"networks"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		for _, n := range attrs.Networks {
			if netID, ok := netIDByURL[n.NetworkURL]; ok {
				if err := st.UpsertRelationship(r.ID, netID, store.RelAttachedTo, "directed", nil); err != nil {
					return fmt.Errorf("upsert responsePolicy→network: %w", err)
				}
			}
		}
	}
	return nil
}

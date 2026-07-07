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

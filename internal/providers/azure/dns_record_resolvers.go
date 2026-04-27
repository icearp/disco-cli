package azure

import (
	"encoding/json"
	"fmt"
	"strings"

	"codeberg.org/icearp/disco/internal/store"
	"codeberg.org/icearp/disco/internal/util"
)

func init() { registerResolver(resolveDNSRecordSetRelationships) }

// resolveDNSRecordSetRelationships derives DNS record-set -[uses]-> Public IP
// edges. Walks both public and private A record sets and matches each
// `properties.aRecords[].ipv4Address` against a per-sub PIP IP-index built
// from `azure:microsoft.network:public-ip-address.properties.ipAddress`.
//
// CNAME / AAAA / SRV / MX / PTR / NS / TXT / CAA records intentionally
// deferred. CNAME requires FQDN→ARM-ID reverse-lookup with per-service DNS
// suffix tables (azurewebsites.net → AppService site, *.cloudapp.azure.com
// → public IP, *.azurefd.net → Front Door, etc.), which is its own iteration.
// AAAA requires PIP IPv6 capture which the network scanner does not yet
// surface as a separate index. Both deferred to follow-up.
func resolveDNSRecordSetRelationships(sub *subscription, st *store.Store) error {
	records, err := st.ListResources(store.ResourceFilter{
		Provider: "azure", AccountID: sub.ID,
		Types: []string{TypeDNSRecordSet, TypeDNSPrivateRecordSet},
		Limit: util.AllResources,
	})
	if err != nil || len(records) == 0 {
		return err
	}
	pips, err := st.ListResources(store.ResourceFilter{
		Provider: "azure", AccountID: sub.ID,
		Types: []string{TypeNetworkPublicIPAddress},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(pips) == 0 {
		return nil
	}
	pipByIP := make(map[string]string, len(pips))
	for _, p := range pips {
		var attrs struct {
			Properties *struct {
				IPAddress *string `json:"ipAddress"`
			} `json:"properties"`
		}
		if err := json.Unmarshal([]byte(p.AttributesJSON), &attrs); err != nil {
			continue
		}
		if attrs.Properties != nil && attrs.Properties.IPAddress != nil && *attrs.Properties.IPAddress != "" {
			pipByIP[*attrs.Properties.IPAddress] = p.ID
		}
	}
	if len(pipByIP) == 0 {
		return nil
	}

	for _, r := range records {
		// Skip non-A records cheaply via the type segment of the NativeID
		// before unmarshaling — covers SRV/MX/PTR/etc.
		if !strings.EqualFold(recordTypeFromID(r.NativeID), "A") {
			continue
		}
		var attrs struct {
			Properties *struct {
				ARecords []struct {
					IPv4Address *string `json:"ipv4Address"`
				} `json:"aRecords"`
			} `json:"properties"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil || attrs.Properties == nil {
			continue
		}
		seen := map[string]bool{}
		for _, a := range attrs.Properties.ARecords {
			if a.IPv4Address == nil || *a.IPv4Address == "" {
				continue
			}
			ip := *a.IPv4Address
			if seen[ip] {
				continue
			}
			seen[ip] = true
			toID, ok := pipByIP[ip]
			if !ok {
				continue
			}
			if err := st.UpsertRelationship(r.ID, toID, store.RelUses, "directed", nil); err != nil {
				return fmt.Errorf("upsert dns record→pip: %w", err)
			}
		}
	}
	return nil
}

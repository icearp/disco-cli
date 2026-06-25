package azure

import (
	"encoding/json"
	"fmt"
	"net"
	"strings"

	"codeberg.org/icearp/disco/internal/util"
	"codeberg.org/icearp/disco/store"
)

func init() {
	registerResolver(
		resolveDNSRecordSetRelationships,
		EdgeDecl{Source: TypeDNSRecordSet, Target: TypeNetworkPublicIPAddress, Kind: store.RelUses},
		EdgeDecl{Source: TypeDNSPrivateRecordSet, Target: TypeNetworkPublicIPAddress, Kind: store.RelUses},
	)
}

// resolveDNSRecordSetRelationships derives DNS record-set -[uses]-> Public IP
// edges. Walks both public and private A (IPv4) and AAAA (IPv6) record sets and
// matches each record's address against a per-sub PIP index built from
// `azure:microsoft.network:public-ip-address.properties.ipAddress` — a single
// version-agnostic field that holds the IPv6 address for IPv6 PIPs, so AAAA
// resolves through the same index. Addresses are canonicalised via net.ParseIP
// before keying so an IPv6 address written compressed in one place and expanded
// in another still matches.
//
// CNAME / SRV / MX / PTR / NS / TXT / CAA records intentionally deferred. CNAME
// requires FQDN→ARM-ID reverse-lookup with per-service DNS suffix tables
// (azurewebsites.net → AppService site, *.cloudapp.azure.com → public IP,
// *.azurefd.net → Front Door, etc.), which is its own iteration.
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
			pipByIP[canonicalIP(*attrs.Properties.IPAddress)] = p.ID
		}
	}
	if len(pipByIP) == 0 {
		return nil
	}

	for _, r := range records {
		// Only A (IPv4) and AAAA (IPv6) record sets carry a public-IP literal;
		// skip the rest cheaply via the type segment of the NativeID before
		// unmarshaling — covers CNAME/SRV/MX/PTR/etc.
		rt := recordTypeFromID(r.NativeID)
		isA, isAAAA := strings.EqualFold(rt, "A"), strings.EqualFold(rt, "AAAA")
		if !isA && !isAAAA {
			continue
		}
		// armdns marshals the array keys PascalCase ("ARecords"/"AAAARecords")
		// while armprivatedns uses camelCase ("aRecords"/"aaaaRecords"); the
		// camelCase tags match both because encoding/json decodes keys
		// case-insensitively.
		var attrs struct {
			Properties *struct {
				ARecords []struct {
					IPv4Address *string `json:"ipv4Address"`
				} `json:"aRecords"`
				AAAARecords []struct {
					IPv6Address *string `json:"ipv6Address"`
				} `json:"aaaaRecords"`
			} `json:"properties"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil || attrs.Properties == nil {
			continue
		}
		var addrs []*string
		if isA {
			for _, a := range attrs.Properties.ARecords {
				addrs = append(addrs, a.IPv4Address)
			}
		} else {
			for _, a := range attrs.Properties.AAAARecords {
				addrs = append(addrs, a.IPv6Address)
			}
		}
		seen := map[string]bool{}
		for _, ap := range addrs {
			if ap == nil || *ap == "" {
				continue
			}
			ip := canonicalIP(*ap)
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

// canonicalIP normalises an IP literal to net.IP's canonical text form so an
// address written compressed in one place and expanded/upper-cased in another
// keys identically. Unparseable input passes through unchanged.
func canonicalIP(s string) string {
	if ip := net.ParseIP(s); ip != nil {
		return ip.String()
	}
	return s
}

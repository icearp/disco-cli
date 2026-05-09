package azure

import (
	"testing"

	"codeberg.org/icearp/disco/store"
)

// TestResolveDNSRecordSetRelationships covers A record set → public-IP edge
// derivation across both public + private DNS zones. Non-A record types skip
// silently. Records pointing at unknown IPs (cross-account / unscanned PIP)
// also skip silently.
func TestResolveDNSRecordSetRelationships(t *testing.T) {
	st := newTestStore(t)
	sub := newTestSubscription("sub-dns")

	pipAttrs := `{"properties":{"ipAddress":"203.0.113.10","publicIPAllocationMethod":"Static"}}`
	pipID := upsertTestResource(t, st, "azure", sub.ID, TypeNetworkPublicIPAddress,
		"/subscriptions/sub-dns/resourceGroups/RG/providers/Microsoft.Network/publicIPAddresses/pip1", "eastus", pipAttrs)

	aAttrs := `{"properties":{"TTL":3600,"aRecords":[{"ipv4Address":"203.0.113.10"},{"ipv4Address":"198.51.100.1"}]}}`
	aRecID := upsertTestResource(t, st, "azure", sub.ID, TypeDNSRecordSet,
		"/subscriptions/sub-dns/resourceGroups/RG/providers/Microsoft.Network/dnsZones/example.com/A/www", "global", aAttrs)

	privAAttrs := `{"properties":{"ttl":300,"aRecords":[{"ipv4Address":"203.0.113.10"}]}}`
	privID := upsertTestResource(t, st, "azure", sub.ID, TypeDNSPrivateRecordSet,
		"/subscriptions/sub-dns/resourceGroups/RG/providers/Microsoft.Network/privateDnsZones/internal.contoso/A/host1", "global", privAAttrs)

	// Non-A record (CNAME) — must skip.
	cnameAttrs := `{"properties":{"cnameRecord":{"cname":"target.example.com"}}}`
	cnameID := upsertTestResource(t, st, "azure", sub.ID, TypeDNSRecordSet,
		"/subscriptions/sub-dns/resourceGroups/RG/providers/Microsoft.Network/dnsZones/example.com/CNAME/alias", "global", cnameAttrs)

	if err := resolveDNSRecordSetRelationships(sub, st); err != nil {
		t.Fatalf("resolveDNSRecordSetRelationships: %v", err)
	}

	for _, c := range []struct {
		name string
		id   string
	}{{"public-A", aRecID}, {"private-A", privID}} {
		rels, _ := st.RelationshipsFrom(c.id)
		if len(rels) != 1 || rels[0].ToID != pipID || rels[0].Kind != store.RelUses {
			t.Errorf("%s: expected one uses → pip, got %+v", c.name, rels)
		}
	}
	if rels, _ := st.RelationshipsFrom(cnameID); len(rels) != 0 {
		t.Errorf("CNAME must not produce edges this iter, got %+v", rels)
	}
}

// TestRecordTypeFromID verifies the ARM-ID parser recovers the type segment.
func TestRecordTypeFromID(t *testing.T) {
	cases := []struct{ in, want string }{
		{"/subscriptions/x/resourceGroups/RG/providers/Microsoft.Network/dnsZones/foo.com/A/www", "A"},
		{"/subscriptions/x/resourceGroups/RG/providers/Microsoft.Network/privateDnsZones/c/CNAME/host", "CNAME"},
		{"", ""},
		{"/A", ""},
	}
	for _, tc := range cases {
		if got := recordTypeFromID(tc.in); got != tc.want {
			t.Errorf("recordTypeFromID(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

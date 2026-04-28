package gcp

import (
	"testing"

	"codeberg.org/icearp/disco/internal/store"
)

// TestResolveDNSRelationships verifies A-record → forwarding-rule via IP
// match, AAAA support, and that non-A/AAAA records (TXT) emit no edges.
func TestResolveDNSRelationships(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("my-project")

	frV4ID := upsertTestResource(t, st, "gcp", p.ID, TypeComputeForwardingRule,
		"https://www.googleapis.com/compute/v1/projects/my-project/global/forwardingRules/fr-v4", "",
		`{"IPAddress": "203.0.113.10"}`)
	frV6ID := upsertTestResource(t, st, "gcp", p.ID, TypeComputeForwardingRule,
		"https://www.googleapis.com/compute/v1/projects/my-project/global/forwardingRules/fr-v6", "",
		`{"IPAddress": "2001:db8::1"}`)

	zone := "projects/my-project/managedZones/example-com"
	rrA := upsertTestResource(t, st, "gcp", p.ID, TypeDNSRecordSet, zone+"/rrsets/A/www.example.com.", "",
		`{"type": "A", "rrdatas": ["203.0.113.10"]}`)
	rrAAAA := upsertTestResource(t, st, "gcp", p.ID, TypeDNSRecordSet, zone+"/rrsets/AAAA/www.example.com.", "",
		`{"type": "AAAA", "rrdatas": ["2001:db8::1"]}`)
	rrTXT := upsertTestResource(t, st, "gcp", p.ID, TypeDNSRecordSet, zone+"/rrsets/TXT/www.example.com.", "",
		`{"type": "TXT", "rrdatas": ["v=spf1 -all"]}`)

	if err := resolveDNSRelationships(p, st); err != nil {
		t.Fatalf("resolveDNSRelationships: %v", err)
	}
	if rels, _ := st.RelationshipsFrom(rrA); len(rels) != 1 || rels[0].ToID != frV4ID || rels[0].Kind != store.RelRoutesTo {
		t.Errorf("A: got %+v, want →fr-v4", rels)
	}
	if rels, _ := st.RelationshipsFrom(rrAAAA); len(rels) != 1 || rels[0].ToID != frV6ID {
		t.Errorf("AAAA: got %+v, want →fr-v6", rels)
	}
	if rels, _ := st.RelationshipsFrom(rrTXT); len(rels) != 0 {
		t.Errorf("TXT: expected no edges, got %+v", rels)
	}
}

package aws

import (
	"fmt"
	"testing"

	"codeberg.org/icearp/disco/internal/store"
)

// TestResolveRoute53Relationships verifies that a record set is linked to its
// hosted zone with an attached-to edge.
func TestResolveRoute53Relationships(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	zoneARN := "arn:aws:route53:::hostedzone/Z1234567890"
	recordNativeID := zoneARN + "/A/api.example.com"

	zoneID := upsertTestResource(t, st, "aws", acct.ID, TypeRoute53HostedZone, zoneARN, "", "{}")
	recordID := upsertTestResource(t, st, "aws", acct.ID, TypeRoute53RecordSet, recordNativeID, "", "{}")

	if err := resolveRoute53Relationships(acct, st); err != nil {
		t.Fatalf("resolveRoute53Relationships: %v", err)
	}

	rels, err := st.RelationshipsFrom(recordID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 1 {
		t.Fatalf("expected 1 relationship, got %d", len(rels))
	}
	if rels[0].ToID != zoneID || rels[0].Kind != store.RelAttachedTo {
		t.Errorf("expected record -[attached-to]-> zone; got %+v", rels[0])
	}
}

// TestResolveRoute53Relationships_NoRecords verifies that a zone with no record
// sets produces no relationships and no error.
func TestResolveRoute53Relationships_NoRecords(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	zoneARN := "arn:aws:route53:::hostedzone/Z9999999999"
	upsertTestResource(t, st, "aws", acct.ID, TypeRoute53HostedZone, zoneARN, "", "{}")

	if err := resolveRoute53Relationships(acct, st); err != nil {
		t.Fatalf("resolveRoute53Relationships: %v", err)
	}
}

// TestResolveRoute53DNSSECRelationships verifies that a DNSSEC resource is
// linked to its hosted zone with an attached-to edge.
func TestResolveRoute53DNSSECRelationships(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	zoneARN := "arn:aws:route53:::hostedzone/Z1111111111"
	dnssecNativeID := zoneARN + "/dnssec"

	zoneID := upsertTestResource(t, st, "aws", acct.ID, TypeRoute53HostedZone, zoneARN, "", "{}")
	dnssecID := upsertTestResource(t, st, "aws", acct.ID, TypeRoute53DNSSEC, dnssecNativeID, "", "{}")

	if err := resolveRoute53DNSSECRelationships(acct, st); err != nil {
		t.Fatalf("resolveRoute53DNSSECRelationships: %v", err)
	}

	rels, err := st.RelationshipsFrom(dnssecID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 1 {
		t.Fatalf("expected 1 relationship, got %d", len(rels))
	}
	assertRelationship(t, rels, dnssecID, zoneID, store.RelAttachedTo)
}

// TestResolveRoute53DNSSECRelationships_NoDNSSEC verifies no error when there
// are no DNSSEC resources.
func TestResolveRoute53DNSSECRelationships_NoDNSSEC(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	if err := resolveRoute53DNSSECRelationships(acct, st); err != nil {
		t.Fatalf("resolveRoute53DNSSECRelationships: %v", err)
	}
}

// TestDNSSECZoneARN verifies the ARN extraction helper for DNSSEC NativeIDs.
func TestDNSSECZoneARN(t *testing.T) {
	tests := []struct {
		nativeID string
		want     string
	}{
		{
			"arn:aws:route53:::hostedzone/Z1234567890/dnssec",
			"arn:aws:route53:::hostedzone/Z1234567890",
		},
		{"not-an-arn", ""},
		{"arn:aws:route53:::hostedzone/Z1234567890", ""},          // no /dnssec suffix
		{"arn:aws:route53:::hostedzone/Z1234/ksk/key/dnssec", ""}, // extra slash in zone portion
	}
	for _, tt := range tests {
		got := dnssecZoneARN(tt.nativeID)
		if got != tt.want {
			t.Errorf("dnssecZoneARN(%q) = %q, want %q", tt.nativeID, got, tt.want)
		}
	}
}

// TestResolveRoute53KSKRelationships verifies that a KSK is linked to its
// DNSSEC resource with an attached-to edge.
func TestResolveRoute53KSKRelationships(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	zoneARN := "arn:aws:route53:::hostedzone/Z2222222222"
	dnssecNativeID := zoneARN + "/dnssec"
	kskNativeID := zoneARN + "/ksk/mykey"

	dnssecID := upsertTestResource(t, st, "aws", acct.ID, TypeRoute53DNSSEC, dnssecNativeID, "", "{}")
	kskID := upsertTestResource(t, st, "aws", acct.ID, TypeRoute53KeySigningKey, kskNativeID, "", "{}")

	if err := resolveRoute53KSKRelationships(acct, st); err != nil {
		t.Fatalf("resolveRoute53KSKRelationships: %v", err)
	}

	rels, err := st.RelationshipsFrom(kskID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 1 {
		t.Fatalf("expected 1 relationship, got %d", len(rels))
	}
	assertRelationship(t, rels, kskID, dnssecID, store.RelAttachedTo)
}

// TestResolveRoute53KSKRelationships_NoKSKs verifies no error when there are
// no key-signing key resources.
func TestResolveRoute53KSKRelationships_NoKSKs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	if err := resolveRoute53KSKRelationships(acct, st); err != nil {
		t.Fatalf("resolveRoute53KSKRelationships: %v", err)
	}
}

// TestKSKDNSSECNativeID verifies the DNSSEC NativeID derivation helper.
func TestKSKDNSSECNativeID(t *testing.T) {
	tests := []struct {
		nativeID string
		want     string
	}{
		{
			"arn:aws:route53:::hostedzone/Z1234567890/ksk/mykey",
			"arn:aws:route53:::hostedzone/Z1234567890/dnssec",
		},
		{"not-an-arn", ""},
		{"arn:aws:route53:::hostedzone/Z1234567890", ""},         // no /ksk/ segment
		{"arn:aws:route53:::hostedzone/Z1234567890/other/x", ""}, // wrong segment prefix
	}
	for _, tt := range tests {
		got := kskDNSSECNativeID(tt.nativeID)
		if got != tt.want {
			t.Errorf("kskDNSSECNativeID(%q) = %q, want %q", tt.nativeID, got, tt.want)
		}
	}
}

// TestResolveRoute53HealthCheckRelationships verifies that a record set with a
// HealthCheckId is linked to the corresponding health check via a "uses" edge.
func TestResolveRoute53HealthCheckRelationships(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	hcID := "abc12345-def6-7890-ghij-klmnopqrstuv"
	hcNativeID := fmt.Sprintf("arn:aws:route53:::healthcheck/%s", hcID)

	hcResID := upsertTestResource(t, st, "aws", acct.ID, TypeRoute53HealthCheck, hcNativeID, "", "{}")

	zoneARN := "arn:aws:route53:::hostedzone/Z3333333333"
	recordNativeID := zoneARN + "/A/api.example.com"
	recordAttrs := fmt.Sprintf(`{"HealthCheckId": %q}`, hcID)
	recordResID := upsertTestResource(t, st, "aws", acct.ID, TypeRoute53RecordSet, recordNativeID, "", recordAttrs)

	if err := resolveRoute53HealthCheckRelationships(acct, st); err != nil {
		t.Fatalf("resolveRoute53HealthCheckRelationships: %v", err)
	}

	rels, err := st.RelationshipsFrom(recordResID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 1 {
		t.Fatalf("expected 1 relationship, got %d", len(rels))
	}
	assertRelationship(t, rels, recordResID, hcResID, store.RelUses)
}

// TestResolveRoute53HealthCheckRelationships_NoHealthCheckId verifies that a
// record set without a HealthCheckId produces no relationships and no error.
func TestResolveRoute53HealthCheckRelationships_NoHealthCheckId(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	zoneARN := "arn:aws:route53:::hostedzone/Z4444444444"
	recordNativeID := zoneARN + "/MX/mail.example.com"
	recordResID := upsertTestResource(t, st, "aws", acct.ID, TypeRoute53RecordSet, recordNativeID, "", "{}")

	if err := resolveRoute53HealthCheckRelationships(acct, st); err != nil {
		t.Fatalf("resolveRoute53HealthCheckRelationships: %v", err)
	}

	rels, err := st.RelationshipsFrom(recordResID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 0 {
		t.Errorf("expected 0 relationships, got %d", len(rels))
	}
}

// TestResolveRoute53HealthCheckRelationships_NoRecords verifies no error when
// there are no record sets at all.
func TestResolveRoute53HealthCheckRelationships_NoRecords(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	if err := resolveRoute53HealthCheckRelationships(acct, st); err != nil {
		t.Fatalf("resolveRoute53HealthCheckRelationships: %v", err)
	}
}

// TestRecordSetZoneARN verifies the ARN extraction helper.
func TestRecordSetZoneARN(t *testing.T) {
	tests := []struct {
		nativeID string
		want     string
	}{
		{
			"arn:aws:route53:::hostedzone/Z1234567890/A/api.example.com",
			"arn:aws:route53:::hostedzone/Z1234567890",
		},
		{
			"arn:aws:route53:::hostedzone/ZABC/MX/mail.example.com",
			"arn:aws:route53:::hostedzone/ZABC",
		},
		{"not-an-arn", ""},
		{"arn:aws:route53:::hostedzone/ZONEID", ""}, // no type/name suffix
	}
	for _, tt := range tests {
		got := recordSetZoneARN(tt.nativeID)
		if got != tt.want {
			t.Errorf("recordSetZoneARN(%q) = %q, want %q", tt.nativeID, got, tt.want)
		}
	}
}

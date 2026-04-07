package aws

import (
	"testing"

	"codeburg.org/icearp/disco/internal/store"
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

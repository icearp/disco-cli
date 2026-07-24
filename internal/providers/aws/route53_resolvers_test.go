package aws

import (
	"fmt"
	"testing"

	"github.com/icearp/disco-cli/store"
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

// TestResolveRoute53AliasToELBv2 verifies alias A-record → ELBv2 LB edge.
// Scanner wraps LB attrs as {"lb": <LoadBalancer>, "type": "..."}. Record
// AliasTarget.DNSName carries `dualstack.` prefix + trailing dot — both
// normalized away before lookup.
func TestResolveRoute53AliasToELBv2(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	lbDNS := "my-alb-123.us-east-1.elb.amazonaws.com"
	lbARN := fmt.Sprintf("arn:aws:elasticloadbalancing:us-east-1:%s:loadbalancer/app/my-alb/abc", acct.ID)
	lbAttrs := fmt.Sprintf(`{"lb":{"DNSName":%q},"type":"application"}`, lbDNS)
	lbID := upsertTestResource(t, st, "aws", acct.ID, TypeELBv2LoadBalancer, lbARN, "us-east-1", lbAttrs)

	zoneARN := "arn:aws:route53:::hostedzone/Z1"
	recordNativeID := zoneARN + "/A/www.example.com"
	recordAttrs := fmt.Sprintf(`{"Name":"www.example.com.","Type":"A","AliasTarget":{"DNSName":"dualstack.%s.","HostedZoneId":"Z35SXDOTRQ7X7K"}}`, lbDNS)
	recordID := upsertTestResource(t, st, "aws", acct.ID, TypeRoute53RecordSet, recordNativeID, "", recordAttrs)

	if err := resolveRoute53AliasRelationships(acct, st); err != nil {
		t.Fatalf("resolveRoute53AliasRelationships: %v", err)
	}
	rels, err := st.RelationshipsFrom(recordID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	assertRelationship(t, rels, recordID, lbID, store.RelUses)
}

// TestResolveRoute53AliasToCloudFront verifies alias → CloudFront distribution edge.
func TestResolveRoute53AliasToCloudFront(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	cfDNS := "d111111abcdef8.cloudfront.net"
	cfARN := fmt.Sprintf("arn:aws:cloudfront::%s:distribution/E1ABCDEFG", acct.ID)
	cfAttrs := fmt.Sprintf(`{"DomainName":%q}`, cfDNS)
	cfID := upsertTestResource(t, st, "aws", acct.ID, TypeCloudFrontDistribution, cfARN, "", cfAttrs)

	zoneARN := "arn:aws:route53:::hostedzone/Z1"
	recordNativeID := zoneARN + "/A/cdn.example.com"
	recordAttrs := fmt.Sprintf(`{"AliasTarget":{"DNSName":"%s."}}`, cfDNS)
	recordID := upsertTestResource(t, st, "aws", acct.ID, TypeRoute53RecordSet, recordNativeID, "", recordAttrs)

	if err := resolveRoute53AliasRelationships(acct, st); err != nil {
		t.Fatalf("resolveRoute53AliasRelationships: %v", err)
	}
	rels, err := st.RelationshipsFrom(recordID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	assertRelationship(t, rels, recordID, cfID, store.RelUses)
}

// TestResolveRoute53AliasToAPIGWv1 verifies alias → APIGW v1 custom domain
// via DistributionDomainName.
func TestResolveRoute53AliasToAPIGWv1(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	apigwDNS := "d-abcdefgh.execute-api.us-east-1.amazonaws.com"
	domainARN := "arn:aws:apigateway:us-east-1::/domainnames/api.example.com"
	domainAttrs := fmt.Sprintf(`{"DomainName":"api.example.com","DistributionDomainName":%q}`, apigwDNS)
	domainID := upsertTestResource(t, st, "aws", acct.ID, TypeAPIGatewayDomainName, domainARN, "us-east-1", domainAttrs)

	zoneARN := "arn:aws:route53:::hostedzone/Z1"
	recordNativeID := zoneARN + "/A/api.example.com"
	recordAttrs := fmt.Sprintf(`{"AliasTarget":{"DNSName":"%s."}}`, apigwDNS)
	recordID := upsertTestResource(t, st, "aws", acct.ID, TypeRoute53RecordSet, recordNativeID, "", recordAttrs)

	if err := resolveRoute53AliasRelationships(acct, st); err != nil {
		t.Fatalf("resolveRoute53AliasRelationships: %v", err)
	}
	rels, err := st.RelationshipsFrom(recordID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	assertRelationship(t, rels, recordID, domainID, store.RelUses)
}

// TestResolveRoute53AliasToAPIGWv2 verifies alias → APIGW v2 custom domain
// via DomainNameConfigurations[].ApiGatewayDomainName.
func TestResolveRoute53AliasToAPIGWv2(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	apigwDNS := "d-xyz123.execute-api.us-east-1.amazonaws.com"
	domainARN := "arn:aws:apigateway:us-east-1::/domainnames/v2.example.com"
	domainAttrs := fmt.Sprintf(`{"DomainName":"v2.example.com","DomainNameConfigurations":[{"ApiGatewayDomainName":%q}]}`, apigwDNS)
	domainID := upsertTestResource(t, st, "aws", acct.ID, TypeAPIGatewayDomainNameV2, domainARN, "us-east-1", domainAttrs)

	zoneARN := "arn:aws:route53:::hostedzone/Z1"
	recordNativeID := zoneARN + "/A/v2.example.com"
	recordAttrs := fmt.Sprintf(`{"AliasTarget":{"DNSName":"%s."}}`, apigwDNS)
	recordID := upsertTestResource(t, st, "aws", acct.ID, TypeRoute53RecordSet, recordNativeID, "", recordAttrs)

	if err := resolveRoute53AliasRelationships(acct, st); err != nil {
		t.Fatalf("resolveRoute53AliasRelationships: %v", err)
	}
	rels, err := st.RelationshipsFrom(recordID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	assertRelationship(t, rels, recordID, domainID, store.RelUses)
}

// TestResolveRoute53Alias_UnmatchedDNS verifies that an alias record whose
// DNS doesn't match any scanned backend emits no edges.
func TestResolveRoute53Alias_UnmatchedDNS(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	zoneARN := "arn:aws:route53:::hostedzone/Z1"
	recordNativeID := zoneARN + "/A/orphan.example.com"
	recordAttrs := `{"AliasTarget":{"DNSName":"unknown.elb.amazonaws.com."}}`
	recordID := upsertTestResource(t, st, "aws", acct.ID, TypeRoute53RecordSet, recordNativeID, "", recordAttrs)

	if err := resolveRoute53AliasRelationships(acct, st); err != nil {
		t.Fatalf("resolveRoute53AliasRelationships: %v", err)
	}
	rels, err := st.RelationshipsFrom(recordID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 0 {
		t.Errorf("expected 0 relationships, got %d", len(rels))
	}
}

// TestResolveRoute53AliasToS3Website verifies alias A-record → S3 bucket
// edge for a static-website-hosted bucket. Resolver pivots on
// `s3-website-` prefix in the alias DNS and looks the bucket up by record
// FQDN (S3 enforces bucket name == FQDN for website hosting).
func TestResolveRoute53AliasToS3Website(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	bucketName := "www.example.com"
	bucketARN := "arn:aws:s3:::" + bucketName
	bucketID := upsertTestResource(t, st, "aws", acct.ID, TypeS3Bucket, bucketARN, "", "{}")

	zoneARN := "arn:aws:route53:::hostedzone/Z1"
	recordNativeID := zoneARN + "/A/www.example.com"
	recordAttrs := `{"Name":"www.example.com.","Type":"A","AliasTarget":{"DNSName":"s3-website-us-east-1.amazonaws.com.","HostedZoneId":"Z3AQBSTGFYJSTF"}}`
	recordID := upsertTestResource(t, st, "aws", acct.ID, TypeRoute53RecordSet, recordNativeID, "", recordAttrs)

	if err := resolveRoute53AliasRelationships(acct, st); err != nil {
		t.Fatalf("resolveRoute53AliasRelationships: %v", err)
	}
	rels, err := st.RelationshipsFrom(recordID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	assertRelationship(t, rels, recordID, bucketID, store.RelUses)
}

// TestResolveRoute53AliasToS3Website_ModernEndpoint covers the newer
// `s3-website.<region>` (dot, not dash) endpoint shape used in regions
// added after ~2018.
func TestResolveRoute53AliasToS3Website_ModernEndpoint(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	bucketName := "blog.example.com"
	bucketARN := "arn:aws:s3:::" + bucketName
	bucketID := upsertTestResource(t, st, "aws", acct.ID, TypeS3Bucket, bucketARN, "", "{}")

	zoneARN := "arn:aws:route53:::hostedzone/Z1"
	recordNativeID := zoneARN + "/A/blog.example.com"
	recordAttrs := `{"Name":"blog.example.com.","AliasTarget":{"DNSName":"s3-website.eu-south-1.amazonaws.com."}}`
	recordID := upsertTestResource(t, st, "aws", acct.ID, TypeRoute53RecordSet, recordNativeID, "", recordAttrs)

	if err := resolveRoute53AliasRelationships(acct, st); err != nil {
		t.Fatalf("resolveRoute53AliasRelationships: %v", err)
	}
	rels, err := st.RelationshipsFrom(recordID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	assertRelationship(t, rels, recordID, bucketID, store.RelUses)
}

// TestResolveRoute53AliasToS3Website_BucketNotScanned verifies that a record
// pointing at an unscanned bucket emits no edge (FK-safe).
func TestResolveRoute53AliasToS3Website_BucketNotScanned(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	zoneARN := "arn:aws:route53:::hostedzone/Z1"
	recordNativeID := zoneARN + "/A/ghost.example.com"
	recordAttrs := `{"Name":"ghost.example.com.","AliasTarget":{"DNSName":"s3-website-us-east-1.amazonaws.com."}}`
	recordID := upsertTestResource(t, st, "aws", acct.ID, TypeRoute53RecordSet, recordNativeID, "", recordAttrs)

	if err := resolveRoute53AliasRelationships(acct, st); err != nil {
		t.Fatalf("resolveRoute53AliasRelationships: %v", err)
	}
	rels, err := st.RelationshipsFrom(recordID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 0 {
		t.Errorf("expected 0 relationships, got %d", len(rels))
	}
}

// TestResolveRoute53Alias_NoAliasTarget verifies that a non-alias record
// (no AliasTarget field) produces no edges and no panic on missing fields.
func TestResolveRoute53Alias_NoAliasTarget(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	// Seed at least one backend so index is non-empty (exercises the
	// record-iteration branch, not the early-return short-circuit).
	lbARN := fmt.Sprintf("arn:aws:elasticloadbalancing:us-east-1:%s:loadbalancer/app/x/y", acct.ID)
	upsertTestResource(t, st, "aws", acct.ID, TypeELBv2LoadBalancer, lbARN, "us-east-1",
		`{"lb":{"DNSName":"x.elb.amazonaws.com"},"type":"application"}`)

	zoneARN := "arn:aws:route53:::hostedzone/Z1"
	recordNativeID := zoneARN + "/A/plain.example.com"
	recordID := upsertTestResource(t, st, "aws", acct.ID, TypeRoute53RecordSet, recordNativeID, "", `{"Type":"A"}`)

	if err := resolveRoute53AliasRelationships(acct, st); err != nil {
		t.Fatalf("resolveRoute53AliasRelationships: %v", err)
	}
	rels, err := st.RelationshipsFrom(recordID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 0 {
		t.Errorf("expected 0 relationships, got %d", len(rels))
	}
}

func TestResolveRoute53QueryLoggingConfig(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	zoneARN := "arn:aws:route53:::hostedzone/Z123"
	zoneID := upsertTestResource(t, st, "aws", acct.ID, TypeRoute53HostedZone, zoneARN, "", "{}")
	lgARN := logGroupNativeIDFromName(acct.ID, testRegion, "/aws/route53/example.com")
	lgID := upsertTestResource(t, st, "aws", acct.ID, TypeLogsLogGroup, lgARN, testRegion, "{}")
	qlcARN := "arn:aws:route53:::queryloggingconfig/qlc-1"
	qlcID := upsertTestResource(t, st, "aws", acct.ID, TypeRoute53QueryLoggingConfig, qlcARN, "",
		fmt.Sprintf(`{"HostedZoneId":"Z123","CloudWatchLogsLogGroupArn":"%s:*"}`, lgARN))
	if err := resolveRoute53QueryLoggingConfig(acct, st); err != nil {
		t.Fatalf("resolveRoute53QueryLoggingConfig: %v", err)
	}
	rels, _ := st.RelationshipsFrom(qlcID)
	assertRelationship(t, rels, qlcID, zoneID, store.RelAttachedTo)
	assertRelationship(t, rels, qlcID, lgID, store.RelRoutesTo)
}

func TestResolveRoute53QueryLoggingConfig_NoAttrs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	qlcARN := "arn:aws:route53:::queryloggingconfig/qlc-1"
	qlcID := upsertTestResource(t, st, "aws", acct.ID, TypeRoute53QueryLoggingConfig, qlcARN, "", "{}")
	if err := resolveRoute53QueryLoggingConfig(acct, st); err != nil {
		t.Fatalf("resolveRoute53QueryLoggingConfig (no attrs): %v", err)
	}
	rels, _ := st.RelationshipsFrom(qlcID)
	if len(rels) != 0 {
		t.Fatalf("expected no relationships, got %d", len(rels))
	}
}

func TestResolveRoute53TrafficPolicyInstance(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	zoneARN := "arn:aws:route53:::hostedzone/Z123"
	zoneID := upsertTestResource(t, st, "aws", acct.ID, TypeRoute53HostedZone, zoneARN, "", "{}")
	tpARN := "arn:aws:route53:::trafficpolicy/tp-1"
	tpID := upsertTestResource(t, st, "aws", acct.ID, TypeRoute53TrafficPolicy, tpARN, "", "{}")
	tiARN := "arn:aws:route53:::trafficpolicyinstance/ti-1"
	tiID := upsertTestResource(t, st, "aws", acct.ID, TypeRoute53TrafficPolicyInstance, tiARN, "",
		`{"HostedZoneId":"Z123","TrafficPolicyId":"tp-1"}`)
	if err := resolveRoute53TrafficPolicyInstance(acct, st); err != nil {
		t.Fatalf("resolveRoute53TrafficPolicyInstance: %v", err)
	}
	rels, _ := st.RelationshipsFrom(tiID)
	assertRelationship(t, rels, tiID, zoneID, store.RelAttachedTo)
	assertRelationship(t, rels, tiID, tpID, store.RelUses)
}

func TestResolveRoute53TrafficPolicyInstance_NoAttrs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	tiARN := "arn:aws:route53:::trafficpolicyinstance/ti-1"
	tiID := upsertTestResource(t, st, "aws", acct.ID, TypeRoute53TrafficPolicyInstance, tiARN, "", "{}")
	if err := resolveRoute53TrafficPolicyInstance(acct, st); err != nil {
		t.Fatalf("resolveRoute53TrafficPolicyInstance (no attrs): %v", err)
	}
	rels, _ := st.RelationshipsFrom(tiID)
	if len(rels) != 0 {
		t.Fatalf("expected no relationships, got %d", len(rels))
	}
}

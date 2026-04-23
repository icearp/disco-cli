package aws

import (
	"fmt"
	"testing"

	"codeberg.org/icearp/disco/internal/store"
)

// --- Distribution Policies ---

// TestResolveDistributionPolicies verifies that a distribution is linked to its
// cache policy, origin request policy, response headers policy, realtime log
// config, key group, and CloudFront function via "uses" relationships.
func TestResolveDistributionPolicies(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	cachePolicyID := "cp-abc123"
	orpID := "orp-def456"
	rhpID := "rhp-ghi789"
	rtlcARN := fmt.Sprintf("arn:aws:cloudfront::%s:realtime-log-config/my-rtlc", testAccountID)
	kgID := "kg-jkl012"
	funcARN := fmt.Sprintf("arn:aws:cloudfront::%s:function/my-func", testAccountID)

	// Seed the referenced resources so FK constraints are satisfied.
	cpID := upsertTestResource(t, st, "aws", acct.ID, TypeCloudFrontCachePolicy, cachePolicyID, "", "{}")
	orpResID := upsertTestResource(t, st, "aws", acct.ID, TypeCloudFrontOriginRequestPolicy, orpID, "", "{}")
	rhpResID := upsertTestResource(t, st, "aws", acct.ID, TypeCloudFrontResponseHeadersPolicy, rhpID, "", "{}")
	rtlcResID := upsertTestResource(t, st, "aws", acct.ID, TypeCloudFrontRealtimeLogConfig, rtlcARN, "", "{}")
	kgResID := upsertTestResource(t, st, "aws", acct.ID, TypeCloudFrontKeyGroup, kgID, "", "{}")
	funcResID := upsertTestResource(t, st, "aws", acct.ID, TypeCloudFrontFunction, funcARN, "", "{}")

	// Distribution referencing all of the above in its DefaultCacheBehavior.
	distARN := fmt.Sprintf("arn:aws:cloudfront::%s:distribution/E1ABCDEF", testAccountID)
	distAttrs := fmt.Sprintf(`{
		"ARN": %q,
		"DefaultCacheBehavior": {
			"CachePolicyId": %q,
			"OriginRequestPolicyId": %q,
			"ResponseHeadersPolicyId": %q,
			"RealtimeLogConfigArn": %q,
			"TrustedKeyGroups": {"Items": [%q]},
			"FunctionAssociations": {"Items": [{"FunctionARN": %q}]}
		}
	}`, distARN, cachePolicyID, orpID, rhpID, rtlcARN, kgID, funcARN)
	distResID := upsertTestResource(t, st, "aws", acct.ID, TypeCloudFrontDistribution, distARN, "", distAttrs)

	if err := resolveDistributionPolicies(acct, st); err != nil {
		t.Fatalf("resolveDistributionPolicies: %v", err)
	}

	rels, err := st.RelationshipsFrom(distResID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 6 {
		t.Fatalf("expected 6 relationships, got %d", len(rels))
	}
	assertRelationship(t, rels, distResID, cpID, store.RelUses)
	assertRelationship(t, rels, distResID, orpResID, store.RelUses)
	assertRelationship(t, rels, distResID, rhpResID, store.RelUses)
	assertRelationship(t, rels, distResID, rtlcResID, store.RelUses)
	assertRelationship(t, rels, distResID, kgResID, store.RelUses)
	assertRelationship(t, rels, distResID, funcResID, store.RelUses)
}

// TestResolveDistributionPolicies_Empty verifies no error when there are no distributions.
func TestResolveDistributionPolicies_Empty(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	if err := resolveDistributionPolicies(acct, st); err != nil {
		t.Fatalf("resolveDistributionPolicies: %v", err)
	}
}

// TestResolveDistributionPolicies_NilBehavior verifies no nil-pointer panic when
// DefaultCacheBehavior is absent from the distribution's attributes.
func TestResolveDistributionPolicies_NilBehavior(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	distARN := fmt.Sprintf("arn:aws:cloudfront::%s:distribution/E2BARE", testAccountID)
	distResID := upsertTestResource(t, st, "aws", acct.ID, TypeCloudFrontDistribution, distARN, "", "{}")

	if err := resolveDistributionPolicies(acct, st); err != nil {
		t.Fatalf("resolveDistributionPolicies: %v", err)
	}
	rels, err := st.RelationshipsFrom(distResID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 0 {
		t.Errorf("expected 0 relationships, got %d", len(rels))
	}
}

// TestResolveDistributionPolicies_CacheBehaviors verifies that path-specific
// CacheBehaviors are also resolved, deduplicating shared policy references.
func TestResolveDistributionPolicies_CacheBehaviors(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	cachePolicyID := "cp-shared"
	cpID := upsertTestResource(t, st, "aws", acct.ID, TypeCloudFrontCachePolicy, cachePolicyID, "", "{}")

	distARN := fmt.Sprintf("arn:aws:cloudfront::%s:distribution/E3MULTI", testAccountID)
	// Both DefaultCacheBehavior and a CacheBehavior reference the same cache policy —
	// only one relationship should be emitted.
	distAttrs := fmt.Sprintf(`{
		"DefaultCacheBehavior": {"CachePolicyId": %q},
		"CacheBehaviors": {"Items": [{"CachePolicyId": %q}]}
	}`, cachePolicyID, cachePolicyID)
	distResID := upsertTestResource(t, st, "aws", acct.ID, TypeCloudFrontDistribution, distARN, "", distAttrs)

	if err := resolveDistributionPolicies(acct, st); err != nil {
		t.Fatalf("resolveDistributionPolicies: %v", err)
	}
	rels, err := st.RelationshipsFrom(distResID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 1 {
		t.Fatalf("expected 1 relationship (deduplicated), got %d", len(rels))
	}
	assertRelationship(t, rels, distResID, cpID, store.RelUses)
}

// --- Distribution Origins ---

// TestResolveDistributionOrigins verifies that distribution origins emit "uses"
// relationships to origin access controls and origin access identities.
func TestResolveDistributionOrigins(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	oacID := "oac-abc123"
	oaiID := "E1OAIID1234567"

	oacResID := upsertTestResource(t, st, "aws", acct.ID, TypeCloudFrontOriginAccessControl, oacID, "", "{}")
	oaiResID := upsertTestResource(t, st, "aws", acct.ID, TypeCloudFrontOAI, oaiID, "", "{}")

	distARN := fmt.Sprintf("arn:aws:cloudfront::%s:distribution/E4ORIGIN", testAccountID)
	distAttrs := fmt.Sprintf(`{
		"Origins": {
			"Items": [
				{"OriginAccessControlId": %q},
				{"S3OriginConfig": {"OriginAccessIdentity": "origin-access-identity/cloudfront/%s"}}
			]
		}
	}`, oacID, oaiID)
	distResID := upsertTestResource(t, st, "aws", acct.ID, TypeCloudFrontDistribution, distARN, "", distAttrs)

	if err := resolveDistributionOrigins(acct, st); err != nil {
		t.Fatalf("resolveDistributionOrigins: %v", err)
	}
	rels, err := st.RelationshipsFrom(distResID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 2 {
		t.Fatalf("expected 2 relationships, got %d", len(rels))
	}
	assertRelationship(t, rels, distResID, oacResID, store.RelUses)
	assertRelationship(t, rels, distResID, oaiResID, store.RelUses)
}

// TestResolveDistributionOrigins_Empty verifies no error when there are no distributions.
func TestResolveDistributionOrigins_Empty(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	if err := resolveDistributionOrigins(acct, st); err != nil {
		t.Fatalf("resolveDistributionOrigins: %v", err)
	}
}

// TestResolveDistributionOrigins_NoOrigins verifies no nil-pointer panic when
// the distribution has no Origins in its attributes.
func TestResolveDistributionOrigins_NoOrigins(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	distARN := fmt.Sprintf("arn:aws:cloudfront::%s:distribution/E5BARE", testAccountID)
	distResID := upsertTestResource(t, st, "aws", acct.ID, TypeCloudFrontDistribution, distARN, "", "{}")

	if err := resolveDistributionOrigins(acct, st); err != nil {
		t.Fatalf("resolveDistributionOrigins: %v", err)
	}
	rels, err := st.RelationshipsFrom(distResID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 0 {
		t.Errorf("expected 0 relationships, got %d", len(rels))
	}
}

// --- Distribution Tenants ---

// TestResolveDistributionTenants verifies that each distribution tenant is
// linked to its parent distribution via a "uses" relationship.
func TestResolveDistributionTenants(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	distID := "E6PARENT1234567"
	distARN := fmt.Sprintf("arn:aws:cloudfront::%s:distribution/%s", testAccountID, distID)
	distResID := upsertTestResource(t, st, "aws", acct.ID, TypeCloudFrontDistribution, distARN, "", "{}")

	tenantARN := fmt.Sprintf("arn:aws:cloudfront::%s:distribution-tenant/tenant-abc", testAccountID)
	tenantAttrs := fmt.Sprintf(`{"DistributionId": %q}`, distID)
	tenantResID := upsertTestResource(t, st, "aws", acct.ID, TypeCloudFrontDistributionTenant, tenantARN, "", tenantAttrs)

	if err := resolveDistributionTenants(acct, st); err != nil {
		t.Fatalf("resolveDistributionTenants: %v", err)
	}
	rels, err := st.RelationshipsFrom(tenantResID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 1 {
		t.Fatalf("expected 1 relationship, got %d", len(rels))
	}
	assertRelationship(t, rels, tenantResID, distResID, store.RelUses)
}

// TestResolveDistributionTenants_Empty verifies no error when there are no tenants.
func TestResolveDistributionTenants_Empty(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	if err := resolveDistributionTenants(acct, st); err != nil {
		t.Fatalf("resolveDistributionTenants: %v", err)
	}
}

// TestResolveDistributionTenants_NoDistributionId verifies no nil-pointer panic
// when the tenant has no DistributionId in its attributes.
func TestResolveDistributionTenants_NoDistributionId(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	tenantARN := fmt.Sprintf("arn:aws:cloudfront::%s:distribution-tenant/orphan-tenant", testAccountID)
	tenantResID := upsertTestResource(t, st, "aws", acct.ID, TypeCloudFrontDistributionTenant, tenantARN, "", "{}")

	if err := resolveDistributionTenants(acct, st); err != nil {
		t.Fatalf("resolveDistributionTenants: %v", err)
	}
	rels, err := st.RelationshipsFrom(tenantResID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 0 {
		t.Errorf("expected 0 relationships, got %d", len(rels))
	}
}

// TestResolveDistributionOriginsS3 verifies Distribution → S3 bucket edge from
// an S3 origin DomainName.
func TestResolveDistributionOriginsS3(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	distARN := "arn:aws:cloudfront::" + testAccountID + ":distribution/E_S3"
	bucketName := "my-assets-bucket"
	bucketARN := "arn:aws:s3:::" + bucketName

	attrs := `{"Origins":{"Items":[{"DomainName":"` + bucketName + `.s3.amazonaws.com"}]}}`
	distID := upsertTestResource(t, st, "aws", acct.ID, TypeCloudFrontDistribution, distARN, "", attrs)
	bucketID := upsertTestResource(t, st, "aws", acct.ID, TypeS3Bucket, bucketARN, "", "{}")

	if err := resolveDistributionOrigins(acct, st); err != nil {
		t.Fatalf("resolveDistributionOrigins: %v", err)
	}
	rels, err := st.RelationshipsFrom(distID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	assertRelationship(t, rels, distID, bucketID, store.RelUses)
}

// TestResolveDistributionBehaviorsLambdaEdge verifies Distribution → Lambda@Edge
// edges from LambdaFunctionAssociations in cache behaviors.
func TestResolveDistributionBehaviorsLambdaEdge(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	distARN := "arn:aws:cloudfront::" + testAccountID + ":distribution/E_LAMBDA"
	// Qualified Lambda ARN — resolver must strip :1 qualifier.
	qualifiedARN := "arn:aws:lambda:us-east-1:" + testAccountID + ":function:my-fn:1"
	fnARN := "arn:aws:lambda:us-east-1:" + testAccountID + ":function:my-fn"

	attrs := `{"DefaultCacheBehavior":{"LambdaFunctionAssociations":{"Items":[{"LambdaFunctionARN":"` + qualifiedARN + `"}]}}}`
	distID := upsertTestResource(t, st, "aws", acct.ID, TypeCloudFrontDistribution, distARN, "", attrs)
	fnID := upsertTestResource(t, st, "aws", acct.ID, TypeLambdaFunction, fnARN, testRegion, "{}")

	if err := resolveDistributionPolicies(acct, st); err != nil {
		t.Fatalf("resolveDistributionPolicies: %v", err)
	}
	rels, err := st.RelationshipsFrom(distID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	assertRelationship(t, rels, distID, fnID, store.RelUses)
}

// TestCloudfrontS3BucketFromDomain verifies domain parsing for various S3 domain formats.
func TestCloudfrontS3BucketFromDomain(t *testing.T) {
	tests := []struct {
		domain string
		want   string
	}{
		{"my-bucket.s3.amazonaws.com", "my-bucket"},
		{"my-bucket.s3.us-east-1.amazonaws.com", "my-bucket"},
		{"my-bucket.s3-website-us-east-1.amazonaws.com", "my-bucket"},
		{"my-bucket.s3-website.us-east-1.amazonaws.com", "my-bucket"},
		{"api.example.com", ""},
		{"", ""},
	}
	for _, tc := range tests {
		got := cloudfrontS3BucketFromDomain(tc.domain)
		if got != tc.want {
			t.Errorf("cloudfrontS3BucketFromDomain(%q) = %q; want %q", tc.domain, got, tc.want)
		}
	}
}

// TestResolveDistributionCertificates verifies Distribution → ACM cert edge.
func TestResolveDistributionCertificates(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	distARN := "arn:aws:cloudfront::" + testAccountID + ":distribution/E1"
	certARN := "arn:aws:acm:us-east-1:" + testAccountID + ":certificate/abc"
	attrs := `{"ViewerCertificate":{"ACMCertificateArn":"` + certARN + `"}}`

	distID := upsertTestResource(t, st, "aws", acct.ID, TypeCloudFrontDistribution, distARN, "", attrs)
	certID := upsertTestResource(t, st, "aws", acct.ID, TypeACMCertificate, certARN, "us-east-1", "{}")

	if err := resolveDistributionCertificates(acct, st); err != nil {
		t.Fatalf("resolveDistributionCertificates: %v", err)
	}
	rels, err := st.RelationshipsFrom(distID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	assertRelationship(t, rels, distID, certID, store.RelUses)
}

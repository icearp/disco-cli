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

// --- Key Group → Public Keys ---

// TestResolveCloudFrontKeyGroupPublicKeys verifies key-group → public-key edges
// are emitted for each public key listed in KeyGroupConfig.Items.
func TestResolveCloudFrontKeyGroupPublicKeys(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	pk1 := upsertTestResource(t, st, "aws", acct.ID, TypeCloudFrontPublicKey, "K1ABC", "", "{}")
	pk2 := upsertTestResource(t, st, "aws", acct.ID, TypeCloudFrontPublicKey, "K2DEF", "", "{}")
	kgAttrs := `{"KeyGroup":{"Id":"kg-1","KeyGroupConfig":{"Items":["K1ABC","K2DEF","K9MISSING"]}}}`
	kgID := upsertTestResource(t, st, "aws", acct.ID, TypeCloudFrontKeyGroup, "kg-1", "", kgAttrs)

	if err := resolveCloudFrontKeyGroupPublicKeys(acct, st); err != nil {
		t.Fatalf("resolveCloudFrontKeyGroupPublicKeys: %v", err)
	}
	rels, _ := st.RelationshipsFrom(kgID)
	if len(rels) != 2 {
		t.Fatalf("expected 2 relationships (missing pk skipped), got %d", len(rels))
	}
	assertRelationship(t, rels, kgID, pk1, store.RelUses)
	assertRelationship(t, rels, kgID, pk2, store.RelUses)
}

// TestResolveCloudFrontKeyGroupPublicKeys_Empty verifies no error / no edges
// when no key groups exist.
func TestResolveCloudFrontKeyGroupPublicKeys_Empty(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	if err := resolveCloudFrontKeyGroupPublicKeys(acct, st); err != nil {
		t.Fatalf("resolveCloudFrontKeyGroupPublicKeys: %v", err)
	}
}

// --- Realtime Log Config → Kinesis stream + IAM role ---

// TestResolveCloudFrontRealtimeLogConfigTargets verifies realtime-log-config →
// kinesis stream (uses) and → IAM role (assumes) edges.
func TestResolveCloudFrontRealtimeLogConfigTargets(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	streamARN := fmt.Sprintf("arn:aws:kinesis:%s:%s:stream/my-stream", testRegion, acct.ID)
	roleARN := fmt.Sprintf("arn:aws:iam::%s:role/cf-realtime-log", acct.ID)
	streamID := upsertTestResource(t, st, "aws", acct.ID, TypeKinesisStream, streamARN, testRegion, "{}")
	roleID := upsertTestResource(t, st, "aws", acct.ID, TypeIAMRole, roleARN, "", "{}")

	rtlcARN := fmt.Sprintf("arn:aws:cloudfront::%s:realtime-log-config/my-rtlc", acct.ID)
	attrs := fmt.Sprintf(`{"EndPoints":[{"KinesisStreamConfig":{"StreamARN":%q,"RoleARN":%q}}]}`, streamARN, roleARN)
	rtlcID := upsertTestResource(t, st, "aws", acct.ID, TypeCloudFrontRealtimeLogConfig, rtlcARN, "", attrs)

	if err := resolveCloudFrontRealtimeLogConfigTargets(acct, st); err != nil {
		t.Fatalf("resolveCloudFrontRealtimeLogConfigTargets: %v", err)
	}
	rels, _ := st.RelationshipsFrom(rtlcID)
	if len(rels) != 2 {
		t.Fatalf("expected 2 relationships, got %d", len(rels))
	}
	assertRelationship(t, rels, rtlcID, streamID, store.RelUses)
	assertRelationship(t, rels, rtlcID, roleID, store.RelAssumes)
}

// TestResolveCloudFrontRealtimeLogConfigTargets_UnscannedTargetsSkipped checks
// that targets not in the scan set silently skip (no FK error, no edge).
func TestResolveCloudFrontRealtimeLogConfigTargets_UnscannedTargetsSkipped(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	rtlcARN := fmt.Sprintf("arn:aws:cloudfront::%s:realtime-log-config/my-rtlc", acct.ID)
	missingStream := fmt.Sprintf("arn:aws:kinesis:%s:%s:stream/missing", testRegion, acct.ID)
	missingRole := fmt.Sprintf("arn:aws:iam::%s:role/missing", acct.ID)
	attrs := fmt.Sprintf(`{"EndPoints":[{"KinesisStreamConfig":{"StreamARN":%q,"RoleARN":%q}}]}`,
		missingStream, missingRole)
	rtlcID := upsertTestResource(t, st, "aws", acct.ID, TypeCloudFrontRealtimeLogConfig, rtlcARN, "", attrs)

	if err := resolveCloudFrontRealtimeLogConfigTargets(acct, st); err != nil {
		t.Fatalf("resolveCloudFrontRealtimeLogConfigTargets: %v", err)
	}
	rels, _ := st.RelationshipsFrom(rtlcID)
	if len(rels) != 0 {
		t.Errorf("expected 0 relationships (FK-safe skip), got %d", len(rels))
	}
}

// --- Streaming Distribution → S3 bucket ---

// TestResolveCloudFrontStreamingDistributionOrigins verifies the S3 origin
// bucket edge from a streaming distribution.
func TestResolveCloudFrontStreamingDistributionOrigins(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	bucketARN := "arn:aws:s3:::media-bucket"
	bucketID := upsertTestResource(t, st, "aws", acct.ID, TypeS3Bucket, bucketARN, "", "{}")

	sdARN := fmt.Sprintf("arn:aws:cloudfront::%s:streaming-distribution/E1XYZ", acct.ID)
	attrs := `{"S3Origin":{"DomainName":"media-bucket.s3.amazonaws.com"}}`
	sdID := upsertTestResource(t, st, "aws", acct.ID, TypeCloudFrontStreamingDistribution, sdARN, "", attrs)

	if err := resolveCloudFrontStreamingDistributionOrigins(acct, st); err != nil {
		t.Fatalf("resolveCloudFrontStreamingDistributionOrigins: %v", err)
	}
	rels, _ := st.RelationshipsFrom(sdID)
	if len(rels) != 1 {
		t.Fatalf("expected 1 relationship, got %d", len(rels))
	}
	assertRelationship(t, rels, sdID, bucketID, store.RelUses)
}

// --- Monitoring Subscription → Distribution ---

// TestResolveCloudFrontMonitoringSubscriptionParent verifies the
// monitoring-subscription → distribution attached-to edge.
func TestResolveCloudFrontMonitoringSubscriptionParent(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	distARN := fmt.Sprintf("arn:aws:cloudfront::%s:distribution/E1ABC", acct.ID)
	distID := upsertTestResource(t, st, "aws", acct.ID, TypeCloudFrontDistribution, distARN, "", "{}")
	subID := upsertTestResource(t, st, "aws", acct.ID, TypeCloudFrontMonitoringSubscription, distARN, "", "{}")

	if err := resolveCloudFrontMonitoringSubscriptionParent(acct, st); err != nil {
		t.Fatalf("resolveCloudFrontMonitoringSubscriptionParent: %v", err)
	}
	rels, _ := st.RelationshipsFrom(subID)
	if len(rels) != 1 {
		t.Fatalf("expected 1 relationship, got %d", len(rels))
	}
	assertRelationship(t, rels, subID, distID, store.RelAttachedTo)
}

// --- Connection Group → Anycast IP List ---

// TestResolveCloudFrontConnectionGroupAnycast verifies the connection-group →
// anycast-ip-list uses edge. The connection group carries the bare list ID; the
// anycast list row carries the full ARN as NativeID.
func TestResolveCloudFrontConnectionGroupAnycast(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	anycastID := "anycast-list-1"
	anycastARN := fmt.Sprintf("arn:aws:cloudfront::%s:anycast-ip-list/%s", acct.ID, anycastID)
	anyResID := upsertTestResource(t, st, "aws", acct.ID, TypeCloudFrontAnycastIPList, anycastARN, "", "{}")

	cgARN := fmt.Sprintf("arn:aws:cloudfront::%s:connection-group/cg-1", acct.ID)
	cgAttrs := fmt.Sprintf(`{"AnycastIpListId":%q}`, anycastID)
	cgID := upsertTestResource(t, st, "aws", acct.ID, TypeCloudFrontConnectionGroup, cgARN, "", cgAttrs)

	if err := resolveCloudFrontConnectionGroupAnycast(acct, st); err != nil {
		t.Fatalf("resolveCloudFrontConnectionGroupAnycast: %v", err)
	}
	rels, _ := st.RelationshipsFrom(cgID)
	if len(rels) != 1 {
		t.Fatalf("expected 1 relationship, got %d", len(rels))
	}
	assertRelationship(t, rels, cgID, anyResID, store.RelUses)
}

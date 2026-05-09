package aws

import (
	"fmt"
	"testing"

	"codeberg.org/icearp/disco/store"
)

// TestResolveAPIGatewayStageToDeployment verifies that a stage's DeploymentId
// produces a stage→deployment attaches-to relationship.
func TestResolveAPIGatewayStageToDeployment(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	apiID := "api123"
	deployID := "dep456"
	stageName := "prod"
	region := testRegion

	restAPIARN := fmt.Sprintf("arn:aws:apigateway:%s::/restapis/%s", region, apiID)
	stageARN := fmt.Sprintf("arn:aws:apigateway:%s::/restapis/%s/stages/%s", region, apiID, stageName)
	deployARN := fmt.Sprintf("arn:aws:apigateway:%s::/restapis/%s/deployments/%s", region, apiID, deployID)

	attrsJSON := fmt.Sprintf(`{"DeploymentId": "%s"}`, deployID)

	stageID := upsertTestResource(t, st, "aws", acct.ID, TypeAPIGatewayStage, stageARN, region, attrsJSON)
	deployResID := upsertTestResource(t, st, "aws", acct.ID, TypeAPIGatewayDeployment, deployARN, region, "{}")
	_ = upsertTestResource(t, st, "aws", acct.ID, TypeAPIGatewayRestAPI, restAPIARN, region, "{}")

	if err := resolveAPIGatewayStageRelationships(acct, st); err != nil {
		t.Fatalf("resolveAPIGatewayStageRelationships: %v", err)
	}

	rels, err := st.RelationshipsFrom(stageID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	assertRelationship(t, rels, stageID, deployResID, store.RelAttachedTo)
}

// TestResolveAPIGatewayStageToWAFv2 verifies that a stage's WebAclArn
// produces a stage→waf-web-acl uses relationship.
func TestResolveAPIGatewayStageToWAFv2(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	region := testRegion
	apiID := "apiWAF"
	stageARN := fmt.Sprintf("arn:aws:apigateway:%s::/restapis/%s/stages/prod", region, apiID)
	restAPIARN := fmt.Sprintf("arn:aws:apigateway:%s::/restapis/%s", region, apiID)
	aclARN := fmt.Sprintf("arn:aws:wafv2:%s:%s:regional/webacl/my-acl/abc123", region, acct.ID)

	attrs := fmt.Sprintf(`{"WebAclArn":%q}`, aclARN)
	stageID := upsertTestResource(t, st, "aws", acct.ID, TypeAPIGatewayStage, stageARN, region, attrs)
	_ = upsertTestResource(t, st, "aws", acct.ID, TypeAPIGatewayRestAPI, restAPIARN, region, "{}")
	aclID := upsertTestResource(t, st, "aws", acct.ID, TypeWAFv2WebACL, aclARN, region, "{}")

	if err := resolveAPIGatewayStageRelationships(acct, st); err != nil {
		t.Fatalf("resolveAPIGatewayStageRelationships: %v", err)
	}
	rels, err := st.RelationshipsFrom(stageID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	assertRelationship(t, rels, stageID, aclID, store.RelUses)
}

// TestResolveAPIGatewayStageToDeployment_NoAttrs verifies that a stage with
// empty attributes produces no error and no deployment relationship.
func TestResolveAPIGatewayStageToDeployment_NoAttrs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	// REST API must exist for the rest-api→stage contains relationship to succeed.
	restAPIARN := fmt.Sprintf("arn:aws:apigateway:%s::/restapis/api999", testRegion)
	stageARN := fmt.Sprintf("arn:aws:apigateway:%s::/restapis/api999/stages/dev", testRegion)
	_ = upsertTestResource(t, st, "aws", acct.ID, TypeAPIGatewayRestAPI, restAPIARN, testRegion, "{}")
	stageID := upsertTestResource(t, st, "aws", acct.ID, TypeAPIGatewayStage, stageARN, testRegion, "{}")

	if err := resolveAPIGatewayStageRelationships(acct, st); err != nil {
		t.Fatalf("resolveAPIGatewayStageRelationships: %v", err)
	}

	rels, err := st.RelationshipsFrom(stageID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	// No deployment edge — only the contains edge from rest-api exists.
	for _, rel := range rels {
		if rel.Kind == store.RelAttachedTo {
			t.Errorf("unexpected attaches-to relationship: %+v", rel)
		}
	}
}

// TestResolveAPIGatewayStageToClientCert verifies that a stage's
// ClientCertificateId produces a stage→client-certificate uses relationship.
func TestResolveAPIGatewayStageToClientCert(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	apiID := "api789"
	certID := "cert001"
	stageName := "v1"
	region := testRegion

	restAPIARN := fmt.Sprintf("arn:aws:apigateway:%s::/restapis/%s", region, apiID)
	stageARN := fmt.Sprintf("arn:aws:apigateway:%s::/restapis/%s/stages/%s", region, apiID, stageName)
	certARN := fmt.Sprintf("arn:aws:apigateway:%s::/clientcertificates/%s", region, certID)
	attrsJSON := fmt.Sprintf(`{"ClientCertificateId": "%s"}`, certID)

	// REST API must exist for the rest-api→stage contains FK to succeed.
	_ = upsertTestResource(t, st, "aws", acct.ID, TypeAPIGatewayRestAPI, restAPIARN, region, "{}")
	stageID := upsertTestResource(t, st, "aws", acct.ID, TypeAPIGatewayStage, stageARN, region, attrsJSON)
	certResID := upsertTestResource(t, st, "aws", acct.ID, TypeAPIGatewayClientCertificate, certARN, region, "{}")

	if err := resolveAPIGatewayStageRelationships(acct, st); err != nil {
		t.Fatalf("resolveAPIGatewayStageRelationships: %v", err)
	}

	rels, err := st.RelationshipsFrom(stageID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	assertRelationship(t, rels, stageID, certResID, store.RelUses)
}

// TestResolveAPIGatewayBasePathMappingToRestAPI verifies that a base-path
// mapping's RestApiId produces a mapping→rest-api routes-to relationship.
func TestResolveAPIGatewayBasePathMappingToRestAPI(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	region := testRegion
	domain := "api.example.com"
	apiID := "api321"

	mappingARN := fmt.Sprintf("arn:aws:apigateway:%s::/domainnames/%s/basepathmappings/v2", region, domain)
	domainARN := fmt.Sprintf("arn:aws:apigateway:%s::/domainnames/%s", region, domain)
	restAPIARN := fmt.Sprintf("arn:aws:apigateway:%s::/restapis/%s", region, apiID)
	attrsJSON := fmt.Sprintf(`{"RestApiId": "%s"}`, apiID)

	mappingID := upsertTestResource(t, st, "aws", acct.ID, TypeAPIGatewayBasePathMapping, mappingARN, region, attrsJSON)
	domainResID := upsertTestResource(t, st, "aws", acct.ID, TypeAPIGatewayDomainName, domainARN, region, "{}")
	restAPIResID := upsertTestResource(t, st, "aws", acct.ID, TypeAPIGatewayRestAPI, restAPIARN, region, "{}")

	if err := resolveAPIGatewayBasePathMappingRelationships(acct, st); err != nil {
		t.Fatalf("resolveAPIGatewayBasePathMappingRelationships: %v", err)
	}

	rels, err := st.RelationshipsFrom(mappingID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	assertRelationship(t, rels, mappingID, domainResID, store.RelAttachedTo)
	assertRelationship(t, rels, mappingID, restAPIResID, store.RelRoutesTo)
}

// TestResolveAPIGatewayPrivateBasePathMappingToPrivateDomain verifies that the
// V2 (private) variants of base-path mapping + domain name wire to each other
// via attached-to and to the rest API via routes-to.
func TestResolveAPIGatewayPrivateBasePathMappingToPrivateDomain(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	region := testRegion
	domain := "private.example.com"
	apiID := "privapi1"

	mappingARN := fmt.Sprintf("arn:aws:apigateway:%s::/domainnames/%s/basepathmappings/v2", region, domain)
	domainARN := fmt.Sprintf("arn:aws:apigateway:%s::/domainnames/%s", region, domain)
	restAPIARN := fmt.Sprintf("arn:aws:apigateway:%s::/restapis/%s", region, apiID)
	attrsJSON := fmt.Sprintf(`{"RestApiId": "%s"}`, apiID)

	mappingID := upsertTestResource(t, st, "aws", acct.ID, TypeAPIGatewayPrivateBasePathMapping, mappingARN, region, attrsJSON)
	domainResID := upsertTestResource(t, st, "aws", acct.ID, TypeAPIGatewayPrivateDomainName, domainARN, region, "{}")
	restAPIResID := upsertTestResource(t, st, "aws", acct.ID, TypeAPIGatewayRestAPI, restAPIARN, region, "{}")

	if err := resolveAPIGatewayBasePathMappingRelationships(acct, st); err != nil {
		t.Fatalf("resolveAPIGatewayBasePathMappingRelationships: %v", err)
	}

	rels, err := st.RelationshipsFrom(mappingID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	assertRelationship(t, rels, mappingID, domainResID, store.RelAttachedTo)
	assertRelationship(t, rels, mappingID, restAPIResID, store.RelRoutesTo)
}

// TestResolveAPIGatewayPrivateDomainToCert verifies that the V2 (private)
// custom domain name is linked to its ACM certificate via the V1-shape
// CertificateArn / RegionalCertificateArn fields shared with public domains.
func TestResolveAPIGatewayPrivateDomainToCert(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	region := testRegion
	domain := "priv2.example.com"
	certARN := fmt.Sprintf("arn:aws:acm:%s:%s:certificate/abcd-1234", region, acct.ID)
	domainARN := fmt.Sprintf("arn:aws:apigateway:%s::/domainnames/%s", region, domain)
	attrsJSON := fmt.Sprintf(`{"RegionalCertificateArn": "%s"}`, certARN)

	domainID := upsertTestResource(t, st, "aws", acct.ID, TypeAPIGatewayPrivateDomainName, domainARN, region, attrsJSON)
	certID := upsertTestResource(t, st, "aws", acct.ID, TypeACMCertificate, certARN, region, "{}")

	if err := resolveAPIGatewayDomainCertRelationships(acct, st); err != nil {
		t.Fatalf("resolveAPIGatewayDomainCertRelationships: %v", err)
	}

	rels, err := st.RelationshipsFrom(domainID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	assertRelationship(t, rels, domainID, certID, store.RelUses)
}

// TestResolveAPIGatewayBasePathMappingToRestAPI_NoAttrs verifies that a mapping
// without a RestApiId produces no routes-to relationship and no error.
func TestResolveAPIGatewayBasePathMappingToRestAPI_NoAttrs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	region := testRegion
	domain := "api2.example.com"
	// Domain must exist for the mapping→domain-name attached-to FK to succeed.
	domainARN := fmt.Sprintf("arn:aws:apigateway:%s::/domainnames/%s", region, domain)
	_ = upsertTestResource(t, st, "aws", acct.ID, TypeAPIGatewayDomainName, domainARN, region, "{}")
	mappingARN := fmt.Sprintf("arn:aws:apigateway:%s::/domainnames/%s/basepathmappings/(none)", region, domain)
	mappingID := upsertTestResource(t, st, "aws", acct.ID, TypeAPIGatewayBasePathMapping, mappingARN, region, "{}")

	if err := resolveAPIGatewayBasePathMappingRelationships(acct, st); err != nil {
		t.Fatalf("resolveAPIGatewayBasePathMappingRelationships: %v", err)
	}

	rels, err := st.RelationshipsFrom(mappingID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	for _, rel := range rels {
		if rel.Kind == store.RelRoutesTo {
			t.Errorf("unexpected routes-to relationship: %+v", rel)
		}
	}
}

// TestResolveAPIGatewayUsagePlanKeyToUsagePlan verifies that a usage-plan key
// ARN is parsed to produce a key→usage-plan attached-to relationship.
func TestResolveAPIGatewayUsagePlanKeyToUsagePlan(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	region := testRegion
	planID := "plan111"
	keyID := "key222"

	planARN := fmt.Sprintf("arn:aws:apigateway:%s::/usageplans/%s", region, planID)
	keyARN := fmt.Sprintf("arn:aws:apigateway:%s::/usageplans/%s/keys/%s", region, planID, keyID)

	keyResID := upsertTestResource(t, st, "aws", acct.ID, TypeAPIGatewayUsagePlanKey, keyARN, region, "{}")
	planResID := upsertTestResource(t, st, "aws", acct.ID, TypeAPIGatewayUsagePlan, planARN, region, "{}")

	if err := resolveAPIGatewayUsagePlanKeyRelationships(acct, st); err != nil {
		t.Fatalf("resolveAPIGatewayUsagePlanKeyRelationships: %v", err)
	}

	rels, err := st.RelationshipsFrom(keyResID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	assertRelationship(t, rels, keyResID, planResID, store.RelAttachedTo)
}

// TestResolveAPIGatewayUsagePlanKeyToUsagePlan_Empty verifies that the resolver
// produces no error when there are no usage plan keys.
func TestResolveAPIGatewayUsagePlanKeyToUsagePlan_Empty(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	if err := resolveAPIGatewayUsagePlanKeyRelationships(acct, st); err != nil {
		t.Fatalf("resolveAPIGatewayUsagePlanKeyRelationships: %v", err)
	}
}

// TestResolveAPIGatewayUsagePlanStages verifies that each ApiStages[] entry
// on a usage plan produces an attached-to edge to the REST API stage.
func TestResolveAPIGatewayUsagePlanStages(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	region := testRegion
	apiID := "api-xyz"
	stageName := "prod"
	planID := "plan-abc"

	stageARN := fmt.Sprintf("arn:aws:apigateway:%s::/restapis/%s/stages/%s", region, apiID, stageName)
	planARN := fmt.Sprintf("arn:aws:apigateway:%s::/usageplans/%s", region, planID)
	attrs := fmt.Sprintf(`{"ApiStages":[{"ApiId":"%s","Stage":"%s"}]}`, apiID, stageName)

	planResID := upsertTestResource(t, st, "aws", acct.ID, TypeAPIGatewayUsagePlan, planARN, region, attrs)
	stageResID := upsertTestResource(t, st, "aws", acct.ID, TypeAPIGatewayStage, stageARN, region, "{}")

	if err := resolveAPIGatewayUsagePlanStages(acct, st); err != nil {
		t.Fatalf("resolveAPIGatewayUsagePlanStages: %v", err)
	}

	rels, err := st.RelationshipsFrom(planResID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	assertRelationship(t, rels, planResID, stageResID, store.RelAttachedTo)
}

// TestResolveAPIGatewayUsagePlanStages_NoAttrs verifies that a usage plan
// with no ApiStages emits no edges and does not panic.
func TestResolveAPIGatewayUsagePlanStages_NoAttrs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	planARN := fmt.Sprintf("arn:aws:apigateway:%s::/usageplans/plan-bare", testRegion)
	planResID := upsertTestResource(t, st, "aws", acct.ID, TypeAPIGatewayUsagePlan, planARN, testRegion, "{}")

	if err := resolveAPIGatewayUsagePlanStages(acct, st); err != nil {
		t.Fatalf("resolveAPIGatewayUsagePlanStages: %v", err)
	}
	rels, _ := st.RelationshipsFrom(planResID)
	if len(rels) != 0 {
		t.Errorf("expected 0 relationships, got %d", len(rels))
	}
}

// TestResolveAPIGatewayAuthorizerCognito verifies that a REST API authorizer
// of type COGNITO_USER_POOLS produces a uses edge to each user pool in
// ProviderARNs.
func TestResolveAPIGatewayAuthorizerCognito(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	poolARN := fmt.Sprintf("arn:aws:cognito-idp:%s:%s:userpool/us-east-1_abcDEF", testRegion, acct.ID)
	authARN := fmt.Sprintf("arn:aws:apigateway:%s::/restapis/api-1/authorizers/auth-1", testRegion)
	attrs := fmt.Sprintf(`{"Type":"COGNITO_USER_POOLS","ProviderARNs":[%q]}`, poolARN)

	authID := upsertTestResource(t, st, "aws", acct.ID, TypeAPIGatewayAuthorizer, authARN, testRegion, attrs)
	poolID := upsertTestResource(t, st, "aws", acct.ID, TypeCognitoUserPool, poolARN, testRegion, "{}")

	if err := resolveAPIGatewayAuthorizerCognito(acct, st); err != nil {
		t.Fatalf("resolveAPIGatewayAuthorizerCognito: %v", err)
	}
	rels, _ := st.RelationshipsFrom(authID)
	assertRelationship(t, rels, authID, poolID, store.RelUses)
}

// TestResolveAPIGatewayAuthorizerCognito_TokenSkipped verifies that non-Cognito
// authorizer types produce no edge.
func TestResolveAPIGatewayAuthorizerCognito_TokenSkipped(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	authARN := fmt.Sprintf("arn:aws:apigateway:%s::/restapis/api-2/authorizers/auth-2", testRegion)
	attrs := `{"Type":"TOKEN","ProviderARNs":[]}`
	authID := upsertTestResource(t, st, "aws", acct.ID, TypeAPIGatewayAuthorizer, authARN, testRegion, attrs)

	if err := resolveAPIGatewayAuthorizerCognito(acct, st); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	rels, _ := st.RelationshipsFrom(authID)
	if len(rels) != 0 {
		t.Errorf("expected 0 rels, got %d", len(rels))
	}
}

// v2 authorizer ARN helper.
func v2AuthARN(region, apiID, authID string) string {
	return fmt.Sprintf("arn:aws:apigateway:%s::/apis/%s/authorizers/%s", region, apiID, authID)
}

// TestResolveAPIGWV2Authorizer_CognitoIssuer — JWT issuer with Cognito URL
// emits authorizer→user-pool edge.
func TestResolveAPIGWV2Authorizer_CognitoIssuer(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	region := "us-east-1"
	poolID := "us-east-1_abc123"
	poolARN := fmt.Sprintf("arn:aws:cognito-idp:%s:%s:userpool/%s", region, acct.ID, poolID)

	poolResID := upsertTestResource(t, st, "aws", acct.ID, TypeCognitoUserPool, poolARN, region, "{}")
	attrs := fmt.Sprintf(`{"AuthorizerType":"JWT","JwtConfiguration":{"Issuer":"https://cognito-idp.%s.amazonaws.com/%s"}}`, region, poolID)
	authResID := upsertTestResource(t, st, "aws", acct.ID, TypeAPIGatewayV2Authorizer, v2AuthARN(region, "api1", "auth1"), region, attrs)

	if err := resolveAPIGatewayV2AuthorizerCognito(acct, st); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	rels, err := st.RelationshipsFrom(authResID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 1 || rels[0].ToID != poolResID || rels[0].Kind != store.RelUses {
		t.Fatalf("want 1 uses edge to %s, got %+v", poolResID, rels)
	}
}

// TestResolveAPIGWV2Authorizer_NonJWTType — REQUEST authorizers skipped.
func TestResolveAPIGWV2Authorizer_NonJWTType(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	attrs := `{"AuthorizerType":"REQUEST","JwtConfiguration":{"Issuer":"https://cognito-idp.us-east-1.amazonaws.com/us-east-1_abc123"}}`
	authID := upsertTestResource(t, st, "aws", acct.ID, TypeAPIGatewayV2Authorizer, v2AuthARN("us-east-1", "api1", "auth1"), "us-east-1", attrs)

	if err := resolveAPIGatewayV2AuthorizerCognito(acct, st); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	rels, _ := st.RelationshipsFrom(authID)
	if len(rels) != 0 {
		t.Errorf("expected 0 rels, got %d", len(rels))
	}
}

// TestResolveAPIGWV2Authorizer_NonCognitoIssuer — Auth0/Okta issuer skipped.
func TestResolveAPIGWV2Authorizer_NonCognitoIssuer(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	attrs := `{"AuthorizerType":"JWT","JwtConfiguration":{"Issuer":"https://example.auth0.com/"}}`
	authID := upsertTestResource(t, st, "aws", acct.ID, TypeAPIGatewayV2Authorizer, v2AuthARN("us-east-1", "api1", "auth1"), "us-east-1", attrs)

	if err := resolveAPIGatewayV2AuthorizerCognito(acct, st); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	rels, _ := st.RelationshipsFrom(authID)
	if len(rels) != 0 {
		t.Errorf("expected 0 rels, got %d", len(rels))
	}
}

// TestResolveAPIGWV2Authorizer_EmptyIssuer — JWT type without issuer skipped.
func TestResolveAPIGWV2Authorizer_EmptyIssuer(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	attrs := `{"AuthorizerType":"JWT"}`
	authID := upsertTestResource(t, st, "aws", acct.ID, TypeAPIGatewayV2Authorizer, v2AuthARN("us-east-1", "api1", "auth1"), "us-east-1", attrs)

	if err := resolveAPIGatewayV2AuthorizerCognito(acct, st); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	rels, _ := st.RelationshipsFrom(authID)
	if len(rels) != 0 {
		t.Errorf("expected 0 rels, got %d", len(rels))
	}
}

// TestResolveAPIGWV2Authorizer_MalformedIssuer — issuer with no path segment skipped.
func TestResolveAPIGWV2Authorizer_MalformedIssuer(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	attrs := `{"AuthorizerType":"JWT","JwtConfiguration":{"Issuer":"https://cognito-idp.us-east-1.amazonaws.com"}}`
	authID := upsertTestResource(t, st, "aws", acct.ID, TypeAPIGatewayV2Authorizer, v2AuthARN("us-east-1", "api1", "auth1"), "us-east-1", attrs)

	if err := resolveAPIGatewayV2AuthorizerCognito(acct, st); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	rels, _ := st.RelationshipsFrom(authID)
	if len(rels) != 0 {
		t.Errorf("expected 0 rels, got %d", len(rels))
	}
}

package aws

import (
	"fmt"
	"testing"

	"codeberg.org/icearp/disco/internal/store"
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

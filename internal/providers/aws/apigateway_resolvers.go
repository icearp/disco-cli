package aws

import (
	"encoding/json"
	"fmt"
	"strings"

	"codeberg.org/icearp/disco/internal/store"
	"codeberg.org/icearp/disco/internal/util"
)

func init() {
	registerResolver(func(acct *account, st *store.Store) error {
		if err := resolveAPIGatewayStageRelationships(acct, st); err != nil {
			return err
		}
		if err := resolveAPIGatewayBasePathMappingRelationships(acct, st); err != nil {
			return err
		}
		return resolveAPIGatewayUsagePlanKeyRelationships(acct, st)
	})
}

// resolveAPIGatewayStageRelationships links each stage to:
//   - its deployment via attaches-to (DeploymentId in attrs)
//   - its client certificate via uses (ClientCertificateId in attrs)
//   - its parent REST API via contains (REST API ID extracted from the stage ARN)
//
// Stage ARN format: arn:aws:apigateway:{region}::/restapis/{apiId}/stages/{stageName}
func resolveAPIGatewayStageRelationships(acct *account, st *store.Store) error {
	stages, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeAPIGatewayStage},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	for _, r := range stages {
		// Link stage to its parent REST API (contains). REST API ARN is derived
		// by stripping the /stages/{name} suffix from the stage ARN.
		apiID := apiGatewayRestAPIIDFromChildARN(r.NativeID, "stages")
		if apiID != "" {
			restAPIARN := apiGatewayRestAPIARN(r.NativeID)
			parentID := store.ResourceID("aws", acct.ID, TypeAPIGatewayRestAPI, restAPIARN)
			if err := st.UpsertRelationship(parentID, r.ID, store.RelContains, "directed", nil); err != nil {
				return fmt.Errorf("upsert rest-api→stage contains: %w", err)
			}
		}

		var attrs struct {
			DeploymentId        *string `json:"DeploymentId"`
			ClientCertificateId *string `json:"ClientCertificateId"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}

		if attrs.DeploymentId != nil && *attrs.DeploymentId != "" && apiID != "" {
			// Reconstruct deployment ARN from stage ARN components.
			deployARN := apiGatewayPerAPIChildARN(r.NativeID, "deployments", *attrs.DeploymentId)
			deployID := store.ResourceID("aws", acct.ID, TypeAPIGatewayDeployment, deployARN)
			if err := st.UpsertRelationship(r.ID, deployID, store.RelAttachedTo, "directed", nil); err != nil {
				return fmt.Errorf("upsert stage→deployment: %w", err)
			}
		}

		if attrs.ClientCertificateId != nil && *attrs.ClientCertificateId != "" {
			region := sv(r.Region)
			certARN := fmt.Sprintf("arn:aws:apigateway:%s::/clientcertificates/%s", region, *attrs.ClientCertificateId)
			certID := store.ResourceID("aws", acct.ID, TypeAPIGatewayClientCertificate, certARN)
			if err := st.UpsertRelationship(r.ID, certID, store.RelUses, "directed", nil); err != nil {
				return fmt.Errorf("upsert stage→client-certificate: %w", err)
			}
		}
	}
	return nil
}

// resolveAPIGatewayBasePathMappingRelationships links each base-path mapping to:
//   - its domain name via attached-to (domain name extracted from the mapping ARN)
//   - its target REST API via routes-to (RestApiId in attrs)
//
// Mapping ARN: arn:aws:apigateway:{region}::/domainnames/{domainName}/basepathmappings/{basePath}
func resolveAPIGatewayBasePathMappingRelationships(acct *account, st *store.Store) error {
	mappings, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeAPIGatewayBasePathMapping},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	for _, r := range mappings {
		region := sv(r.Region)

		// Extract domain name from ARN: .../domainnames/{domainName}/basepathmappings/...
		domainName := apiGatewayDomainNameFromMappingARN(r.NativeID)
		if domainName != "" {
			domainARN := fmt.Sprintf("arn:aws:apigateway:%s::/domainnames/%s", region, domainName)
			domainID := store.ResourceID("aws", acct.ID, TypeAPIGatewayDomainName, domainARN)
			if err := st.UpsertRelationship(r.ID, domainID, store.RelAttachedTo, "directed", nil); err != nil {
				return fmt.Errorf("upsert base-path-mapping→domain-name: %w", err)
			}
		}

		var attrs struct {
			RestApiId *string `json:"RestApiId"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if attrs.RestApiId != nil && *attrs.RestApiId != "" {
			restAPIARN := fmt.Sprintf("arn:aws:apigateway:%s::/restapis/%s", region, *attrs.RestApiId)
			restAPIID := store.ResourceID("aws", acct.ID, TypeAPIGatewayRestAPI, restAPIARN)
			if err := st.UpsertRelationship(r.ID, restAPIID, store.RelRoutesTo, "directed", nil); err != nil {
				return fmt.Errorf("upsert base-path-mapping→rest-api: %w", err)
			}
		}
	}
	return nil
}

// resolveAPIGatewayUsagePlanKeyRelationships links each usage-plan key to its
// parent usage plan via attached-to.
// Key ARN: arn:aws:apigateway:{region}::/usageplans/{planId}/keys/{keyId}
func resolveAPIGatewayUsagePlanKeyRelationships(acct *account, st *store.Store) error {
	keys, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeAPIGatewayUsagePlanKey},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	for _, r := range keys {
		// ARN: arn:aws:apigateway:{region}::/usageplans/{planId}/keys/{keyId}
		// Strip /keys/{keyId} suffix to get the usage plan ARN.
		planARN := apiGatewayUsagePlanARNFromKeyARN(r.NativeID)
		if planARN == "" {
			continue
		}
		planID := store.ResourceID("aws", acct.ID, TypeAPIGatewayUsagePlan, planARN)
		if err := st.UpsertRelationship(r.ID, planID, store.RelAttachedTo, "directed", nil); err != nil {
			return fmt.Errorf("upsert usage-plan-key→usage-plan: %w", err)
		}
	}
	return nil
}

// --- ARN helper functions ---

// apiGatewayRestAPIIDFromChildARN extracts the REST API ID from a child resource ARN.
// e.g. "arn:aws:apigateway:us-east-1::/restapis/abc123/stages/prod" with segment "stages" → "abc123"
func apiGatewayRestAPIIDFromChildARN(arn, segment string) string {
	// ARN path after "::/restapis/" is "{apiId}/{segment}/..."
	const prefix = "::/restapis/"
	idx := strings.Index(arn, prefix)
	if idx < 0 {
		return ""
	}
	rest := arn[idx+len(prefix):]
	parts := strings.SplitN(rest, "/", 3)
	if len(parts) < 2 || parts[1] != segment {
		return ""
	}
	return parts[0]
}

// apiGatewayRestAPIARN reconstructs the parent REST API ARN from a child ARN.
// e.g. "arn:aws:apigateway:us-east-1::/restapis/abc123/stages/prod" → "arn:aws:apigateway:us-east-1::/restapis/abc123"
func apiGatewayRestAPIARN(childARN string) string {
	const prefix = "::/restapis/"
	idx := strings.Index(childARN, prefix)
	if idx < 0 {
		return ""
	}
	rest := childARN[idx+len(prefix):]
	apiID := strings.SplitN(rest, "/", 2)[0]
	return childARN[:idx] + prefix + apiID
}

// apiGatewayPerAPIChildARN reconstructs a child resource ARN from a sibling ARN.
// e.g. stage ARN + "deployments" + "dep123" → deployment ARN for same region/API
func apiGatewayPerAPIChildARN(siblingARN, childSegment, childID string) string {
	restAPIARN := apiGatewayRestAPIARN(siblingARN)
	if restAPIARN == "" {
		return ""
	}
	return fmt.Sprintf("%s/%s/%s", restAPIARN, childSegment, childID)
}

// apiGatewayDomainNameFromMappingARN extracts the domain name from a base-path-mapping ARN.
// e.g. "arn:aws:apigateway:us-east-1::/domainnames/api.example.com/basepathmappings/v1" → "api.example.com"
func apiGatewayDomainNameFromMappingARN(arn string) string {
	const prefix = "::/domainnames/"
	idx := strings.Index(arn, prefix)
	if idx < 0 {
		return ""
	}
	rest := arn[idx+len(prefix):]
	return strings.SplitN(rest, "/", 2)[0]
}

// apiGatewayUsagePlanARNFromKeyARN strips the /keys/{keyId} suffix to produce
// the parent usage plan ARN.
// e.g. "arn:aws:apigateway:us-east-1::/usageplans/plan1/keys/key1" → "arn:aws:apigateway:us-east-1::/usageplans/plan1"
func apiGatewayUsagePlanARNFromKeyARN(arn string) string {
	const segment = "/keys/"
	idx := strings.LastIndex(arn, segment)
	if idx < 0 {
		return ""
	}
	return arn[:idx]
}

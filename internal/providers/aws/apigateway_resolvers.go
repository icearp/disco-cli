package aws

import (
	"encoding/json"
	"fmt"
	"strings"

	"codeberg.org/icearp/disco/internal/util"
	"codeberg.org/icearp/disco/store"
)

// resolveAPIGatewayAll runs every API Gateway sub-resolver in sequence,
// stopping at the first error. Named so it surfaces in `disco coverage
// resolvers` and `ScanError.Service` rather than the reflected `func1`
// closure name.
func resolveAPIGatewayAll(acct *account, st *store.Store) error {
	if err := resolveAPIGatewayStageRelationships(acct, st); err != nil {
		return err
	}
	if err := resolveAPIGatewayBasePathMappingRelationships(acct, st); err != nil {
		return err
	}
	if err := resolveAPIGatewayUsagePlanKeyRelationships(acct, st); err != nil {
		return err
	}
	if err := resolveAPIGatewayUsagePlanStages(acct, st); err != nil {
		return err
	}
	if err := resolveAPIGatewayMethodRelationships(acct, st); err != nil {
		return err
	}
	if err := resolveAPIGatewayAuthorizerCognito(acct, st); err != nil {
		return err
	}
	if err := resolveAPIGatewayV2AuthorizerCognito(acct, st); err != nil {
		return err
	}
	if err := resolveAPIGatewayV2VpcLinkRelationships(acct, st); err != nil {
		return err
	}
	return resolveAPIGatewayDomainCertRelationships(acct, st)
}

func init() {
	registerResolver(
		resolveAPIGatewayAll,
		// resolveAPIGatewayStageRelationships
		EdgeDecl{TypeAPIGatewayRestAPI, TypeAPIGatewayStage, store.RelContains},
		EdgeDecl{TypeAPIGatewayStage, TypeAPIGatewayDeployment, store.RelAttachedTo},
		EdgeDecl{TypeAPIGatewayStage, TypeAPIGatewayClientCertificate, store.RelUses},
		EdgeDecl{TypeAPIGatewayStage, TypeWAFv2WebACL, store.RelUses},
		// resolveAPIGatewayBasePathMappingRelationships
		EdgeDecl{TypeAPIGatewayBasePathMapping, TypeAPIGatewayDomainName, store.RelAttachedTo},
		EdgeDecl{TypeAPIGatewayBasePathMapping, TypeAPIGatewayPrivateDomainName, store.RelAttachedTo},
		EdgeDecl{TypeAPIGatewayBasePathMapping, TypeAPIGatewayRestAPI, store.RelRoutesTo},
		EdgeDecl{TypeAPIGatewayPrivateBasePathMapping, TypeAPIGatewayDomainName, store.RelAttachedTo},
		EdgeDecl{TypeAPIGatewayPrivateBasePathMapping, TypeAPIGatewayPrivateDomainName, store.RelAttachedTo},
		EdgeDecl{TypeAPIGatewayPrivateBasePathMapping, TypeAPIGatewayRestAPI, store.RelRoutesTo},
		// resolveAPIGatewayUsagePlanKeyRelationships
		EdgeDecl{TypeAPIGatewayUsagePlanKey, TypeAPIGatewayUsagePlan, store.RelAttachedTo},
		// resolveAPIGatewayUsagePlanStages
		EdgeDecl{TypeAPIGatewayUsagePlan, TypeAPIGatewayStage, store.RelAttachedTo},
		// resolveAPIGatewayMethodRelationships
		EdgeDecl{TypeAPIGatewayMethod, TypeLambdaFunction, store.RelUses},
		EdgeDecl{TypeAPIGatewayMethod, TypeAPIGatewayVpcLink, store.RelAttachedTo},
		// resolveAPIGatewayAuthorizerCognito
		EdgeDecl{TypeAPIGatewayAuthorizer, TypeCognitoUserPool, store.RelUses},
		// resolveAPIGatewayV2AuthorizerCognito
		EdgeDecl{TypeAPIGatewayV2Authorizer, TypeCognitoUserPool, store.RelUses},
		// resolveAPIGatewayV2VpcLinkRelationships
		EdgeDecl{TypeAPIGatewayV2VpcLink, TypeEC2SecurityGroup, store.RelUses},
		EdgeDecl{TypeAPIGatewayV2VpcLink, TypeEC2Subnet, store.RelAttachedTo},
		// resolveAPIGatewayDomainCertRelationships
		EdgeDecl{TypeAPIGatewayDomainName, TypeACMCertificate, store.RelUses},
		EdgeDecl{TypeAPIGatewayPrivateDomainName, TypeACMCertificate, store.RelUses},
		EdgeDecl{TypeAPIGatewayDomainNameV2, TypeACMCertificate, store.RelUses},
	)
}

// resolveAPIGatewayV2VpcLinkRelationships emits VPC-link → security-group
// (uses) and VPC-link → subnet (attached-to) edges. SecurityGroupIds and
// SubnetIds are required fields on the SDK VpcLink struct; FK-safe via
// scanned id sets.
func resolveAPIGatewayV2VpcLinkRelationships(acct *account, st *store.Store) error {
	links, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeAPIGatewayV2VpcLink},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(links) == 0 {
		return nil
	}
	sgIDs, err := scannedIDSet(acct, st, TypeEC2SecurityGroup)
	if err != nil {
		return err
	}
	subnetIDs, err := scannedIDSet(acct, st, TypeEC2Subnet)
	if err != nil {
		return err
	}
	type attrs struct {
		SecurityGroupIDs []string `json:"SecurityGroupIds"`
		SubnetIDs        []string `json:"SubnetIds"`
	}
	for _, l := range links {
		var a attrs
		if err := json.Unmarshal([]byte(l.AttributesJSON), &a); err != nil {
			continue
		}
		region := sv(l.Region)
		for _, sg := range a.SecurityGroupIDs {
			if sg == "" {
				continue
			}
			id := store.ResourceID("aws", acct.ID, TypeEC2SecurityGroup, ec2ARN(region, acct.ID, "security-group", sg))
			if _, ok := sgIDs[id]; ok {
				if err := st.UpsertRelationship(l.ID, id, store.RelUses, "directed", nil); err != nil {
					return fmt.Errorf("upsert apigatewayv2-vpclink→sg: %w", err)
				}
			}
		}
		for _, sn := range a.SubnetIDs {
			if sn == "" {
				continue
			}
			id := store.ResourceID("aws", acct.ID, TypeEC2Subnet, ec2ARN(region, acct.ID, "subnet", sn))
			if _, ok := subnetIDs[id]; ok {
				if err := st.UpsertRelationship(l.ID, id, store.RelAttachedTo, "directed", nil); err != nil {
					return fmt.Errorf("upsert apigatewayv2-vpclink→subnet: %w", err)
				}
			}
		}
	}
	return nil
}

// resolveAPIGatewayAuthorizerCognito emits an authorizer → Cognito user-pool
// edge for each REST API authorizer whose Type is COGNITO_USER_POOLS. The
// user-pool ARNs are carried in ProviderARNs[]. Skip any with no ARNs.
func resolveAPIGatewayAuthorizerCognito(acct *account, st *store.Store) error {
	auths, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeAPIGatewayAuthorizer},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	for _, r := range auths {
		var attrs struct {
			Type         string   `json:"Type"`
			ProviderARNs []string `json:"ProviderARNs"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if attrs.Type != "COGNITO_USER_POOLS" {
			continue
		}
		for _, arn := range attrs.ProviderARNs {
			if arn == "" {
				continue
			}
			poolID := store.ResourceID("aws", acct.ID, TypeCognitoUserPool, arn)
			if err := st.UpsertRelationship(r.ID, poolID, store.RelUses, "directed", nil); err != nil {
				return fmt.Errorf("upsert apigw-authorizer→cognito: %w", err)
			}
		}
	}
	return nil
}

// resolveAPIGatewayV2AuthorizerCognito emits an authorizer → Cognito user-pool
// edge for each v2 (HTTP API) authorizer whose AuthorizerType is JWT and whose
// JwtConfiguration.Issuer is a Cognito pool URL of the form
// https://cognito-idp.{region}.amazonaws.com/{userPoolId}. Non-Cognito JWT
// issuers (Auth0, Okta, ...) are skipped — no phantom edges.
func resolveAPIGatewayV2AuthorizerCognito(acct *account, st *store.Store) error {
	auths, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeAPIGatewayV2Authorizer},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	const issuerPrefix = "https://cognito-idp."
	const hostSuffix = ".amazonaws.com"
	for _, r := range auths {
		var attrs struct {
			AuthorizerType   string `json:"AuthorizerType"`
			JwtConfiguration *struct {
				Issuer *string `json:"Issuer"`
			} `json:"JwtConfiguration"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if attrs.AuthorizerType != "JWT" || attrs.JwtConfiguration == nil {
			continue
		}
		issuer := sv(attrs.JwtConfiguration.Issuer)
		if !strings.HasPrefix(issuer, issuerPrefix) {
			continue
		}
		// Trim scheme/prefix, split host/path on first "/".
		rest := strings.TrimPrefix(issuer, issuerPrefix)
		host, poolID, ok := strings.Cut(rest, "/")
		if !ok {
			continue
		}
		if !strings.HasSuffix(host, hostSuffix) || poolID == "" {
			continue
		}
		region := strings.TrimSuffix(host, hostSuffix)
		if region == "" {
			continue
		}
		poolARN := fmt.Sprintf("arn:aws:cognito-idp:%s:%s:userpool/%s", region, acct.ID, poolID)
		targetID := store.ResourceID("aws", acct.ID, TypeCognitoUserPool, poolARN)
		if err := st.UpsertRelationship(r.ID, targetID, store.RelUses, "directed", nil); err != nil {
			return fmt.Errorf("upsert apigw-v2-authorizer→cognito: %w", err)
		}
	}
	return nil
}

// resolveAPIGatewayDomainCertRelationships links each custom domain name
// (v1 + v2) to its ACM certificate. APIGW v1 exposes both edge-optimized
// (CertificateArn) and regional (RegionalCertificateArn) ARNs; either may be
// present. APIGW v2 uses DomainNameConfigurations[].CertificateArn.
func resolveAPIGatewayDomainCertRelationships(acct *account, st *store.Store) error {
	// v1 domains
	v1, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID,
		Types: []string{TypeAPIGatewayDomainName, TypeAPIGatewayPrivateDomainName},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	for _, r := range v1 {
		var attrs struct {
			CertificateArn         *string `json:"CertificateArn"`
			RegionalCertificateArn *string `json:"RegionalCertificateArn"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		for _, arn := range []string{sv(attrs.CertificateArn), sv(attrs.RegionalCertificateArn)} {
			if !strings.HasPrefix(arn, "arn:aws:acm:") {
				continue
			}
			certID := store.ResourceID("aws", acct.ID, TypeACMCertificate, arn)
			if err := st.UpsertRelationship(r.ID, certID, store.RelUses, "directed", nil); err != nil {
				return fmt.Errorf("upsert apigw-domain→acm-cert: %w", err)
			}
		}
	}
	// v2 domains
	v2, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeAPIGatewayDomainNameV2},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	for _, r := range v2 {
		var attrs struct {
			DomainNameConfigurations []struct {
				CertificateArn *string `json:"CertificateArn"`
			} `json:"DomainNameConfigurations"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		for _, dc := range attrs.DomainNameConfigurations {
			arn := sv(dc.CertificateArn)
			if !strings.HasPrefix(arn, "arn:aws:acm:") {
				continue
			}
			certID := store.ResourceID("aws", acct.ID, TypeACMCertificate, arn)
			if err := st.UpsertRelationship(r.ID, certID, store.RelUses, "directed", nil); err != nil {
				return fmt.Errorf("upsert apigwv2-domain→acm-cert: %w", err)
			}
		}
	}
	return nil
}

// resolveAPIGatewayMethodRelationships walks each method's MethodIntegration
// and emits edges to the backend. Lambda proxy/non-proxy integrations produce
// a uses→Lambda function edge (extracted from the URI). VPC_LINK integrations
// produce an attached-to→VpcLink edge (ConnectionID).
func resolveAPIGatewayMethodRelationships(acct *account, st *store.Store) error {
	methods, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeAPIGatewayMethod},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	for _, r := range methods {
		var attrs struct {
			MethodIntegration *struct {
				Type         *string `json:"Type"`
				URI          *string `json:"URI"`
				ConnectionID *string `json:"ConnectionID"`
			} `json:"MethodIntegration"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil || attrs.MethodIntegration == nil {
			continue
		}
		integ := attrs.MethodIntegration
		if fnARN := apigwLambdaInvokeARN(sv(integ.URI)); fnARN != "" {
			fnID := store.ResourceID("aws", acct.ID, TypeLambdaFunction, fnARN)
			if err := st.UpsertRelationship(r.ID, fnID, store.RelUses, "directed", nil); err != nil {
				return fmt.Errorf("upsert apigw-method→lambda: %w", err)
			}
		}
		if sv(integ.Type) == "VPC_LINK" && sv(integ.ConnectionID) != "" {
			vpcLinkARN := apigatewayARN(sv(r.Region), "vpclinks", *integ.ConnectionID)
			vpcLinkID := store.ResourceID("aws", acct.ID, TypeAPIGatewayVpcLink, vpcLinkARN)
			if err := st.UpsertRelationship(r.ID, vpcLinkID, store.RelAttachedTo, "directed", nil); err != nil {
				return fmt.Errorf("upsert apigw-method→vpclink: %w", err)
			}
		}
	}
	return nil
}

// apigwLambdaInvokeARN extracts the Lambda function ARN from an API Gateway
// integration URI. Lambda integration URI format:
// arn:aws:apigateway:{r}:lambda:path/2015-03-31/functions/{fnARN}/invocations
// Returns "" if uri is not a Lambda integration.
func apigwLambdaInvokeARN(uri string) string {
	const marker = ":lambda:path/2015-03-31/functions/"
	_, after, ok := strings.Cut(uri, marker)
	if !ok {
		return ""
	}
	if fnARN, _, ok := strings.Cut(after, "/invocations"); ok {
		after = fnARN
	}
	if !strings.HasPrefix(after, "arn:") {
		return ""
	}
	return after
}

// resolveAPIGatewayStageRelationships links each stage to:
//   - its deployment via attaches-to (DeploymentID in attrs)
//   - its client certificate via uses (ClientCertificateID in attrs)
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
			DeploymentID        *string `json:"DeploymentID"`
			ClientCertificateID *string `json:"ClientCertificateID"`
			WebACLArn           *string `json:"WebACLArn"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}

		if attrs.DeploymentID != nil && *attrs.DeploymentID != "" && apiID != "" {
			// Reconstruct deployment ARN from stage ARN components.
			deployARN := apiGatewayPerAPIChildARN(r.NativeID, "deployments", *attrs.DeploymentID)
			deployID := store.ResourceID("aws", acct.ID, TypeAPIGatewayDeployment, deployARN)
			if err := st.UpsertRelationship(r.ID, deployID, store.RelAttachedTo, "directed", nil); err != nil {
				return fmt.Errorf("upsert stage→deployment: %w", err)
			}
		}

		if attrs.ClientCertificateID != nil && *attrs.ClientCertificateID != "" {
			region := sv(r.Region)
			certARN := apigatewayARN(region, "clientcertificates", *attrs.ClientCertificateID)
			certID := store.ResourceID("aws", acct.ID, TypeAPIGatewayClientCertificate, certARN)
			if err := st.UpsertRelationship(r.ID, certID, store.RelUses, "directed", nil); err != nil {
				return fmt.Errorf("upsert stage→client-certificate: %w", err)
			}
		}

		if sv(attrs.WebACLArn) != "" {
			aclID := store.ResourceID("aws", acct.ID, TypeWAFv2WebACL, *attrs.WebACLArn)
			if err := st.UpsertRelationship(r.ID, aclID, store.RelUses, "directed", nil); err != nil {
				return fmt.Errorf("upsert stage→waf-web-acl: %w", err)
			}
		}
	}
	return nil
}

// resolveAPIGatewayBasePathMappingRelationships links each base-path mapping to:
//   - its domain name via attached-to (domain name extracted from the mapping ARN)
//   - its target REST API via routes-to (RestAPIID in attrs)
//
// Mapping ARN: arn:aws:apigateway:{region}::/domainnames/{domainName}/basepathmappings/{basePath}
func resolveAPIGatewayBasePathMappingRelationships(acct *account, st *store.Store) error {
	mappings, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID,
		Types: []string{TypeAPIGatewayBasePathMapping, TypeAPIGatewayPrivateBasePathMapping},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	// Build the set of private-domain IDs once so the parent-domain edge can
	// fall through to the private type when the public-type lookup misses.
	privateDomainIDs, err := scannedIDSet(acct, st, TypeAPIGatewayPrivateDomainName)
	if err != nil {
		return err
	}
	for _, r := range mappings {
		region := sv(r.Region)

		// Extract domain name from ARN: .../domainnames/{domainName}/basepathmappings/...
		domainName := apiGatewayDomainNameFromMappingARN(r.NativeID)
		if domainName != "" {
			domainARN := apigatewayARN(region, "domainnames", domainName)
			domainType := TypeAPIGatewayDomainName
			if r.Type == TypeAPIGatewayPrivateBasePathMapping {
				domainType = TypeAPIGatewayPrivateDomainName
			}
			domainID := store.ResourceID("aws", acct.ID, domainType, domainARN)
			// Mapping inherits its parent-domain type from the scanner branch;
			// fall back to the other type only if the primary lookup misses
			// (defensive for older rows scanned before the V2 split).
			if domainType == TypeAPIGatewayDomainName {
				if _, isPrivate := privateDomainIDs[store.ResourceID("aws", acct.ID, TypeAPIGatewayPrivateDomainName, domainARN)]; isPrivate {
					domainID = store.ResourceID("aws", acct.ID, TypeAPIGatewayPrivateDomainName, domainARN)
				}
			}
			if err := st.UpsertRelationship(r.ID, domainID, store.RelAttachedTo, "directed", nil); err != nil {
				return fmt.Errorf("upsert base-path-mapping→domain-name: %w", err)
			}
		}

		var attrs struct {
			RestAPIID *string `json:"RestAPIID"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if attrs.RestAPIID != nil && *attrs.RestAPIID != "" {
			restAPIARN := apigatewayARN(region, "restapis", *attrs.RestAPIID)
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

// resolveAPIGatewayUsagePlanStages links each usage plan to the REST API
// stages it applies to. The scanner stores the full GetUsagePlans item under
// attributes; APIStages[] carries {APIID, Stage} pairs. Rebuild each stage's
// NativeID using the scanner's ARN shape
// (arn:aws:apigateway:{region}::/restapis/{apiId}/stages/{stage}) and emit an
// attached-to edge.
func resolveAPIGatewayUsagePlanStages(acct *account, st *store.Store) error {
	plans, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeAPIGatewayUsagePlan},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	for _, p := range plans {
		var attrs struct {
			APIStages []struct {
				APIID *string `json:"APIID"`
				Stage *string `json:"Stage"`
			} `json:"APIStages"`
		}
		if err := json.Unmarshal([]byte(p.AttributesJSON), &attrs); err != nil {
			continue
		}
		region := sv(p.Region)
		for _, s := range attrs.APIStages {
			apiID := sv(s.APIID)
			stage := sv(s.Stage)
			if apiID == "" || stage == "" {
				continue
			}
			stageARN := apigatewayARN(region, "restapis", apiID, "stages", stage)
			stageID := store.ResourceID("aws", acct.ID, TypeAPIGatewayStage, stageARN)
			if err := st.UpsertRelationship(p.ID, stageID, store.RelAttachedTo, "directed", nil); err != nil {
				return fmt.Errorf("upsert usage-plan→stage: %w", err)
			}
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

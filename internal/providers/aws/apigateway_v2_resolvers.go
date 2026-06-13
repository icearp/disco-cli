package aws

import (
	"encoding/json"
	"fmt"
	"strings"

	"codeberg.org/icearp/disco/internal/util"
	"codeberg.org/icearp/disco/store"
)

func init() {
	registerResolver(
		resolveAPIGatewayV2APIChildren,
		EdgeDecl{TypeAPIGatewayV2Authorizer, TypeAPIGatewayV2API, store.RelAttachedTo},
		EdgeDecl{TypeAPIGatewayV2Deployment, TypeAPIGatewayV2API, store.RelAttachedTo},
		EdgeDecl{TypeAPIGatewayV2Integration, TypeAPIGatewayV2API, store.RelAttachedTo},
		EdgeDecl{TypeAPIGatewayV2IntegrationResponse, TypeAPIGatewayV2API, store.RelAttachedTo},
		EdgeDecl{TypeAPIGatewayV2Model, TypeAPIGatewayV2API, store.RelAttachedTo},
		EdgeDecl{TypeAPIGatewayV2Route, TypeAPIGatewayV2API, store.RelAttachedTo},
		EdgeDecl{TypeAPIGatewayV2RouteResponse, TypeAPIGatewayV2API, store.RelAttachedTo},
		EdgeDecl{TypeAPIGatewayV2Stage, TypeAPIGatewayV2API, store.RelAttachedTo},
	)
	registerResolver(
		resolveAPIGatewayV2GrandparentChildren,
		EdgeDecl{TypeAPIGatewayV2IntegrationResponse, TypeAPIGatewayV2Integration, store.RelAttachedTo},
		EdgeDecl{TypeAPIGatewayV2RouteResponse, TypeAPIGatewayV2Route, store.RelAttachedTo},
	)
	registerResolver(
		resolveAPIGatewayV2DomainChildren,
		EdgeDecl{TypeAPIGatewayBasePathMappingV2, TypeAPIGatewayDomainNameV2, store.RelAttachedTo},
		EdgeDecl{TypeAPIGatewayV2RoutingRule, TypeAPIGatewayDomainNameV2, store.RelAttachedTo},
	)
	registerResolver(
		resolveAPIGatewayV2BasePathMappingTargets,
		EdgeDecl{TypeAPIGatewayBasePathMappingV2, TypeAPIGatewayV2API, store.RelRoutesTo},
		EdgeDecl{TypeAPIGatewayBasePathMappingV2, TypeAPIGatewayV2Stage, store.RelRoutesTo},
	)
	registerResolver(
		resolveAPIGatewayV2RouteTargets,
		EdgeDecl{TypeAPIGatewayV2Route, TypeAPIGatewayV2Authorizer, store.RelUses},
		EdgeDecl{TypeAPIGatewayV2Route, TypeAPIGatewayV2Integration, store.RelRoutesTo},
	)
	registerResolver(
		resolveAPIGatewayV2IntegrationVpcLink,
		EdgeDecl{TypeAPIGatewayV2Integration, TypeAPIGatewayV2VpcLink, store.RelAttachedTo},
		EdgeDecl{TypeAPIGatewayV2Integration, TypeIAMRole, store.RelAssumes},
	)
	registerResolver(
		resolveAPIGatewayV2StageRefs,
		EdgeDecl{TypeAPIGatewayV2Stage, TypeAPIGatewayV2Deployment, store.RelUses},
	)
}

// apigatewayV2APIARNFromChild extracts the parent API ARN
// `arn:aws:apigateway:{r}::/apis/{apiId}` from any child NativeID of shape
// `…::/apis/{apiId}/<kind>/<id>[/...]`.
func apigatewayV2APIARNFromChild(arn string) string {
	const prefix = "/apis/"
	i := strings.Index(arn, prefix)
	if i < 0 {
		return ""
	}
	tail := arn[i+len(prefix):]
	end := strings.IndexByte(tail, '/')
	if end < 0 {
		return ""
	}
	return arn[:i] + prefix + tail[:end]
}

// apigatewayV2DomainARNFromChild extracts the parent domain ARN
// `arn:aws:apigateway:{r}::/domainnames/{name}` from any child of shape
// `…::/domainnames/{name}/<kind>/<id>`.
func apigatewayV2DomainARNFromChild(arn string) string {
	const prefix = "/domainnames/"
	i := strings.Index(arn, prefix)
	if i < 0 {
		return ""
	}
	tail := arn[i+len(prefix):]
	end := strings.IndexByte(tail, '/')
	if end < 0 {
		return ""
	}
	return arn[:i] + prefix + tail[:end]
}

// apigatewayV2GrandparentARN strips trailing `/<kind>/<id>` from a grandchild
// (integration-response, route-response) to recover its parent integration or
// route ARN.
func apigatewayV2GrandparentARN(arn string) string {
	last := strings.LastIndexByte(arn, '/')
	if last < 0 {
		return ""
	}
	mid := strings.LastIndexByte(arn[:last], '/')
	if mid < 0 {
		return ""
	}
	return arn[:mid]
}

func resolveAPIGatewayV2APIChildren(acct *account, st *store.Store) error {
	apiSet, err := scannedIDSet(acct, st, TypeAPIGatewayV2API)
	if err != nil {
		return err
	}
	childTypes := []string{
		TypeAPIGatewayV2Authorizer,
		TypeAPIGatewayV2Deployment,
		TypeAPIGatewayV2Integration,
		TypeAPIGatewayV2IntegrationResponse,
		TypeAPIGatewayV2Model,
		TypeAPIGatewayV2Route,
		TypeAPIGatewayV2RouteResponse,
		TypeAPIGatewayV2Stage,
	}
	for _, ctype := range childTypes {
		rows, err := st.ListResources(store.ResourceFilter{
			Provider: "aws", AccountID: acct.ID, Types: []string{ctype}, Limit: util.AllResources,
		})
		if err != nil {
			return err
		}
		for _, r := range rows {
			parent := apigatewayV2APIARNFromChild(r.NativeID)
			if parent == "" {
				continue
			}
			tgtID := store.ResourceID("aws", acct.ID, TypeAPIGatewayV2API, parent)
			if !apiSet[tgtID] {
				continue
			}
			if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
				return fmt.Errorf("upsert apigatewayv2 %s→api: %w", ctype, err)
			}
		}
	}
	return nil
}

func resolveAPIGatewayV2GrandparentChildren(acct *account, st *store.Store) error {
	if err := resolveAPIGatewayV2GrandparentOne(acct, st, TypeAPIGatewayV2IntegrationResponse, TypeAPIGatewayV2Integration); err != nil {
		return err
	}
	return resolveAPIGatewayV2GrandparentOne(acct, st, TypeAPIGatewayV2RouteResponse, TypeAPIGatewayV2Route)
}

func resolveAPIGatewayV2GrandparentOne(acct *account, st *store.Store, childType, parentType string) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{childType}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	parentSet, err := scannedIDSet(acct, st, parentType)
	if err != nil {
		return err
	}
	for _, r := range rows {
		parent := apigatewayV2GrandparentARN(r.NativeID)
		if parent == "" {
			continue
		}
		tgtID := store.ResourceID("aws", acct.ID, parentType, parent)
		if !parentSet[tgtID] {
			continue
		}
		if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
			return fmt.Errorf("upsert apigatewayv2 %s→%s: %w", childType, parentType, err)
		}
	}
	return nil
}

func resolveAPIGatewayV2DomainChildren(acct *account, st *store.Store) error {
	domSet, err := scannedIDSet(acct, st, TypeAPIGatewayDomainNameV2)
	if err != nil {
		return err
	}
	childTypes := []string{TypeAPIGatewayBasePathMappingV2, TypeAPIGatewayV2RoutingRule}
	for _, ctype := range childTypes {
		rows, err := st.ListResources(store.ResourceFilter{
			Provider: "aws", AccountID: acct.ID, Types: []string{ctype}, Limit: util.AllResources,
		})
		if err != nil {
			return err
		}
		for _, r := range rows {
			parent := apigatewayV2DomainARNFromChild(r.NativeID)
			if parent == "" {
				continue
			}
			tgtID := store.ResourceID("aws", acct.ID, TypeAPIGatewayDomainNameV2, parent)
			if !domSet[tgtID] {
				continue
			}
			if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
				return fmt.Errorf("upsert apigatewayv2 %s→domain: %w", ctype, err)
			}
		}
	}
	return nil
}

// resolveAPIGatewayV2BasePathMappingTargets links each api-mapping (base-path)
// to its target API and stage by name.
func resolveAPIGatewayV2BasePathMappingTargets(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeAPIGatewayBasePathMappingV2},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	apiSet, err := scannedIDSet(acct, st, TypeAPIGatewayV2API)
	if err != nil {
		return err
	}
	stageSet, err := scannedIDSet(acct, st, TypeAPIGatewayV2Stage)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			APIID *string `json:"ApiId"`
			Stage *string `json:"Stage"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		apiID := sv(attrs.APIID)
		if apiID == "" {
			continue
		}
		region := sv(r.Region)
		apiARN := apigatewayARN(region, "apis", apiID)
		apiTgt := store.ResourceID("aws", acct.ID, TypeAPIGatewayV2API, apiARN)
		if apiSet[apiTgt] {
			if err := st.UpsertRelationship(r.ID, apiTgt, store.RelRoutesTo, "directed", nil); err != nil {
				return fmt.Errorf("upsert apigatewayv2 mapping→api: %w", err)
			}
		}
		stage := sv(attrs.Stage)
		if stage == "" {
			continue
		}
		stageARN := apigatewayARN(region, "apis", apiID, "stages", stage)
		stageTgt := store.ResourceID("aws", acct.ID, TypeAPIGatewayV2Stage, stageARN)
		if !stageSet[stageTgt] {
			continue
		}
		if err := st.UpsertRelationship(r.ID, stageTgt, store.RelRoutesTo, "directed", nil); err != nil {
			return fmt.Errorf("upsert apigatewayv2 mapping→stage: %w", err)
		}
	}
	return nil
}

// resolveAPIGatewayV2RouteTargets wires each route to its authorizer
// (AuthorizerID) and, when `Target` carries `integrations/{id}` form, the
// backing integration.
func resolveAPIGatewayV2RouteTargets(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeAPIGatewayV2Route},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	authSet, err := scannedIDSet(acct, st, TypeAPIGatewayV2Authorizer)
	if err != nil {
		return err
	}
	intSet, err := scannedIDSet(acct, st, TypeAPIGatewayV2Integration)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			AuthorizerID *string `json:"AuthorizerId"`
			Target       *string `json:"Target"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		region := sv(r.Region)
		apiARN := apigatewayV2APIARNFromChild(r.NativeID)
		if apiARN == "" {
			continue
		}
		apiID := apiARN[strings.LastIndexByte(apiARN, '/')+1:]
		if aid := sv(attrs.AuthorizerID); aid != "" {
			authARN := apigatewayARN(region, "apis", apiID, "authorizers", aid)
			tgtID := store.ResourceID("aws", acct.ID, TypeAPIGatewayV2Authorizer, authARN)
			if authSet[tgtID] {
				if err := st.UpsertRelationship(r.ID, tgtID, store.RelUses, "directed", nil); err != nil {
					return fmt.Errorf("upsert apigatewayv2 route→authorizer: %w", err)
				}
			}
		}
		if target := sv(attrs.Target); strings.HasPrefix(target, "integrations/") {
			intID := strings.TrimPrefix(target, "integrations/")
			intARN := apigatewayARN(region, "apis", apiID, "integrations", intID)
			tgtID := store.ResourceID("aws", acct.ID, TypeAPIGatewayV2Integration, intARN)
			if intSet[tgtID] {
				if err := st.UpsertRelationship(r.ID, tgtID, store.RelRoutesTo, "directed", nil); err != nil {
					return fmt.Errorf("upsert apigatewayv2 route→integration: %w", err)
				}
			}
		}
	}
	return nil
}

// resolveAPIGatewayV2IntegrationVpcLink wires VPC_LINK-typed integrations to
// their vpc-link (ConnectionID) and any CredentialsArn IAM role.
func resolveAPIGatewayV2IntegrationVpcLink(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeAPIGatewayV2Integration},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	vlSet, err := scannedIDSet(acct, st, TypeAPIGatewayV2VpcLink)
	if err != nil {
		return err
	}
	roleSet, err := scannedIDSet(acct, st, TypeIAMRole)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			ConnectionID   *string `json:"ConnectionId"`
			ConnectionType *string `json:"ConnectionType"`
			CredentialsArn *string `json:"CredentialsArn"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		region := sv(r.Region)
		if sv(attrs.ConnectionType) == "VPC_LINK" {
			if cid := sv(attrs.ConnectionID); cid != "" {
				vlARN := apigatewayARN(region, "vpclinks", cid)
				tgtID := store.ResourceID("aws", acct.ID, TypeAPIGatewayV2VpcLink, vlARN)
				if vlSet[tgtID] {
					if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
						return fmt.Errorf("upsert apigatewayv2 integ→vpc-link: %w", err)
					}
				}
			}
		}
		if role := sv(attrs.CredentialsArn); role != "" {
			tgtID := store.ResourceID("aws", acct.ID, TypeIAMRole, role)
			if roleSet[tgtID] {
				if err := st.UpsertRelationship(r.ID, tgtID, store.RelAssumes, "directed", nil); err != nil {
					return fmt.Errorf("upsert apigatewayv2 integ→role: %w", err)
				}
			}
		}
	}
	return nil
}

// resolveAPIGatewayV2StageRefs links stage to its DeploymentID.
func resolveAPIGatewayV2StageRefs(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeAPIGatewayV2Stage},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	depSet, err := scannedIDSet(acct, st, TypeAPIGatewayV2Deployment)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			DeploymentID *string `json:"DeploymentId"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		dep := sv(attrs.DeploymentID)
		if dep == "" {
			continue
		}
		region := sv(r.Region)
		apiARN := apigatewayV2APIARNFromChild(r.NativeID)
		if apiARN == "" {
			continue
		}
		apiID := apiARN[strings.LastIndexByte(apiARN, '/')+1:]
		depARN := apigatewayARN(region, "apis", apiID, "deployments", dep)
		tgtID := store.ResourceID("aws", acct.ID, TypeAPIGatewayV2Deployment, depARN)
		if !depSet[tgtID] {
			continue
		}
		if err := st.UpsertRelationship(r.ID, tgtID, store.RelUses, "directed", nil); err != nil {
			return fmt.Errorf("upsert apigatewayv2 stage→deployment: %w", err)
		}
	}
	return nil
}

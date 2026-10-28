package aws

import (
	"cmp"
	"context"
	"strings"

	"github.com/aws/aws-sdk-go-v2/service/apigatewayv2"
	apigwv2types "github.com/aws/aws-sdk-go-v2/service/apigatewayv2/types"
	"github.com/icearp/disco-cli/store"
)

// apigatewayv2ExtAPI is the per-API child listings of API Gateway v2
// (authorizers, deployments, integrations, integration responses, models,
// routes, route responses, stages).
type apigatewayv2ExtAPI interface {
	GetAuthorizers(context.Context, *apigatewayv2.GetAuthorizersInput, ...func(*apigatewayv2.Options)) (*apigatewayv2.GetAuthorizersOutput, error)
	GetDeployments(context.Context, *apigatewayv2.GetDeploymentsInput, ...func(*apigatewayv2.Options)) (*apigatewayv2.GetDeploymentsOutput, error)
	GetIntegrations(context.Context, *apigatewayv2.GetIntegrationsInput, ...func(*apigatewayv2.Options)) (*apigatewayv2.GetIntegrationsOutput, error)
	GetIntegrationResponses(context.Context, *apigatewayv2.GetIntegrationResponsesInput, ...func(*apigatewayv2.Options)) (*apigatewayv2.GetIntegrationResponsesOutput, error)
	GetModels(context.Context, *apigatewayv2.GetModelsInput, ...func(*apigatewayv2.Options)) (*apigatewayv2.GetModelsOutput, error)
	GetRoutes(context.Context, *apigatewayv2.GetRoutesInput, ...func(*apigatewayv2.Options)) (*apigatewayv2.GetRoutesOutput, error)
	GetRouteResponses(context.Context, *apigatewayv2.GetRouteResponsesInput, ...func(*apigatewayv2.Options)) (*apigatewayv2.GetRouteResponsesOutput, error)
	GetStages(context.Context, *apigatewayv2.GetStagesInput, ...func(*apigatewayv2.Options)) (*apigatewayv2.GetStagesOutput, error)
}

const (
	agv2GetAuthorizersOp          = "apigatewayv2:GetAuthorizers"
	agv2GetDeploymentsOp          = "apigatewayv2:GetDeployments"
	agv2GetIntegrationsOp         = "apigatewayv2:GetIntegrations"
	agv2GetIntegrationResponsesOp = "apigatewayv2:GetIntegrationResponses"
	agv2GetModelsOp               = "apigatewayv2:GetModels"
	agv2GetRoutesOp               = "apigatewayv2:GetRoutes"
	agv2GetRouteResponsesOp       = "apigatewayv2:GetRouteResponses"
	agv2GetStagesOp               = "apigatewayv2:GetStages"
)

// agv2ChildKey is the one string storeTokenEachParent keys an integration or
// route by: its API's id and its own, joined by "/", which strings.Cut splits
// back (agv2SplitKey). The model does not constrain the ids, but observed ids
// are alphanumeric, and an id with "/" would already break its ARN NativeID.
func agv2ChildKey(apiID, childID string) string { return apiID + "/" + childID }

func agv2SplitKey(key string) (apiID, childID string) {
	apiID, childID, _ = strings.Cut(key, "/")
	return apiID, childID
}

// scanAPIGatewayV2APIChildren stores the children of the APIs stored this
// scan. Each child op is one family that reads every API (storeTokenEachParent,
// fanoutMed at a time), and the families run concurrently on ctx itself
// (runFamilies), so no family's error cancels another or discards its counts.
// Integration and route responses are read, per integration and per route,
// after the integrations and routes are stored. Each error stays under the op
// that failed. A child listing whose API, integration or route was deleted
// since it was listed (NotFoundException) stores what it read.
func scanAPIGatewayV2APIChildren(ctx context.Context, client apigatewayv2ExtAPI, acct *account, region string, apiIDs []string, st *store.Store, scanID string) *phaseTotals {
	return runFamilies(ctx, "apigatewayv2",
		countedFamily(agv2GetAuthorizersOp, func() (int, int, error) {
			return storeTokenEachParent(ctx, st, acct, region, agv2GetAuthorizersOp, apigatewayv2NotFound, apiIDs,
				func(ctx context.Context, apiID string, token *string) ([]apigwv2types.Authorizer, *string, error) {
					out, err := client.GetAuthorizers(ctx, &apigatewayv2.GetAuthorizersInput{ApiId: &apiID, NextToken: token})
					if err != nil {
						return nil, nil, err
					}
					return out.Items, out.NextToken, nil
				},
				func(apiID string, a apigwv2types.Authorizer) (*store.Resource, string, string) {
					id := sv(a.AuthorizerId)
					if id == "" {
						return nil, "authorizers have no AuthorizerId", "api " + apiID
					}
					return agv2Row(acct, region, scanID, TypeAPIGatewayV2Authorizer, apigatewayARNIn(acct.partition(), region, "apis", apiID, "authorizers", id), sv(a.Name), a), "", ""
				})
		}),
		countedFamily(agv2GetDeploymentsOp, func() (int, int, error) {
			return storeTokenEachParent(ctx, st, acct, region, agv2GetDeploymentsOp, apigatewayv2NotFound, apiIDs,
				func(ctx context.Context, apiID string, token *string) ([]apigwv2types.Deployment, *string, error) {
					out, err := client.GetDeployments(ctx, &apigatewayv2.GetDeploymentsInput{ApiId: &apiID, NextToken: token})
					if err != nil {
						return nil, nil, err
					}
					return out.Items, out.NextToken, nil
				},
				func(apiID string, d apigwv2types.Deployment) (*store.Resource, string, string) {
					id := sv(d.DeploymentId)
					if id == "" {
						return nil, "deployments have no DeploymentId", "api " + apiID
					}
					return agv2Row(acct, region, scanID, TypeAPIGatewayV2Deployment, apigatewayARNIn(acct.partition(), region, "apis", apiID, "deployments", id), id, d), "", ""
				})
		}),
		phaseFamily(func(p *phaseTotals) {
			scanAPIGatewayV2IntegrationsAndResponses(ctx, client, acct, region, apiIDs, st, scanID, p)
		}),
		countedFamily(agv2GetModelsOp, func() (int, int, error) {
			return storeTokenEachParent(ctx, st, acct, region, agv2GetModelsOp, apigatewayv2NotFound, apiIDs,
				func(ctx context.Context, apiID string, token *string) ([]apigwv2types.Model, *string, error) {
					out, err := client.GetModels(ctx, &apigatewayv2.GetModelsInput{ApiId: &apiID, NextToken: token})
					if err != nil {
						return nil, nil, err
					}
					return out.Items, out.NextToken, nil
				},
				func(apiID string, m apigwv2types.Model) (*store.Resource, string, string) {
					id := sv(m.ModelId)
					if id == "" {
						return nil, "models have no ModelId", "api " + apiID
					}
					return agv2Row(acct, region, scanID, TypeAPIGatewayV2Model, apigatewayARNIn(acct.partition(), region, "apis", apiID, "models", id), cmp.Or(sv(m.Name), id), m), "", ""
				})
		}),
		phaseFamily(func(p *phaseTotals) {
			scanAPIGatewayV2RoutesAndResponses(ctx, client, acct, region, apiIDs, st, scanID, p)
		}),
		countedFamily(agv2GetStagesOp, func() (int, int, error) {
			return scanAPIGatewayV2Stages(ctx, client, acct, region, apiIDs, st, scanID)
		}),
	)
}

// scanAPIGatewayV2IntegrationsAndResponses stores the APIs' integrations,
// then the responses of each integration it stored. Counts and errors go to
// p, each error under its own op; a failed write stops it.
func scanAPIGatewayV2IntegrationsAndResponses(ctx context.Context, client apigatewayv2ExtAPI, acct *account, region string, apiIDs []string, st *store.Store, scanID string, p *phaseTotals) {
	var integrations []string
	total, inserted, err := storeTokenEachParent(ctx, st, acct, region, agv2GetIntegrationsOp, apigatewayv2NotFound, apiIDs,
		func(ctx context.Context, apiID string, token *string) ([]apigwv2types.Integration, *string, error) {
			out, err := client.GetIntegrations(ctx, &apigatewayv2.GetIntegrationsInput{ApiId: &apiID, NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.Items, out.NextToken, nil
		},
		func(apiID string, it apigwv2types.Integration) (*store.Resource, string, string) {
			id := sv(it.IntegrationId)
			if id == "" {
				return nil, "integrations have no IntegrationId", "api " + apiID
			}
			integrations = append(integrations, agv2ChildKey(apiID, id))
			return agv2Row(acct, region, scanID, TypeAPIGatewayV2Integration, apigatewayARNIn(acct.partition(), region, "apis", apiID, "integrations", id), id, it), "", ""
		})
	if p.addPhase(agv2GetIntegrationsOp, total, inserted, err) {
		return
	}
	total, inserted, err = storeTokenEachParent(ctx, st, acct, region, agv2GetIntegrationResponsesOp, apigatewayv2NotFound, integrations,
		func(ctx context.Context, key string, token *string) ([]apigwv2types.IntegrationResponse, *string, error) {
			apiID, intID := agv2SplitKey(key)
			out, err := client.GetIntegrationResponses(ctx, &apigatewayv2.GetIntegrationResponsesInput{ApiId: &apiID, IntegrationId: &intID, NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.Items, out.NextToken, nil
		},
		func(key string, ir apigwv2types.IntegrationResponse) (*store.Resource, string, string) {
			id := sv(ir.IntegrationResponseId)
			if id == "" {
				return nil, "integration responses have no IntegrationResponseId", "integration " + key
			}
			apiID, intID := agv2SplitKey(key)
			nativeID := apigatewayARNIn(acct.partition(), region, "apis", apiID, "integrations", intID, "integrationresponses", id)
			return agv2Row(acct, region, scanID, TypeAPIGatewayV2IntegrationResponse, nativeID, cmp.Or(sv(ir.IntegrationResponseKey), id), ir), "", ""
		})
	p.addPhase(agv2GetIntegrationResponsesOp, total, inserted, err)
}

// scanAPIGatewayV2RoutesAndResponses stores the APIs' routes, then the
// responses of each route it stored. Counts and errors go to p, each error
// under its own op; a failed write stops it.
func scanAPIGatewayV2RoutesAndResponses(ctx context.Context, client apigatewayv2ExtAPI, acct *account, region string, apiIDs []string, st *store.Store, scanID string, p *phaseTotals) {
	var routes []string
	total, inserted, err := storeTokenEachParent(ctx, st, acct, region, agv2GetRoutesOp, apigatewayv2NotFound, apiIDs,
		func(ctx context.Context, apiID string, token *string) ([]apigwv2types.Route, *string, error) {
			out, err := client.GetRoutes(ctx, &apigatewayv2.GetRoutesInput{ApiId: &apiID, NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.Items, out.NextToken, nil
		},
		func(apiID string, r apigwv2types.Route) (*store.Resource, string, string) {
			id := sv(r.RouteId)
			if id == "" {
				return nil, "routes have no RouteId", "api " + apiID
			}
			routes = append(routes, agv2ChildKey(apiID, id))
			return agv2Row(acct, region, scanID, TypeAPIGatewayV2Route, apigatewayARNIn(acct.partition(), region, "apis", apiID, "routes", id), cmp.Or(sv(r.RouteKey), id), r), "", ""
		})
	if p.addPhase(agv2GetRoutesOp, total, inserted, err) {
		return
	}
	total, inserted, err = storeTokenEachParent(ctx, st, acct, region, agv2GetRouteResponsesOp, apigatewayv2NotFound, routes,
		func(ctx context.Context, key string, token *string) ([]apigwv2types.RouteResponse, *string, error) {
			apiID, routeID := agv2SplitKey(key)
			out, err := client.GetRouteResponses(ctx, &apigatewayv2.GetRouteResponsesInput{ApiId: &apiID, RouteId: &routeID, NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.Items, out.NextToken, nil
		},
		func(key string, rr apigwv2types.RouteResponse) (*store.Resource, string, string) {
			id := sv(rr.RouteResponseId)
			if id == "" {
				return nil, "route responses have no RouteResponseId", "route " + key
			}
			apiID, routeID := agv2SplitKey(key)
			nativeID := apigatewayARNIn(acct.partition(), region, "apis", apiID, "routes", routeID, "routeresponses", id)
			return agv2Row(acct, region, scanID, TypeAPIGatewayV2RouteResponse, nativeID, cmp.Or(sv(rr.RouteResponseKey), id), rr), "", ""
		})
	p.addPhase(agv2GetRouteResponsesOp, total, inserted, err)
}

// scanAPIGatewayV2Stages stores the APIs' stages with the tags each listed
// Stage carries, so no GetTags call is made.
func scanAPIGatewayV2Stages(ctx context.Context, client apigatewayv2ExtAPI, acct *account, region string, apiIDs []string, st *store.Store, scanID string) (int, int, error) {
	return storeTokenEachParent(ctx, st, acct, region, agv2GetStagesOp, apigatewayv2NotFound, apiIDs,
		func(ctx context.Context, apiID string, token *string) ([]apigwv2types.Stage, *string, error) {
			out, err := client.GetStages(ctx, &apigatewayv2.GetStagesInput{ApiId: &apiID, NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.Items, out.NextToken, nil
		},
		func(apiID string, s apigwv2types.Stage) (*store.Resource, string, string) {
			name := sv(s.StageName)
			if name == "" {
				return nil, "stages have no StageName", "api " + apiID
			}
			r := agv2Row(acct, region, scanID, TypeAPIGatewayV2Stage, apigatewayARNIn(acct.partition(), region, "apis", apiID, "stages", name), name, s)
			r.TagsJSON = mapTagsJSON(s.Tags)
			return r, "", ""
		})
}

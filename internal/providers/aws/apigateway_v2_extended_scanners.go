package aws

import (
	"context"
	"fmt"
	"sync/atomic"

	"github.com/aws/aws-sdk-go-v2/service/apigatewayv2"
	"github.com/icearp/disco-cli/store"
	"golang.org/x/sync/errgroup"
)

// apigatewayv2ExtAPI lists the API Gateway v2 ops used by the per-API and
// per-domain extended scanners (deployments, integrations, integration
// responses, models, routes, route responses, stages, routing rules). All ops
// use manual NextToken loops — no SDK paginator helpers are exposed.
type apigatewayv2ExtAPI interface {
	GetDeployments(context.Context, *apigatewayv2.GetDeploymentsInput, ...func(*apigatewayv2.Options)) (*apigatewayv2.GetDeploymentsOutput, error)
	GetIntegrations(context.Context, *apigatewayv2.GetIntegrationsInput, ...func(*apigatewayv2.Options)) (*apigatewayv2.GetIntegrationsOutput, error)
	GetIntegrationResponses(context.Context, *apigatewayv2.GetIntegrationResponsesInput, ...func(*apigatewayv2.Options)) (*apigatewayv2.GetIntegrationResponsesOutput, error)
	GetModels(context.Context, *apigatewayv2.GetModelsInput, ...func(*apigatewayv2.Options)) (*apigatewayv2.GetModelsOutput, error)
	GetRoutes(context.Context, *apigatewayv2.GetRoutesInput, ...func(*apigatewayv2.Options)) (*apigatewayv2.GetRoutesOutput, error)
	GetRouteResponses(context.Context, *apigatewayv2.GetRouteResponsesInput, ...func(*apigatewayv2.Options)) (*apigatewayv2.GetRouteResponsesOutput, error)
	GetStages(context.Context, *apigatewayv2.GetStagesInput, ...func(*apigatewayv2.Options)) (*apigatewayv2.GetStagesOutput, error)
	ListRoutingRules(context.Context, *apigatewayv2.ListRoutingRulesInput, ...func(*apigatewayv2.Options)) (*apigatewayv2.ListRoutingRulesOutput, error)
}

// scanAPIGatewayV2APIChildren fans out per-API child scans (deployments,
// integrations, models, routes, stages) for a single v2 API. Integration
// responses fan out per-integration; route responses per-route.
func scanAPIGatewayV2APIChildren(ctx context.Context, client apigatewayv2ExtAPI, acct *account, region, apiID string, st *store.Store, scanID string) (total, inserted int, err error) {
	var t, ni atomic.Int64
	eg, egCtx := errgroup.WithContext(ctx)

	eg.Go(func() error {
		tt, nn, e := scanAPIGatewayV2Deployments(egCtx, client, acct, region, apiID, st, scanID)
		t.Add(int64(tt))
		ni.Add(int64(nn))
		return e
	})
	eg.Go(func() error {
		tt, nn, e := scanAPIGatewayV2IntegrationsAndResponses(egCtx, client, acct, region, apiID, st, scanID)
		t.Add(int64(tt))
		ni.Add(int64(nn))
		return e
	})
	eg.Go(func() error {
		tt, nn, e := scanAPIGatewayV2Models(egCtx, client, acct, region, apiID, st, scanID)
		t.Add(int64(tt))
		ni.Add(int64(nn))
		return e
	})
	eg.Go(func() error {
		tt, nn, e := scanAPIGatewayV2RoutesAndResponses(egCtx, client, acct, region, apiID, st, scanID)
		t.Add(int64(tt))
		ni.Add(int64(nn))
		return e
	})
	eg.Go(func() error {
		tt, nn, e := scanAPIGatewayV2Stages(egCtx, client, acct, region, apiID, st, scanID)
		t.Add(int64(tt))
		ni.Add(int64(nn))
		return e
	})

	if err := eg.Wait(); err != nil {
		return 0, 0, err
	}
	return int(t.Load()), int(ni.Load()), nil
}

func scanAPIGatewayV2Deployments(ctx context.Context, client apigatewayv2ExtAPI, acct *account, region, apiID string, st *store.Store, scanID string) (int, int, error) {
	input := &apigatewayv2.GetDeploymentsInput{ApiId: &apiID}
	var batch []*store.Resource
	for {
		page, err := client.GetDeployments(ctx, input)
		if err != nil {
			if isAccessDenied(err) {
				_ = skipIfAccessDenied(st, "apigatewayv2:GetDeployments", acct.ID, region, err)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("apigatewayv2:GetDeployments(%s): %w", apiID, err)
		}
		for _, d := range page.Items {
			id := sv(d.DeploymentId)
			if id == "" {
				continue
			}
			arn := apigatewayARN(region, "apis", apiID, "deployments", id)
			label := id
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeAPIGatewayV2Deployment, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(d), DiscoveredBy: scanID,
			})
		}
		if page.NextToken == nil {
			break
		}
		input.NextToken = page.NextToken
	}
	return upsertBatch(st, batch, "apigatewayv2 deployments")
}

func scanAPIGatewayV2IntegrationsAndResponses(ctx context.Context, client apigatewayv2ExtAPI, acct *account, region, apiID string, st *store.Store, scanID string) (int, int, error) {
	input := &apigatewayv2.GetIntegrationsInput{ApiId: &apiID}
	var batch []*store.Resource
	var intIDs []string
	for {
		page, err := client.GetIntegrations(ctx, input)
		if err != nil {
			if isAccessDenied(err) {
				_ = skipIfAccessDenied(st, "apigatewayv2:GetIntegrations", acct.ID, region, err)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("apigatewayv2:GetIntegrations(%s): %w", apiID, err)
		}
		for _, integration := range page.Items {
			id := sv(integration.IntegrationId)
			if id == "" {
				continue
			}
			arn := apigatewayARN(region, "apis", apiID, "integrations", id)
			label := id
			intIDs = append(intIDs, id)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeAPIGatewayV2Integration, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(integration), DiscoveredBy: scanID,
			})
		}
		if page.NextToken == nil {
			break
		}
		input.NextToken = page.NextToken
	}
	t, i, err := upsertBatch(st, batch, "apigatewayv2 integrations")
	if err != nil {
		return t, i, err
	}
	for _, intID := range intIDs {
		tt, nn, ferr := scanAPIGatewayV2IntegrationResponses(ctx, client, acct, region, apiID, intID, st, scanID)
		if ferr != nil {
			return t, i, ferr
		}
		t += tt
		i += nn
	}
	return t, i, nil
}

func scanAPIGatewayV2IntegrationResponses(ctx context.Context, client apigatewayv2ExtAPI, acct *account, region, apiID, intID string, st *store.Store, scanID string) (int, int, error) {
	input := &apigatewayv2.GetIntegrationResponsesInput{ApiId: &apiID, IntegrationId: &intID}
	var batch []*store.Resource
	for {
		page, err := client.GetIntegrationResponses(ctx, input)
		if err != nil {
			if isAccessDenied(err) {
				_ = skipIfAccessDenied(st, "apigatewayv2:GetIntegrationResponses", acct.ID, region, err)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("apigatewayv2:GetIntegrationResponses(%s/%s): %w", apiID, intID, err)
		}
		for _, ir := range page.Items {
			id := sv(ir.IntegrationResponseId)
			if id == "" {
				continue
			}
			arn := apigatewayARN(region, "apis", apiID, "integrations", intID, "integrationresponses", id)
			label := sv(ir.IntegrationResponseKey)
			if label == "" {
				label = id
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeAPIGatewayV2IntegrationResponse, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(ir), DiscoveredBy: scanID,
			})
		}
		if page.NextToken == nil {
			break
		}
		input.NextToken = page.NextToken
	}
	return upsertBatch(st, batch, "apigatewayv2 integration-responses")
}

func scanAPIGatewayV2Models(ctx context.Context, client apigatewayv2ExtAPI, acct *account, region, apiID string, st *store.Store, scanID string) (int, int, error) {
	input := &apigatewayv2.GetModelsInput{ApiId: &apiID}
	var batch []*store.Resource
	for {
		page, err := client.GetModels(ctx, input)
		if err != nil {
			if isAccessDenied(err) {
				_ = skipIfAccessDenied(st, "apigatewayv2:GetModels", acct.ID, region, err)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("apigatewayv2:GetModels(%s): %w", apiID, err)
		}
		for _, m := range page.Items {
			id := sv(m.ModelId)
			if id == "" {
				continue
			}
			arn := apigatewayARN(region, "apis", apiID, "models", id)
			label := sv(m.Name)
			if label == "" {
				label = id
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeAPIGatewayV2Model, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(m), DiscoveredBy: scanID,
			})
		}
		if page.NextToken == nil {
			break
		}
		input.NextToken = page.NextToken
	}
	return upsertBatch(st, batch, "apigatewayv2 models")
}

func scanAPIGatewayV2RoutesAndResponses(ctx context.Context, client apigatewayv2ExtAPI, acct *account, region, apiID string, st *store.Store, scanID string) (int, int, error) {
	input := &apigatewayv2.GetRoutesInput{ApiId: &apiID}
	var batch []*store.Resource
	var routeIDs []string
	for {
		page, err := client.GetRoutes(ctx, input)
		if err != nil {
			if isAccessDenied(err) {
				_ = skipIfAccessDenied(st, "apigatewayv2:GetRoutes", acct.ID, region, err)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("apigatewayv2:GetRoutes(%s): %w", apiID, err)
		}
		for _, r := range page.Items {
			id := sv(r.RouteId)
			if id == "" {
				continue
			}
			arn := apigatewayARN(region, "apis", apiID, "routes", id)
			label := sv(r.RouteKey)
			if label == "" {
				label = id
			}
			routeIDs = append(routeIDs, id)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeAPIGatewayV2Route, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(r), DiscoveredBy: scanID,
			})
		}
		if page.NextToken == nil {
			break
		}
		input.NextToken = page.NextToken
	}
	t, i, err := upsertBatch(st, batch, "apigatewayv2 routes")
	if err != nil {
		return t, i, err
	}
	for _, rid := range routeIDs {
		tt, nn, ferr := scanAPIGatewayV2RouteResponses(ctx, client, acct, region, apiID, rid, st, scanID)
		if ferr != nil {
			return t, i, ferr
		}
		t += tt
		i += nn
	}
	return t, i, nil
}

func scanAPIGatewayV2RouteResponses(ctx context.Context, client apigatewayv2ExtAPI, acct *account, region, apiID, routeID string, st *store.Store, scanID string) (int, int, error) {
	input := &apigatewayv2.GetRouteResponsesInput{ApiId: &apiID, RouteId: &routeID}
	var batch []*store.Resource
	for {
		page, err := client.GetRouteResponses(ctx, input)
		if err != nil {
			if isAccessDenied(err) {
				_ = skipIfAccessDenied(st, "apigatewayv2:GetRouteResponses", acct.ID, region, err)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("apigatewayv2:GetRouteResponses(%s/%s): %w", apiID, routeID, err)
		}
		for _, rr := range page.Items {
			id := sv(rr.RouteResponseId)
			if id == "" {
				continue
			}
			arn := apigatewayARN(region, "apis", apiID, "routes", routeID, "routeresponses", id)
			label := sv(rr.RouteResponseKey)
			if label == "" {
				label = id
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeAPIGatewayV2RouteResponse, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(rr), DiscoveredBy: scanID,
			})
		}
		if page.NextToken == nil {
			break
		}
		input.NextToken = page.NextToken
	}
	return upsertBatch(st, batch, "apigatewayv2 route-responses")
}

func scanAPIGatewayV2Stages(ctx context.Context, client apigatewayv2ExtAPI, acct *account, region, apiID string, st *store.Store, scanID string) (int, int, error) {
	input := &apigatewayv2.GetStagesInput{ApiId: &apiID}
	var batch []*store.Resource
	for {
		page, err := client.GetStages(ctx, input)
		if err != nil {
			if isAccessDenied(err) {
				_ = skipIfAccessDenied(st, "apigatewayv2:GetStages", acct.ID, region, err)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("apigatewayv2:GetStages(%s): %w", apiID, err)
		}
		for _, s := range page.Items {
			name := sv(s.StageName)
			if name == "" {
				continue
			}
			arn := apigatewayARN(region, "apis", apiID, "stages", name)
			label := name
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeAPIGatewayV2Stage, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(s), DiscoveredBy: scanID,
			})
		}
		if page.NextToken == nil {
			break
		}
		input.NextToken = page.NextToken
	}
	return upsertBatch(st, batch, "apigatewayv2 stages")
}

// scanAPIGatewayV2RoutingRules discovers routing rules under one custom domain.
// NativeID = RoutingRuleArn (SDK supplies it on every entry).
func scanAPIGatewayV2RoutingRules(ctx context.Context, client apigatewayv2ExtAPI, acct *account, region, domainName string, st *store.Store, scanID string) (int, int, error) {
	input := &apigatewayv2.ListRoutingRulesInput{DomainName: &domainName}
	var batch []*store.Resource
	for {
		page, err := client.ListRoutingRules(ctx, input)
		if err != nil {
			if isAccessDenied(err) {
				_ = skipIfAccessDenied(st, "apigatewayv2:ListRoutingRules", acct.ID, region, err)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("apigatewayv2:ListRoutingRules(%s): %w", domainName, err)
		}
		for _, rr := range page.RoutingRules {
			arn := sv(rr.RoutingRuleArn)
			if arn == "" {
				continue
			}
			label := arn
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeAPIGatewayV2RoutingRule, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(rr), DiscoveredBy: scanID,
			})
		}
		if page.NextToken == nil {
			break
		}
		input.NextToken = page.NextToken
	}
	return upsertBatch(st, batch, "apigatewayv2 routing-rules")
}

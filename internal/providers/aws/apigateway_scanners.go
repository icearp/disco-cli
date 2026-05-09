package aws

import (
	"context"
	"fmt"
	"slices"
	"sync/atomic"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/store"
	"github.com/aws/aws-sdk-go-v2/service/apigateway"
	apigatewaytypes "github.com/aws/aws-sdk-go-v2/service/apigateway/types"
	"golang.org/x/sync/errgroup"
)

// apigatewayAPI is the narrow set of API Gateway v1 (REST) operations
// called by the scanAPIGateway sub-phases.
type apigatewayAPI interface {
	GetRestApis(context.Context, *apigateway.GetRestApisInput, ...func(*apigateway.Options)) (*apigateway.GetRestApisOutput, error)
	GetAccount(context.Context, *apigateway.GetAccountInput, ...func(*apigateway.Options)) (*apigateway.GetAccountOutput, error)
	GetAuthorizers(context.Context, *apigateway.GetAuthorizersInput, ...func(*apigateway.Options)) (*apigateway.GetAuthorizersOutput, error)
	GetDeployments(context.Context, *apigateway.GetDeploymentsInput, ...func(*apigateway.Options)) (*apigateway.GetDeploymentsOutput, error)
	GetStages(context.Context, *apigateway.GetStagesInput, ...func(*apigateway.Options)) (*apigateway.GetStagesOutput, error)
	GetResources(context.Context, *apigateway.GetResourcesInput, ...func(*apigateway.Options)) (*apigateway.GetResourcesOutput, error)
	GetModels(context.Context, *apigateway.GetModelsInput, ...func(*apigateway.Options)) (*apigateway.GetModelsOutput, error)
	GetRequestValidators(context.Context, *apigateway.GetRequestValidatorsInput, ...func(*apigateway.Options)) (*apigateway.GetRequestValidatorsOutput, error)
	GetGatewayResponses(context.Context, *apigateway.GetGatewayResponsesInput, ...func(*apigateway.Options)) (*apigateway.GetGatewayResponsesOutput, error)
	GetDocumentationParts(context.Context, *apigateway.GetDocumentationPartsInput, ...func(*apigateway.Options)) (*apigateway.GetDocumentationPartsOutput, error)
	GetDocumentationVersions(context.Context, *apigateway.GetDocumentationVersionsInput, ...func(*apigateway.Options)) (*apigateway.GetDocumentationVersionsOutput, error)
	GetApiKeys(context.Context, *apigateway.GetApiKeysInput, ...func(*apigateway.Options)) (*apigateway.GetApiKeysOutput, error)
	GetUsagePlans(context.Context, *apigateway.GetUsagePlansInput, ...func(*apigateway.Options)) (*apigateway.GetUsagePlansOutput, error)
	GetUsagePlanKeys(context.Context, *apigateway.GetUsagePlanKeysInput, ...func(*apigateway.Options)) (*apigateway.GetUsagePlanKeysOutput, error)
	GetClientCertificates(context.Context, *apigateway.GetClientCertificatesInput, ...func(*apigateway.Options)) (*apigateway.GetClientCertificatesOutput, error)
	GetDomainNames(context.Context, *apigateway.GetDomainNamesInput, ...func(*apigateway.Options)) (*apigateway.GetDomainNamesOutput, error)
	GetDomainNameAccessAssociations(context.Context, *apigateway.GetDomainNameAccessAssociationsInput, ...func(*apigateway.Options)) (*apigateway.GetDomainNameAccessAssociationsOutput, error)
	GetBasePathMappings(context.Context, *apigateway.GetBasePathMappingsInput, ...func(*apigateway.Options)) (*apigateway.GetBasePathMappingsOutput, error)
	GetVpcLinks(context.Context, *apigateway.GetVpcLinksInput, ...func(*apigateway.Options)) (*apigateway.GetVpcLinksOutput, error)
}

func init() {
	registerService(serviceEntry{
		name: "aws:apigateway",
		fn:   scanAPIGateway,
		emits: []coverage.TypeDecl{
			{Service: "apigateway", DiscoType: TypeAPIGatewayAccount, Leaf: true},
			{Service: "apigateway", DiscoType: TypeAPIGatewayAPIKey, Leaf: true},
			{Service: "apigateway", DiscoType: TypeAPIGatewayAuthorizer},
			{Service: "apigateway", DiscoType: TypeAPIGatewayBasePathMapping},
			{Service: "apigateway", DiscoType: TypeAPIGatewayClientCertificate, Leaf: true},
			{Service: "apigateway", DiscoType: TypeAPIGatewayDeployment},
			{Service: "apigateway", DiscoType: TypeAPIGatewayDocumentationPart},
			{Service: "apigateway", DiscoType: TypeAPIGatewayDocumentationVersion},
			{Service: "apigateway", DiscoType: TypeAPIGatewayDomainName},
			{Service: "apigateway", DiscoType: TypeAPIGatewayDomainNameAccessAssoc, Leaf: true},
			{Service: "apigateway", DiscoType: TypeAPIGatewayPrivateDomainName},
			{Service: "apigateway", DiscoType: TypeAPIGatewayPrivateBasePathMapping},
			{Service: "apigateway", DiscoType: TypeAPIGatewayGatewayResponse},
			{Service: "apigateway", DiscoType: TypeAPIGatewayMethod},
			{Service: "apigateway", DiscoType: TypeAPIGatewayModel},
			{Service: "apigateway", DiscoType: TypeAPIGatewayRequestValidator},
			{Service: "apigateway", DiscoType: TypeAPIGatewayResource},
			{Service: "apigateway", DiscoType: TypeAPIGatewayRestAPI},
			{Service: "apigateway", DiscoType: TypeAPIGatewayStage},
			{Service: "apigateway", DiscoType: TypeAPIGatewayUsagePlan},
			{Service: "apigateway", DiscoType: TypeAPIGatewayUsagePlanKey},
			{Service: "apigateway", DiscoType: TypeAPIGatewayVpcLink},
		},
	})
	registerService(serviceEntry{
		name: "aws:apigatewayv2",
		fn:   scanAPIGatewayV2,
		emits: []coverage.TypeDecl{
			{Service: "apigatewayv2", DiscoType: TypeAPIGatewayV2API, Leaf: true},
			{Service: "apigatewayv2", DiscoType: TypeAPIGatewayV2Authorizer},
			{Service: "apigatewayv2", DiscoType: TypeAPIGatewayDomainNameV2},
			{Service: "apigatewayv2", DiscoType: TypeAPIGatewayBasePathMappingV2},
			{Service: "apigatewayv2", DiscoType: TypeAPIGatewayV2VpcLink},
			{Service: "apigatewayv2", DiscoType: TypeAPIGatewayV2Deployment},
			{Service: "apigatewayv2", DiscoType: TypeAPIGatewayV2Integration},
			{Service: "apigatewayv2", DiscoType: TypeAPIGatewayV2IntegrationResponse},
			{Service: "apigatewayv2", DiscoType: TypeAPIGatewayV2Model},
			{Service: "apigatewayv2", DiscoType: TypeAPIGatewayV2Route},
			{Service: "apigatewayv2", DiscoType: TypeAPIGatewayV2RouteResponse},
			{Service: "apigatewayv2", DiscoType: TypeAPIGatewayV2Stage},
			{Service: "apigatewayv2", DiscoType: TypeAPIGatewayV2RoutingRule},
		},
	})
}

// scanAPIGateway is the orchestrator for all API Gateway v1 (REST) resource types.
// It runs all sub-scanners concurrently and aggregates their counts.
func scanAPIGateway(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	return runScanners(
		ctx,
		func(ctx context.Context) (int, int, error) {
			return scanAPIGatewayREST(ctx, acct, region, st, scanID)
		},
		func(ctx context.Context) (int, int, error) {
			return scanAPIGatewayAccount(ctx, acct, region, st, scanID)
		},
		func(ctx context.Context) (int, int, error) {
			return scanAPIGatewayAPIKeys(ctx, acct, region, st, scanID)
		},
		func(ctx context.Context) (int, int, error) {
			return scanAPIGatewayClientCertificates(ctx, acct, region, st, scanID)
		},
		func(ctx context.Context) (int, int, error) {
			return scanAPIGatewayDomainNames(ctx, acct, region, st, scanID)
		},
		func(ctx context.Context) (int, int, error) {
			return scanAPIGatewayUsagePlans(ctx, acct, region, st, scanID)
		},
		func(ctx context.Context) (int, int, error) {
			return scanAPIGatewayVPCLinks(ctx, acct, region, st, scanID)
		},
	)
}

// restAPISummary holds the minimal info needed to fan out per-API sub-scans.
type restAPISummary struct {
	id string
}

// scanAPIGatewayREST discovers REST APIs (API Gateway v1) then fans out to scan
// all per-API child resources concurrently.
func scanAPIGatewayREST(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := apigateway.NewFromConfig(acct.cfg, func(o *apigateway.Options) { o.Region = region })

	// 1. Page through all REST APIs, collect IDs and upsert resources.
	pager := apigateway.NewGetRestApisPaginator(client, &apigateway.GetRestApisInput{})
	var apis []restAPISummary
	var batch []*store.Resource
	for pager.HasMorePages() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return total, inserted, skipIfAccessDenied(st, "apigateway:GetRestApis", acct.ID, region, err)
			}
			return total, inserted, fmt.Errorf("apigateway:GetRestApis: %w", err)
		}
		for _, api := range page.Items {
			nativeID := apigatewayARN(region, "restapis", sv(api.Id))
			name := sv(api.Name)
			batch = append(batch, &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeAPIGatewayRestAPI,
				NativeID:       nativeID,
				Name:           &name,
				Region:         &region,
				AttributesJSON: mustJSON(api),
				TagsJSON:       mapTagsJSON(api.Tags),
				DiscoveredBy:   scanID,
			})
			apis = append(apis, restAPISummary{id: sv(api.Id)})
		}
	}
	if len(batch) > 0 {
		n, err := st.UpsertResources(batch)
		if err != nil {
			return total, inserted, fmt.Errorf("upsert REST APIs (%s): %w", region, err)
		}
		total += len(batch)
		inserted += n
	}

	// 2. Fan out child resource scans for each REST API concurrently.
	var t, ni atomic.Int64
	add := func(tt, nn int) { t.Add(int64(tt)); ni.Add(int64(nn)) }
	eg, egCtx := errgroup.WithContext(ctx)
	for _, a := range apis {
		eg.Go(func() error {
			tt, nn, e := scanAPIGatewayPerAPI(egCtx, client, acct, region, a.id, st, scanID)
			add(tt, nn)
			return e
		})
	}
	if err := eg.Wait(); err != nil {
		return total, inserted, err
	}
	total += int(t.Load())
	inserted += int(ni.Load())
	return
}

// scanAPIGatewayPerAPI scans all child resources of a single REST API concurrently.
func scanAPIGatewayPerAPI(ctx context.Context, client apigatewayAPI, acct *account, region, apiID string, st *store.Store, scanID string) (total, inserted int, err error) {
	return runScanners(
		ctx,
		func(ctx context.Context) (int, int, error) {
			return scanAPIGatewayAuthorizers(ctx, client, acct, region, apiID, st, scanID)
		},
		func(ctx context.Context) (int, int, error) {
			return scanAPIGatewayDeployments(ctx, client, acct, region, apiID, st, scanID)
		},
		func(ctx context.Context) (int, int, error) {
			return scanAPIGatewayStages(ctx, client, acct, region, apiID, st, scanID)
		},
		func(ctx context.Context) (int, int, error) {
			return scanAPIGatewayResources(ctx, client, acct, region, apiID, st, scanID)
		},
		func(ctx context.Context) (int, int, error) {
			return scanAPIGatewayModels(ctx, client, acct, region, apiID, st, scanID)
		},
		func(ctx context.Context) (int, int, error) {
			return scanAPIGatewayRequestValidators(ctx, client, acct, region, apiID, st, scanID)
		},
		func(ctx context.Context) (int, int, error) {
			return scanAPIGatewayGatewayResponses(ctx, client, acct, region, apiID, st, scanID)
		},
		func(ctx context.Context) (int, int, error) {
			return scanAPIGatewayDocumentationParts(ctx, client, acct, region, apiID, st, scanID)
		},
		func(ctx context.Context) (int, int, error) {
			return scanAPIGatewayDocumentationVersions(ctx, client, acct, region, apiID, st, scanID)
		},
	)
}

// scanAPIGatewayAuthorizers discovers authorizers for a single REST API.
// ARN format: arn:aws:apigateway:{region}::/restapis/{apiId}/authorizers/{authorizerId}
func scanAPIGatewayAuthorizers(ctx context.Context, client apigatewayAPI, acct *account, region, apiID string, st *store.Store, scanID string) (total, inserted int, err error) {
	out, err := client.GetAuthorizers(ctx, &apigateway.GetAuthorizersInput{RestApiId: &apiID})
	if err != nil {
		if isAccessDenied(err) {
			return 0, 0, skipIfAccessDenied(st, "apigateway:GetAuthorizers", acct.ID, region, err)
		}
		return 0, 0, fmt.Errorf("apigateway:GetAuthorizers(%s): %w", apiID, err)
	}
	var batch []*store.Resource
	for _, item := range out.Items {
		nativeID := apigatewayARN(region, "restapis", apiID, "authorizers", sv(item.Id))
		name := sv(item.Name)
		batch = append(batch, &store.Resource{
			Provider:       "aws",
			AccountID:      acct.ID,
			AccountName:    &acct.Name,
			Type:           TypeAPIGatewayAuthorizer,
			NativeID:       nativeID,
			Name:           &name,
			Region:         &region,
			AttributesJSON: mustJSON(item),
			DiscoveredBy:   scanID,
		})
	}
	if len(batch) > 0 {
		n, err := st.UpsertResources(batch)
		if err != nil {
			return 0, 0, fmt.Errorf("upsert authorizers (%s/%s): %w", region, apiID, err)
		}
		total = len(batch)
		inserted = n
	}
	return
}

// scanAPIGatewayDeployments discovers deployments for a single REST API.
// ARN format: arn:aws:apigateway:{region}::/restapis/{apiId}/deployments/{deploymentId}
func scanAPIGatewayDeployments(ctx context.Context, client apigatewayAPI, acct *account, region, apiID string, st *store.Store, scanID string) (total, inserted int, err error) {
	pager := apigateway.NewGetDeploymentsPaginator(client, &apigateway.GetDeploymentsInput{RestApiId: &apiID})
	var batch []*store.Resource
	for pager.HasMorePages() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return total, inserted, skipIfAccessDenied(st, "apigateway:GetDeployments", acct.ID, region, err)
			}
			return total, inserted, fmt.Errorf("apigateway:GetDeployments(%s): %w", apiID, err)
		}
		for _, item := range page.Items {
			nativeID := apigatewayARN(region, "restapis", apiID, "deployments", sv(item.Id))
			desc := sv(item.Description)
			batch = append(batch, &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeAPIGatewayDeployment,
				NativeID:       nativeID,
				Name:           &desc,
				Region:         &region,
				AttributesJSON: mustJSON(item),
				DiscoveredBy:   scanID,
			})
		}
	}
	if len(batch) > 0 {
		n, err := st.UpsertResources(batch)
		if err != nil {
			return total, inserted, fmt.Errorf("upsert deployments (%s/%s): %w", region, apiID, err)
		}
		total = len(batch)
		inserted = n
	}
	return
}

// scanAPIGatewayStages discovers stages for a single REST API.
// ARN format: arn:aws:apigateway:{region}::/restapis/{apiId}/stages/{stageName}
func scanAPIGatewayStages(ctx context.Context, client apigatewayAPI, acct *account, region, apiID string, st *store.Store, scanID string) (total, inserted int, err error) {
	out, err := client.GetStages(ctx, &apigateway.GetStagesInput{RestApiId: &apiID})
	if err != nil {
		if isAccessDenied(err) {
			return 0, 0, skipIfAccessDenied(st, "apigateway:GetStages", acct.ID, region, err)
		}
		return 0, 0, fmt.Errorf("apigateway:GetStages(%s): %w", apiID, err)
	}
	var batch []*store.Resource
	for _, item := range out.Item {
		stageName := sv(item.StageName)
		nativeID := apigatewayARN(region, "restapis", apiID, "stages", stageName)
		batch = append(batch, &store.Resource{
			Provider:       "aws",
			AccountID:      acct.ID,
			AccountName:    &acct.Name,
			Type:           TypeAPIGatewayStage,
			NativeID:       nativeID,
			Name:           &stageName,
			Region:         &region,
			AttributesJSON: mustJSON(item),
			TagsJSON:       mapTagsJSON(item.Tags),
			DiscoveredBy:   scanID,
		})
	}
	if len(batch) > 0 {
		n, err := st.UpsertResources(batch)
		if err != nil {
			return 0, 0, fmt.Errorf("upsert stages (%s/%s): %w", region, apiID, err)
		}
		total = len(batch)
		inserted = n
	}
	return
}

// scanAPIGatewayResources discovers resources and their methods for a single REST API.
// Resources: arn:aws:apigateway:{region}::/restapis/{apiId}/resources/{resourceId}
// Methods:   arn:aws:apigateway:{region}::/restapis/{apiId}/resources/{resourceId}/methods/{httpMethod}
func scanAPIGatewayResources(ctx context.Context, client apigatewayAPI, acct *account, region, apiID string, st *store.Store, scanID string) (total, inserted int, err error) {
	// embed=methods includes the ResourceMethods map in each Resource, avoiding N+1.
	pager := apigateway.NewGetResourcesPaginator(client, &apigateway.GetResourcesInput{
		RestApiId: &apiID,
		Embed:     []string{"methods"},
	})
	var resources []*store.Resource
	var methods []*store.Resource
	for pager.HasMorePages() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return total, inserted, skipIfAccessDenied(st, "apigateway:GetResources", acct.ID, region, err)
			}
			return total, inserted, fmt.Errorf("apigateway:GetResources(%s): %w", apiID, err)
		}
		for _, item := range page.Items {
			resourceID := sv(item.Id)
			path := sv(item.Path)
			resourceNativeID := apigatewayARN(region, "restapis", apiID, "resources", resourceID)
			resources = append(resources, &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeAPIGatewayResource,
				NativeID:       resourceNativeID,
				Name:           &path,
				Region:         &region,
				AttributesJSON: mustJSON(item),
				DiscoveredBy:   scanID,
			})
			// Emit one method resource per HTTP verb embedded in this resource.
			for httpMethod, method := range item.ResourceMethods {
				methodNativeID := apigatewayARN(region, "restapis", apiID, "resources", resourceID, "methods", httpMethod)
				name := fmt.Sprintf("%s %s", httpMethod, path)
				methods = append(methods, &store.Resource{
					Provider:       "aws",
					AccountID:      acct.ID,
					AccountName:    &acct.Name,
					Type:           TypeAPIGatewayMethod,
					NativeID:       methodNativeID,
					Name:           &name,
					Region:         &region,
					AttributesJSON: mustJSON(method),
					DiscoveredBy:   scanID,
				})
			}
		}
	}
	if len(resources) > 0 {
		n, err := st.UpsertResources(resources)
		if err != nil {
			return total, inserted, fmt.Errorf("upsert resources (%s/%s): %w", region, apiID, err)
		}
		total += len(resources)
		inserted += n
	}
	if len(methods) > 0 {
		n, err := st.UpsertResources(methods)
		if err != nil {
			return total, inserted, fmt.Errorf("upsert methods (%s/%s): %w", region, apiID, err)
		}
		total += len(methods)
		inserted += n
	}
	return
}

// scanAPIGatewayModels discovers models for a single REST API.
// ARN format: arn:aws:apigateway:{region}::/restapis/{apiId}/models/{modelName}
func scanAPIGatewayModels(ctx context.Context, client apigatewayAPI, acct *account, region, apiID string, st *store.Store, scanID string) (total, inserted int, err error) {
	pager := apigateway.NewGetModelsPaginator(client, &apigateway.GetModelsInput{RestApiId: &apiID})
	var batch []*store.Resource
	for pager.HasMorePages() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return total, inserted, skipIfAccessDenied(st, "apigateway:GetModels", acct.ID, region, err)
			}
			return total, inserted, fmt.Errorf("apigateway:GetModels(%s): %w", apiID, err)
		}
		for _, item := range page.Items {
			name := sv(item.Name)
			nativeID := apigatewayARN(region, "restapis", apiID, "models", name)
			batch = append(batch, &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeAPIGatewayModel,
				NativeID:       nativeID,
				Name:           &name,
				Region:         &region,
				AttributesJSON: mustJSON(item),
				DiscoveredBy:   scanID,
			})
		}
	}
	if len(batch) > 0 {
		n, err := st.UpsertResources(batch)
		if err != nil {
			return total, inserted, fmt.Errorf("upsert models (%s/%s): %w", region, apiID, err)
		}
		total = len(batch)
		inserted = n
	}
	return
}

// scanAPIGatewayRequestValidators discovers request validators for a single REST API.
// ARN format: arn:aws:apigateway:{region}::/restapis/{apiId}/requestvalidators/{validatorId}
func scanAPIGatewayRequestValidators(ctx context.Context, client apigatewayAPI, acct *account, region, apiID string, st *store.Store, scanID string) (total, inserted int, err error) {
	out, err := client.GetRequestValidators(ctx, &apigateway.GetRequestValidatorsInput{RestApiId: &apiID})
	if err != nil {
		if isAccessDenied(err) {
			return 0, 0, skipIfAccessDenied(st, "apigateway:GetRequestValidators", acct.ID, region, err)
		}
		return 0, 0, fmt.Errorf("apigateway:GetRequestValidators(%s): %w", apiID, err)
	}
	var batch []*store.Resource
	for _, item := range out.Items {
		nativeID := apigatewayARN(region, "restapis", apiID, "requestvalidators", sv(item.Id))
		name := sv(item.Name)
		batch = append(batch, &store.Resource{
			Provider:       "aws",
			AccountID:      acct.ID,
			AccountName:    &acct.Name,
			Type:           TypeAPIGatewayRequestValidator,
			NativeID:       nativeID,
			Name:           &name,
			Region:         &region,
			AttributesJSON: mustJSON(item),
			DiscoveredBy:   scanID,
		})
	}
	if len(batch) > 0 {
		n, err := st.UpsertResources(batch)
		if err != nil {
			return 0, 0, fmt.Errorf("upsert request validators (%s/%s): %w", region, apiID, err)
		}
		total = len(batch)
		inserted = n
	}
	return
}

// scanAPIGatewayGatewayResponses discovers gateway responses for a single REST API.
// ARN format: arn:aws:apigateway:{region}::/restapis/{apiId}/gatewayresponses/{responseType}
func scanAPIGatewayGatewayResponses(ctx context.Context, client apigatewayAPI, acct *account, region, apiID string, st *store.Store, scanID string) (total, inserted int, err error) {
	out, err := client.GetGatewayResponses(ctx, &apigateway.GetGatewayResponsesInput{RestApiId: &apiID})
	if err != nil {
		if isAccessDenied(err) {
			return 0, 0, skipIfAccessDenied(st, "apigateway:GetGatewayResponses", acct.ID, region, err)
		}
		return 0, 0, fmt.Errorf("apigateway:GetGatewayResponses(%s): %w", apiID, err)
	}
	var batch []*store.Resource
	for _, item := range out.Items {
		responseType := string(item.ResponseType)
		nativeID := apigatewayARN(region, "restapis", apiID, "gatewayresponses", responseType)
		batch = append(batch, &store.Resource{
			Provider:       "aws",
			AccountID:      acct.ID,
			AccountName:    &acct.Name,
			Type:           TypeAPIGatewayGatewayResponse,
			NativeID:       nativeID,
			Name:           &responseType,
			Region:         &region,
			AttributesJSON: mustJSON(item),
			DiscoveredBy:   scanID,
		})
	}
	if len(batch) > 0 {
		n, err := st.UpsertResources(batch)
		if err != nil {
			return 0, 0, fmt.Errorf("upsert gateway responses (%s/%s): %w", region, apiID, err)
		}
		total = len(batch)
		inserted = n
	}
	return
}

// scanAPIGatewayDocumentationParts discovers documentation parts for a single REST API.
// Uses manual Position-based pagination (no built-in paginator).
// ARN format: arn:aws:apigateway:{region}::/restapis/{apiId}/documentation/parts/{partId}
func scanAPIGatewayDocumentationParts(ctx context.Context, client apigatewayAPI, acct *account, region, apiID string, st *store.Store, scanID string) (total, inserted int, err error) {
	input := &apigateway.GetDocumentationPartsInput{RestApiId: &apiID}
	var batch []*store.Resource
	for {
		out, apiErr := client.GetDocumentationParts(ctx, input)
		if apiErr != nil {
			if isAccessDenied(apiErr) {
				return 0, 0, skipIfAccessDenied(st, "apigateway:GetDocumentationParts", acct.ID, region, apiErr)
			}
			return 0, 0, fmt.Errorf("apigateway:GetDocumentationParts(%s): %w", apiID, apiErr)
		}
		for _, item := range out.Items {
			nativeID := apigatewayARN(region, "restapis", apiID, "documentation", "parts", sv(item.Id))
			id := sv(item.Id)
			batch = append(batch, &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeAPIGatewayDocumentationPart,
				NativeID:       nativeID,
				Name:           &id,
				Region:         &region,
				AttributesJSON: mustJSON(item),
				DiscoveredBy:   scanID,
			})
		}
		if out.Position == nil {
			break
		}
		input.Position = out.Position
	}
	if len(batch) > 0 {
		n, err := st.UpsertResources(batch)
		if err != nil {
			return 0, 0, fmt.Errorf("upsert documentation parts (%s/%s): %w", region, apiID, err)
		}
		total = len(batch)
		inserted = n
	}
	return
}

// scanAPIGatewayDocumentationVersions discovers documentation versions for a single REST API.
// Uses manual Position-based pagination (no built-in paginator).
// ARN format: arn:aws:apigateway:{region}::/restapis/{apiId}/documentation/versions/{version}
func scanAPIGatewayDocumentationVersions(ctx context.Context, client apigatewayAPI, acct *account, region, apiID string, st *store.Store, scanID string) (total, inserted int, err error) {
	input := &apigateway.GetDocumentationVersionsInput{RestApiId: &apiID}
	var batch []*store.Resource
	for {
		out, apiErr := client.GetDocumentationVersions(ctx, input)
		if apiErr != nil {
			if isAccessDenied(apiErr) {
				return 0, 0, skipIfAccessDenied(st, "apigateway:GetDocumentationVersions", acct.ID, region, apiErr)
			}
			return 0, 0, fmt.Errorf("apigateway:GetDocumentationVersions(%s): %w", apiID, apiErr)
		}
		for _, item := range out.Items {
			version := sv(item.Version)
			nativeID := apigatewayARN(region, "restapis", apiID, "documentation", "versions", version)
			batch = append(batch, &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeAPIGatewayDocumentationVersion,
				NativeID:       nativeID,
				Name:           &version,
				Region:         &region,
				AttributesJSON: mustJSON(item),
				DiscoveredBy:   scanID,
			})
		}
		if out.Position == nil {
			break
		}
		input.Position = out.Position
	}
	if len(batch) > 0 {
		n, err := st.UpsertResources(batch)
		if err != nil {
			return 0, 0, fmt.Errorf("upsert documentation versions (%s/%s): %w", region, apiID, err)
		}
		total = len(batch)
		inserted = n
	}
	return
}

// scanAPIGatewayAccount discovers the account-level API Gateway settings (singleton per region).
// ARN format: arn:aws:apigateway:{region}::/account
func scanAPIGatewayAccount(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := apigateway.NewFromConfig(acct.cfg, func(o *apigateway.Options) { o.Region = region })
	out, err := client.GetAccount(ctx, &apigateway.GetAccountInput{})
	if err != nil {
		if isAccessDenied(err) {
			return 0, 0, skipIfAccessDenied(st, "apigateway:GetAccount", acct.ID, region, err)
		}
		return 0, 0, fmt.Errorf("apigateway:GetAccount: %w", err)
	}
	nativeID := apigatewayARN(region, "account")
	name := "account"
	n, err := st.UpsertResource(&store.Resource{
		Provider:    "aws",
		AccountID:   acct.ID,
		AccountName: &acct.Name,
		Type:        TypeAPIGatewayAccount,
		NativeID:    nativeID,
		Name:        &name,
		Region:      &region,
		// Per-region account-level CloudWatch role + throttle config singleton —
		// not a user-created resource, hardcoded name "account".
		ManagedByProvider: true,
		AttributesJSON:    mustJSON(out),
		DiscoveredBy:      scanID,
	})
	if err != nil {
		return 0, 0, err
	}
	return 1, n, nil
}

// scanAPIGatewayAPIKeys discovers API keys.
// ARN format: arn:aws:apigateway:{region}::/apikeys/{keyId}
func scanAPIGatewayAPIKeys(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := apigateway.NewFromConfig(acct.cfg, func(o *apigateway.Options) { o.Region = region })
	pager := apigateway.NewGetApiKeysPaginator(client, &apigateway.GetApiKeysInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return total, inserted, skipIfAccessDenied(st, "apigateway:GetApiKeys", acct.ID, region, err)
			}
			return total, inserted, fmt.Errorf("apigateway:GetApiKeys: %w", err)
		}
		for _, item := range page.Items {
			nativeID := apigatewayARN(region, "apikeys", sv(item.Id))
			name := sv(item.Name)
			batch = append(batch, &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeAPIGatewayAPIKey,
				NativeID:       nativeID,
				Name:           &name,
				Region:         &region,
				AttributesJSON: mustJSON(item),
				TagsJSON:       mapTagsJSON(item.Tags),
				DiscoveredBy:   scanID,
			})
		}
	}
	if len(batch) > 0 {
		n, err := st.UpsertResources(batch)
		if err != nil {
			return total, inserted, fmt.Errorf("upsert API keys (%s): %w", region, err)
		}
		total = len(batch)
		inserted = n
	}
	return
}

// scanAPIGatewayClientCertificates discovers client certificates.
// ARN format: arn:aws:apigateway:{region}::/clientcertificates/{certId}
func scanAPIGatewayClientCertificates(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := apigateway.NewFromConfig(acct.cfg, func(o *apigateway.Options) { o.Region = region })
	pager := apigateway.NewGetClientCertificatesPaginator(client, &apigateway.GetClientCertificatesInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return total, inserted, skipIfAccessDenied(st, "apigateway:GetClientCertificates", acct.ID, region, err)
			}
			return total, inserted, fmt.Errorf("apigateway:GetClientCertificates: %w", err)
		}
		for _, item := range page.Items {
			certID := sv(item.ClientCertificateId)
			nativeID := apigatewayARN(region, "clientcertificates", certID)
			desc := sv(item.Description)
			batch = append(batch, &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeAPIGatewayClientCertificate,
				NativeID:       nativeID,
				Name:           &desc,
				Region:         &region,
				AttributesJSON: mustJSON(item),
				TagsJSON:       mapTagsJSON(item.Tags),
				DiscoveredBy:   scanID,
			})
		}
	}
	if len(batch) > 0 {
		n, err := st.UpsertResources(batch)
		if err != nil {
			return total, inserted, fmt.Errorf("upsert client certificates (%s): %w", region, err)
		}
		total = len(batch)
		inserted = n
	}
	return
}

// scanAPIGatewayDomainNames discovers custom domain names (v1), their base-path mappings,
// and domain-name access associations.
// Domain name ARN:        arn:aws:apigateway:{region}::/domainnames/{domainName}
// Base-path mapping ARN:  arn:aws:apigateway:{region}::/domainnames/{domainName}/basepathmappings/{basePath}
// Access association NativeID: the DomainNameAccessAssociationArn from the API response
// apigatewayDomainIsPrivate reports whether the V1 SDK DomainName carries an
// EndpointConfiguration with Types containing PRIVATE. Private custom-domain
// rows are the V1-SDK manifestation of CFN AWS::ApiGateway::DomainNameV2.
func apigatewayDomainIsPrivate(ec *apigatewaytypes.EndpointConfiguration) bool {
	if ec == nil {
		return false
	}
	return slices.Contains(ec.Types, apigatewaytypes.EndpointTypePrivate)
}

func scanAPIGatewayDomainNames(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := apigateway.NewFromConfig(acct.cfg, func(o *apigateway.Options) { o.Region = region })

	// 1. Page through domain names and their base-path mappings.
	pager := apigateway.NewGetDomainNamesPaginator(client, &apigateway.GetDomainNamesInput{})
	var domains []*store.Resource
	var mappings []*store.Resource
	for pager.HasMorePages() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return total, inserted, skipIfAccessDenied(st, "apigateway:GetDomainNames", acct.ID, region, err)
			}
			return total, inserted, fmt.Errorf("apigateway:GetDomainNames: %w", err)
		}
		for _, item := range page.Items {
			domainName := sv(item.DomainName)
			nativeID := apigatewayARN(region, "domainnames", domainName)
			// Branch on EndpointConfiguration.Types containing PRIVATE — CFN
			// models private REST custom domains as the V2-suffixed type
			// (`AWS::ApiGateway::DomainNameV2`) even though the V1 SDK serves
			// them. Same NativeID shape; child base-path mappings inherit the
			// V2 type via the same branch.
			isPrivate := apigatewayDomainIsPrivate(item.EndpointConfiguration)
			domainType := TypeAPIGatewayDomainName
			bpmType := TypeAPIGatewayBasePathMapping
			if isPrivate {
				domainType = TypeAPIGatewayPrivateDomainName
				bpmType = TypeAPIGatewayPrivateBasePathMapping
			}
			domains = append(domains, &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           domainType,
				NativeID:       nativeID,
				Name:           &domainName,
				Region:         &region,
				AttributesJSON: mustJSON(item),
				TagsJSON:       mapTagsJSON(item.Tags),
				DiscoveredBy:   scanID,
			})

			// Fetch base-path mappings for this domain.
			bpmOut, err := client.GetBasePathMappings(ctx, &apigateway.GetBasePathMappingsInput{DomainName: &domainName})
			if err != nil {
				if isAccessDenied(err) {
					_ = skipIfAccessDenied(st, "apigateway:GetBasePathMappings", acct.ID, region, err)
					continue
				}
				return total, inserted, fmt.Errorf("apigateway:GetBasePathMappings(%s): %w", domainName, err)
			}
			for _, bpm := range bpmOut.Items {
				basePath := sv(bpm.BasePath)
				bpmNativeID := apigatewayARN(region, "domainnames", domainName, "basepathmappings", basePath)
				name := fmt.Sprintf("%s → %s", domainName, basePath)
				mappings = append(mappings, &store.Resource{
					Provider:       "aws",
					AccountID:      acct.ID,
					AccountName:    &acct.Name,
					Type:           bpmType,
					NativeID:       bpmNativeID,
					Name:           &name,
					Region:         &region,
					AttributesJSON: mustJSON(bpm),
					DiscoveredBy:   scanID,
				})
			}
		}
	}
	if len(domains) > 0 {
		n, err := st.UpsertResources(domains)
		if err != nil {
			return total, inserted, fmt.Errorf("upsert domain names (%s): %w", region, err)
		}
		total += len(domains)
		inserted += n
	}
	if len(mappings) > 0 {
		n, err := st.UpsertResources(mappings)
		if err != nil {
			return total, inserted, fmt.Errorf("upsert base-path mappings (%s): %w", region, err)
		}
		total += len(mappings)
		inserted += n
	}

	// 2. Domain name access associations (uses Position-based pagination).
	assocInput := &apigateway.GetDomainNameAccessAssociationsInput{}
	var assocs []*store.Resource
	for {
		out, apiErr := client.GetDomainNameAccessAssociations(ctx, assocInput)
		if apiErr != nil {
			if isAccessDenied(apiErr) {
				return total, inserted, skipIfAccessDenied(st, "apigateway:GetDomainNameAccessAssociations", acct.ID, region, apiErr)
			}
			return total, inserted, fmt.Errorf("apigateway:GetDomainNameAccessAssociations: %w", apiErr)
		}
		for _, item := range out.Items {
			nativeID := sv(item.DomainNameAccessAssociationArn)
			name := sv(item.AccessAssociationSource)
			assocs = append(assocs, &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeAPIGatewayDomainNameAccessAssoc,
				NativeID:       nativeID,
				Name:           &name,
				Region:         &region,
				AttributesJSON: mustJSON(item),
				TagsJSON:       mapTagsJSON(item.Tags),
				DiscoveredBy:   scanID,
			})
		}
		if out.Position == nil {
			break
		}
		assocInput.Position = out.Position
	}
	if len(assocs) > 0 {
		n, err := st.UpsertResources(assocs)
		if err != nil {
			return total, inserted, fmt.Errorf("upsert domain name access associations (%s): %w", region, err)
		}
		total += len(assocs)
		inserted += n
	}
	return
}

// scanAPIGatewayUsagePlans discovers usage plans and their keys.
// Usage plan ARN: arn:aws:apigateway:{region}::/usageplans/{planId}
// Usage plan key ARN: arn:aws:apigateway:{region}::/usageplans/{planId}/keys/{keyId}
func scanAPIGatewayUsagePlans(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := apigateway.NewFromConfig(acct.cfg, func(o *apigateway.Options) { o.Region = region })
	pager := apigateway.NewGetUsagePlansPaginator(client, &apigateway.GetUsagePlansInput{})
	var plans []*store.Resource
	var keys []*store.Resource
	for pager.HasMorePages() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return total, inserted, skipIfAccessDenied(st, "apigateway:GetUsagePlans", acct.ID, region, err)
			}
			return total, inserted, fmt.Errorf("apigateway:GetUsagePlans: %w", err)
		}
		for _, item := range page.Items {
			planID := sv(item.Id)
			nativeID := apigatewayARN(region, "usageplans", planID)
			name := sv(item.Name)
			plans = append(plans, &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeAPIGatewayUsagePlan,
				NativeID:       nativeID,
				Name:           &name,
				Region:         &region,
				AttributesJSON: mustJSON(item),
				TagsJSON:       mapTagsJSON(item.Tags),
				DiscoveredBy:   scanID,
			})

			// Fetch keys for this plan (Position-based pagination).
			keyInput := &apigateway.GetUsagePlanKeysInput{UsagePlanId: &planID}
			for {
				keyOut, err := client.GetUsagePlanKeys(ctx, keyInput)
				if err != nil {
					if isAccessDenied(err) {
						_ = skipIfAccessDenied(st, "apigateway:GetUsagePlanKeys", acct.ID, region, err)
						break
					}
					return total, inserted, fmt.Errorf("apigateway:GetUsagePlanKeys(%s): %w", planID, err)
				}
				for _, key := range keyOut.Items {
					keyID := sv(key.Id)
					keyNativeID := apigatewayARN(region, "usageplans", planID, "keys", keyID)
					keyName := sv(key.Name)
					keys = append(keys, &store.Resource{
						Provider:       "aws",
						AccountID:      acct.ID,
						AccountName:    &acct.Name,
						Type:           TypeAPIGatewayUsagePlanKey,
						NativeID:       keyNativeID,
						Name:           &keyName,
						Region:         &region,
						AttributesJSON: mustJSON(key),
						DiscoveredBy:   scanID,
					})
				}
				if keyOut.Position == nil {
					break
				}
				keyInput.Position = keyOut.Position
			}
		}
	}
	if len(plans) > 0 {
		n, err := st.UpsertResources(plans)
		if err != nil {
			return total, inserted, fmt.Errorf("upsert usage plans (%s): %w", region, err)
		}
		total += len(plans)
		inserted += n
	}
	if len(keys) > 0 {
		n, err := st.UpsertResources(keys)
		if err != nil {
			return total, inserted, fmt.Errorf("upsert usage plan keys (%s): %w", region, err)
		}
		total += len(keys)
		inserted += n
	}
	return
}

// scanAPIGatewayVPCLinks discovers VPC links for REST APIs (v1).
// ARN format: arn:aws:apigateway:{region}::/vpclinks/{linkId}
func scanAPIGatewayVPCLinks(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := apigateway.NewFromConfig(acct.cfg, func(o *apigateway.Options) { o.Region = region })
	pager := apigateway.NewGetVpcLinksPaginator(client, &apigateway.GetVpcLinksInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return total, inserted, skipIfAccessDenied(st, "apigateway:GetVpcLinks", acct.ID, region, err)
			}
			return total, inserted, fmt.Errorf("apigateway:GetVpcLinks: %w", err)
		}
		for _, item := range page.Items {
			nativeID := apigatewayARN(region, "vpclinks", sv(item.Id))
			name := sv(item.Name)
			batch = append(batch, &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeAPIGatewayVpcLink,
				NativeID:       nativeID,
				Name:           &name,
				Region:         &region,
				AttributesJSON: mustJSON(item),
				TagsJSON:       mapTagsJSON(item.Tags),
				DiscoveredBy:   scanID,
			})
		}
	}
	if len(batch) > 0 {
		n, err := st.UpsertResources(batch)
		if err != nil {
			return total, inserted, fmt.Errorf("upsert VPC links (%s): %w", region, err)
		}
		total = len(batch)
		inserted = n
	}
	return
}

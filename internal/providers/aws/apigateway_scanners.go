package aws

import (
	"context"
	"fmt"
	"sync/atomic"

	"codeburg.org/icearp/disco/internal/store"
	"github.com/aws/aws-sdk-go-v2/service/apigateway"
	"github.com/aws/aws-sdk-go-v2/service/apigatewayv2"
	"golang.org/x/sync/errgroup"
)

func init() {
	registerService(serviceEntry{name: "aws:apigateway", fn: scanAPIGateway})
	registerService(serviceEntry{name: "aws:apigatewayv2", fn: scanAPIGatewayV2})
}

// scanAPIGateway is the orchestrator for all API Gateway v1 (REST) resource types.
// It runs all sub-scanners concurrently and aggregates their counts.
func scanAPIGateway(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	return runScanners(ctx,
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

// scanAPIGatewayV2 is the orchestrator for all API Gateway v2 (HTTP/WebSocket) resource types.
func scanAPIGatewayV2(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	return runScanners(ctx,
		func(ctx context.Context) (int, int, error) {
			return scanAPIGatewayHTTPAPIs(ctx, acct, region, st, scanID)
		},
		func(ctx context.Context) (int, int, error) {
			return scanAPIGatewayV2DomainNames(ctx, acct, region, st, scanID)
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
				return total, inserted, skipIfAccessDenied("apigateway:GetRestApis", acct.ID, region, err)
			}
			return total, inserted, fmt.Errorf("apigateway:GetRestApis: %w", err)
		}
		for _, api := range page.Items {
			nativeID := fmt.Sprintf("arn:aws:apigateway:%s::/restapis/%s", region, sv(api.Id))
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
func scanAPIGatewayPerAPI(ctx context.Context, client *apigateway.Client, acct *account, region, apiID string, st *store.Store, scanID string) (total, inserted int, err error) {
	return runScanners(ctx,
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
func scanAPIGatewayAuthorizers(ctx context.Context, client *apigateway.Client, acct *account, region, apiID string, st *store.Store, scanID string) (total, inserted int, err error) {
	out, err := client.GetAuthorizers(ctx, &apigateway.GetAuthorizersInput{RestApiId: &apiID})
	if err != nil {
		if isAccessDenied(err) {
			return 0, 0, skipIfAccessDenied("apigateway:GetAuthorizers", acct.ID, region, err)
		}
		return 0, 0, fmt.Errorf("apigateway:GetAuthorizers(%s): %w", apiID, err)
	}
	var batch []*store.Resource
	for _, item := range out.Items {
		nativeID := fmt.Sprintf("arn:aws:apigateway:%s::/restapis/%s/authorizers/%s", region, apiID, sv(item.Id))
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
func scanAPIGatewayDeployments(ctx context.Context, client *apigateway.Client, acct *account, region, apiID string, st *store.Store, scanID string) (total, inserted int, err error) {
	pager := apigateway.NewGetDeploymentsPaginator(client, &apigateway.GetDeploymentsInput{RestApiId: &apiID})
	var batch []*store.Resource
	for pager.HasMorePages() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return total, inserted, skipIfAccessDenied("apigateway:GetDeployments", acct.ID, region, err)
			}
			return total, inserted, fmt.Errorf("apigateway:GetDeployments(%s): %w", apiID, err)
		}
		for _, item := range page.Items {
			nativeID := fmt.Sprintf("arn:aws:apigateway:%s::/restapis/%s/deployments/%s", region, apiID, sv(item.Id))
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
func scanAPIGatewayStages(ctx context.Context, client *apigateway.Client, acct *account, region, apiID string, st *store.Store, scanID string) (total, inserted int, err error) {
	out, err := client.GetStages(ctx, &apigateway.GetStagesInput{RestApiId: &apiID})
	if err != nil {
		if isAccessDenied(err) {
			return 0, 0, skipIfAccessDenied("apigateway:GetStages", acct.ID, region, err)
		}
		return 0, 0, fmt.Errorf("apigateway:GetStages(%s): %w", apiID, err)
	}
	var batch []*store.Resource
	for _, item := range out.Item {
		stageName := sv(item.StageName)
		nativeID := fmt.Sprintf("arn:aws:apigateway:%s::/restapis/%s/stages/%s", region, apiID, stageName)
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
func scanAPIGatewayResources(ctx context.Context, client *apigateway.Client, acct *account, region, apiID string, st *store.Store, scanID string) (total, inserted int, err error) {
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
				return total, inserted, skipIfAccessDenied("apigateway:GetResources", acct.ID, region, err)
			}
			return total, inserted, fmt.Errorf("apigateway:GetResources(%s): %w", apiID, err)
		}
		for _, item := range page.Items {
			resourceID := sv(item.Id)
			path := sv(item.Path)
			resourceNativeID := fmt.Sprintf("arn:aws:apigateway:%s::/restapis/%s/resources/%s", region, apiID, resourceID)
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
				methodNativeID := fmt.Sprintf("arn:aws:apigateway:%s::/restapis/%s/resources/%s/methods/%s", region, apiID, resourceID, httpMethod)
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
func scanAPIGatewayModels(ctx context.Context, client *apigateway.Client, acct *account, region, apiID string, st *store.Store, scanID string) (total, inserted int, err error) {
	pager := apigateway.NewGetModelsPaginator(client, &apigateway.GetModelsInput{RestApiId: &apiID})
	var batch []*store.Resource
	for pager.HasMorePages() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return total, inserted, skipIfAccessDenied("apigateway:GetModels", acct.ID, region, err)
			}
			return total, inserted, fmt.Errorf("apigateway:GetModels(%s): %w", apiID, err)
		}
		for _, item := range page.Items {
			name := sv(item.Name)
			nativeID := fmt.Sprintf("arn:aws:apigateway:%s::/restapis/%s/models/%s", region, apiID, name)
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
func scanAPIGatewayRequestValidators(ctx context.Context, client *apigateway.Client, acct *account, region, apiID string, st *store.Store, scanID string) (total, inserted int, err error) {
	out, err := client.GetRequestValidators(ctx, &apigateway.GetRequestValidatorsInput{RestApiId: &apiID})
	if err != nil {
		if isAccessDenied(err) {
			return 0, 0, skipIfAccessDenied("apigateway:GetRequestValidators", acct.ID, region, err)
		}
		return 0, 0, fmt.Errorf("apigateway:GetRequestValidators(%s): %w", apiID, err)
	}
	var batch []*store.Resource
	for _, item := range out.Items {
		nativeID := fmt.Sprintf("arn:aws:apigateway:%s::/restapis/%s/requestvalidators/%s", region, apiID, sv(item.Id))
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
func scanAPIGatewayGatewayResponses(ctx context.Context, client *apigateway.Client, acct *account, region, apiID string, st *store.Store, scanID string) (total, inserted int, err error) {
	out, err := client.GetGatewayResponses(ctx, &apigateway.GetGatewayResponsesInput{RestApiId: &apiID})
	if err != nil {
		if isAccessDenied(err) {
			return 0, 0, skipIfAccessDenied("apigateway:GetGatewayResponses", acct.ID, region, err)
		}
		return 0, 0, fmt.Errorf("apigateway:GetGatewayResponses(%s): %w", apiID, err)
	}
	var batch []*store.Resource
	for _, item := range out.Items {
		responseType := string(item.ResponseType)
		nativeID := fmt.Sprintf("arn:aws:apigateway:%s::/restapis/%s/gatewayresponses/%s", region, apiID, responseType)
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
func scanAPIGatewayDocumentationParts(ctx context.Context, client *apigateway.Client, acct *account, region, apiID string, st *store.Store, scanID string) (total, inserted int, err error) {
	input := &apigateway.GetDocumentationPartsInput{RestApiId: &apiID}
	var batch []*store.Resource
	for {
		out, apiErr := client.GetDocumentationParts(ctx, input)
		if apiErr != nil {
			if isAccessDenied(apiErr) {
				return 0, 0, skipIfAccessDenied("apigateway:GetDocumentationParts", acct.ID, region, apiErr)
			}
			return 0, 0, fmt.Errorf("apigateway:GetDocumentationParts(%s): %w", apiID, apiErr)
		}
		for _, item := range out.Items {
			nativeID := fmt.Sprintf("arn:aws:apigateway:%s::/restapis/%s/documentation/parts/%s", region, apiID, sv(item.Id))
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
func scanAPIGatewayDocumentationVersions(ctx context.Context, client *apigateway.Client, acct *account, region, apiID string, st *store.Store, scanID string) (total, inserted int, err error) {
	input := &apigateway.GetDocumentationVersionsInput{RestApiId: &apiID}
	var batch []*store.Resource
	for {
		out, apiErr := client.GetDocumentationVersions(ctx, input)
		if apiErr != nil {
			if isAccessDenied(apiErr) {
				return 0, 0, skipIfAccessDenied("apigateway:GetDocumentationVersions", acct.ID, region, apiErr)
			}
			return 0, 0, fmt.Errorf("apigateway:GetDocumentationVersions(%s): %w", apiID, apiErr)
		}
		for _, item := range out.Items {
			version := sv(item.Version)
			nativeID := fmt.Sprintf("arn:aws:apigateway:%s::/restapis/%s/documentation/versions/%s", region, apiID, version)
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
			return 0, 0, skipIfAccessDenied("apigateway:GetAccount", acct.ID, region, err)
		}
		return 0, 0, fmt.Errorf("apigateway:GetAccount: %w", err)
	}
	nativeID := fmt.Sprintf("arn:aws:apigateway:%s::/account", region)
	name := "account"
	n, err := st.UpsertResource(&store.Resource{
		Provider:       "aws",
		AccountID:      acct.ID,
		AccountName:    &acct.Name,
		Type:           TypeAPIGatewayAccount,
		NativeID:       nativeID,
		Name:           &name,
		Region:         &region,
		AttributesJSON: mustJSON(out),
		DiscoveredBy:   scanID,
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
				return total, inserted, skipIfAccessDenied("apigateway:GetApiKeys", acct.ID, region, err)
			}
			return total, inserted, fmt.Errorf("apigateway:GetApiKeys: %w", err)
		}
		for _, item := range page.Items {
			nativeID := fmt.Sprintf("arn:aws:apigateway:%s::/apikeys/%s", region, sv(item.Id))
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
				return total, inserted, skipIfAccessDenied("apigateway:GetClientCertificates", acct.ID, region, err)
			}
			return total, inserted, fmt.Errorf("apigateway:GetClientCertificates: %w", err)
		}
		for _, item := range page.Items {
			certID := sv(item.ClientCertificateId)
			nativeID := fmt.Sprintf("arn:aws:apigateway:%s::/clientcertificates/%s", region, certID)
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
				return total, inserted, skipIfAccessDenied("apigateway:GetDomainNames", acct.ID, region, err)
			}
			return total, inserted, fmt.Errorf("apigateway:GetDomainNames: %w", err)
		}
		for _, item := range page.Items {
			domainName := sv(item.DomainName)
			nativeID := fmt.Sprintf("arn:aws:apigateway:%s::/domainnames/%s", region, domainName)
			domains = append(domains, &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeAPIGatewayDomainName,
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
					_ = skipIfAccessDenied("apigateway:GetBasePathMappings", acct.ID, region, err)
					continue
				}
				return total, inserted, fmt.Errorf("apigateway:GetBasePathMappings(%s): %w", domainName, err)
			}
			for _, bpm := range bpmOut.Items {
				basePath := sv(bpm.BasePath)
				bpmNativeID := fmt.Sprintf("arn:aws:apigateway:%s::/domainnames/%s/basepathmappings/%s", region, domainName, basePath)
				name := fmt.Sprintf("%s → %s", domainName, basePath)
				mappings = append(mappings, &store.Resource{
					Provider:       "aws",
					AccountID:      acct.ID,
					AccountName:    &acct.Name,
					Type:           TypeAPIGatewayBasePathMapping,
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
				return total, inserted, skipIfAccessDenied("apigateway:GetDomainNameAccessAssociations", acct.ID, region, apiErr)
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
				return total, inserted, skipIfAccessDenied("apigateway:GetUsagePlans", acct.ID, region, err)
			}
			return total, inserted, fmt.Errorf("apigateway:GetUsagePlans: %w", err)
		}
		for _, item := range page.Items {
			planID := sv(item.Id)
			nativeID := fmt.Sprintf("arn:aws:apigateway:%s::/usageplans/%s", region, planID)
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
						_ = skipIfAccessDenied("apigateway:GetUsagePlanKeys", acct.ID, region, err)
						break
					}
					return total, inserted, fmt.Errorf("apigateway:GetUsagePlanKeys(%s): %w", planID, err)
				}
				for _, key := range keyOut.Items {
					keyID := sv(key.Id)
					keyNativeID := fmt.Sprintf("arn:aws:apigateway:%s::/usageplans/%s/keys/%s", region, planID, keyID)
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
				return total, inserted, skipIfAccessDenied("apigateway:GetVpcLinks", acct.ID, region, err)
			}
			return total, inserted, fmt.Errorf("apigateway:GetVpcLinks: %w", err)
		}
		for _, item := range page.Items {
			nativeID := fmt.Sprintf("arn:aws:apigateway:%s::/vpclinks/%s", region, sv(item.Id))
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

// scanAPIGatewayV2 discovers HTTP and WebSocket APIs (API Gateway v2).
// Tags are included in the GetApis response; no separate tag call is needed.
// ARN format: arn:aws:apigateway:{region}::/apis/{id}
// scanAPIGatewayHTTPAPIs discovers HTTP and WebSocket APIs (API Gateway v2).
func scanAPIGatewayHTTPAPIs(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := apigatewayv2.NewFromConfig(acct.cfg, func(o *apigatewayv2.Options) { o.Region = region })

	// GetApis uses NextToken pagination; no built-in paginator exists.
	input := &apigatewayv2.GetApisInput{}
	var batch []*store.Resource
	for {
		page, apiErr := client.GetApis(ctx, input)
		if apiErr != nil {
			if isAccessDenied(apiErr) {
				return 0, 0, skipIfAccessDenied("apigatewayv2:GetApis", acct.ID, region, apiErr)
			}
			return 0, 0, fmt.Errorf("apigatewayv2:GetApis: %w", apiErr)
		}
		for _, api := range page.Items {
			nativeID := fmt.Sprintf("arn:aws:apigateway:%s::/apis/%s", region, sv(api.ApiId))
			name := sv(api.Name)
			batch = append(batch, &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeAPIGatewayV2API,
				NativeID:       nativeID,
				Name:           &name,
				Region:         &region,
				AttributesJSON: mustJSON(api),
				TagsJSON:       mapTagsJSON(api.Tags),
				DiscoveredBy:   scanID,
			})
		}
		if page.NextToken == nil {
			break
		}
		input.NextToken = page.NextToken
	}
	if len(batch) > 0 {
		n, err := st.UpsertResources(batch)
		if err != nil {
			return 0, 0, fmt.Errorf("upsert HTTP/WebSocket APIs (%s): %w", region, err)
		}
		total = len(batch)
		inserted = n
	}
	return
}

// scanAPIGatewayV2DomainNames discovers custom domain names and API mappings for API Gateway v2.
// Domain name ARN: arn:aws:apigateway:{region}::/domainnames/{domainName}
// API mapping ARN: arn:aws:apigateway:{region}::/domainnames/{domainName}/apimappings/{mappingId}
func scanAPIGatewayV2DomainNames(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := apigatewayv2.NewFromConfig(acct.cfg, func(o *apigatewayv2.Options) { o.Region = region })

	// GetDomainNames uses NextToken pagination; no built-in paginator exists.
	input := &apigatewayv2.GetDomainNamesInput{}
	var domains []*store.Resource
	var mappings []*store.Resource
	for {
		page, apiErr := client.GetDomainNames(ctx, input)
		if apiErr != nil {
			if isAccessDenied(apiErr) {
				return 0, 0, skipIfAccessDenied("apigatewayv2:GetDomainNames", acct.ID, region, apiErr)
			}
			return 0, 0, fmt.Errorf("apigatewayv2:GetDomainNames: %w", apiErr)
		}
		for _, item := range page.Items {
			domainName := sv(item.DomainName)
			nativeID := fmt.Sprintf("arn:aws:apigateway:%s::/domainnames/%s", region, domainName)
			domains = append(domains, &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeAPIGatewayDomainNameV2,
				NativeID:       nativeID,
				Name:           &domainName,
				Region:         &region,
				AttributesJSON: mustJSON(item),
				TagsJSON:       mapTagsJSON(item.Tags),
				DiscoveredBy:   scanID,
			})

			// Fetch API mappings for this domain (NextToken pagination).
			mapInput := &apigatewayv2.GetApiMappingsInput{DomainName: &domainName}
			for {
				mapOut, mapErr := client.GetApiMappings(ctx, mapInput)
				if mapErr != nil {
					if isAccessDenied(mapErr) {
						_ = skipIfAccessDenied("apigatewayv2:GetApiMappings", acct.ID, region, mapErr)
						break
					}
					return total, inserted, fmt.Errorf("apigatewayv2:GetApiMappings(%s): %w", domainName, mapErr)
				}
				for _, m := range mapOut.Items {
					mappingID := sv(m.ApiMappingId)
					mapNativeID := fmt.Sprintf("arn:aws:apigateway:%s::/domainnames/%s/apimappings/%s", region, domainName, mappingID)
					name := fmt.Sprintf("%s → %s", domainName, sv(m.ApiMappingKey))
					mappings = append(mappings, &store.Resource{
						Provider:       "aws",
						AccountID:      acct.ID,
						AccountName:    &acct.Name,
						Type:           TypeAPIGatewayBasePathMappingV2,
						NativeID:       mapNativeID,
						Name:           &name,
						Region:         &region,
						AttributesJSON: mustJSON(m),
						DiscoveredBy:   scanID,
					})
				}
				if mapOut.NextToken == nil {
					break
				}
				mapInput.NextToken = mapOut.NextToken
			}
		}
		if page.NextToken == nil {
			break
		}
		input.NextToken = page.NextToken
	}
	if len(domains) > 0 {
		n, err := st.UpsertResources(domains)
		if err != nil {
			return total, inserted, fmt.Errorf("upsert v2 domain names (%s): %w", region, err)
		}
		total += len(domains)
		inserted += n
	}
	if len(mappings) > 0 {
		n, err := st.UpsertResources(mappings)
		if err != nil {
			return total, inserted, fmt.Errorf("upsert v2 API mappings (%s): %w", region, err)
		}
		total += len(mappings)
		inserted += n
	}
	return
}

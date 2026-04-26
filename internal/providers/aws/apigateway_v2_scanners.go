package aws

import (
	"context"
	"fmt"
	"sync/atomic"

	"codeberg.org/icearp/disco/internal/store"
	"github.com/aws/aws-sdk-go-v2/service/apigatewayv2"
	"golang.org/x/sync/errgroup"
)

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

// scanAPIGatewayV2 discovers HTTP and WebSocket APIs (API Gateway v2).
// Tags are included in the GetApis response; no separate tag call is needed.
// ARN format: arn:aws:apigateway:{region}::/apis/{id}
// scanAPIGatewayHTTPAPIs discovers HTTP and WebSocket APIs (API Gateway v2),
// then fans out to scan per-API child resources (authorizers) concurrently.
func scanAPIGatewayHTTPAPIs(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := apigatewayv2.NewFromConfig(acct.cfg, func(o *apigatewayv2.Options) { o.Region = region })

	// GetApis uses NextToken pagination; no built-in paginator exists.
	input := &apigatewayv2.GetApisInput{}
	var batch []*store.Resource
	var apiIDs []string
	for {
		page, apiErr := client.GetApis(ctx, input)
		if apiErr != nil {
			if isAccessDenied(apiErr) {
				return 0, 0, skipIfAccessDenied(st, "apigatewayv2:GetApis", acct.ID, region, apiErr)
			}
			return 0, 0, fmt.Errorf("apigatewayv2:GetApis: %w", apiErr)
		}
		for _, api := range page.Items {
			nativeID := apigatewayARN(region, "apis", sv(api.ApiId))
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
			apiIDs = append(apiIDs, sv(api.ApiId))
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

	// Fan out per-API child scans (authorizers) concurrently.
	var t, ni atomic.Int64
	eg, egCtx := errgroup.WithContext(ctx)
	for _, id := range apiIDs {
		eg.Go(func() error {
			tt, nn, e := scanAPIGatewayV2Authorizers(egCtx, client, acct, region, id, st, scanID)
			t.Add(int64(tt))
			ni.Add(int64(nn))
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

// scanAPIGatewayV2Authorizers discovers authorizers for a single v2 (HTTP/WebSocket) API.
// ARN format: arn:aws:apigateway:{region}::/apis/{apiId}/authorizers/{authorizerId}
func scanAPIGatewayV2Authorizers(ctx context.Context, client *apigatewayv2.Client, acct *account, region, apiID string, st *store.Store, scanID string) (total, inserted int, err error) {
	input := &apigatewayv2.GetAuthorizersInput{ApiId: &apiID}
	var batch []*store.Resource
	for {
		page, apiErr := client.GetAuthorizers(ctx, input)
		if apiErr != nil {
			if isAccessDenied(apiErr) {
				return 0, 0, skipIfAccessDenied(st, "apigatewayv2:GetAuthorizers", acct.ID, region, apiErr)
			}
			return 0, 0, fmt.Errorf("apigatewayv2:GetAuthorizers(%s): %w", apiID, apiErr)
		}
		for _, item := range page.Items {
			nativeID := apigatewayARN(region, "apis", apiID, "authorizers", sv(item.AuthorizerId))
			name := sv(item.Name)
			batch = append(batch, &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeAPIGatewayV2Authorizer,
				NativeID:       nativeID,
				Name:           &name,
				Region:         &region,
				AttributesJSON: mustJSON(item),
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
			return 0, 0, fmt.Errorf("upsert v2 authorizers (%s/%s): %w", region, apiID, err)
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
				return 0, 0, skipIfAccessDenied(st, "apigatewayv2:GetDomainNames", acct.ID, region, apiErr)
			}
			return 0, 0, fmt.Errorf("apigatewayv2:GetDomainNames: %w", apiErr)
		}
		for _, item := range page.Items {
			domainName := sv(item.DomainName)
			nativeID := apigatewayARN(region, "domainnames", domainName)
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
						_ = skipIfAccessDenied(st, "apigatewayv2:GetApiMappings", acct.ID, region, mapErr)
						break
					}
					return total, inserted, fmt.Errorf("apigatewayv2:GetApiMappings(%s): %w", domainName, mapErr)
				}
				for _, m := range mapOut.Items {
					mappingID := sv(m.ApiMappingId)
					mapNativeID := apigatewayARN(region, "domainnames", domainName, "apimappings", mappingID)
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

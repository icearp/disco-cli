package aws

import (
	"context"
	"fmt"

	"codeburg.org/icearp/disco/internal/store"
	"github.com/aws/aws-sdk-go-v2/service/apigateway"
	"github.com/aws/aws-sdk-go-v2/service/apigatewayv2"
)

func init() {
	registerService(serviceEntry{
		name: "aws:apigateway",
		fn: func(ctx context.Context, acct *account, region string, st *store.Store, scanID string) error {
			return scanAPIGatewayREST(ctx, acct, region, st, scanID)
		},
	})
	registerService(serviceEntry{
		name: "aws:apigatewayv2",
		fn: func(ctx context.Context, acct *account, region string, st *store.Store, scanID string) error {
			return scanAPIGatewayV2(ctx, acct, region, st, scanID)
		},
	})
}

// scanAPIGatewayREST discovers REST APIs (API Gateway v1).
// Tags are included in the GetRestApis response; no separate tag call is needed.
// ARN format: arn:aws:apigateway:{region}::/restapis/{id}
func scanAPIGatewayREST(ctx context.Context, acct *account, region string, st *store.Store, scanID string) error {
	client := apigateway.NewFromConfig(acct.cfg, func(o *apigateway.Options) { o.Region = region })

	pager := apigateway.NewGetRestApisPaginator(client, &apigateway.GetRestApisInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return skipIfAccessDenied("apigateway:GetRestApis", acct.ID, region, err)
			}
			return fmt.Errorf("apigateway:GetRestApis: %w", err)
		}
		for _, api := range page.Items {
			nativeID := fmt.Sprintf("arn:aws:apigateway:%s::/restapis/%s", region, sv(api.Id))
			name := sv(api.Name)
			r := &store.Resource{
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
			}
			batch = append(batch, r)
		}
	}
	if len(batch) > 0 {
		if err := st.UpsertResources(batch); err != nil {
			return fmt.Errorf("upsert REST APIs (%s): %w", region, err)
		}
	}
	return nil
}

// scanAPIGatewayV2 discovers HTTP and WebSocket APIs (API Gateway v2).
// Tags are included in the GetApis response; no separate tag call is needed.
// ARN format: arn:aws:apigateway:{region}::/apis/{id}
func scanAPIGatewayV2(ctx context.Context, acct *account, region string, st *store.Store, scanID string) error {
	client := apigatewayv2.NewFromConfig(acct.cfg, func(o *apigatewayv2.Options) { o.Region = region })

	// GetApis uses NextToken pagination; no built-in paginator exists.
	input := &apigatewayv2.GetApisInput{}
	var batch []*store.Resource
	for {
		page, err := client.GetApis(ctx, input)
		if err != nil {
			if isAccessDenied(err) {
				return skipIfAccessDenied("apigatewayv2:GetApis", acct.ID, region, err)
			}
			return fmt.Errorf("apigatewayv2:GetApis: %w", err)
		}
		for _, api := range page.Items {
			nativeID := fmt.Sprintf("arn:aws:apigateway:%s::/apis/%s", region, sv(api.ApiId))
			name := sv(api.Name)
			r := &store.Resource{
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
			}
			batch = append(batch, r)
		}
		if page.NextToken == nil {
			break
		}
		input.NextToken = page.NextToken
	}
	if len(batch) > 0 {
		if err := st.UpsertResources(batch); err != nil {
			return fmt.Errorf("upsert HTTP/WebSocket APIs (%s): %w", region, err)
		}
	}
	return nil
}

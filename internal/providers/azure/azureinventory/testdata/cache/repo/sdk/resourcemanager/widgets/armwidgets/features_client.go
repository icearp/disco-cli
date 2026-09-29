package armwidgets

// The namespace follows the last providers segment whose successor is static; the trailing providers pair strips.
func (client *FeaturesClient) List(ctx context.Context, options *FeaturesClientListOptions) (FeaturesClientListResponse, error) {
	req, err := client.listCreateRequest(ctx, options)
	if err != nil {
		return FeaturesClientListResponse{}, err
	}
	httpResp, err := client.internal.Pipeline().Do(req)
	if err != nil {
		return FeaturesClientListResponse{}, err
	}
	return client.listHandleResponse(httpResp)
}

func (client *FeaturesClient) listCreateRequest(ctx context.Context, options *FeaturesClientListOptions) (*policy.Request, error) {
	urlPath := "/subscriptions/{subscriptionId}/providers/Microsoft.Widgets/providers/{resourceProviderNamespace}/features"
	req, err := runtime.NewRequest(ctx, http.MethodGet, runtime.JoinPaths(client.internal.Endpoint(), urlPath))
	return req, err
}

func (client *FeaturesClient) listHandleResponse(resp *http.Response) (FeaturesClientListResponse, error) {
	result := FeaturesClientListResponse{}
	if err := runtime.UnmarshalAsJSON(resp, &result.FeatureList); err != nil {
		return FeaturesClientListResponse{}, err
	}
	return result, nil
}

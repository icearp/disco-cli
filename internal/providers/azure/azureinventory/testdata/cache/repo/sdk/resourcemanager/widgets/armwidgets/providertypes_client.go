package armwidgets

// The namespace is a parameter: Other, with a diagnostic.
func (client *ProviderTypesClient) List(ctx context.Context, options *ProviderTypesClientListOptions) (ProviderTypesClientListResponse, error) {
	req, err := client.listCreateRequest(ctx, options)
	if err != nil {
		return ProviderTypesClientListResponse{}, err
	}
	httpResp, err := client.internal.Pipeline().Do(req)
	if err != nil {
		return ProviderTypesClientListResponse{}, err
	}
	return client.listHandleResponse(httpResp)
}

func (client *ProviderTypesClient) listCreateRequest(ctx context.Context, options *ProviderTypesClientListOptions) (*policy.Request, error) {
	urlPath := "/subscriptions/{subscriptionId}/providers/{resourceProviderNamespace}/resourceTypes"
	req, err := runtime.NewRequest(ctx, http.MethodGet, runtime.JoinPaths(client.internal.Endpoint(), urlPath))
	return req, err
}

func (client *ProviderTypesClient) listHandleResponse(resp *http.Response) (ProviderTypesClientListResponse, error) {
	result := ProviderTypesClientListResponse{}
	if err := runtime.UnmarshalAsJSON(resp, &result.ProviderTypeList); err != nil {
		return ProviderTypesClientListResponse{}, err
	}
	return result, nil
}

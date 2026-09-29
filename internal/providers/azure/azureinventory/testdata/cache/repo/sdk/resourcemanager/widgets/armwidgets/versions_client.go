package armwidgets

// Reached only through a location: the regional catalog, whatever its envelope.
func (client *VersionsClient) NewListPager(options *VersionsClientListOptions) *runtime.Pager[VersionsClientListResponse] {
	return runtime.NewPager(runtime.PagingHandler[VersionsClientListResponse]{
		Fetcher: func(ctx context.Context, page *VersionsClientListResponse) (VersionsClientListResponse, error) {
			resp, err := runtime.FetcherForNextLink(ctx, client.internal.Pipeline(), "", func(ctx context.Context) (*policy.Request, error) {
				return client.listCreateRequest(ctx, options)
			}, nil)
			if err != nil {
				return VersionsClientListResponse{}, err
			}
			return client.listHandleResponse(resp)
		},
	})
}

func (client *VersionsClient) listCreateRequest(ctx context.Context, options *VersionsClientListOptions) (*policy.Request, error) {
	urlPath := "/subscriptions/{subscriptionId}/providers/Microsoft.Widgets/locations/{location}/versions"
	req, err := runtime.NewRequest(ctx, http.MethodGet, runtime.JoinPaths(client.internal.Endpoint(), urlPath))
	return req, err
}

func (client *VersionsClient) listHandleResponse(resp *http.Response) (VersionsClientListResponse, error) {
	result := VersionsClientListResponse{}
	if err := runtime.UnmarshalAsJSON(resp, &result.VersionList); err != nil {
		return VersionsClientListResponse{}, err
	}
	return result, nil
}

func (client *VersionsClient) Get(ctx context.Context, options *VersionsClientGetOptions) (VersionsClientGetResponse, error) {
	req, err := client.getCreateRequest(ctx, options)
	if err != nil {
		return VersionsClientGetResponse{}, err
	}
	httpResp, err := client.internal.Pipeline().Do(req)
	if err != nil {
		return VersionsClientGetResponse{}, err
	}
	return client.getHandleResponse(httpResp)
}

func (client *VersionsClient) getCreateRequest(ctx context.Context, options *VersionsClientGetOptions) (*policy.Request, error) {
	urlPath := "/subscriptions/{subscriptionId}/providers/Microsoft.Widgets/locations/{location}/versions/{version}"
	req, err := runtime.NewRequest(ctx, http.MethodGet, runtime.JoinPaths(client.internal.Endpoint(), urlPath))
	return req, err
}

func (client *VersionsClient) getHandleResponse(resp *http.Response) (VersionsClientGetResponse, error) {
	result := VersionsClientGetResponse{}
	if err := runtime.UnmarshalAsJSON(resp, &result.Version); err != nil {
		return VersionsClientGetResponse{}, err
	}
	return result, nil
}

package armwidgets

func (client *SKUsClient) NewListPager(options *SKUsClientListOptions) *runtime.Pager[SKUsClientListResponse] {
	return runtime.NewPager(runtime.PagingHandler[SKUsClientListResponse]{
		Fetcher: func(ctx context.Context, page *SKUsClientListResponse) (SKUsClientListResponse, error) {
			resp, err := runtime.FetcherForNextLink(ctx, client.internal.Pipeline(), "", func(ctx context.Context) (*policy.Request, error) {
				return client.listCreateRequest(ctx, options)
			}, nil)
			if err != nil {
				return SKUsClientListResponse{}, err
			}
			return client.listHandleResponse(resp)
		},
	})
}

func (client *SKUsClient) listCreateRequest(ctx context.Context, options *SKUsClientListOptions) (*policy.Request, error) {
	urlPath := "/subscriptions/{subscriptionId}/providers/Microsoft.Widgets/skus"
	req, err := runtime.NewRequest(ctx, http.MethodGet, runtime.JoinPaths(client.internal.Endpoint(), urlPath))
	return req, err
}

func (client *SKUsClient) listHandleResponse(resp *http.Response) (SKUsClientListResponse, error) {
	result := SKUsClientListResponse{}
	if err := runtime.UnmarshalAsJSON(resp, &result.SKUListResult); err != nil {
		return SKUsClientListResponse{}, err
	}
	return result, nil
}

func (client *SKUsClient) Get(ctx context.Context, options *SKUsClientGetOptions) (SKUsClientGetResponse, error) {
	req, err := client.getCreateRequest(ctx, options)
	if err != nil {
		return SKUsClientGetResponse{}, err
	}
	httpResp, err := client.internal.Pipeline().Do(req)
	if err != nil {
		return SKUsClientGetResponse{}, err
	}
	return client.getHandleResponse(httpResp)
}

func (client *SKUsClient) getCreateRequest(ctx context.Context, options *SKUsClientGetOptions) (*policy.Request, error) {
	urlPath := "/subscriptions/{subscriptionId}/providers/Microsoft.Widgets/skus/{skuName}"
	req, err := runtime.NewRequest(ctx, http.MethodGet, runtime.JoinPaths(client.internal.Endpoint(), urlPath))
	return req, err
}

func (client *SKUsClient) getHandleResponse(resp *http.Response) (SKUsClientGetResponse, error) {
	result := SKUsClientGetResponse{}
	if err := runtime.UnmarshalAsJSON(resp, &result.SKU); err != nil {
		return SKUsClientGetResponse{}, err
	}
	return result, nil
}

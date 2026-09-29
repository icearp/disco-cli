package armwidgets

// Reached only through a locations pair: scope-pair:locations.
func (client *UsagesClient) NewListPager(options *UsagesClientListOptions) *runtime.Pager[UsagesClientListResponse] {
	return runtime.NewPager(runtime.PagingHandler[UsagesClientListResponse]{
		Fetcher: func(ctx context.Context, page *UsagesClientListResponse) (UsagesClientListResponse, error) {
			resp, err := runtime.FetcherForNextLink(ctx, client.internal.Pipeline(), "", func(ctx context.Context) (*policy.Request, error) {
				return client.listCreateRequest(ctx, options)
			}, nil)
			if err != nil {
				return UsagesClientListResponse{}, err
			}
			return client.listHandleResponse(resp)
		},
	})
}

func (client *UsagesClient) listCreateRequest(ctx context.Context, options *UsagesClientListOptions) (*policy.Request, error) {
	urlPath := "/subscriptions/{subscriptionId}/providers/Microsoft.Widgets/locations/{location}/usages"
	req, err := runtime.NewRequest(ctx, http.MethodGet, runtime.JoinPaths(client.internal.Endpoint(), urlPath))
	return req, err
}

func (client *UsagesClient) listHandleResponse(resp *http.Response) (UsagesClientListResponse, error) {
	result := UsagesClientListResponse{}
	if err := runtime.UnmarshalAsJSON(resp, &result.UsageList); err != nil {
		return UsagesClientListResponse{}, err
	}
	return result, nil
}

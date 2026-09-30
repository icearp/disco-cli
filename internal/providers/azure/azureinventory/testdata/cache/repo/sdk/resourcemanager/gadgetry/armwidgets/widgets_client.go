package armwidgets

// A second armwidgets module under another resource-provider directory: the
// live SDK ships resources/armmanagedapplications and
// solutions/armmanagedapplications. Same basename, same client and op names,
// so only a "<rp>/<armX>" module key tells their ops apart.

func (client *WidgetsClient) NewListPager(options *WidgetsClientListOptions) *runtime.Pager[WidgetsClientListResponse] {
	return runtime.NewPager(runtime.PagingHandler[WidgetsClientListResponse]{
		Fetcher: func(ctx context.Context, page *WidgetsClientListResponse) (WidgetsClientListResponse, error) {
			resp, err := runtime.FetcherForNextLink(ctx, client.internal.Pipeline(), "", func(ctx context.Context) (*policy.Request, error) {
				return client.listCreateRequest(ctx, options)
			}, nil)
			if err != nil {
				return WidgetsClientListResponse{}, err
			}
			return client.listHandleResponse(resp)
		},
	})
}

func (client *WidgetsClient) listCreateRequest(ctx context.Context, options *WidgetsClientListOptions) (*policy.Request, error) {
	urlPath := "/subscriptions/{subscriptionId}/providers/Microsoft.Gadgetry/widgets"
	req, err := runtime.NewRequest(ctx, http.MethodGet, runtime.JoinPaths(client.internal.Endpoint(), urlPath))
	return req, err
}

func (client *WidgetsClient) listHandleResponse(resp *http.Response) (WidgetsClientListResponse, error) {
	result := WidgetsClientListResponse{}
	if err := runtime.UnmarshalAsJSON(resp, &result.WidgetListResult); err != nil {
		return WidgetsClientListResponse{}, err
	}
	return result, nil
}

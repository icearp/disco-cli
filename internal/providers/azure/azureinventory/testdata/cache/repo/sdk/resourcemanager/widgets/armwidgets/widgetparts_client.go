package armwidgets

// The older decode shape: the response struct holds the slice itself.
func (client *WidgetPartsClient) List(ctx context.Context, options *WidgetPartsClientListOptions) (WidgetPartsClientListResponse, error) {
	req, err := client.listCreateRequest(ctx, options)
	if err != nil {
		return WidgetPartsClientListResponse{}, err
	}
	httpResp, err := client.internal.Pipeline().Do(req)
	if err != nil {
		return WidgetPartsClientListResponse{}, err
	}
	return client.listHandleResponse(httpResp)
}

func (client *WidgetPartsClient) listCreateRequest(ctx context.Context, options *WidgetPartsClientListOptions) (*policy.Request, error) {
	urlPath := "/subscriptions/{subscriptionId}/resourceGroups/{resourceGroupName}/providers/Microsoft.Widgets/widgets/{widgetName}/parts"
	req, err := runtime.NewRequest(ctx, http.MethodGet, runtime.JoinPaths(client.internal.Endpoint(), urlPath))
	return req, err
}

func (client *WidgetPartsClient) listHandleResponse(resp *http.Response) (WidgetPartsClientListResponse, error) {
	result := WidgetPartsClientListResponse{}
	if err := runtime.UnmarshalAsJSON(resp, &result.WidgetPartArray); err != nil {
		return WidgetPartsClientListResponse{}, err
	}
	return result, nil
}

func (client *WidgetPartsClient) BeginCreateOrUpdate(ctx context.Context, options *WidgetPartsClientBeginCreateOrUpdateOptions) (*runtime.Poller[WidgetPartsClientCreateOrUpdateResponse], error) {
	resp, err := client.createOrUpdate(ctx, options)
	if err != nil {
		return nil, err
	}
	return runtime.NewPoller[WidgetPartsClientCreateOrUpdateResponse](resp, client.internal.Pipeline(), nil)
}

func (client *WidgetPartsClient) createOrUpdate(ctx context.Context, options *WidgetPartsClientBeginCreateOrUpdateOptions) (*http.Response, error) {
	req, err := client.createOrUpdateCreateRequest(ctx, options)
	if err != nil {
		return nil, err
	}
	return client.internal.Pipeline().Do(req)
}

func (client *WidgetPartsClient) createOrUpdateCreateRequest(ctx context.Context, options *WidgetPartsClientCreateOrUpdateOptions) (*policy.Request, error) {
	urlPath := "/subscriptions/{subscriptionId}/resourceGroups/{resourceGroupName}/providers/Microsoft.Widgets/widgets/{widgetName}/parts/{partName}"
	req, err := runtime.NewRequest(ctx, http.MethodPut, runtime.JoinPaths(client.internal.Endpoint(), urlPath))
	return req, err
}

// An alternate lister of the WidgetPart the widgets/parts entry writes; its result also holds a summaries slice.
func (client *WidgetPartsClient) NewListAllPager(options *WidgetPartsClientListAllOptions) *runtime.Pager[WidgetPartsClientListAllResponse] {
	return runtime.NewPager(runtime.PagingHandler[WidgetPartsClientListAllResponse]{
		Fetcher: func(ctx context.Context, page *WidgetPartsClientListAllResponse) (WidgetPartsClientListAllResponse, error) {
			resp, err := runtime.FetcherForNextLink(ctx, client.internal.Pipeline(), "", func(ctx context.Context) (*policy.Request, error) {
				return client.listAllCreateRequest(ctx, options)
			}, nil)
			if err != nil {
				return WidgetPartsClientListAllResponse{}, err
			}
			return client.listAllHandleResponse(resp)
		},
	})
}

func (client *WidgetPartsClient) listAllCreateRequest(ctx context.Context, options *WidgetPartsClientListAllOptions) (*policy.Request, error) {
	urlPath := "/subscriptions/{subscriptionId}/providers/Microsoft.Widgets/parts"
	req, err := runtime.NewRequest(ctx, http.MethodGet, runtime.JoinPaths(client.internal.Endpoint(), urlPath))
	return req, err
}

func (client *WidgetPartsClient) listAllHandleResponse(resp *http.Response) (WidgetPartsClientListAllResponse, error) {
	result := WidgetPartsClientListAllResponse{}
	if err := runtime.UnmarshalAsJSON(resp, &result.PartListAllResult); err != nil {
		return WidgetPartsClientListAllResponse{}, err
	}
	return result, nil
}

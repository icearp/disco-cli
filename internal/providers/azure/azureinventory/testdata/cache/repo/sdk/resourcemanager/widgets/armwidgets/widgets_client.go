package armwidgets

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
	urlPath := "/subscriptions/{subscriptionId}/providers/Microsoft.Widgets/widgets"
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

func (client *WidgetsClient) NewListByResourceGroupPager(options *WidgetsClientListByResourceGroupOptions) *runtime.Pager[WidgetsClientListByResourceGroupResponse] {
	return runtime.NewPager(runtime.PagingHandler[WidgetsClientListByResourceGroupResponse]{
		Fetcher: func(ctx context.Context, page *WidgetsClientListByResourceGroupResponse) (WidgetsClientListByResourceGroupResponse, error) {
			resp, err := runtime.FetcherForNextLink(ctx, client.internal.Pipeline(), "", func(ctx context.Context) (*policy.Request, error) {
				return client.listByResourceGroupCreateRequest(ctx, options)
			}, nil)
			if err != nil {
				return WidgetsClientListByResourceGroupResponse{}, err
			}
			return client.listByResourceGroupHandleResponse(resp)
		},
	})
}

func (client *WidgetsClient) listByResourceGroupCreateRequest(ctx context.Context, options *WidgetsClientListByResourceGroupOptions) (*policy.Request, error) {
	urlPath := "/subscriptions/{subscriptionId}/resourceGroups/{resourceGroupName}/providers/Microsoft.Widgets/widgets"
	req, err := runtime.NewRequest(ctx, http.MethodGet, runtime.JoinPaths(client.internal.Endpoint(), urlPath))
	return req, err
}

func (client *WidgetsClient) listByResourceGroupHandleResponse(resp *http.Response) (WidgetsClientListByResourceGroupResponse, error) {
	result := WidgetsClientListByResourceGroupResponse{}
	if err := runtime.UnmarshalAsJSON(resp, &result.WidgetListResult); err != nil {
		return WidgetsClientListByResourceGroupResponse{}, err
	}
	return result, nil
}

func (client *WidgetsClient) Get(ctx context.Context, options *WidgetsClientGetOptions) (WidgetsClientGetResponse, error) {
	req, err := client.getCreateRequest(ctx, options)
	if err != nil {
		return WidgetsClientGetResponse{}, err
	}
	httpResp, err := client.internal.Pipeline().Do(req)
	if err != nil {
		return WidgetsClientGetResponse{}, err
	}
	return client.getHandleResponse(httpResp)
}

func (client *WidgetsClient) getCreateRequest(ctx context.Context, options *WidgetsClientGetOptions) (*policy.Request, error) {
	urlPath := "/subscriptions/{subscriptionId}/resourceGroups/{resourceGroupName}/providers/Microsoft.Widgets/widgets/{widgetName}"
	req, err := runtime.NewRequest(ctx, http.MethodGet, runtime.JoinPaths(client.internal.Endpoint(), urlPath))
	return req, err
}

func (client *WidgetsClient) getHandleResponse(resp *http.Response) (WidgetsClientGetResponse, error) {
	result := WidgetsClientGetResponse{}
	if err := runtime.UnmarshalAsJSON(resp, &result.Widget); err != nil {
		return WidgetsClientGetResponse{}, err
	}
	return result, nil
}

// Reached through the unexported createOrUpdate: builders are followed, not named.
func (client *WidgetsClient) BeginCreateOrUpdate(ctx context.Context, options *WidgetsClientBeginCreateOrUpdateOptions) (*runtime.Poller[WidgetsClientCreateOrUpdateResponse], error) {
	resp, err := client.createOrUpdate(ctx, options)
	if err != nil {
		return nil, err
	}
	return runtime.NewPoller[WidgetsClientCreateOrUpdateResponse](resp, client.internal.Pipeline(), nil)
}

func (client *WidgetsClient) createOrUpdate(ctx context.Context, options *WidgetsClientBeginCreateOrUpdateOptions) (*http.Response, error) {
	req, err := client.createOrUpdateCreateRequest(ctx, options)
	if err != nil {
		return nil, err
	}
	return client.internal.Pipeline().Do(req)
}

func (client *WidgetsClient) createOrUpdateCreateRequest(ctx context.Context, options *WidgetsClientCreateOrUpdateOptions) (*policy.Request, error) {
	urlPath := "/subscriptions/{subscriptionId}/resourceGroups/{resourceGroupName}/providers/Microsoft.Widgets/widgets/{widgetName}"
	req, err := runtime.NewRequest(ctx, http.MethodPut, runtime.JoinPaths(client.internal.Endpoint(), urlPath))
	return req, err
}

func (client *WidgetsClient) BeginDelete(ctx context.Context, options *WidgetsClientBeginDeleteOptions) (*runtime.Poller[WidgetsClientDeleteResponse], error) {
	resp, err := client.deleteOperation(ctx, options)
	if err != nil {
		return nil, err
	}
	return runtime.NewPoller[WidgetsClientDeleteResponse](resp, client.internal.Pipeline(), nil)
}

func (client *WidgetsClient) deleteOperation(ctx context.Context, options *WidgetsClientBeginDeleteOptions) (*http.Response, error) {
	req, err := client.deleteCreateRequest(ctx, options)
	if err != nil {
		return nil, err
	}
	return client.internal.Pipeline().Do(req)
}

func (client *WidgetsClient) deleteCreateRequest(ctx context.Context, options *WidgetsClientDeleteOptions) (*policy.Request, error) {
	urlPath := "/subscriptions/{subscriptionId}/resourceGroups/{resourceGroupName}/providers/Microsoft.Widgets/widgets/{widgetName}"
	req, err := runtime.NewRequest(ctx, http.MethodDelete, runtime.JoinPaths(client.internal.Endpoint(), urlPath))
	return req, err
}

// A GET answering one model (several slices, no list shape) on a static path below an item: a singleton read, never a lister.
func (client *WidgetsClient) InstanceView(ctx context.Context, options *WidgetsClientInstanceViewOptions) (WidgetsClientInstanceViewResponse, error) {
	req, err := client.instanceViewCreateRequest(ctx, options)
	if err != nil {
		return WidgetsClientInstanceViewResponse{}, err
	}
	httpResp, err := client.internal.Pipeline().Do(req)
	if err != nil {
		return WidgetsClientInstanceViewResponse{}, err
	}
	return client.instanceViewHandleResponse(httpResp)
}

func (client *WidgetsClient) instanceViewCreateRequest(ctx context.Context, options *WidgetsClientInstanceViewOptions) (*policy.Request, error) {
	urlPath := "/subscriptions/{subscriptionId}/resourceGroups/{resourceGroupName}/providers/Microsoft.Widgets/widgets/{widgetName}/instanceView"
	req, err := runtime.NewRequest(ctx, http.MethodGet, runtime.JoinPaths(client.internal.Endpoint(), urlPath))
	return req, err
}

func (client *WidgetsClient) instanceViewHandleResponse(resp *http.Response) (WidgetsClientInstanceViewResponse, error) {
	result := WidgetsClientInstanceViewResponse{}
	if err := runtime.UnmarshalAsJSON(resp, &result.WidgetInstanceView); err != nil {
		return WidgetsClientInstanceViewResponse{}, err
	}
	return result, nil
}

// One slice of models, but the model carries its own ID: one model, not a list result.
func (client *WidgetsClient) Validate(ctx context.Context, options *WidgetsClientValidateOptions) (WidgetsClientValidateResponse, error) {
	req, err := client.validateCreateRequest(ctx, options)
	if err != nil {
		return WidgetsClientValidateResponse{}, err
	}
	httpResp, err := client.internal.Pipeline().Do(req)
	if err != nil {
		return WidgetsClientValidateResponse{}, err
	}
	return client.validateHandleResponse(httpResp)
}

func (client *WidgetsClient) validateCreateRequest(ctx context.Context, options *WidgetsClientValidateOptions) (*policy.Request, error) {
	urlPath := "/subscriptions/{subscriptionId}/resourceGroups/{resourceGroupName}/providers/Microsoft.Widgets/widgets/{widgetName}/validation"
	req, err := runtime.NewRequest(ctx, http.MethodGet, runtime.JoinPaths(client.internal.Endpoint(), urlPath))
	return req, err
}

func (client *WidgetsClient) validateHandleResponse(resp *http.Response) (WidgetsClientValidateResponse, error) {
	result := WidgetsClientValidateResponse{}
	if err := runtime.UnmarshalAsJSON(resp, &result.WidgetValidation); err != nil {
		return WidgetsClientValidateResponse{}, err
	}
	return result, nil
}

// An exported method ending in CreateRequest returns no *policy.Request: not a builder.
func (client *WidgetsClient) ValidateWidgetCreateRequest(ctx context.Context) error {
	return nil
}

// The builder sets urlPath from a variable: accounted as no-request-path.
func (client *WidgetsClient) GetStream(ctx context.Context, options *WidgetsClientGetStreamOptions) (WidgetsClientGetStreamResponse, error) {
	req, err := client.getStreamCreateRequest(ctx, options)
	if err != nil {
		return WidgetsClientGetStreamResponse{}, err
	}
	httpResp, err := client.internal.Pipeline().Do(req)
	if err != nil {
		return WidgetsClientGetStreamResponse{}, err
	}
	return WidgetsClientGetStreamResponse{}, err
}

func (client *WidgetsClient) getStreamCreateRequest(ctx context.Context, options *WidgetsClientGetStreamOptions) (*policy.Request, error) {
	urlPath := client.streamPath
	req, err := runtime.NewRequest(ctx, http.MethodGet, runtime.JoinPaths(client.internal.Endpoint(), urlPath))
	return req, err
}

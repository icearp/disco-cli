package armwidgets

func (client *WidgetsClient) NewListPager(options *WidgetsClientListOptions) *runtime.Pager[WidgetsClientListResponse] {
	return nil
}

func (client *WidgetsClient) listCreateRequest(ctx context.Context, nextLink string, options *WidgetsClientListOptions) (*policy.Request, error) {
	urlPath := "/subscriptions/{subscriptionId}/providers/Microsoft.Widgets/widgets"
	req, err := runtime.NewRequest(ctx, http.MethodGet, runtime.JoinPaths(client.internal.Endpoint(), urlPath))
	return req, err
}

func (client *WidgetsClient) NewListByResourceGroupPager(resourceGroupName string, options *WidgetsClientListByResourceGroupOptions) *runtime.Pager[WidgetsClientListByResourceGroupResponse] {
	return nil
}

func (client *WidgetsClient) listByResourceGroupCreateRequest(ctx context.Context, nextLink string, resourceGroupName string, options *WidgetsClientListByResourceGroupOptions) (*policy.Request, error) {
	urlPath := "/subscriptions/{subscriptionId}/resourcegroups/{resourceGroupName}/providers/Microsoft.Widgets/widgets"
	req, err := runtime.NewRequest(ctx, http.MethodGet, runtime.JoinPaths(client.internal.Endpoint(), urlPath))
	return req, err
}

func (client *WidgetsClient) getCreateRequest(ctx context.Context, resourceGroupName string, widgetName string, options *WidgetsClientGetOptions) (*policy.Request, error) {
	urlPath := "/subscriptions/{subscriptionId}/resourceGroups/{resourceGroupName}/providers/Microsoft.Widgets/widgets/{widgetName}"
	req, err := runtime.NewRequest(ctx, http.MethodGet, runtime.JoinPaths(client.internal.Endpoint(), urlPath))
	return req, err
}

func (client *WidgetsClient) createOrUpdateCreateRequest(ctx context.Context, resourceGroupName string, widgetName string, parameters Widget, options *WidgetsClientBeginCreateOrUpdateOptions) (*policy.Request, error) {
	urlPath := "/subscriptions/{subscriptionId}/resourceGroups/{resourceGroupName}/providers/Microsoft.Widgets/widgets/{widgetName}"
	req, err := runtime.NewRequest(ctx, http.MethodPut, runtime.JoinPaths(client.internal.Endpoint(), urlPath))
	return req, err
}

func (client *WidgetsClient) deleteCreateRequest(ctx context.Context, resourceGroupName string, widgetName string, options *WidgetsClientBeginDeleteOptions) (*policy.Request, error) {
	urlPath := "/subscriptions/{subscriptionId}/resourceGroups/{resourceGroupName}/providers/Microsoft.Widgets/widgets/{widgetName}"
	req, err := runtime.NewRequest(ctx, http.MethodDelete, runtime.JoinPaths(client.internal.Endpoint(), urlPath))
	return req, err
}

func (client *WidgetsClient) instanceViewCreateRequest(ctx context.Context, resourceGroupName string, widgetName string, options *WidgetsClientInstanceViewOptions) (*policy.Request, error) {
	urlPath := "/subscriptions/{subscriptionId}/resourceGroups/{resourceGroupName}/providers/Microsoft.Widgets/widgets/{widgetName}/instanceView"
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

func (client *WidgetsClient) listByResourceGroupHandleResponse(resp *http.Response) (WidgetsClientListByResourceGroupResponse, error) {
	result := WidgetsClientListByResourceGroupResponse{}
	if err := runtime.UnmarshalAsJSON(resp, &result.WidgetListResult); err != nil {
		return WidgetsClientListByResourceGroupResponse{}, err
	}
	return result, nil
}

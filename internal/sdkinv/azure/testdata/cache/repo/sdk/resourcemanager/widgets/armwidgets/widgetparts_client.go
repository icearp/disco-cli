package armwidgets

func (client *WidgetPartsClient) List(ctx context.Context, resourceGroupName string, widgetName string, options *WidgetPartsClientListOptions) (WidgetPartsClientListResponse, error) {
	return WidgetPartsClientListResponse{}, nil
}

func (client *WidgetPartsClient) listCreateRequest(ctx context.Context, resourceGroupName string, widgetName string, options *WidgetPartsClientListOptions) (*policy.Request, error) {
	urlPath := "/subscriptions/{subscriptionId}/resourceGroups/{resourceGroupName}/providers/Microsoft.Widgets/widgets/{widgetName}/parts"
	req, err := runtime.NewRequest(ctx, http.MethodGet, runtime.JoinPaths(client.internal.Endpoint(), urlPath))
	return req, err
}

func (client *WidgetPartsClient) createOrUpdateCreateRequest(ctx context.Context, resourceGroupName string, widgetName string, partName string, options *WidgetPartsClientCreateOrUpdateOptions) (*policy.Request, error) {
	urlPath := "/subscriptions/{subscriptionId}/resourceGroups/{resourceGroupName}/providers/Microsoft.Widgets/widgets/{widgetName}/parts/{partName}"
	req, err := runtime.NewRequest(ctx, http.MethodPut, runtime.JoinPaths(client.internal.Endpoint(), urlPath))
	return req, err
}

func (client *WidgetPartsClient) BeginCreateOrUpdate(ctx context.Context) error {
	return nil
}

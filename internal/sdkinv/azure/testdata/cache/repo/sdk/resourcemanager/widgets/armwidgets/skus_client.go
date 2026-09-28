package armwidgets

func (client *SKUsClient) listCreateRequest(ctx context.Context, options *SKUsClientListOptions) (*policy.Request, error) {
	urlPath := "/subscriptions/{subscriptionId}/providers/Microsoft.Widgets/skus"
	req, err := runtime.NewRequest(ctx, http.MethodGet, runtime.JoinPaths(client.internal.Endpoint(), urlPath))
	return req, err
}

func (client *SKUsClient) getCreateRequest(ctx context.Context, skuName string, options *SKUsClientGetOptions) (*policy.Request, error) {
	urlPath := "/subscriptions/{subscriptionId}/providers/Microsoft.Widgets/skus/{skuName}"
	req, err := runtime.NewRequest(ctx, http.MethodGet, runtime.JoinPaths(client.internal.Endpoint(), urlPath))
	return req, err
}

func (client *SKUsClient) NewListPager(ctx context.Context) error {
	return nil
}

func (client *SKUsClient) Get(ctx context.Context) error {
	return nil
}

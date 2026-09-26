package armwidgets

func (client *OperationsClient) listCreateRequest(ctx context.Context, options *OperationsClientListOptions) (*policy.Request, error) {
	urlPath := "/providers/Microsoft.Widgets/operations"
	req, err := runtime.NewRequest(ctx, http.MethodGet, runtime.JoinPaths(client.internal.Endpoint(), urlPath))
	return req, err
}

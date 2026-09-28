package armwidgets

func (client *TenantThingsClient) NewListPager(options *TenantThingsClientListOptions) *runtime.Pager[TenantThingsClientListResponse] {
	return nil
}

func (client *TenantThingsClient) listCreateRequest(ctx context.Context, nextLink string, options *TenantThingsClientListOptions) (*policy.Request, error) {
	urlPath := "/providers/Microsoft.Widgets/tenantThings"
	req, err := runtime.NewRequest(ctx, http.MethodGet, runtime.JoinPaths(client.internal.Endpoint(), urlPath))
	return req, err
}

func (client *TenantThingsClient) createCreateRequest(ctx context.Context, thingName string, options *TenantThingsClientCreateOptions) (*policy.Request, error) {
	urlPath := "/providers/Microsoft.Widgets/tenantThings/{thingName}"
	req, err := runtime.NewRequest(ctx, http.MethodPut, runtime.JoinPaths(client.internal.Endpoint(), urlPath))
	return req, err
}

func (client *TenantThingsClient) checkNameCreateRequest(ctx context.Context, options *TenantThingsClientCheckNameOptions) (*policy.Request, error) {
	urlPath := "/providers/Microsoft.Widgets/checkNameAvailability"
	req, err := runtime.NewRequest(ctx, http.MethodPost, runtime.JoinPaths(client.internal.Endpoint(), urlPath))
	return req, err
}

func (client *TenantThingsClient) Create(ctx context.Context) error {
	return nil
}

func (client *TenantThingsClient) CheckName(ctx context.Context) error {
	return nil
}

// Rename has no request builder: accounting drops it as no-request-builder.
func (client *TenantThingsClient) Rename(ctx context.Context) error {
	return nil
}

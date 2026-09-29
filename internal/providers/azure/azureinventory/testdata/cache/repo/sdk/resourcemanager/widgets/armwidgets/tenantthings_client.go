package armwidgets

func (client *TenantThingsClient) NewListPager(options *TenantThingsClientListOptions) *runtime.Pager[TenantThingsClientListResponse] {
	return runtime.NewPager(runtime.PagingHandler[TenantThingsClientListResponse]{
		Fetcher: func(ctx context.Context, page *TenantThingsClientListResponse) (TenantThingsClientListResponse, error) {
			resp, err := runtime.FetcherForNextLink(ctx, client.internal.Pipeline(), "", func(ctx context.Context) (*policy.Request, error) {
				return client.listCreateRequest(ctx, options)
			}, nil)
			if err != nil {
				return TenantThingsClientListResponse{}, err
			}
			return client.listHandleResponse(resp)
		},
	})
}

func (client *TenantThingsClient) listCreateRequest(ctx context.Context, options *TenantThingsClientListOptions) (*policy.Request, error) {
	urlPath := "/providers/Microsoft.Widgets/tenantThings"
	req, err := runtime.NewRequest(ctx, http.MethodGet, runtime.JoinPaths(client.internal.Endpoint(), urlPath))
	return req, err
}

func (client *TenantThingsClient) listHandleResponse(resp *http.Response) (TenantThingsClientListResponse, error) {
	result := TenantThingsClientListResponse{}
	if err := runtime.UnmarshalAsJSON(resp, &result.TenantThingListResult); err != nil {
		return TenantThingsClientListResponse{}, err
	}
	return result, nil
}

func (client *TenantThingsClient) Create(ctx context.Context, options *TenantThingsClientCreateOptions) (TenantThingsClientCreateResponse, error) {
	req, err := client.createCreateRequest(ctx, options)
	if err != nil {
		return TenantThingsClientCreateResponse{}, err
	}
	httpResp, err := client.internal.Pipeline().Do(req)
	if err != nil {
		return TenantThingsClientCreateResponse{}, err
	}
	return client.createHandleResponse(httpResp)
}

func (client *TenantThingsClient) createCreateRequest(ctx context.Context, options *TenantThingsClientCreateOptions) (*policy.Request, error) {
	urlPath := "/providers/Microsoft.Widgets/tenantThings/{thingName}"
	req, err := runtime.NewRequest(ctx, http.MethodPut, runtime.JoinPaths(client.internal.Endpoint(), urlPath))
	return req, err
}

func (client *TenantThingsClient) createHandleResponse(resp *http.Response) (TenantThingsClientCreateResponse, error) {
	result := TenantThingsClientCreateResponse{}
	if err := runtime.UnmarshalAsJSON(resp, &result.TenantThing); err != nil {
		return TenantThingsClientCreateResponse{}, err
	}
	return result, nil
}

func (client *TenantThingsClient) CheckName(ctx context.Context, options *TenantThingsClientCheckNameOptions) (TenantThingsClientCheckNameResponse, error) {
	req, err := client.checkNameCreateRequest(ctx, options)
	if err != nil {
		return TenantThingsClientCheckNameResponse{}, err
	}
	httpResp, err := client.internal.Pipeline().Do(req)
	if err != nil {
		return TenantThingsClientCheckNameResponse{}, err
	}
	return TenantThingsClientCheckNameResponse{}, err
}

func (client *TenantThingsClient) checkNameCreateRequest(ctx context.Context, options *TenantThingsClientCheckNameOptions) (*policy.Request, error) {
	urlPath := "/providers/Microsoft.Widgets/checkNameAvailability"
	req, err := runtime.NewRequest(ctx, http.MethodPost, runtime.JoinPaths(client.internal.Endpoint(), urlPath))
	return req, err
}

// Rename has no request builder: accounting drops it as no-request-builder.
func (client *TenantThingsClient) Rename(ctx context.Context) error {
	return nil
}

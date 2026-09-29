package armwidgets

// The only GET on the written partners collection answers one model: promoted to its lister.
func (client *PartnersClient) Get(ctx context.Context, options *PartnersClientGetOptions) (PartnersClientGetResponse, error) {
	req, err := client.getCreateRequest(ctx, options)
	if err != nil {
		return PartnersClientGetResponse{}, err
	}
	httpResp, err := client.internal.Pipeline().Do(req)
	if err != nil {
		return PartnersClientGetResponse{}, err
	}
	return client.getHandleResponse(httpResp)
}

func (client *PartnersClient) getCreateRequest(ctx context.Context, options *PartnersClientGetOptions) (*policy.Request, error) {
	urlPath := "/providers/Microsoft.Widgets/partners"
	req, err := runtime.NewRequest(ctx, http.MethodGet, runtime.JoinPaths(client.internal.Endpoint(), urlPath))
	return req, err
}

func (client *PartnersClient) getHandleResponse(resp *http.Response) (PartnersClientGetResponse, error) {
	result := PartnersClientGetResponse{}
	if err := runtime.UnmarshalAsJSON(resp, &result.Partner); err != nil {
		return PartnersClientGetResponse{}, err
	}
	return result, nil
}

func (client *PartnersClient) Create(ctx context.Context, options *PartnersClientCreateOptions) (PartnersClientCreateResponse, error) {
	req, err := client.createCreateRequest(ctx, options)
	if err != nil {
		return PartnersClientCreateResponse{}, err
	}
	httpResp, err := client.internal.Pipeline().Do(req)
	if err != nil {
		return PartnersClientCreateResponse{}, err
	}
	return client.createHandleResponse(httpResp)
}

func (client *PartnersClient) createCreateRequest(ctx context.Context, options *PartnersClientCreateOptions) (*policy.Request, error) {
	urlPath := "/providers/Microsoft.Widgets/partners/{partnerId}"
	req, err := runtime.NewRequest(ctx, http.MethodPut, runtime.JoinPaths(client.internal.Endpoint(), urlPath))
	return req, err
}

func (client *PartnersClient) createHandleResponse(resp *http.Response) (PartnersClientCreateResponse, error) {
	result := PartnersClientCreateResponse{}
	if err := runtime.UnmarshalAsJSON(resp, &result.Partner); err != nil {
		return PartnersClientCreateResponse{}, err
	}
	return result, nil
}

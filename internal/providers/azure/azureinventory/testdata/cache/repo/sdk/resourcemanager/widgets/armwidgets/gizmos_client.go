package armwidgets

// Polymorphic elements: Value []GizmoClassification resolves through GetGizmo() *Gizmo.
func (client *GizmosClient) NewListPager(options *GizmosClientListOptions) *runtime.Pager[GizmosClientListResponse] {
	return runtime.NewPager(runtime.PagingHandler[GizmosClientListResponse]{
		Fetcher: func(ctx context.Context, page *GizmosClientListResponse) (GizmosClientListResponse, error) {
			resp, err := runtime.FetcherForNextLink(ctx, client.internal.Pipeline(), "", func(ctx context.Context) (*policy.Request, error) {
				return client.listCreateRequest(ctx, options)
			}, nil)
			if err != nil {
				return GizmosClientListResponse{}, err
			}
			return client.listHandleResponse(resp)
		},
	})
}

func (client *GizmosClient) listCreateRequest(ctx context.Context, options *GizmosClientListOptions) (*policy.Request, error) {
	urlPath := "/subscriptions/{subscriptionId}/providers/Microsoft.Widgets/gizmos"
	req, err := runtime.NewRequest(ctx, http.MethodGet, runtime.JoinPaths(client.internal.Endpoint(), urlPath))
	return req, err
}

func (client *GizmosClient) listHandleResponse(resp *http.Response) (GizmosClientListResponse, error) {
	result := GizmosClientListResponse{}
	if err := runtime.UnmarshalAsJSON(resp, &result.GizmoList); err != nil {
		return GizmosClientListResponse{}, err
	}
	return result, nil
}

func (client *GizmosClient) Get(ctx context.Context, options *GizmosClientGetOptions) (GizmosClientGetResponse, error) {
	req, err := client.getCreateRequest(ctx, options)
	if err != nil {
		return GizmosClientGetResponse{}, err
	}
	httpResp, err := client.internal.Pipeline().Do(req)
	if err != nil {
		return GizmosClientGetResponse{}, err
	}
	return client.getHandleResponse(httpResp)
}

func (client *GizmosClient) getCreateRequest(ctx context.Context, options *GizmosClientGetOptions) (*policy.Request, error) {
	urlPath := "/subscriptions/{subscriptionId}/providers/Microsoft.Widgets/gizmos/{gizmoName}"
	req, err := runtime.NewRequest(ctx, http.MethodGet, runtime.JoinPaths(client.internal.Endpoint(), urlPath))
	return req, err
}

func (client *GizmosClient) getHandleResponse(resp *http.Response) (GizmosClientGetResponse, error) {
	result := GizmosClientGetResponse{}
	if err := runtime.UnmarshalAsJSON(resp, &result.GizmoClassification); err != nil {
		return GizmosClientGetResponse{}, err
	}
	return result, nil
}

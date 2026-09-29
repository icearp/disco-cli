package armwidgets

// Listed through a location and also per subscription: no scope pair, so the envelope makes it a resource.
func (client *QuotasClient) NewListPager(options *QuotasClientListOptions) *runtime.Pager[QuotasClientListResponse] {
	return runtime.NewPager(runtime.PagingHandler[QuotasClientListResponse]{
		Fetcher: func(ctx context.Context, page *QuotasClientListResponse) (QuotasClientListResponse, error) {
			resp, err := runtime.FetcherForNextLink(ctx, client.internal.Pipeline(), "", func(ctx context.Context) (*policy.Request, error) {
				return client.listCreateRequest(ctx, options)
			}, nil)
			if err != nil {
				return QuotasClientListResponse{}, err
			}
			return client.listHandleResponse(resp)
		},
	})
}

func (client *QuotasClient) listCreateRequest(ctx context.Context, options *QuotasClientListOptions) (*policy.Request, error) {
	urlPath := "/subscriptions/{subscriptionId}/providers/Microsoft.Widgets/locations/{location}/quotas"
	req, err := runtime.NewRequest(ctx, http.MethodGet, runtime.JoinPaths(client.internal.Endpoint(), urlPath))
	return req, err
}

func (client *QuotasClient) listHandleResponse(resp *http.Response) (QuotasClientListResponse, error) {
	result := QuotasClientListResponse{}
	if err := runtime.UnmarshalAsJSON(resp, &result.QuotaList); err != nil {
		return QuotasClientListResponse{}, err
	}
	return result, nil
}

func (client *QuotasClient) NewListBySubscriptionPager(options *QuotasClientListBySubscriptionOptions) *runtime.Pager[QuotasClientListBySubscriptionResponse] {
	return runtime.NewPager(runtime.PagingHandler[QuotasClientListBySubscriptionResponse]{
		Fetcher: func(ctx context.Context, page *QuotasClientListBySubscriptionResponse) (QuotasClientListBySubscriptionResponse, error) {
			resp, err := runtime.FetcherForNextLink(ctx, client.internal.Pipeline(), "", func(ctx context.Context) (*policy.Request, error) {
				return client.listBySubscriptionCreateRequest(ctx, options)
			}, nil)
			if err != nil {
				return QuotasClientListBySubscriptionResponse{}, err
			}
			return client.listBySubscriptionHandleResponse(resp)
		},
	})
}

func (client *QuotasClient) listBySubscriptionCreateRequest(ctx context.Context, options *QuotasClientListBySubscriptionOptions) (*policy.Request, error) {
	urlPath := "/subscriptions/{subscriptionId}/providers/Microsoft.Widgets/quotas"
	req, err := runtime.NewRequest(ctx, http.MethodGet, runtime.JoinPaths(client.internal.Endpoint(), urlPath))
	return req, err
}

func (client *QuotasClient) listBySubscriptionHandleResponse(resp *http.Response) (QuotasClientListBySubscriptionResponse, error) {
	result := QuotasClientListBySubscriptionResponse{}
	if err := runtime.UnmarshalAsJSON(resp, &result.QuotaList); err != nil {
		return QuotasClientListBySubscriptionResponse{}, err
	}
	return result, nil
}

func (client *QuotasClient) Get(ctx context.Context, options *QuotasClientGetOptions) (QuotasClientGetResponse, error) {
	req, err := client.getCreateRequest(ctx, options)
	if err != nil {
		return QuotasClientGetResponse{}, err
	}
	httpResp, err := client.internal.Pipeline().Do(req)
	if err != nil {
		return QuotasClientGetResponse{}, err
	}
	return client.getHandleResponse(httpResp)
}

func (client *QuotasClient) getCreateRequest(ctx context.Context, options *QuotasClientGetOptions) (*policy.Request, error) {
	urlPath := "/subscriptions/{subscriptionId}/providers/Microsoft.Widgets/quotas/{quotaName}"
	req, err := runtime.NewRequest(ctx, http.MethodGet, runtime.JoinPaths(client.internal.Endpoint(), urlPath))
	return req, err
}

func (client *QuotasClient) getHandleResponse(resp *http.Response) (QuotasClientGetResponse, error) {
	result := QuotasClientGetResponse{}
	if err := runtime.UnmarshalAsJSON(resp, &result.Quota); err != nil {
		return QuotasClientGetResponse{}, err
	}
	return result, nil
}

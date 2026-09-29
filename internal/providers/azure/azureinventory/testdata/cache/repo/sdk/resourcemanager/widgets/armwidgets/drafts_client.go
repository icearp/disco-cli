package armwidgets

// draft is itself an instance, so draft/testJob is the test job, not an id of a draft.
func (client *DraftsClient) GetDraft(ctx context.Context, options *DraftsClientGetDraftOptions) (DraftsClientGetDraftResponse, error) {
	req, err := client.getDraftCreateRequest(ctx, options)
	if err != nil {
		return DraftsClientGetDraftResponse{}, err
	}
	httpResp, err := client.internal.Pipeline().Do(req)
	if err != nil {
		return DraftsClientGetDraftResponse{}, err
	}
	return client.getDraftHandleResponse(httpResp)
}

func (client *DraftsClient) getDraftCreateRequest(ctx context.Context, options *DraftsClientGetDraftOptions) (*policy.Request, error) {
	urlPath := "/subscriptions/{subscriptionId}/resourceGroups/{resourceGroupName}/providers/Microsoft.Widgets/widgets/{widgetName}/draft"
	req, err := runtime.NewRequest(ctx, http.MethodGet, runtime.JoinPaths(client.internal.Endpoint(), urlPath))
	return req, err
}

func (client *DraftsClient) getDraftHandleResponse(resp *http.Response) (DraftsClientGetDraftResponse, error) {
	result := DraftsClientGetDraftResponse{}
	if err := runtime.UnmarshalAsJSON(resp, &result.Draft); err != nil {
		return DraftsClientGetDraftResponse{}, err
	}
	return result, nil
}

func (client *DraftsClient) CreateTestJob(ctx context.Context, options *DraftsClientCreateTestJobOptions) (DraftsClientCreateTestJobResponse, error) {
	req, err := client.createTestJobCreateRequest(ctx, options)
	if err != nil {
		return DraftsClientCreateTestJobResponse{}, err
	}
	httpResp, err := client.internal.Pipeline().Do(req)
	if err != nil {
		return DraftsClientCreateTestJobResponse{}, err
	}
	return client.createTestJobHandleResponse(httpResp)
}

func (client *DraftsClient) createTestJobCreateRequest(ctx context.Context, options *DraftsClientCreateTestJobOptions) (*policy.Request, error) {
	urlPath := "/subscriptions/{subscriptionId}/resourceGroups/{resourceGroupName}/providers/Microsoft.Widgets/widgets/{widgetName}/draft/testJob"
	req, err := runtime.NewRequest(ctx, http.MethodPut, runtime.JoinPaths(client.internal.Endpoint(), urlPath))
	return req, err
}

func (client *DraftsClient) createTestJobHandleResponse(resp *http.Response) (DraftsClientCreateTestJobResponse, error) {
	result := DraftsClientCreateTestJobResponse{}
	if err := runtime.UnmarshalAsJSON(resp, &result.TestJob); err != nil {
		return DraftsClientCreateTestJobResponse{}, err
	}
	return result, nil
}

func (client *DraftsClient) NewListStreamsPager(options *DraftsClientListStreamsOptions) *runtime.Pager[DraftsClientListStreamsResponse] {
	return runtime.NewPager(runtime.PagingHandler[DraftsClientListStreamsResponse]{
		Fetcher: func(ctx context.Context, page *DraftsClientListStreamsResponse) (DraftsClientListStreamsResponse, error) {
			resp, err := runtime.FetcherForNextLink(ctx, client.internal.Pipeline(), "", func(ctx context.Context) (*policy.Request, error) {
				return client.listStreamsCreateRequest(ctx, options)
			}, nil)
			if err != nil {
				return DraftsClientListStreamsResponse{}, err
			}
			return client.listStreamsHandleResponse(resp)
		},
	})
}

func (client *DraftsClient) listStreamsCreateRequest(ctx context.Context, options *DraftsClientListStreamsOptions) (*policy.Request, error) {
	urlPath := "/subscriptions/{subscriptionId}/resourceGroups/{resourceGroupName}/providers/Microsoft.Widgets/widgets/{widgetName}/draft/testJob/streams"
	req, err := runtime.NewRequest(ctx, http.MethodGet, runtime.JoinPaths(client.internal.Endpoint(), urlPath))
	return req, err
}

func (client *DraftsClient) listStreamsHandleResponse(resp *http.Response) (DraftsClientListStreamsResponse, error) {
	result := DraftsClientListStreamsResponse{}
	if err := runtime.UnmarshalAsJSON(resp, &result.StreamList); err != nil {
		return DraftsClientListStreamsResponse{}, err
	}
	return result, nil
}

func (client *DraftsClient) GetStream(ctx context.Context, options *DraftsClientGetStreamOptions) (DraftsClientGetStreamResponse, error) {
	req, err := client.getStreamCreateRequest(ctx, options)
	if err != nil {
		return DraftsClientGetStreamResponse{}, err
	}
	httpResp, err := client.internal.Pipeline().Do(req)
	if err != nil {
		return DraftsClientGetStreamResponse{}, err
	}
	return client.getStreamHandleResponse(httpResp)
}

func (client *DraftsClient) getStreamCreateRequest(ctx context.Context, options *DraftsClientGetStreamOptions) (*policy.Request, error) {
	urlPath := "/subscriptions/{subscriptionId}/resourceGroups/{resourceGroupName}/providers/Microsoft.Widgets/widgets/{widgetName}/draft/testJob/streams/{streamId}"
	req, err := runtime.NewRequest(ctx, http.MethodGet, runtime.JoinPaths(client.internal.Endpoint(), urlPath))
	return req, err
}

func (client *DraftsClient) getStreamHandleResponse(resp *http.Response) (DraftsClientGetStreamResponse, error) {
	result := DraftsClientGetStreamResponse{}
	if err := runtime.UnmarshalAsJSON(resp, &result.Stream); err != nil {
		return DraftsClientGetStreamResponse{}, err
	}
	return result, nil
}

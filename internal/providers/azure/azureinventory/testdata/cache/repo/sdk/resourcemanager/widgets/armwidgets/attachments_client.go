package armwidgets

// A providers pair before the namespace attaches the resource to whatever the caller names: extension scope.
func (client *AttachmentsClient) NewListPager(options *AttachmentsClientListOptions) *runtime.Pager[AttachmentsClientListResponse] {
	return runtime.NewPager(runtime.PagingHandler[AttachmentsClientListResponse]{
		Fetcher: func(ctx context.Context, page *AttachmentsClientListResponse) (AttachmentsClientListResponse, error) {
			resp, err := runtime.FetcherForNextLink(ctx, client.internal.Pipeline(), "", func(ctx context.Context) (*policy.Request, error) {
				return client.listCreateRequest(ctx, options)
			}, nil)
			if err != nil {
				return AttachmentsClientListResponse{}, err
			}
			return client.listHandleResponse(resp)
		},
	})
}

func (client *AttachmentsClient) listCreateRequest(ctx context.Context, options *AttachmentsClientListOptions) (*policy.Request, error) {
	urlPath := "/subscriptions/{subscriptionId}/resourceGroups/{resourceGroupName}/providers/{parentProviderNamespace}/{parentResourceType}/{parentResourceName}/providers/Microsoft.Widgets/attachments"
	req, err := runtime.NewRequest(ctx, http.MethodGet, runtime.JoinPaths(client.internal.Endpoint(), urlPath))
	return req, err
}

func (client *AttachmentsClient) listHandleResponse(resp *http.Response) (AttachmentsClientListResponse, error) {
	result := AttachmentsClientListResponse{}
	if err := runtime.UnmarshalAsJSON(resp, &result.AttachmentList); err != nil {
		return AttachmentsClientListResponse{}, err
	}
	return result, nil
}

func (client *AttachmentsClient) Create(ctx context.Context, options *AttachmentsClientCreateOptions) (AttachmentsClientCreateResponse, error) {
	req, err := client.createCreateRequest(ctx, options)
	if err != nil {
		return AttachmentsClientCreateResponse{}, err
	}
	httpResp, err := client.internal.Pipeline().Do(req)
	if err != nil {
		return AttachmentsClientCreateResponse{}, err
	}
	return AttachmentsClientCreateResponse{}, err
}

func (client *AttachmentsClient) createCreateRequest(ctx context.Context, options *AttachmentsClientCreateOptions) (*policy.Request, error) {
	urlPath := "/subscriptions/{subscriptionId}/resourceGroups/{resourceGroupName}/providers/{parentProviderNamespace}/{parentResourceType}/{parentResourceName}/providers/Microsoft.Widgets/attachments/{attachmentName}"
	req, err := runtime.NewRequest(ctx, http.MethodPut, runtime.JoinPaths(client.internal.Endpoint(), urlPath))
	return req, err
}

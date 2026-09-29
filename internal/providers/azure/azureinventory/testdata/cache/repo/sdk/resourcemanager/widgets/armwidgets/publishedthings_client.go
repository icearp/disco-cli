package armwidgets

// Not paged, and the list result carries SystemData beside its slice: still a list.
func (client *PublishedThingsClient) List(ctx context.Context, options *PublishedThingsClientListOptions) (PublishedThingsClientListResponse, error) {
	req, err := client.listCreateRequest(ctx, options)
	if err != nil {
		return PublishedThingsClientListResponse{}, err
	}
	httpResp, err := client.internal.Pipeline().Do(req)
	if err != nil {
		return PublishedThingsClientListResponse{}, err
	}
	return client.listHandleResponse(httpResp)
}

func (client *PublishedThingsClient) listCreateRequest(ctx context.Context, options *PublishedThingsClientListOptions) (*policy.Request, error) {
	urlPath := "/providers/Microsoft.Widgets/publishedThings"
	req, err := runtime.NewRequest(ctx, http.MethodGet, runtime.JoinPaths(client.internal.Endpoint(), urlPath))
	return req, err
}

func (client *PublishedThingsClient) listHandleResponse(resp *http.Response) (PublishedThingsClientListResponse, error) {
	result := PublishedThingsClientListResponse{}
	if err := runtime.UnmarshalAsJSON(resp, &result.PublishedThingList); err != nil {
		return PublishedThingsClientListResponse{}, err
	}
	return result, nil
}

func (client *PublishedThingsClient) Get(ctx context.Context, options *PublishedThingsClientGetOptions) (PublishedThingsClientGetResponse, error) {
	req, err := client.getCreateRequest(ctx, options)
	if err != nil {
		return PublishedThingsClientGetResponse{}, err
	}
	httpResp, err := client.internal.Pipeline().Do(req)
	if err != nil {
		return PublishedThingsClientGetResponse{}, err
	}
	return client.getHandleResponse(httpResp)
}

func (client *PublishedThingsClient) getCreateRequest(ctx context.Context, options *PublishedThingsClientGetOptions) (*policy.Request, error) {
	urlPath := "/providers/Microsoft.Widgets/publishedThings/{thingName}"
	req, err := runtime.NewRequest(ctx, http.MethodGet, runtime.JoinPaths(client.internal.Endpoint(), urlPath))
	return req, err
}

func (client *PublishedThingsClient) getHandleResponse(resp *http.Response) (PublishedThingsClientGetResponse, error) {
	result := PublishedThingsClientGetResponse{}
	if err := runtime.UnmarshalAsJSON(resp, &result.PublishedThing); err != nil {
		return PublishedThingsClientGetResponse{}, err
	}
	return result, nil
}

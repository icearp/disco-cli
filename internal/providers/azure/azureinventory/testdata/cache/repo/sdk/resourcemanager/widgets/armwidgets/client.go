package armwidgets

// The bare base client; a path of one param names no resource type.
func (client *Client) GetByID(ctx context.Context, options *ClientGetByIDOptions) (ClientGetByIDResponse, error) {
	req, err := client.getByIDCreateRequest(ctx, options)
	if err != nil {
		return ClientGetByIDResponse{}, err
	}
	httpResp, err := client.internal.Pipeline().Do(req)
	if err != nil {
		return ClientGetByIDResponse{}, err
	}
	return client.getByIDHandleResponse(httpResp)
}

func (client *Client) getByIDCreateRequest(ctx context.Context, options *ClientGetByIDOptions) (*policy.Request, error) {
	urlPath := "/{resourceId}"
	req, err := runtime.NewRequest(ctx, http.MethodGet, runtime.JoinPaths(client.internal.Endpoint(), urlPath))
	return req, err
}

func (client *Client) getByIDHandleResponse(resp *http.Response) (ClientGetByIDResponse, error) {
	result := ClientGetByIDResponse{}
	if err := runtime.UnmarshalAsJSON(resp, &result.GenericResource); err != nil {
		return ClientGetByIDResponse{}, err
	}
	return result, nil
}

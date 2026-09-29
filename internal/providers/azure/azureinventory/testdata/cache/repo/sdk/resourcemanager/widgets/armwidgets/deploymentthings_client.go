package armwidgets

// No providers segment: ARM's own namespace, microsoft.resources.
func (client *DeploymentThingsClient) NewListPager(options *DeploymentThingsClientListOptions) *runtime.Pager[DeploymentThingsClientListResponse] {
	return runtime.NewPager(runtime.PagingHandler[DeploymentThingsClientListResponse]{
		Fetcher: func(ctx context.Context, page *DeploymentThingsClientListResponse) (DeploymentThingsClientListResponse, error) {
			resp, err := runtime.FetcherForNextLink(ctx, client.internal.Pipeline(), "", func(ctx context.Context) (*policy.Request, error) {
				return client.listCreateRequest(ctx, options)
			}, nil)
			if err != nil {
				return DeploymentThingsClientListResponse{}, err
			}
			return client.listHandleResponse(resp)
		},
	})
}

func (client *DeploymentThingsClient) listCreateRequest(ctx context.Context, options *DeploymentThingsClientListOptions) (*policy.Request, error) {
	urlPath := "/subscriptions/{subscriptionId}/resourceGroups/{resourceGroupName}/deploymentThings"
	req, err := runtime.NewRequest(ctx, http.MethodGet, runtime.JoinPaths(client.internal.Endpoint(), urlPath))
	return req, err
}

func (client *DeploymentThingsClient) listHandleResponse(resp *http.Response) (DeploymentThingsClientListResponse, error) {
	result := DeploymentThingsClientListResponse{}
	if err := runtime.UnmarshalAsJSON(resp, &result.DeploymentThingList); err != nil {
		return DeploymentThingsClientListResponse{}, err
	}
	return result, nil
}

func (client *DeploymentThingsClient) Delete(ctx context.Context, options *DeploymentThingsClientDeleteOptions) (DeploymentThingsClientDeleteResponse, error) {
	req, err := client.deleteCreateRequest(ctx, options)
	if err != nil {
		return DeploymentThingsClientDeleteResponse{}, err
	}
	httpResp, err := client.internal.Pipeline().Do(req)
	if err != nil {
		return DeploymentThingsClientDeleteResponse{}, err
	}
	return DeploymentThingsClientDeleteResponse{}, err
}

func (client *DeploymentThingsClient) deleteCreateRequest(ctx context.Context, options *DeploymentThingsClientDeleteOptions) (*policy.Request, error) {
	urlPath := "/subscriptions/{subscriptionId}/resourceGroups/{resourceGroupName}/deploymentThings/{thingName}"
	req, err := runtime.NewRequest(ctx, http.MethodDelete, runtime.JoinPaths(client.internal.Endpoint(), urlPath))
	return req, err
}

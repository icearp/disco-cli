package armwidgets

func (client *ConfigsClient) NewListConfigsPager(options *ConfigsClientListConfigsOptions) *runtime.Pager[ConfigsClientListConfigsResponse] {
	return runtime.NewPager(runtime.PagingHandler[ConfigsClientListConfigsResponse]{
		Fetcher: func(ctx context.Context, page *ConfigsClientListConfigsResponse) (ConfigsClientListConfigsResponse, error) {
			resp, err := runtime.FetcherForNextLink(ctx, client.internal.Pipeline(), "", func(ctx context.Context) (*policy.Request, error) {
				return client.listConfigsCreateRequest(ctx, options)
			}, nil)
			if err != nil {
				return ConfigsClientListConfigsResponse{}, err
			}
			return client.listConfigsHandleResponse(resp)
		},
	})
}

func (client *ConfigsClient) listConfigsCreateRequest(ctx context.Context, options *ConfigsClientListConfigsOptions) (*policy.Request, error) {
	urlPath := "/subscriptions/{subscriptionId}/resourceGroups/{resourceGroupName}/providers/Microsoft.Widgets/widgets/{widgetName}/configs"
	req, err := runtime.NewRequest(ctx, http.MethodGet, runtime.JoinPaths(client.internal.Endpoint(), urlPath))
	return req, err
}

func (client *ConfigsClient) listConfigsHandleResponse(resp *http.Response) (ConfigsClientListConfigsResponse, error) {
	result := ConfigsClientListConfigsResponse{}
	if err := runtime.UnmarshalAsJSON(resp, &result.ConfigList); err != nil {
		return ConfigsClientListConfigsResponse{}, err
	}
	return result, nil
}

// A write on configs/web: web is the config instance id, not a collection.
func (client *ConfigsClient) UpdateWeb(ctx context.Context, options *ConfigsClientUpdateWebOptions) (ConfigsClientUpdateWebResponse, error) {
	req, err := client.updateWebCreateRequest(ctx, options)
	if err != nil {
		return ConfigsClientUpdateWebResponse{}, err
	}
	httpResp, err := client.internal.Pipeline().Do(req)
	if err != nil {
		return ConfigsClientUpdateWebResponse{}, err
	}
	return client.updateWebHandleResponse(httpResp)
}

func (client *ConfigsClient) updateWebCreateRequest(ctx context.Context, options *ConfigsClientUpdateWebOptions) (*policy.Request, error) {
	urlPath := "/subscriptions/{subscriptionId}/resourceGroups/{resourceGroupName}/providers/Microsoft.Widgets/widgets/{widgetName}/configs/web"
	req, err := runtime.NewRequest(ctx, http.MethodPut, runtime.JoinPaths(client.internal.Endpoint(), urlPath))
	return req, err
}

func (client *ConfigsClient) updateWebHandleResponse(resp *http.Response) (ConfigsClientUpdateWebResponse, error) {
	result := ConfigsClientUpdateWebResponse{}
	if err := runtime.UnmarshalAsJSON(resp, &result.Config); err != nil {
		return ConfigsClientUpdateWebResponse{}, err
	}
	return result, nil
}

// Below the configs/web instance: keys widgets/configs/snapshots.
func (client *ConfigsClient) NewListSnapshotsPager(options *ConfigsClientListSnapshotsOptions) *runtime.Pager[ConfigsClientListSnapshotsResponse] {
	return runtime.NewPager(runtime.PagingHandler[ConfigsClientListSnapshotsResponse]{
		Fetcher: func(ctx context.Context, page *ConfigsClientListSnapshotsResponse) (ConfigsClientListSnapshotsResponse, error) {
			resp, err := runtime.FetcherForNextLink(ctx, client.internal.Pipeline(), "", func(ctx context.Context) (*policy.Request, error) {
				return client.listSnapshotsCreateRequest(ctx, options)
			}, nil)
			if err != nil {
				return ConfigsClientListSnapshotsResponse{}, err
			}
			return client.listSnapshotsHandleResponse(resp)
		},
	})
}

func (client *ConfigsClient) listSnapshotsCreateRequest(ctx context.Context, options *ConfigsClientListSnapshotsOptions) (*policy.Request, error) {
	urlPath := "/subscriptions/{subscriptionId}/resourceGroups/{resourceGroupName}/providers/Microsoft.Widgets/widgets/{widgetName}/configs/web/snapshots"
	req, err := runtime.NewRequest(ctx, http.MethodGet, runtime.JoinPaths(client.internal.Endpoint(), urlPath))
	return req, err
}

func (client *ConfigsClient) listSnapshotsHandleResponse(resp *http.Response) (ConfigsClientListSnapshotsResponse, error) {
	result := ConfigsClientListSnapshotsResponse{}
	if err := runtime.UnmarshalAsJSON(resp, &result.SnapshotList); err != nil {
		return ConfigsClientListSnapshotsResponse{}, err
	}
	return result, nil
}

func (client *ConfigsClient) GetSnapshot(ctx context.Context, options *ConfigsClientGetSnapshotOptions) (ConfigsClientGetSnapshotResponse, error) {
	req, err := client.getSnapshotCreateRequest(ctx, options)
	if err != nil {
		return ConfigsClientGetSnapshotResponse{}, err
	}
	httpResp, err := client.internal.Pipeline().Do(req)
	if err != nil {
		return ConfigsClientGetSnapshotResponse{}, err
	}
	return client.getSnapshotHandleResponse(httpResp)
}

func (client *ConfigsClient) getSnapshotCreateRequest(ctx context.Context, options *ConfigsClientGetSnapshotOptions) (*policy.Request, error) {
	urlPath := "/subscriptions/{subscriptionId}/resourceGroups/{resourceGroupName}/providers/Microsoft.Widgets/widgets/{widgetName}/configs/web/snapshots/{snapshotId}"
	req, err := runtime.NewRequest(ctx, http.MethodGet, runtime.JoinPaths(client.internal.Endpoint(), urlPath))
	return req, err
}

func (client *ConfigsClient) getSnapshotHandleResponse(resp *http.Response) (ConfigsClientGetSnapshotResponse, error) {
	result := ConfigsClientGetSnapshotResponse{}
	if err := runtime.UnmarshalAsJSON(resp, &result.Snapshot); err != nil {
		return ConfigsClientGetSnapshotResponse{}, err
	}
	return result, nil
}

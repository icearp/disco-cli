package armwidgets

func (client *SettingsClient) NewListPager(options *SettingsClientListOptions) *runtime.Pager[SettingsClientListResponse] {
	return runtime.NewPager(runtime.PagingHandler[SettingsClientListResponse]{
		Fetcher: func(ctx context.Context, page *SettingsClientListResponse) (SettingsClientListResponse, error) {
			resp, err := runtime.FetcherForNextLink(ctx, client.internal.Pipeline(), "", func(ctx context.Context) (*policy.Request, error) {
				return client.listCreateRequest(ctx, options)
			}, nil)
			if err != nil {
				return SettingsClientListResponse{}, err
			}
			return client.listHandleResponse(resp)
		},
	})
}

func (client *SettingsClient) listCreateRequest(ctx context.Context, options *SettingsClientListOptions) (*policy.Request, error) {
	urlPath := "/subscriptions/{subscriptionId}/resourceGroups/{resourceGroupName}/providers/Microsoft.Widgets/widgets/{widgetName}/settings"
	req, err := runtime.NewRequest(ctx, http.MethodGet, runtime.JoinPaths(client.internal.Endpoint(), urlPath))
	return req, err
}

func (client *SettingsClient) listHandleResponse(resp *http.Response) (SettingsClientListResponse, error) {
	result := SettingsClientListResponse{}
	if err := runtime.UnmarshalAsJSON(resp, &result.SettingList); err != nil {
		return SettingsClientListResponse{}, err
	}
	return result, nil
}

// A GET answering one model on settings/Default, and settings is no instance: default is an id. Spelled in another case than listRules: statics compare case-folded.
func (client *SettingsClient) GetDefault(ctx context.Context, options *SettingsClientGetDefaultOptions) (SettingsClientGetDefaultResponse, error) {
	req, err := client.getDefaultCreateRequest(ctx, options)
	if err != nil {
		return SettingsClientGetDefaultResponse{}, err
	}
	httpResp, err := client.internal.Pipeline().Do(req)
	if err != nil {
		return SettingsClientGetDefaultResponse{}, err
	}
	return client.getDefaultHandleResponse(httpResp)
}

func (client *SettingsClient) getDefaultCreateRequest(ctx context.Context, options *SettingsClientGetDefaultOptions) (*policy.Request, error) {
	urlPath := "/subscriptions/{subscriptionId}/resourceGroups/{resourceGroupName}/providers/Microsoft.Widgets/widgets/{widgetName}/settings/Default"
	req, err := runtime.NewRequest(ctx, http.MethodGet, runtime.JoinPaths(client.internal.Endpoint(), urlPath))
	return req, err
}

func (client *SettingsClient) getDefaultHandleResponse(resp *http.Response) (SettingsClientGetDefaultResponse, error) {
	result := SettingsClientGetDefaultResponse{}
	if err := runtime.UnmarshalAsJSON(resp, &result.Setting); err != nil {
		return SettingsClientGetDefaultResponse{}, err
	}
	return result, nil
}

func (client *SettingsClient) NewListRulesPager(options *SettingsClientListRulesOptions) *runtime.Pager[SettingsClientListRulesResponse] {
	return runtime.NewPager(runtime.PagingHandler[SettingsClientListRulesResponse]{
		Fetcher: func(ctx context.Context, page *SettingsClientListRulesResponse) (SettingsClientListRulesResponse, error) {
			resp, err := runtime.FetcherForNextLink(ctx, client.internal.Pipeline(), "", func(ctx context.Context) (*policy.Request, error) {
				return client.listRulesCreateRequest(ctx, options)
			}, nil)
			if err != nil {
				return SettingsClientListRulesResponse{}, err
			}
			return client.listRulesHandleResponse(resp)
		},
	})
}

func (client *SettingsClient) listRulesCreateRequest(ctx context.Context, options *SettingsClientListRulesOptions) (*policy.Request, error) {
	urlPath := "/subscriptions/{subscriptionId}/resourceGroups/{resourceGroupName}/providers/Microsoft.Widgets/widgets/{widgetName}/settings/default/rules"
	req, err := runtime.NewRequest(ctx, http.MethodGet, runtime.JoinPaths(client.internal.Endpoint(), urlPath))
	return req, err
}

func (client *SettingsClient) listRulesHandleResponse(resp *http.Response) (SettingsClientListRulesResponse, error) {
	result := SettingsClientListRulesResponse{}
	if err := runtime.UnmarshalAsJSON(resp, &result.RuleList); err != nil {
		return SettingsClientListRulesResponse{}, err
	}
	return result, nil
}

func (client *SettingsClient) CreateRule(ctx context.Context, options *SettingsClientCreateRuleOptions) (SettingsClientCreateRuleResponse, error) {
	req, err := client.createRuleCreateRequest(ctx, options)
	if err != nil {
		return SettingsClientCreateRuleResponse{}, err
	}
	httpResp, err := client.internal.Pipeline().Do(req)
	if err != nil {
		return SettingsClientCreateRuleResponse{}, err
	}
	return client.createRuleHandleResponse(httpResp)
}

func (client *SettingsClient) createRuleCreateRequest(ctx context.Context, options *SettingsClientCreateRuleOptions) (*policy.Request, error) {
	urlPath := "/subscriptions/{subscriptionId}/resourceGroups/{resourceGroupName}/providers/Microsoft.Widgets/widgets/{widgetName}/settings/default/rules/{ruleName}"
	req, err := runtime.NewRequest(ctx, http.MethodPut, runtime.JoinPaths(client.internal.Endpoint(), urlPath))
	return req, err
}

func (client *SettingsClient) createRuleHandleResponse(resp *http.Response) (SettingsClientCreateRuleResponse, error) {
	result := SettingsClientCreateRuleResponse{}
	if err := runtime.UnmarshalAsJSON(resp, &result.Rule); err != nil {
		return SettingsClientCreateRuleResponse{}, err
	}
	return result, nil
}

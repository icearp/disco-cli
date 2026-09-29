package armwidgets

func (client *BudgetsClient) NewListPager(options *BudgetsClientListOptions) *runtime.Pager[BudgetsClientListResponse] {
	return runtime.NewPager(runtime.PagingHandler[BudgetsClientListResponse]{
		Fetcher: func(ctx context.Context, page *BudgetsClientListResponse) (BudgetsClientListResponse, error) {
			resp, err := runtime.FetcherForNextLink(ctx, client.internal.Pipeline(), "", func(ctx context.Context) (*policy.Request, error) {
				return client.listCreateRequest(ctx, options)
			}, nil)
			if err != nil {
				return BudgetsClientListResponse{}, err
			}
			return client.listHandleResponse(resp)
		},
	})
}

func (client *BudgetsClient) listCreateRequest(ctx context.Context, options *BudgetsClientListOptions) (*policy.Request, error) {
	urlPath := "/providers/Microsoft.Management/managementGroups/{groupId}/providers/Microsoft.Widgets/budgets"
	req, err := runtime.NewRequest(ctx, http.MethodGet, runtime.JoinPaths(client.internal.Endpoint(), urlPath))
	return req, err
}

func (client *BudgetsClient) listHandleResponse(resp *http.Response) (BudgetsClientListResponse, error) {
	result := BudgetsClientListResponse{}
	if err := runtime.UnmarshalAsJSON(resp, &result.BudgetList); err != nil {
		return BudgetsClientListResponse{}, err
	}
	return result, nil
}

func (client *BudgetsClient) Create(ctx context.Context, options *BudgetsClientCreateOptions) (BudgetsClientCreateResponse, error) {
	req, err := client.createCreateRequest(ctx, options)
	if err != nil {
		return BudgetsClientCreateResponse{}, err
	}
	httpResp, err := client.internal.Pipeline().Do(req)
	if err != nil {
		return BudgetsClientCreateResponse{}, err
	}
	return BudgetsClientCreateResponse{}, err
}

func (client *BudgetsClient) createCreateRequest(ctx context.Context, options *BudgetsClientCreateOptions) (*policy.Request, error) {
	urlPath := "/providers/Microsoft.Management/managementGroups/{groupId}/providers/Microsoft.Widgets/budgets/{budgetName}"
	req, err := runtime.NewRequest(ctx, http.MethodPut, runtime.JoinPaths(client.internal.Endpoint(), urlPath))
	return req, err
}

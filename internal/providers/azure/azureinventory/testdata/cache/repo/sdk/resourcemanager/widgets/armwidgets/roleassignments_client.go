package armwidgets

func (client *RoleAssignmentsClient) NewListForScopePager(options *RoleAssignmentsClientListForScopeOptions) *runtime.Pager[RoleAssignmentsClientListForScopeResponse] {
	return runtime.NewPager(runtime.PagingHandler[RoleAssignmentsClientListForScopeResponse]{
		Fetcher: func(ctx context.Context, page *RoleAssignmentsClientListForScopeResponse) (RoleAssignmentsClientListForScopeResponse, error) {
			resp, err := runtime.FetcherForNextLink(ctx, client.internal.Pipeline(), "", func(ctx context.Context) (*policy.Request, error) {
				return client.listForScopeCreateRequest(ctx, options)
			}, nil)
			if err != nil {
				return RoleAssignmentsClientListForScopeResponse{}, err
			}
			return client.listForScopeHandleResponse(resp)
		},
	})
}

func (client *RoleAssignmentsClient) listForScopeCreateRequest(ctx context.Context, options *RoleAssignmentsClientListForScopeOptions) (*policy.Request, error) {
	urlPath := "/{scope}/providers/Microsoft.Widgets/roleAssignments"
	req, err := runtime.NewRequest(ctx, http.MethodGet, runtime.JoinPaths(client.internal.Endpoint(), urlPath))
	return req, err
}

func (client *RoleAssignmentsClient) listForScopeHandleResponse(resp *http.Response) (RoleAssignmentsClientListForScopeResponse, error) {
	result := RoleAssignmentsClientListForScopeResponse{}
	if err := runtime.UnmarshalAsJSON(resp, &result.RoleAssignmentListResult); err != nil {
		return RoleAssignmentsClientListForScopeResponse{}, err
	}
	return result, nil
}

func (client *RoleAssignmentsClient) Create(ctx context.Context, options *RoleAssignmentsClientCreateOptions) (RoleAssignmentsClientCreateResponse, error) {
	req, err := client.createCreateRequest(ctx, options)
	if err != nil {
		return RoleAssignmentsClientCreateResponse{}, err
	}
	httpResp, err := client.internal.Pipeline().Do(req)
	if err != nil {
		return RoleAssignmentsClientCreateResponse{}, err
	}
	return client.createHandleResponse(httpResp)
}

func (client *RoleAssignmentsClient) createCreateRequest(ctx context.Context, options *RoleAssignmentsClientCreateOptions) (*policy.Request, error) {
	urlPath := "/{scope}/providers/Microsoft.Widgets/roleAssignments/{roleAssignmentName}"
	req, err := runtime.NewRequest(ctx, http.MethodPut, runtime.JoinPaths(client.internal.Endpoint(), urlPath))
	return req, err
}

func (client *RoleAssignmentsClient) createHandleResponse(resp *http.Response) (RoleAssignmentsClientCreateResponse, error) {
	result := RoleAssignmentsClientCreateResponse{}
	if err := runtime.UnmarshalAsJSON(resp, &result.RoleAssignment); err != nil {
		return RoleAssignmentsClientCreateResponse{}, err
	}
	return result, nil
}

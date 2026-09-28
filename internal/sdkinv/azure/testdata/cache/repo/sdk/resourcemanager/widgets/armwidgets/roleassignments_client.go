package armwidgets

func (client *RoleAssignmentsClient) listForScopeCreateRequest(ctx context.Context, scope string, options *RoleAssignmentsClientListForScopeOptions) (*policy.Request, error) {
	urlPath := "/{scope}/providers/Microsoft.Widgets/roleAssignments"
	req, err := runtime.NewRequest(ctx, http.MethodGet, runtime.JoinPaths(client.internal.Endpoint(), urlPath))
	return req, err
}

func (client *RoleAssignmentsClient) createCreateRequest(ctx context.Context, scope string, roleAssignmentName string, options *RoleAssignmentsClientCreateOptions) (*policy.Request, error) {
	urlPath := "/{scope}/providers/Microsoft.Widgets/roleAssignments/{roleAssignmentName}"
	req, err := runtime.NewRequest(ctx, http.MethodPut, runtime.JoinPaths(client.internal.Endpoint(), urlPath))
	return req, err
}

func (client *RoleAssignmentsClient) NewListForScopePager(ctx context.Context) error {
	return nil
}

func (client *RoleAssignmentsClient) Create(ctx context.Context) error {
	return nil
}

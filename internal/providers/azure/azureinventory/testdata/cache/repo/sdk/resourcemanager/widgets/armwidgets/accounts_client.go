package armwidgets

func (client *AccountsClient) NewListPager(options *AccountsClientListOptions) *runtime.Pager[AccountsClientListResponse] {
	return runtime.NewPager(runtime.PagingHandler[AccountsClientListResponse]{
		Fetcher: func(ctx context.Context, page *AccountsClientListResponse) (AccountsClientListResponse, error) {
			resp, err := runtime.FetcherForNextLink(ctx, client.internal.Pipeline(), "", func(ctx context.Context) (*policy.Request, error) {
				return client.listCreateRequest(ctx, options)
			}, nil)
			if err != nil {
				return AccountsClientListResponse{}, err
			}
			return client.listHandleResponse(resp)
		},
	})
}

func (client *AccountsClient) listCreateRequest(ctx context.Context, options *AccountsClientListOptions) (*policy.Request, error) {
	urlPath := "/providers/Microsoft.Widgets/accounts"
	req, err := runtime.NewRequest(ctx, http.MethodGet, runtime.JoinPaths(client.internal.Endpoint(), urlPath))
	return req, err
}

func (client *AccountsClient) listHandleResponse(resp *http.Response) (AccountsClientListResponse, error) {
	result := AccountsClientListResponse{}
	if err := runtime.UnmarshalAsJSON(resp, &result.AccountList); err != nil {
		return AccountsClientListResponse{}, err
	}
	return result, nil
}

// accounts/{accountName} spells a param where accounts/default spells a static: default is an id.
func (client *AccountsClient) Get(ctx context.Context, options *AccountsClientGetOptions) (AccountsClientGetResponse, error) {
	req, err := client.getCreateRequest(ctx, options)
	if err != nil {
		return AccountsClientGetResponse{}, err
	}
	httpResp, err := client.internal.Pipeline().Do(req)
	if err != nil {
		return AccountsClientGetResponse{}, err
	}
	return client.getHandleResponse(httpResp)
}

func (client *AccountsClient) getCreateRequest(ctx context.Context, options *AccountsClientGetOptions) (*policy.Request, error) {
	urlPath := "/providers/Microsoft.Widgets/accounts/{accountName}"
	req, err := runtime.NewRequest(ctx, http.MethodGet, runtime.JoinPaths(client.internal.Endpoint(), urlPath))
	return req, err
}

func (client *AccountsClient) getHandleResponse(resp *http.Response) (AccountsClientGetResponse, error) {
	result := AccountsClientGetResponse{}
	if err := runtime.UnmarshalAsJSON(resp, &result.Account); err != nil {
		return AccountsClientGetResponse{}, err
	}
	return result, nil
}

func (client *AccountsClient) NewListInvoicesPager(options *AccountsClientListInvoicesOptions) *runtime.Pager[AccountsClientListInvoicesResponse] {
	return runtime.NewPager(runtime.PagingHandler[AccountsClientListInvoicesResponse]{
		Fetcher: func(ctx context.Context, page *AccountsClientListInvoicesResponse) (AccountsClientListInvoicesResponse, error) {
			resp, err := runtime.FetcherForNextLink(ctx, client.internal.Pipeline(), "", func(ctx context.Context) (*policy.Request, error) {
				return client.listInvoicesCreateRequest(ctx, options)
			}, nil)
			if err != nil {
				return AccountsClientListInvoicesResponse{}, err
			}
			return client.listInvoicesHandleResponse(resp)
		},
	})
}

func (client *AccountsClient) listInvoicesCreateRequest(ctx context.Context, options *AccountsClientListInvoicesOptions) (*policy.Request, error) {
	urlPath := "/providers/Microsoft.Widgets/accounts/default/invoices"
	req, err := runtime.NewRequest(ctx, http.MethodGet, runtime.JoinPaths(client.internal.Endpoint(), urlPath))
	return req, err
}

func (client *AccountsClient) listInvoicesHandleResponse(resp *http.Response) (AccountsClientListInvoicesResponse, error) {
	result := AccountsClientListInvoicesResponse{}
	if err := runtime.UnmarshalAsJSON(resp, &result.InvoiceList); err != nil {
		return AccountsClientListInvoicesResponse{}, err
	}
	return result, nil
}

func (client *AccountsClient) GetInvoice(ctx context.Context, options *AccountsClientGetInvoiceOptions) (AccountsClientGetInvoiceResponse, error) {
	req, err := client.getInvoiceCreateRequest(ctx, options)
	if err != nil {
		return AccountsClientGetInvoiceResponse{}, err
	}
	httpResp, err := client.internal.Pipeline().Do(req)
	if err != nil {
		return AccountsClientGetInvoiceResponse{}, err
	}
	return client.getInvoiceHandleResponse(httpResp)
}

func (client *AccountsClient) getInvoiceCreateRequest(ctx context.Context, options *AccountsClientGetInvoiceOptions) (*policy.Request, error) {
	urlPath := "/providers/Microsoft.Widgets/accounts/default/invoices/{invoiceName}"
	req, err := runtime.NewRequest(ctx, http.MethodGet, runtime.JoinPaths(client.internal.Endpoint(), urlPath))
	return req, err
}

func (client *AccountsClient) getInvoiceHandleResponse(resp *http.Response) (AccountsClientGetInvoiceResponse, error) {
	result := AccountsClientGetInvoiceResponse{}
	if err := runtime.UnmarshalAsJSON(resp, &result.Invoice); err != nil {
		return AccountsClientGetInvoiceResponse{}, err
	}
	return result, nil
}

// A POST-only static beside accounts/{accountName}: an action, not an id.
func (client *AccountsClient) CheckName(ctx context.Context, options *AccountsClientCheckNameOptions) (AccountsClientCheckNameResponse, error) {
	req, err := client.checkNameCreateRequest(ctx, options)
	if err != nil {
		return AccountsClientCheckNameResponse{}, err
	}
	httpResp, err := client.internal.Pipeline().Do(req)
	if err != nil {
		return AccountsClientCheckNameResponse{}, err
	}
	return AccountsClientCheckNameResponse{}, err
}

func (client *AccountsClient) checkNameCreateRequest(ctx context.Context, options *AccountsClientCheckNameOptions) (*policy.Request, error) {
	urlPath := "/providers/Microsoft.Widgets/accounts/checkName"
	req, err := runtime.NewRequest(ctx, http.MethodPost, runtime.JoinPaths(client.internal.Endpoint(), urlPath))
	return req, err
}

// accounts/deleted is itself listed: a collection, never an id, whatever its sibling param says.
func (client *AccountsClient) NewListDeletedPager(options *AccountsClientListDeletedOptions) *runtime.Pager[AccountsClientListDeletedResponse] {
	return runtime.NewPager(runtime.PagingHandler[AccountsClientListDeletedResponse]{
		Fetcher: func(ctx context.Context, page *AccountsClientListDeletedResponse) (AccountsClientListDeletedResponse, error) {
			resp, err := runtime.FetcherForNextLink(ctx, client.internal.Pipeline(), "", func(ctx context.Context) (*policy.Request, error) {
				return client.listDeletedCreateRequest(ctx, options)
			}, nil)
			if err != nil {
				return AccountsClientListDeletedResponse{}, err
			}
			return client.listDeletedHandleResponse(resp)
		},
	})
}

func (client *AccountsClient) listDeletedCreateRequest(ctx context.Context, options *AccountsClientListDeletedOptions) (*policy.Request, error) {
	urlPath := "/providers/Microsoft.Widgets/accounts/deleted"
	req, err := runtime.NewRequest(ctx, http.MethodGet, runtime.JoinPaths(client.internal.Endpoint(), urlPath))
	return req, err
}

func (client *AccountsClient) listDeletedHandleResponse(resp *http.Response) (AccountsClientListDeletedResponse, error) {
	result := AccountsClientListDeletedResponse{}
	if err := runtime.UnmarshalAsJSON(resp, &result.AccountList); err != nil {
		return AccountsClientListDeletedResponse{}, err
	}
	return result, nil
}

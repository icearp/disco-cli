package azure

import (
	"context"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/widgets/armwidgets"
	"github.com/icearp/disco-cli/store"
)

func scanWidgets(ctx context.Context, sub string, st *store.Store) error {
	client, err := armwidgets.NewWidgetsClient(sub, nil, nil)
	if err != nil {
		return err
	}
	return widgetsFromPager(ctx, st, client.NewListPager(nil))
}

// widgetsFromPager stores; the caller built the pager.
func widgetsFromPager(ctx context.Context, st *store.Store, pager *runtime.Pager[armwidgets.WidgetsClientListResponse]) error {
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return skipIfAccessDenied(st, "armwidgets:Widgets.List", err)
		}
		for _, w := range page.Value {
			st.Put(&store.Resource{Type: TypeWidget, NativeID: *w.ID})
		}
	}
	return nil
}

// partLister is the test seam over armwidgets.WidgetPartsClient.
type partLister interface {
	List(ctx context.Context, rg, name string, options *armwidgets.WidgetPartsClientListOptions) (armwidgets.WidgetPartsClientListResponse, error)
}

func scanParts(ctx context.Context, client partLister, st *store.Store) error {
	resp, err := client.List(ctx, "rg", "w", nil)
	if err != nil {
		return skipIfAccessDenied(st, "armwidgets:WidgetParts.List", err)
	}
	for _, p := range resp.Value {
		st.Put(&store.Resource{Type: TypePart, NativeID: *p.ID})
	}
	return nil
}

func scanTenantThings(ctx context.Context, cf *armwidgets.ClientFactory, st *store.Store) error {
	client := cf.NewTenantThingsClient()
	pager := client.NewListPager(nil)
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return err
		}
		for _, t := range page.Value {
			st.Put(&store.Resource{Type: TypeTenantThing, NativeID: *t.ID})
		}
	}
	return nil
}

type skuScan struct {
	skus *armwidgets.SKUsClient
	st   *store.Store
}

func (s *skuScan) scanSKUs(ctx context.Context) error {
	pager := s.skus.NewListPager(nil)
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return err
		}
		for _, k := range page.Value {
			s.st.Put(&store.Resource{Type: TypeSKU, NativeID: *k.Name})
		}
	}
	return nil
}

// scanStale calls a pager the pinned SDK no longer ships.
func scanStale(ctx context.Context, sub string, st *store.Store) error {
	client, err := armwidgets.NewWidgetsClient(sub, nil, nil)
	if err != nil {
		return err
	}
	pager := client.NewListGonePager(nil)
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return skipIfAccessDenied(st, "armwidgets:Widgets.ListGone", err)
		}
		for _, w := range page.Value {
			st.Put(&store.Resource{Type: TypeStale, NativeID: *w.ID})
		}
	}
	return nil
}

// scanUnbound pages on a receiver several clients could be.
func scanUnbound(ctx context.Context, client any, st *store.Store) error {
	pager := client.NewListPager(nil)
	for pager.More() {
		if _, err := pager.NextPage(ctx); err != nil {
			return err
		}
	}
	return nil
}

func skipIfAccessDenied(st *store.Store, op string, err error) error { return err }

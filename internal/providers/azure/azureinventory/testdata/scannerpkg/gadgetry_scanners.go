package azure

import (
	"context"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/gadgetry/armwidgets"
	"github.com/icearp/disco-cli/store"
)

// Imports gadgetry/armwidgets only: its WidgetsClient.List must pair to the
// gadgetry candidate, never to widgets/armwidgets' same-named op.
func scanGadgetryWidgets(ctx context.Context, sub string, st *store.Store) error {
	client, err := armwidgets.NewWidgetsClient(sub, nil, nil)
	if err != nil {
		return err
	}
	pager := client.NewListPager(nil)
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return err
		}
		for _, w := range page.Value {
			st.Put(&store.Resource{Type: TypeGadgetryWidget, NativeID: *w.ID})
		}
	}
	return nil
}

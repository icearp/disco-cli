package aws

import (
	"context"

	"github.com/aws/aws-sdk-go-v2/service/widgets"
	"github.com/icearp/disco-cli/store"
)

type widgetsAPI interface {
	ListGrants(context.Context, *widgets.ListGrantsInput, ...func(*widgets.Options)) (*widgets.ListGrantsOutput, error)
}

// scanAll dispatches; its own SDK calls are none.
func scanAll(ctx context.Context, client *widgets.Client, st *store.Store) error {
	if err := scanWidgets(ctx, client, st); err != nil {
		return err
	}
	scanSchemas(st, nil)
	return scanGadgets(ctx, client, st)
}

func scanWidgets(ctx context.Context, client *widgets.Client, st *store.Store) error {
	pager := widgets.NewListWidgetsPaginator(client, &widgets.ListWidgetsInput{})
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			return skipIfAccessDenied(st, "widgets:ListWidgets", err)
		}
		for _, w := range out.Widgets {
			st.Put(&store.Resource{Type: TypeWidget, NativeID: *w.Arn})
		}
	}
	return scanGrants(ctx, client, st)
}

func scanGrants(ctx context.Context, client widgetsAPI, st *store.Store) error {
	out, err := client.ListGrants(ctx, &widgets.ListGrantsInput{})
	if err != nil {
		return skipIfAccessDenied(st, "widgets:ListGrants", err)
	}
	for _, g := range out.Grants {
		st.Put(&store.Resource{Type: TypeGrant, NativeID: *g.Id})
	}
	return nil
}

// scanSchemas stores one row per listed widget: no SDK call of its own.
func scanSchemas(st *store.Store, ids []string) {
	for _, id := range ids {
		st.Put(&store.Resource{Type: TypeSchema, NativeID: id})
	}
}

func scanGadgets(ctx context.Context, client *widgets.Client, st *store.Store) error {
	names, err := listGadgets(ctx, client)
	if err != nil {
		return err
	}
	for _, n := range names {
		st.Put(&store.Resource{Type: TypeGadget, NativeID: n})
	}
	return nil
}

// listGadgets only lists; the caller stores.
func listGadgets(ctx context.Context, client *widgets.Client) ([]string, error) {
	out, err := client.DescribeGadgets(ctx, &widgets.DescribeGadgetsInput{})
	if err != nil {
		return nil, err
	}
	return out.Names, nil
}

func scanAccountSettings(ctx context.Context, client *widgets.Client, st *store.Store) error {
	_, err := client.GetAccountSettings(ctx, &widgets.GetAccountSettingsInput{})
	reportDefaults(st, err)
	return nil
}

// reportDefaults labels its caller's call.
func reportDefaults(st *store.Store, err error) {
	const op = "widgets:GetAccountSettings"
	_ = skipIfAccessDenied(st, op, err)
}

func scanTyped(ctx context.Context, client *widgets.Client, st *store.Store) error {
	return storeTyped(ctx, client, st, TypeWidget)
}

func storeTyped(ctx context.Context, client *widgets.Client, st *store.Store, rtype string) error {
	pager := widgets.NewListWidgetsV2Paginator(client, &widgets.ListWidgetsV2Input{})
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			return err
		}
		for _, w := range out.Widgets {
			st.Put(&store.Resource{Type: rtype, NativeID: *w.Arn})
		}
	}
	return nil
}

func scanOrphan(st *store.Store) {
	st.Put(&store.Resource{Type: TypeOrphan, NativeID: serviceLabel})
}

func badLabel(st *store.Store) error {
	return skipIfAccessDenied(st, "widgets:ListWidgetz", nil)
}

func staleLabel(st *store.Store) error {
	return skipIfAccessDenied(st, "widgets:DescribeZones", nil)
}

func concatLabel(op string) string { return "widgets:List" + op }

func skipIfAccessDenied(st *store.Store, op string, err error) error { return err }

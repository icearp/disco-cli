package aws

import (
	"context"

	"github.com/aws/aws-sdk-go-v2/service/widgets"
	"github.com/icearp/disco-cli/store"
)

// A resolver: it pages a listing and names types, but every write it makes is
// an edge. Naming a type is not storing it.
func resolveAliasLinks(ctx context.Context, client *widgets.Client, st *store.Store) error {
	out, err := client.ListAliases(ctx, &widgets.ListAliasesInput{})
	if err != nil {
		return err
	}
	rows, err := st.List(store.ResourceFilter{Type: TypeWidget})
	if err != nil {
		return err
	}
	for _, id := range out.Ids {
		for _, r := range rows {
			st.Link(id, r, TypeGadget)
		}
	}
	return nil
}

// The store sits four plain hops below the anchor: a correct scanner the
// callee-depth cut-off used to report as unexplained.
func scanDeep(ctx context.Context, client *widgets.Client, st *store.Store) error {
	out, err := client.DescribeZones(ctx, &widgets.DescribeZonesInput{})
	if err != nil {
		return err
	}
	return deepA(st, out.Ids)
}

func deepA(st *store.Store, ids []string) error { return deepB(st, ids) }
func deepB(st *store.Store, ids []string) error { return deepC(st, ids) }
func deepC(st *store.Store, ids []string) error { return deepD(st, ids) }

func deepD(st *store.Store, ids []string) error {
	for _, id := range ids {
		st.Put(&store.Resource{Type: TypeDeep, NativeID: id})
	}
	return nil
}

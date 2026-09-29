package aws

import (
	"context"

	"github.com/aws/aws-sdk-go-v2/service/widgets"
	"github.com/icearp/disco-cli/store"
)

// scanWidgetPair reaches two listing helpers that anchor the same candidate
// under different operations and store nothing themselves. Which op the
// pairing reports must not depend on map iteration order; the live shape is a
// GuardDuty scanner whose callees anchor ListDetectors and GetDetector.
func scanWidgetPair(ctx context.Context, client *widgets.Client, st *store.Store) error {
	one, err := readWidgetDetail(ctx, client)
	if err != nil {
		return err
	}
	rest, err := listWidgetPage(ctx, client)
	if err != nil {
		return err
	}
	for _, arn := range append(one, rest...) {
		st.Put(&store.Resource{Type: TypeWidget, NativeID: arn})
	}
	return nil
}

func readWidgetDetail(ctx context.Context, client *widgets.Client) ([]string, error) {
	out, err := client.GetWidget(ctx, &widgets.GetWidgetInput{})
	if err != nil {
		return nil, err
	}
	return []string{*out.Widget.Arn}, nil
}

func listWidgetPage(ctx context.Context, client *widgets.Client) ([]string, error) {
	var arns []string
	pager := widgets.NewListWidgetsPaginator(client, &widgets.ListWidgetsInput{})
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			return nil, err
		}
		for _, w := range out.Widgets {
			arns = append(arns, *w.Arn)
		}
	}
	return arns, nil
}

package fake

import (
	"context"

	"example.com/fakesdk/widgets"
	"github.com/icearp/disco-cli/store"
)

const (
	TypeWidget = "fake:widgets:widget"
	TypeDetail = "fake:widgets:detail"
	TypeOrphan = "fake:widgets:orphan"
)

func scanWidgets(ctx context.Context, st *store.Store) {
	for _, w := range widgets.ListWidgets(ctx) {
		st.UpsertResource(ctx, store.Resource{Type: TypeWidget, NativeID: w})
	}
}

func describeWidget(ctx context.Context, st *store.Store) {
	st.UpsertResource(ctx, store.Resource{Type: TypeDetail, NativeID: widgets.GetWidget(ctx)})
}

func orphan() string { return TypeOrphan }

func typo() string { return "widgets:ListGizmos" }

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
	TypeTally  = "fake:widgets:tally" // built from the widget listing: derived
	TypeNote   = "fake:gizmos:note"   // another service: proximity is no evidence
)

func scanWidgets(ctx context.Context, st *store.Store) {
	for _, w := range widgets.ListWidgets(ctx) {
		st.UpsertResource(ctx, store.Resource{Type: TypeWidget, NativeID: w})
	}
}

func describeWidget(ctx context.Context, st *store.Store) {
	st.UpsertResource(ctx, store.Resource{Type: TypeDetail, NativeID: widgets.GetWidget(ctx)})
}

// scanAll anchors nothing itself; its rows come from the listing it reaches.
func scanAll(ctx context.Context, st *store.Store) {
	scanWidgets(ctx, st)
	st.UpsertResource(ctx, store.Resource{Type: TypeTally, NativeID: "tally"})
	st.UpsertResource(ctx, store.Resource{Type: TypeNote, NativeID: "note"})
}

func orphan() string { return TypeOrphan }

func typo() string { return "widgets:ListGizmos" }

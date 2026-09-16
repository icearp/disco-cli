package aws

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/service/widgets"
	"github.com/icearp/disco-cli/store"
)

// A format string that only becomes a label at run time.
func sprintfLabel(st *store.Store, op string, err error) error {
	return skipIfAccessDenied(st, fmt.Sprintf("widgets:%s", op), err)
}

// Table-driven: op names and types sit in one literal element each.
var tableScans = []struct {
	op  string
	typ string
}{
	{"ListGizmos", TypeTableGizmo},
	{"DescribeZones", TypeTableZone},
}

func scanTable(ctx context.Context, client *widgets.Client, st *store.Store) error {
	for _, tc := range tableScans {
		var ids []string
		switch tc.op {
		case "ListGizmos":
			out, err := client.ListGizmos(ctx, &widgets.ListGizmosInput{})
			if err != nil {
				return sprintfLabel(st, tc.op, err)
			}
			ids = out.Ids
		case "DescribeZones":
			out, err := client.DescribeZones(ctx, &widgets.DescribeZonesInput{})
			if err != nil {
				return sprintfLabel(st, tc.op, err)
			}
			ids = out.Ids
		}
		for _, id := range ids {
			st.Put(&store.Resource{Type: tc.typ, NativeID: id})
		}
	}
	return nil
}

// Shared store helper fed from two anchored callers: each type must follow
// its own caller's op, not the other's.
func scanAliases(ctx context.Context, client *widgets.Client, st *store.Store) error {
	out, err := client.ListAliases(ctx, &widgets.ListAliasesInput{})
	if err != nil {
		return err
	}
	storeIDs(st, TypeAlias, out.Ids)
	return nil
}

func scanGizmos(ctx context.Context, client *widgets.Client, st *store.Store) error {
	out, err := client.ListGizmos(ctx, &widgets.ListGizmosInput{})
	if err != nil {
		return err
	}
	storeIDs(st, TypeGizmo, out.Ids)
	return nil
}

func storeIDs(st *store.Store, rtype string, ids []string) {
	for _, id := range ids {
		st.Put(&store.Resource{Type: rtype, NativeID: id})
	}
}

// No SDK call of its own: the type it hands the helper must stay unexplained
// rather than ride on another caller's anchor.
func scanMasked(st *store.Store) { storeIDs(st, TypeMasked, nil) }

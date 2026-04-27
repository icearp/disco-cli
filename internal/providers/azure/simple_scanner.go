package azure

import (
	"context"

	"codeberg.org/icearp/disco/internal/store"
)

// azTrackedBase is the four-field shape every Azure SDK tracked-resource type
// satisfies: ID, Name, Location, Tags. Per-type extractors return this base
// (plus the original SDK pointer in `full` for AttributesJSON serialization)
// so generic helpers can build store.Resource batches without knowing the
// concrete SDK type.
type azTrackedBase struct {
	id, name, location string
	tags               map[string]*string
	full               any
}

// azTrackedRows builds a store.Resource batch + RG hierarchy pairs from a
// slice of SDK tracked-resource pointers. Generalizes the wanRows precedent.
// Items with nil pointers or empty IDs are skipped. RG hierarchy pairs are
// emitted only when the ID contains a /resourceGroups/ segment.
func azTrackedRows[T any](sub *subscription, scanID, rtype string, items []*T, extract func(*T) azTrackedBase) ([]*store.Resource, [][2]string) {
	var batch []*store.Resource
	var pairs [][2]string
	for _, item := range items {
		if item == nil {
			continue
		}
		b := extract(item)
		if b.id == "" {
			continue
		}
		batch = append(batch, &store.Resource{
			Provider: "azure", AccountID: sub.ID, AccountName: &sub.Name,
			Type: rtype, NativeID: b.id,
			Name: &b.name, Region: &b.location,
			TagsJSON: azTagsJSON(b.tags), AttributesJSON: mustJSON(b.full),
			DiscoveredBy: scanID,
		})
		if rgFromID(b.id) != "" {
			pairs = append(pairs, rgHierarchyPair(sub, rtype, b.id))
		}
	}
	return batch, pairs
}

// azSimpleScan wires NewListPager → azPageScan → azTrackedRows in a single
// call. Use for simple subscription-scoped list scanners with no sub-resource
// fanout. The scanner shrinks to: build SDK client, call azSimpleScan, supply
// (pageItems, extract) functions.
func azSimpleScan[T any, P any](
	ctx context.Context,
	action, rtype string,
	sub *subscription,
	st *store.Store,
	scanID string,
	pager azPager[P],
	pageItems func(P) []*T,
	extract func(*T) azTrackedBase,
) (total, inserted int, err error) {
	return azPageScan(ctx, action, sub, st, pager, func(page P) ([]*store.Resource, [][2]string) {
		return azTrackedRows(sub, scanID, rtype, pageItems(page), extract)
	})
}

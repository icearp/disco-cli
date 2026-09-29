package aws

import "github.com/icearp/disco-cli/store"

// No SDK import: rows come from a raw HTTP client.
func scanGraph(st *store.Store) {
	st.Put(&store.Resource{Type: TypeGraphOnly})
}

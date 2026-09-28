package azure

import "github.com/icearp/disco-cli/store"

// Graph over raw HTTP: no ARM module.
func scanEntraUsers(st *store.Store) {
	st.Put(&store.Resource{Type: TypeEntraUser})
}

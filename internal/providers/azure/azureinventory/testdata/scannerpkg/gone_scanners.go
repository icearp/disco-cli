package azure

import (
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/gone/armgone"
	"github.com/icearp/disco-cli/store"
)

var _ = armgone.ModuleName

// absentLabel names a module the pinned SDK does not ship.
func absentLabel(st *store.Store) error {
	return skipIfAccessDenied(st, "armgone:Things.List", nil)
}

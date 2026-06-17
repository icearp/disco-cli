//go:build !slim || azure

package all

// Blank-importing the azureregions leaf runs its init(), registering the Azure
// location list into the regions registry. Gated so `-tags slim` without
// `azure` excludes it.
import _ "codeberg.org/icearp/disco/internal/providers/azure/azureregions"

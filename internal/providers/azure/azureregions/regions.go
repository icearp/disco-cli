// Package azureregions is disco's static list of Azure ARM public-cloud
// locations. Deliberately SDK-free (stdlib only) so external callers —
// the public github.com/icearp/disco-cli/regions package and, through it, the
// SaaS control plane — can import the list without linking the Azure SDK.
//
// Excludes sovereign clouds (USGov, China, Germany) — those need separate
// creds + different ARM endpoints.
//
// Refresh when Azure adds a region. Source shape:
// `az account list-locations --query "[].name"`.
package azureregions

import "github.com/icearp/disco-cli/regions"

func init() { regions.Register("azure", Regions) }

// Regions is the supported Azure location list. Treat as read-only; callers
// that expose it (e.g. RegionNames) clone first.
var Regions = []string{
	"australiacentral",
	"australiacentral2",
	"australiaeast",
	"australiasoutheast",
	"brazilsouth",
	"brazilsoutheast",
	"brazilus",
	"canadacentral",
	"canadaeast",
	"centralindia",
	"centralus",
	"centraluseuap",
	"chilecentral",
	"eastasia",
	"eastus",
	"eastus2",
	"eastus2euap",
	"francecentral",
	"francesouth",
	"germanynorth",
	"germanywestcentral",
	"indonesiacentral",
	"israelcentral",
	"italynorth",
	"japaneast",
	"japanwest",
	"jioindiacentral",
	"jioindiawest",
	"koreacentral",
	"koreasouth",
	"malaysiawest",
	"mexicocentral",
	"newzealandnorth",
	"northcentralus",
	"northeurope",
	"norwayeast",
	"norwaywest",
	"polandcentral",
	"qatarcentral",
	"southafricanorth",
	"southafricawest",
	"southcentralus",
	"southeastasia",
	"southindia",
	"spaincentral",
	"swedencentral",
	"swedensouth",
	"switzerlandnorth",
	"switzerlandwest",
	"taiwannorth",
	"taiwannorthwest",
	"uaecentral",
	"uaenorth",
	"uksouth",
	"ukwest",
	"westcentralus",
	"westeurope",
	"westindia",
	"westus",
	"westus2",
	"westus3",
}

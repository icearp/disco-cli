package azure

import "slices"

// azureLocations is disco's static list of Azure ARM public-cloud
// locations. Excludes sovereign clouds (USGov, China, Germany) — those
// need separate creds + different ARM endpoints.
//
// Refresh when Azure adds a region. Source shape:
// `az account list-locations --query "[].name"`.
var azureLocations = []string{
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

// RegionNames implements providers.RegionNamer. Returns a clone so callers
// can't mutate the package-level list.
func (s *Scanner) RegionNames() []string { return slices.Clone(azureLocations) }

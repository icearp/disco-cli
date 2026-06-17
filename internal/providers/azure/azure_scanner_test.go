package azure

import (
	"context"
	"slices"
	"testing"

	"codeberg.org/icearp/disco/store"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
)

// expectedAzureServices is the authoritative list of service names that must be
// registered. Update this list when adding a new service scanner.
var expectedAzureServices = []string{
	"azure:aks",
	"azure:analysisservices",
	"azure:apimanagement",
	"azure:appconfiguration",
	"azure:appplatform",
	"azure:authorization",
	"azure:automation",
	"azure:avs",
	"azure:azurearcdata",
	"azure:azurestackhci",
	"azure:batch",
	"azure:botservice",
	"azure:cdn",
	"azure:chaos",
	"azure:cognitiveservices",
	"azure:communication",
	"azure:compute",
	"azure:connectedvmware",
	"azure:containerapps",
	"azure:containerregistry",
	"azure:cosmos",
	"azure:dashboard",
	"azure:databasewatcher",
	"azure:databox",
	"azure:databoxedge",
	"azure:databricks",
	"azure:datafactory",
	"azure:datamigration",
	"azure:dataprotection",
	"azure:datashare",
	"azure:desktopvirtualization",
	"azure:digitaltwins",
	"azure:dns",
	"azure:edgezones",
	"azure:elasticsan",
	"azure:eventgrid",
	"azure:eventhub",
	"azure:extendedlocation",
	"azure:fileshares",
	"azure:hdinsight",
	"azure:healthcareapis",
	"azure:horizondb",
	"azure:hybridcompute",
	"azure:hybridcontainerservice",
	"azure:hybridnetwork",
	"azure:iothub",
	"azure:keyvault",
	"azure:kubernetes",
	"azure:kusto",
	"azure:logic",
	"azure:machinelearning",
	"azure:management",
	"azure:managednetworkfabric",
	"azure:managedidentity",
	"azure:maps",
	"azure:mongocluster",
	"azure:mysql",
	"azure:netapp",
	"azure:networkcloud",
	"azure:networkfunction",
	"azure:network",
	"azure:operationalinsights",
	"azure:peering",
	"azure:orbital",
	"azure:policy",
	"azure:postgresql",
	"azure:postgresqlhsc",
	"azure:powerbidedicated",
	"azure:privateendpoints",
	"azure:purview",
	"azure:recoveryservices",
	"azure:resourceconnector",
	"azure:redis",
	"azure:relay",
	"azure:scvmm",
	"azure:search",
	"azure:security",
	"azure:servicebus",
	"azure:servicefabric",
	"azure:servicenetworking",
	"azure:signalr",
	"azure:sql",
	"azure:storage",
	"azure:storageactions",
	"azure:storagecache",
	"azure:storagediscovery",
	"azure:storagemover",
	"azure:storagesync",
	"azure:streamanalytics",
	"azure:synapse",
	"azure:trafficmanager",
	"azure:web",
	"azure:webpubsub",
}

// TestRegisteredServices_NoDuplicates verifies that no two services share the same name.
func TestRegisteredServices_NoDuplicates(t *testing.T) {
	seen := make(map[string]bool, len(registeredServices))
	for _, svc := range registeredServices {
		if seen[svc.name] {
			t.Errorf("duplicate service registration: %q", svc.name)
		}
		seen[svc.name] = true
	}
}

// TestRegisteredServices_ExpectedNames verifies that every expected service is registered.
func TestRegisteredServices_ExpectedNames(t *testing.T) {
	registered := make(map[string]bool, len(registeredServices))
	for _, svc := range registeredServices {
		registered[svc.name] = true
	}
	for _, want := range expectedAzureServices {
		if !registered[want] {
			t.Errorf("service %q is not registered", want)
		}
	}
	for _, svc := range registeredServices {
		if !slices.Contains(expectedAzureServices, svc.name) {
			t.Errorf("unrecognised service %q — add it to expectedAzureServices in this test", svc.name)
		}
	}
}

// TestFilteredServices_Nil verifies that nil filter returns all registered services.
func TestFilteredServices_Nil(t *testing.T) {
	got := filteredServices(nil)
	if len(got) != len(registeredServices) {
		t.Errorf("filteredServices(nil): got %d, want %d", len(got), len(registeredServices))
	}
}

// TestFilteredServices_Subset verifies that a named filter returns only the matching service.
func TestFilteredServices_Subset(t *testing.T) {
	got := filteredServices([]string{"azure:compute"})
	if len(got) != 1 {
		t.Fatalf("filteredServices([azure:compute]): got %d results, want 1", len(got))
	}
	if got[0].name != "azure:compute" {
		t.Errorf("filteredServices([azure:compute]): got %q", got[0].name)
	}
}

// TestFilteredServices_Unknown verifies that an unknown service returns empty slice.
func TestFilteredServices_Unknown(t *testing.T) {
	got := filteredServices([]string{"azure:nonexistent"})
	if len(got) != 0 {
		t.Errorf("filteredServices([azure:nonexistent]): got %d results, want 0", len(got))
	}
}

// expectedAzureTenantServices is the authoritative list of tenant-scope
// Azure services that must be registered. Update when adding tenant-scope
// scanners (Entra ID users/groups/SPs/app-regs/directory-roles, etc.).
var expectedAzureTenantServices = []string{
	"azure:microsoft.entra",
}

// TestRegisteredTenantServices_NoDuplicates verifies no duplicate names.
func TestRegisteredTenantServices_NoDuplicates(t *testing.T) {
	seen := make(map[string]bool, len(registeredTenantServices))
	for _, svc := range registeredTenantServices {
		if seen[svc.name] {
			t.Errorf("duplicate tenant service registration: %q", svc.name)
		}
		seen[svc.name] = true
	}
}

// TestRegisteredTenantServices_ExpectedNames verifies registry matches the
// expected set in both directions.
func TestRegisteredTenantServices_ExpectedNames(t *testing.T) {
	registered := make(map[string]bool, len(registeredTenantServices))
	for _, svc := range registeredTenantServices {
		registered[svc.name] = true
	}
	for _, want := range expectedAzureTenantServices {
		if !registered[want] {
			t.Errorf("tenant service %q is not registered", want)
		}
	}
	for _, svc := range registeredTenantServices {
		if !slices.Contains(expectedAzureTenantServices, svc.name) {
			t.Errorf("unrecognised tenant service %q — add it to expectedAzureTenantServices", svc.name)
		}
	}
}

// TestRegisterTenantService_DuplicatePanics confirms double-registration is rejected.
func TestRegisterTenantService_DuplicatePanics(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("registerTenantService accepted duplicate name without panicking")
		}
	}()
	noop := func(_ context.Context, _ []subscription, _ *azidentity.DefaultAzureCredential, _ *store.Store, _ string) (int, int, error) {
		return 0, 0, nil
	}
	registerTenantService(tenantServiceEntry{name: "azure:dup-test", fn: noop})
	registerTenantService(tenantServiceEntry{name: "azure:dup-test", fn: noop})
}

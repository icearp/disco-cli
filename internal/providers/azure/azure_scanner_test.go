package azure

import (
	"context"
	"net/http"
	"slices"
	"testing"

	"codeberg.org/icearp/disco/store"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
)

// expectedAzureServices is the authoritative list of service names that must be
// registered. Update this list when adding a new service scanner.
var expectedAzureServices = []string{
	"azure:microsoft.analysisservices",
	"azure:microsoft.apicenter",
	"azure:microsoft.apimanagement",
	"azure:microsoft.app",
	"azure:microsoft.appconfiguration",
	"azure:microsoft.appplatform",
	"azure:microsoft.attestation",
	"azure:microsoft.authorization",
	"azure:microsoft.automanage",
	"azure:microsoft.automation",
	"azure:microsoft.avs",
	"azure:microsoft.azurearcdata",
	"azure:microsoft.azurefleet",
	"azure:microsoft.azurelargeinstance",
	"azure:microsoft.azureplaywrightservice",
	"azure:microsoft.azureresiliencemanagement",
	"azure:microsoft.azuresphere",
	"azure:microsoft.azurestackhci",
	"azure:microsoft.baremetalinfrastructure",
	"azure:microsoft.batch",
	"azure:microsoft.blueprint",
	"azure:microsoft.botservice",
	"azure:microsoft.cache",
	"azure:microsoft.cdn",
	"azure:microsoft.certificateregistration",
	"azure:microsoft.chaos",
	"azure:microsoft.cloudhealth",
	"azure:microsoft.codesigning",
	"azure:microsoft.cognitiveservices",
	"azure:microsoft.communication",
	"azure:microsoft.compute",
	"azure:microsoft.confidentialledger",
	"azure:microsoft.connectedcache",
	"azure:microsoft.connectedvmwarevsphere",
	"azure:microsoft.containerinstance",
	"azure:microsoft.containerregistry",
	"azure:microsoft.containerservice",
	"azure:microsoft.customproviders",
	"azure:microsoft.dashboard",
	"azure:microsoft.databasewatcher",
	"azure:microsoft.databox",
	"azure:microsoft.databoxedge",
	"azure:microsoft.databricks",
	"azure:microsoft.datafactory",
	"azure:microsoft.datalakeanalytics",
	"azure:microsoft.datamigration",
	"azure:microsoft.dataprotection",
	"azure:microsoft.datareplication",
	"azure:microsoft.datashare",
	"azure:microsoft.dbformariadb",
	"azure:microsoft.dbformysql",
	"azure:microsoft.dbforpostgresql",
	"azure:microsoft.dependencymap",
	"azure:microsoft.desktopvirtualization",
	"azure:microsoft.devcenter",
	"azure:microsoft.devhub",
	"azure:microsoft.deviceregistry",
	"azure:microsoft.devices",
	"azure:microsoft.deviceupdate",
	"azure:microsoft.devopsinfrastructure",
	"azure:microsoft.devtestlab",
	"azure:microsoft.digitaltwins",
	"azure:microsoft.documentdb",
	"azure:microsoft.domainregistration",
	"azure:microsoft.durabletask",
	"azure:microsoft.edgemarketplace",
	"azure:microsoft.edgeorder",
	"azure:microsoft.edgezones",
	"azure:microsoft.elastic",
	"azure:microsoft.elasticsan",
	"azure:microsoft.eventgrid",
	"azure:microsoft.eventhub",
	"azure:microsoft.extendedlocation",
	"azure:microsoft.fabric",
	"azure:microsoft.fileshares",
	"azure:microsoft.fluidrelay",
	"azure:microsoft.graphservices",
	"azure:microsoft.hardwaresecuritymodules",
	"azure:microsoft.hdinsight",
	"azure:microsoft.healthbot",
	"azure:microsoft.healthcareapis",
	"azure:microsoft.healthdataaiservices",
	"azure:microsoft.horizondb",
	"azure:microsoft.hybridcompute",
	"azure:microsoft.hybridconnectivity",
	"azure:microsoft.hybridcontainerservice",
	"azure:microsoft.hybridnetwork",
	"azure:microsoft.integrationspaces",
	"azure:microsoft.iotcentral",
	"azure:microsoft.iotfirmwaredefense",
	"azure:microsoft.iotoperations",
	"azure:microsoft.keyvault",
	"azure:microsoft.kubernetes",
	"azure:microsoft.kusto",
	"azure:microsoft.labservices",
	"azure:microsoft.loadtestservice",
	"azure:microsoft.logic",
	"azure:microsoft.machinelearningservices",
	"azure:microsoft.maintenance",
	"azure:microsoft.managedidentity",
	"azure:microsoft.managednetworkfabric",
	"azure:microsoft.managedservices",
	"azure:microsoft.management",
	"azure:microsoft.maps",
	"azure:microsoft.migrate",
	"azure:microsoft.netapp",
	"azure:microsoft.network",
	"azure:microsoft.networkcloud",
	"azure:microsoft.networkfunction",
	"azure:microsoft.notificationhubs",
	"azure:microsoft.offazurespringboot",
	"azure:microsoft.onlineexperimentation",
	"azure:microsoft.operationalinsights",
	"azure:microsoft.operationsmanagement",
	"azure:microsoft.orbital",
	"azure:microsoft.peering",
	"azure:microsoft.policyinsights",
	"azure:microsoft.powerbidedicated",
	"azure:microsoft.powerplatform",
	"azure:microsoft.purview",
	"azure:microsoft.quantum",
	"azure:microsoft.recoveryservices",
	"azure:microsoft.redhatopenshift",
	"azure:microsoft.relay",
	"azure:microsoft.resourceconnector",
	"azure:microsoft.resources",
	"azure:microsoft.saas",
	"azure:microsoft.scvmm",
	"azure:microsoft.search",
	"azure:microsoft.security",
	"azure:microsoft.servicebus",
	"azure:microsoft.servicefabric",
	"azure:microsoft.servicenetworking",
	"azure:microsoft.signalrservice",
	"azure:microsoft.solutions",
	"azure:microsoft.sql",
	"azure:microsoft.sqlvirtualmachine",
	"azure:microsoft.standbypool",
	"azure:microsoft.storage",
	"azure:microsoft.storageactions",
	"azure:microsoft.storagecache",
	"azure:microsoft.storagediscovery",
	"azure:microsoft.storagemover",
	"azure:microsoft.storagesync",
	"azure:microsoft.streamanalytics",
	"azure:microsoft.synapse",
	"azure:microsoft.virtualmachineimages",
	"azure:microsoft.web",
	"azure:microsoft.workloads",
}

// TestAzClientOptions_PooledTransport verifies the shared ARM client options
// wire a custom HTTP client whose transport keeps a per-host idle-connection
// pool well above Go's stdlib default of 2 — every arm* client targets the
// single host management.azure.com, so connection reuse there gates scan
// wall-clock under the service/fanout concurrency.
func TestAzClientOptions_PooledTransport(t *testing.T) {
	hc, ok := azClientOptions.Transport.(*http.Client)
	if !ok {
		t.Fatalf("azClientOptions.Transport = %T; want *http.Client", azClientOptions.Transport)
	}
	tr, ok := hc.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("http client transport = %T; want *http.Transport", hc.Transport)
	}
	if tr.MaxIdleConnsPerHost <= 2 {
		t.Errorf("MaxIdleConnsPerHost = %d; want > 2 (stdlib default) to pool ARM connections", tr.MaxIdleConnsPerHost)
	}
	if tr.MaxIdleConnsPerHost != 100 {
		t.Errorf("MaxIdleConnsPerHost = %d; want 100", tr.MaxIdleConnsPerHost)
	}
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
	got := filteredServices([]string{"azure:microsoft.compute"})
	if len(got) != 1 {
		t.Fatalf("filteredServices([azure:compute]): got %d results, want 1", len(got))
	}
	if got[0].name != "azure:microsoft.compute" {
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
	noop := func(_ context.Context, _ []subscription, _ azcore.TokenCredential, _ *store.Store, _ string) (int, int, error) {
		return 0, 0, nil
	}
	registerTenantService(tenantServiceEntry{name: "azure:dup-test", fn: noop})
	registerTenantService(tenantServiceEntry{name: "azure:dup-test", fn: noop})
}

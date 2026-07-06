package azure

import (
	"context"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"codeberg.org/icearp/disco/store"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
)

// TestReportPanic_RecoversAndReports is the contract behind the per-goroutine
// panic guard: a panicking scanner must surface as a reported ScanError for its
// service/scope and let the goroutine return normally, never crash the process.
func TestReportPanic_RecoversAndReports(t *testing.T) {
	st := newTestStore(t)
	var (
		mu   sync.Mutex
		got  store.ScanError
		hits int
	)
	st.OnError = func(e store.ScanError) {
		mu.Lock()
		got, hits = e, hits+1
		mu.Unlock()
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		defer reportPanic(st, "azure:microsoft.compute", "sub-1")
		panic("boom in scanner")
	}()
	<-done // would not be reached if the panic propagated (test process would crash)

	mu.Lock()
	defer mu.Unlock()
	if hits != 1 {
		t.Fatalf("OnError fired %d times; want 1", hits)
	}
	if got.Service != "azure:microsoft.compute" || got.Scope != "sub-1" {
		t.Errorf("error meta = {Service:%q Scope:%q}; want service+scope set", got.Service, got.Scope)
	}
	if !strings.Contains(got.Message, "panic:") || !strings.Contains(got.Message, "boom in scanner") {
		t.Errorf("message = %q; want it to carry the panic cause", got.Message)
	}
}

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
	"azure:microsoft.datamigration",
	"azure:microsoft.dataprotection",
	"azure:microsoft.datareplication",
	"azure:microsoft.datashare",
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
	"azure:microsoft.quota",
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
// wire a custom HTTP client whose transport keeps per-host idle connections
// well above Go's stdlib default of 2 — every arm* client targets
// management.azure.com, so connection reuse there gates scan wall-clock
// under service/fanout concurrency.
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

// TestRegisteredServices_NoDuplicates verifies no two services share the same name.
func TestRegisteredServices_NoDuplicates(t *testing.T) {
	seen := make(map[string]bool, len(registeredServices))
	for _, svc := range registeredServices {
		if seen[svc.name] {
			t.Errorf("duplicate service registration: %q", svc.name)
		}
		seen[svc.name] = true
	}
}

// TestRegisteredServices_ExpectedNames verifies every expected service is registered.
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

// TestFilteredServices_Nil verifies nil filter returns all registered services.
func TestFilteredServices_Nil(t *testing.T) {
	got := filteredServices(nil)
	if len(got) != len(registeredServices) {
		t.Errorf("filteredServices(nil): got %d, want %d", len(got), len(registeredServices))
	}
}

// TestFilteredServices_Subset verifies a named filter returns only the matching service.
func TestFilteredServices_Subset(t *testing.T) {
	got := filteredServices([]string{"azure:microsoft.compute"})
	if len(got) != 1 {
		t.Fatalf("filteredServices([azure:compute]): got %d results, want 1", len(got))
	}
	if got[0].name != "azure:microsoft.compute" {
		t.Errorf("filteredServices([azure:compute]): got %q", got[0].name)
	}
}

// TestFilteredServices_Unknown verifies an unknown service returns empty slice.
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
	"azure:microsoft.authorization",
	"azure:microsoft.entra",
	"azure:microsoft.management",
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

// assertReturns runs fn in a goroutine and fails if it has not returned within a
// generous deadline — turning a "blocks forever" regression into a localized
// failure instead of a whole-suite hang. The deadline only matters on the
// failure path; a correct fn returns near-instantly.
func assertReturns(t *testing.T, fn func()) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		defer close(done)
		fn()
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("did not return within deadline — blocked unexpectedly")
	}
}

// TestWaitForTenant pins the synchronization contract gating each subscription's
// phase-2 resolvers on the concurrent tenant (Entra) phase: it returns when the
// tenant phase signals completion, and also returns on ctx cancellation so a
// cancelled scan never hangs at the join.
func TestWaitForTenant(t *testing.T) {
	t.Run("returns when done is closed", func(t *testing.T) {
		done := make(chan struct{})
		close(done)
		assertReturns(t, func() { waitForTenant(context.Background(), done) })
	})
	t.Run("returns on ctx cancellation even if done stays open", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		done := make(chan struct{}) // never closed
		assertReturns(t, func() { waitForTenant(ctx, done) })
	})
}

// TestTenantPhase_ClosesChannelOnPanic guards the no-deadlock contract of the
// concurrent tenant goroutine in Scan: even when the tenant phase panics, the
// entraDone channel must still close so every subscription waiting at the phase-2
// join is released. It mirrors Scan's exact defer ordering (close deferred so it
// runs after reportPanic recovers) and drives a panic through a temporarily
// registered tenant service. Regression mode: making close non-deferred (run
// after runTenantServices) skips it on panic and deadlocks — assertReturns
// localizes that to this test.
func TestTenantPhase_ClosesChannelOnPanic(t *testing.T) {
	saved := registeredTenantServices
	t.Cleanup(func() { registeredTenantServices = saved })
	registeredTenantServices = []tenantServiceEntry{{
		name: "azure:panic-test",
		fn: func(_ context.Context, _ []subscription, _ azcore.TokenCredential, _ *store.Store, _ string) (int, int, error) {
			panic("boom in tenant phase")
		},
	}}

	st := newTestStore(t)
	entraDone := make(chan struct{})
	assertReturns(t, func() {
		// Same shape as the tenant goroutine in Scan: close deferred first (runs
		// last, after the panic is recovered), reportPanic deferred second.
		defer close(entraDone)
		defer reportPanic(st, "entra", "tenant")
		runTenantServices(context.Background(), nil, nil, nil, st, "scan-id")
	})

	select {
	case <-entraDone:
	default:
		t.Fatal("entraDone was not closed after the tenant phase panicked")
	}
}

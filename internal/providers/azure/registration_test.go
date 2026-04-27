package azure

import (
	"slices"
	"testing"
)

// expectedAzureServices is the authoritative list of service names that must be
// registered. Update this list when adding a new service scanner.
var expectedAzureServices = []string{
	"azure:aks",
	"azure:appservice",
	"azure:authorization",
	"azure:compute",
	"azure:containerregistry",
	"azure:cosmos",
	"azure:keyvault",
	"azure:managedidentity",
	"azure:network",
	"azure:operationalinsights",
	"azure:sql",
	"azure:storage",
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

package gcp

import (
	"slices"
	"testing"
)

// expectedGCPServices is the authoritative list of service names that must be
// registered. Update this list when adding a new service scanner.
var expectedGCPServices = []string{
	"gcp:artifactregistry",
	"gcp:bigquery",
	"gcp:bigtable",
	"gcp:binaryauthorization",
	"gcp:certificatemanager",
	"gcp:cloudarmor",
	"gcp:cloudbuild",
	"gcp:clouddns",
	"gcp:cloudfunctions",
	"gcp:cloudkms",
	"gcp:cloudrun",
	"gcp:composer",
	"gcp:compute",
	"gcp:firestore",
	"gcp:gke",
	"gcp:iam",
	"gcp:iam-key",
	"gcp:iam-policy",
	"gcp:loadbalancing",
	"gcp:logging",
	"gcp:monitoring",
	"gcp:pubsub",
	"gcp:secretmanager",
	"gcp:spanner",
	"gcp:sql",
	"gcp:storage",
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
	for _, want := range expectedGCPServices {
		if !registered[want] {
			t.Errorf("service %q is not registered", want)
		}
	}
	for _, svc := range registeredServices {
		if !slices.Contains(expectedGCPServices, svc.name) {
			t.Errorf("unrecognised service %q — add it to expectedGCPServices in this test", svc.name)
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
	got := filteredServices([]string{"gcp:compute"})
	if len(got) != 1 {
		t.Fatalf("filteredServices([gcp:compute]): got %d results, want 1", len(got))
	}
	if got[0].name != "gcp:compute" {
		t.Errorf("filteredServices([gcp:compute]): got %q", got[0].name)
	}
}

// TestFilteredServices_Unknown verifies that an unknown service returns empty slice.
func TestFilteredServices_Unknown(t *testing.T) {
	got := filteredServices([]string{"gcp:nonexistent"})
	if len(got) != 0 {
		t.Errorf("filteredServices([gcp:nonexistent]): got %d results, want 0", len(got))
	}
}

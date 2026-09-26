package gcp

import (
	"context"
	"errors"
	"slices"
	"sync"
	"testing"

	"github.com/icearp/disco-cli/store"
)

// expectedGCPServices is the authoritative list of service names that must be
// registered. Update this list when adding a new service scanner.
var expectedGCPServices = []string{
	"gcp:artifactregistry",
	"gcp:bigquery",
	"gcp:bigqueryconnection",
	"gcp:batch",
	"gcp:bigtable",
	"gcp:binaryauthorization",
	"gcp:certificatemanager",
	"gcp:cloudarmor",
	"gcp:cloudbuild",
	"gcp:clouddns",
	"gcp:cloudquotas",
	"gcp:cloudfunctions",
	"gcp:cloudkms",
	"gcp:cloudresourcemanager-liens",
	"gcp:cloudrun",
	"gcp:cloudrunjobs",
	"gcp:composer",
	"gcp:dataflow",
	"gcp:dataproc",
	"gcp:compute",
	"gcp:firestore",
	"gcp:gke",
	"gcp:iam",
	"gcp:iam-key",
	"gcp:iam-policy",
	"gcp:iam-project",
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

// expectedGCPOrgServices is the authoritative list of org/folder-scope service
// names that must be registered. Update when adding a new org-service scanner
// (e.g. VPC Service Controls, folder/org IAM policies, org Logging sinks).
var expectedGCPOrgServices = []string{
	"gcp:cloudidentity",
	"gcp:cloudresourcemanager-tags",
	"gcp:iam-org",
	"gcp:iam-policy-org",
	"gcp:logging-org",
	"gcp:vpcsc",
}

// TestRegisteredOrgServices_NoDuplicates verifies that no two org services share the same name.
func TestRegisteredOrgServices_NoDuplicates(t *testing.T) {
	seen := make(map[string]bool, len(registeredOrgServices))
	for _, svc := range registeredOrgServices {
		if seen[svc.name] {
			t.Errorf("duplicate org service registration: %q", svc.name)
		}
		seen[svc.name] = true
	}
}

// TestRegisteredOrgServices_ExpectedNames verifies that every expected org
// service is registered and no unrecognised ones slipped in.
func TestRegisteredOrgServices_ExpectedNames(t *testing.T) {
	registered := make(map[string]bool, len(registeredOrgServices))
	for _, svc := range registeredOrgServices {
		registered[svc.name] = true
	}
	for _, want := range expectedGCPOrgServices {
		if !registered[want] {
			t.Errorf("org service %q is not registered", want)
		}
	}
	for _, svc := range registeredOrgServices {
		if !slices.Contains(expectedGCPOrgServices, svc.name) {
			t.Errorf("unrecognised org service %q — add it to expectedGCPOrgServices in this test", svc.name)
		}
	}
}

// TestRegisterOrgService_DuplicatePanics confirms the registry rejects double-registration.
func TestRegisterOrgService_DuplicatePanics(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("registerOrgService accepted duplicate name without panicking")
		}
	}()
	noop := func(_ context.Context, _ []orgScope, _ *store.Store, _ string) (int, int, error) {
		return 0, 0, nil
	}
	registerOrgService(orgServiceEntry{name: "gcp:dup-test", fn: noop})
	registerOrgService(orgServiceEntry{name: "gcp:dup-test", fn: noop})
}

// TestFilteredServices_Unknown verifies that an unknown service returns empty slice.
func TestFilteredServices_Unknown(t *testing.T) {
	got := filteredServices([]string{"gcp:nonexistent"})
	if len(got) != 0 {
		t.Errorf("filteredServices([gcp:nonexistent]): got %d results, want 0", len(got))
	}
}

// One service failing must not take its siblings with it. The dispatcher used
// an errgroup, so a transient 500 or the per-service timeout cancelled every
// other service in every project and phase 2 never ran — the scan kept its
// phase-1 rows and lost every relationship edge.
func TestScanProject_OneServiceFailureDoesNotCancelSiblings(t *testing.T) {
	st := newTestStore(t)
	var errs []store.ScanError
	st.OnError = func(e store.ScanError) { errs = append(errs, e) }

	var mu sync.Mutex
	ran := map[string]bool{}
	record := func(name string) {
		mu.Lock()
		defer mu.Unlock()
		ran[name] = true
	}
	saved := registeredServices
	t.Cleanup(func() { registeredServices = saved })
	registeredServices = []serviceEntry{
		{name: "gcp:boom", fn: func(context.Context, *project, *store.Store, string) (int, int, error) {
			record("gcp:boom")
			return 0, 0, errors.New("500 backend error")
		}},
		{name: "gcp:fine", fn: func(context.Context, *project, *store.Store, string) (int, int, error) {
			record("gcp:fine")
			return 3, 3, nil
		}},
	}

	scanProject(context.Background(), &project{ID: "proj-1"}, nil, st, "scan-1")

	if !ran["gcp:boom"] || !ran["gcp:fine"] {
		t.Fatalf("ran = %v; both services must run", ran)
	}
	if len(errs) != 1 || errs[0].Service != "gcp:boom" || errs[0].Scope != "proj-1" {
		t.Fatalf("errors = %+v; want one gcp:boom failure scoped to proj-1", errs)
	}
}

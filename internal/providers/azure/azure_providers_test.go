package azure

import (
	"net/http"
	"testing"

	azfake "github.com/Azure/azure-sdk-for-go/sdk/azcore/fake"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
	armresourcesfake "github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources/fake"
)

// TestProviderDisabled pins the gate decision: a service is only skipped when
// its ARM namespace is present in the registration map AND not registered.
// Unknown namespaces and a nil map (probe failed) always scan.
func TestProviderDisabled(t *testing.T) {
	reg := map[string]bool{
		"microsoft.compute": true,
		"microsoft.orbital": false,
	}
	cases := []struct {
		name    string
		reg     map[string]bool
		svc     string
		want    bool
		comment string
	}{
		{"registered → scan", reg, "azure:microsoft.compute", false, ""},
		{"not registered → disabled", reg, "azure:microsoft.orbital", true, ""},
		{"unknown namespace → scan", reg, "azure:microsoft.unknown", false, ""},
		{"nil map (probe failed) → scan", nil, "azure:microsoft.orbital", false, ""},
		{"case-insensitive match", map[string]bool{"microsoft.compute": false}, "azure:Microsoft.Compute", true, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := providerDisabled(tc.reg, tc.svc); got != tc.want {
				t.Errorf("providerDisabled(%q) = %v; want %v", tc.svc, got, tc.want)
			}
		})
	}
}

// TestRegisteredProvidersFromPager drives the pager drain against an
// armresources fake transport with a mix of registration states (including
// nil-field rows that must be skipped) and asserts the resulting map. Only
// "Registered"/"Registering" map to true; "NotRegistered"/"Unregistering" map
// to false.
func TestRegisteredProvidersFromPager(t *testing.T) {
	server := armresourcesfake.ProvidersServer{
		NewListPager: func(_ *armresources.ProvidersClientListOptions) azfake.PagerResponder[armresources.ProvidersClientListResponse] {
			r := azfake.PagerResponder[armresources.ProvidersClientListResponse]{}
			r.AddPage(http.StatusOK, armresources.ProvidersClientListResponse{
				ProviderListResult: armresources.ProviderListResult{Value: []*armresources.Provider{
					{Namespace: to.Ptr("Microsoft.Compute"), RegistrationState: to.Ptr("Registered")},
					{Namespace: to.Ptr("Microsoft.Orbital"), RegistrationState: to.Ptr("NotRegistered")},
					{Namespace: to.Ptr("Microsoft.Web"), RegistrationState: to.Ptr("Registering")},
					{Namespace: to.Ptr("Microsoft.Sql"), RegistrationState: to.Ptr("Unregistering")},
					{Namespace: to.Ptr("Microsoft.NoState"), RegistrationState: nil},
					{Namespace: nil, RegistrationState: to.Ptr("Registered")},
				}},
			}, nil)
			return r
		},
	}

	client, err := armresources.NewProvidersClient(testSubID, fakeCred(),
		fakeClientOptions(t, armresourcesfake.NewProvidersServerTransport(&server)))
	if err != nil {
		t.Fatalf("NewProvidersClient: %v", err)
	}

	got, err := registeredProvidersFromPager(t.Context(), client.NewListPager(nil))
	if err != nil {
		t.Fatalf("registeredProvidersFromPager: %v", err)
	}

	want := map[string]bool{
		"microsoft.compute": true,  // Registered
		"microsoft.orbital": false, // NotRegistered
		"microsoft.web":     true,  // Registering → treated as scan
		"microsoft.sql":     false, // Unregistering → treated as disabled
	}
	if len(got) != len(want) {
		t.Fatalf("map size: got %d (%v); want %d (%v)", len(got), got, len(want), want)
	}
	for ns, w := range want {
		if got[ns] != w {
			t.Errorf("registered[%q] = %v; want %v", ns, got[ns], w)
		}
	}
}

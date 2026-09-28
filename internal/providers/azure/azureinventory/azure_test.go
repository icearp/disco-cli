package azureinventory

import "testing"

func TestKeepARMFile(t *testing.T) {
	cases := map[string]bool{
		"sdk/resourcemanager/compute/armcompute/virtualmachines_client.go":                true,
		"sdk/resourcemanager/compute/armcompute/models.go":                                true,
		"sdk/resourcemanager/compute/armcompute/response_types.go":                        true,
		"sdk/resourcemanager/compute/armcompute/responses.go":                             true,
		"sdk/resourcemanager/compute/armcompute/client_factory.go":                        false,
		"sdk/resourcemanager/compute/armcompute/virtualmachines_client_example_test.go":   false,
		"sdk/resourcemanager/compute/armcompute/fake/virtualmachines_server.go":           false,
		"sdk/resourcemanager/compute/armcompute/go.mod":                                   false,
		"profile/p20200901/resourcemanager/compute/armcompute/availabilitysets_client.go": false,
		"sdk/storage/azblob/client.go":                                                    false,
	}
	for p, want := range cases {
		if got := keepARMFile(p); got != want {
			t.Errorf("keepARMFile(%q) = %v, want %v", p, got, want)
		}
	}
}

package azure

import (
	"testing"
)

// TestRGFromID verifies that the resource group name is correctly extracted
// from a well-formed Azure resource ID.
func TestRGFromID(t *testing.T) {
	cases := []struct {
		name, id, want string
	}{
		{
			"VM resource ID",
			"/subscriptions/sub-123/resourceGroups/myRG/providers/Microsoft.Compute/virtualMachines/my-vm",
			"myrg",
		},
		{
			"Storage account resource ID",
			"/subscriptions/sub-123/resourceGroups/UPPERCASE-RG/providers/Microsoft.Storage/storageAccounts/myacct",
			"uppercase-rg",
		},
		{
			"Subnet resource ID (nested under VNet)",
			"/subscriptions/sub-123/resourceGroups/NetRG/providers/Microsoft.Network/virtualNetworks/my-vnet/subnets/my-subnet",
			"netrg",
		},
	}
	for _, tc := range cases {
		got := rgFromID(tc.id)
		if got != tc.want {
			t.Errorf("%s: rgFromID(%q) = %q, want %q", tc.name, tc.id, got, tc.want)
		}
	}
}

// TestRGFromID_Malformed verifies that a malformed or empty resource ID
// returns "" without panicking.
func TestRGFromID_Malformed(t *testing.T) {
	cases := []string{
		"",
		"/",
		"not-an-azure-id",
		"/subscriptions/sub-123",
	}
	for _, id := range cases {
		got := rgFromID(id)
		if got != "" {
			t.Errorf("rgFromID(%q) = %q, want empty string", id, got)
		}
	}
}

// TestVNetIDFromSubnetID verifies that the VNet ID is correctly stripped from
// a subnet resource ID. This drives the subnet→vnet and aks→vnet resolvers.
func TestVNetIDFromSubnetID(t *testing.T) {
	subnetID := "/subscriptions/sub-123/resourceGroups/NetRG/providers/Microsoft.Network/virtualNetworks/my-vnet/subnets/my-subnet"
	want := "/subscriptions/sub-123/resourceGroups/NetRG/providers/Microsoft.Network/virtualNetworks/my-vnet"

	got := vnetIDFromSubnetID(subnetID)
	if got != want {
		t.Errorf("vnetIDFromSubnetID:\n  got:  %q\n  want: %q", got, want)
	}
}

// TestVNetIDFromSubnetID_NoSubnet verifies that a non-subnet ID returns "".
func TestVNetIDFromSubnetID_NoSubnet(t *testing.T) {
	cases := []string{
		"",
		"/subscriptions/sub/resourceGroups/rg/providers/Microsoft.Network/virtualNetworks/vnet",
	}
	for _, id := range cases {
		if got := vnetIDFromSubnetID(id); got != "" {
			t.Errorf("vnetIDFromSubnetID(%q) = %q, want empty", id, got)
		}
	}
}

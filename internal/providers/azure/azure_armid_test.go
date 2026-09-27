package azure

import (
	"testing"
)

// TestRGFromID verifies the resource group name extracts correctly from a
// well-formed Azure resource ID.
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

// TestRGFromID_Malformed verifies a malformed or empty resource ID returns ""
// without panicking.
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

// TestVNetIDFromSubnetID verifies the VNet ID strips correctly from a subnet
// resource ID; drives the subnet→vnet and aks→vnet resolvers.
func TestVNetIDFromSubnetID(t *testing.T) {
	subnetID := "/subscriptions/sub-123/resourceGroups/NetRG/providers/Microsoft.Network/virtualNetworks/my-vnet/subnets/my-subnet"
	want := "/subscriptions/sub-123/resourceGroups/NetRG/providers/Microsoft.Network/virtualNetworks/my-vnet"

	got := vnetIDFromSubnetID(subnetID)
	if got != want {
		t.Errorf("vnetIDFromSubnetID:\n  got:  %q\n  want: %q", got, want)
	}
}

// TestVNetIDFromSubnetID_NoSubnet verifies a non-subnet ID returns "".
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

func TestRepairARMID(t *testing.T) {
	const sub = "sub-123"
	cases := []struct {
		name, id, resName, want string
	}{
		{
			"well-formed kept", "/subscriptions/sub-123/resourceGroups/rg/providers/Microsoft.Network/virtualNetworks/v", "v",
			"/subscriptions/sub-123/resourceGroups/rg/providers/Microsoft.Network/virtualNetworks/v",
		},
		{
			"empty name appended", "/subscriptions//resourceGroups//providers/Microsoft.Network/azureFirewallFqdnTags/", "AppServiceEnvironment",
			"/subscriptions/sub-123/providers/Microsoft.Network/azureFirewallFqdnTags/AppServiceEnvironment",
		},
		{
			"empty scope, name present", "/subscriptions//resourceGroups//providers/Microsoft.Network/bgpServiceCommunities/AzureSQL", "AzureSQL",
			"/subscriptions/sub-123/providers/Microsoft.Network/bgpServiceCommunities/AzureSQL",
		},
		{
			"no name to rebuild from", "/subscriptions//resourceGroups//providers/Microsoft.Network/azureFirewallFqdnTags/", "",
			"/subscriptions//resourceGroups//providers/Microsoft.Network/azureFirewallFqdnTags/",
		},
		{"no provider path", "/subscriptions//", "x", "/subscriptions//"},
		{"empty inner segment", "/providers/Microsoft.Network//x", "x", "/providers/Microsoft.Network//x"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := repairARMID(sub, c.id, c.resName); got != c.want {
				t.Errorf("repairARMID(%q, %q) = %q, want %q", c.id, c.resName, got, c.want)
			}
		})
	}
}

// TestAzTrackedRows_MalformedIDsStayDistinct pins the live shape: Azure lists
// every FQDN tag under one nameless id, which stored raw collapses the list
// onto a single NativeID.
func TestAzTrackedRows_MalformedIDsStayDistinct(t *testing.T) {
	type item struct{ id, name string }
	const bad = "/subscriptions//resourceGroups//providers/Microsoft.Network/azureFirewallFqdnTags/"
	items := []*item{{bad, "AppServiceEnvironment"}, {bad, "WindowsUpdate"}}
	batch, pairs := azTrackedRows(newTestSubscription(testSubID), "scan-1", TypeNetworkAzureFirewallFqdnTag, items,
		func(i *item) azTrackedBase { return azTrackedBase{id: i.id, name: i.name, full: i} })
	if len(batch) != 2 || batch[0].NativeID == batch[1].NativeID {
		t.Fatalf("want 2 distinct NativeIDs, got %d rows: %v", len(batch), batch)
	}
	if want := "/subscriptions/" + testSubID + "/providers/Microsoft.Network/azureFirewallFqdnTags/WindowsUpdate"; batch[1].NativeID != want {
		t.Errorf("NativeID = %q, want %q", batch[1].NativeID, want)
	}
	if len(pairs) != 0 {
		t.Errorf("repaired IDs carry no resource group, got hierarchy pairs %v", pairs)
	}
}

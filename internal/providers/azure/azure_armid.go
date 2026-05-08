package azure

import "strings"

// ARM resource-ID parsing helpers. Each helper centralises one shape so a
// drift in segment naming or case rules fixes in one place rather than
// scattering the same logic across resolvers and scanners. ARM IDs are
// case-insensitive when stored by Azure but returned with whatever case
// the user typed at create time, so anywhere we *match* against an ID we
// lowercase first; anywhere we *call APIs* with extracted segments we
// preserve the original case.

// rgFromID extracts the resource group name from an Azure resource ID,
// lowercased for use in computing stable hierarchy IDs.
// e.g. /subscriptions/xxx/resourceGroups/myRG/... → "myrg"
func rgFromID(id string) string {
	parts := strings.Split(strings.ToLower(id), "/")
	for i, p := range parts {
		if p == "resourcegroups" && i+1 < len(parts) {
			return parts[i+1]
		}
	}
	return ""
}

// rgNameFromID extracts the resource group name from an Azure resource ID,
// preserving original casing for use in API calls.
// e.g. /subscriptions/xxx/resourceGroups/MyRG/... → "MyRG"
func rgNameFromID(id string) string {
	lower := strings.ToLower(id)
	const sep = "/resourcegroups/"
	start := strings.Index(lower, sep)
	if start < 0 {
		return ""
	}
	rest := id[start+len(sep):]
	if i := strings.IndexByte(rest, '/'); i >= 0 {
		return rest[:i]
	}
	return rest
}

// nameFromID returns the last path segment of an Azure resource ID,
// preserving original casing. Used to extract resource names for API calls.
// e.g. /subscriptions/xxx/.../virtualMachines/myVM → "myVM"
func nameFromID(id string) string {
	idx := strings.LastIndex(id, "/")
	if idx < 0 || idx == len(id)-1 {
		return id
	}
	return id[idx+1:]
}

// truncateAtSegment returns the portion of id before the first occurrence of
// the case-insensitive separator. Used by resolvers to derive parent resource IDs
// from child NativeIDs (e.g. strip "/extensions/" suffix to get VM ID).
func truncateAtSegment(id, separator string) string {
	idx := strings.Index(strings.ToLower(id), strings.ToLower(separator))
	if idx < 0 {
		return ""
	}
	return id[:idx]
}

// vnetIDFromSubnetID extracts the VNet resource ID from a subnet resource ID.
// e.g. /.../virtualNetworks/foo/subnets/bar → /.../virtualNetworks/foo
func vnetIDFromSubnetID(subnetID string) string {
	lower := strings.ToLower(subnetID)
	idx := strings.Index(lower, "/subnets/")
	if idx < 0 {
		return ""
	}
	return subnetID[:idx]
}

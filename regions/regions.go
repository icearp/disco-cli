// Package regions is the dependency-free public view of the cloud regions
// disco's scanners support, per provider. The per-provider lists are the
// source of truth and live next to each provider's scanner
// (internal/providers/<p>/<p>regions); this package only aggregates them so
// external callers (e.g. the SaaS control plane) can validate scan regions
// without linking any cloud SDK.
//
// Provider keys are the lower-case wire values "aws", "azure", and "gcp".
package regions

import (
	"slices"

	"codeberg.org/icearp/disco/internal/providers/aws/awsregions"
	"codeberg.org/icearp/disco/internal/providers/azure/azureregions"
	"codeberg.org/icearp/disco/internal/providers/gcp/gcpregions"
)

var byProvider = map[string][]string{
	"aws":   awsregions.Regions,
	"azure": azureregions.Regions,
	"gcp":   gcpregions.Regions,
}

// For returns the supported regions for provider in sorted order, or nil for an
// unknown provider. The returned slice is a copy the caller may retain or
// mutate.
func For(provider string) []string {
	src, ok := byProvider[provider]
	if !ok {
		return nil
	}
	out := slices.Clone(src)
	slices.Sort(out)
	return out
}

// Valid reports whether region is a supported region for provider. It is an
// exact, case-sensitive membership test.
func Valid(provider, region string) bool {
	return slices.Contains(byProvider[provider], region)
}

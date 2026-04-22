package providers

import (
	"context"
	"sort"

	"codeberg.org/icearp/disco/internal/store"
)

// Scanner is the interface every cloud provider must implement.
// Each provider package registers itself via init() by calling Register.
type Scanner interface {
	// Name returns the provider's canonical identifier (e.g. "aws", "azure", "gcp").
	Name() string
	// Scan discovers all resources for this provider and persists them via st.
	// scanID ties every upserted resource and relationship to this scan run.
	Scan(ctx context.Context, st *store.Store, scanID string) error
}

// ServiceFilterer is an optional interface that providers may implement to
// support scanning a subset of their registered services.
// The --services flag on "disco scan <provider>" uses this when present.
type ServiceFilterer interface {
	SetServiceFilter(services []string)
}

// ServiceNamer is an optional interface that providers may implement to
// expose the list of service names they will report via ReportService.
// cmd/scan.go uses this to compute column widths for aligned output.
type ServiceNamer interface {
	ServiceNames() []string
}

// RegionOverrider is an optional interface for providers that support
// overriding scan regions via the --region CLI flag.
type RegionOverrider interface {
	SetRegionOverride(regions []string)
}

// ProfileOverrider is an optional interface for providers that support
// selecting a named credential profile via the --profile CLI flag.
type ProfileOverrider interface {
	SetProfile(profile string)
}

// registry maps provider name → Scanner. Populated by provider init() calls.
var registry = map[string]Scanner{}

// Register adds a scanner to the global registry.
// Providers call this from their package init() function.
func Register(s Scanner) {
	registry[s.Name()] = s
}

// Get returns the scanner for the given provider name, if registered.
func Get(name string) (Scanner, bool) {
	s, ok := registry[name]
	return s, ok
}

// All returns all registered scanners sorted by name.
func All() []Scanner {
	scanners := make([]Scanner, 0, len(registry))
	for _, s := range registry {
		scanners = append(scanners, s)
	}
	sort.Slice(scanners, func(i, j int) bool {
		return scanners[i].Name() < scanners[j].Name()
	})
	return scanners
}

// Names returns the names of all registered providers, sorted.
// Used for error messages and scan metadata.
func Names() []string {
	names := make([]string, 0, len(registry))
	for name := range registry {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

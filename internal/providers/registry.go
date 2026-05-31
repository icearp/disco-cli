// Package providers hosts the cloud-provider Scanner registry. Concrete
// scanners live in subpackages (aws, azure, gcp) and self-register via
// init() — see internal/providers/CLAUDE.md for the registration shape.
package providers

import (
	"context"
	"sort"

	"codeberg.org/icearp/disco/store"
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

// RegionNamer surfaces every region/location the provider's static
// capability list covers — AWS partitions (us-east-1, ...), Azure ARM
// locations (eastus, ...), GCP compute regions (us-central1, ...). The
// list is the disco-side opinion of "what could be scanned"; providers
// scan a subset based on config / creds at runtime. Static — refresh by
// editing the per-provider list when a new cloud region launches.
// cmd/scan.go uses this to compute the scope column width for aligned
// progress output.
type RegionNamer interface {
	RegionNames() []string
}

// ProfileOverrider is an optional interface for providers that support
// selecting a named credential profile via the --profile CLI flag.
type ProfileOverrider interface {
	SetProfile(profile string)
}

// RoleOverrider is an optional interface for providers that support
// overriding the assume-role identity via CLI flags. Used by the SaaS
// scan-trigger Lambda to pin the worker to the connected_account's
// principal + external-id pair without writing a config file. Empty
// roleARN restores the default credential chain; non-empty externalID
// is included in the AssumeRole call only when roleARN is also set.
type RoleOverrider interface {
	SetRoleOverride(roleARN, externalID string)
}

// SubscriptionOverrider is an optional interface for providers that support
// pinning the scan to an explicit subscription set via the --subscriptions CLI
// flag. Used by the SaaS scan-trigger to constrain the shared worker identity
// to one tenant's subscriptions without writing a config file — the Azure
// analogue of RoleOverrider. A non-nil override pins the scan and disables
// auto-enumeration (fail-closed): an override that resolves to zero
// subscriptions is an error, never a fall-through to enumeration. A nil
// override is never set and preserves the config-then-enumerate default.
type SubscriptionOverrider interface {
	SetSubscriptionOverride(subscriptionIDs []string)
}

// GlobalsSkipper is an optional interface for providers that support
// suppressing global / cross-region service scans via --skip-globals.
// Globals are services whose endpoints live in a single region but whose
// resource scope is account-wide (IAM, Route53, CloudFront, Globalaccelerator,
// etc.). When set, the provider must not invoke any service registered as
// global; per-region services are unaffected.
type GlobalsSkipper interface {
	SetSkipGlobals(skip bool)
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

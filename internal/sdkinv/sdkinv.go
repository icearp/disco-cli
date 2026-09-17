// Package sdkinv derives the coverage denominator from the cloud providers'
// own SDKs and catalogs. Each provider registers an Extractor that knows what
// to fetch (FetchSpec) and how to turn the fetched sources into a Universe of
// listable resource candidates. The package imports nothing from
// internal/providers or internal/coverage so it stays a pure data +
// filesystem layer that any provider can plug into.
package sdkinv

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
)

// Scope names the container a list operation enumerates within.
type Scope string

// Scope values shared across providers. A provider uses the subset that
// matches its API model.
const (
	ScopeAccount         Scope = "account"
	ScopeRegion          Scope = "region"
	ScopeGlobal          Scope = "global"
	ScopeSubscription    Scope = "subscription"
	ScopeResourceGroup   Scope = "resource-group"
	ScopeTenant          Scope = "tenant"
	ScopeManagementGroup Scope = "management-group"
	ScopeExtension       Scope = "extension"
	ScopeProject         Scope = "project"
	ScopeOrg             Scope = "org"
	ScopeFolder          Scope = "folder"
	ScopeBillingAccount  Scope = "billing-account"
)

// Class is the derived classification of a candidate.
type Class string

// Class values. Only ClassResource enters the coverage denominator.
const (
	ClassResource    Class = "resource"     // a listable, persistent resource
	ClassAttribute   Class = "attribute"    // a Get on a parent's setting; no collection
	ClassCatalog     Class = "catalog"      // provider-published, read-only (instance types, zones)
	ClassNonResource Class = "non-resource" // account attributes, operations, metrics, history
)

// Operation is one SDK operation relevant to listing a candidate.
type Operation struct {
	Service  string   `json:"service"`            // join key: AWS sigv4 name; Azure namespace; GCP discovery name
	Name     string   `json:"name"`               // SDK operation name
	Label    string   `json:"label"`              // disco op-label grammar for this provider
	IsList   bool     `json:"isList"`             // enumerates a collection
	Paged    bool     `json:"paged"`              // has a paginator / next token
	Required []string `json:"required,omitempty"` // required inputs after scope-param removal
	Targets  []string `json:"targets,omitempty"`  // parent resources the op operates within
	Scope    Scope    `json:"scope,omitempty"`
	Path     string   `json:"path,omitempty"` // REST path template where the SDK exposes one
	Module   string   `json:"module"`         // source module and version/ref
}

// Candidate is one SDK-listable resource, the unit of the coverage denominator.
type Candidate struct {
	Provider string      `json:"provider"`
	Service  string      `json:"service"`
	Key      string      `json:"key"` // "<service>/<resource path>", provider-shaped
	Depth    int         `json:"depth"`
	Parent   string      `json:"parent,omitempty"`
	Class    Class       `json:"class"`
	Ops      []Operation `json:"ops"`
	Signals  []string    `json:"signals,omitempty"` // audit trail of the rules that fired
	// Refs are dotted field paths on the listed element that name other
	// resources (VpcId, properties.networkProfile.networkInterfaces,
	// networkInterfaces.subnetwork): hints for which resolver edges a scanner
	// of this candidate could wire. Sorted, deduplicated, never the element's
	// own id.
	Refs []string `json:"refs,omitempty"`
}

// Diagnostic records something an extractor could not classify confidently.
type Diagnostic struct {
	Severity string `json:"severity"` // "warn" | "error"
	Source   string `json:"source"`   // file or URL
	Message  string `json:"message"`
}

// Universe is everything one provider's SDK can list.
type Universe struct {
	Provider   string            `json:"provider"`
	Pins       map[string]string `json:"pins"` // source name -> ref/version/sha
	Candidates []Candidate       `json:"candidates"`
	// Other lists operations the SDK ships that are not candidates (writes,
	// item reads, actions), so a scanner label naming one is a known op, not
	// a typo.
	Other       []Operation  `json:"other,omitempty"`
	Diagnostics []Diagnostic `json:"diagnostics,omitempty"`
}

// Kind selects how a FetchSource is retrieved.
type Kind string

// FetchSource kinds.
const (
	KindTarball   Kind = "tarball"    // gzipped tar over HTTPS; entries filtered by Keep
	KindModuleZip Kind = "module-zip" // Go module zip from the module proxy; entries filtered by Keep
	KindJSONIndex Kind = "json-index" // a JSON index document expanded into per-entry JSON fetches
)

// FetchSource is one artifact an Extractor needs on disk.
type FetchSource struct {
	Name string
	Kind Kind
	// URL is the tarball / zip / index document location.
	URL string
	// Dest is the subdirectory under the provider cache dir that receives the files.
	Dest string
	// Strip drops this many leading path components from archive entries
	// (GitHub tarballs prefix "<repo>-<ref>/", module zips "<module>@<ver>/").
	Strip int
	// Keep decides whether an archive entry (after Strip) is written. nil keeps all.
	Keep func(path string) bool
	// LocalDir, when set and present on disk, is walked instead of downloading
	// (e.g. an already-populated GOMODCACHE). Paths are relative to LocalDir.
	LocalDir string
	// Expand parses the index document into per-entry fetches (KindJSONIndex only).
	Expand func(index []byte) ([]IndexEntry, error)
}

// IndexEntry is one document referenced by a JSON index. It is written to
// <Dest>/<Name>.json.
type IndexEntry struct {
	Name string
	URL  string
}

// Extractor is implemented once per provider and registered from init().
type Extractor interface {
	// Name is the provider name ("aws", "azure", "gcp").
	Name() string
	// Ref identifies the pinned source snapshot; the cache dir is <root>/<name>@<ref>.
	Ref() string
	// FetchSpec lists what `disco coverage sdk fetch` pulls into the cache.
	FetchSpec() []FetchSource
	// Extract builds the Universe from a populated cache dir.
	Extract(ctx context.Context, dir string) (*Universe, error)
}

// ErrNotImplemented is returned by Extract while an extractor only ships its FetchSpec.
var ErrNotImplemented = errors.New("sdkinv: extractor not implemented")

var (
	mu       sync.RWMutex
	registry = map[string]Extractor{}
)

// Register adds an extractor; duplicate names panic, mirroring providers.Register.
func Register(e Extractor) {
	mu.Lock()
	defer mu.Unlock()
	if _, dup := registry[e.Name()]; dup {
		panic(fmt.Sprintf("sdkinv: duplicate extractor %q", e.Name()))
	}
	registry[e.Name()] = e
}

// Get returns the extractor registered under name.
func Get(name string) (Extractor, bool) {
	mu.RLock()
	defer mu.RUnlock()
	e, ok := registry[name]
	return e, ok
}

// Names returns registered extractor names, sorted.
func Names() []string {
	mu.RLock()
	defer mu.RUnlock()
	out := make([]string, 0, len(registry))
	for n := range registry {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

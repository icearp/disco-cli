package cmd

import (
	"testing"

	"github.com/icearp/disco-cli/internal/coverage"
	"github.com/icearp/disco-cli/internal/providers"
)

// coreStoredTypes are stored by a provider's Scan before any service runs
// (the GCP hierarchy walk); a failure there is a whole-scan error, which
// verify joins to every type, so they need no service of their own.
var coreStoredTypes = map[string]bool{
	"gcp:cloudresourcemanager:organization": true,
	"gcp:cloudresourcemanager:folder":       true,
	"gcp:cloudresourcemanager:project":      true,
}

// TestEveryEmittedTypeHasScannerService: `coverage verify` explains a
// declared type by the scan-record entry of the scanner service that stores
// it, joined through the type's file, declared service or type segment
// (typeServiceNames). Every emitted type must reach a registered service
// through one of them; otherwise the row silently reads "no rows" instead of
// "scan-error". The file-derived names need no check of their own:
// TypeServices only ever returns names registerService, registerOrgService or
// registerTenantService noted, and the org/tenant scanners report errors
// under those same literals. (A check against ServiceNames() could never
// fire: it lists only the per-scope services and was escaped by a prefix
// test every name passed.)
func TestEveryEmittedTypeHasScannerService(t *testing.T) {
	for _, p := range coverage.All() {
		m, ok := p.(coverage.ServiceMapper)
		if !ok {
			t.Errorf("%s: coverage provider lacks TypeServices", p.Name())
			continue
		}
		known := map[string]bool{}
		for _, sc := range providers.All() {
			if sn, ok := sc.(interface{ ServiceNames() []string }); ok && sc.Name() == p.Name() {
				for _, n := range sn.ServiceNames() {
					known[n] = true
				}
			}
		}
		byType := m.TypeServices()
		for _, d := range p.Emits() {
			svcs := byType[d.DiscoType]
			if len(svcs) == 0 && !known[p.Name()+":"+d.Service] && !known[p.Name()+":"+discoServiceSegment(d.DiscoType)] && !coreStoredTypes[d.DiscoType] {
				t.Errorf("%s: %s reaches no registered scanner service", p.Name(), d.DiscoType)
			}
		}
	}
}

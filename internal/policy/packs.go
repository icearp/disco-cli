package policy

import (
	"embed"
	"fmt"
	"io/fs"
	"strings"
)

// packsFS holds every bundled OPA Rego pack, embedded at build time.
// Each pack lives under its own subdirectory (e.g. aws-waf/) and is named
// <provider>-<framework>. Add a new //go:embed line per new pack and one
// entry to AvailablePacks; everything else flows from filesystem walk.

//go:embed aws-waf/*.rego
var packsFS embed.FS

// AvailablePacks returns the names of bundled packs in stable order.
// Each entry maps to a subdirectory of internal/policy/ embedded via
// packsFS. Curated packs (full WAF, CIS, NIST 800-53, PCI-DSS, ISO 27001)
// are future work, not yet bundled.
func AvailablePacks() []string {
	return []string{"aws-waf"}
}

// LoadPacks reads the named packs and returns merged module sources
// keyed by "<pack>/<filename>" so compile errors point to the original
// .rego file. Unknown pack names error with the available-packs list.
func LoadPacks(names []string) (map[string]string, error) {
	out := map[string]string{}
	known := map[string]bool{}
	for _, n := range AvailablePacks() {
		known[n] = true
	}
	for _, name := range names {
		if !known[name] {
			return nil, fmt.Errorf("unknown pack %q (available: %s)", name, strings.Join(AvailablePacks(), ", "))
		}
		err := fs.WalkDir(packsFS, name, func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || !strings.HasSuffix(path, ".rego") {
				return err
			}
			b, rerr := packsFS.ReadFile(path)
			if rerr != nil {
				return rerr
			}
			out[path] = string(b)
			return nil
		})
		if err != nil {
			return nil, fmt.Errorf("read pack %q: %w", name, err)
		}
	}
	return out, nil
}

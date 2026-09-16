// Package gcp registers the GCP SDK inventory extractor. Source: the Discovery
// documents (<api>-api.json) vendored inside google.golang.org/api at the exact
// version disco links, copied from GOMODCACHE when present or downloaded from
// the module proxy otherwise.
package gcp

import (
	"context"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"

	"github.com/icearp/disco-cli/internal/sdkinv"
)

const modulePath = "google.golang.org/api"

type extractor struct{}

func (extractor) Name() string { return "gcp" }

// Ref is the google.golang.org/api version this binary links, read from build
// info so the inventory always describes the SDK the scanners compile against.
// Falls back to the sdkinv.GCPAPIRef pin when the binary does not link the
// module (sdkinv's own tests).
func (extractor) Ref() string { return moduleVersion() }

func moduleVersion() string {
	if bi, ok := debug.ReadBuildInfo(); ok {
		for _, d := range bi.Deps {
			if d.Path == modulePath {
				if d.Replace != nil {
					return d.Replace.Version
				}
				return d.Version
			}
		}
	}
	return sdkinv.GCPAPIRef
}

func modCache() string {
	if v := os.Getenv("GOMODCACHE"); v != "" {
		return v
	}
	if v := os.Getenv("GOPATH"); v != "" {
		return filepath.Join(v, "pkg", "mod")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, "go", "pkg", "mod")
}

func (e extractor) FetchSpec() []sdkinv.FetchSource {
	ver := e.Ref()
	return []sdkinv.FetchSource{{
		Name:     "discovery-docs",
		Kind:     sdkinv.KindModuleZip,
		URL:      "https://proxy.golang.org/" + modulePath + "/@v/" + ver + ".zip",
		Dest:     "api",
		Strip:    2, // "google.golang.org/api@<ver>/"
		Keep:     func(p string) bool { return strings.HasSuffix(p, "-api.json") },
		LocalDir: filepath.Join(modCache(), modulePath+"@"+ver),
	}}
}

func (extractor) Extract(context.Context, string) (*sdkinv.Universe, error) {
	return nil, sdkinv.ErrNotImplemented
}

func init() { sdkinv.Register(extractor{}) }

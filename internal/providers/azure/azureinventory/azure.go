// Package azureinventory registers the Azure SDK inventory extractor. Source: the
// generated *_client.go files of every arm* module in azure-sdk-for-go, whose
// request builders embed the ARM urlPath template verbatim, plus the models
// and response types needed to tell a collection GET from an item GET.
package azureinventory

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"

	"github.com/icearp/disco-cli/internal/sdkinv"
)

const (
	tarballURL = "https://codeload.github.com/Azure/azure-sdk-for-go/tar.gz/" + SDKRef
	// rmPrefix anchors on the shipped modules; the repo also carries
	// profile/*/resourcemanager copies that must not be double-counted.
	rmPrefix = "sdk/resourcemanager/"
)

type extractor struct{}

func (extractor) Name() string { return "azure" }
func (extractor) Ref() string  { return SDKRef }

func (extractor) FetchSpec() []sdkinv.FetchSource {
	return []sdkinv.FetchSource{{
		Name:   "arm-clients",
		Kind:   sdkinv.KindTarball,
		URL:    tarballURL,
		Dest:   "repo",
		Strip:  1,
		Keep:   keepARMFile,
		KeepID: "arm-go-sources-v3",
	}}
}

// keepARMFile keeps the generated Go sources of arm* modules: every non-test
// .go file except fakes and client_factory.go, whose constructors carry no
// request. models_serde.go stays: its MarshalJSON bodies are the only place
// the SDK states each field's JSON name.
func keepARMFile(p string) bool {
	if !strings.HasPrefix(p, rmPrefix) || !strings.HasSuffix(p, ".go") {
		return false
	}
	if strings.Contains(p, "/fake/") || strings.Contains(p, "/testdata/") || strings.HasSuffix(p, "_test.go") {
		return false
	}
	return !strings.HasSuffix(p, "/client_factory.go")
}

// linkedModules lists the arm* modules this binary links.
func linkedModules() []*debug.Module {
	bi, ok := debug.ReadBuildInfo()
	if !ok {
		return nil
	}
	return bi.Deps
}

// absentModules reports each linked arm* module the pinned monorepo HEAD no
// longer holds. The universe follows HEAD, not go.mod (pinning each module
// would delete rows HEAD still lists), so a module retired upstream has no
// rows at all: its scanners pair as sdk-skew and its types leave the
// denominator. This says so instead of letting them vanish.
func absentModules(root string, deps []*debug.Module) []sdkinv.Diagnostic {
	var out []sdkinv.Diagnostic
	for _, d := range deps {
		rel, ok := strings.CutPrefix(d.Path, armPrefix)
		if !ok {
			continue
		}
		if i := strings.LastIndex(rel, "/"); i >= 0 && armMajorRe.MatchString(rel[i+1:]) {
			rel = rel[:i]
		}
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(rel))); err == nil {
			continue
		}
		out = append(out, sdkinv.Diagnostic{
			Severity: "warn", Source: d.Path + "@" + d.Version,
			Message: fmt.Sprintf("linked module %s is absent at the pinned SDK HEAD; its operations are outside the universe", rel),
		})
	}
	return out
}

func init() { sdkinv.Register(extractor{}) }

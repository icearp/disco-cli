// Package azureinventory registers the Azure SDK inventory extractor. Source: the
// generated *_client.go files of every arm* module in azure-sdk-for-go, whose
// request builders embed the ARM urlPath template verbatim, plus the models
// and response types needed to tell a collection GET from an item GET.
package azureinventory

import (
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
		KeepID: "arm-go-sources-v2",
	}}
}

// keepARMFile keeps the generated Go sources of arm* modules. Request builders
// live in *_client.go but also in client.go / api_client.go (armresources'
// generic client, armmanagementgroups), so every non-test .go file is kept
// except fakes and the large models_serde.go marshal code.
func keepARMFile(p string) bool {
	if !strings.HasPrefix(p, rmPrefix) || !strings.HasSuffix(p, ".go") {
		return false
	}
	if strings.Contains(p, "/fake/") || strings.Contains(p, "/testdata/") || strings.HasSuffix(p, "_test.go") {
		return false
	}
	base := p[strings.LastIndex(p, "/")+1:]
	return base != "models_serde.go" && base != "client_factory.go"
}

func init() { sdkinv.Register(extractor{}) }

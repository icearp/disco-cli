// Package azure registers the Azure SDK inventory extractor. Source: the
// generated *_client.go files of every arm* module in azure-sdk-for-go, whose
// request builders embed the ARM urlPath template verbatim, plus the models
// and response types needed to tell a collection GET from an item GET.
package azure

import (
	"context"
	"strings"

	"github.com/icearp/disco-cli/internal/sdkinv"
)

const (
	tarballURL = "https://codeload.github.com/Azure/azure-sdk-for-go/tar.gz/" + sdkinv.AzureSDKRef
	// rmPrefix anchors on the shipped modules; the repo also carries
	// profile/*/resourcemanager copies that must not be double-counted.
	rmPrefix = "sdk/resourcemanager/"
)

type extractor struct{}

func (extractor) Name() string { return "azure" }
func (extractor) Ref() string  { return sdkinv.AzureSDKRef }

func (extractor) FetchSpec() []sdkinv.FetchSource {
	return []sdkinv.FetchSource{{
		Name:  "arm-clients",
		Kind:  sdkinv.KindTarball,
		URL:   tarballURL,
		Dest:  "repo",
		Strip: 1,
		Keep:  keepARMFile,
	}}
}

// keepARMFile keeps generated client, model and response-type sources of arm*
// modules; tests, fakes, examples and the client factory carry no paths.
func keepARMFile(p string) bool {
	if !strings.HasPrefix(p, rmPrefix) || !strings.HasSuffix(p, ".go") {
		return false
	}
	if strings.Contains(p, "/fake/") || strings.Contains(p, "/testdata/") || strings.HasSuffix(p, "_test.go") {
		return false
	}
	base := p[strings.LastIndex(p, "/")+1:]
	switch {
	case base == "client_factory.go":
		return false
	case strings.HasSuffix(base, "_client.go"), base == "models.go", base == "response_types.go", base == "responses.go":
		return true
	}
	return false
}

func (extractor) Extract(context.Context, string) (*sdkinv.Universe, error) {
	return nil, sdkinv.ErrNotImplemented
}

func init() { sdkinv.Register(extractor{}) }

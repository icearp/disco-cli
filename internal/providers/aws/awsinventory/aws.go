// Package awsinventory registers the AWS SDK inventory extractor. Sources: the Smithy
// models shipped inside aws-sdk-go-v2 (every service, required/paginated
// traits, output shapes, the sigv4 signing name) and the credential-free AWS
// Service Reference catalog (per-action IsList/IsWrite, per-action target
// resources, the op-to-IAM-action map, per-service resource types with ARN
// templates).
package awsinventory

import (
	"encoding/json"
	"strings"

	"github.com/icearp/disco-cli/internal/sdkinv"
)

const (
	// smithyModelsURL is the aws-sdk-go-v2 source tarball at the pinned tag.
	smithyModelsURL = "https://codeload.github.com/aws/aws-sdk-go-v2/tar.gz/refs/tags/" + SDKRef
	smithyModelsDir = "codegen/sdk-codegen/aws-models/"
	// serviceReferenceURL is the public Service Reference index, the
	// machine-readable IAM Service Authorization Reference.
	serviceReferenceURL = "https://servicereference.us-east-1.amazonaws.com/"
)

type extractor struct{}

func (extractor) Name() string { return "aws" }
func (extractor) Ref() string  { return SDKRef }

func (extractor) FetchSpec() []sdkinv.FetchSource {
	return []sdkinv.FetchSource{
		{
			Name:  "smithy-models",
			Kind:  sdkinv.KindTarball,
			URL:   smithyModelsURL,
			Dest:  "repo",
			Strip: 1,
			Keep: func(p string) bool {
				return strings.HasPrefix(p, smithyModelsDir) && strings.HasSuffix(p, ".json")
			},
			KeepID: "aws-models-json-v1",
		},
		{
			Name:         "service-reference",
			Kind:         sdkinv.KindJSONIndex,
			URL:          serviceReferenceURL,
			Dest:         "service-reference",
			Expand:       expandServiceReference,
			ExpandID:     "sr-services-v1",
			PinnedDigest: ServiceReferenceDigest,
		},
	}
}

type srIndexEntry struct {
	Service string `json:"service"`
	URL     string `json:"url"`
}

func expandServiceReference(index []byte) ([]sdkinv.IndexEntry, error) {
	var entries []srIndexEntry
	if err := json.Unmarshal(index, &entries); err != nil {
		return nil, err
	}
	out := make([]sdkinv.IndexEntry, 0, len(entries))
	for _, e := range entries {
		if e.Service == "" || e.URL == "" {
			continue
		}
		out = append(out, sdkinv.IndexEntry{Name: e.Service, URL: e.URL})
	}
	return out, nil
}

func init() { sdkinv.Register(extractor{}) }

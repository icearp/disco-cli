package main

import (
	"context"
	"go/format"
	"strings"
	"testing"

	"github.com/icearp/disco-cli/internal/coverage"
)

func TestSplitWordsKebabPascal(t *testing.T) {
	cases := []struct {
		in            string
		kebab, pascal string
	}{
		{"Topic", "topic", "Topic"},
		{"RestApi", "rest-api", "RestApi"},
		{"virtualMachines", "virtual-machines", "VirtualMachines"},
		{"resource-name", "resource-name", "ResourceName"},
		{"Chromeosdevice", "chromeosdevice", "Chromeosdevice"},
		{"RESTApi", "rest-api", "RestApi"},
		{"S3Bucket", "s3-bucket", "S3Bucket"},
		{"AuthorizedOrgsDesc", "authorized-orgs-desc", "AuthorizedOrgsDesc"},
	}
	for _, c := range cases {
		if got := kebab(c.in); got != c.kebab {
			t.Errorf("kebab(%q) = %q; want %q", c.in, got, c.kebab)
		}
		if got := pascal(c.in); got != c.pascal {
			t.Errorf("pascal(%q) = %q; want %q", c.in, got, c.pascal)
		}
	}
}

func TestResourceSegment(t *testing.T) {
	cases := []struct{ prov, key, want string }{
		{"aws", "AWS::ApiGateway::RestApi", "RestApi"},
		{"aws", "AWS::sms-voice::Registration", "Registration"},
		{"gcp", "pubsub.googleapis.com/Topic", "Topic"},
		{"azure", "Microsoft.Compute/virtualMachines", "virtualMachines"},
		{"azure", "Microsoft.Network/virtualNetworks/subnets", "subnets"},
	}
	for _, c := range cases {
		if got := resourceSegment(c.prov, c.key); got != c.want {
			t.Errorf("resourceSegment(%q, %q) = %q; want %q", c.prov, c.key, got, c.want)
		}
	}
}

func TestGenScaffold_OmitsRedundantUpstream(t *testing.T) {
	// algo reproduces the key for :topic (no Upstream needed) but not for :queue.
	prov := stubProvider{algo: map[string]string{
		"gcp:pubsub:topic": "pubsub.googleapis.com/Topic",
	}}
	rows := []coverage.Row{
		{Service: "pubsub", UpstreamKey: "pubsub.googleapis.com/Topic", Bucket: coverage.BucketUncovered},
		{Service: "pubsub", UpstreamKey: "pubsub.googleapis.com/Queue", Bucket: coverage.BucketUncovered},
	}
	src := genScaffold("gcp", "pubsub", rows, prov)
	if _, err := format.Source([]byte(src)); err != nil {
		t.Fatalf("generated source does not gofmt/compile-parse: %v\n%s", err, src)
	}
	if strings.Contains(src, `Type: TypePubsubTopic, Service: "pubsub", Upstream:`) {
		t.Error("Topic should omit Upstream (algorithmic key reproduces it)")
	}
	if !strings.Contains(src, `Type: TypePubsubQueue, Service: "pubsub", Upstream: "pubsub.googleapis.com/Queue"`) {
		t.Errorf("Queue should carry Upstream (algorithmic key differs); got:\n%s", src)
	}
	if !strings.Contains(src, "func scanPubsub(ctx context.Context, p *project") {
		t.Error("expected a GCP-shaped stub scanner signature")
	}
}

// stubProvider implements coverage.Provider; only Name + AlgorithmicKey are
// exercised by genScaffold, the rest satisfy the interface.
type stubProvider struct{ algo map[string]string }

func (stubProvider) Name() string { return "gcp" }
func (stubProvider) Fetch(context.Context, coverage.FetchOptions) ([]coverage.UpstreamType, error) {
	return nil, nil
}
func (stubProvider) Emits() []coverage.TypeDecl               { return nil }
func (stubProvider) Aliases() map[string]string               { return nil }
func (s stubProvider) AlgorithmicKey(discoType string) string { return s.algo[discoType] }

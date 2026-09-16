package main

import (
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
	cases := []struct{ key, want string }{
		{"ec2/instance", "instance"},
		{"kms/grant", "grant"},
		{"pubsub/topics", "topic"},
		{"microsoft.compute/virtualmachines", "virtualmachine"},
		{"microsoft.network/virtualnetworks/subnets", "subnet"},
		{"compute/regiondisks", "regiondisk"},
	}
	for _, c := range cases {
		if got := resourceSegment(c.key); got != c.want {
			t.Errorf("resourceSegment(%q) = %q; want %q", c.key, got, c.want)
		}
	}
}

func TestGenScaffold(t *testing.T) {
	rows := []coverage.Row{
		{Service: "pubsub", Key: "pubsub/topics", Bucket: coverage.BucketUncovered, Ops: []string{"pubsub:projects.topics.list"}, Scope: "project"},
		{Service: "pubsub", Key: "pubsub/topics/subscriptions", Bucket: coverage.BucketUncovered, Ops: []string{"pubsub:projects.topics.subscriptions.list"}, Depth: 1, Parent: "pubsub/topics", Scope: "project"},
	}
	src := genScaffold("gcp", "pubsub", rows)
	if _, err := format.Source([]byte(src)); err != nil {
		t.Fatalf("generated source does not gofmt/compile-parse: %v\n%s", err, src)
	}
	for _, want := range []string{
		`TypePubsubTopic        = "gcp:pubsub:topic"`,
		`TypePubsubSubscription = "gcp:pubsub:subscription"`,
		`// pubsub/topics: ops pubsub:projects.topics.list; depth 0; scope project`,
		`registerType(restype.Descriptor{Type: TypePubsubTopic, Service: "pubsub"})`,
		`// pubsub/topics/subscriptions: ops pubsub:projects.topics.subscriptions.list; depth 1 (parent pubsub/topics); scope project`,
		"func scanPubsub(ctx context.Context, p *project",
	} {
		if !strings.Contains(src, want) {
			t.Errorf("scaffold lacks %q:\n%s", want, src)
		}
	}
}

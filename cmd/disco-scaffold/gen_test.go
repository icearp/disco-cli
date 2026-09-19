package main

import (
	"go/format"
	"slices"
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

func TestResourceSegments(t *testing.T) {
	cases := []struct {
		key, service string
		want         []string
	}{
		{"ec2/instance", "ec2", []string{"instance"}},
		{"kms/grant", "kms", []string{"grant"}},
		{"pubsub/topics", "pubsub", []string{"topic"}},
		{"microsoft.compute/virtualmachines", "microsoft.compute", []string{"virtualmachine"}},
		// The parent segment stays: virtualmachines/runcommands and
		// virtualmachinescalesets/virtualmachines/runcommands are different
		// resources and used to produce the same declaration twice.
		{"microsoft.network/virtualnetworks/subnets", "microsoft.network", []string{"virtualnetwork", "subnet"}},
		{"microsoft.compute/virtualmachinescalesets/virtualmachines/runcommands", "microsoft.compute", []string{"virtualmachinescaleset", "virtualmachine", "runcommand"}},
		{"compute/regiondisks", "compute", []string{"regiondisk"}},
		// Shapes Singular can only stem, never spell.
		{"monitoring/timeseries", "monitoring", []string{"timeseries"}},
		{"kendra/thesauri", "kendra", []string{"thesauri"}},
	}
	for _, c := range cases {
		if got := resourceSegments(c.key, c.service); !slices.Equal(got, c.want) {
			t.Errorf("resourceSegments(%q) = %v; want %v", c.key, got, c.want)
		}
	}
}

// TestGenScaffold_RefusesDuplicateAndExistingTypes: a type string this
// provider already declares, or one the rows would declare twice, is a compile
// error in the emitted file that format.Source does not catch.
func TestGenScaffold_RefusesDuplicateAndExistingTypes(t *testing.T) {
	rows := []coverage.Row{
		{Service: "pubsub", Key: "pubsub/topics", Bucket: coverage.BucketUncovered},
	}
	opts := scaffoldOpts{existingTypes: map[string]bool{"gcp:pubsub:topic": true}}
	if _, err := genScaffold("gcp", "pubsub", rows, opts); err == nil {
		t.Error("an already-declared type must refuse the scaffold")
	}
}

// TestGenScaffold_SkipsRegistrationWhenServiceExists: a second registerService
// panics the provider at init and a second func scan<Svc> does not compile.
func TestGenScaffold_SkipsRegistrationWhenServiceExists(t *testing.T) {
	rows := []coverage.Row{{Service: "pubsub", Key: "pubsub/topics", Bucket: coverage.BucketUncovered}}
	src, err := genScaffold("gcp", "pubsub", rows, scaffoldOpts{serviceRegistered: true, existingTypes: map[string]bool{}})
	if err != nil {
		t.Fatal(err)
	}
	for _, unwanted := range []string{"registerService(", "func scanPubsub("} {
		if strings.Contains(src, unwanted) {
			t.Errorf("scaffold emits %q for an already-registered service:\n%s", unwanted, src)
		}
	}
	if !strings.Contains(src, "TypePubsubTopic") {
		t.Errorf("scaffold dropped the types too:\n%s", src)
	}
}

func TestGenScaffold(t *testing.T) {
	rows := []coverage.Row{
		{Service: "pubsub", Key: "pubsub/topics", Bucket: coverage.BucketUncovered, Ops: []string{"pubsub:projects.topics.list"}, Scope: "project"},
		{Service: "pubsub", Key: "pubsub/topics/subscriptions", Bucket: coverage.BucketUncovered, Ops: []string{"pubsub:projects.topics.subscriptions.list"}, Depth: 1, Parent: "pubsub/topics", Scope: "project"},
	}
	src, err := genScaffold("gcp", "pubsub", rows, scaffoldOpts{existingTypes: map[string]bool{}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := format.Source([]byte(src)); err != nil {
		t.Fatalf("generated source does not gofmt/compile-parse: %v\n%s", err, src)
	}
	for _, want := range []string{
		// gofmt aligns the const block, so match the value, not the spacing.
		`= "gcp:pubsub:topic"`,
		`= "gcp:pubsub:topic:subscription"`,
		"TypePubsubTopic ",
		"TypePubsubTopicSubscription ",
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

package regionsgen

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/icearp/disco-cli/internal/providers/aws/awsregions"
)

// TestGeneratedPartitionsFileIsCurrent is the partition table's staleness
// guard, for the same reason TestGeneratedFileIsCurrent exists: an SDK bump
// that adds a partition or region changes nothing visible until a scan of that
// partition is refused as unknown.
func TestGeneratedPartitionsFileIsCurrent(t *testing.T) {
	root := repoRoot(t)

	parts := loadSource(t).Partitions
	want, err := RenderPartitions(parts)
	if err != nil {
		t.Fatalf("RenderPartitions: %v", err)
	}

	path := filepath.Join(root, "internal", "providers", "aws", "awsregions", PartitionsFile)
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if string(got) != string(want) {
		t.Errorf("%s is stale (%d partitions regenerate to %d bytes, file has %d); run `make gen-regions` and commit the result",
			PartitionsFile, len(parts), len(want), len(got))
	}
}

// TestParsePartitions_RejectsIncompletePartition pins that a partition the
// schema no longer describes fully fails generation instead of vanishing from
// the table, where it would read as a partition disco does not support.
func TestParsePartitions_RejectsIncompletePartition(t *testing.T) {
	cases := map[string]string{
		"no partitions":  `{"partitions":[]}`,
		"no id":          `{"partitions":[{"regionRegex":"^us\\-\\w+\\-\\d+$","outputs":{"dnsSuffix":"amazonaws.com"}}]}`,
		"no dnsSuffix":   `{"partitions":[{"id":"aws","regionRegex":"^us\\-\\w+\\-\\d+$","outputs":{}}]}`,
		"no regionRegex": `{"partitions":[{"id":"aws","outputs":{"dnsSuffix":"amazonaws.com"}}]}`,
		"non-RE2 regex":  `{"partitions":[{"id":"aws","regionRegex":"^(?=us)","outputs":{"dnsSuffix":"amazonaws.com"}}]}`,
		"malformed json": `{"partitions":`,
	}
	for name, src := range cases {
		t.Run(name, func(t *testing.T) {
			if got, err := parsePartitions("partitions.json", []byte(src)); err == nil {
				t.Errorf("parsePartitions(%s) = %+v, nil; want an error", src, got)
			}
		})
	}

	t.Run("complete", func(t *testing.T) {
		src := `{"partitions":[{"id":"aws","regionRegex":"^us\\-\\w+\\-\\d+$","regions":{"us-west-2":{},"us-east-1":{}},` +
			`"outputs":{"dnsSuffix":"amazonaws.com","implicitGlobalRegion":"us-east-1"}}]}`
		got, err := parsePartitions("partitions.json", []byte(src))
		if err != nil {
			t.Fatalf("parsePartitions(complete) error: %v", err)
		}
		if len(got) != 1 {
			t.Fatalf("parsePartitions(complete) = %+v; want one partition", got)
		}
		p := got[0]
		if p.ID != "aws" || p.DNSSuffix != "amazonaws.com" || p.DualStackDNSSuffix != "" || p.ImplicitGlobalRegion != "us-east-1" {
			t.Errorf("parsePartitions(complete) = %+v; want id aws, suffix amazonaws.com, no dual-stack suffix, global us-east-1", p)
		}
		if len(p.Regions) != 2 || p.Regions[0] != "us-east-1" || p.Regions[1] != "us-west-2" {
			t.Errorf("parsePartitions(complete).Regions = %v; want [us-east-1 us-west-2] (sorted)", p.Regions)
		}
	})
}

// The global pseudo-region names an endpoint, not a region a scan can be sent
// to, so it is not one of a partition's real regions.
func TestRealRegions_DropsPseudoRegions(t *testing.T) {
	p := awsregions.Partition{ID: "aws", RegionRegex: `^(us|eu)\-\w+\-\d+$`, Regions: []string{"aws-global", "eu-west-1", "us-east-1"}}
	if got := realRegions(p); !slices.Equal(got, []string{"eu-west-1", "us-east-1"}) {
		t.Errorf("realRegions(%v) = %v; want [eu-west-1 us-east-1]", p.Regions, got)
	}
}

// A region two partitions list would make the account partition read from it
// ambiguous, so generation fails instead of picking one.
func TestRenderPartitions_RejectsSharedRegion(t *testing.T) {
	parts := []awsregions.Partition{
		{ID: "aws", RegionRegex: `^us\-\w+\-\d+$`, DNSSuffix: "amazonaws.com", Regions: []string{"us-east-1"}},
		{ID: "aws-x", RegionRegex: `^us\-\w+\-\d+$`, DNSSuffix: "example", Regions: []string{"us-east-1"}},
	}
	if _, err := RenderPartitions(parts); err == nil {
		t.Error("RenderPartitions with us-east-1 in two partitions = nil error; want an error")
	}
	parts[1].Regions = []string{"us-east-2"}
	if _, err := RenderPartitions(parts); err != nil {
		t.Errorf("RenderPartitions with disjoint regions: %v", err)
	}
}

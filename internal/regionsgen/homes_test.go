package regionsgen

import (
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"testing"
)

// TestGeneratedHomesFileIsCurrent is the home table's staleness guard: an SDK
// bump that moves or adds a service's endpoint changes where global scanners
// call only once the table is regenerated.
func TestGeneratedHomesFileIsCurrent(t *testing.T) {
	root := repoRoot(t)
	src := loadSource(t)

	homes := src.Homes()
	want, err := RenderHomes(homes, src.OnceRegions())
	if err != nil {
		t.Fatalf("RenderHomes: %v", err)
	}

	path := filepath.Join(root, "internal", "providers", "aws", "awsregions", HomesFile)
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if string(got) != string(want) {
		t.Errorf("%s is stale (%d packages regenerate to %d bytes, file has %d); run `make gen-regions` and commit the result",
			HomesFile, len(homes), len(want), len(got))
	}
}

// testSource is a Source over one package, "svc", whose partition "aws" has
// real regions us-east-1, us-east-2 and us-west-2 and implicit global region
// us-east-1.
func testSource(tables ...endpointTable) *Source {
	return &Source{
		servicePkgs: map[string][]string{"aws:svc": {"svc"}},
		tables:      map[string][]endpointTable{"svc": tables},
		real:        map[string][]string{"aws": {"us-east-1", "us-east-2", "us-west-2"}},
		implicit:    map[string]string{"aws": "us-east-1"},
	}
}

// TestHomesAndOnceRegions walks each rule of Source.Homes, the cases that must
// give no home, and what OnceRegions picks for each.
func TestHomesAndOnceRegions(t *testing.T) {
	cases := []struct {
		name       string
		table      endpointTable
		home, once string // "" for none
	}{
		{
			"rule 1: partition endpoint's credential scope",
			endpointTable{partitionEndpoint: "aws-global", scope: map[string]string{"aws-global": "us-west-2", "us-east-1": ""}},
			"us-west-2", "us-west-2",
		},
		{
			"rule 1: no credential scope is no home",
			endpointTable{partitionEndpoint: "aws-global", scope: map[string]string{"aws-global": ""}},
			"", "",
		},
		{
			"rule 2: single listed region",
			endpointTable{regionalized: true, scope: map[string]string{"us-west-2": ""}},
			"us-west-2", "us-west-2",
		},
		{
			"rule 2: pseudo-regions do not count as listed",
			endpointTable{regionalized: true, scope: map[string]string{"us-east-2": "", "aws-global": "us-east-1", "fips-us-east-1": ""}},
			"us-east-2", "us-east-2",
		},
		{
			"several regions including the implicit global region: no home, once there",
			endpointTable{regionalized: true, scope: map[string]string{"us-east-1": "", "us-west-2": ""}},
			"", "us-east-1",
		},
		{
			"several regions without the implicit global region",
			endpointTable{regionalized: true, scope: map[string]string{"us-east-2": "", "us-west-2": ""}},
			"", "",
		},
		{
			"non-regionalized without a credential scope: no once-region from its region keys",
			endpointTable{partitionEndpoint: "aws-global", scope: map[string]string{"aws-global": "", "us-east-1": ""}},
			"", "",
		},
		{
			"empty table",
			endpointTable{regionalized: true, scope: map[string]string{}},
			"", "",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			c.table.id = "aws"
			src := testSource(c.table)
			if got := src.Homes()["svc"]["aws"]; got != c.home {
				t.Errorf("Homes()[svc][aws] = %q; want %q", got, c.home)
			}
			if got := src.OnceRegions()["svc"]["aws"]; got != c.once {
				t.Errorf("OnceRegions()[svc][aws] = %q; want %q", got, c.once)
			}
		})
	}
}

// A non-regionalized partition entry sends every request to one global
// endpoint, so a real-region key it carries adds no ServiceRegions entry, and
// the list does not speak for that partition (Partitions omits it).
func TestServiceRegions_SkipsNonRegionalizedEntries(t *testing.T) {
	global := endpointTable{id: "aws", partitionEndpoint: "aws-global", scope: map[string]string{"aws-global": "us-east-1", "us-east-1": ""}}
	if got := testSource(global).ServiceRegions(); len(got.Regions) != 0 || len(got.Partitions) != 0 {
		t.Errorf("ServiceRegions() with only a non-regionalized entry = %+v; want no key", got)
	}
	regional := endpointTable{id: "aws", regionalized: true, scope: map[string]string{"us-west-2": "", "us-east-1": "", "fips-us-east-1": ""}}
	src := testSource(regional)
	src.real["aws-cn"] = []string{"cn-north-1"}
	src.tables["svc"] = append(src.tables["svc"], endpointTable{id: "aws-cn", partitionEndpoint: "aws-cn-global", scope: map[string]string{"aws-cn-global": "cn-north-1"}})
	got := src.ServiceRegions()
	if !slices.Equal(got.Regions["aws:svc"], []string{"us-east-1", "us-west-2"}) {
		t.Errorf("Regions[aws:svc] = %v; want [us-east-1 us-west-2]", got.Regions["aws:svc"])
	}
	if !slices.Equal(got.Partitions["aws:svc"], []string{"aws"}) {
		t.Errorf("Partitions[aws:svc] = %v; want [aws], not the non-regionalized aws-cn", got.Partitions["aws:svc"])
	}
}

// A regionalized entry that lists no real region (only Defaults or
// pseudo-regions) asserts nothing, so its partition stays uncovered.
func TestServiceRegions_EmptyRegionalizedEntryIsNotCovered(t *testing.T) {
	src := testSource(endpointTable{id: "aws", regionalized: true, scope: map[string]string{"us-west-2": ""}})
	src.real["aws-us-gov"] = []string{"us-gov-west-1"}
	src.tables["svc"] = append(src.tables["svc"], endpointTable{id: "aws-us-gov", regionalized: true, scope: map[string]string{"fips-us-gov-west-1": ""}})
	if got := src.ServiceRegions().Partitions["aws:svc"]; !slices.Equal(got, []string{"aws"}) {
		t.Errorf("Partitions[aws:svc] = %v; want [aws], not the empty aws-us-gov entry", got)
	}
}

// TestEndpointTables_ParsesSDKShape pins the parse against the shape the SDK
// generates: variant entries are skipped, a CredentialScope region is read, and
// a non-regionalized partition keeps its PartitionEndpoint.
func TestEndpointTables_ParsesSDKShape(t *testing.T) {
	src := `package endpoints

var defaultPartitions = endpoints.Partitions{
	{
		ID:             "aws",
		IsRegionalized: true,
		Endpoints: endpoints.Endpoints{
			endpoints.EndpointKey{Region: "us-east-1"}: endpoints.Endpoint{},
			endpoints.EndpointKey{Region: "us-west-2", Variant: endpoints.FIPSVariant}: {},
		},
	},
	{
		ID:                "aws-cn",
		IsRegionalized:    false,
		PartitionEndpoint: "aws-cn-global",
		Endpoints: endpoints.Endpoints{
			endpoints.EndpointKey{Region: "aws-cn-global"}: endpoints.Endpoint{
				CredentialScope: endpoints.CredentialScope{Region: "cn-northwest-1"},
			},
		},
	},
}
`
	path := filepath.Join(t.TempDir(), "endpoints.go")
	if err := os.WriteFile(path, []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := endpointTables(path)
	if err != nil {
		t.Fatalf("endpointTables: %v", err)
	}
	want := []endpointTable{
		{id: "aws", regionalized: true, scope: map[string]string{"us-east-1": ""}},
		{id: "aws-cn", partitionEndpoint: "aws-cn-global", scope: map[string]string{"aws-cn-global": "cn-northwest-1"}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("endpointTables = %+v; want %+v", got, want)
	}
}

// A package without an endpoints file has no table, and so no home; that is
// not an error.
func TestEndpointTables_MissingFile(t *testing.T) {
	got, err := endpointTables(filepath.Join(t.TempDir(), "endpoints.go"))
	if err != nil || got != nil {
		t.Errorf("endpointTables(missing) = %v, %v; want nil, nil", got, err)
	}
}

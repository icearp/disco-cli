package awsregions

import (
	"regexp"
	"slices"
	"testing"
)

// TestRegionsByPartitionAnchors pins the region lists a regeneration must keep:
// every partition's regions match its own regex, its implicit global region is
// among them, the pseudo-regions are not, and Regions is exactly their union.
func TestRegionsByPartitionAnchors(t *testing.T) {
	var union []string
	for _, p := range Partitions {
		regions, ok := RegionsByPartition[p.ID]
		if !ok {
			t.Errorf("RegionsByPartition has no entry for partition %s", p.ID)
			continue
		}
		re := regexp.MustCompile(p.RegionRegex)
		for _, r := range regions {
			if !re.MatchString(r) {
				t.Errorf("RegionsByPartition[%s] has %q, which fails the partition's regionRegex", p.ID, r)
			}
		}
		if p.ImplicitGlobalRegion != "" && !slices.Contains(regions, p.ImplicitGlobalRegion) {
			t.Errorf("RegionsByPartition[%s] omits its implicit global region %s", p.ID, p.ImplicitGlobalRegion)
		}
		union = append(union, regions...)
	}
	slices.Sort(union)
	if !slices.Equal(union, Regions) {
		t.Errorf("Regions = %v; want the sorted union of RegionsByPartition %v", Regions, union)
	}
	for partition, region := range map[string]string{"aws": "us-east-1", "aws-us-gov": "us-gov-west-1", "aws-cn": "cn-north-1"} {
		if !slices.Contains(RegionsByPartition[partition], region) {
			t.Errorf("RegionsByPartition[%s] omits %s", partition, region)
		}
	}
	if slices.Contains(Regions, "aws-global") {
		t.Error("Regions includes the aws-global pseudo-region")
	}
}

// TestHomesAnchors pins homes and once-regions a regeneration must not lose,
// that every one is a region of its own partition, and that every home is
// also its package's once-region.
func TestHomesAnchors(t *testing.T) {
	for name, table := range map[string]map[string]map[string]string{"Homes": Homes, "OnceRegions": OnceRegions} {
		for pkg, byPartition := range table {
			for partition, region := range byPartition {
				if !slices.Contains(RegionsByPartition[partition], region) {
					t.Errorf("%s[%s][%s] = %s, which is not a region of %s", name, pkg, partition, region, partition)
				}
			}
		}
	}
	for pkg, byPartition := range Homes {
		for partition, home := range byPartition {
			if once := OnceRegions[pkg][partition]; once != home {
				t.Errorf("OnceRegions[%s][%s] = %q; want the home %s", pkg, partition, once, home)
			}
		}
	}
	for _, c := range []struct{ pkg, partition, want string }{
		{"route53", "aws", "us-east-1"},
		{"route53", "aws-us-gov", "us-gov-west-1"},
		{"networkmanager", "aws", "us-west-2"},
		{"devicefarm", "aws", "us-west-2"},
	} {
		if got := Homes[c.pkg][c.partition]; got != c.want {
			t.Errorf("Homes[%s][%s] = %q; want %q", c.pkg, c.partition, got, c.want)
		}
	}
	// A regional service listed in many regions has no home, only a
	// once-region: the partition's implicit global region.
	for _, c := range []struct{ pkg, partition, want string }{
		{"ec2", "aws", "us-east-1"},
		{"ec2", "aws-cn", "cn-northwest-1"},
		{"s3", "aws-us-gov", "us-gov-west-1"},
	} {
		if home, ok := Homes[c.pkg][c.partition]; ok {
			t.Errorf("Homes[%s][%s] = %s; the SDK lists it in several regions, so it has no home", c.pkg, c.partition, home)
		}
		if got := OnceRegions[c.pkg][c.partition]; got != c.want {
			t.Errorf("OnceRegions[%s][%s] = %q; want %q", c.pkg, c.partition, got, c.want)
		}
	}
	if home, ok := Homes["budgets"]["aws-us-gov"]; ok {
		t.Errorf("Homes[budgets][aws-us-gov] = %s; the SDK lists Budgets in no GovCloud region", home)
	}
}

func TestPartitionForRegion(t *testing.T) {
	cases := []struct {
		in     string
		want   string
		wantOK bool
	}{
		{"us-east-1", "aws", true},
		{"cn-northwest-1", "aws-cn", true},
		{"us-gov-west-1", "aws-us-gov", true},
		{"eusc-de-east-1", "aws-eusc", true},
		{"us-isob-east-1", "aws-iso-b", true},
		// Listed by name only; no regionRegex matches it.
		{"aws-us-gov-global", "aws-us-gov", true},
		// Not listed by name in partitions.json: only the regex places them.
		{"eu-west-9", "aws", true},
		{"cn-south-7", "aws-cn", true},
		{"us-gov-central-4", "aws-us-gov", true},
		// The SDK would put these in aws; disco must not guess.
		{"mars-north-1", "", false},
		{"all", "", false},
		{"", "", false},
	}
	for _, c := range cases {
		got, ok := PartitionForRegion(c.in)
		if got != c.want || ok != c.wantOK {
			t.Errorf("PartitionForRegion(%q) = (%q, %v); want (%q, %v)", c.in, got, ok, c.want, c.wantOK)
		}
	}
}

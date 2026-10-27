package regionsgen

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"os"
	"slices"
	"sort"
	"strings"
)

// HomesFile is the basename tools/genregions writes the SDK-package home
// table to, next to GeneratedFile.
const HomesFile = "homes_generated.go"

// Homes returns, for each aws-sdk-go-v2 service package a disco scanner file
// imports, its home region in each partition: the one region the SDK's own
// endpoint table confirms an account-wide call goes to.
//
// Two rules confirm a home, applied per partition entry of that table:
//
//  1. The partition is not regionalized (IsRegionalized: false): every request
//     goes to its PartitionEndpoint, and that endpoint's CredentialScope.Region
//     is the region the SDK signs it for. That region is the home. A
//     non-regionalized partition whose endpoint names no CredentialScope.Region
//     has no home.
//  2. The partition is regionalized and lists the service in exactly one real
//     region: the service exists only there, so that region is the home.
//
// A regionalized partition listing several regions has no home: the table
// says where the service runs, not which region holds the account-wide view.
// Nothing is guessed; the caller makes no call there. A package that ships no
// table, or whose table gives no partition a home, is absent.
func (s *Source) Homes() map[string]map[string]string {
	return s.perPackage(func(t endpointTable) (string, bool) { return t.home(s.real[t.id]) })
}

// OnceRegions returns, for each SDK package and partition, one region the
// package's endpoint table lists, chosen so a call whose answer is the same
// in every region runs once per account: the home when Homes confirms one,
// otherwise the partition's implicit global region (partitions.json) when the
// table lists the service there. It is not a home: the call could go to any
// listed region, and this one is picked only to make the choice stable.
func (s *Source) OnceRegions() map[string]map[string]string {
	return s.perPackage(func(t endpointTable) (string, bool) {
		if home, ok := t.home(s.real[t.id]); ok {
			return home, true
		}
		implicit := s.implicit[t.id]
		if t.regionalized && implicit != "" && slices.Contains(t.listed(s.real[t.id]), implicit) {
			return implicit, true
		}
		return "", false
	})
}

// S3RegionlessHosts returns, per partition, the hostnames the S3 package's
// endpoint table gives an endpoint whose hostname names no real region of any
// partition as a label (s3.amazonaws.com for "aws-global",
// s3-external-1.amazonaws.com). A partition listing none is absent. Resolvers
// accept a region-less S3 host from this table for the account's partition;
// the host itself carries no region to check. (S3 Transfer Acceleration hosts
// are the one other region-less form they accept, from a hand-coded fact the
// SDK cannot supply.)
func (s *Source) S3RegionlessHosts() map[string][]string {
	allRegions := map[string]bool{}
	for _, regions := range s.real {
		for _, r := range regions {
			allRegions[r] = true
		}
	}
	out := map[string][]string{}
	for _, t := range s.tables["s3"] {
		for _, host := range t.hostnames {
			if namesRegion(host, allRegions) || slices.Contains(out[t.id], host) {
				continue
			}
			out[t.id] = append(out[t.id], host)
		}
		slices.Sort(out[t.id])
	}
	return out
}

// namesRegion reports whether one of host's dot-separated labels is a region.
func namesRegion(host string, regions map[string]bool) bool {
	return slices.ContainsFunc(strings.Split(host, "."), func(label string) bool { return regions[label] })
}

// perPackage applies pick to every partition entry of every package's table,
// keeping the packages it gives at least one region.
func (s *Source) perPackage(pick func(endpointTable) (string, bool)) map[string]map[string]string {
	out := make(map[string]map[string]string, len(s.tables))
	for pkg, tables := range s.tables {
		regions := map[string]string{}
		for _, t := range tables {
			if r, ok := pick(t); ok {
				regions[t.id] = r
			}
		}
		if len(regions) > 0 {
			out[pkg] = regions
		}
	}
	return out
}

// scannerImports returns the SDK service packages any *_scanners.go file
// imports, including files that register no service of their own (such as
// ecr_extended_scanners.go, whose ecrpublic client a registered scanner
// calls).
func scannerImports(root string) (map[string]bool, error) {
	files, err := scannerFiles(root)
	if err != nil {
		return nil, err
	}
	fset := token.NewFileSet()
	out := map[string]bool{}
	for _, path := range files {
		file, err := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
		if err != nil {
			return nil, fmt.Errorf("regionsgen: parse %s: %w", path, err)
		}
		for _, pkg := range sdkServicePackages(file) {
			out[pkg] = true
		}
	}
	return out, nil
}

// endpointTable is one partition entry of an SDK endpoints file.
type endpointTable struct {
	id                string
	regionalized      bool
	partitionEndpoint string
	// scope maps each variant-free endpoint key to its CredentialScope.Region,
	// "" when the entry names none. Variant keys (FIPS, dual-stack) are left
	// out: they repeat a region the variant-free key already names.
	scope map[string]string
	// hostnames maps each variant-free endpoint key that names a Hostname to
	// it; nil when none does.
	hostnames map[string]string
}

// listed returns the regions of real the table has an entry for, in real's
// order, so pseudo-region keys ("aws-global", "fips-*") never count.
func (t endpointTable) listed(real []string) []string {
	var out []string
	for _, r := range real {
		if _, ok := t.scope[r]; ok {
			out = append(out, r)
		}
	}
	return out
}

// home applies the two rules documented on Source.Homes, given the
// partition's real regions.
func (t endpointTable) home(real []string) (string, bool) {
	if !t.regionalized {
		r := t.scope[t.partitionEndpoint]
		return r, r != ""
	}
	if listed := t.listed(real); len(listed) == 1 {
		return listed[0], true
	}
	return "", false
}

// endpointTables parses every partition entry of an SDK endpoints file. A
// package without the file contributes nothing.
func endpointTables(path string) ([]endpointTable, error) {
	src, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("regionsgen: read %s: %w", path, err)
	}
	file, err := parser.ParseFile(token.NewFileSet(), path, src, 0)
	if err != nil {
		return nil, fmt.Errorf("regionsgen: parse %s: %w", path, err)
	}
	partitions := findValueSpec(file, "defaultPartitions")
	if partitions == nil {
		return nil, nil
	}
	var out []endpointTable
	for _, elt := range partitions.Elts {
		part, ok := elt.(*ast.CompositeLit)
		if !ok {
			continue
		}
		t, err := parseEndpointTable(path, part)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, nil
}

func parseEndpointTable(path string, part *ast.CompositeLit) (endpointTable, error) {
	id, ok := stringField(part, "ID")
	if !ok {
		return endpointTable{}, fmt.Errorf("regionsgen: %s: a partition entry has no string ID; the endpoints-file shape changed", path)
	}
	regionalized, ok := fieldValue(part, "IsRegionalized").(*ast.Ident)
	if !ok || (regionalized.Name != "true" && regionalized.Name != "false") {
		return endpointTable{}, fmt.Errorf("regionsgen: %s: partition %s has no literal IsRegionalized; the endpoints-file shape changed", path, id)
	}
	t := endpointTable{id: id, regionalized: regionalized.Name == "true", scope: map[string]string{}}
	t.partitionEndpoint, _ = stringField(part, "PartitionEndpoint")
	if !t.regionalized && t.partitionEndpoint == "" {
		return endpointTable{}, fmt.Errorf("regionsgen: %s: partition %s is not regionalized but names no PartitionEndpoint; the endpoints-file shape changed", path, id)
	}

	endpoints, _ := fieldValue(part, "Endpoints").(*ast.CompositeLit)
	if endpoints == nil {
		return t, nil
	}
	for _, entry := range endpoints.Elts {
		kv, ok := entry.(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		key, ok := kv.Key.(*ast.CompositeLit)
		if !ok || fieldValue(key, "Variant") != nil {
			continue
		}
		region, ok := stringField(key, "Region")
		if !ok {
			continue
		}
		t.scope[region] = ""
		value, ok := kv.Value.(*ast.CompositeLit)
		if !ok {
			continue
		}
		if cs, ok := fieldValue(value, "CredentialScope").(*ast.CompositeLit); ok {
			t.scope[region], _ = stringField(cs, "Region")
		}
		if host, ok := stringField(value, "Hostname"); ok {
			if t.hostnames == nil {
				t.hostnames = map[string]string{}
			}
			t.hostnames[region] = host
		}
	}
	return t, nil
}

// RenderHomes formats the home and once-region tables and the region-less S3
// hosts as the source of the generated awsregions file.
func RenderHomes(homes, once map[string]map[string]string, s3Regionless map[string][]string) ([]byte, error) {
	var buf bytes.Buffer
	buf.WriteString(`// Code generated by tools/genregions. DO NOT EDIT.

package awsregions

// Homes maps each aws-sdk-go-v2 service package disco's scanners import to
// its home region in each partition: the region the endpoint table the package
// embeds confirms an account-wide call goes to (the rules are documented on
// regionsgen.Source.Homes). A partition absent from a package's entry, or a
// package absent from the map, has no SDK-confirmed home: the caller makes no
// call there rather than guess one. Treat as read-only.
var Homes = `)
	writeRegionTable(&buf, homes)
	buf.WriteString(`
// OnceRegions maps each SDK package to one region its endpoint table lists in
// each partition, chosen so a call whose answer is the same in every region
// runs once per account (regionsgen.Source.OnceRegions). It is not a home.
// Treat as read-only.
var OnceRegions = `)
	writeRegionTable(&buf, once)
	buf.WriteString(`
// S3RegionlessHosts maps each partition to the S3 endpoint hostnames that name
// no region, as the S3 package's endpoint table gives them
// (regionsgen.Source.S3RegionlessHosts). A partition absent here has none.
// Treat as read-only.
var S3RegionlessHosts = map[string][]string{
`)
	partitions := make([]string, 0, len(s3Regionless))
	for p := range s3Regionless {
		partitions = append(partitions, p)
	}
	sort.Strings(partitions)
	for _, p := range partitions {
		fmt.Fprintf(&buf, "\t%q: {", p)
		for _, h := range s3Regionless[p] {
			fmt.Fprintf(&buf, "%q, ", h)
		}
		buf.WriteString("},\n")
	}
	buf.WriteString("}\n")

	formatted, err := format.Source(buf.Bytes())
	if err != nil {
		return nil, fmt.Errorf("regionsgen: format generated homes source: %w", err)
	}
	return formatted, nil
}

func writeRegionTable(buf *bytes.Buffer, table map[string]map[string]string) {
	pkgs := make([]string, 0, len(table))
	for pkg := range table {
		pkgs = append(pkgs, pkg)
	}
	sort.Strings(pkgs)
	buf.WriteString("map[string]map[string]string{\n")
	for _, pkg := range pkgs {
		fmt.Fprintf(buf, "\t%q: {\n", pkg)
		partitions := make([]string, 0, len(table[pkg]))
		for p := range table[pkg] {
			partitions = append(partitions, p)
		}
		sort.Strings(partitions)
		for _, p := range partitions {
			fmt.Fprintf(buf, "\t\t%q: %q,\n", p, table[pkg][p])
		}
		buf.WriteString("\t},\n")
	}
	buf.WriteString("}\n")
}

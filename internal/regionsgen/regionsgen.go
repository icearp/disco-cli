// Package regionsgen derives, for each disco AWS service, the regions AWS
// offers that service in — by reading the region table the AWS SDK already
// embeds in every service package.
//
// The join key is the SDK PACKAGE, not the service name. Each scanner file
// imports exactly the SDK service package it scans, and that package carries
// AWS's own generated endpoint table at internal/endpoints/endpoints.go. So the
// import IS the mapping, and no name-to-catalog-code translation is needed —
// which matters because disco service names diverge from AWS's
// global-infrastructure codes for 83 of 297 services (keyspaces is filed as
// "mcs", connectcampaignsv2 as "connectcampaigns"), and no derivation from the
// name gets that right.
//
// Load is separate from the tools/genregions command so the staleness guards
// can call it: a test rebuilds each table in memory and diffs it against the
// committed one. disco's CI runs `go test ./...` and no make targets, so a test
// is the only guard that actually gates.
package regionsgen

import (
	"bytes"
	"errors"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/icearp/disco-cli/internal/providers/aws/awsregions"
)

// sdkModulePrefix is the module path prefix every aws-sdk-go-v2 service package
// shares. The path segment after it is the service package name.
const sdkModulePrefix = "github.com/aws/aws-sdk-go-v2/service/"

// GeneratedFile is the basename tools/genregions writes and the staleness test
// points at.
const GeneratedFile = "services_generated.go"

// Source is everything the generated tables are derived from, read once: the
// SDK packages the scanner files import, the pinned SDK's partitions and the
// endpoint table of each imported package. ServiceRegions, Homes and
// OnceRegions all read the same parsed tables.
type Source struct {
	Partitions []awsregions.Partition
	// servicePkgs maps each registered disco AWS service to the SDK packages
	// its scanner file imports.
	servicePkgs map[string][]string
	// tables maps each SDK package any scanner file imports to its endpoint
	// table entries, one per partition; a package without a table maps to nil.
	tables map[string][]endpointTable
	// real and implicit are each partition's real regions and implicit global
	// region, by partition id.
	real     map[string][]string
	implicit map[string]string
}

// Load reads the Source from the repository at root, against the SDK versions
// pinned in its go.mod. It runs `go list -m all` once.
//
// It reports an error rather than an empty Source when it parses no scanner
// files or resolves no SDK modules: a silently empty table would disable
// region scoping everywhere while looking perfectly healthy.
func Load(root string) (*Source, error) {
	servicePkgs, err := scannerServicePackages(root)
	if err != nil {
		return nil, err
	}
	if len(servicePkgs) == 0 {
		return nil, errors.New("regionsgen: no registerService entries found; the scanner files or registration shape changed")
	}
	imported, err := scannerImports(root)
	if err != nil {
		return nil, err
	}
	dirs, err := moduleDirs(root)
	if err != nil {
		return nil, err
	}
	coreDir, ok := dirs[sdkCoreModule]
	if !ok {
		return nil, errors.New("regionsgen: " + sdkCoreModule + " has no module directory; run `go mod download` first")
	}
	parts, err := readPartitions(coreDir)
	if err != nil {
		return nil, err
	}

	src := &Source{
		Partitions:  parts,
		servicePkgs: servicePkgs,
		tables:      make(map[string][]endpointTable, len(imported)),
		real:        regionsByPartition(parts),
		implicit:    make(map[string]string, len(parts)),
	}
	for _, p := range parts {
		src.implicit[p.ID] = p.ImplicitGlobalRegion
	}
	for pkg := range imported {
		dir, ok := dirs[sdkModulePrefix+pkg]
		if !ok {
			return nil, fmt.Errorf("regionsgen: a scanner imports SDK package %s, which go list -m all does not resolve; run `go mod download` first", pkg)
		}
		tables, err := endpointTables(filepath.Join(dir, "internal", "endpoints", "endpoints.go"))
		if err != nil {
			return nil, err
		}
		src.tables[pkg] = tables
	}
	return src, nil
}

// ServiceRegions returns, by disco AWS service name, the regions AWS offers it
// in and the partitions that list speaks for, from the endpoint tables of the SDK packages its scanner file imports.
//
// A region counts when a regionalized partition entry lists it without a
// variant and it is one of that partition's real regions, so the "fips-*"
// pseudo-regions and endpoint keys such as "aws-global" never count. A
// non-regionalized entry (IsRegionalized: false) is skipped: every request in
// that partition goes to one global endpoint, so a region key it carries says
// nothing about where the service runs. A partition whose regionalized entry
// lists no real region adds none and is not covered (see Partitions).
//
// Partitions records, per service, the partitions where at least one of its
// packages has a regionalized entry listing a real region: only there does the
// table assert where the service runs. In any other partition (a
// non-regionalized entry, an entry with only Defaults or pseudo-regions, or
// none) the table has no opinion, and callers must fail open.
//
// The result omits services whose scanner imports no SDK service package, or
// whose tables list no region; an absent service means "no opinion", and
// callers must fail open.
func (s *Source) ServiceRegions() ServiceTable {
	type pkgData struct{ regions, partitions []string }
	byPkg := make(map[string]pkgData, len(s.tables))
	for pkg, tables := range s.tables {
		var d pkgData
		for _, t := range tables {
			listed := t.listed(s.real[t.id])
			if t.regionalized && len(listed) > 0 {
				d.regions = append(d.regions, listed...)
				d.partitions = append(d.partitions, t.id)
			}
		}
		byPkg[pkg] = d
	}

	out := ServiceTable{Regions: map[string][]string{}, Partitions: map[string][]string{}}
	for service, pkgs := range s.servicePkgs {
		// Union, not intersection: a scanner importing several SDK packages
		// (chime pulls four chimesdk* clients, ses three) is reachable wherever
		// ANY of its clients is. Union over-scans; intersection would skip a
		// region one client genuinely serves.
		var regions, partitions []string
		for _, pkg := range pkgs {
			regions = append(regions, byPkg[pkg].regions...)
			partitions = append(partitions, byPkg[pkg].partitions...)
		}
		if len(regions) == 0 {
			continue
		}
		out.Regions[service] = sortedUnique(regions)
		out.Partitions[service] = sortedUnique(partitions)
	}
	return out
}

// ServiceTable is Source.ServiceRegions' result: each service's listed regions,
// and the partitions whose regions that list speaks for.
type ServiceTable struct {
	Regions    map[string][]string
	Partitions map[string][]string
}

func sortedUnique(xs []string) []string {
	slices.Sort(xs)
	return slices.Compact(xs)
}

// scannerServicePackages maps each disco AWS service name to the SDK service
// packages its scanner file imports.
func scannerServicePackages(root string) (map[string][]string, error) {
	files, err := scannerFiles(root)
	if err != nil {
		return nil, err
	}
	fset := token.NewFileSet()
	out := map[string][]string{}
	for _, path := range files {
		file, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			return nil, fmt.Errorf("regionsgen: parse %s: %w", path, err)
		}
		pkgs := sdkServicePackages(file)
		if len(pkgs) == 0 {
			continue
		}
		for _, name := range registeredServiceNames(file) {
			// A file registering several services shares its imports across all
			// of them; that is the same over-scanning union as above.
			out[name] = pkgs
		}
	}
	return out, nil
}

func scannerFiles(root string) ([]string, error) {
	glob := filepath.Join(root, "internal", "providers", "aws", "*_scanners.go")
	files, err := filepath.Glob(glob)
	if err != nil {
		return nil, fmt.Errorf("regionsgen: glob %s: %w", glob, err)
	}
	return files, nil
}

// sdkServicePackages returns the deduped aws-sdk-go-v2 service package names a
// file imports. Subpackages collapse onto their parent: keyspaces and
// keyspaces/types are one client, and only the parent is a module with an
// endpoints table.
func sdkServicePackages(file *ast.File) []string {
	seen := map[string]bool{}
	var out []string
	for _, spec := range file.Imports {
		path, err := strconv.Unquote(spec.Path.Value)
		if err != nil {
			continue
		}
		rest, ok := strings.CutPrefix(path, sdkModulePrefix)
		if !ok {
			continue
		}
		pkg, _, _ := strings.Cut(rest, "/")
		if pkg != "" && !seen[pkg] {
			seen[pkg] = true
			out = append(out, pkg)
		}
	}
	slices.Sort(out)
	return out
}

// registeredServiceNames returns the service names a file registers, read from
// the `name:` field of each registerService(serviceEntry{...}) call.
func registeredServiceNames(file *ast.File) []string {
	var out []string
	ast.Inspect(file, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		if ident, ok := call.Fun.(*ast.Ident); !ok || ident.Name != "registerService" {
			return true
		}
		if len(call.Args) != 1 {
			return true
		}
		lit, ok := call.Args[0].(*ast.CompositeLit)
		if !ok {
			return true
		}
		if name, ok := stringField(lit, "name"); ok {
			out = append(out, name)
		}
		return true
	})
	return out
}

// findValueSpec returns the composite literal assigned to a package-level var.
func findValueSpec(file *ast.File, name string) *ast.CompositeLit {
	for _, decl := range file.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.VAR {
			continue
		}
		for _, spec := range gen.Specs {
			vs, ok := spec.(*ast.ValueSpec)
			if !ok || len(vs.Names) != 1 || vs.Names[0].Name != name || len(vs.Values) != 1 {
				continue
			}
			if lit, ok := vs.Values[0].(*ast.CompositeLit); ok {
				return lit
			}
		}
	}
	return nil
}

// fieldValue returns the value of a composite literal's named field, or nil.
func fieldValue(lit *ast.CompositeLit, field string) ast.Expr {
	for _, elt := range lit.Elts {
		kv, ok := elt.(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		if ident, ok := kv.Key.(*ast.Ident); ok && ident.Name == field {
			return kv.Value
		}
	}
	return nil
}

// stringField returns the unquoted value of a composite literal's named string
// field, reporting whether it was present and a plain string literal.
func stringField(lit *ast.CompositeLit, field string) (string, bool) {
	basic, ok := fieldValue(lit, field).(*ast.BasicLit)
	if !ok || basic.Kind != token.STRING {
		return "", false
	}
	s, err := strconv.Unquote(basic.Value)
	if err != nil {
		return "", false
	}
	return s, true
}

// moduleDirs maps every module path in root's build list to the directory the
// go command resolves it to, at the version pinned in root's go.mod. Modules
// the go command reports without a directory (not downloaded) are omitted.
func moduleDirs(root string) (map[string]string, error) {
	cmd := exec.Command("go", "list", "-m", "-f", "{{.Path}}\t{{.Dir}}", "all")
	cmd.Dir = root
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("regionsgen: go list -m all: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	dirs := map[string]string{}
	for line := range strings.Lines(string(out)) {
		path, dir, ok := strings.Cut(strings.TrimSpace(line), "\t")
		if ok && dir != "" {
			dirs[path] = dir
		}
	}
	return dirs, nil
}

// Render formats table as the source of the generated awsregions file.
func Render(table ServiceTable) ([]byte, error) {
	var buf bytes.Buffer
	buf.WriteString(`// Code generated by tools/genregions. DO NOT EDIT.

package awsregions

import "github.com/icearp/disco-cli/regions"

func init() { regions.RegisterServices("aws", ServiceRegions) }

// ServiceRegions maps each disco AWS service to the regions AWS offers it in,
// as declared by the region table embedded in that service's aws-sdk-go-v2
// package, in every partition where that table is regionalized
// (ServicePartitions). Filtered to Regions, so it names no FIPS pseudo-region.
//
// A service absent from this map has NO OPINION — it may be one whose scanner
// imports no SDK service package — and must be scanned rather than skipped.
// Treat as read-only.
`)
	writeServiceTable(&buf, "ServiceRegions", table.Regions)
	buf.WriteString(`
// ServicePartitions maps each service in ServiceRegions to the partitions whose
// regions its list speaks for: those where its SDK table has a regionalized
// entry. In any other partition the table has no opinion. Treat as read-only.
`)
	writeServiceTable(&buf, "ServicePartitions", table.Partitions)

	formatted, err := format.Source(buf.Bytes())
	if err != nil {
		return nil, fmt.Errorf("regionsgen: format generated source: %w", err)
	}
	return formatted, nil
}

// writeServiceTable writes "var name = map[string][]string{...}" with sorted keys.
func writeServiceTable(buf *bytes.Buffer, name string, table map[string][]string) {
	services := make([]string, 0, len(table))
	for service := range table {
		services = append(services, service)
	}
	sort.Strings(services)
	fmt.Fprintf(buf, "var %s = map[string][]string{\n", name)
	for _, service := range services {
		fmt.Fprintf(buf, "\t%q: {\n", service)
		for _, v := range table[service] {
			fmt.Fprintf(buf, "\t\t%q,\n", v)
		}
		buf.WriteString("\t},\n")
	}
	buf.WriteString("}\n")
}

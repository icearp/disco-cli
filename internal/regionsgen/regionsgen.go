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
// Build is separate from the tools/genregions command so the staleness guard can
// call it: a test rebuilds the table in memory and diffs it against the
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
	"os"
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

// awsPartitionID is the endpoints-table partition disco scans. GovCloud
// ("aws-us-gov"), China ("aws-cn") and the ISO partitions need separate
// credentials and are excluded from awsregions.Regions, so their region lists
// are dropped by the intersection below even if a table lists them.
const awsPartitionID = "aws"

// GeneratedFile is the basename tools/genregions writes and the staleness test
// points at.
const GeneratedFile = "services_generated.go"

// Build returns disco AWS service name → the regions AWS offers it in, derived
// from the SDK endpoint tables at the versions pinned in root's go.mod.
//
// root is the disco repository root. The result omits services whose scanner
// imports no SDK service package, or whose SDK tables list no region disco
// supports; an absent service means "no opinion", and callers must fail open.
//
// Build reports an error rather than an empty table when it parses no scanner
// files or resolves no SDK modules. A silently empty table would disable region
// scoping everywhere while looking perfectly healthy.
func Build(root string) (map[string][]string, error) {
	svcPkgs, err := scannerServicePackages(root)
	if err != nil {
		return nil, err
	}
	if len(svcPkgs) == 0 {
		return nil, errors.New("regionsgen: no registerService entries found; the scanner files or registration shape changed")
	}

	dirs, err := sdkModuleDirs(root)
	if err != nil {
		return nil, err
	}
	if len(dirs) == 0 {
		return nil, fmt.Errorf("regionsgen: no %s* modules resolved; run `go mod download` first", sdkModulePrefix)
	}

	// One parse per SDK package, shared across the services that import it.
	pkgRegions := make(map[string][]string, len(dirs))
	supported := supportedRegionSet()
	for pkg, dir := range dirs {
		regions, err := partitionRegions(filepath.Join(dir, "internal", "endpoints", "endpoints.go"), supported)
		if err != nil {
			return nil, err
		}
		if len(regions) > 0 {
			pkgRegions[pkg] = regions
		}
	}

	out := make(map[string][]string, len(svcPkgs))
	for service, pkgs := range svcPkgs {
		// Union, not intersection: a scanner importing several SDK packages
		// (chime pulls four chimesdk* clients, ses three) is reachable wherever
		// ANY of its clients is. Union over-scans; intersection would skip a
		// region one client genuinely serves.
		seen := map[string]bool{}
		var merged []string
		for _, pkg := range pkgs {
			for _, r := range pkgRegions[pkg] {
				if !seen[r] {
					seen[r] = true
					merged = append(merged, r)
				}
			}
		}
		if len(merged) > 0 {
			slices.Sort(merged)
			out[service] = merged
		}
	}
	return out, nil
}

// supportedRegionSet is the membership test the extracted regions are filtered
// through. Intersecting with disco's own supported list drops, in one rule, the
// "fips-*" pseudo-regions every table carries plus non-region endpoint keys such
// as "aws-global" and "s3-external-1". A region genuinely too new to appear in
// awsregions.Regions falls out too, which is the safe direction: the service
// then has no opinion for it and is scanned.
func supportedRegionSet() map[string]bool {
	set := make(map[string]bool, len(awsregions.Regions))
	for _, r := range awsregions.Regions {
		set[r] = true
	}
	return set
}

// scannerServicePackages maps each disco AWS service name to the SDK service
// packages its scanner file imports.
func scannerServicePackages(root string) (map[string][]string, error) {
	glob := filepath.Join(root, "internal", "providers", "aws", "*_scanners.go")
	files, err := filepath.Glob(glob)
	if err != nil {
		return nil, fmt.Errorf("regionsgen: glob %s: %w", glob, err)
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

// partitionRegions extracts the aws-partition regions an SDK endpoints file
// lists, keeping only those in supported. Regions repeat once per FIPS and
// dual-stack variant (ec2 lists us-east-1 four times, s3 seven), so the result
// is deduped.
func partitionRegions(path string, supported map[string]bool) ([]string, error) {
	src, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			// A service package without an embedded endpoints table simply
			// contributes no opinion.
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
	seen := map[string]bool{}
	var out []string
	for _, elt := range partitions.Elts {
		part, ok := elt.(*ast.CompositeLit)
		if !ok {
			continue
		}
		if id, ok := stringField(part, "ID"); !ok || id != awsPartitionID {
			continue
		}
		endpoints, ok := fieldValue(part, "Endpoints").(*ast.CompositeLit)
		if !ok {
			continue
		}
		for _, entry := range endpoints.Elts {
			kv, ok := entry.(*ast.KeyValueExpr)
			if !ok {
				continue
			}
			key, ok := kv.Key.(*ast.CompositeLit)
			if !ok {
				continue
			}
			region, ok := stringField(key, "Region")
			if !ok || !supported[region] || seen[region] {
				continue
			}
			seen[region] = true
			out = append(out, region)
		}
	}
	slices.Sort(out)
	return out, nil
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

// sdkModuleDirs maps each aws-sdk-go-v2 service package name to the module
// directory the go command resolves it to, at the version pinned in root's
// go.mod. Modules the go command reports without a directory (not downloaded)
// are omitted.
func sdkModuleDirs(root string) (map[string]string, error) {
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
		if !ok || dir == "" {
			continue
		}
		pkg, ok := strings.CutPrefix(path, sdkModulePrefix)
		if !ok || strings.Contains(pkg, "/") {
			continue
		}
		dirs[pkg] = dir
	}
	return dirs, nil
}

// Render formats table as the source of the generated awsregions file.
func Render(table map[string][]string) ([]byte, error) {
	services := make([]string, 0, len(table))
	for name := range table {
		services = append(services, name)
	}
	sort.Strings(services)

	var buf bytes.Buffer
	buf.WriteString(`// Code generated by tools/genregions. DO NOT EDIT.

package awsregions

import "github.com/icearp/disco-cli/regions"

func init() { regions.RegisterServices("aws", ServiceRegions) }

// ServiceRegions maps each disco AWS service to the regions AWS offers it in,
// as declared by the region table embedded in that service's aws-sdk-go-v2
// package. Filtered to Regions, so it names no FIPS pseudo-region and no
// partition disco does not scan.
//
// A service absent from this map has NO OPINION — it may be one whose scanner
// imports no SDK service package — and must be scanned rather than skipped.
// Treat as read-only.
var ServiceRegions = map[string][]string{
`)
	for _, name := range services {
		fmt.Fprintf(&buf, "\t%q: {\n", name)
		for _, region := range table[name] {
			fmt.Fprintf(&buf, "\t\t%q,\n", region)
		}
		buf.WriteString("\t},\n")
	}
	buf.WriteString("}\n")

	formatted, err := format.Source(buf.Bytes())
	if err != nil {
		return nil, fmt.Errorf("regionsgen: format generated source: %w", err)
	}
	return formatted, nil
}

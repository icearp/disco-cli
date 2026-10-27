// Command genregions regenerates the per-service region table that lets the AWS
// scanner skip (service × region) cells AWS does not offer, the partition and
// region tables copied from the SDK's partitions.json, and the per-partition
// home region and once-region of each SDK service package the scanners import.
//
// Run it through go generate:
//
//	go generate ./internal/providers/aws/awsregions/...
//
// or via `make gen-regions`. The output is committed; internal/regionsgen holds
// the logic and its tests fail when a committed file drifts.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/icearp/disco-cli/internal/regionsgen"
)

func main() {
	out := flag.String("out", regionsgen.GeneratedFile, "path of the generated service-region file to write")
	partitionsOut := flag.String("partitions-out", regionsgen.PartitionsFile, "path of the generated partition file to write")
	homesOut := flag.String("homes-out", regionsgen.HomesFile, "path of the generated SDK-package home file to write")
	flag.Parse()

	if err := run(*out, *partitionsOut, *homesOut); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(out, partitionsOut, homesOut string) error {
	root, err := moduleRoot()
	if err != nil {
		return err
	}
	src, err := regionsgen.Load(root)
	if err != nil {
		return err
	}

	table := src.ServiceRegions()
	tableSrc, err := regionsgen.Render(table)
	if err != nil {
		return err
	}
	if err := write(out, tableSrc); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "genregions: wrote %s (%d services)\n", out, len(table.Regions))

	partsSrc, err := regionsgen.RenderPartitions(src.Partitions)
	if err != nil {
		return err
	}
	if err := write(partitionsOut, partsSrc); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "genregions: wrote %s (%d partitions)\n", partitionsOut, len(src.Partitions))

	homes := src.Homes()
	homesSrc, err := regionsgen.RenderHomes(homes, src.OnceRegions(), src.S3RegionlessHosts())
	if err != nil {
		return err
	}
	if err := write(homesOut, homesSrc); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "genregions: wrote %s (%d SDK packages with a home)\n", homesOut, len(homes))
	return nil
}

func write(path string, src []byte) error {
	if err := os.WriteFile(path, src, 0o644); err != nil {
		return fmt.Errorf("genregions: write %s: %w", path, err)
	}
	return nil
}

// moduleRoot walks up from the working directory to the directory holding
// go.mod. go generate runs with the working directory set to the package that
// carries the directive, so the root cannot be assumed.
func moduleRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("genregions: working directory: %w", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("genregions: no go.mod above the working directory")
		}
		dir = parent
	}
}

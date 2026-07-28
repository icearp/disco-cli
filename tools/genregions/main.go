// Command genregions regenerates the per-service region table that lets the AWS
// scanner skip (service × region) cells AWS does not offer.
//
// Run it through go generate:
//
//	go generate ./internal/providers/aws/awsregions/...
//
// or via `make gen-regions`. The output is committed; internal/regionsgen holds
// the logic and its test fails when the committed file drifts.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/icearp/disco-cli/internal/regionsgen"
)

func main() {
	out := flag.String("out", regionsgen.GeneratedFile, "path of the generated file to write")
	flag.Parse()

	if err := run(*out); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(out string) error {
	root, err := moduleRoot()
	if err != nil {
		return err
	}
	table, err := regionsgen.Build(root)
	if err != nil {
		return err
	}
	src, err := regionsgen.Render(table)
	if err != nil {
		return err
	}
	if err := os.WriteFile(out, src, 0o644); err != nil {
		return fmt.Errorf("genregions: write %s: %w", out, err)
	}
	fmt.Fprintf(os.Stderr, "genregions: wrote %s (%d services)\n", out, len(table))
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

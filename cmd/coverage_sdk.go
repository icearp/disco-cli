package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/icearp/disco-cli/internal/sdkinv"
	"github.com/spf13/cobra"
)

var coverageSDKCmd = &cobra.Command{
	Use:   "sdk",
	Short: "Manage the local SDK source cache the coverage denominator is derived from",
	Long: `The coverage denominator is derived from each provider's own SDK sources and
catalogs rather than from a live cloud API:

  - AWS:    Smithy models inside aws-sdk-go-v2 (pinned release tag) and the
            credential-free AWS Service Reference catalog
  - Azure:  generated arm* client sources in azure-sdk-for-go (pinned commit)
  - GCP:    Discovery documents vendored in google.golang.org/api at the
            version this binary links

'fetch' downloads them once into the cache directory (default
$XDG_CACHE_HOME/disco/sdk), keeping only the files the extractors read.
'status' shows what is present. Pins live in each provider's
internal/providers/<p>/<p>inventory/pins.go; a pin
bump changes the denominator, so every coverage report prints the pins.`,
	Args: cobra.NoArgs,
	Run: func(c *cobra.Command, _ []string) {
		_ = c.Help()
	},
}

var coverageSDKFetchCmd = &cobra.Command{
	Use:   "fetch",
	Short: "Download the pinned SDK sources into the cache (no-op when present)",
	Example: `  disco coverage sdk fetch
  disco coverage sdk fetch --providers aws --force
  disco coverage sdk fetch --sdk-cache /tmp/sdk`,
	Args: cobra.NoArgs,
	RunE: runCoverageSDKFetch,
}

var coverageSDKStatusCmd = &cobra.Command{
	Use:     "status",
	Short:   "Show which provider snapshots are present in the cache",
	Example: `  disco coverage sdk status\n  disco coverage sdk status -o json`,
	Args:    cobra.NoArgs,
	RunE:    runCoverageSDKStatus,
}

func init() {
	coverageSDKCmd.PersistentFlags().String("sdk-cache", sdkinv.DefaultCacheRoot(), "SDK source cache directory")
	coverageSDKFetchCmd.Flags().StringSlice("providers", nil, fmt.Sprintf("Limit to listed providers (%s); empty = all", strings.Join(sdkinv.Names(), ", ")))
	coverageSDKFetchCmd.Flags().Bool("force", false, "Refetch even when the pinned snapshot is already present")
	coverageSDKStatusCmd.Flags().StringSlice("providers", nil, "Limit to listed providers; empty = all")
	coverageSDKCmd.AddCommand(coverageSDKFetchCmd, coverageSDKStatusCmd)
	coverageCmd.AddCommand(coverageSDKCmd)
}

func sdkCacheFromFlags(cmd *cobra.Command) sdkinv.Cache {
	root, _ := cmd.Flags().GetString("sdk-cache")
	return sdkinv.Cache{Root: root}
}

// sdkOutputFormats are what these two subcommands render; the parent's
// --output also offers markdown/csv/jsonl, which have no meaning for a cache
// listing and used to print the table and exit 0.
func checkSDKOutput(cmd *cobra.Command) error {
	switch outputFormat(cmd) {
	case "table", "json":
		return nil
	default:
		return fmt.Errorf("unknown --output format %q (supported: table, json)", outputFormat(cmd))
	}
}

// resolveExtractors maps --providers to registered extractors; empty = all.
// sdkinv.Get lower-cases and trims, so "AWS" and " aws " resolve here exactly
// as they do for `coverage services`.
func resolveExtractors(names []string) ([]sdkinv.Extractor, error) {
	if len(names) == 0 {
		names = sdkinv.Names()
	}
	out := make([]sdkinv.Extractor, 0, len(names))
	for _, n := range names {
		e, ok := sdkinv.Get(n)
		if !ok {
			return nil, fmt.Errorf("unknown provider %q (known: %s)", n, strings.Join(sdkinv.Names(), ", "))
		}
		out = append(out, e)
	}
	return out, nil
}

// sdkFetchRow is one provider's fetch outcome under -o json.
type sdkFetchRow struct {
	Provider string `json:"provider"`
	Ref      string `json:"ref"`
	State    string `json:"state"` // "fetched" | "present"
	Dir      string `json:"dir"`
}

func runCoverageSDKFetch(cmd *cobra.Command, _ []string) (rerr error) {
	defer func() { maybeStructuredError(outputFormat(cmd), rerr) }()
	if err := checkSDKOutput(cmd); err != nil {
		return err
	}
	names, _ := cmd.Flags().GetStringSlice("providers")
	force, _ := cmd.Flags().GetBool("force")
	exts, err := resolveExtractors(names)
	if err != nil {
		return err
	}
	cache := sdkCacheFromFlags(cmd)
	logf := func(format string, args ...any) { fmt.Fprintf(os.Stderr, format+"\n", args...) }
	rows := make([]sdkFetchRow, 0, len(exts))
	for _, e := range exts {
		dir, fetched, err := cache.Ensure(cmd.Context(), e, sdkinv.EnsureOptions{Force: force, Log: logf})
		if err != nil {
			return err
		}
		state := "present"
		if fetched {
			state = "fetched"
		}
		rows = append(rows, sdkFetchRow{Provider: e.Name(), Ref: e.Ref(), State: state, Dir: dir})
	}
	if outputFormat(cmd) == "json" {
		enc := json.NewEncoder(cmd.OutOrStdout())
		enc.SetIndent("", "  ")
		return enc.Encode(rows)
	}
	for _, r := range rows {
		fmt.Fprintf(cmd.OutOrStdout(), "%s@%s\t%s\t%s\n", r.Provider, r.Ref, r.State, r.Dir)
	}
	return nil
}

type sdkStatusRow struct {
	Provider string                `json:"provider"`
	Ref      string                `json:"ref"`
	Present  bool                  `json:"present"`
	Dir      string                `json:"dir"`
	Manifest *sdkinv.Manifest      `json:"manifest,omitempty"`
	Sources  []sdkinv.SourceRecord `json:"-"`
	// PinNotes name the sources whose fetched content disagrees with the
	// digest its extractor pins.
	PinNotes []string `json:"pinNotes,omitempty"`
	// Notes explain a snapshot directory that exists but does not count as
	// fetched (wrong provider/ref, or a different source spec).
	Notes []string `json:"notes,omitempty"`
}

// pinNotes compares each pinned-by-digest source against what the snapshot
// actually holds. An unversioned catalog cannot be fetched at a ref, so the
// disagreement is reported here and in the report's pins rather than hidden.
func pinNotes(e sdkinv.Extractor, m *sdkinv.Manifest) []string {
	if m == nil {
		return nil
	}
	var out []string
	for _, src := range e.FetchSpec() {
		if src.PinnedDigest == "" {
			continue
		}
		for _, rec := range m.Sources {
			if rec.Name != src.Name || rec.SHA256 == "" {
				continue
			}
			if got := rec.SHA256[:min(len(rec.SHA256), len(src.PinnedDigest))]; got != src.PinnedDigest {
				out = append(out, fmt.Sprintf("%s: cached %s, pinned %s (bump the %s extractor's pin with the regenerated baseline)", src.Name, got, src.PinnedDigest, e.Name()))
			}
		}
	}
	return out
}

func runCoverageSDKStatus(cmd *cobra.Command, _ []string) (rerr error) {
	defer func() { maybeStructuredError(outputFormat(cmd), rerr) }()
	if err := checkSDKOutput(cmd); err != nil {
		return err
	}
	names, _ := cmd.Flags().GetStringSlice("providers")
	exts, err := resolveExtractors(names)
	if err != nil {
		return err
	}
	cache := sdkCacheFromFlags(cmd)
	rows := make([]sdkStatusRow, 0, len(exts))
	for _, e := range exts {
		row := sdkStatusRow{Provider: e.Name(), Ref: e.Ref(), Dir: cache.Dir(e.Name(), e.Ref())}
		m, serr := cache.Status(e)
		switch {
		case serr == nil:
			row.Present, row.Manifest = true, m
			row.PinNotes = pinNotes(e, m)
		case !errors.Is(serr, sdkinv.ErrNotFetched):
			return serr
		default:
			// A snapshot rejected for a stale source spec looks exactly like
			// one that was never fetched; the reason is only in the error.
			if _, err := os.Stat(row.Dir); err == nil {
				row.Notes = append(row.Notes, serr.Error())
			}
		}
		rows = append(rows, row)
	}
	w := cmd.OutOrStdout()
	if outputFormat(cmd) == "json" {
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		return enc.Encode(rows)
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "PROVIDER\tREF\tSTATE\tFETCHED\tSOURCES\tDIR")
	for _, r := range rows {
		state, fetched, sources := "absent", "-", "-"
		if r.Present {
			state = "present"
			fetched = r.Manifest.FetchedAt.Format("2006-01-02T15:04Z")
			parts := make([]string, 0, len(r.Manifest.Sources))
			for _, s := range r.Manifest.Sources {
				parts = append(parts, fmt.Sprintf("%s=%d files", s.Name, s.Files))
			}
			sources = strings.Join(parts, ", ")
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n", r.Provider, r.Ref, state, fetched, sources, r.Dir)
	}
	if err := tw.Flush(); err != nil {
		return err
	}
	for _, r := range rows {
		for _, note := range append(append([]string{}, r.Notes...), r.PinNotes...) {
			fmt.Fprintf(os.Stderr, "  %s: %s\n", r.Provider, note)
		}
	}
	return nil
}

package cmd

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"os"
	"text/tabwriter"

	"codeberg.org/icearp/disco/store"
	"github.com/spf13/cobra"
)

var historyOutputFmt string

// historyColumns is the canonical column order for CSV/markdown output.
var historyColumns = []string{
	"version", "id", "type", "name", "discovered_at", "verified_at", "verified_by", "current", "attributes",
}

// historyEntry is the timeline shape for one version of a resource. It is a
// purpose-built view (not store.ResourceVersion) because store.Resource carries
// a value-receiver MarshalJSON that would be promoted onto the embedding struct
// and silently drop the version fields in JSON output.
type historyEntry struct {
	Version      int             `json:"version"`
	ID           string          `json:"id"`
	Type         string          `json:"type"`
	Name         *string         `json:"name"`
	DiscoveredAt string          `json:"discoveredAt"`
	VerifiedAt   *string         `json:"verifiedAt"`
	VerifiedBy   *string         `json:"verifiedBy"`
	Current      bool            `json:"current"`
	Attributes   json.RawMessage `json:"attributes"`
}

func historyEntries(versions []store.ResourceVersion) []historyEntry {
	out := make([]historyEntry, 0, len(versions))
	for i, v := range versions {
		out = append(out, historyEntry{
			// Chain order is root-first; GetResourceVersions tiebreaks on the
			// UUIDv7 row id (discovered_at is inherited identically across a
			// chain, so it carries no ordering signal). root = 1, current = len.
			Version:      i + 1,
			ID:           v.RootID,
			Type:         v.Type,
			Name:         v.Name,
			DiscoveredAt: v.DiscoveredAt,
			VerifiedAt:   v.VerifiedAt,
			VerifiedBy:   v.VerifiedBy,
			Current:      v.SupersededBy == nil,
			Attributes:   rawJSONObject(v.AttributesJSON),
		})
	}
	return out
}

// rawJSONObject returns s as raw JSON, falling back to an empty object so output
// never carries a null or malformed attributes field (mirrors the list dialect).
func rawJSONObject(s string) json.RawMessage {
	if s == "" || !json.Valid([]byte(s)) {
		return json.RawMessage("{}")
	}
	return json.RawMessage(s)
}

func historyRow(e historyEntry) []string {
	name := ""
	if e.Name != nil {
		name = *e.Name
	}
	return []string{
		fmt.Sprintf("%d", e.Version), e.ID, e.Type, name,
		e.DiscoveredAt, derefOr(e.VerifiedAt, ""), derefOr(e.VerifiedBy, ""),
		fmt.Sprintf("%t", e.Current), string(e.Attributes),
	}
}

func derefOr(p *string, dflt string) string {
	if p == nil {
		return dflt
	}
	return *p
}

var historyCmd = &cobra.Command{
	Use:   "history <name|native-id|resource-id>",
	Short: "Show a resource's version history (changes over time)",
	Args:  cobra.ExactArgs(1),
	Long: `Show every recorded version of a resource, oldest to newest.

disco keeps a version chain per resource: re-scanning supersedes the previous
row only when the resource's attributes change, so this is the change-over-time
view. Accepts a resource id, name, native id, or short-id prefix (same lookup as
'disco graph').`,
	Example: `  disco history 1f3c0a9b2e4d5c6a7b8c9d0e1f2a3b4c
  disco resources --type azure:microsoft.quota:quotas -o json | jq -r '.[0].id' | xargs disco history`,
	RunE: func(_ *cobra.Command, args []string) (rerr error) {
		defer func() { maybeStructuredError(historyOutputFmt, rerr) }()
		db, err := openDB()
		if err != nil {
			return fmt.Errorf("open database: %w", err)
		}
		defer func() { _ = db.Close() }()

		// Resolve the arg the same way graph / list --id do (exact id, name,
		// native_id, or short-id prefix) so history accepts whatever the user
		// pasted; the resolved current row's .ID is the root_id keying the chain.
		target, err := db.ResolveResource(args[0], "", "", "")
		if err != nil {
			return err
		}
		versions, err := db.GetResourceVersions(target.ID)
		if err != nil {
			return fmt.Errorf("get resource versions: %w", err)
		}
		// Zero rows is a valid query outcome, not a process failure: exit 0
		// and emit the format's empty shape (machine formats get []/no lines;
		// table prints a stderr note below) — same contract as resources/scans.
		entries := historyEntries(versions)

		switch historyOutputFmt {
		case "json":
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			return enc.Encode(entries)
		case "jsonl":
			enc := json.NewEncoder(os.Stdout)
			for _, e := range entries {
				if err := enc.Encode(e); err != nil {
					return err
				}
			}
			return nil
		case "csv":
			w := csv.NewWriter(os.Stdout)
			defer w.Flush()
			if err := w.Write(historyColumns); err != nil {
				return err
			}
			for _, e := range entries {
				if err := w.Write(historyRow(e)); err != nil {
					return err
				}
			}
			return nil
		case "markdown", "md":
			rows := make([][]string, 0, len(entries))
			for _, e := range entries {
				rows = append(rows, historyRow(e))
			}
			return renderMarkdownTable(os.Stdout, historyColumns, rows)
		case "table", "":
			if len(entries) == 0 {
				_, _ = fmt.Fprintln(os.Stderr, "No version history found.")
				return nil
			}
			w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
			_, _ = fmt.Fprintln(w, "VERSION\tDISCOVERED AT\tVERIFIED AT\tVERIFIED BY\tCURRENT\tATTRIBUTES")
			for _, e := range entries {
				_, _ = fmt.Fprintf(w, "%d\t%s\t%s\t%s\t%t\t%s\n",
					e.Version, e.DiscoveredAt, ptrOrDash(e.VerifiedAt),
					short(ptrOrDash(e.VerifiedBy)), e.Current, string(e.Attributes))
			}
			return w.Flush()
		default:
			return fmt.Errorf("unknown --output format %q (supported: table, markdown, csv, json, jsonl)", historyOutputFmt)
		}
	},
}

func init() {
	historyCmd.Flags().StringVarP(&historyOutputFmt, "output", "o", "table", "Output format: table, markdown, csv, json, jsonl")
	_ = historyCmd.RegisterFlagCompletionFunc("output", staticCompletion("table", "markdown", "csv", "json", "jsonl"))
	rootCmd.AddCommand(historyCmd)
}

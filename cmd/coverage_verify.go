package cmd

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/icearp/disco-cli/internal/coverage"
	"github.com/icearp/disco-cli/internal/sdkinv"
	"github.com/icearp/disco-cli/store"
)

// errCoverageUndeclared is returned after rendering when a scan stored rows of
// a type no scanner declares; root maps it to exit 1 without a second print.
var errCoverageUndeclared = errors.New("stored types missing from declared emits")

// Verify statuses.
const (
	verifyEmittedUndeclared  = "emitted-undeclared"   // rows in the DB for a type no scanner declares: a bug
	verifyDeclaredNotEmitted = "declared-not-emitted" // a declared type the scan stored no rows for
)

var coverageVerifyCmd = &cobra.Command{
	Use:   "verify",
	Short: "Check a scan's stored resource types against the declared scanner emits",
	Long: `Compares the resource types one scan actually stored (rows it discovered or
re-verified) with the types the scanners declare they emit.

  emitted-undeclared    the DB holds rows of a type no scanner declares — a
                        registration bug; the command exits 1.
  declared-not-emitted  a declared type the scan stored no rows for, with a
                        derived reason: the provider was out of the scan's
                        scope, the service failed (scans.errors), an
                        operation was skipped (scans.warnings, joined to the
                        scanner pairing when the SDK cache is present), or
                        simply "no rows" — the account has none.

--scan-id names the scan (default: the latest completed or partial scan).
--providers defaults to the providers the scan ran; naming one the scan
skipped lists every type of it as out-of-scope. --db selects the database as
for every other read command.

Unrelated to 'disco verify', which checks a snapshot archive's integrity.`,
	Example: `  disco coverage verify
  disco coverage verify --scan-id 0123abcd --providers aws -o json
  disco coverage verify --db ./disco.db --sdk-cache ~/.cache/disco/sdk`,
	Args: cobra.NoArgs,
	RunE: runCoverageVerify,
}

func init() {
	coverageVerifyCmd.Flags().String("scan-id", "latest", "Scan to verify: a scan id, or 'latest'")
	coverageVerifyCmd.Flags().StringSlice("providers", nil, fmt.Sprintf("Limit to listed providers (%s); empty = all registered", providerListHint()))
	coverageVerifyCmd.Flags().String("sdk-cache", sdkinv.DefaultCacheRoot(), "SDK source cache used to join scan warnings to the types their operation lists")
	coverageVerifyCmd.Flags().String("source-root", defaultSourceRoot(), "disco source checkout for scanner pairing; empty = join warnings by service only")
	coverageCmd.AddCommand(coverageVerifyCmd)
}

// verifyRow is one mismatch between a scan and the declared emits.
type verifyRow struct {
	Provider  string `json:"provider"`
	DiscoType string `json:"discoType"`
	Service   string `json:"service"`
	Status    string `json:"status"`
	Reason    string `json:"reason,omitempty"`
}

func runCoverageVerify(cmd *cobra.Command, _ []string) (rerr error) {
	outputFmt := outputFormat(cmd)
	defer func() {
		// The rows are the payload and the exit code the gate: no error
		// envelope after a rendered report (precedent: check's findings).
		if !errors.Is(rerr, errCoverageUndeclared) {
			maybeStructuredError(outputFmt, rerr)
		}
	}()
	switch outputFmt {
	case "table", "markdown", "md", "json", "jsonl", "csv":
	default:
		return fmt.Errorf("unknown --output format %q (supported: table, markdown, csv, json, jsonl)", outputFmt)
	}
	provNames, _ := cmd.Flags().GetStringSlice("providers")
	scanID, _ := cmd.Flags().GetString("scan-id")
	var o servicesOptions
	root, _ := cmd.Flags().GetString("sdk-cache")
	o.cache = sdkinv.Cache{Root: root}
	o.sourceRoot, _ = cmd.Flags().GetString("source-root")

	provs, err := selectedProviders(provNames)
	if err != nil {
		return err
	}
	db, err := openDB()
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()
	sc, err := resolveScan(db, scanID)
	if err != nil {
		return err
	}
	stored, err := db.TypesForScan(sc.ID)
	if err != nil {
		return err
	}
	ctx := scanContext{scan: sc}
	if err := ctx.load(); err != nil {
		return err
	}
	if len(provNames) == 0 && len(ctx.providers) > 0 {
		provs = ctx.inScope(provs)
	}
	var rows []verifyRow
	undeclared := 0
	for _, p := range provs {
		labels := buildLabelIndex(cmd.Context(), o, p)
		declared := map[string]coverage.TypeDecl{}
		for _, d := range p.Emits() {
			declared[d.DiscoType] = d
		}
		emitted := map[string]bool{}
		for _, pt := range stored {
			if pt.Provider != p.Name() {
				continue
			}
			emitted[pt.Type] = true
			if _, ok := declared[pt.Type]; !ok {
				undeclared++
				rows = append(rows, verifyRow{Provider: p.Name(), DiscoType: pt.Type, Service: discoServiceSegment(pt.Type), Status: verifyEmittedUndeclared})
			}
		}
		for typ, d := range declared {
			if emitted[typ] {
				continue
			}
			rows = append(rows, verifyRow{Provider: p.Name(), DiscoType: typ, Service: d.Service, Status: verifyDeclaredNotEmitted, Reason: ctx.reason(p.Name(), d, labels)})
		}
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Status != rows[j].Status {
			return rows[i].Status == verifyEmittedUndeclared
		}
		if rows[i].Provider != rows[j].Provider {
			return rows[i].Provider < rows[j].Provider
		}
		return rows[i].DiscoType < rows[j].DiscoType
	})
	if err := renderVerifyRows(cmd.OutOrStdout(), outputFmt, rows); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "\nscan %s: %d types stored, %d undeclared, %d declared types without rows\n", sc.ID, len(stored), undeclared, len(rows)-undeclared)
	if undeclared > 0 {
		return fmt.Errorf("%w: %d type(s); register them with registerType", errCoverageUndeclared, undeclared)
	}
	return nil
}

// selectedProviders resolves --providers against the coverage registry.
func selectedProviders(names []string) ([]coverage.Provider, error) {
	if len(names) == 0 {
		provs := coverage.All()
		if len(provs) == 0 {
			return nil, errors.New("no provider in this build supports coverage")
		}
		return provs, nil
	}
	var out []coverage.Provider
	for _, n := range names {
		p, ok := coverage.Get(n)
		if !ok {
			return nil, fmt.Errorf("provider %q has no coverage support; registered: %v", n, coverage.Names())
		}
		out = append(out, p)
	}
	return out, nil
}

// resolveScan maps --scan-id to a scan record; "latest" is the newest
// completed or partial scan, never a running one whose rows are still landing.
func resolveScan(db *store.Store, id string) (*store.Scan, error) {
	if id == "" || id == "latest" {
		sc, err := db.LatestCompleteScan("")
		if err != nil {
			return nil, fmt.Errorf("no completed scan to verify (run `disco scan <provider>` first): %w", err)
		}
		return sc, nil
	}
	sc, err := db.GetScan(id)
	if err != nil {
		return nil, fmt.Errorf("scan %q: %w", id, err)
	}
	return sc, nil
}

// scanContext is what a scan record says about why a type may be absent.
type scanContext struct {
	scan      *store.Scan
	providers map[string]bool // scope.providers
	errors    []store.ScanErrorEntry
	warnings  []store.ScanWarningEntry
}

// inScope keeps the providers the scan actually ran. Naming a provider with
// --providers still reports it (every type out-of-scope), the default does
// not: those rows would outnumber the real ones twenty to one.
func (c *scanContext) inScope(provs []coverage.Provider) []coverage.Provider {
	var out []coverage.Provider
	for _, p := range provs {
		if c.providers[p.Name()] {
			out = append(out, p)
		}
	}
	return out
}

func (c *scanContext) load() error {
	var scope struct {
		Providers []string `json:"providers"`
	}
	if c.scan.ScopeJSON != "" {
		if err := json.Unmarshal([]byte(c.scan.ScopeJSON), &scope); err != nil {
			return fmt.Errorf("scan %s scope: %w", c.scan.ID, err)
		}
	}
	c.providers = lowerSet(scope.Providers)
	if c.scan.ErrorsJSON != nil && *c.scan.ErrorsJSON != "" {
		if err := json.Unmarshal([]byte(*c.scan.ErrorsJSON), &c.errors); err != nil {
			return fmt.Errorf("scan %s errors: %w", c.scan.ID, err)
		}
	}
	if c.scan.WarningsJSON != nil && *c.scan.WarningsJSON != "" {
		if err := json.Unmarshal([]byte(*c.scan.WarningsJSON), &c.warnings); err != nil {
			return fmt.Errorf("scan %s warnings: %w", c.scan.ID, err)
		}
	}
	return nil
}

// reason explains one declared type without rows. Entries are recorded as
// "<provider>:<service>" (errors) and "<provider>:<op label>" (warnings).
func (c *scanContext) reason(provider string, d coverage.TypeDecl, labels labelIndex) string {
	if len(c.providers) > 0 && !c.providers[provider] {
		return "out-of-scope: provider not scanned"
	}
	svc := strings.ToLower(d.Service)
	for _, e := range c.errors {
		if strings.EqualFold(stripProvider(e.Service, provider), svc) {
			return "scan-error: " + e.Code + regionSuffix(e.Region)
		}
	}
	for _, w := range c.warnings {
		label := stripProvider(w.Service, provider)
		if labels.explains(label, d.DiscoType, svc) {
			return "warning: " + label + regionSuffix(w.Region) + ": " + truncate(w.Message, 80)
		}
	}
	return "no rows"
}

// stripProvider drops the "<provider>:" prefix the scan runner adds when it
// persists entries; ScanError.Service already carried one for some scanners,
// so the prefix can appear twice.
func stripProvider(s, provider string) string {
	for strings.HasPrefix(strings.ToLower(s), provider+":") {
		s = s[len(provider)+1:]
	}
	return s
}

func regionSuffix(region string) string {
	if region == "" {
		return ""
	}
	return " (" + region + ")"
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// labelIndex is the scanner pairing seen from the warning side: which types
// each op label stores. A label the pairing knows but that stores none of a
// type's rows must not explain that type; an unknown label (pairing absent,
// or a spelling the walk never saw) falls back to its service prefix.
type labelIndex struct {
	byType map[string]map[string]bool
	known  map[string]bool
}

func (ix labelIndex) explains(label, typ, service string) bool {
	if ix.byType[typ][label] {
		return true
	}
	if ix.known[label] {
		return false
	}
	prefix, _, _ := strings.Cut(label, ":")
	return strings.EqualFold(prefix, service)
}

// buildLabelIndex derives the index from the SDK cache and scanner source;
// an unavailable inventory yields an empty index and one stderr note.
func buildLabelIndex(ctx context.Context, o servicesOptions, p coverage.Provider) labelIndex {
	ix := labelIndex{byType: map[string]map[string]bool{}, known: map[string]bool{}}
	in, err := inventoryInputs(ctx, o, p)
	if err != nil {
		fmt.Fprintf(os.Stderr, "  %s: warnings join by service only (%v)\n", p.Name(), err)
		return ix
	}
	for _, pr := range in.Pairings {
		labels := append([]string{pr.Label}, pr.Labels...)
		for _, l := range labels {
			ix.known[l] = true
			for _, t := range pr.Types {
				if ix.byType[t] == nil {
					ix.byType[t] = map[string]bool{}
				}
				ix.byType[t][l] = true
			}
		}
	}
	return ix
}

func renderVerifyRows(w io.Writer, outputFmt string, rows []verifyRow) error {
	switch outputFmt {
	case "json":
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		return enc.Encode(rows)
	case "jsonl":
		enc := json.NewEncoder(w)
		for _, r := range rows {
			if err := enc.Encode(r); err != nil {
				return err
			}
		}
	case "csv":
		cw := csv.NewWriter(w)
		_ = cw.Write([]string{"provider", "disco_type", "service", "status", "reason"})
		for _, r := range rows {
			_ = cw.Write([]string{r.Provider, r.DiscoType, r.Service, r.Status, r.Reason})
		}
		cw.Flush()
		return cw.Error()
	case "markdown", "md":
		mdRows := make([][]string, 0, len(rows))
		for _, r := range rows {
			mdRows = append(mdRows, []string{r.Provider, r.DiscoType, r.Service, r.Status, r.Reason})
		}
		return renderMarkdownTable(w, []string{"Provider", "Disco Type", "Service", "Status", "Reason"}, mdRows)
	default:
		tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
		_, _ = fmt.Fprintln(tw, "PROVIDER\tDISCO_TYPE\tSERVICE\tSTATUS\tREASON")
		for _, r := range rows {
			_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", r.Provider, r.DiscoType, r.Service, r.Status, r.Reason)
		}
		return tw.Flush()
	}
	return nil
}

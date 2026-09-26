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
	ctx := scanContext{scan: sc, stored: map[string]bool{}}
	for _, pt := range stored {
		ctx.stored[pt.Provider] = true
	}
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
		services := typeServiceNames(p)
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
			rows = append(rows, verifyRow{Provider: p.Name(), DiscoType: typ, Service: d.Service, Status: verifyDeclaredNotEmitted, Reason: ctx.reason(p.Name(), d, services[typ], labels)})
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
	// Every other scan-id-taking command accepts the 8-char prefix `disco
	// scans` prints; this one used to accept only the 32-hex form the CLI
	// never shows. `latest` stays LatestCompleteScan, which resolveScanID
	// does not implement (it would pick a running scan).
	if isScanIDPrefix(id) {
		full, err := resolveScanIDPrefix(db, id)
		if err != nil {
			return nil, err
		}
		id = full
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
	stored    map[string]bool // providers with at least one row in this scan
	providers map[string]bool // scope.providers
	// services and regions are scope.<provider>.services / .regions, absent
	// when the scan named none ("all"). A --services-filtered scan otherwise
	// reads exactly like an empty account.
	services map[string]map[string]bool
	regions  map[string]map[string]bool
	errors   []store.ScanErrorEntry
	warnings []store.ScanWarningEntry
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
	c.services, c.regions = map[string]map[string]bool{}, map[string]map[string]bool{}
	if c.scan.ScopeJSON != "" {
		if err := json.Unmarshal([]byte(c.scan.ScopeJSON), &scope); err != nil {
			return fmt.Errorf("scan %s scope: %w", c.scan.ID, err)
		}
		// The scope object mixes shapes — a "providers" list beside one block
		// per provider, whose own values are polymorphic (a list, the string
		// "all", a bool) — so each key decodes on its own and loosely: an
		// unreadable block must narrow nothing rather than fail the command.
		var raw map[string]json.RawMessage
		if err := json.Unmarshal([]byte(c.scan.ScopeJSON), &raw); err == nil {
			for prov, blob := range raw {
				var block struct {
					Services any `json:"services"`
					Regions  any `json:"regions"`
				}
				if err := json.Unmarshal(blob, &block); err != nil {
					continue
				}
				if s := scopeList(block.Services, prov); s != nil {
					c.services[strings.ToLower(prov)] = s
				}
				if r := scopeList(block.Regions, ""); r != nil {
					c.regions[strings.ToLower(prov)] = r
				}
			}
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

// scopeList reads a scope value that names a selection. A JSON array narrows;
// the literal "all" (and anything else, including a bool or a missing key)
// does not, and returns nil so the caller records no narrowing at all. The
// provider prefix is dropped because --services is written "aws:ec2" while
// every service name this file joins on is bare.
func scopeList(v any, provider string) map[string]bool {
	items, ok := v.([]any)
	if !ok {
		return nil
	}
	out := map[string]bool{}
	for _, it := range items {
		s, ok := it.(string)
		if !ok || s == "" {
			continue
		}
		if provider != "" {
			s = stripProvider(s, strings.ToLower(provider))
		}
		out[strings.ToLower(s)] = true
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// reason explains one declared type without rows. Entries are recorded as
// "<provider>:<service>" (errors) and "<provider>:<op label>" (warnings).
func (c *scanContext) reason(provider string, d coverage.TypeDecl, services map[string]bool, labels labelIndex) string {
	if len(c.providers) > 0 && !c.providers[provider] {
		return "out-of-scope: provider not scanned"
	}
	if sel := c.services[provider]; len(sel) > 0 && !anyOf(sel, services) {
		return "out-of-scope: service not in --services"
	}
	if r := c.errorReason(provider, services); r != "" {
		return r
	}
	for _, w := range c.warnings {
		label := stripProvider(w.Service, provider)
		if labels.explains(w, label, d.DiscoType, services, provider) {
			return "warning: " + label + regionSuffix(w.Region) + ": " + truncate(w.Message, 80)
		}
	}
	// "no rows" claims the account has none, which a provider that stored
	// nothing at all has not established.
	if !c.stored[provider] && len(c.errors) == 0 {
		return "nothing stored: " + provider + " recorded no rows and no failure"
	}
	if sel := c.regions[provider]; len(sel) > 0 {
		// A claim about the scan, not about the type: nothing here knows
		// whether the type is regional, and a global service listed under a
		// --regions run is not absent "in those regions".
		return "no rows (scan limited to regions: " + strings.Join(sortedKeysOf(sel), ", ") + ")"
	}
	return "no rows"
}

// errorReason matches the scan's recorded failures against one type, exact
// matches first. Both tests used to run in one pass over the list, so the
// first provider-prefixed entry short-circuited every later exact match.
func (c *scanContext) errorReason(provider string, services map[string]bool) string {
	for _, e := range c.errors {
		svc := strings.ToLower(stripProvider(e.Service, provider))
		if svc == wholeScanService || svc == interruptedService || services[svc] {
			return scanErrorReason(e, svc)
		}
	}
	// A provider that stored nothing and failed before any service ran
	// (aws:load-accounts, gcp:load-projects, one unreachable subscription
	// when it was the only one) lost every type to that failure, whatever
	// label the scanner gave it.
	if !c.stored[provider] {
		for _, e := range c.errors {
			if strings.HasPrefix(strings.ToLower(e.Service), provider+":") {
				return scanErrorReason(e, strings.ToLower(stripProvider(e.Service, provider)))
			}
		}
	}
	return ""
}

// scanErrorReason renders one error entry the way the warning branch renders a
// warning: the code alone is right in kind and useless in content, because the
// runner writes the literal "Error" whenever it cannot read a code.
func scanErrorReason(e store.ScanErrorEntry, svc string) string {
	out := "scan-error: " + e.Code
	// Neither whole-scan label names a service, so neither belongs in the
	// service slot; "Canceled (scan:interrupted)" reads as a service called
	// scan:interrupted.
	if svc != "" && svc != wholeScanService && svc != interruptedService {
		out += " (" + svc + ")"
	}
	switch {
	case e.Region != "":
		out += regionSuffix(e.Region)
	case e.Scope != "":
		out += " [" + e.Scope + "]"
	}
	if e.Message != "" {
		// Wider than the warning budget: a provider error message opens with
		// SDK boilerplate ("operation error STS: GetCallerIdentity, …") and
		// the part worth reading is behind it.
		out += ": " + truncate(e.Message, 120)
	}
	return out
}

// anyOf reports whether any of the type's service names was selected.
func anyOf(selected, names map[string]bool) bool {
	for n := range names {
		if selected[n] {
			return true
		}
	}
	return false
}

func sortedKeysOf(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// wholeScanService is the service the runner records when a provider's Scan
// itself fails (internal/scanrun): nothing of that provider was listed.
const wholeScanService = "scan"

// interruptedService is the entry Finalize records on Ctrl-C. It carries no
// provider prefix, so it explains every provider of the scan — and it is the
// default --scan-id latest target right after an interrupt, because
// LatestCompleteScan accepts a partial scan.
const interruptedService = "scan:interrupted"

// typeServiceNames is every name a scan-record entry may carry for a type:
// the scanner services registered from the type's own file
// (coverage.ServiceMapper), the API service it declares and its type
// segment. Scanner names ("aws:sso-admin") and declared services ("sso")
// differ for dozens of services; no single key joins them all.
func typeServiceNames(p coverage.Provider) map[string]map[string]bool {
	out := map[string]map[string]bool{}
	add := func(typ, name string) {
		if out[typ] == nil {
			out[typ] = map[string]bool{}
		}
		out[typ][strings.ToLower(stripProvider(name, p.Name()))] = true
	}
	for _, d := range p.Emits() {
		add(d.DiscoType, d.Service)
		add(d.DiscoType, discoServiceSegment(d.DiscoType))
	}
	if m, ok := p.(coverage.ServiceMapper); ok {
		for typ, names := range m.TypeServices() {
			for _, n := range names {
				add(typ, n)
			}
		}
	}
	return out
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
// each op label stores. With the pairing present every op label a scanner
// can persist is known (the pairing tests fail on an unresolved label), so
// a label it does not know is no op at all — a store-level warning such as
// a native-id collision, which explains nothing unless the scan recorded the
// service it came from. Without the pairing a warning joins by that service,
// else by its label prefix.
type labelIndex struct {
	byType map[string]map[string]bool
	known  map[string]bool
}

func (ix labelIndex) explains(w store.ScanWarningEntry, label, typ string, services map[string]bool, provider string) bool {
	if ix.byType[typ][label] {
		return true
	}
	// A label the pairing does not know may still be a warning the scan
	// tagged with the service it came from. That tag is the only join for an
	// Azure "arm<module>:<Client>.<Method>" label, whose prefix is a module
	// name no rule turns into "microsoft.<ns>", and for the scanners that
	// warn under a service name rather than an op label.
	if !ix.known[label] {
		for _, n := range strings.Split(w.ServiceName, ",") {
			n = strings.ToLower(stripProvider(strings.TrimSpace(n), provider))
			if n != "" && services[n] {
				return true
			}
		}
	}
	if len(ix.known) > 0 {
		return false
	}
	prefix, _, _ := strings.Cut(label, ":")
	return services[strings.ToLower(prefix)]
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

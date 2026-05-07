package cmd

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"

	"codeberg.org/icearp/disco/internal/policy"
)

// SARIF v2.1.0 — minimal subset sufficient for GitHub / GitLab code-scanning
// ingest. Only fields disco actually populates are modelled; omitempty keeps
// the doc small. Spec: https://docs.oasis-open.org/sarif/sarif/v2.1.0/sarif-v2.1.0.html

type sarifLog struct {
	Schema  string     `json:"$schema"`
	Version string     `json:"version"`
	Runs    []sarifRun `json:"runs"`
}

type sarifRun struct {
	Tool       sarifTool       `json:"tool"`
	Taxonomies []sarifTaxonomy `json:"taxonomies,omitempty"`
	Results    []sarifResult   `json:"results"`
}

// sarifTaxonomy / sarifTaxon model the SARIF 2.1.0 taxonomies block —
// the canonical place to surface framework mappings (WAF pillar, SOC 2,
// ISO 27001, PCI-DSS, NIST 800-53). Each unique tag value present on
// loaded rules becomes one taxa under the matching taxonomy. Code
// scanning UIs and audit pivots key off these.
type sarifTaxonomy struct {
	Name             string         `json:"name"`
	ShortDescription *sarifMessage  `json:"shortDescription,omitempty"`
	Taxa             []sarifTaxon   `json:"taxa"`
	Properties       map[string]any `json:"properties,omitempty"`
}

type sarifTaxon struct {
	ID               string        `json:"id"`
	ShortDescription *sarifMessage `json:"shortDescription,omitempty"`
}

type sarifTool struct {
	Driver sarifDriver `json:"driver"`
}

type sarifDriver struct {
	Name           string           `json:"name"`
	InformationURI string           `json:"informationUri,omitempty"`
	Version        string           `json:"version,omitempty"`
	Rules          []sarifRuleDescr `json:"rules"`
}

type sarifRuleDescr struct {
	ID                   string                     `json:"id"`
	Name                 string                     `json:"name,omitempty"`
	ShortDescription     *sarifMessage              `json:"shortDescription,omitempty"`
	FullDescription      *sarifMessage              `json:"fullDescription,omitempty"`
	DefaultConfiguration *sarifDefaultConfiguration `json:"defaultConfiguration,omitempty"`
	HelpURI              string                     `json:"helpUri,omitempty"`
	Help                 *sarifMessage              `json:"help,omitempty"`
	Properties           map[string]any             `json:"properties,omitempty"`
}

type sarifDefaultConfiguration struct {
	Level string `json:"level"`
}

type sarifResult struct {
	RuleID              string            `json:"ruleId"`
	RuleIndex           int               `json:"ruleIndex"`
	Level               string            `json:"level"`
	Message             sarifMessage      `json:"message"`
	Locations           []sarifLocation   `json:"locations,omitempty"`
	PartialFingerprints map[string]string `json:"partialFingerprints,omitempty"`
	Properties          map[string]any    `json:"properties,omitempty"`
}

type sarifMessage struct {
	Text string `json:"text"`
}

type sarifLocation struct {
	LogicalLocations []sarifLogicalLocation `json:"logicalLocations"`
}

type sarifLogicalLocation struct {
	FullyQualifiedName string `json:"fullyQualifiedName"`
	Kind               string `json:"kind,omitempty"`
}

// severityToLevel maps disco's four severity tiers to the four SARIF levels.
// SARIF has no "critical" — collapses to "error" alongside "high".
func severityToLevel(s string) string {
	switch strings.ToLower(s) {
	case "critical", "high":
		return "error"
	case "medium":
		return "warning"
	case "low":
		return "note"
	default:
		return "none"
	}
}

// renderCheckSARIF writes findings as a SARIF v2.1.0 document. Empty input
// still produces a valid (results: []) doc — code-scanning ingesters use
// that to clear stale findings, which is the desired post-fix behaviour.
func renderCheckSARIF(findings []policy.Finding, w io.Writer) error {
	rules, ruleIndex := buildSARIFRules(findings)
	results := make([]sarifResult, 0, len(findings))
	for _, f := range findings {
		results = append(results, findingToSARIFResult(f, ruleIndex))
	}
	doc := sarifLog{
		Schema:  "https://json.schemastore.org/sarif-2.1.0.json",
		Version: "2.1.0",
		Runs: []sarifRun{{
			Tool: sarifTool{Driver: sarifDriver{
				Name:           "disco",
				InformationURI: "https://codeberg.org/icearp/disco",
				Version:        discoVersion(),
				Rules:          rules,
			}},
			Taxonomies: buildSARIFTaxonomies(findings),
			Results:    results,
		}},
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(doc)
}

// taxonomyKeys lists the rule-tag keys that surface as SARIF taxonomies
// when present on any loaded rule. Order is the rendering order in the
// taxonomies[] block.
var taxonomyKeys = []string{"pillar", "soc2", "iso27001", "pci_dss", "nist_800_53", "waf_qid"}

// buildSARIFTaxonomies walks findings, harvests `tags.<key>` values for
// the well-known framework tag-keys, and emits one taxonomy per non-empty
// key. Empty keys are skipped. Each taxonomy's taxa list is sorted for
// deterministic output (byte-stable across runs).
func buildSARIFTaxonomies(findings []policy.Finding) []sarifTaxonomy {
	values := map[string]map[string]struct{}{}
	for _, f := range findings {
		for _, key := range taxonomyKeys {
			v, ok := f.Tags[key]
			if !ok || v == "" {
				continue
			}
			if _, exists := values[key]; !exists {
				values[key] = map[string]struct{}{}
			}
			values[key][v] = struct{}{}
		}
	}
	out := make([]sarifTaxonomy, 0, len(taxonomyKeys))
	for _, key := range taxonomyKeys {
		set, ok := values[key]
		if !ok || len(set) == 0 {
			continue
		}
		taxa := make([]sarifTaxon, 0, len(set))
		for v := range set {
			taxa = append(taxa, sarifTaxon{ID: v})
		}
		sort.Slice(taxa, func(i, j int) bool { return taxa[i].ID < taxa[j].ID })
		out = append(out, sarifTaxonomy{
			Name: key,
			Taxa: taxa,
		})
	}
	return out
}

// buildSARIFRules dedupes findings on rule ID and returns both the rule
// descriptor slice and a name→index map for populating result.ruleIndex.
// First-seen finding wins for help text / URL / category — later duplicates
// inherit the same descriptor.
func buildSARIFRules(findings []policy.Finding) ([]sarifRuleDescr, map[string]int) {
	rules := make([]sarifRuleDescr, 0)
	idx := make(map[string]int)
	for _, f := range findings {
		if _, seen := idx[f.ID]; seen {
			continue
		}
		descr := sarifRuleDescr{
			ID:                   f.ID,
			ShortDescription:     &sarifMessage{Text: f.Message},
			FullDescription:      &sarifMessage{Text: f.Message},
			DefaultConfiguration: &sarifDefaultConfiguration{Level: severityToLevel(f.Severity)},
			HelpURI:              f.RefURL,
		}
		if f.Remediation != "" {
			descr.Help = &sarifMessage{Text: f.Remediation}
		}
		props := map[string]any{}
		if f.Category != "" {
			props["category"] = f.Category
		}
		if len(f.Tags) > 0 {
			props["tags"] = flattenRuleTags(f.Tags)
		}
		if len(props) > 0 {
			descr.Properties = props
		}
		idx[f.ID] = len(rules)
		rules = append(rules, descr)
	}
	return rules, idx
}

// flattenRuleTags converts a tags map to a sorted "k:v" string slice for
// SARIF rule.properties.tags. SARIF tags must be a string array; flat
// "k:v" preserves both key and value while sorting deterministically.
func flattenRuleTags(t map[string]string) []string {
	out := make([]string, 0, len(t))
	for k, v := range t {
		out = append(out, fmt.Sprintf("%s:%s", k, v))
	}
	sort.Strings(out)
	return out
}

// findingToSARIFResult builds a single result row. Resource ID + type land
// in logicalLocations (cloud resources have no physical file path); other
// disco-specific fields ride along in result.properties so consumers that
// understand the disco schema can pivot without re-querying.
func findingToSARIFResult(f policy.Finding, ruleIndex map[string]int) sarifResult {
	res := sarifResult{
		RuleID:    f.ID,
		RuleIndex: ruleIndex[f.ID],
		Level:     severityToLevel(f.Severity),
		Message:   sarifMessage{Text: f.Message},
	}
	// partialFingerprints lets GitHub code-scanning de-dup a finding across
	// runs even if line numbers / paths shift; for cloud resources we hash
	// (rule, resource) so a finding stays one alert across re-scans.
	if f.ResourceID != "" {
		h := sha256.Sum256([]byte(f.ID + ":" + f.ResourceID))
		res.PartialFingerprints = map[string]string{
			"disco/v1": hex.EncodeToString(h[:16]),
		}
	}
	if f.ResourceID != "" {
		res.Locations = []sarifLocation{{
			LogicalLocations: []sarifLogicalLocation{{
				FullyQualifiedName: f.ResourceID,
				Kind:               f.Type,
			}},
		}}
	}
	props := map[string]any{}
	if f.Provider != "" {
		props["provider"] = f.Provider
	}
	if f.Region != "" {
		props["region"] = f.Region
	}
	if f.Category != "" {
		props["category"] = f.Category
	}
	if len(f.Tags) > 0 {
		props["tags"] = f.Tags
	}
	if len(props) > 0 {
		res.Properties = props
	}
	return res
}

// discoVersion returns the build-time version stamped into cmd.Version via
// `-X cmd.Version=<git-describe>` ldflag (Makefile owns the stamp). Falls
// through to "dev" for plain `go build .` invocations and tests.
func discoVersion() string {
	return Version
}

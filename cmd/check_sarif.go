package cmd

import (
	"encoding/json"
	"io"
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
	Tool    sarifTool     `json:"tool"`
	Results []sarifResult `json:"results"`
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
	ID               string         `json:"id"`
	Name             string         `json:"name,omitempty"`
	ShortDescription *sarifMessage  `json:"shortDescription,omitempty"`
	HelpURI          string         `json:"helpUri,omitempty"`
	Help             *sarifMessage  `json:"help,omitempty"`
	Properties       map[string]any `json:"properties,omitempty"`
}

type sarifResult struct {
	RuleID     string          `json:"ruleId"`
	RuleIndex  int             `json:"ruleIndex"`
	Level      string          `json:"level"`
	Message    sarifMessage    `json:"message"`
	Locations  []sarifLocation `json:"locations,omitempty"`
	Properties map[string]any  `json:"properties,omitempty"`
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
			Results: results,
		}},
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(doc)
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
		descr := sarifRuleDescr{ID: f.ID, HelpURI: f.RefURL}
		if f.Remediation != "" {
			descr.Help = &sarifMessage{Text: f.Remediation}
		}
		if f.Category != "" {
			descr.Properties = map[string]any{"category": f.Category}
		}
		idx[f.ID] = len(rules)
		rules = append(rules, descr)
	}
	return rules, idx
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

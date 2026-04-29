// Package rules defines the disco rule-engine schema and loader.
//
// Rules are simple YAML documents declaring a named match predicate over the
// resources table. Evaluation lives in eval.go; this file only handles schema,
// validation, and file/dir loading.
package rules

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"

	"gopkg.in/yaml.v3"
)

// CurrentSchemaVersion is the only rules-file version this build accepts.
// Bump when the schema changes in a backwards-incompatible way; loaders for
// older versions should branch on the `version` field.
const CurrentSchemaVersion = 1

// Severity is the rule severity level.
type Severity string

const (
	SevLow      Severity = "low"
	SevMedium   Severity = "medium"
	SevHigh     Severity = "high"
	SevCritical Severity = "critical"
)

// severityRank orders severities so callers can apply a minimum threshold.
var severityRank = map[Severity]int{
	SevLow: 1, SevMedium: 2, SevHigh: 3, SevCritical: 4,
}

// AtLeast reports whether s meets or exceeds min.
func (s Severity) AtLeast(min Severity) bool {
	return severityRank[s] >= severityRank[min]
}

// ParseSeverity validates s and returns it in canonical form.
func ParseSeverity(s string) (Severity, error) {
	sv := Severity(s)
	if _, ok := severityRank[sv]; !ok {
		return "", fmt.Errorf("invalid severity %q (want low|medium|high|critical)", s)
	}
	return sv, nil
}

// Rule is one named check.
type Rule struct {
	ID          string   `yaml:"id"`
	Description string   `yaml:"description"`
	Severity    Severity `yaml:"severity"`
	// Tags maps a tag namespace to one or more values, e.g.
	// {cis-aws: ["5.3"], nist-800-53: ["AC-3"], pci: ["1.2"]}. Free-form keys
	// — engine treats values as opaque metadata + propagates onto Finding.
	// Enables compliance-pack mapping and `disco check --tag k=v` filtering.
	Tags map[string][]string `yaml:"tags,omitempty"`
	// Category is a coarse grouping (e.g. "Networking", "IAM", "Encryption")
	// used for human-facing report grouping.
	Category string `yaml:"category,omitempty"`
	// Remediation describes how to address the finding. Plain text, surfaced
	// on Finding for downstream consumers.
	Remediation string `yaml:"remediation,omitempty"`
	// RefURL points at upstream guidance (CIS control page, NIST control,
	// vendor doc).
	RefURL string `yaml:"ref_url,omitempty"`
	Match  Match  `yaml:"match"`
	// Source tracks where the rule was loaded from (file path or "<builtin>").
	Source string `yaml:"-"`
}

// Match is the predicate set applied to each resource.
type Match struct {
	Provider string      `yaml:"provider"`
	Type     string      `yaml:"type"`
	Region   string      `yaml:"region"`
	Where    []Predicate `yaml:"where"`
	// Related (optional) requires the resource to have at least one edge
	// satisfying the inner Match. Single-hop traversal only — depth>1 is a
	// future extension. The inner Match's Where predicates evaluate against
	// the TARGET resource's attributes JSON, not the parent's.
	Related *RelatedMatch `yaml:"related,omitempty"`
}

// RelatedMatch describes the edge-traversal predicate. Direction "out" walks
// outbound edges (RelationshipsFrom); "in" walks inbound (RelationshipsTo).
// Empty/missing direction defaults to "out". Kinds (optional) restricts the
// edge kind filter (e.g. ["uses","attached-to"]); empty means any kind.
type RelatedMatch struct {
	Direction string   `yaml:"direction"`
	Kinds     []string `yaml:"kinds"`
	Target    Match    `yaml:"target"`
}

// Predicate is a single path/op/value triple evaluated against a resource's
// attributes JSON. `Regex` is populated at load time for op=matches.
type Predicate struct {
	Path  string `yaml:"path"`
	Op    string `yaml:"op"`
	Value any    `yaml:"value"`

	Regex *regexp.Regexp `yaml:"-"`
}

// validOps lists accepted predicate operators.
var validOps = map[string]bool{
	"eq": true, "ne": true,
	"exists": true, "not_exists": true,
	"contains": true, "matches": true,
}

// file is the on-disk shape of a rules YAML document.
type file struct {
	Version int    `yaml:"version"`
	Rules   []Rule `yaml:"rules"`
}

// Load reads every path (file or directory) and returns the merged rule set.
// Directories are walked recursively for *.yaml and *.yml files.
// Duplicate rule IDs across sources produce an error.
func Load(paths ...string) ([]Rule, error) {
	var out []Rule
	seen := map[string]string{} // id -> source

	for _, p := range paths {
		files, err := expandPath(p)
		if err != nil {
			return nil, err
		}
		for _, fp := range files {
			rules, err := loadFile(fp)
			if err != nil {
				return nil, err
			}
			for _, r := range rules {
				if prev, dup := seen[r.ID]; dup {
					return nil, fmt.Errorf("duplicate rule id %q (%s and %s)", r.ID, prev, r.Source)
				}
				seen[r.ID] = r.Source
				out = append(out, r)
			}
		}
	}
	return out, nil
}

// expandPath resolves a single --rules argument to a list of YAML files.
// If the path is a directory, every *.yaml/*.yml under it (recursive) is
// returned; otherwise the single file is returned.
func expandPath(p string) ([]string, error) {
	info, err := os.Stat(p)
	if err != nil {
		return nil, fmt.Errorf("stat %s: %w", p, err)
	}
	if !info.IsDir() {
		return []string{p}, nil
	}
	var files []string
	err = filepath.WalkDir(p, func(path string, d fs.DirEntry, werr error) error {
		if werr != nil {
			return werr
		}
		if d.IsDir() {
			return nil
		}
		ext := filepath.Ext(path)
		if ext == ".yaml" || ext == ".yml" {
			files = append(files, path)
		}
		return nil
	})
	return files, err
}

func loadFile(path string) ([]Rule, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	return parseRules(b, path)
}

// parseRules decodes a YAML document, validates it, and tags each rule with
// its source. Exported via loadFile + embedded builtin reader.
func parseRules(data []byte, source string) ([]Rule, error) {
	var f file
	if err := yaml.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("parse %s: %w", source, err)
	}
	if f.Version != CurrentSchemaVersion {
		return nil, fmt.Errorf("%s: unsupported schema version %d (want %d)",
			source, f.Version, CurrentSchemaVersion)
	}
	for i := range f.Rules {
		r := &f.Rules[i]
		r.Source = source
		if err := validateRule(r); err != nil {
			return nil, fmt.Errorf("%s: %w", source, err)
		}
	}
	return f.Rules, nil
}

func validateRule(r *Rule) error {
	if r.ID == "" {
		return fmt.Errorf("rule missing id")
	}
	if _, err := ParseSeverity(string(r.Severity)); err != nil {
		return fmt.Errorf("rule %s: %w", r.ID, err)
	}
	if err := validateMatch(r.ID, "", &r.Match); err != nil {
		return err
	}
	return nil
}

// validateMatch recursively validates a Match (predicates + nested Related
// target). Used by validateRule for the top-level Match and by itself for
// the inner RelatedMatch.Target.
func validateMatch(ruleID, scope string, m *Match) error {
	for i := range m.Where {
		p := &m.Where[i]
		if p.Path == "" {
			return fmt.Errorf("rule %s: %swhere[%d] missing path", ruleID, scope, i)
		}
		if !validOps[p.Op] {
			return fmt.Errorf("rule %s: %swhere[%d] unknown op %q", ruleID, scope, i, p.Op)
		}
		if p.Op == "matches" {
			s, ok := p.Value.(string)
			if !ok {
				return fmt.Errorf("rule %s: %swhere[%d] op=matches requires string value", ruleID, scope, i)
			}
			re, err := regexp.Compile(s)
			if err != nil {
				return fmt.Errorf("rule %s: %swhere[%d] bad regex: %w", ruleID, scope, i, err)
			}
			p.Regex = re
		}
	}
	if m.Related != nil {
		dir := m.Related.Direction
		if dir != "" && dir != "in" && dir != "out" {
			return fmt.Errorf("rule %s: related.direction must be 'in' or 'out' (got %q)", ruleID, dir)
		}
		if err := validateMatch(ruleID, "related.target.", &m.Related.Target); err != nil {
			return err
		}
	}
	return nil
}
